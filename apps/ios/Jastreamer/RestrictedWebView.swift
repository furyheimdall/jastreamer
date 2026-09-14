import Foundation
import SwiftUI
import UIKit
import WebKit

enum RemoteWebFailure: Equatable {
    case contentRules
    case cookies
    case navigation
    case download
    case tls
    case authentication
    case unreachable
    case timeout
    case response(Int)
    case load
    case processTerminated
    case suspended

    var localizationKey: String {
        switch self {
        case .contentRules: "web.error.rules"
        case .cookies: "web.error.cookies"
        case .navigation: "web.error.navigation"
        case .download: "web.error.download"
        case .tls: "web.error.tls"
        case .authentication: "web.error.authentication"
        case .unreachable: "web.error.unreachable"
        case .timeout: "web.error.timeout"
        case .response: "web.error.response"
        case .load: "web.error.load"
        case .processTerminated: "web.error.process"
        case .suspended: "web.error.suspended"
        }
    }
}

@MainActor
final class RestrictedWebController: NSObject, ObservableObject {
    @Published private(set) var isLoading = true
    @Published private(set) var canGoBack = false
    @Published private(set) var failure: RemoteWebFailure?

    private enum Phase {
        case configuring
        case idle
        case writingCookie
        case loading
        case loaded
        case failed
        case disposed
    }

    private let server: ServerEndpoint
    private let rootURL: URL
    private let dataStore: WKWebsiteDataStore
    private let userContentController = WKUserContentController()

    private var webView: WKWebView?
    private var activeNavigation: WKNavigation?
    private var phase: Phase = .configuring {
        didSet { NSLog("iOS Web phase=%@", String(describing: phase)) }
    }
    private var generation: UInt64 = 0
    private var active = true
    private var setupComplete = false
    private var language: String
    private var desiredLanguage: String
    private var pendingLanguage: String?
    private var cookieWriteInFlight = false
    private var timeoutWorkItem: DispatchWorkItem?
    private var onLanguageChanged: ((String) -> Void)?

    init(server: ServerEndpoint, language: String, dataStore: WKWebsiteDataStore) {
        precondition(dataStore.identifier == server.profileID)
        self.server = server
        rootURL = URL(string: server.origin + "/")!
        self.dataStore = dataStore
        self.language = language == "ko" ? "ko" : "en"
        desiredLanguage = language == "ko" ? "ko" : "en"
        super.init()
    }

    func mount(in hostView: UIView, active: Bool, onLanguageChanged: @escaping (String) -> Void) {
        guard webView == nil, phase != .disposed else { return }
        self.active = active
        self.onLanguageChanged = onLanguageChanged

        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = dataStore
        configuration.userContentController = userContentController
        configuration.defaultWebpagePreferences.allowsContentJavaScript = true
        configuration.preferences.javaScriptCanOpenWindowsAutomatically = false
        configuration.allowsAirPlayForMediaPlayback = false
        configuration.allowsPictureInPictureMediaPlayback = false
        configuration.allowsInlineMediaPlayback = false
        configuration.mediaTypesRequiringUserActionForPlayback = .all
        configuration.dataDetectorTypes = []

        let candidate = WKWebView(frame: .zero, configuration: configuration)
        candidate.translatesAutoresizingMaskIntoConstraints = false
        candidate.navigationDelegate = self
        candidate.uiDelegate = self
        candidate.allowsBackForwardNavigationGestures = true
        candidate.allowsLinkPreview = false
        candidate.isInspectable = false
        candidate.scrollView.keyboardDismissMode = .interactive
        candidate.isHidden = !active
        candidate.isUserInteractionEnabled = active
        candidate.accessibilityElementsHidden = !active
        candidate.accessibilityIdentifier = "web-control"
        candidate.accessibilityLabel = "JASTREAMER Web Control"
        hostView.addSubview(candidate)
        NSLayoutConstraint.activate([
            candidate.leadingAnchor.constraint(equalTo: hostView.leadingAnchor),
            candidate.trailingAnchor.constraint(equalTo: hostView.trailingAnchor),
            candidate.topAnchor.constraint(equalTo: hostView.topAnchor),
            candidate.bottomAnchor.constraint(equalTo: hostView.bottomAnchor)
        ])
        webView = candidate
        dataStore.httpCookieStore.add(self)
        generation &+= 1
        let token = generation
        if active {
            scheduleTimeout(token: token)
        }
        NSLog("iOS Web mounted; active=%@", active ? "yes" : "no")
        compileBoundaryRules(token: token)
    }

