// SPDX-License-Identifier: MIT

import Darwin
import Dispatch
import Foundation

@main
struct DaemonDiagnosticsSelfCheck {
    static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        guard condition() else {
            fputs("daemon diagnostics self-check failed: \(message)\n", stderr)
            exit(EXIT_FAILURE)
        }
    }

    static func main() throws {
        let manager = FileManager.default
        let temporaryDirectory = manager.temporaryDirectory.appendingPathComponent("amneziawg-daemon-diagnostics-\(UUID().uuidString)")
        defer { try? manager.removeItem(at: temporaryDirectory) }

        let symlinkDirectory = temporaryDirectory.appendingPathComponent("symlink")
        try manager.createDirectory(at: temporaryDirectory, withIntermediateDirectories: true)
        try manager.createSymbolicLink(at: symlinkDirectory, withDestinationURL: temporaryDirectory)
        require(!DaemonDiagnosticFileSink.append("Daemon diagnostic: probe=symlink", directory: symlinkDirectory), "symlink directory accepted")

        let hardlinkDirectory = temporaryDirectory.appendingPathComponent("hardlink")
        try manager.createDirectory(at: hardlinkDirectory, withIntermediateDirectories: true)
        let log = hardlinkDirectory.appendingPathComponent("daemon-diagnostics.log")
        manager.createFile(atPath: log.path, contents: Data(), attributes: [.posixPermissions: 0o600])
        let hardlink = hardlinkDirectory.appendingPathComponent("same-file")
        require(link(log.path, hardlink.path) == 0, "cannot make hard link")
        require(!DaemonDiagnosticFileSink.append("Daemon diagnostic: probe=hardlink", directory: hardlinkDirectory), "hard-linked file accepted")

        let permissiveDirectory = temporaryDirectory.appendingPathComponent("permissive")
        try manager.createDirectory(at: permissiveDirectory, withIntermediateDirectories: true,
                                    attributes: [.posixPermissions: 0o700])
        let permissiveLog = permissiveDirectory.appendingPathComponent("daemon-diagnostics.log")
        manager.createFile(atPath: permissiveLog.path, contents: Data(), attributes: [.posixPermissions: 0o600])
        try manager.setAttributes([.posixPermissions: 0o644], ofItemAtPath: permissiveLog.path)
        require(!DaemonDiagnosticFileSink.append("Daemon diagnostic: probe=permissions", directory: permissiveDirectory), "permissive file accepted")

        let concurrentDirectory = temporaryDirectory.appendingPathComponent("concurrent")
        let queue = DispatchQueue(label: "daemon-diagnostics-self-check", attributes: .concurrent)
        let group = DispatchGroup()
        let lock = NSLock()
        var successes = 0
        for _ in 0 ..< 64 {
            group.enter()
            queue.async {
                if DaemonDiagnosticFileSink.append("Daemon diagnostic: probe=parallel", directory: concurrentDirectory) {
                    lock.lock()
                    successes += 1
                    lock.unlock()
                }
                group.leave()
            }
        }
        group.wait()
        require(successes == 64, "parallel writes lost")
        let content = try String(contentsOf: concurrentDirectory.appendingPathComponent("daemon-diagnostics.log"), encoding: .utf8)
        require(content.split(separator: "\n").count == 64, "parallel log record count")
        let attributes = try manager.attributesOfItem(atPath: concurrentDirectory.appendingPathComponent("daemon-diagnostics.log").path)
        require((attributes[.posixPermissions] as? NSNumber)?.intValue == 0o600, "log permissions")
        print("daemon diagnostics self-check passed")
    }
}
