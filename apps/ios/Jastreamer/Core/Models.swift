import CryptoKit
import Foundation

enum JastreamerClientError: LocalizedError {
    case invalidEndpoint
    case invalidMetadata
    case incompatibleServer
    case identityMismatch
    case redirect
    case httpStatus(Int)
    case timeout
    case tls
    case unreachable
    case storage
    case discovery

    var errorDescription: String? {
        switch self {
        case .invalidEndpoint:
            return "Enter a valid HTTP or HTTPS server address."
        case .invalidMetadata:
            return "The Server returned invalid discovery information."
        case .incompatibleServer:
            return "This Server uses an unsupported discovery protocol."
        case .identityMismatch:
            return "The Server identity changed."
        case .redirect:
            return "Server discovery redirects are not allowed."
        case let .httpStatus(status):
            return "Server discovery failed with HTTP status \(status)."
        case .timeout:
            return "The Server did not respond in time."
        case .tls:
            return "The Server certificate could not be verified."
        case .unreachable:
            return "The Server is unreachable."
        case .storage:
            return "Client preferences could not be read or saved."
        case .discovery:
            return "LAN Server discovery is unavailable. Check Local Network access."
        }
    }
}
enum MetadataPolicy {
    static func cleanDisplayText(_ value: String, maximum: Int) -> String? {
        let text = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, text.count <= maximum else { return nil }
        for scalar in text.unicodeScalars {
            switch scalar.properties.generalCategory {
            case .control, .format, .lineSeparator, .paragraphSeparator, .privateUse, .surrogate, .unassigned:
                return nil
            default:
                continue
            }
        }
        return text
    }
}


struct ServerEndpoint: Codable, Equatable, Hashable, Sendable {
    let id: String
    let name: String
    let version: String
    let origin: String

    var profileID: UUID {
        let normalizedID = (try? EndpointPolicy.normalizeServerID(id)) ?? "invalid-id:\(id)"
        let normalizedOrigin = (try? EndpointPolicy.normalize(origin)) ?? "invalid-origin:\(origin)"
        let digest = SHA256.hash(data: Data("\(normalizedID)\u{0}\(normalizedOrigin)".utf8))
        var bytes = Array(digest.prefix(16))

        // Give the deterministic identifier an RFC 4122 variant and a name-based
        // version. This is a storage namespace, not a Server or user identity.
        bytes[6] = (bytes[6] & 0x0f) | 0x50
        bytes[8] = (bytes[8] & 0x3f) | 0x80

        let value: uuid_t = (
            bytes[0], bytes[1], bytes[2], bytes[3],
            bytes[4], bytes[5], bytes[6], bytes[7],
            bytes[8], bytes[9], bytes[10], bytes[11],
            bytes[12], bytes[13], bytes[14], bytes[15]
        )
        return UUID(uuid: value)
    }
}
