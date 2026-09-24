// SPDX-License-Identifier: MIT

#if os(macOS)
import Foundation

final class DaemonTunnelsManager: TunnelsManager {
    private static let controlSocketPath = "/private/var/db/amneziawg-daemon-control-poc/control.sock"

    private let store: DaemonProfileStore
    private let mutationQueue = DispatchQueue(label: "com.amneziawg.daemon-profile-mutations")

    private init(
        store: DaemonProfileStore,
        records: [(id: UUID, configuration: TunnelConfiguration)],
        statuses: [DaemonControlProfileStatus]
    ) {
        self.store = store
        let tunnels = records.map { record in
            TunnelContainer(
                daemonProfileID: record.id,
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
                    completionHandler(.success(DaemonTunnelsManager(store: store, records: records, statuses: statuses)))
                }
            } catch {
                DispatchQueue.main.async {
                    completionHandler(.failure(.systemErrorOnListingTunnels(systemError: error)))
                }
            }
        }
    }

    override func reload() {
        // The daemon GUI owns its in-memory snapshot until daemon lifecycle support exists.
    }

    override func add(
        tunnelConfiguration: TunnelConfiguration,
        onDemandOption: ActivateOnDemandOption,
        completionHandler: @escaping (Result<TunnelContainer, TunnelsManagerError>) -> Void
    ) {
        guard onDemandOption == .off else {
            completionHandler(.failure(.daemonModeOperationUnavailable))
            return
        }
        guard let name = normalizedName(from: tunnelConfiguration) else {
            completionHandler(.failure(.tunnelNameEmpty))
            return
        }
        guard tunnel(named: name) == nil else {
            completionHandler(.failure(.tunnelAlreadyExistsWithThatName))
            return
        }

        tunnelConfiguration.name = name
        let profileID = UUID()
        let configuration = tunnelConfiguration.asWgQuickConfig()
        mutationQueue.async { [weak self] in
            guard let self else { return }
            do {
                try self.store.save(id: profileID, name: name, wgQuickConfig: configuration)
                DispatchQueue.main.async {
                    let tunnel = TunnelContainer(
                        daemonProfileID: profileID,
                        daemonConfiguration: tunnelConfiguration,
                        status: .inactive
                    )
                    self.tunnels.append(tunnel)
                    self.tunnels.sort { TunnelsManager.tunnelNameIsLessThan($0.name, $1.name) }
                    self.tunnelsListDelegate?.tunnelAdded(at: self.tunnels.firstIndex(of: tunnel)!)
                    completionHandler(.success(tunnel))
                }
            } catch {
                DispatchQueue.main.async {
                    completionHandler(.failure(.systemErrorOnAddTunnel(systemError: error)))
                }
            }
        }
    }

    override func addMultiple(
        tunnelConfigurations: [TunnelConfiguration],
        completionHandler: @escaping (UInt, TunnelsManagerError?) -> Void
    ) {
        addMultiple(
            configurations: ArraySlice(tunnelConfigurations),
            numberSuccessful: 0,
            lastError: nil,
            completionHandler: completionHandler
        )
    }

    override func modify(
        tunnel: TunnelContainer,
        tunnelConfiguration: TunnelConfiguration,
        onDemandOption: ActivateOnDemandOption,
        shouldEnsureOnDemandEnabled: Bool,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        guard onDemandOption == .off, !shouldEnsureOnDemandEnabled else {
            completionHandler(.daemonModeOperationUnavailable)
            return
        }
        guard tunnels.contains(tunnel), let profileID = tunnel.daemonProfileID else {
            completionHandler(.systemErrorOnModifyTunnel(systemError: DaemonProfileStoreError.profileNotFound))
            return
        }
        guard let name = normalizedName(from: tunnelConfiguration) else {
            completionHandler(.tunnelNameEmpty)
            return
        }
        guard !tunnels.contains(where: { $0 !== tunnel && $0.name == name }) else {
            completionHandler(.tunnelAlreadyExistsWithThatName)
            return
        }

        tunnelConfiguration.name = name
        let configuration = tunnelConfiguration.asWgQuickConfig()
        mutationQueue.async { [weak self] in
            guard let self else { return }
            do {
                try self.store.save(id: profileID, name: name, wgQuickConfig: configuration)
                DispatchQueue.main.async {
                    let oldIndex = self.tunnels.firstIndex(of: tunnel)!
                    let nameChanged = tunnel.name != name
                    tunnel.updateDaemonConfiguration(tunnelConfiguration)
                    if nameChanged {
                        self.tunnels.sort { TunnelsManager.tunnelNameIsLessThan($0.name, $1.name) }
                        self.tunnelsListDelegate?.tunnelMoved(
                            from: oldIndex,
                            to: self.tunnels.firstIndex(of: tunnel)!
                        )
                    }
                    self.tunnelsListDelegate?.tunnelModified(at: self.tunnels.firstIndex(of: tunnel)!)
                    completionHandler(nil)
                }
            } catch {
                DispatchQueue.main.async {
                    completionHandler(.systemErrorOnModifyTunnel(systemError: error))
                }
            }
        }
    }

    override func remove(
        tunnel: TunnelContainer,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        guard tunnel.status == .inactive else {
            completionHandler(.daemonModeOperationUnavailable)
            return
        }
        guard tunnels.contains(tunnel), let profileID = tunnel.daemonProfileID else {
            completionHandler(.systemErrorOnRemoveTunnel(systemError: DaemonProfileStoreError.profileNotFound))
            return
        }

        mutationQueue.async { [weak self] in
            guard let self else { return }
            do {
                try self.store.delete(id: profileID)
                DispatchQueue.main.async {
                    guard let index = self.tunnels.firstIndex(of: tunnel) else {
                        completionHandler(.systemErrorOnRemoveTunnel(systemError: DaemonProfileStoreError.profileNotFound))
                        return
                    }
                    self.tunnels.remove(at: index)
                    self.tunnelsListDelegate?.tunnelRemoved(at: index, tunnel: tunnel)
                    completionHandler(nil)
                }
            } catch {
                DispatchQueue.main.async {
                    completionHandler(.systemErrorOnRemoveTunnel(systemError: error))
                }
            }
        }
    }

    override func removeMultiple(
        tunnels: [TunnelContainer],
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        guard tunnels.allSatisfy({ $0.status == .inactive }) else {
            completionHandler(.daemonModeOperationUnavailable)
            return
        }
        removeMultiple(tunnels: ArraySlice(tunnels), completionHandler: completionHandler)
    }

    override func setOnDemandEnabled(
        _ isOnDemandEnabled: Bool,
        on tunnel: TunnelContainer,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        completionHandler(.daemonModeOperationUnavailable)
    }

    override func startActivation(of tunnel: TunnelContainer) {
        guard tunnels.contains(tunnel), tunnel.status == .inactive,
              let profileID = tunnel.daemonProfileID,
              let configuration = tunnel.tunnelConfiguration
        else {
            activationDelegate?.tunnelActivationAttemptFailed(tunnel: tunnel, error: .tunnelIsNotInactive)
            return
        }
        let plan: MacOSDaemonRoutePlan
        switch MacOSDaemonRoutePlan.buildSingleIPv4Split(
            activating: tunnel.name,
            configuration: configuration
        ) {
        case .success(let builtPlan):
            plan = builtPlan
        case .failure:
            activationDelegate?.tunnelActivationAttemptFailed(
                tunnel: tunnel,
                error: .daemonModeUnsupportedProfile
            )
            return
        }
        guard let endpoint = configuration.peers.first?.endpoint else {
            activationDelegate?.tunnelActivationAttemptFailed(
                tunnel: tunnel,
                error: .daemonModeUnsupportedProfile
            )
            return
        }

        let uapiConfiguration = PacketTunnelSettingsGenerator(
            tunnelConfiguration: configuration,
            resolvedEndpoints: [endpoint]
        ).uapiConfiguration().0
        tunnel.status = .activating
        mutationQueue.async { [weak tunnel] in
            guard let tunnel else { return }
            let client: DaemonControlClient
            let statuses: [DaemonControlProfileStatus]
            do {
                client = try DaemonControlClient(socketPath: Self.controlSocketPath)
                statuses = try client.list()
            } catch {
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    self.markDaemonStatusesDegraded()
                    tunnel.status = .reasserting
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: .daemonModeStateUncertain
                    )
                }
                return
            }
            guard statuses.isEmpty else {
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    self.applyDaemonStatuses(statuses)
                    tunnel.status = Self.tunnelStatus(for: profileID, statuses: statuses)
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: .daemonModeSingleTunnelOnly
                    )
                }
                return
            }
            do {
                _ = try client.start(
                    profileID: profileID,
                    uapiConfiguration: uapiConfiguration,
                    routePlan: plan
                )
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else {
                        DaemonTunnelsManager.stopOrphanedProfile(profileID)
                        return
                    }
                    tunnel.status = .active
                    self.activationDelegate?.tunnelActivationSucceeded(tunnel: tunnel)
                }
            } catch {
                let startError = error
                let reconciliation = Self.reconcileFailedStart(profileID: profileID)
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = reconciliation.status
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: reconciliation.isCertain ?
                            .daemonModeOperationFailed(systemError: startError) :
                            .daemonModeStateUncertain
                    )
                }
            }
        }
    }

    override func startDeactivation(of tunnel: TunnelContainer) {
        guard tunnels.contains(tunnel),
              let profileID = tunnel.daemonProfileID,
              tunnel.status == .active || tunnel.status == .reasserting
        else { return }
        tunnel.status = .deactivating
        mutationQueue.async { [weak self, weak tunnel] in
            do {
                try DaemonControlClient(socketPath: Self.controlSocketPath).stop(profileID: profileID)
                DispatchQueue.main.async {
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = .inactive
                }
            } catch {
                let stopError = error
                let refreshedStatus: TunnelStatus
                do {
                    let statuses = try DaemonControlClient(socketPath: Self.controlSocketPath).list()
                    refreshedStatus = Self.tunnelStatus(for: profileID, statuses: statuses)
                } catch {
                    refreshedStatus = .reasserting
                }
                DispatchQueue.main.async {
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = refreshedStatus
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: .daemonModeOperationFailed(systemError: stopError)
                    )
                }
            }
        }
    }

    override func refreshStatuses() {
        mutationQueue.async { [weak self] in
            let result = Result { try DaemonControlClient(socketPath: Self.controlSocketPath).list() }
            DispatchQueue.main.async {
                guard let self else { return }
                switch result {
                case .success(let statuses):
                    self.applyDaemonStatuses(statuses)
                case .failure:
                    self.markDaemonStatusesDegraded()
                }
            }
        }
    }

    private func applyDaemonStatuses(_ statuses: [DaemonControlProfileStatus]) {
        for tunnel in tunnels where tunnel.status != .activating && tunnel.status != .deactivating {
            guard let profileID = tunnel.daemonProfileID else { continue }
            tunnel.status = Self.tunnelStatus(for: profileID, statuses: statuses)
        }
    }

    private func markDaemonStatusesDegraded() {
        for tunnel in tunnels where tunnel.status == .active {
            tunnel.status = .reasserting
        }
    }

    private static func reconcileFailedStart(profileID: UUID) -> (status: TunnelStatus, isCertain: Bool) {
        do {
            let client = try DaemonControlClient(socketPath: controlSocketPath)
            let statuses = try client.list()
            guard let status = statuses.first(where: { $0.id == profileID }) else {
                return (.reasserting, false)
            }
            guard status.state == .running else {
                return (.reasserting, false)
            }
            try client.stop(profileID: profileID)
            let remaining = try client.list()
            return remaining.contains(where: { $0.id == profileID }) ? (.reasserting, false) : (.inactive, true)
        } catch {
            return (.reasserting, false)
        }
    }

    private static func stopOrphanedProfile(_ profileID: UUID) {
        DispatchQueue.global(qos: .utility).async {
            for _ in 0 ..< 2 {
                if orphanedProfileStopIsConfirmed(profileID) { return }
            }
            wg_log(.error, message: "Daemon cleanup could not be confirmed for profile \(profileID.uuidString.lowercased())")
        }
    }

    private static func orphanedProfileStopIsConfirmed(_ profileID: UUID) -> Bool {
        do {
            let client = try DaemonControlClient(socketPath: controlSocketPath)
            do {
                try client.stop(profileID: profileID)
            } catch {
                // A lost stop response can still mean the daemon stopped the profile.
            }
            return try !client.list().contains(where: { $0.id == profileID })
        } catch {
            return false
        }
    }

    private func addMultiple(
        configurations: ArraySlice<TunnelConfiguration>,
        numberSuccessful: UInt,
        lastError: TunnelsManagerError?,
        completionHandler: @escaping (UInt, TunnelsManagerError?) -> Void
    ) {
        guard let configuration = configurations.first else {
            completionHandler(numberSuccessful, lastError)
            return
        }
        add(tunnelConfiguration: configuration, onDemandOption: .off) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success:
                self.addMultiple(
                    configurations: configurations.dropFirst(),
                    numberSuccessful: numberSuccessful + 1,
                    lastError: lastError,
                    completionHandler: completionHandler
                )
            case .failure(let error):
                self.addMultiple(
                    configurations: configurations.dropFirst(),
                    numberSuccessful: numberSuccessful,
                    lastError: error,
                    completionHandler: completionHandler
                )
            }
        }
    }

    private func removeMultiple(
        tunnels: ArraySlice<TunnelContainer>,
        completionHandler: @escaping (TunnelsManagerError?) -> Void
    ) {
        guard let tunnel = tunnels.first else {
            completionHandler(nil)
            return
        }
        remove(tunnel: tunnel) { [weak self] error in
            guard let self else { return }
            if let error {
                completionHandler(error)
            } else {
                self.removeMultiple(tunnels: tunnels.dropFirst(), completionHandler: completionHandler)
            }
        }
    }

    private func normalizedName(from tunnelConfiguration: TunnelConfiguration) -> String? {
        guard let name = tunnelConfiguration.name?.trimmingCharacters(in: .whitespacesAndNewlines),
              !name.isEmpty,
              !name.contains("\0")
        else {
            return nil
        }
        return name
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
