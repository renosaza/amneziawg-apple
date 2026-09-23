// SPDX-License-Identifier: MIT

#if os(macOS)
import Darwin
import Foundation
import Security

enum DaemonProfileStoreError: Error, Equatable {
    case invalidName
    case invalidConfiguration
    case profileNotFound
    case secretMissing
    case insecureMetadata
    case metadataTooLarge
    case metadataCorrupt
    case fileOperationFailed
    case keychainOperationFailed(OSStatus)
    case rollbackFailed
}

struct DaemonProfileMetadata: Codable, Equatable, Hashable {
    let id: UUID
    var name: String
}

struct DaemonProfileSecretStore {
    let read: (UUID) throws -> Data?
    let write: (UUID, Data) throws -> Void
    let delete: (UUID) throws -> Void

    static let loginKeychain = DaemonProfileSecretStore(
        read: LoginKeychain.read,
        write: LoginKeychain.write,
        delete: LoginKeychain.delete
    )
}

final class DaemonProfileStore {
    private struct Document: Codable {
        var profiles: [DaemonProfileMetadata]
    }

    private static let maximumMetadataBytes = 64 * 1024

    private let directory: URL
    private let metadataURL: URL
    private let secrets: DaemonProfileSecretStore
    private let lock = NSLock()

    convenience init() throws {
        let supportDirectory = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )
        try self.init(
            directory: supportDirectory.appendingPathComponent("com.amneziawg.daemon-profile-store", isDirectory: true),
            secrets: .loginKeychain
        )
    }

    init(directory: URL, secrets: DaemonProfileSecretStore) throws {
        self.directory = directory
        self.metadataURL = directory.appendingPathComponent("profiles.json", isDirectory: false)
        self.secrets = secrets
        try ensureDirectory()
    }

    func profiles() throws -> [DaemonProfileMetadata] {
        lock.lock()
        defer { lock.unlock() }
        return try readDocument().profiles
    }

    func configuration(for id: UUID) throws -> TunnelConfiguration {
        lock.lock()
        defer { lock.unlock() }

        guard let metadata = try readDocument().profiles.first(where: { $0.id == id }) else {
            throw DaemonProfileStoreError.profileNotFound
        }
        guard var secret = try secrets.read(id) else {
            throw DaemonProfileStoreError.secretMissing
        }
        defer { secret.resetBytes(in: 0..<secret.count) }
        guard let configuration = String(data: secret, encoding: .utf8) else {
            throw DaemonProfileStoreError.invalidConfiguration
        }
        return try validatedConfiguration(configuration, name: metadata.name)
    }

    func save(id: UUID, name: String, wgQuickConfig: String) throws {
        let validName = try validatedName(name)
        _ = try validatedConfiguration(wgQuickConfig, name: validName)
        guard var secret = wgQuickConfig.data(using: .utf8) else {
            throw DaemonProfileStoreError.invalidConfiguration
        }
        defer { secret.resetBytes(in: 0..<secret.count) }

        lock.lock()
        defer { lock.unlock() }

        let oldDocument = try readDocument()
        var oldSecret = try secrets.read(id)
        defer {
            if oldSecret != nil {
                let count = oldSecret!.count
                oldSecret!.resetBytes(in: 0..<count)
            }
        }
        var newDocument = oldDocument
        if let index = newDocument.profiles.firstIndex(where: { $0.id == id }) {
            newDocument.profiles[index].name = validName
        } else {
            newDocument.profiles.append(DaemonProfileMetadata(id: id, name: validName))
        }

        try secrets.write(id, secret)
        do {
            try writeDocument(newDocument)
        } catch {
            do {
                try restoreSecret(oldSecret, for: id)
            } catch {
                throw DaemonProfileStoreError.rollbackFailed
            }
            throw error
        }
    }

    func rename(id: UUID, to name: String) throws {
        let validName = try validatedName(name)

        lock.lock()
        defer { lock.unlock() }

        var document = try readDocument()
        guard let index = document.profiles.firstIndex(where: { $0.id == id }) else {
            throw DaemonProfileStoreError.profileNotFound
        }
        document.profiles[index].name = validName
        try writeDocument(document)
    }

    func delete(id: UUID) throws {
        lock.lock()
        defer { lock.unlock() }

        let oldDocument = try readDocument()
        guard oldDocument.profiles.contains(where: { $0.id == id }) else {
            throw DaemonProfileStoreError.profileNotFound
        }
        guard var oldSecret = try secrets.read(id) else {
            throw DaemonProfileStoreError.secretMissing
        }
        defer { oldSecret.resetBytes(in: 0..<oldSecret.count) }
        var newDocument = oldDocument
        newDocument.profiles.removeAll { $0.id == id }

        try secrets.delete(id)
        do {
            try writeDocument(newDocument)
        } catch {
            do {
                try secrets.write(id, oldSecret)
            } catch {
                throw DaemonProfileStoreError.rollbackFailed
            }
            throw error
        }
    }

    private func ensureDirectory() throws {
        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        var info = stat()
        guard lstat(directory.path, &info) == 0,
              (info.st_mode & S_IFMT) == S_IFDIR,
              info.st_uid == getuid(),
              info.st_mode & 0o077 == 0
        else {
            throw DaemonProfileStoreError.insecureMetadata
        }
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
    }

    private func readDocument() throws -> Document {
        guard FileManager.default.fileExists(atPath: metadataURL.path) else {
            return Document(profiles: [])
        }

        var info = stat()
        guard lstat(metadataURL.path, &info) == 0,
              (info.st_mode & S_IFMT) == S_IFREG,
              info.st_uid == getuid(),
              info.st_mode & 0o077 == 0
        else {
            throw DaemonProfileStoreError.insecureMetadata
        }
        let data = try Data(contentsOf: metadataURL, options: .mappedIfSafe)
        guard data.count <= Self.maximumMetadataBytes else {
            throw DaemonProfileStoreError.metadataTooLarge
        }
        guard let document = try? JSONDecoder().decode(Document.self, from: data),
              Set(document.profiles.map(\.id)).count == document.profiles.count,
              document.profiles.allSatisfy({ (try? validatedName($0.name)) != nil })
        else {
            throw DaemonProfileStoreError.metadataCorrupt
        }
        return document
    }

    private func writeDocument(_ document: Document) throws {
        let data = try JSONEncoder().encode(document)
        let temporaryURL = directory.appendingPathComponent(".profiles-\(UUID().uuidString).tmp", isDirectory: false)
        defer { try? FileManager.default.removeItem(at: temporaryURL) }

        let descriptor = open(temporaryURL.path, O_WRONLY | O_CREAT | O_EXCL, mode_t(0o600))
        guard descriptor >= 0 else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
        let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
        do {
            try handle.write(contentsOf: data)
            try handle.synchronize()
            try handle.close()
        } catch {
            try? handle.close()
            throw DaemonProfileStoreError.fileOperationFailed
        }
        guard chmod(temporaryURL.path, mode_t(0o600)) == 0,
              Darwin.rename(temporaryURL.path, metadataURL.path) == 0
        else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
    }

    private func validatedName(_ name: String) throws -> String {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !name.contains("\0") else {
            throw DaemonProfileStoreError.invalidName
        }
        return name
    }

    private func validatedConfiguration(_ configuration: String, name: String) throws -> TunnelConfiguration {
        guard let parsed = try? TunnelConfiguration(fromWgQuickConfig: configuration, called: name) else {
            throw DaemonProfileStoreError.invalidConfiguration
        }
        return parsed
    }

    private func restoreSecret(_ secret: Data?, for id: UUID) throws {
        if let secret {
            try secrets.write(id, secret)
        } else {
            try secrets.delete(id)
        }
    }
}

private enum LoginKeychain {
    private static let service = "com.amneziawg.daemon-profile-store"

    static func read(id: UUID) throws -> Data? {
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query(for: id, returningData: true) as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
        return data
    }

    static func write(id: UUID, data: Data) throws {
        let status = SecItemUpdate(
            query(for: id, returningData: false) as CFDictionary,
            [kSecValueData: data] as CFDictionary
        )
        if status == errSecSuccess {
            return
        }
        guard status == errSecItemNotFound else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
        var attributes = query(for: id, returningData: false)
        attributes[kSecValueData] = data
        attributes[kSecAttrAccessible] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let addStatus = SecItemAdd(attributes as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw DaemonProfileStoreError.keychainOperationFailed(addStatus)
        }
    }

    static func delete(id: UUID) throws {
        let status = SecItemDelete(query(for: id, returningData: false) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
    }

    private static func query(for id: UUID, returningData: Bool) -> [CFString: Any] {
        var query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: id.uuidString.lowercased(),
            kSecAttrSynchronizable: false
        ]
        if returningData {
            query[kSecMatchLimit] = kSecMatchLimitOne
            query[kSecReturnData] = true
        }
        return query
    }
}
#endif
