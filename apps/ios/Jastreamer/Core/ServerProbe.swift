import Foundation

final class ServerProbe {
    typealias Loader = (URLRequest) async throws -> (HTTPURLResponse, Data)

    private let loader: Loader

    init(configuration: URLSessionConfiguration = .ephemeral) {
        let isolated = URLSessionConfiguration.ephemeral
        isolated.protocolClasses = configuration.protocolClasses
        isolated.urlCache = nil
        isolated.requestCachePolicy = .reloadIgnoringLocalCacheData
        isolated.httpCookieStorage = nil
        isolated.urlCredentialStorage = nil
        isolated.httpShouldSetCookies = false
        isolated.httpCookieAcceptPolicy = .never
        isolated.timeoutIntervalForRequest = 4
        isolated.timeoutIntervalForResource = 4
        isolated.waitsForConnectivity = false
        loader = { request in
            try await ProbeTransaction(configuration: isolated).perform(request)
        }
    }

    init(loader: @escaping Loader) {
        self.loader = loader
    }

    func probe(_ input: String, expectedID: String? = nil) async throws -> ServerEndpoint {
        let origin = try EndpointPolicy.normalize(input)
        let requiredID = try expectedID.map(EndpointPolicy.normalizeServerID)
        guard let url = URL(string: origin + "/api/v1/discovery") else {
            throw JastreamerClientError.invalidEndpoint
        }

        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 4)
        request.httpMethod = "GET"
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")

        let response: HTTPURLResponse
        let body: Data
        do {
            (response, body) = try await loader(request)
        } catch is CancellationError {
            throw CancellationError()
        } catch let error as JastreamerClientError {
            throw error
        } catch let error as URLError {
            throw classify(error)
        } catch {
            throw JastreamerClientError.unreachable
        }

        if (300...399).contains(response.statusCode) {
            throw JastreamerClientError.redirect
        }
        guard (200...299).contains(response.statusCode) else {
            throw JastreamerClientError.httpStatus(response.statusCode)
        }
        if let contentType = response.value(forHTTPHeaderField: "Content-Type"),
           !contentType.lowercased().contains("application/json") {
            throw JastreamerClientError.invalidMetadata
        }

        let document: DiscoveryDocument
        do {
            document = try JSONDecoder().decode(DiscoveryDocument.self, from: body)
        } catch {
            throw JastreamerClientError.invalidMetadata
        }
        guard document.product == "jastreamer", document.protocol == 1 else {
            throw JastreamerClientError.incompatibleServer
        }

        let id: String
        do {
            id = try EndpointPolicy.normalizeServerID(document.id)
        } catch {
            throw JastreamerClientError.invalidMetadata
        }
        guard let name = MetadataPolicy.cleanDisplayText(document.name, maximum: 128),
              let version = MetadataPolicy.cleanDisplayText(document.version, maximum: 64)
        else {
            throw JastreamerClientError.invalidMetadata
        }
        if let requiredID, id != requiredID {
            throw JastreamerClientError.identityMismatch
        }
        return ServerEndpoint(id: id, name: name, version: version, origin: origin)
    }

    private func classify(_ error: URLError) -> JastreamerClientError {
        switch error.code {
        case .timedOut:
            return .timeout
        case .secureConnectionFailed,
             .serverCertificateHasBadDate,
             .serverCertificateUntrusted,
             .serverCertificateHasUnknownRoot,
             .serverCertificateNotYetValid,
             .clientCertificateRejected,
             .clientCertificateRequired:
            return .tls
        default:
            return .unreachable
        }
    }

}

private struct DiscoveryDocument: Decodable {
    let product: String
    let `protocol`: Int
    let id: String
    let name: String
    let version: String
}

private final class ProbeTransaction: NSObject, URLSessionDataDelegate, URLSessionTaskDelegate {
    typealias Result = (HTTPURLResponse, Data)

    private let configuration: URLSessionConfiguration
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Result, Error>?
    private var session: URLSession?
    private var task: URLSessionDataTask?
    private var response: HTTPURLResponse?
    private var body = Data()
    private var completed = false
    private var cancelledByCaller = false

    init(configuration: URLSessionConfiguration) {
        self.configuration = configuration
    }

    func perform(_ request: URLRequest) async throws -> Result {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                lock.lock()
                if completed {
                    lock.unlock()
                    continuation.resume(throwing: CancellationError())
                    return
                }
                self.continuation = continuation
                let session = URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
                let task = session.dataTask(with: request)
                self.session = session
                self.task = task
                let cancelled = cancelledByCaller || Task.isCancelled
                lock.unlock()

                if cancelled {
                    cancel()
                } else {
                    task.resume()
                }
            }
        } onCancel: {
            self.cancel()
        }
    }

    func cancel() {
        lock.lock()
        cancelledByCaller = true
        let task = task
        lock.unlock()
        task?.cancel()
        finish(.failure(CancellationError()))
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        lock.lock()
        self.response = response
        lock.unlock()
        completionHandler(nil)
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
    ) {
        guard let response = response as? HTTPURLResponse else {
            completionHandler(.cancel)
            finish(.failure(JastreamerClientError.invalidMetadata))
            return
        }
        if response.expectedContentLength > 32 * 1_024 {
            completionHandler(.cancel)
            finish(.failure(JastreamerClientError.invalidMetadata))
            return
        }
        lock.lock()
        self.response = response
        lock.unlock()
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        guard !completed else {
            lock.unlock()
            return
        }
        if body.count > 32 * 1_024 - data.count {
            lock.unlock()
            finish(.failure(JastreamerClientError.invalidMetadata))
            return
        }
        body.append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        lock.lock()
        let cancelled = cancelledByCaller
        let response = response
        let body = body
        lock.unlock()

        if cancelled {
            finish(.failure(CancellationError()))
        } else if let error {
            finish(.failure(error))
        } else if let response {
            finish(.success((response, body)))
        } else {
            finish(.failure(JastreamerClientError.invalidMetadata))
        }
    }

    private func finish(_ result: Swift.Result<Result, Error>) {
        lock.lock()
        guard !completed else {
            lock.unlock()
            return
        }
        completed = true
        let continuation = continuation
        self.continuation = nil
        let session = session
        self.session = nil
        task = nil
        lock.unlock()

        session?.invalidateAndCancel()
        continuation?.resume(with: result)
    }
}
