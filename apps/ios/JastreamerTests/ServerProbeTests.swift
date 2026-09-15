import Foundation
import XCTest
@testable import Jastreamer

final class ServerProbeTests: XCTestCase {
    func testRejectsRedirectWithoutFollowingIt() async throws {
        let origin = "http://127.0.0.1:18083"
        let error = await failure { try await ServerProbe().probe(origin) }
        guard let clientError = error as? JastreamerClientError, case .redirect = clientError else {
            return XCTFail("Expected redirect rejection, got \(String(describing: error))")
        }
        let counterURL = try XCTUnwrap(URL(string: "http://127.0.0.1:18081/redirect-count"))
        let session = URLSession(configuration: .ephemeral)
        defer { session.invalidateAndCancel() }
        let (blockedCount, _) = try await session.data(from: counterURL)
        XCTAssertEqual(String(decoding: blockedCount, as: UTF8.self), "0")

        // A normal URLSession must reach the same redirect target: the fixture
        // is live and would expose a missing no-redirect policy.
        let (body, response) = try await session.data(from: XCTUnwrap(URL(string: origin + "/api/v1/discovery")))
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
        let metadata = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        XCTAssertEqual(metadata?["product"] as? String, "jastreamer")
        let (followedCount, _) = try await session.data(from: counterURL)
        XCTAssertEqual(String(decoding: followedCount, as: UTF8.self), "1")
    }

    func testRejectsUnknownLengthBodyOver32KiB() async {
        let padding = String(repeating: "a", count: 32 * 1_024)
        let otherwiseValid = Data("""
        {"product":"jastreamer","protocol":1,"id":"11111111-1111-4111-8111-111111111111","name":"Living room","version":"0.2.0","padding":"\(padding)"}
        """.utf8)
        ProbeURLProtocol.install(host: "oversize.local") { protocolInstance in
            protocolInstance.respond(status: 200, headers: ["Content-Type": "application/json"], data: otherwiseValid)
        }
        defer { ProbeURLProtocol.remove(host: "oversize.local") }

        let error = await failure { try await self.makeProbe().probe("http://oversize.local") }
        guard let clientError = error as? JastreamerClientError, case .invalidMetadata = clientError else {
            return XCTFail("Expected an invalid metadata error, got \(String(describing: error))")
        }
    }

    func testExpectedIdentityCannotChange() async {
        ProbeURLProtocol.install(host: "mismatch.local") { protocolInstance in
            protocolInstance.respond(
                status: 200,
                headers: ["Content-Type": "application/json; charset=utf-8"],
                data: Self.discoveryJSON(id: "22222222-2222-4222-8222-222222222222")
            )
        }
        defer { ProbeURLProtocol.remove(host: "mismatch.local") }

        let error = await failure {
            try await self.makeProbe().probe(
                "http://mismatch.local",
                expectedID: "11111111-1111-4111-8111-111111111111"
            )
        }
        guard let clientError = error as? JastreamerClientError, case .identityMismatch = clientError else {
            return XCTFail("Expected an identity mismatch, got \(String(describing: error))")
        }
    }

    func testProbeUsesDiscoveryPathWithoutAmbientCookies() async throws {
        let observed = LockedRequest()
        ProbeURLProtocol.install(host: "cookie.local") { protocolInstance in
            observed.set(protocolInstance.request)
            protocolInstance.respond(
                status: 200,
                headers: ["Content-Type": "application/json"],
                data: Self.discoveryJSON(id: "11111111-1111-4111-8111-111111111111")
            )
        }
        defer { ProbeURLProtocol.remove(host: "cookie.local") }
        let configuration = URLSessionConfiguration.ephemeral
        let storage = try XCTUnwrap(configuration.httpCookieStorage)
        let cookie = try XCTUnwrap(HTTPCookie(properties: [
            .domain: "cookie.local",
            .path: "/",
            .name: "session",
            .value: "secret",
            .secure: "FALSE",
        ]))
        storage.setCookie(cookie)
        XCTAssertTrue(storage.cookies?.contains(where: { $0.name == "session" }) == true)
        configuration.protocolClasses = [ProbeURLProtocol.self]

        let endpoint = try await ServerProbe(configuration: configuration).probe("http://cookie.local")
        let request = try XCTUnwrap(observed.value)
        XCTAssertEqual(request.url?.path, "/api/v1/discovery")
        XCTAssertNil(request.value(forHTTPHeaderField: "Cookie"))
        XCTAssertEqual(endpoint.origin, "http://cookie.local")
    }

    func testCancellationStopsUnderlyingLoad() async throws {
        let started = expectation(description: "protocol started")
        let stopped = expectation(description: "protocol stopped")
        ProbeURLProtocol.install(host: "cancel.local", handler: { _ in started.fulfill() }, onStop: { stopped.fulfill() })
        defer { ProbeURLProtocol.remove(host: "cancel.local") }

        let task = Task { try await self.makeProbe().probe("http://cancel.local") }
        await fulfillment(of: [started], timeout: 1)
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("Cancelled probe unexpectedly succeeded")
        } catch is CancellationError {
        } catch {
            XCTFail("Cancelled probe returned the wrong error: \(error)")
        }
        await fulfillment(of: [stopped], timeout: 1)
    }

    private func makeProbe() -> ServerProbe {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ProbeURLProtocol.self]
        return ServerProbe(configuration: configuration)
    }

    private func failure(_ operation: () async throws -> ServerEndpoint) async -> Error? {
        do {
            _ = try await operation()
            XCTFail("Operation unexpectedly succeeded")
            return nil
        } catch {
            return error
        }
    }

    private static func discoveryJSON(id: String) -> Data {
        Data("""
        {"product":"jastreamer","protocol":1,"id":"\(id)","name":"Living room","version":"0.2.0"}
        """.utf8)
    }
}

private final class ProbeURLProtocol: URLProtocol {
    typealias Handler = (ProbeURLProtocol) -> Void
    private struct Behavior {
        let handler: Handler
        let onStop: (() -> Void)?
    }

    private static let registryLock = NSLock()
    private static var behaviors: [String: Behavior] = [:]

    static func install(host: String, handler: @escaping Handler, onStop: (() -> Void)? = nil) {
        registryLock.withLock {
            behaviors[host] = Behavior(handler: handler, onStop: onStop)
        }
    }

    static func remove(host: String) {
        registryLock.withLock {
            behaviors.removeValue(forKey: host)
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let host = request.url?.host,
              let behavior = Self.registryLock.withLock({ Self.behaviors[host] })
        else {
            client?.urlProtocol(self, didFailWithError: URLError(.resourceUnavailable))
            return
        }
        behavior.handler(self)
    }

    override func stopLoading() {
        guard let host = request.url?.host else { return }
        Self.registryLock.withLock { Self.behaviors[host]?.onStop }?()
    }


    func respond(status: Int, headers: [String: String], data: Data) {
        guard let url = request.url,
              let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)
        else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse))
            return
        }
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        if !data.isEmpty { client?.urlProtocol(self, didLoad: data) }
        client?.urlProtocolDidFinishLoading(self)
    }
}


private final class LockedRequest {
    private let lock = NSLock()
    private var stored: URLRequest?

    var value: URLRequest? {
        lock.withLock { stored }
    }

    func set(_ request: URLRequest) {
        lock.withLock { stored = request }
    }
}

private extension NSLock {
    func withLock<T>(_ operation: () -> T) -> T {
        lock()
        defer { unlock() }
        return operation()
    }
}
