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

enum DaemonProfileSecretVersion: Equatable {
    case active
    case pending
}

struct DaemonProfileSecretStore {
    let read: (UUID, DaemonProfileSecretVersion) throws -> Data?
    let write: (UUID, DaemonProfileSecretVersion, Data) throws -> Void
    let delete: (UUID, DaemonProfileSecretVersion) throws -> Void

    static func loginKeychain(service: String = LoginKeychain.defaultService) -> DaemonProfileSecretStore {
        DaemonProfileSecretStore(
            read: { try LoginKeychain.read(id: $0, version: $1, service: service) },
            write: { try LoginKeychain.write(id: $0, version: $1, data: $2, service: service) },
            delete: { try LoginKeychain.delete(id: $0, version: $1, service: service) }
        )
    }
}

final class DaemonProfileStore {
    private struct Document: Codable {
        var profiles: [DaemonProfileMetadata]
    }

    private struct Transaction: Codable {
        enum Operation: String, Codable {
            case save
            case delete
        }

        enum Phase: String, Codable {
            case prepared
            case staged
        }

        let operation: Operation
        let phase: Phase
        let id: UUID
        let name: String?
    }

    private static let maximumMetadataBytes = 64 * 1024
    private static let maximumJournalBytes = 4 * 1024

    private let directory: URL
    private let metadataURL: URL
    private let journalURL: URL
    private let lockURL: URL
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
            secrets: .loginKeychain()
        )
    }

    init(directory: URL, secrets: DaemonProfileSecretStore) throws {
        self.directory = directory
        self.metadataURL = directory.appendingPathComponent("profiles.json", isDirectory: false)
        self.journalURL = directory.appendingPathComponent("transaction.json", isDirectory: false)
        self.lockURL = directory.appendingPathComponent(".lock", isDirectory: false)
        self.secrets = secrets
        try ensureDirectory()
    }

    func profiles() throws -> [DaemonProfileMetadata] {
        try withLock {
            try readDocument().profiles
        }
    }

    func configuration(for id: UUID) throws -> TunnelConfiguration {
        try withLock {
            guard let metadata = try readDocument().profiles.first(where: { $0.id == id }) else {
                throw DaemonProfileStoreError.profileNotFound
            }
            guard var secret = try secrets.read(id, .active) else {
                throw DaemonProfileStoreError.secretMissing
            }
            defer { secret.resetBytes(in: 0..<secret.count) }
            guard let configuration = String(data: secret, encoding: .utf8) else {
                throw DaemonProfileStoreError.invalidConfiguration
            }
            return try validatedConfiguration(configuration, name: metadata.name)
        }
    }

    func save(id: UUID, name: String, wgQuickConfig: String) throws {
        let validName = try validatedName(name)
        _ = try validatedConfiguration(wgQuickConfig, name: validName)
        guard var secret = wgQuickConfig.data(using: .utf8) else {
            throw DaemonProfileStoreError.invalidConfiguration
        }
        defer { secret.resetBytes(in: 0..<secret.count) }

        try withLock {
            let oldDocument = try readDocument()
            var newDocument = oldDocument
            if let index = newDocument.profiles.firstIndex(where: { $0.id == id }) {
                newDocument.profiles[index].name = validName
            } else {
                newDocument.profiles.append(DaemonProfileMetadata(id: id, name: validName))
            }
            let transaction = Transaction(operation: .save, phase: .prepared, id: id, name: validName)
            try writeJournal(transaction)
            var metadataCommitted = false
            do {
                try secrets.write(id, .pending, secret)
                try writeJournal(Transaction(operation: .save, phase: .staged, id: id, name: validName))
                try writeDocument(newDocument)
                metadataCommitted = true
                try promoteSave(id: id)
                try removeJournal()
            } catch {
                if metadataCommitted {
                    throw error
                }
                do {
                    try writeDocument(oldDocument)
                    try secrets.delete(id, .pending)
                    try removeJournal()
                } catch {
                    // The staged secret and journal permit a later fail-closed recovery.
                    throw DaemonProfileStoreError.rollbackFailed
                }
                throw error
            }
        }
    }

    func rename(id: UUID, to name: String) throws {
        let validName = try validatedName(name)

        try withLock {
            var document = try readDocument()
            guard let index = document.profiles.firstIndex(where: { $0.id == id }) else {
                throw DaemonProfileStoreError.profileNotFound
            }
            document.profiles[index].name = validName
            try writeDocument(document)
        }
    }

    func delete(id: UUID) throws {
        try withLock {
            let oldDocument = try readDocument()
            guard oldDocument.profiles.contains(where: { $0.id == id }) else {
                throw DaemonProfileStoreError.profileNotFound
            }
            var newDocument = oldDocument
            newDocument.profiles.removeAll { $0.id == id }
            try writeJournal(Transaction(operation: .delete, phase: .prepared, id: id, name: nil))
            var metadataCommitted = false
            do {
                try writeDocument(newDocument)
                metadataCommitted = true
                try secrets.delete(id, .active)
                try secrets.delete(id, .pending)
                try removeJournal()
            } catch {
                if metadataCommitted {
                    throw error
                }
                do {
                    try writeDocument(oldDocument)
                    try removeJournal()
                } catch {
                    // The journal permits a later fail-closed deletion recovery.
                    throw DaemonProfileStoreError.rollbackFailed
                }
                throw error
            }
        }
    }

    private func withLock<T>(_ body: () throws -> T) throws -> T {
        lock.lock()
        defer { lock.unlock() }

        let descriptor = open(lockURL.path, O_RDWR | O_CREAT | O_NOFOLLOW | O_CLOEXEC, mode_t(0o600))
        guard descriptor >= 0 else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
        defer { _ = Darwin.close(descriptor) }
        try validateProtectedDescriptor(descriptor, expectedMode: 0o600)
        guard flock(descriptor, LOCK_EX) == 0 else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
        defer { _ = flock(descriptor, LOCK_UN) }

        try recoverTransaction()
        return try body()
    }

    private func recoverTransaction() throws {
        guard let transaction = try readJournal() else { return }
        switch transaction.operation {
        case .save:
            try recoverSave(transaction)
        case .delete:
            try recoverDelete(transaction)
        }
    }

    private func recoverSave(_ transaction: Transaction) throws {
        guard let name = transaction.name else {
            throw DaemonProfileStoreError.metadataCorrupt
        }
        let pending = try secrets.read(transaction.id, .pending)
        switch transaction.phase {
        case .prepared:
            guard pending != nil else {
                try removeJournal()
                return
            }
            try writeJournal(Transaction(operation: .save, phase: .staged, id: transaction.id, name: name))
            fallthrough
        case .staged:
            if var pending = pending {
                defer { pending.resetBytes(in: 0..<pending.count) }
                var document = try readDocument()
                if let index = document.profiles.firstIndex(where: { $0.id == transaction.id }) {
                    document.profiles[index].name = name
                } else {
                    document.profiles.append(DaemonProfileMetadata(id: transaction.id, name: name))
                }
                try writeDocument(document)
                try secrets.write(transaction.id, .active, pending)
                try secrets.delete(transaction.id, .pending)
                try removeJournal()
            } else {
                let document = try readDocument()
                guard document.profiles.contains(DaemonProfileMetadata(id: transaction.id, name: name)),
                      try secrets.read(transaction.id, .active) != nil
                else {
                    throw DaemonProfileStoreError.metadataCorrupt
                }
                try removeJournal()
            }
        }
    }

    private func recoverDelete(_ transaction: Transaction) throws {
        let document = try readDocument()
        guard !document.profiles.contains(where: { $0.id == transaction.id }) else {
            try removeJournal()
            return
        }
        try secrets.delete(transaction.id, .active)
        try secrets.delete(transaction.id, .pending)
        try removeJournal()
    }

    private func promoteSave(id: UUID) throws {
        guard var pending = try secrets.read(id, .pending) else {
            throw DaemonProfileStoreError.secretMissing
        }
        defer { pending.resetBytes(in: 0..<pending.count) }
        try secrets.write(id, .active, pending)
        try secrets.delete(id, .pending)
    }

    private func ensureDirectory() throws {
        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        let descriptor = open(directory.path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
        guard descriptor >= 0 else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
        defer { _ = Darwin.close(descriptor) }
        try validateDirectoryDescriptor(descriptor)
        guard fchmod(descriptor, mode_t(0o700)) == 0 else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
    }

    private func readDocument() throws -> Document {
        guard let data = try readProtectedFile(at: metadataURL, maximumBytes: Self.maximumMetadataBytes) else {
            return Document(profiles: [])
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
        try writeProtectedFile(try JSONEncoder().encode(document), to: metadataURL)
    }

    private func readJournal() throws -> Transaction? {
        guard let data = try readProtectedFile(at: journalURL, maximumBytes: Self.maximumJournalBytes) else {
            return nil
        }
        guard let transaction = try? JSONDecoder().decode(Transaction.self, from: data) else {
            throw DaemonProfileStoreError.metadataCorrupt
        }
        return transaction
    }

    private func writeJournal(_ transaction: Transaction) throws {
        try writeProtectedFile(try JSONEncoder().encode(transaction), to: journalURL)
    }

    private func removeJournal() throws {
        try removeProtectedFile(at: journalURL)
    }

    private func readProtectedFile(at url: URL, maximumBytes: Int) throws -> Data? {
        let descriptor = open(url.path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC)
        if descriptor < 0 {
            if errno == ENOENT { return nil }
            throw DaemonProfileStoreError.fileOperationFailed
        }
        defer { _ = Darwin.close(descriptor) }
        try validateProtectedDescriptor(descriptor, expectedMode: 0o600)
        let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: false)
        do {
            guard let data = try handle.read(upToCount: maximumBytes + 1), data.count <= maximumBytes else {
                throw DaemonProfileStoreError.metadataTooLarge
            }
            return data
        } catch let error as DaemonProfileStoreError {
            throw error
        } catch {
            throw DaemonProfileStoreError.fileOperationFailed
        }
    }

    private func writeProtectedFile(_ data: Data, to url: URL) throws {
        let temporaryURL = directory.appendingPathComponent(".\(url.lastPathComponent)-\(UUID().uuidString).tmp", isDirectory: false)
        defer { try? FileManager.default.removeItem(at: temporaryURL) }

        let descriptor = open(temporaryURL.path, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, mode_t(0o600))
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
              Darwin.rename(temporaryURL.path, url.path) == 0
        else {
            throw DaemonProfileStoreError.fileOperationFailed
        }
    }

    private func removeProtectedFile(at url: URL) throws {
        var info = stat()
        if lstat(url.path, &info) != 0 {
            if errno == ENOENT { return }
            throw DaemonProfileStoreError.fileOperationFailed
        }
        guard (info.st_mode & S_IFMT) == S_IFREG,
              info.st_uid == getuid(),
              info.st_mode & 0o077 == 0,
              unlink(url.path) == 0
        else {
            throw DaemonProfileStoreError.insecureMetadata
        }
    }

    private func validateProtectedDescriptor(_ descriptor: Int32, expectedMode: mode_t) throws {
        var info = stat()
        guard fstat(descriptor, &info) == 0,
              (info.st_mode & S_IFMT) == S_IFREG,
              info.st_uid == getuid(),
              info.st_mode & 0o077 == 0,
              info.st_mode & 0o700 == expectedMode
        else {
            throw DaemonProfileStoreError.insecureMetadata
        }
    }

    private func validateDirectoryDescriptor(_ descriptor: Int32) throws {
        var info = stat()
        guard fstat(descriptor, &info) == 0,
              (info.st_mode & S_IFMT) == S_IFDIR,
              info.st_uid == getuid(),
              info.st_mode & 0o077 == 0
        else {
            throw DaemonProfileStoreError.insecureMetadata
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
}

private enum LoginKeychain {
    static let defaultService = "com.amneziawg.daemon-profile-store"

    static func read(id: UUID, version: DaemonProfileSecretVersion, service: String) throws -> Data? {
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query(for: id, version: version, service: service, returningData: true) as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
        return data
    }

    static func write(id: UUID, version: DaemonProfileSecretVersion, data: Data, service: String) throws {
        let status = SecItemUpdate(
            query(for: id, version: version, service: service, returningData: false) as CFDictionary,
            [kSecValueData: data] as CFDictionary
        )
        if status == errSecSuccess {
            return
        }
        guard status == errSecItemNotFound else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
        var attributes = query(for: id, version: version, service: service, returningData: false)
        attributes[kSecValueData] = data
        attributes[kSecAttrAccessible] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let addStatus = SecItemAdd(attributes as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw DaemonProfileStoreError.keychainOperationFailed(addStatus)
        }
    }

    static func delete(id: UUID, version: DaemonProfileSecretVersion, service: String) throws {
        let status = SecItemDelete(query(for: id, version: version, service: service, returningData: false) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw DaemonProfileStoreError.keychainOperationFailed(status)
        }
    }

    private static func query(for id: UUID, version: DaemonProfileSecretVersion, service: String, returningData: Bool) -> [CFString: Any] {
        let account: String
        switch version {
        case .active:
            account = id.uuidString.lowercased()
        case .pending:
            account = "\(id.uuidString.lowercased()).pending"
        }
        var query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: service,
            kSecAttrAccount: account,
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
