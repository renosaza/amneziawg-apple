// SPDX-License-Identifier: MIT

#if os(macOS)
import XCTest
@testable import WireGuardKit

final class DaemonProfileStoreTests: XCTestCase {
    func testSaveRenameLoadAndDeleteKeepsSecretsOutOfMetadata() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = Self.configuration()

        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: config)

        XCTAssertEqual(try fixture.store.profiles(), [DaemonProfileMetadata(id: id, name: "Synthetic")])
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Synthetic")
        let metadata = try Data(contentsOf: fixture.metadataURL)
        XCTAssertFalse(String(decoding: metadata, as: UTF8.self).contains("PrivateKey"))
        XCTAssertEqual(try filePermissions(fixture.metadataURL), 0o600)

        try fixture.store.rename(id: id, to: "Renamed")
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Renamed")
        XCTAssertEqual(fixture.secrets.activeValue(for: id), Data(config.utf8))

        try fixture.store.delete(id: id)
        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertNil(fixture.secrets.activeValue(for: id))
    }

    func testSaveMetadataFailurePreservesActiveSecret() throws {
        let fixture = try Fixture()
        let id = UUID()
        let oldSecret = Data("old synthetic secret".utf8)
        fixture.secrets.write(id, .active, oldSecret)
        try FileManager.default.createDirectory(at: fixture.metadataURL, withIntermediateDirectories: false)

        XCTAssertThrowsError(try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: Self.configuration()))
        XCTAssertEqual(fixture.secrets.activeValue(for: id), oldSecret)
        XCTAssertNil(fixture.secrets.pendingValue(for: id))
    }

    func testDeleteKeychainFailureRecoversOnNextCall() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = Self.configuration()
        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: config)
        fixture.secrets.rejectActiveDelete = true

        XCTAssertThrowsError(try fixture.store.delete(id: id))
        fixture.secrets.rejectActiveDelete = false
        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertNil(fixture.secrets.activeValue(for: id))
    }

    func testRecoveryPromotesStagedSecret() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = Self.configuration()
        fixture.secrets.write(id, .pending, Data(config.utf8))
        let journal = """
        {"operation":"save","phase":"staged","id":"\(id.uuidString.lowercased())","name":"Recovered"}
        """
        try Data(journal.utf8).write(to: fixture.journalURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fixture.journalURL.path)

        XCTAssertEqual(try fixture.store.profiles(), [DaemonProfileMetadata(id: id, name: "Recovered")])
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Recovered")
        XCTAssertNil(fixture.secrets.pendingValue(for: id))
    }

    func testPreparedJournalWithoutPendingRollsBack() throws {
        let fixture = try Fixture()
        let id = UUID()
        let journal = """
        {"operation":"save","phase":"prepared","id":"\(id.uuidString.lowercased())","name":"Prepared"}
        """
        try Data(journal.utf8).write(to: fixture.journalURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fixture.journalURL.path)

        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.journalURL.path))
    }

    func testStagedJournalAfterMetadataAndActiveCommitIsRemoved() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = Self.configuration()
        fixture.secrets.write(id, .active, Data(config.utf8))
        let metadata = "{\"profiles\":[{\"id\":\"(id.uuidString.lowercased())\",\"name\":\"Committed\"}]}"
        try Data(metadata.utf8).write(to: fixture.metadataURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fixture.metadataURL.path)
        let journal = """
        {"operation":"save","phase":"staged","id":"\(id.uuidString.lowercased())","name":"Committed"}
        """
        try Data(journal.utf8).write(to: fixture.journalURL)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fixture.journalURL.path)

        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Committed")
        XCTAssertFalse(FileManager.default.fileExists(atPath: fixture.journalURL.path))
    }

    func testUnjournaledPendingWithoutMetadataIsDiscardedBeforeSave() throws {
        let fixture = try Fixture()
        let id = UUID()
        fixture.secrets.write(id, .pending, Data(Self.configuration().utf8))

        try fixture.store.save(id: id, name: "Recovered", wgQuickConfig: Self.configuration())
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Recovered")
        XCTAssertNil(fixture.secrets.pendingValue(for: id))
    }

    func testUnjournaledPendingRestoresMissingActiveSecretForMetadata() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = Self.configuration()
        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: config)
        fixture.secrets.write(id, .pending, Data(config.utf8))
        try fixture.secrets.delete(id, .active)

        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Synthetic")
        XCTAssertEqual(fixture.secrets.activeValue(for: id), Data(config.utf8))
        XCTAssertNil(fixture.secrets.pendingValue(for: id))
    }

    func testUnjournaledPendingPreservesExistingActiveSecret() throws {
        let fixture = try Fixture()
        let id = UUID()
        let active = Self.configuration()
        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: active)
        let replacement = Self.configuration()
        fixture.secrets.write(id, .pending, Data(replacement.utf8))

        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Synthetic")
        XCTAssertEqual(fixture.secrets.activeValue(for: id), Data(active.utf8))
        XCTAssertNil(fixture.secrets.pendingValue(for: id))
    }

    func testConfigurationRoundTripsExcludeIPs() throws {
        let fixture = try Fixture()
        let id = UUID()
        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: Self.configuration(excludeIPs: true))

        XCTAssertEqual(
            try fixture.store.configuration(for: id).peers[0].excludeIPs.map(\.stringRepresentation),
            ["192.0.2.0/24", "2001:db8::/32"]
        )
    }

    func testLoginKeychainRoundTripUsesUniqueServiceAndCleansUp() throws {
        let service = "com.amneziawg.daemon-profile-store.test.\(UUID().uuidString.lowercased())"
        let secrets = DaemonProfileSecretStore.loginKeychain(service: service)
        let id = UUID()
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        defer {
            try? secrets.delete(id, .active)
            try? secrets.delete(id, .pending)
            try? FileManager.default.removeItem(at: directory)
        }
        let store = try DaemonProfileStore(directory: directory, secrets: secrets)
        let config = Self.configuration()

        try store.save(id: id, name: "Keychain", wgQuickConfig: config)
        XCTAssertEqual(try secrets.read(id, .active), Data(config.utf8))
        XCTAssertNil(try secrets.read(id, .pending))
        XCTAssertEqual(try store.configuration(for: id).name, "Keychain")

        try store.delete(id: id)
        XCTAssertNil(try secrets.read(id, .active))
    }

    func testRejectsInvalidConfigurationBeforeWriting() throws {
        let fixture = try Fixture()
        let id = UUID()

        XCTAssertThrowsError(try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: "[Interface]\n"))
        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertNil(fixture.secrets.activeValue(for: id))
    }

    private func filePermissions(_ url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return (attributes[.posixPermissions] as? NSNumber)?.intValue ?? -1
    }

    private static func configuration(excludeIPs: Bool = false) -> String {
        let privateKey = PrivateKey()
        let peerKey = PrivateKey().publicKey
        let exclusions = excludeIPs ? "\nExcludeIPs = 192.0.2.0/24, 2001:db8::/32" : ""
        return """
        [Interface]
        PrivateKey = \(privateKey.base64Key)
        Address = 192.0.2.2/32

        [Peer]
        PublicKey = \(peerKey.base64Key)
        AllowedIPs = 192.0.2.0/24\(exclusions)
        """
    }

    private final class Fixture {
        let directory: URL
        let metadataURL: URL
        let journalURL: URL
        let secrets = MemorySecrets()
        let store: DaemonProfileStore

        init() throws {
            directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
            metadataURL = directory.appendingPathComponent("profiles.json", isDirectory: false)
            journalURL = directory.appendingPathComponent("transaction.json", isDirectory: false)
            store = try DaemonProfileStore(
                directory: directory,
                secrets: DaemonProfileSecretStore(
                    read: { [secrets] id, version in secrets.read(id, version) },
                    write: { [secrets] id, version, data in secrets.write(id, version, data) },
                    delete: { [secrets] id, version in try secrets.delete(id, version) }
                )
            )
        }

        deinit {
            try? FileManager.default.removeItem(at: directory)
        }
    }

    private final class MemorySecrets {
        private var values = [String: Data]()
        var rejectActiveDelete = false

        func read(_ id: UUID, _ version: DaemonProfileSecretVersion) -> Data? {
            values[key(id, version)]
        }

        func write(_ id: UUID, _ version: DaemonProfileSecretVersion, _ data: Data) {
            values[key(id, version)] = data
        }

        func delete(_ id: UUID, _ version: DaemonProfileSecretVersion) throws {
            if version == .active && rejectActiveDelete {
                throw FixtureError.rejectedDelete
            }
            values.removeValue(forKey: key(id, version))
        }

        func activeValue(for id: UUID) -> Data? {
            read(id, .active)
        }

        func pendingValue(for id: UUID) -> Data? {
            read(id, .pending)
        }

        private func key(_ id: UUID, _ version: DaemonProfileSecretVersion) -> String {
            "\(id.uuidString)-\(version == .active ? "active" : "pending")"
        }
    }

    private enum FixtureError: Error {
        case rejectedDelete
    }
}
#endif