    func update(active: Bool, language: String, onLanguageChanged: @escaping (String) -> Void) {
        guard phase != .disposed else { return }
        self.onLanguageChanged = onLanguageChanged
        let normalized = language == "ko" ? "ko" : "en"
        if normalized != desiredLanguage {
            desiredLanguage = normalized
            pendingLanguage = normalized
        }
        setActive(active)
        if setupComplete, active, (normalized != self.language || pendingLanguage != nil) {
            requestLanguageCookieAndLoad(normalized)
        }
    }

    func goBack() {
        guard active, phase != .disposed, let webView, webView.canGoBack else { return }
        beginLoad()
        activeNavigation = webView.goBack()
    }

    func reload() {
        guard active, setupComplete, phase != .disposed, phase != .failed else { return }
        failure = nil
        requestLanguageCookieAndLoad(desiredLanguage)
    }

    func dispose() {
        guard phase != .disposed else { return }
        generation &+= 1
        cancelTimeout()
        phase = .disposed
        pendingLanguage = nil
        cookieWriteInFlight = false
        activeNavigation = nil
        setupComplete = false
        dataStore.httpCookieStore.remove(self)
        onLanguageChanged = nil
        if let webView {
            webView.stopLoading()
            webView.navigationDelegate = nil
            webView.uiDelegate = nil
            webView.removeFromSuperview()
        }
        webView = nil
    }

    private func setActive(_ value: Bool) {
        guard active != value, phase != .disposed else { return }
        active = value
        NSLog("iOS Web active=%@", value ? "yes" : "no")
        webView?.isHidden = !value
        webView?.isUserInteractionEnabled = value
        webView?.accessibilityElementsHidden = !value
        if !value {
            webView?.endEditing(true)
            cancelTimeout()
            activeNavigation = nil
            switch phase {
            case .configuring:
                break
            case .writingCookie, .loading:
                generation &+= 1
                pendingLanguage = nil
                cookieWriteInFlight = false
                webView?.stopLoading()
                phase = .failed
                failure = .suspended
                isLoading = false
            case .loaded:
                generation &+= 1
            case .idle, .failed, .disposed:
                break
            }
        } else {
            switch phase {
            case .configuring:
                scheduleTimeout(token: generation)
            case .idle:
                if failure == nil {
                    requestLanguageCookieAndLoad(desiredLanguage)
                }
            case .loaded:
                if desiredLanguage != language || pendingLanguage != nil {
                    requestLanguageCookieAndLoad(desiredLanguage)
                }
            case .writingCookie, .loading, .failed, .disposed:
                break
            }
        }
    }

    private func compileBoundaryRules(token: UInt64) {
        let escapedOrigin = NSRegularExpression.escapedPattern(for: server.origin)
        let rules: [[String: Any]] = [
            [
                // Block every hierarchical network/file scheme first. data: and
                // blob: URLs do not contain // after their own scheme and remain
                // available for in-page images and generated content.
                "trigger": ["url-filter": "^[a-z][a-z0-9+.-]*://.*"],
                "action": ["type": "block"]
            ],
            [
                "trigger": ["url-filter": "^\(escapedOrigin)/"],
                "action": ["type": "ignore-previous-rules"]
            ],
            [
                "trigger": ["url-filter": ".*", "resource-type": ["media"]],
                "action": ["type": "block"]
            ]
        ]
        guard
            JSONSerialization.isValidJSONObject(rules),
            let data = try? JSONSerialization.data(withJSONObject: rules),
            let encoded = String(data: data, encoding: .utf8)
        else {
            fail(.contentRules)
            return
        }

        let identifier = "io.jastreamer.origin.\(server.profileID.uuidString.lowercased())"
        WKContentRuleListStore.default().compileContentRuleList(
            forIdentifier: identifier,
            encodedContentRuleList: encoded
        ) { [weak self] list, error in
            DispatchQueue.main.async {
                guard let self, self.phase == .configuring, self.generation == token else { return }
                guard error == nil, let list else {
                    self.fail(.contentRules)
                    return
                }
                self.cancelTimeout()
                self.userContentController.add(list)
                self.setupComplete = true
                self.phase = .idle
                if self.active {
                    self.requestLanguageCookieAndLoad(self.desiredLanguage)
                } else {
                    self.isLoading = false
                }
            }
        }
    }

