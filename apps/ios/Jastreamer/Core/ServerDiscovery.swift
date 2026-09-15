import Combine
import Darwin
import Foundation

@MainActor
final class ServerDiscovery: ObservableObject {
    @Published private(set) var servers: [ServerEndpoint] = []
    @Published private(set) var error: String?

    private let probe: ServerProbe
    private let browsing: ServerDiscoveryBrowsing
    private var fence = DiscoveryFence()
    private var candidates: [String: ProbeCandidate] = [:]
    private var queuedKeys: [String] = []
    private var activeCount = 0

    init(probe: ServerProbe = ServerProbe()) {
        self.probe = probe
        browsing = BonjourServerBrowser()
    }

    init(probe: ServerProbe, browsing: ServerDiscoveryBrowsing) {
        self.probe = probe
        self.browsing = browsing
    }

    deinit {
        let browsing = browsing
        Task { @MainActor in
            browsing.eventHandler = nil
            browsing.stop()
        }
        candidates.values.forEach { $0.task?.cancel() }
    }

    func start() {
        resetSession()
        error = nil
        let generation = fence.beginSession()
        browsing.eventHandler = { [weak self] event in
            guard let self, self.fence.isSessionCurrent(generation) else { return }
            self.receive(event)
        }
        browsing.start()
    }

    func stop() {
        resetSession()
        error = nil
    }

    private func resetSession() {
        browsing.eventHandler = nil
        browsing.stop()
        _ = fence.endSession()
        candidates.values.forEach { $0.task?.cancel() }
        candidates.removeAll()
        queuedKeys.removeAll()
        activeCount = 0
        servers = []
    }

    private func receive(_ event: ServerDiscoveryEvent) {
        switch event {
        case let .found(advertisement):
            accept(advertisement)
        case let .lost(key):
            remove(key)
        case .failed:
            error = JastreamerClientError.discovery.localizedDescription
        }
    }

    private func accept(_ advertisement: ServerAdvertisement) {
        guard !advertisement.key.isEmpty,
              advertisement.key.utf8.count <= 512,
              let id = try? EndpointPolicy.normalizeServerID(advertisement.id)
        else { return }
        let origins = advertisement.origins.prefix(6).compactMap { try? EndpointPolicy.normalizeRequiredOrigin($0) }
        guard !origins.isEmpty else { return }
        if candidates[advertisement.key] == nil, candidates.count >= 64 { return }
        remove(advertisement.key)

        let token = fence.register(key: advertisement.key)
        let normalized = ServerAdvertisement(key: advertisement.key, id: id, origins: Array(Set(origins)).sorted())
        let candidate = ProbeCandidate(advertisement: normalized, token: token)
        candidates[advertisement.key] = candidate
        queuedKeys.append(advertisement.key)
        drainQueue()
    }

    private func remove(_ key: String) {
        guard let candidate = candidates.removeValue(forKey: key) else { return }
        fence.remove(key: key)
        queuedKeys.removeAll { $0 == key }
        candidate.task?.cancel()
        if candidate.isActive { activeCount = max(0, activeCount - 1) }
        publishServers()
        drainQueue()
    }

    private func drainQueue() {
        while activeCount < 4, !queuedKeys.isEmpty {
            let key = queuedKeys.removeFirst()
            guard let candidate = candidates[key], fence.accepts(candidate.token), !candidate.isActive else { continue }
            candidate.isActive = true
            activeCount += 1
            let probe = self.probe
            let advertisement = candidate.advertisement
            candidate.task = Task { [weak self, weak candidate] in
                guard let candidate else { return }
                var endpoint: ServerEndpoint?
                for origin in advertisement.origins {
                    guard !Task.isCancelled,
                          self?.fence.accepts(candidate.token) == true,
                          self?.candidates[key] === candidate
                    else { return }
                    do {
                        endpoint = try await probe.probe(origin, expectedID: advertisement.id)
                        break
                    } catch is CancellationError {
                        return
                    } catch {
                        continue
                    }
                }
                guard !Task.isCancelled,
                      self?.fence.accepts(candidate.token) == true,
                      self?.candidates[key] === candidate
                else { return }
                self?.finish(candidate, endpoint: endpoint)
            }
        }
    }

