// SPDX-License-Identifier: MIT

#if os(macOS)
import Foundation

final class DaemonTunnelsManager: TunnelsManager {
    private static let controlSocketPath = "/private/var/db/amneziawg-daemon-control-poc/control.sock"

    private init(records: [(id: UUID, configuration: TunnelConfiguration)], statuses: [DaemonControlProfileStatus]) {
        let tunnels = records.map { record in
            TunnelContainer(
                daemonConfiguration: record.configuration,
                status: Self.tunnelStatus(for: record.id, statuses: statuses)
            )
        }
        super.init(tunnels: tunnels, observesNetworkExtension: false)
    }

    static func createDaemon(completionHandler: @escaping (Result<TunnelsManager, TunnelsManagerError>) -> Void) {
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let store = try DaemonProfileStore()
                let metadata = try store.profiles()
                guard Set(metadata.map(\.name)).count == metadata.count else {
                    throw DaemonProfileStoreError.invalidName
                }
                let records = try metadata.map { metadata in
                    (id: metadata.id, configuration: try store.configuration(for: metadata.id))
                }
                let statuses = (try? DaemonControlClient(socketPath: controlSocketPath).list()) ?? []
                DispatchQueue.main.async {
                    completionHandler(.success(DaemonTunnelsManager(records: records, statuses: statuses)))
                }
            } catch {
                DispatchQueue.main.async {
                    completionHandler(.failure(.systemErrorOnListingTunnels(systemError: error)))
                }
            }
        }
    }

    override func reload() {
        // The first daemon GUI slice loads a read-only snapshot at launch.
    }

    override func add(
        tunnelConfiguration: TunnelConfiguration,
        onDemandOption: ActivateOnDemandOption,
        completionHandler: @escaping (Result<TunnelContainer, TunnelsManagerError>) -> Void
    ) {
        completionHandler(.failure(.daemonModeOperationUnavailable))
    }

    override func addMultiple(
        tunnelConfigurations: [TunnelConfiguration],
        completionHandler: @escaping (UInt, TunnelsManagerError?) -> Void
    ) {
        completionHandler(0, .daemonModeOperationUnavailable)
    }

    override func modify(
        tunnel: TunnelContainer,
        tunnelConfiguration: TunnelConfiguration,
        onDemandOption: ActivateOnDemandOption,
        shouldEnsureOnDemandEnabled: Bool,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        completionHandler(.daemonModeOperationUnavailable)
    }

    override func remove(
        tunnel: TunnelContainer,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        completionHandler(.daemonModeOperationUnavailable)
    }

    override func removeMultiple(
        tunnels: [TunnelContainer],
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        completionHandler(.daemonModeOperationUnavailable)
    }

    override func setOnDemandEnabled(
        _ isOnDemandEnabled: Bool,
        on tunnel: TunnelContainer,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        completionHandler(.daemonModeOperationUnavailable)
    }

    override func startActivation(of tunnel: TunnelContainer) {
        activationDelegate?.tunnelActivationAttemptFailed(
            tunnel: tunnel,
            error: .daemonModeOperationUnavailable
        )
    }

    override func startDeactivation(of tunnel: TunnelContainer) {
        activationDelegate?.tunnelActivationAttemptFailed(
            tunnel: tunnel,
            error: .daemonModeOperationUnavailable
        )
    }

    override func refreshStatuses() {
        // Statuses belong to the launch snapshot until lifecycle support exists.
    }

    private static func tunnelStatus(for profileID: UUID, statuses: [DaemonControlProfileStatus]) -> TunnelStatus {
        switch DaemonTunnelState.current(for: profileID, statuses: statuses) {
        case .inactive:
            return .inactive
        case .running:
            return .active
        case .degraded:
            return .reasserting
        }
    }
}
#endif