    private func requestLanguageCookieAndLoad(_ value: String) {
        desiredLanguage = value
        pendingLanguage = value
        guard active, setupComplete, phase != .disposed, phase != .failed else { return }
        startPendingCookieWrite()
    }

    private func startPendingCookieWrite() {
        guard active, setupComplete, !cookieWriteInFlight,
              phase != .disposed, phase != .failed,
              let value = pendingLanguage
        else { return }

        pendingLanguage = nil
        cookieWriteInFlight = true
        generation &+= 1
        let token = generation
        cancelTimeout()
        activeNavigation = nil
        webView?.stopLoading()
        phase = .writingCookie
        failure = nil
        isLoading = true
        scheduleTimeout(token: token)

        dataStore.httpCookieStore.getAllCookies { [weak self] cookies in
            DispatchQueue.main.async {
                guard let self, self.isCurrent(token), self.phase == .writingCookie else { return }
                NSLog("iOS Web cookies read")
                let existing = cookies.filter { self.isLanguageCookie($0) }
                self.deleteCookies(existing, token: token, value: value)
            }
        }
    }

    private func deleteCookies(_ cookies: [HTTPCookie], token: UInt64, value: String) {
        guard !cookies.isEmpty else {
            setLanguageCookie(token: token, value: value)
            return
        }
        let group = DispatchGroup()
        for cookie in cookies {
            group.enter()
            dataStore.httpCookieStore.delete(cookie) {
                group.leave()
            }
        }
        group.notify(queue: .main) { [weak self] in
            guard let self, self.isCurrent(token), self.phase == .writingCookie else { return }
            self.setLanguageCookie(token: token, value: value)
        }
    }

    private func setLanguageCookie(token: UInt64, value: String) {
        let secure = rootURL.scheme == "https" ? "; Secure" : ""
        let header = "jastreamer_language=\(value); Path=/; Max-Age=31536000; SameSite=Strict\(secure)"
        guard let cookie = HTTPCookie.cookies(
            withResponseHeaderFields: ["Set-Cookie": header],
            for: rootURL
        ).first else {
            fail(.cookies, token: token)
            return
        }

        dataStore.httpCookieStore.setCookie(cookie) { [weak self] in
            DispatchQueue.main.async {
                guard let self, self.isCurrent(token), self.phase == .writingCookie else { return }
                NSLog("iOS Web language cookie stored")
                self.verifyLanguageCookie(token: token, value: value)
            }
        }
    }

    private func verifyLanguageCookie(token: UInt64, value: String) {
        dataStore.httpCookieStore.getAllCookies { [weak self] cookies in
            DispatchQueue.main.async {
                guard let self, self.isCurrent(token), self.phase == .writingCookie else { return }
                NSLog("iOS Web language cookie readback")
                guard cookies.contains(where: { self.isLanguageCookie($0) && $0.value == value }) else {
                    self.fail(.cookies, token: token)
                    return
                }

                self.language = value
                self.cookieWriteInFlight = false
                if self.desiredLanguage != value {
                    self.pendingLanguage = self.desiredLanguage
                    self.phase = .idle
                    self.startPendingCookieWrite()
                } else {
                    self.pendingLanguage = nil
                    self.loadRoot(token: token)
                }
            }
        }
    }

