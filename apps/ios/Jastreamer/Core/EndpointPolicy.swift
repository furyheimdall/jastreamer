import Darwin
import Foundation

enum EndpointPolicy {
    private static let maximumEndpointLength = 2_048

    static func normalize(_ input: String) throws -> String {
        guard input.utf8.count <= maximumEndpointLength else {
            throw JastreamerClientError.invalidEndpoint
        }

        // Ordinary spaces around pasted input are harmless. Other whitespace,
        // control characters, and URL backslashes are rejected before parsing.
        let trimmed = input.trimmingCharacters(in: CharacterSet(charactersIn: " "))
        guard !trimmed.isEmpty,
              trimmed.unicodeScalars.allSatisfy({ scalar in
                  scalar != "\\" && !scalar.properties.isWhitespace && !isNonPrintable(scalar)
              })
        else {
            throw JastreamerClientError.invalidEndpoint
        }

        let scheme: String
        let remainder: Substring
        if let separator = trimmed.range(of: "://") {
            let rawScheme = String(trimmed[..<separator.lowerBound])
            guard isScheme(rawScheme) else {
                throw JastreamerClientError.invalidEndpoint
            }
            scheme = rawScheme.lowercased()
            remainder = trimmed[separator.upperBound...]
        } else {
            scheme = "http"
            remainder = trimmed[...]
        }
        guard scheme == "http" || scheme == "https" else {
            throw JastreamerClientError.invalidEndpoint
        }

        let boundary = remainder.firstIndex(where: { $0 == "/" || $0 == "?" || $0 == "#" })
        let authority = boundary.map { remainder[..<$0] } ?? remainder
        let suffix = boundary.map { remainder[$0...] } ?? Substring()
        guard suffix.isEmpty || suffix == "/",
              !authority.isEmpty,
              !authority.contains("@")
        else {
            throw JastreamerClientError.invalidEndpoint
        }

        let host: String
        let rawPort: Substring?
        if authority.first == "[" {
            guard let close = authority.firstIndex(of: "]") else {
                throw JastreamerClientError.invalidEndpoint
            }
            let rawHost = authority[authority.index(after: authority.startIndex)..<close]
            let tail = authority[authority.index(after: close)...]
            guard !rawHost.isEmpty, !rawHost.contains("%") else {
                throw JastreamerClientError.invalidEndpoint
            }
            if tail.isEmpty {
                rawPort = nil
            } else {
                guard tail.first == ":", tail.dropFirst().allSatisfy(\.isNumber), !tail.dropFirst().isEmpty else {
                    throw JastreamerClientError.invalidEndpoint
                }
                rawPort = tail.dropFirst()
            }
            host = try canonicalIPv6(String(rawHost))
        } else {
            guard !authority.contains("[") && !authority.contains("]") else {
                throw JastreamerClientError.invalidEndpoint
            }
            let colonCount = authority.reduce(into: 0) { count, character in
                if character == ":" { count += 1 }
            }
            guard colonCount <= 1 else {
                throw JastreamerClientError.invalidEndpoint
            }
            if let colon = authority.lastIndex(of: ":") {
                let port = authority[authority.index(after: colon)...]
                guard !port.isEmpty, port.allSatisfy(\.isNumber) else {
                    throw JastreamerClientError.invalidEndpoint
                }
                rawPort = port
                host = try canonicalHost(String(authority[..<colon]))
            } else {
                rawPort = nil
                host = try canonicalHost(String(authority))
            }
        }

        let port: Int
        if let rawPort {
            guard rawPort.allSatisfy({ $0 >= "0" && $0 <= "9" }),
                  let parsed = Int(rawPort),
                  (1...65_535).contains(parsed)
            else {
                throw JastreamerClientError.invalidEndpoint
            }
            port = parsed
        } else {
            port = scheme == "https" ? 443 : 80
        }

        let renderedHost = host.contains(":") ? "[\(host)]" : host
        let renderedPort = (scheme == "http" && port == 80) || (scheme == "https" && port == 443)
            ? ""
            : ":\(port)"
        return "\(scheme)://\(renderedHost)\(renderedPort)"
    }

    static func normalizeServerID(_ input: String) throws -> String {
        guard input.utf8.count == 36 else {
            throw JastreamerClientError.invalidMetadata
        }
        let lowercase = input.lowercased()
        for (index, character) in lowercase.enumerated() {
            if [8, 13, 18, 23].contains(index) {
                guard character == "-" else { throw JastreamerClientError.invalidMetadata }
            } else {
                guard character.isASCII, character.isHexDigit else {
                    throw JastreamerClientError.invalidMetadata
                }
            }
        }
        guard let uuid = UUID(uuidString: lowercase), uuid.uuidString.lowercased() == lowercase else {
            throw JastreamerClientError.invalidMetadata
        }
        return lowercase
    }

