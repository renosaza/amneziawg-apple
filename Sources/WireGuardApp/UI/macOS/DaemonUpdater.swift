// SPDX-License-Identifier: MIT
// Copyright © 2026 AmneziaWG Contributors. All Rights Reserved.

#if DAEMON_MODE
    import Cocoa
    import Sparkle

    final class DaemonUpdater {
        private let controller: SPUStandardUpdaterController

        static var isConfigured: Bool {
            guard let feedURL = Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") as? String,
                  let url = URL(string: feedURL),
                  url.scheme?.lowercased() == "https",
                  url.host != nil,
                  let publicKey = Bundle.main.object(forInfoDictionaryKey: "SUPublicEDKey") as? String,
                  Data(base64Encoded: publicKey)?.count == 32 else {
                return false
            }
            return true
        }

        static func isCompatibleDaemon(at controlSocketPath: String) -> Bool {
            guard let client = try? DaemonControlClient(socketPath: controlSocketPath) else {
                return false
            }
            return (try? client.list()) != nil
        }

        init?() {
            guard Self.isConfigured else {
                return nil
            }
            controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)
        }

        func configure(checkForUpdatesMenuItem menuItem: NSMenuItem) {
            menuItem.target = controller
            menuItem.action = #selector(SPUStandardUpdaterController.checkForUpdates(_:))
        }
    }
#endif