    private func loadRoot(token: UInt64) {
        guard isCurrent(token), let webView else { return }
        phase = .loading
        scheduleTimeout(token: token)
        var request = URLRequest(url: rootURL, cachePolicy: .useProtocolCachePolicy, timeoutInterval: 30)
        request.httpMethod = "GET"
        activeNavigation = webView.load(request)
    }

    private func beginLoad() {
        generation &+= 1
        activeNavigation = nil
        phase = .loading
        failure = nil
        isLoading = true
        scheduleTimeout(token: generation)
    }

    private func completeLoad() {
        guard active, phase == .loading else { return }
        cancelTimeout()
        phase = .loaded
        failure = nil
        isLoading = false
        canGoBack = webView?.canGoBack == true
        observeLanguageCookie()
    }

    private func scheduleTimeout(token: UInt64) {
        cancelTimeout()
        let item = DispatchWorkItem { [weak self] in
            guard let self, self.isCurrent(token) else { return }
            switch self.phase {
            case .configuring, .writingCookie, .loading:
                self.fail(.timeout, token: token)
            case .idle, .loaded, .failed, .disposed:
                break
            }
        }
        timeoutWorkItem = item
        DispatchQueue.main.asyncAfter(deadline: .now() + 30, execute: item)
    }

    private func cancelTimeout() {
        timeoutWorkItem?.cancel()
        timeoutWorkItem = nil
    }

    private func fail(_ failure: RemoteWebFailure, token: UInt64? = nil) {
        if let token, generation != token { return }
        guard phase != .disposed else { return }
        generation &+= 1
        cancelTimeout()
        pendingLanguage = nil
        cookieWriteInFlight = false
        activeNavigation = nil
        webView?.stopLoading()
        phase = .failed
        self.failure = failure
        isLoading = false
        canGoBack = webView?.canGoBack == true
    }

    private func isCurrent(_ token: UInt64) -> Bool {
        active && phase != .disposed && generation == token
    }

    private func isLanguageCookie(_ cookie: HTTPCookie) -> Bool {
        guard cookie.name == "jastreamer_language", cookie.path == "/", let host = rootURL.host else {
            return false
        }
        return cookie.domain.trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .caseInsensitiveCompare(host) == .orderedSame
    }

    private func observeLanguageCookie() {
        guard active, phase != .disposed else { return }
        let token = generation
        dataStore.httpCookieStore.getAllCookies { [weak self] cookies in
            DispatchQueue.main.async {
                guard let self, self.isCurrent(token) else { return }
                guard let value = cookies.last(where: self.isLanguageCookie)?.value,
                      value == "en" || value == "ko",
                      value != self.language
                else { return }
                self.language = value
                self.desiredLanguage = value
                self.onLanguageChanged?(value)
            }
        }
    }

    private func classify(_ error: Error) -> RemoteWebFailure {
        let code = (error as NSError).code
        switch code {
        case NSURLErrorTimedOut:
            return .timeout
        case NSURLErrorServerCertificateUntrusted,
             NSURLErrorServerCertificateHasBadDate,
             NSURLErrorServerCertificateHasUnknownRoot,
             NSURLErrorServerCertificateNotYetValid,
             NSURLErrorClientCertificateRejected,
             NSURLErrorClientCertificateRequired:
            return .tls
        case NSURLErrorCannotFindHost,
             NSURLErrorCannotConnectToHost,
             NSURLErrorNetworkConnectionLost,
             NSURLErrorNotConnectedToInternet,
             NSURLErrorDNSLookupFailed:
            return .unreachable
        default:
            return .load
        }
    }
}

extension RestrictedWebController: WKHTTPCookieStoreObserver {
    func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
        guard cookieStore === dataStore.httpCookieStore, phase == .loaded else { return }
        observeLanguageCookie()
    }
}

