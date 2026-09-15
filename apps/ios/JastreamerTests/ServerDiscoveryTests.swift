import Foundation
import XCTest
@testable import Jastreamer

@MainActor
final class ServerDiscoveryTests: XCTestCase {
    func testVerifiedAdvertisementAppearsThenLostServiceRemovesIt() async throws {
        let id = "11111111-1111-4111-8111-111111111111"
        let browser = FakeDiscoveryBrowser()
        let probe = ServerProbe { request in
            try Self.response(for: request, id: id)
        }
        let discovery = ServerDiscovery(probe: probe, browsing: browser)
        defer { discovery.stop() }
        discovery.start()

        browser.emit(.found(ServerAdvertisement(key: "living-room", id: id, origins: ["http://192.168.1.8:8080"])))
        let appeared = await eventually { discovery.servers.count == 1 }
        XCTAssertTrue(appeared)
        XCTAssertEqual(discovery.servers.first?.origin, "http://192.168.1.8:8080")

        browser.emit(.lost("living-room"))
        XCTAssertEqual(discovery.servers, [])
    }

    func testCompletionFromPreviousSessionCannotRepublishAfterRestart() async throws {
        let id = "22222222-2222-4222-8222-222222222222"
        let started = expectation(description: "probe started")
        let loader = DeferredDiscoveryLoader(onStart: { started.fulfill() })
        let browser = FakeDiscoveryBrowser()
        let discovery = ServerDiscovery(probe: ServerProbe(loader: loader.load), browsing: browser)
        defer {
            discovery.stop()
            loader.cancel()
        }
        discovery.start()
        browser.emit(.found(ServerAdvertisement(key: "office", id: id, origins: ["http://192.168.1.9:8080"])))
        await fulfillment(of: [started], timeout: 1)

        discovery.stop()
        discovery.start()
        loader.succeed(id: id)
        let resumed = await eventually { loader.didResume }
        XCTAssertTrue(resumed)
        XCTAssertEqual(discovery.servers, [])
    }

    func testCompletionAfterServiceIsLostCannotRepublishIt() async throws {
        let id = "33333333-3333-4333-8333-333333333333"
        let started = expectation(description: "probe started")
        let loader = DeferredDiscoveryLoader(onStart: { started.fulfill() })
        let browser = FakeDiscoveryBrowser()
        let discovery = ServerDiscovery(probe: ServerProbe(loader: loader.load), browsing: browser)
        defer {
            discovery.stop()
            loader.cancel()
        }
        discovery.start()
        browser.emit(.found(ServerAdvertisement(key: "kitchen", id: id, origins: ["http://192.168.1.10:8080"])))
        await fulfillment(of: [started], timeout: 1)

        browser.emit(.lost("kitchen"))
        loader.succeed(id: id)
        let resumed = await eventually { loader.didResume }
        XCTAssertTrue(resumed)
        XCTAssertEqual(discovery.servers, [])
    }

    func testAtMostFourAdvertisementsAreProbedConcurrently() async throws {
        let loader = BoundedDiscoveryLoader()
        let browser = FakeDiscoveryBrowser()
        let discovery = ServerDiscovery(probe: ServerProbe(loader: loader.load), browsing: browser)
        defer {
            discovery.stop()
            loader.cancelAll()
        }
        discovery.start()

        var identities: [String: String] = [:]
        for index in 1...5 {
            let host = "192.168.1.\(index)"
            let id = String(format: "00000000-0000-4000-8000-%012x", index)
            identities[host] = id
            browser.emit(.found(ServerAdvertisement(key: "server-\(index)", id: id, origins: ["http://\(host):8080"])))
        }

        let firstWaveStarted = await eventually { loader.startedCount == 4 }
        XCTAssertTrue(firstWaveStarted)
        XCTAssertEqual(loader.maximumActiveCount, 4)
        loader.succeed(host: "192.168.1.1", id: try XCTUnwrap(identities["192.168.1.1"]))
        let queuedProbeStarted = await eventually { loader.startedCount == 5 }
        XCTAssertTrue(queuedProbeStarted)
        XCTAssertEqual(loader.maximumActiveCount, 4)

        for index in 2...5 {
            let host = "192.168.1.\(index)"
            loader.succeed(host: host, id: try XCTUnwrap(identities[host]))
        }
        let allAppeared = await eventually { discovery.servers.count == 5 }
        XCTAssertTrue(allAppeared)
    }

