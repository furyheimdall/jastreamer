import Foundation
import SwiftUI

@MainActor
final class ClientBootstrap: ObservableObject {
    @Published private(set) var recentServers: RecentServers?
    @Published private(set) var error: String?

    init() {
        reload()
    }

    func reload() {
        do {
            recentServers = try RecentServers()
            error = nil
        } catch {
            recentServers = nil
            self.error = L10n(L10n.bootstrapLanguage).errorMessage(error)
        }
    }
}

@MainActor
final class ClientModel: ObservableObject {
    let recentServers: RecentServers
    let discovery: ServerDiscovery

    @Published private(set) var currentServer: ServerEndpoint?
    @Published private(set) var isConnecting = false
    @Published private(set) var isReconnecting = false
    @Published private(set) var isRemoteVerified = false
    @Published private(set) var selectionError: String?
    @Published private(set) var remoteError: String?
    @Published private(set) var webAttemptID = UUID()

    private let probe: ServerProbe
    private var connectionTask: Task<Void, Never>?
    private var generation: UInt64 = 0
    private var foreground = false
    private var retryTarget: (origin: String, id: String?)?

    init(
        recentServers: RecentServers,
        probe: ServerProbe = ServerProbe(),
        discovery: ServerDiscovery? = nil
    ) {
        self.recentServers = recentServers
        self.probe = probe
        self.discovery = discovery ?? ServerDiscovery(probe: probe)
    }

    func activate() {
        guard !foreground else { return }
        foreground = true
        if currentServer == nil {
            discovery.start()
        } else {
            verifyCurrentServer(recreateWebView: false)
        }
    }

    func deactivate() {
        foreground = false
        generation &+= 1
        connectionTask?.cancel()
        connectionTask = nil
        isConnecting = false
        isReconnecting = false
        isRemoteVerified = false
        discovery.stop()
    }

    func refreshDiscovery() {
        guard foreground, currentServer == nil else { return }
        discovery.stop()
        discovery.start()
    }

    func connect(_ input: String, expectedID: String? = nil) {
        generation &+= 1
        let attempt = generation
        connectionTask?.cancel()
        isRemoteVerified = false
        currentServer = nil
        isConnecting = true
        isReconnecting = false
        selectionError = nil
        remoteError = nil
        retryTarget = (input, expectedID)

        connectionTask = Task { [weak self] in
            guard let self else { return }
            do {
                let server = try await probe.probe(input, expectedID: expectedID)
                try Task.checkCancellation()
                guard foreground, generation == attempt else { return }

                // A Server becomes current only after both identity verification and
                // the atomic recent-Server write have succeeded.
                try recentServers.save(server: server)
                guard foreground, generation == attempt else { return }

                discovery.stop()
                currentServer = server
                retryTarget = (server.origin, server.id)
                isConnecting = false
                isRemoteVerified = true
                connectionTask = nil
                webAttemptID = UUID()
            } catch is CancellationError {
                return
            } catch {
                guard generation == attempt else { return }
                isConnecting = false
                connectionTask = nil
                selectionError = userFacing(error)
            }
        }
    }

    func cancelConnection() {
        generation &+= 1
        connectionTask?.cancel()
        connectionTask = nil
        isConnecting = false
        selectionError = nil
        if foreground {
            discovery.start()
        }
    }

    func retrySelection() {
        guard let retryTarget else { return }
        connect(retryTarget.origin, expectedID: retryTarget.id)
    }

    func switchServer() {
        generation &+= 1
        connectionTask?.cancel()
        connectionTask = nil
        currentServer = nil
        isConnecting = false
        isRemoteVerified = false
        isReconnecting = false
        selectionError = nil
        remoteError = nil
        if foreground {
            discovery.start()
        }
    }

    func retryRemote() {
        verifyCurrentServer(recreateWebView: true)
    }

    private func verifyCurrentServer(recreateWebView: Bool) {
        guard foreground, let server = currentServer else { return }
        generation &+= 1
        let attempt = generation
        connectionTask?.cancel()
        isRemoteVerified = false
        isReconnecting = true
        remoteError = nil

        connectionTask = Task { [weak self] in
            guard let self else { return }
            do {
                let verified = try await probe.probe(server.origin, expectedID: server.id)
                try Task.checkCancellation()
                guard foreground, generation == attempt, currentServer == server else { return }
                try recentServers.save(server: verified)
                guard foreground, generation == attempt, currentServer == server else { return }

                currentServer = verified
                retryTarget = (verified.origin, verified.id)
                isRemoteVerified = true
                isReconnecting = false
                connectionTask = nil
                if recreateWebView {
                    webAttemptID = UUID()
                }
            } catch is CancellationError {
                return
            } catch {
                guard generation == attempt, currentServer == server else { return }
                isRemoteVerified = false
                isReconnecting = false
                connectionTask = nil
                remoteError = userFacing(error)
            }
        }
    }

    func removeRecent(_ server: ServerEndpoint) {
        do {
            try recentServers.remove(server: server)
            selectionError = nil
        } catch {
            selectionError = userFacing(error)
        }
    }

    func changeLanguage(_ value: String) {
        do {
            try recentServers.setLanguage(value)
            selectionError = nil
            remoteError = nil
        } catch {
            if currentServer == nil {
                selectionError = userFacing(error)
            } else {
                remoteError = userFacing(error)
            }
        }
    }

    func acceptWebLanguage(_ value: String) {
        guard value == "en" || value == "ko", value != recentServers.language else { return }
        changeLanguage(value)
    }

    func clearSelectionError() {
        selectionError = nil
    }

    func clearRemoteError() {
        remoteError = nil
    }

    private func userFacing(_ error: Error) -> String {
        L10n(recentServers.language).errorMessage(error)
    }
}
