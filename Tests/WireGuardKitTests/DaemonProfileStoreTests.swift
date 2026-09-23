// SPDX-License-Identifier: MIT

#if os(macOS)
import XCTest
@testable import WireGuardKit

final class DaemonProfileStoreTests: XCTestCase {
    func testSaveRenameLoadAndDeleteKeepsSecretsOutOfMetadata() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = fixture.configuration()

        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: config)

        XCTAssertEqual(try fixture.store.profiles(), [DaemonProfileMetadata(id: id, name: "Synthetic")])
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Synthetic")
        let metadata = try Data(contentsOf: fixture.metadataURL)
        XCTAssertFalse(String(decoding: metadata, as: UTF8.self).contains("PrivateKey"))
        XCTAssertEqual(try filePermissions(fixture.metadataURL), 0o600)

        try fixture.store.rename(id: id, to: "Renamed")
        XCTAssertEqual(try fixture.store.configuration(for: id).name, "Renamed")
        XCTAssertEqual(fixture.secrets.values[id], Data(config.utf8))

        try fixture.store.delete(id: id)
        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertNil(fixture.secrets.values[id])
    }

    func testMetadataFailureRestoresPriorSecret() throws {
        let fixture = try Fixture()
        let id = UUID()
        let oldSecret = Data("old synthetic secret".utf8)
        fixture.secrets.values[id] = oldSecret
        try FileManager.default.createDirectory(at: fixture.metadataURL, withIntermediateDirectories: false)

        XCTAssertThrowsError(try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: fixture.configuration()))
        XCTAssertEqual(fixture.secrets.values[id], oldSecret)
    }

    func testRejectsInvalidConfigurationBeforeWriting() throws {
        let fixture = try Fixture()
        let id = UUID()

        XCTAssertThrowsError(try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: "[Interface]\n"))
        XCTAssertTrue(try fixture.store.profiles().isEmpty)
        XCTAssertNil(fixture.secrets.values[id])
    }

    func testDeleteMetadataFailureRestoresSecret() throws {
        let fixture = try Fixture()
        let id = UUID()
        let config = fixture.configuration()
        try fixture.store.save(id: id, name: "Synthetic", wgQuickConfig: config)
        fixture.secrets.onDelete = {
            try FileManager.default.removeItem(at: fixture.metadataURL)
            try FileManager.default.createDirectory(at: fixture.metadataURL, withIntermediateDirectories: false)
        }

        XCTAssertThrowsError(try fixture.store.delete(id: id))
        XCTAssertEqual(fixture.secrets.values[id], Data(config.utf8))
    }

    private func filePermissions(_ url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return (attributes[.posixPermissions] as? NSNumber)?.intValue ?? -1
    }

    private final class Fixture {
        let directory: URL
        let metadataURL: URL
        let secrets = MemorySecrets()
        let store: DaemonProfileStore

        init() throws {
            directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
            metadataURL = directory.appendingPathComponent("profiles.json", isDirectory: false)
            store = try DaemonProfileStore(
                directory: directory,
                secrets: DaemonProfileSecretStore(
                    read: { [secrets] id in secrets.values[id] },
                    write: { [secrets] id, data in secrets.values[id] = data },
                    delete: { [secrets] id in
                        secrets.values.removeValue(forKey: id)
                        try secrets.onDelete?()
                    }
                )
            )
        }

        deinit {
            try? FileManager.default.removeItem(at: directory)
        }

        func configuration() -> String {
            let privateKey = PrivateKey()
            let peerKey = PrivateKey().publicKey
            return """
            [Interface]
            PrivateKey = \(privateKey.base64Key)
            Address = 192.0.2.2/32

            [Peer]
            PublicKey = \(peerKey.base64Key)
            AllowedIPs = 192.0.2.0/24
            """
        }
    }

    private final class MemorySecrets {
        var values = [UUID: Data]()
        var onDelete: (() throws -> Void)?
    }
}
#endif