extension RestrictedWebController: WKNavigationDelegate {
    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard active, self.webView === webView, phase != .disposed else {
            decisionHandler(.cancel)
            return
        }
        guard navigationAction.targetFrame != nil else {
            decisionHandler(.cancel)
            fail(.navigation)
            return
        }
        guard let url = navigationAction.request.url,
              EndpointPolicy.sameOrigin(url, origin: server.origin)
        else {
            decisionHandler(.cancel)
            fail(navigationAction.shouldPerformDownload ? .download : .navigation)
            return
        }
        guard !navigationAction.shouldPerformDownload else {
            decisionHandler(.cancel)
            fail(.download)
            return
        }
        if phase == .loaded || phase == .failed || phase == .idle {
            beginLoad()
        }
        decisionHandler(.allow)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void
    ) {
        guard active, self.webView === webView, phase != .disposed,
              let url = navigationResponse.response.url,
              EndpointPolicy.sameOrigin(url, origin: server.origin)
        else {
            decisionHandler(.cancel)
            fail(.navigation)
            return
        }

        if let response = navigationResponse.response as? HTTPURLResponse {
            if !(200 ... 399).contains(response.statusCode) {
                decisionHandler(.cancel)
                fail(.response(response.statusCode))
                return
            }
            if response.value(forHTTPHeaderField: "Content-Disposition")?
                .lowercased().contains("attachment") == true {
                decisionHandler(.cancel)
                fail(.download)
                return
            }
        }
        guard navigationResponse.canShowMIMEType else {
            decisionHandler(.cancel)
            fail(.download)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        guard active, self.webView === webView, phase == .loading else { return }
        activeNavigation = navigation
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard self.webView === webView, activeNavigation === navigation else { return }
        completeLoad()
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        guard self.webView === webView, phase == .loading, activeNavigation === navigation else { return }
        fail(classify(error))
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        guard self.webView === webView, phase == .loading, activeNavigation === navigation else { return }
        fail(classify(error))
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        guard self.webView === webView else { return }
        webView.stopLoading()
        webView.navigationDelegate = nil
        webView.uiDelegate = nil
        webView.removeFromSuperview()
        self.webView = nil
        activeNavigation = nil
        setupComplete = false
        fail(.processTerminated)
    }

    func webView(
        _ webView: WKWebView,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        if challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust {
            completionHandler(.performDefaultHandling, nil)
        } else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            fail(.authentication)
        }
    }
}

extension RestrictedWebController: WKUIDelegate {
    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        fail(.navigation)
        return nil
    }

    // iOS 18.4 exposes public denial callbacks for capture, motion and file
    // selection, but not for Web geolocation. The app deliberately has no
    // location usage description or native location code, and the compatible
    // Server's Permissions-Policy disables geolocation. Do not replace that
    // boundary with JavaScript injection or a private WebKit selector.

    func webView(
        _ webView: WKWebView,
        requestMediaCapturePermissionFor origin: WKSecurityOrigin,
        initiatedByFrame frame: WKFrameInfo,
        type: WKMediaCaptureType,
        decisionHandler: @escaping (WKPermissionDecision) -> Void
    ) {
        decisionHandler(.deny)
    }

    func webView(
        _ webView: WKWebView,
        requestDeviceOrientationAndMotionPermissionFor origin: WKSecurityOrigin,
        initiatedByFrame frame: WKFrameInfo,
        decisionHandler: @escaping (WKPermissionDecision) -> Void
    ) {
        decisionHandler(.deny)
    }

    func webView(
        _ webView: WKWebView,
        runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping ([URL]?) -> Void
    ) {
        completionHandler(nil)
    }
}

struct RestrictedWebSurface: UIViewRepresentable {
    @ObservedObject var controller: RestrictedWebController
    let active: Bool
    let language: String
    let onLanguageChanged: (String) -> Void

    final class Coordinator {
        let controller: RestrictedWebController

        init(controller: RestrictedWebController) {
            self.controller = controller
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(controller: controller)
    }

    func makeUIView(context: Context) -> UIView {
        let host = UIView()
        host.backgroundColor = .systemBackground
        controller.mount(in: host, active: active, onLanguageChanged: onLanguageChanged)
        return host
    }

    func updateUIView(_ uiView: UIView, context: Context) {
        controller.update(active: active, language: language, onLanguageChanged: onLanguageChanged)
    }

    static func dismantleUIView(_ uiView: UIView, coordinator: Coordinator) {
        coordinator.controller.dispose()
    }
}
