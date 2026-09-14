import Foundation

struct L10n {
    let language: String

    init(_ language: String) {
        self.language = language == "ko" ? "ko" : "en"
    }

    func callAsFunction(_ key: String) -> String {
        guard
            let path = Bundle.main.path(forResource: language, ofType: "lproj"),
            let bundle = Bundle(path: path)
        else {
            return key
        }
        return bundle.localizedString(forKey: key, value: nil, table: nil)
    }

    func errorMessage(_ error: Error) -> String {
        guard let clientError = error as? JastreamerClientError else {
            return self("error.generic")
        }
        switch clientError {
        case .invalidEndpoint:
            return self("error.endpoint")
        case .invalidMetadata:
            return self("error.metadata")
        case .incompatibleServer:
            return self("error.incompatible")
        case .identityMismatch:
            return self("error.identity")
        case .redirect:
            return self("error.redirect")
        case let .httpStatus(status):
            return "\(self("error.http")) \(status)"
        case .timeout:
            return self("error.timeout")
        case .tls:
            return self("error.tls")
        case .unreachable:
            return self("error.unreachable")
        case .storage:
            return self("error.storage")
        case .discovery:
            return self("discovery.error")
        }
    }

    static var bootstrapLanguage: String {
        Locale.preferredLanguages.first?.lowercased().hasPrefix("ko") == true ? "ko" : "en"
    }
}