    private func finish(_ candidate: ProbeCandidate, endpoint: ServerEndpoint?) {
        let key = candidate.advertisement.key
        guard candidates[key] === candidate, fence.accepts(candidate.token), candidate.isActive else { return }
        candidate.task = nil
        candidate.endpoint = endpoint
        candidate.isActive = false
        activeCount = max(0, activeCount - 1)
        publishServers()
        drainQueue()
    }

    private func publishServers() {
        var seen = Set<String>()
        servers = candidates.values.compactMap(\.endpoint).filter { endpoint in
            seen.insert("\(endpoint.id)\u{0}\(endpoint.origin)").inserted
        }.sorted { first, second in
            let ordered = first.name.localizedCaseInsensitiveCompare(second.name)
            return ordered == .orderedSame ? first.origin < second.origin : ordered == .orderedAscending
        }
    }
}

struct ServerAdvertisement: Equatable {
    let key: String
    let id: String
    let origins: [String]
}

enum ServerDiscoveryEvent {
    case found(ServerAdvertisement)
    case lost(String)
    case failed
}

@MainActor
protocol ServerDiscoveryBrowsing: AnyObject {
    var eventHandler: ((ServerDiscoveryEvent) -> Void)? { get set }
    func start()
    func stop()
}

private final class ProbeCandidate {
    let advertisement: ServerAdvertisement
    let token: DiscoveryFence.Token
    var isActive = false
    var task: Task<Void, Never>?
    var endpoint: ServerEndpoint?

    init(advertisement: ServerAdvertisement, token: DiscoveryFence.Token) {
        self.advertisement = advertisement
        self.token = token
    }
}

struct DiscoveryFence {
    struct Token: Equatable {
        fileprivate let session: UInt64
        fileprivate let serial: UInt64
        fileprivate let key: String
    }

    private var session: UInt64 = 0
    private var serial: UInt64 = 0
    private var current: [String: Token] = [:]

    mutating func beginSession() -> UInt64 {
        session &+= 1
        current.removeAll()
        return session
    }

    mutating func endSession() -> UInt64 {
        session &+= 1
        current.removeAll()
        return session
    }

    mutating func register(key: String) -> Token {
        serial &+= 1
        let token = Token(session: session, serial: serial, key: key)
        current[key] = token
        return token
    }

    mutating func remove(key: String) {
        current.removeValue(forKey: key)
    }

    func isSessionCurrent(_ value: UInt64) -> Bool {
        session == value
    }

    func accepts(_ token: Token) -> Bool {
        token.session == session && current[token.key] == token
    }
}

@MainActor
private final class BonjourServerBrowser: NSObject, ServerDiscoveryBrowsing {
    var eventHandler: ((ServerDiscoveryEvent) -> Void)?

    private var browser: NetServiceBrowser?
    private var services: [String: BonjourCandidate] = [:]
    private var queuedKeys: [String] = []
    private var activeCount = 0

    func start() {
        stop()
        let browser = NetServiceBrowser()
        self.browser = browser
        browser.delegate = self
        browser.searchForServices(ofType: "_jastreamer._tcp.", inDomain: "local.")
    }

    func stop() {
        let oldBrowser = browser
        browser = nil
        oldBrowser?.delegate = nil
        oldBrowser?.stop()
        for candidate in services.values {
            candidate.service.delegate = nil
            candidate.service.stop()
            candidate.service.remove(from: .main, forMode: .common)
        }
        services.removeAll()
        queuedKeys.removeAll()
        activeCount = 0
    }

    private func add(_ service: NetService) {
        guard service.type.lowercased() == "_jastreamer._tcp." else { return }
        let key = serviceKey(service)
        guard !key.isEmpty else { return }
        if services[key] == nil, services.count >= 64 { return }
        remove(key, notify: true)
        services[key] = BonjourCandidate(key: key, service: service)
        queuedKeys.append(key)
        drainQueue()
    }

