import Foundation
import XCTest
@testable import Jastreamer

final class EndpointPolicyTests: XCTestCase {
    func testCanonicalizesOnlyCredentialFreeRootOrigins() throws {
        XCTAssertEqual(try EndpointPolicy.normalize("media-box.local:8080"), "http://media-box.local:8080")
        XCTAssertEqual(try EndpointPolicy.normalize(" HTTPS://MEDIA-BOX.LOCAL:443/ "), "https://media-box.local")
        XCTAssertEqual(
            try EndpointPolicy.normalize("http://[2001:0DB8:0:0:0:0:0:1]:80"),
            "http://[2001:db8::1]"
        )
    }

    func testRejectsHostileAndAmbiguousOriginInputs() {
        let rejected = [
            "ftp://media-box.local",
            "http://user:secret@media-box.local",
            "http://media-box.local/api",
            "http://media-box.local/a/../",
            "http://media-box.local?next=other",
            "http://media-box.local#section",
            "http://2001:db8::1",
            "http://bad_host.local",
            "http://127.1",
            "http://0177.0.0.1",
            "http://0x7f000001",
            "http://0x7f.0.0.1",
            "http://2130706433",
            "http://[fe80::1%en0]",
            "http://[fe80::1%25en0]",
            "http://media-box.local\n",
            "http://media-box.local\\@other.local",
            "http://media-box.local:0",
            "http://media-box.local:65536",
        ]
        for input in rejected {
            XCTAssertThrowsError(try EndpointPolicy.normalize(input), "Accepted hostile input: \(input)")
        }
    }

    func testSameOriginIncludesSchemeHostAndEffectivePort() throws {
        XCTAssertTrue(
            EndpointPolicy.sameOrigin(
                try XCTUnwrap(URL(string: "https://MEDIA-BOX.local:443/library/42?view=grid")),
                origin: "https://media-box.local"
            )
        )
        XCTAssertFalse(EndpointPolicy.sameOrigin(try XCTUnwrap(URL(string: "http://media-box.local/library")), origin: "https://media-box.local"))
        XCTAssertFalse(EndpointPolicy.sameOrigin(try XCTUnwrap(URL(string: "https://media-box.local:8443/library")), origin: "https://media-box.local"))
        XCTAssertFalse(EndpointPolicy.sameOrigin(try XCTUnwrap(URL(string: "https://media-box.local.evil/library")), origin: "https://media-box.local"))
        XCTAssertFalse(EndpointPolicy.sameOrigin(try XCTUnwrap(URL(string: "https://user@media-box.local/library")), origin: "https://media-box.local"))
    }

    func testProfileIdentifierBindsCanonicalIdentityAndOrigin() throws {
        let server = ServerEndpoint(
            id: "11111111-1111-4111-8111-111111111111",
            name: "Living room",
            version: "0.2.0",
            origin: "https://MEDIA-BOX.LOCAL:443/"
        )
        let equivalent = ServerEndpoint(id: server.id, name: server.name, version: server.version, origin: "https://media-box.local")
        let otherIdentity = ServerEndpoint(id: "22222222-2222-4222-8222-222222222222", name: server.name, version: server.version, origin: server.origin)
        let otherPort = ServerEndpoint(id: server.id, name: server.name, version: server.version, origin: "https://media-box.local:8443")

        XCTAssertEqual(server.profileID, equivalent.profileID)
        XCTAssertNotEqual(server.profileID, otherIdentity.profileID)
        XCTAssertNotEqual(server.profileID, otherPort.profileID)
        XCTAssertNotEqual(server.profileID, UUID(uuid: (0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)))
    }
}