    private func eventually(_ condition: @MainActor () -> Bool) async -> Bool {
        for _ in 0..<100 {
            if condition() { return true }
            try? await Task.sleep(nanoseconds: 10_000_000)
        }
        return condition()
    }

    nonisolated fileprivate static func response(for request: URLRequest, id: String) throws -> (HTTPURLResponse, Data) {
        guard let url = request.url,
              let response = HTTPURLResponse(
                  url: url,
                  statusCode: 200,
                  httpVersion: "HTTP/1.1",
                  headerFields: ["Content-Type": "application/json"]
              )
        else { throw URLError(.badServerResponse) }
        let body = Data("""
        {"product":"jastreamer","protocol":1,"id":"\(id)","name":"Living room","version":"0.2.0"}
        """.utf8)
        return (response, body)
    }
}

@MainActor
private final class FakeDiscoveryBrowser: ServerDiscoveryBrowsing {
    var eventHandler: ((ServerDiscoveryEvent) -> Void)?
    private(set) var running = false

    func start() { running = true }
    func stop() { running = false }

    func emit(_ event: ServerDiscoveryEvent) {
        guard running else { return }
        eventHandler?(event)
    }
}

private final class DeferredDiscoveryLoader {
    private let lock = NSLock()
    private let onStart: () -> Void
    private var request: URLRequest?
    private var continuation: CheckedContinuation<(HTTPURLResponse, Data), Error>?
    private var resumed = false

    init(onStart: @escaping () -> Void) {
        self.onStart = onStart
    }

    var didResume: Bool { lock.withDiscoveryLock { resumed } }

    func load(_ request: URLRequest) async throws -> (HTTPURLResponse, Data) {
        let result: (HTTPURLResponse, Data) = try await withCheckedThrowingContinuation { continuation in
            lock.withDiscoveryLock {
                self.request = request
                self.continuation = continuation
            }
            onStart()
        }
        lock.withDiscoveryLock { resumed = true }
        return result
    }

    func succeed(id: String) {
        let pending = lock.withDiscoveryLock { () -> (URLRequest, CheckedContinuation<(HTTPURLResponse, Data), Error>)? in
            guard let request, let continuation else { return nil }
            self.request = nil
            self.continuation = nil
            return (request, continuation)
        }
        guard let pending, let response = try? ServerDiscoveryTests.response(for: pending.0, id: id) else { return }
        pending.1.resume(returning: response)
    }

    func cancel() {
        let continuation = lock.withDiscoveryLock { () -> CheckedContinuation<(HTTPURLResponse, Data), Error>? in
            let value = self.continuation
            self.continuation = nil
            request = nil
            return value
        }
        continuation?.resume(throwing: CancellationError())
    }
}

private final class BoundedDiscoveryLoader {
    private let lock = NSLock()
    private var pending: [String: (URLRequest, CheckedContinuation<(HTTPURLResponse, Data), Error>)] = [:]
    private var started = 0
    private var active = 0
    private var maximumActive = 0

    var startedCount: Int { lock.withDiscoveryLock { started } }
    var maximumActiveCount: Int { lock.withDiscoveryLock { maximumActive } }

    func load(_ request: URLRequest) async throws -> (HTTPURLResponse, Data) {
        let result: (HTTPURLResponse, Data) = try await withCheckedThrowingContinuation { continuation in
            guard let host = request.url?.host else {
                continuation.resume(throwing: URLError(.badURL))
                return
            }
            lock.withDiscoveryLock {
                pending[host] = (request, continuation)
                started += 1
                active += 1
                maximumActive = max(maximumActive, active)
            }
        }
        lock.withDiscoveryLock { active -= 1 }
        return result
    }

    func succeed(host: String, id: String) {
        let requestAndContinuation = lock.withDiscoveryLock { pending.removeValue(forKey: host) }
        guard let requestAndContinuation,
              let response = try? ServerDiscoveryTests.response(for: requestAndContinuation.0, id: id)
        else { return }
        requestAndContinuation.1.resume(returning: response)
    }

    func cancelAll() {
        let continuations = lock.withDiscoveryLock { () -> [CheckedContinuation<(HTTPURLResponse, Data), Error>] in
            let values = pending.values.map(\.1)
            pending.removeAll()
            return values
        }
        continuations.forEach { $0.resume(throwing: CancellationError()) }
    }
}

private extension NSLock {
    func withDiscoveryLock<T>(_ operation: () -> T) -> T {
        lock()
        defer { unlock() }
        return operation()
    }
}