    private func remove(_ key: String, notify: Bool) {
        guard let candidate = services.removeValue(forKey: key) else { return }
        queuedKeys.removeAll { $0 == key }
        candidate.service.delegate = nil
        candidate.service.stop()
        candidate.service.remove(from: .main, forMode: .common)
        if candidate.isActive { activeCount = max(0, activeCount - 1) }
        if notify { eventHandler?(.lost(key)) }
        drainQueue()
    }

    private func drainQueue() {
        while activeCount < 4, !queuedKeys.isEmpty {
            let key = queuedKeys.removeFirst()
            guard let candidate = services[key], !candidate.isActive else { continue }
            candidate.isActive = true
            activeCount += 1
            candidate.service.delegate = self
            candidate.service.schedule(in: .main, forMode: .common)
            candidate.service.resolve(withTimeout: 3)
        }
    }

    private func resolved(_ service: NetService) {
        let key = serviceKey(service)
        guard let candidate = services[key], candidate.service === service, candidate.isActive else { return }
        service.stop()
        if let advertisement = parseAdvertisement(service, key: key) {
            eventHandler?(.found(advertisement))
        }
        finish(candidate)
    }

    private func resolutionFailed(_ service: NetService) {
        let key = serviceKey(service)
        guard let candidate = services[key], candidate.service === service, candidate.isActive else { return }
        finish(candidate)
    }

    private func finish(_ candidate: BonjourCandidate) {
        guard services[candidate.key] === candidate, candidate.isActive else { return }
        candidate.isActive = false
        activeCount = max(0, activeCount - 1)
        candidate.service.delegate = nil
        candidate.service.remove(from: .main, forMode: .common)
        drainQueue()
    }

    private func parseAdvertisement(_ service: NetService, key: String) -> ServerAdvertisement? {
        guard (1...65_535).contains(service.port), let record = service.txtRecordData() else { return nil }
        let values = NetService.dictionary(fromTXTRecord: record)
        if let productData = values["product"], strictText(productData, maximumBytes: 32) != "jastreamer" { return nil }
        guard strictText(values["protocol"], maximumBytes: 16) == "1",
              strictText(values["path"], maximumBytes: 16) == "/",
              let rawID = strictText(values["id"], maximumBytes: 64),
              let rawName = strictText(values["name"], maximumBytes: 255),
              let rawVersion = strictText(values["version"], maximumBytes: 255),
              let rawScheme = strictText(values["scheme"], maximumBytes: 16),
              let id = try? EndpointPolicy.normalizeServerID(rawID),
              MetadataPolicy.cleanDisplayText(rawName, maximum: 128) != nil,
              MetadataPolicy.cleanDisplayText(rawVersion, maximum: 64) != nil
        else { return nil }
        let scheme = rawScheme.lowercased()
        guard scheme == "http" || scheme == "https" else { return nil }

        var seen = Set<String>()
        var origins: [String] = []
        for data in service.addresses ?? [] {
            guard let host = numericLANHost(data) else { continue }
            let renderedHost = host.contains(":") ? "[\(host)]" : host
            guard let origin = try? EndpointPolicy.normalize("\(scheme)://\(renderedHost):\(service.port)"), seen.insert(origin).inserted else { continue }
            origins.append(origin)
            if origins.count == 6 { break }
        }
        guard !origins.isEmpty else { return nil }
        return ServerAdvertisement(key: key, id: id, origins: origins)
    }