    static func sameOrigin(_ url: URL, origin: String) -> Bool {
        let raw = url.absoluteString
        guard raw.unicodeScalars.allSatisfy({ scalar in
            scalar != "\\" && !scalar.properties.isWhitespace && !isNonPrintable(scalar)
        }), let separator = raw.range(of: "://") else {
            return false
        }
        let scheme = raw[..<separator.lowerBound]
        guard isScheme(String(scheme)) else { return false }
        let remainder = raw[separator.upperBound...]
        let boundary = remainder.firstIndex(where: { $0 == "/" || $0 == "?" || $0 == "#" })
        let authority = boundary.map { remainder[..<$0] } ?? remainder
        guard !authority.isEmpty, !authority.contains("@") else { return false }

        do {
            let target = try normalize("\(scheme)://\(authority)")
            let expected = try normalizeRequiredOrigin(origin)
            return target == expected
        } catch {
            return false
        }
    }

    static func normalizeRequiredOrigin(_ input: String) throws -> String {
        guard input.range(of: "://") != nil else {
            throw JastreamerClientError.invalidEndpoint
        }
        return try normalize(input)
    }

    private static func canonicalHost(_ input: String) throws -> String {
        var host = input.lowercased()
        if host.hasSuffix(".") { host.removeLast() }
        guard !host.isEmpty, host.utf8.count <= 253, host.unicodeScalars.allSatisfy(\.isASCII) else {
            throw JastreamerClientError.invalidEndpoint
        }

        if host.allSatisfy({ $0.isNumber || $0 == "." }) {
            return try canonicalIPv4(host)
        }
        if looksLikeNonCanonicalNumericAddress(host) {
            throw JastreamerClientError.invalidEndpoint
        }

        let labels = host.split(separator: ".", omittingEmptySubsequences: false)
        guard labels.allSatisfy({ label in
            guard (1...63).contains(label.count), label.first != "-", label.last != "-" else { return false }
            return label.allSatisfy { character in
                character.isASCII && (character.isLetter || character.isNumber || character == "-")
            }
        }) else {
            throw JastreamerClientError.invalidEndpoint
        }
        return host
    }

    private static func canonicalIPv4(_ input: String) throws -> String {
        let parts = input.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { throw JastreamerClientError.invalidEndpoint }
        let octets = try parts.map { part -> Int in
            guard !part.isEmpty,
                  part.allSatisfy({ $0 >= "0" && $0 <= "9" }),
                  (part.count == 1 || part.first != "0"),
                  let value = Int(part),
                  (0...255).contains(value)
            else {
                throw JastreamerClientError.invalidEndpoint
            }
            return value
        }
        return octets.map(String.init).joined(separator: ".")
    }

    private static func canonicalIPv6(_ input: String) throws -> String {
        var address = in6_addr()
        guard input.withCString({ inet_pton(AF_INET6, $0, &address) }) == 1 else {
            throw JastreamerClientError.invalidEndpoint
        }
        var output = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
        let rendered = output.withUnsafeMutableBufferPointer { buffer -> String? in
            guard let base = buffer.baseAddress,
                  inet_ntop(AF_INET6, &address, base, socklen_t(buffer.count)) != nil
            else { return nil }
            return String(cString: base)
        }
        guard let rendered else { throw JastreamerClientError.invalidEndpoint }
        return rendered.lowercased()
    }

    private static func looksLikeNonCanonicalNumericAddress(_ host: String) -> Bool {
        let labels = host.split(separator: ".", omittingEmptySubsequences: false)
        guard !labels.isEmpty else { return false }
        return labels.allSatisfy { label in
            if label.allSatisfy(\.isNumber) { return true }
            return label.count > 2 && label.lowercased().hasPrefix("0x") && label.dropFirst(2).allSatisfy(\.isHexDigit)
        }
    }

    private static func isScheme(_ value: String) -> Bool {
        guard let first = value.first, first.isASCII && first.isLetter else { return false }
        return value.dropFirst().allSatisfy { character in
            character.isASCII && (character.isLetter || character.isNumber || character == "+" || character == "-" || character == ".")
        }
    }

    private static func isNonPrintable(_ scalar: Unicode.Scalar) -> Bool {
        switch scalar.properties.generalCategory {
        case .control, .format, .lineSeparator, .paragraphSeparator, .privateUse, .surrogate, .unassigned:
            return true
        default:
            return false
        }
    }
}
