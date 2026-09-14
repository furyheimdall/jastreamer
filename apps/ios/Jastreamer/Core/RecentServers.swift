import Combine
import Foundation

@MainActor
final class RecentServers: ObservableObject {
    @Published private(set) var servers: [ServerEndpoint]
    @Published private(set) var language: String

    private let directory: URL
    private let file: URL
    private let fileManager: FileManager

    init(directory: URL? = nil) throws {
        let fileManager = FileManager.default
        let storageDirectory: URL
        if let directory {
            storageDirectory = directory
        } else {
            guard let applicationSupport = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
                throw JastreamerClientError.storage
            }
            storageDirectory = applicationSupport.appendingPathComponent("Jastreamer", isDirectory: true)
        }

        do {
            try fileManager.createDirectory(at: storageDirectory, withIntermediateDirectories: true)
        } catch {
            throw JastreamerClientError.storage
        }
        try Self.requireWritableDirectory(storageDirectory, fileManager: fileManager)


        self.directory = storageDirectory
        file = storageDirectory.appendingPathComponent("client.json", isDirectory: false)
        self.fileManager = fileManager

        let snapshot = try Self.load(from: file, fileManager: fileManager)
        servers = snapshot.recents
        language = snapshot.language
    }

    func save(server: ServerEndpoint) throws {
        let normalized = try Self.validate(server)
        var next = servers.filter { $0.id != normalized.id || $0.origin != normalized.origin }
        next.insert(normalized, at: 0)
        if next.count > 20 { next.removeLast(next.count - 20) }

        try commit(Snapshot(version: 1, language: language, recents: next))
        servers = next
    }

    func remove(server: ServerEndpoint) throws {
        let normalized = try Self.validate(server)
        let next = servers.filter { $0.id != normalized.id || $0.origin != normalized.origin }
        guard next != servers else { return }

        try commit(Snapshot(version: 1, language: language, recents: next))
        servers = next
    }

    func setLanguage(_ value: String) throws {
        guard value == "en" || value == "ko" else {
            throw JastreamerClientError.storage
        }
        guard value != language else { return }

        try commit(Snapshot(version: 1, language: value, recents: servers))
        language = value
    }

    private func commit(_ snapshot: Snapshot) throws {
        let data: Data
        do {
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.sortedKeys]
            data = try encoder.encode(snapshot)
        } catch {
            throw JastreamerClientError.storage
        }
        guard data.count <= 65_536 else { throw JastreamerClientError.storage }

        let temporary = directory.appendingPathComponent("client-\(UUID().uuidString).tmp", isDirectory: false)
        do {
            guard fileManager.createFile(atPath: temporary.path, contents: nil) else {
                throw JastreamerClientError.storage
            }
            let handle = try FileHandle(forWritingTo: temporary)
            do {
                try handle.write(contentsOf: data)
                try handle.synchronize()
                try handle.close()
            } catch {
                try? handle.close()
                throw error
            }

            if fileManager.fileExists(atPath: file.path) {
                _ = try fileManager.replaceItemAt(file, withItemAt: temporary)
            } else {
                try fileManager.moveItem(at: temporary, to: file)
            }
        } catch {
            try? fileManager.removeItem(at: temporary)
            throw JastreamerClientError.storage
        }
    }

    private static func load(from file: URL, fileManager: FileManager) throws -> Snapshot {
        guard fileManager.fileExists(atPath: file.path) else {
            return Snapshot(version: 1, language: "en", recents: [])
        }

        do {
            let values = try file.resourceValues(forKeys: [.fileSizeKey, .isRegularFileKey])
            guard values.isRegularFile == true, let size = values.fileSize, size <= 65_536 else {
                throw JastreamerClientError.storage
            }
            let data = try Data(contentsOf: file)
            guard data.count <= 65_536 else { throw JastreamerClientError.storage }
            let decoded = try JSONDecoder().decode(Snapshot.self, from: data)
            guard decoded.version == 1, decoded.language == "en" || decoded.language == "ko" else {
                throw JastreamerClientError.storage
            }

            var seen = Set<ServerKey>()
            var recents: [ServerEndpoint] = []
            for entry in decoded.recents {
                let server = try validate(entry)
                let key = ServerKey(id: server.id, origin: server.origin)
                if seen.insert(key).inserted, recents.count < 20 {
                    recents.append(server)
                }
            }
            return Snapshot(version: 1, language: decoded.language, recents: recents)
        } catch let error as JastreamerClientError {
            throw error
        } catch {
            throw JastreamerClientError.storage
        }
    }

    private static func validate(_ server: ServerEndpoint) throws -> ServerEndpoint {
        let id: String
        let origin: String
        do {
            id = try EndpointPolicy.normalizeServerID(server.id)
            origin = try EndpointPolicy.normalizeRequiredOrigin(server.origin)
        } catch {
            throw JastreamerClientError.invalidMetadata
        }
        guard let name = MetadataPolicy.cleanDisplayText(server.name, maximum: 128),
              let version = MetadataPolicy.cleanDisplayText(server.version, maximum: 64)
        else {
            throw JastreamerClientError.invalidMetadata
        }
        return ServerEndpoint(id: id, name: name, version: version, origin: origin)
    }

    private static func requireWritableDirectory(_ directory: URL, fileManager: FileManager) throws {
        let probe = directory.appendingPathComponent(".write-\(UUID().uuidString)", isDirectory: false)
        guard fileManager.createFile(atPath: probe.path, contents: Data()) else {
            throw JastreamerClientError.storage
        }
        do {
            try fileManager.removeItem(at: probe)
        } catch {
            try? fileManager.removeItem(at: probe)
            throw JastreamerClientError.storage
        }
    }

}

private struct Snapshot: Codable {
    let version: Int
    let language: String
    let recents: [ServerEndpoint]
}

private struct ServerKey: Hashable {
    let id: String
    let origin: String
}