    private func numericLANHost(_ data: Data) -> String? {
        guard data.count >= MemoryLayout<sockaddr>.size else { return nil }
        var header = sockaddr()
        withUnsafeMutableBytes(of: &header) { $0.copyBytes(from: data.prefix($0.count)) }
        switch Int32(header.sa_family) {
        case AF_INET:
            guard data.count >= MemoryLayout<sockaddr_in>.size else { return nil }
            var socketAddress = sockaddr_in()
            withUnsafeMutableBytes(of: &socketAddress) { $0.copyBytes(from: data.prefix($0.count)) }
            var address = socketAddress.sin_addr
            let bytes = withUnsafeBytes(of: &address) { Array($0) }
            guard bytes.count == 4,
                  bytes[0] == 10 ||
                  (bytes[0] == 172 && (16...31).contains(bytes[1])) ||
                  (bytes[0] == 192 && bytes[1] == 168) ||
                  (bytes[0] == 169 && bytes[1] == 254)
            else { return nil }
            return renderAddress(family: AF_INET, address: &address, capacity: Int(INET_ADDRSTRLEN))
        case AF_INET6:
            guard data.count >= MemoryLayout<sockaddr_in6>.size else { return nil }
            var socketAddress = sockaddr_in6()
            withUnsafeMutableBytes(of: &socketAddress) { $0.copyBytes(from: data.prefix($0.count)) }
            guard socketAddress.sin6_scope_id == 0 else { return nil }
            var address = socketAddress.sin6_addr
            let bytes = withUnsafeBytes(of: &address) { Array($0) }
            guard bytes.count == 16, bytes[0] & 0xfe == 0xfc else { return nil }
            return renderAddress(family: AF_INET6, address: &address, capacity: Int(INET6_ADDRSTRLEN))
        default:
            return nil
        }
    }

    private func renderAddress<T>(family: Int32, address: inout T, capacity: Int) -> String? {
        var buffer = [CChar](repeating: 0, count: capacity)
        return buffer.withUnsafeMutableBufferPointer { output in
            guard let base = output.baseAddress, inet_ntop(family, &address, base, socklen_t(output.count)) != nil else { return nil }
            return String(cString: base).lowercased()
        }
    }

    private func strictText(_ data: Data?, maximumBytes: Int) -> String? {
        guard let data, !data.isEmpty, data.count <= maximumBytes,
              let value = String(data: data, encoding: .utf8), Data(value.utf8) == data
        else { return nil }
        return value
    }

    private func serviceKey(_ service: NetService) -> String {
        let name = service.name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard MetadataPolicy.cleanDisplayText(name, maximum: 128) != nil else { return "" }
        return "\(name)\u{0}\(service.type.lowercased())\u{0}\(service.domain.lowercased())"
    }
}

extension BonjourServerBrowser: NetServiceBrowserDelegate {
    nonisolated func netServiceBrowser(_ browser: NetServiceBrowser, didFind service: NetService, moreComing: Bool) {
        Task { @MainActor [weak self] in
            guard let self, self.browser === browser else { return }
            self.add(service)
        }
    }

    nonisolated func netServiceBrowser(_ browser: NetServiceBrowser, didRemove service: NetService, moreComing: Bool) {
        Task { @MainActor [weak self] in
            guard let self, self.browser === browser else { return }
            self.remove(self.serviceKey(service), notify: true)
        }
    }

    nonisolated func netServiceBrowser(_ browser: NetServiceBrowser, didNotSearch errorDict: [String: NSNumber]) {
        Task { @MainActor [weak self] in
            guard let self, self.browser === browser else { return }
            self.eventHandler?(.failed)
        }
    }

    nonisolated func netServiceBrowserDidStopSearch(_ browser: NetServiceBrowser) {
        Task { @MainActor [weak self] in
            guard let self, self.browser === browser else { return }
            self.eventHandler?(.failed)
        }
    }
}

extension BonjourServerBrowser: NetServiceDelegate {
    nonisolated func netServiceDidResolveAddress(_ sender: NetService) {
        Task { @MainActor [weak self] in self?.resolved(sender) }
    }

    nonisolated func netService(_ sender: NetService, didNotResolve errorDict: [String: NSNumber]) {
        Task { @MainActor [weak self] in self?.resolutionFailed(sender) }
    }
}

private final class BonjourCandidate {
    let key: String
    let service: NetService
    var isActive = false

    init(key: String, service: NetService) {
        self.key = key
        self.service = service
    }
}
