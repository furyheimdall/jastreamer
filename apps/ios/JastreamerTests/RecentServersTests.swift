import Foundation
import XCTest
@testable import Jastreamer

@MainActor
final class RecentServersTests: XCTestCase {
    func testPersistsDistinctOriginProfilesAndDeduplicatesExactEndpoint() throws {
        let directory = try temporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let first = endpoint(origin: "http://192.168.1.8:8080")
        let second = endpoint(origin: "http://192.168.1.8:8081")
        let store = try RecentServers(directory: directory)

        try store.save(server: first)
        try store.save(server: second)
        try store.save(server: first)
        try store.setLanguage("ko")

        XCTAssertEqual(store.servers, [first, second])
        XCTAssertNotEqual(first.profileID, second.profileID)
        let reopened = try RecentServers(directory: directory)
        XCTAssertEqual(reopened.servers, [first, second])
        XCTAssertEqual(reopened.language, "ko")
    }

    func testRecentListIsCappedAtTwentyNewestEndpoints() throws {
        let directory = try temporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = try RecentServers(directory: directory)

        for index in 0..<21 {
            let id = String(format: "00000000-0000-4000-8000-%012x", index)
            try store.save(server: ServerEndpoint(id: id, name: "Server \(index)", version: "0.2.0", origin: "http://192.168.1.8:\(8_000 + index)"))
        }

        XCTAssertEqual(store.servers.count, 20)
        XCTAssertEqual(store.servers.first?.name, "Server 20")
        XCTAssertEqual(store.servers.last?.name, "Server 1")
    }

    func testFailedAtomicReplacementNeverChangesPublishedOrPersistedState() throws {
        let directory = try temporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let server = endpoint(origin: "http://192.168.1.8:8080")
        let store = try RecentServers(directory: directory)
        try store.save(server: server)
        let originalPermissions = try XCTUnwrap(
            try FileManager.default.attributesOfItem(atPath: directory.path)[.posixPermissions] as? NSNumber
        )

        try FileManager.default.setAttributes([.posixPermissions: NSNumber(value: 0o500)], ofItemAtPath: directory.path)
        defer {
            try? FileManager.default.setAttributes([.posixPermissions: originalPermissions], ofItemAtPath: directory.path)
        }
        XCTAssertThrowsError(try store.setLanguage("ko"))
        XCTAssertThrowsError(try store.remove(server: server))
        XCTAssertEqual(store.language, "en")
        XCTAssertEqual(store.servers, [server])

        try FileManager.default.setAttributes([.posixPermissions: originalPermissions], ofItemAtPath: directory.path)
        let reopened = try RecentServers(directory: directory)
        XCTAssertEqual(reopened.language, "en")
        XCTAssertEqual(reopened.servers, [server])
    }

    func testCorruptStorageFailsClosed() throws {
        let directory = try temporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        try Data("{\"version\":1,\"language\":\"en\",\"recents\":[{\"id\":\"not-a-uuid\"}]}".utf8)
            .write(to: directory.appendingPathComponent("client.json"))

        XCTAssertThrowsError(try RecentServers(directory: directory))
    }

    func testRejectedCredentialsCannotReplaceExistingState() throws {
        let directory = try temporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let existing = endpoint(origin: "https://server.local")
        let store = try RecentServers(directory: directory)
        try store.save(server: existing)
        let hostile = ServerEndpoint(id: existing.id, name: existing.name, version: existing.version, origin: "https://user:secret@server.local")

        XCTAssertThrowsError(try store.save(server: hostile))
        XCTAssertEqual(store.servers, [existing])
        XCTAssertEqual(try RecentServers(directory: directory).servers, [existing])
    }

    private func temporaryDirectory() throws -> URL {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("jastreamer-ios-tests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        return directory
    }

    private func endpoint(origin: String) -> ServerEndpoint {
        ServerEndpoint(
            id: "877f173c-d9c0-4eeb-8e49-887e2f044996",
            name: "Server",
            version: "0.2.0",
            origin: origin
        )
    }
}
