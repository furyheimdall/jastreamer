package io.jastreamer.android

import android.content.Context
import android.graphics.Bitmap
import android.net.http.SslError
import android.os.Handler
import android.os.Looper
import android.os.Message
import android.view.ViewGroup
import android.webkit.ClientCertRequest
import android.webkit.CookieManager
import android.webkit.GeolocationPermissions
import android.webkit.HttpAuthHandler
import android.webkit.PermissionRequest
import android.webkit.RenderProcessGoneDetail
import android.webkit.SafeBrowsingResponse
import android.webkit.ServiceWorkerClient
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.annotation.MainThread
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import java.io.ByteArrayInputStream

@MainThread
class RemoteServerView(
    context: Context,
    private val server: ServerEndpoint,
    language: String,
    private val canStartNativePlayback: () -> Boolean,
    private val onLoaded: () -> Unit,
    private val onError: (ClientException) -> Unit,
    private val onLanguageChanged: (String) -> Unit,
) : FrameLayout(context) {
    private enum class LoadPhase { IDLE, PREPARING, LOADING, LOADED }

    private val mainHandler = Handler(Looper.getMainLooper())
    private val rootUrl = "${server.origin}/"
    private lateinit var webView: WebView
    private lateinit var cookieManager: CookieManager
    private var nativeBridge: NativeAudioBridge? = null
    private var language = requireLanguage(language)
    private var generation = 0L
    private var activeGeneration: Long? = null
    private var timeout: Runnable? = null
    private var pendingLanguage: String? = null
    private var cookieWriteInFlight = false
    private var phase = LoadPhase.IDLE
    private var pausing = false
    @Volatile private var disposed = false
    @Volatile private var unusable = false
    private var lastBlockedUrl: String? = null

    init {
        requireMainThread()
        initializeWebView()
    }

    @MainThread
    fun load() {
        requireMainThread()
        if (disposed || unusable) return
        prepareLoad(language)
    }

    @MainThread
    fun retryLoad() {
        requireMainThread()
        if (
            !disposed &&
            !unusable &&
            (phase == LoadPhase.IDLE || phase == LoadPhase.LOADED)
        ) {
            prepareLoad(language)
        }
    }

    @MainThread
    fun resume() {
        requireMainThread()
        if (!disposed && !unusable) {
            webView.onResume()
            nativeBridge?.setHostInteractive(true)
        }
    }

    @MainThread
    fun pause() {
        requireMainThread()
        if (disposed) return
        nativeBridge?.setHostInteractive(false)
        if (unusable || pausing) return
        pausing = true
        try {
            observeLanguageCookie()
            if (!disposed && !unusable) {
                webView.onPause()
                cookieManager.flush()
            }
        } catch (failure: RuntimeException) {
            onError(ClientException(ClientErrorCode.STORAGE, "Unable to persist WebView cookies", failure))
        } finally {
            pausing = false
        }
    }

    @MainThread
    fun canGoBack(): Boolean {
        requireMainThread()
        return !disposed && !unusable && webView.canGoBack()
    }

    @MainThread
    fun goBack() {
        requireMainThread()
        if (!disposed && !unusable && webView.canGoBack()) webView.goBack()
    }

    @MainThread
    fun setLanguage(language: String) {
        requireMainThread()
        if (disposed || unusable) return
        if (!isLanguage(language)) {
            onError(ClientException(ClientErrorCode.STORAGE, "Unsupported language: $language"))
            return
        }
        prepareLoad(language)
    }

    @MainThread
    fun dispose() {
        requireMainThread()
        if (disposed) return
        disposed = true
        nativeBridge?.dispose()
        nativeBridge = null

        if (!unusable) {
            observeLanguageCookie()
            try {
                cookieManager.flush()
            } catch (failure: RuntimeException) {
                onError(ClientException(ClientErrorCode.STORAGE, "Unable to persist WebView cookies", failure))
            }
        }

        generation++
        activeGeneration = null
        pendingLanguage = null
        phase = LoadPhase.IDLE
        cancelTimeout()
        if (!unusable) {
            try {
                WebViewCompat.removeWebMessageListener(webView, NativeAudioBridge.OBJECT_NAME)
            } catch (_: RuntimeException) {
                // The WebView is being destroyed and no native capability remains reachable.
            }
            webView.stopLoading()
            webView.setDownloadListener(null)
            removeView(webView)
            webView.destroy()
        }
        removeAllViews()
    }

    private fun initializeWebView() {
        val profileName = EndpointPolicy.profileName(server)
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)) {
            throw ClientException(
                ClientErrorCode.WEBVIEW_UNSUPPORTED,
                "The installed Android System WebView does not support isolated profiles",
            )
        }
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            throw ClientException(
                ClientErrorCode.WEBVIEW_UNSUPPORTED,
                "The installed Android System WebView does not support restricted native messages",
            )
        }

        val profile = try {
            ProfileStore.getInstance().getOrCreateProfile(profileName)
        } catch (failure: RuntimeException) {
            throw ClientException(
                ClientErrorCode.WEBVIEW_UNSUPPORTED,
                "Unable to create an isolated WebView profile",
                failure,
            )
        }
        val candidate = try {
            WebView(context)
        } catch (failure: RuntimeException) {
            throw ClientException(ClientErrorCode.WEBVIEW_UNSUPPORTED, "Unable to create WebView", failure)
        }

        try {
            // Profile selection must be the first operation on the new WebView.
            WebViewCompat.setProfile(candidate, profileName)
            cookieManager = profile.cookieManager
            cookieManager.setAcceptCookie(true)
            cookieManager.setAcceptThirdPartyCookies(candidate, false)

            candidate.settings.apply {
                userAgentString = "$userAgentString JaStreamerAndroid/${BuildConfig.VERSION_NAME}"
                javaScriptEnabled = true
                domStorageEnabled = true
                databaseEnabled = false
                allowFileAccess = false
                allowContentAccess = false
                allowFileAccessFromFileURLs = false
                allowUniversalAccessFromFileURLs = false
                mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
                javaScriptCanOpenWindowsAutomatically = false
                setSupportMultipleWindows(true)
                setGeolocationEnabled(false)
                mediaPlaybackRequiresUserGesture = true
                saveFormData = false
                cacheMode = WebSettings.LOAD_DEFAULT
                useWideViewPort = true
                loadWithOverviewMode = true
                builtInZoomControls = false
                displayZoomControls = false
                setSupportZoom(false)
                safeBrowsingEnabled = true
            }

            val allowedOrigin = server.origin
            profile.serviceWorkerController.serviceWorkerWebSettings.apply {
                allowContentAccess = false
                allowFileAccess = false
                blockNetworkLoads = false
                cacheMode = WebSettings.LOAD_DEFAULT
            }
            profile.serviceWorkerController.setServiceWorkerClient(object : ServiceWorkerClient() {
                override fun shouldInterceptRequest(request: WebResourceRequest): WebResourceResponse? =
                    if (isAllowed(request.url.toString(), allowedOrigin)) null else blockedResponse()
            })

            val bridge = NativeAudioBridge(candidate, server) {
                !disposed &&
                    !unusable &&
                    isShown &&
                    windowVisibility == VISIBLE &&
                    canStartNativePlayback()
            }
            WebViewCompat.addWebMessageListener(
                candidate,
                NativeAudioBridge.OBJECT_NAME,
                setOf(server.origin),
                bridge,
            )
            nativeBridge = bridge

            candidate.webViewClient = RestrictedWebViewClient()
            candidate.webChromeClient = RestrictedWebChromeClient()
            candidate.setDownloadListener { url, _, _, _, _ ->
                if (!disposed && !unusable) {
                    onError(
                        ClientException(
                            ClientErrorCode.NAVIGATION_BLOCKED,
                            "Downloads are not permitted: $url",
                        ),
                    )
                }
            }
            webView = candidate
            addView(candidate, LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        } catch (failure: RuntimeException) {
            nativeBridge?.dispose()
            nativeBridge = null
            candidate.destroy()
            throw ClientException(
                ClientErrorCode.WEBVIEW_UNSUPPORTED,
                "Unable to configure an isolated WebView profile",
                failure,
            )
        }
    }

    private fun prepareLoad(nextLanguage: String) {
        nativeBridge?.invalidateDocument()
        val token = ++generation
        activeGeneration = token
        pendingLanguage = nextLanguage
        phase = LoadPhase.PREPARING
        lastBlockedUrl = null
        cancelTimeout()
        webView.stopLoading()
        scheduleTimeout(token)
        if (!cookieWriteInFlight) writeLanguageCookie(token, nextLanguage)
    }

    private fun writeLanguageCookie(token: Long, nextLanguage: String) {
        cookieWriteInFlight = true
        val cookie = buildString {
            append(LANGUAGE_COOKIE_NAME)
            append('=')
            append(nextLanguage)
            append("; Path=/; Max-Age=")
            append(LANGUAGE_COOKIE_LIFETIME_SECONDS)
            append("; SameSite=Strict")
            if (server.origin.startsWith("https://")) append("; Secure")
        }
        try {
            cookieManager.setCookie(rootUrl, cookie) { accepted ->
                cookieWriteInFlight = false
                if (
                    !isCurrent(token) ||
                    phase != LoadPhase.PREPARING ||
                    pendingLanguage != nextLanguage
                ) {
                    continuePendingLanguageWrite()
                    return@setCookie
                }
                if (accepted != true) {
                    fail(token, ClientException(ClientErrorCode.STORAGE, "Unable to persist language cookie"))
                    return@setCookie
                }
                try {
                    cookieManager.flush()
                    if (readLanguageCookie() != nextLanguage) {
                        fail(token, ClientException(ClientErrorCode.STORAGE, "Unable to verify language cookie"))
                        return@setCookie
                    }
                } catch (failure: RuntimeException) {
                    fail(
                        token,
                        ClientException(ClientErrorCode.STORAGE, "Unable to persist language cookie", failure),
                    )
                    return@setCookie
                }
                pendingLanguage = null
                language = nextLanguage
                phase = LoadPhase.LOADING
                try {
                    webView.loadUrl(rootUrl)
                } catch (failure: RuntimeException) {
                    fail(token, ClientException(ClientErrorCode.WEB_LOAD, "Unable to load $rootUrl", failure))
                }
            }
        } catch (failure: RuntimeException) {
            cookieWriteInFlight = false
            if (isCurrent(token)) {
                fail(token, ClientException(ClientErrorCode.STORAGE, "Unable to write language cookie", failure))
            } else {
                continuePendingLanguageWrite()
            }
        }
    }

    private fun continuePendingLanguageWrite() {
        if (disposed || unusable || cookieWriteInFlight || phase != LoadPhase.PREPARING) return
        val token = activeGeneration ?: return
        val nextLanguage = pendingLanguage ?: return
        writeLanguageCookie(token, nextLanguage)
    }

    private fun scheduleTimeout(token: Long) {
        val task = Runnable {
            if (isCurrent(token)) {
                fail(token, ClientException(ClientErrorCode.TIMEOUT, "Web page load timed out"))
            }
        }
        timeout = task
        mainHandler.postDelayed(task, PAGE_LOAD_TIMEOUT_MILLIS)
    }

    private fun cancelTimeout() {
        timeout?.let(mainHandler::removeCallbacks)
        timeout = null
    }

    private fun fail(token: Long, failure: ClientException) {
        if (!isCurrent(token)) return
        generation++
        activeGeneration = null
        pendingLanguage = null
        phase = LoadPhase.IDLE
        cancelTimeout()
        nativeBridge?.invalidateDocument()
        if (!unusable) webView.stopLoading()
        onError(failure)
    }

    private fun failOrReport(failure: ClientException) {
        val token = activeGeneration
        if (token != null && phase == LoadPhase.LOADING) {
            fail(token, failure)
        } else if (token == null && phase == LoadPhase.LOADED && !disposed && !unusable) {
            nativeBridge?.invalidateDocument()
            onError(failure)
        }
    }

    private fun isCurrent(token: Long): Boolean =
        !disposed && !unusable && activeGeneration == token && generation == token

    private fun beginObservedNavigation() {
        val token = ++generation
        activeGeneration = token
        phase = LoadPhase.LOADING
        cancelTimeout()
        scheduleTimeout(token)
    }

    private fun completeLoad() {
        if (disposed || unusable || phase != LoadPhase.LOADING) return
        activeGeneration = null
        phase = LoadPhase.LOADED
        cancelTimeout()
        observeLanguageCookie()
        if (!disposed && !unusable && phase == LoadPhase.LOADED) onLoaded()
    }

    private fun denyNavigation(url: String, redirect: Boolean, abortActiveLoad: Boolean) {
        if (disposed || unusable || lastBlockedUrl == url) return
        lastBlockedUrl = url
        if (abortActiveLoad && activeGeneration != null) {
            generation++
            activeGeneration = null
            phase = LoadPhase.IDLE
            pendingLanguage = null
            cancelTimeout()
            nativeBridge?.invalidateDocument()
            webView.stopLoading()
        }
        onError(
            ClientException(
                if (redirect) ClientErrorCode.REDIRECT else ClientErrorCode.NAVIGATION_BLOCKED,
                "Navigation outside ${server.origin} was blocked: $url",
            ),
        )
    }

    private fun observeLanguageCookie() {
        val observed = try {
            readLanguageCookie()
        } catch (failure: RuntimeException) {
            onError(ClientException(ClientErrorCode.STORAGE, "Unable to read language cookie", failure))
            return
        } ?: return
        if (observed == language) return

        try {
            cookieManager.flush()
        } catch (failure: RuntimeException) {
            onError(ClientException(ClientErrorCode.STORAGE, "Unable to persist language cookie", failure))
            return
        }
        language = observed
        onLanguageChanged(observed)
    }

    private fun readLanguageCookie(): String? {
        val header = cookieManager.getCookie(rootUrl) ?: return null
        for (part in header.split(';')) {
            val cookie = part.trim()
            val separator = cookie.indexOf('=')
            if (separator <= 0 || cookie.substring(0, separator) != LANGUAGE_COOKIE_NAME) continue
            val value = cookie.substring(separator + 1)
            if (isLanguage(value)) return value
        }
        return null
    }

    private fun handleRendererGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
        if (disposed || unusable || view !== webView) return true
        generation++
        activeGeneration = null
        pendingLanguage = null
        phase = LoadPhase.IDLE
        cancelTimeout()
        nativeBridge?.dispose()
        nativeBridge = null
        unusable = true
        removeView(view)
        view.destroy()
        onError(
            ClientException(
                ClientErrorCode.WEB_LOAD,
                if (detail.didCrash()) "WebView renderer crashed" else "WebView renderer was terminated",
                retryable = false,
            ),
        )
        return true
    }

    private inner class RestrictedWebViewClient : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            if (disposed || unusable || view !== webView) return true
            val url = request.url.toString()
            if (isAllowed(url, server.origin)) return false
            denyNavigation(url, request.isRedirect, abortActiveLoad = request.isForMainFrame)
            return true
        }

        override fun shouldInterceptRequest(
            view: WebView,
            request: WebResourceRequest,
        ): WebResourceResponse? {
            val url = request.url.toString()
            if (isAllowed(url, server.origin)) return null
            if (request.isForMainFrame) {
                val redirect = request.isRedirect
                mainHandler.post {
                    denyNavigation(url, redirect, abortActiveLoad = true)
                }
            }
            return blockedResponse()
        }

        override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
            if (disposed || unusable || view !== webView) return
            nativeBridge?.documentStarted()
            if (!isAllowed(url, server.origin)) {
                view.stopLoading()
                denyNavigation(url, redirect = false, abortActiveLoad = true)
                return
            }
            lastBlockedUrl = null
            // Late callbacks from a failed document cannot make it healthy again.
            // Initial loads and retries enter LOADING explicitly.
            if (phase == LoadPhase.LOADED) beginObservedNavigation()
        }

        override fun onPageCommitVisible(view: WebView, url: String) {
            if (disposed || unusable || view !== webView) return
            if (phase == LoadPhase.LOADING && isAllowed(url, server.origin)) {
                nativeBridge?.documentCommitted()
            }
        }

        override fun onPageFinished(view: WebView, url: String) {
            if (disposed || unusable || view !== webView) return
            if (phase == LoadPhase.LOADING && isAllowed(url, server.origin)) {
                nativeBridge?.documentCommitted()
                completeLoad()
            }
        }

        override fun onReceivedError(
            view: WebView,
            request: WebResourceRequest,
            error: WebResourceError,
        ) {
            if (
                !request.isForMainFrame ||
                phase != LoadPhase.LOADING ||
                !isAllowed(request.url.toString(), server.origin)
            ) return
            val code = when (error.errorCode) {
                WebViewClient.ERROR_TIMEOUT -> ClientErrorCode.TIMEOUT
                WebViewClient.ERROR_FAILED_SSL_HANDSHAKE -> ClientErrorCode.TLS
                WebViewClient.ERROR_REDIRECT_LOOP -> ClientErrorCode.REDIRECT
                WebViewClient.ERROR_HOST_LOOKUP,
                WebViewClient.ERROR_CONNECT,
                WebViewClient.ERROR_IO -> ClientErrorCode.UNREACHABLE
                else -> ClientErrorCode.WEB_LOAD
            }
            val retryable = code == ClientErrorCode.UNREACHABLE ||
                code == ClientErrorCode.TIMEOUT ||
                (
                    code == ClientErrorCode.WEB_LOAD &&
                        error.errorCode != WebViewClient.ERROR_UNSUPPORTED_SCHEME &&
                        error.errorCode != WebViewClient.ERROR_AUTHENTICATION &&
                        error.errorCode != WebViewClient.ERROR_PROXY_AUTHENTICATION &&
                        error.errorCode != WebViewClient.ERROR_UNSUPPORTED_AUTH_SCHEME &&
                        error.errorCode != WebViewClient.ERROR_BAD_URL &&
                        error.errorCode != WebViewClient.ERROR_FILE &&
                        error.errorCode != WebViewClient.ERROR_FILE_NOT_FOUND &&
                        error.errorCode != WebViewClient.ERROR_UNSAFE_RESOURCE
                    )
            activeGeneration?.let { token ->
                fail(
                    token,
                    ClientException(code, "${error.description}: ${request.url}", retryable = retryable),
                )
            }
        }

        override fun onReceivedHttpError(
            view: WebView,
            request: WebResourceRequest,
            errorResponse: WebResourceResponse,
        ) {
            if (
                !request.isForMainFrame ||
                phase != LoadPhase.LOADING ||
                !isAllowed(request.url.toString(), server.origin)
            ) return
            activeGeneration?.let { token ->
                val status = errorResponse.statusCode
                fail(
                    token,
                    ClientException(
                        ClientErrorCode.HTTP_STATUS,
                        "HTTP $status for ${request.url}",
                        retryable = status == 408 ||
                            status == 425 ||
                            status == 429 ||
                            status in 500..599,
                    ),
                )
            }
        }

        override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
            handler.cancel()
            failOrReport(
                ClientException(
                    ClientErrorCode.TLS,
                    "TLS validation failed (${error.primaryError}) for ${error.url}",
                ),
            )
        }

        override fun onReceivedClientCertRequest(view: WebView, request: ClientCertRequest) {
            request.cancel()
            failOrReport(ClientException(ClientErrorCode.TLS, "Client certificates are not supported"))
        }

        override fun onReceivedHttpAuthRequest(
            view: WebView,
            handler: HttpAuthHandler,
            host: String,
            realm: String,
        ) {
            handler.cancel()
            failOrReport(
                ClientException(
                    ClientErrorCode.WEB_LOAD,
                    "HTTP authentication was rejected for $host",
                    retryable = false,
                ),
            )
        }

        override fun onFormResubmission(view: WebView, dontResend: Message, resend: Message) {
            dontResend.sendToTarget()
        }

        override fun onSafeBrowsingHit(
            view: WebView,
            request: WebResourceRequest,
            threatType: Int,
            callback: SafeBrowsingResponse,
        ) {
            callback.backToSafety(true)
            failOrReport(
                ClientException(
                    ClientErrorCode.WEB_LOAD,
                    "Unsafe content was blocked: ${request.url}",
                    retryable = false,
                ),
            )
        }

        override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean =
            handleRendererGone(view, detail)
    }

    private inner class RestrictedWebChromeClient : WebChromeClient() {
        override fun onCreateWindow(
            view: WebView,
            isDialog: Boolean,
            isUserGesture: Boolean,
            resultMsg: Message,
        ): Boolean = false

        override fun onPermissionRequest(request: PermissionRequest) {
            request.deny()
        }

        override fun onGeolocationPermissionsShowPrompt(
            origin: String,
            callback: GeolocationPermissions.Callback,
        ) {
            callback.invoke(origin, false, false)
        }

        override fun onShowFileChooser(
            webView: WebView,
            filePathCallback: ValueCallback<Array<android.net.Uri>>,
            fileChooserParams: WebChromeClient.FileChooserParams,
        ): Boolean {
            filePathCallback.onReceiveValue(null)
            return true
        }
    }

    private fun requireMainThread() {
        check(Looper.myLooper() == Looper.getMainLooper()) { "RemoteServerView must be used on the main thread" }
    }

    companion object {
        private const val LANGUAGE_COOKIE_NAME = "jastreamer_language"
        private const val LANGUAGE_COOKIE_LIFETIME_SECONDS = 31_536_000
        private const val PAGE_LOAD_TIMEOUT_MILLIS = 30_000L

        private fun isLanguage(value: String): Boolean = value == "en" || value == "ko"

        private fun requireLanguage(value: String): String {
            if (!isLanguage(value)) {
                throw ClientException(ClientErrorCode.STORAGE, "Unsupported language: $value")
            }
            return value
        }

        private fun isAllowed(url: String, origin: String): Boolean =
            EndpointPolicy.sameOrigin(url, origin)

        private fun blockedResponse(): WebResourceResponse = WebResourceResponse(
            "text/plain",
            "UTF-8",
            403,
            "Blocked",
            emptyMap(),
            ByteArrayInputStream(ByteArray(0)),
        )
    }
}
