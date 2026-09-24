// SPDX-License-Identifier: MIT

#if os(macOS)
import Foundation

/// Opt-in, deliberately low-detail records for diagnosing the daemon GUI.
/// It never receives a profile, route destination, UAPI configuration, or raw error.
private enum DaemonDiagnostics {
    static let userDefaultsKey = "DaemonDiagnosticLogging"
    static func install() {
        DaemonControlClient.diagnosticHandler = record
    }

    static func newOperationID() -> String {
        String(UUID().uuidString.prefix(8)).lowercased()
    }

    static func record(_ event: String, fields: [String: String] = [:]) {
        guard UserDefaults.standard.bool(forKey: userDefaultsKey) ||
                ProcessInfo.processInfo.environment["AMNEZIAWG_DAEMON_DIAGNOSTICS"] == "1"
        else { return }
        let renderedFields = fields.keys.sorted().compactMap { key -> String? in
            guard let value = fields[key],
                  key.allSatisfy({ $0.isASCII && ($0.isLetter || $0 == "_") }),
                  value.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "_" || $0 == "-" || $0 == "," || $0 == ":" || $0 == "/") })
            else { return nil }
            return "\(key)=\(value)"
        }
        let message = "Daemon diagnostic: \(event)\(renderedFields.isEmpty ? "" : " \(renderedFields.joined(separator: " "))")"
        wg_log(.info, message: message)
        _ = DaemonDiagnosticFileSink.append(message)
    }

    static func recordRoutePlan(_ plan: MacOSDaemonRoutePlan, operationID: String) {
        let owners = Dictionary(grouping: plan.routes, by: \.owner.rawValue).map {
            "\($0.key):\($0.value.count)"
        }.sorted().joined(separator: ",")
        record("route_plan", fields: ["id": operationID, "owners": owners])
    }

    static func recordValidation(_ operationID: String, _ error: MacOSDaemonSingleSplitProfileError?) {
        let outcome: String
        switch error {
        case nil: outcome = "accepted"
        case .unsupportedConfiguration?: outcome = "unsupported_configuration"
        case .routePlan(.routeOwnership(.routeConflict))?: outcome = "route_conflict"
        case .routePlan(.routeOwnership(.missingEndpointExclusion))?: outcome = "missing_endpoint_exclusion"
        case .routePlan(.invalidResolvedEndpoints)?: outcome = "invalid_resolved_endpoints"
        case .routePlan(.invalidInterfaceAddress)?: outcome = "invalid_interface_address"
        }
        recordStage(operationID, stage: "profile_validation", outcome: outcome)
    }

    static func recordStage(_ operationID: String, stage: String, outcome: String) {
        record("operation", fields: ["id": operationID, "stage": stage, "outcome": outcome])
    }

}

final class DaemonTunnelsManager: TunnelsManager {
    static let controlSocketPath = "/private/var/db/amneziawg-daemon-control-poc/control.sock"

    private let store: DaemonProfileStore
    private let mutationQueue = DispatchQueue(label: "com.amneziawg.daemon-profile-mutations")
    // Accessed only from mutationQueue. A successful stop is required before another start.
    private var uncertainDaemonProfileIDs = Set<UUID>()

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
        DaemonDiagnostics.install()
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
        guard tunnel.status == .inactive else {
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
        let localProfiles = daemonProfiles()
        let diagnosticID = DaemonDiagnostics.newOperationID()
        tunnel.status = .activating
        DaemonDiagnostics.record("status_transition", fields: [
            "id": diagnosticID, "from": "inactive", "to": "activating"
        ])
        DaemonDiagnostics.recordStage(diagnosticID, stage: "activation", outcome: "started")
        mutationQueue.async { [weak self, weak tunnel] in
            guard let self, let tunnel else { return }
            guard self.uncertainDaemonProfileIDs.isEmpty else {
                DaemonDiagnostics.recordStage(diagnosticID, stage: "preflight", outcome: "state_uncertain")
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = .reasserting
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: .daemonModeStateUncertain
                    )
                }
                return
            }
            let client: DaemonControlClient
            let capabilities: DaemonControlCapabilities
            let statuses: [DaemonControlProfileStatus]
            do {
                client = try DaemonControlClient(
                    socketPath: Self.controlSocketPath, diagnosticOperationID: diagnosticID)
                capabilities = try client.capabilities()
                statuses = try client.list()
            } catch {
                DaemonDiagnostics.recordStage(diagnosticID, stage: "preflight", outcome: "daemon_unavailable")
                self.uncertainDaemonProfileIDs.insert(profileID)
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
            guard let activeTunnels = Self.activeDaemonTunnels(
                from: statuses,
                localProfiles: localProfiles,
                excluding: profileID
            ) else {
                DaemonDiagnostics.recordStage(diagnosticID, stage: "preflight", outcome: "state_uncertain")
                self.uncertainDaemonProfileIDs.insert(profileID)
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
            let plan: MacOSDaemonRoutePlan
            let routePlanResult: Result<MacOSDaemonRoutePlan, MacOSDaemonSingleSplitProfileError>
            if Self.isIPv4FullTunnel(configuration) {
                routePlanResult = capabilities.supportsIPv4FullRoute ?
                    MacOSDaemonRoutePlan.buildSingleIPv4Full(
                        activating: tunnel.name,
                        configuration: configuration,
                        activeTunnels: activeTunnels
                    ) : .failure(.unsupportedConfiguration)
            } else {
                routePlanResult = MacOSDaemonRoutePlan.buildSingleIPv4Split(
                    activating: tunnel.name,
                    configuration: configuration,
                    activeTunnels: activeTunnels
                )
            }
            switch routePlanResult {
            case .success(let builtPlan):
                plan = builtPlan
                DaemonDiagnostics.recordValidation(diagnosticID, nil)
                DaemonDiagnostics.recordRoutePlan(builtPlan, operationID: diagnosticID)
            case .failure(let error):
                DaemonDiagnostics.recordValidation(diagnosticID, error)
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    self.applyDaemonStatuses(statuses)
                    tunnel.status = .inactive
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: Self.activationError(for: error, activating: tunnel.name)
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
                    DaemonDiagnostics.record("status_transition", fields: [
                        "id": diagnosticID, "from": "activating", "to": "active"
                    ])
                    DaemonDiagnostics.recordStage(diagnosticID, stage: "activation", outcome: "finished")
                    self.activationDelegate?.tunnelActivationSucceeded(tunnel: tunnel)
                }
            } catch {
                let startError = error
                let reconciliation = Self.reconcileFailedStart(profileID: profileID)
                if !reconciliation.isCertain {
                    self.uncertainDaemonProfileIDs.insert(profileID)
                }
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = reconciliation.status
                    DaemonDiagnostics.record("status_transition", fields: [
                        "id": diagnosticID, "from": "activating",
                        "to": reconciliation.status == .inactive ? "inactive" : "reasserting"
                    ])
                    DaemonDiagnostics.recordStage(diagnosticID, stage: "activation", outcome: "failed")
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
        let diagnosticID = DaemonDiagnostics.newOperationID()
        tunnel.status = .deactivating
        DaemonDiagnostics.record("status_transition", fields: [
            "id": diagnosticID, "from": "active", "to": "deactivating"
        ])
        DaemonDiagnostics.recordStage(diagnosticID, stage: "deactivation", outcome: "started")
        mutationQueue.async { [weak self, weak tunnel] in
            guard let self else { return }
            do {
                try DaemonControlClient(
                    socketPath: Self.controlSocketPath, diagnosticOperationID: diagnosticID
                ).stop(profileID: profileID)
                self.uncertainDaemonProfileIDs.remove(profileID)
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = .inactive
                    DaemonDiagnostics.record("status_transition", fields: [
                        "id": diagnosticID, "from": "deactivating", "to": "inactive"
                    ])
                    DaemonDiagnostics.recordStage(diagnosticID, stage: "deactivation", outcome: "finished")
                }
            } catch {
                let stopError = error
                let refreshedStatus: TunnelStatus
                do {
                    let statuses = try DaemonControlClient(socketPath: Self.controlSocketPath).list()
                    refreshedStatus = Self.tunnelStatus(for: profileID, statuses: statuses)
                    if DaemonTunnelState.isAuthoritativelyAbsent(
                        profileID: profileID, statuses: statuses
                    ) {
                        self.uncertainDaemonProfileIDs.remove(profileID)
                    }
                } catch {
                    self.uncertainDaemonProfileIDs.insert(profileID)
                    refreshedStatus = .reasserting
                }
                DispatchQueue.main.async { [weak self, weak tunnel] in
                    guard let self, let tunnel, self.tunnels.contains(tunnel) else { return }
                    tunnel.status = refreshedStatus
                    DaemonDiagnostics.record("status_transition", fields: [
                        "id": diagnosticID, "from": "deactivating",
                        "to": refreshedStatus == .inactive ? "inactive" : "reasserting"
                    ])
                    DaemonDiagnostics.recordStage(diagnosticID, stage: "deactivation", outcome: "failed")
                    self.activationDelegate?.tunnelActivationAttemptFailed(
                        tunnel: tunnel,
                        error: .daemonModeOperationFailed(systemError: stopError)
                    )
                }
            }
        }
    }

    override func refreshStatuses() {
        let knownProfileIDs = Set(tunnels.compactMap(\.daemonProfileID))
        mutationQueue.async { [weak self] in
            guard let self else { return }
            let result = Result { try DaemonControlClient(socketPath: Self.controlSocketPath).list() }
            if case .success(let statuses) = result,
               Set(statuses.map(\.id)).isSubset(of: knownProfileIDs) {
                self.uncertainDaemonProfileIDs.formIntersection(statuses.map(\.id))
            }
            DispatchQueue.main.async {
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
        guard Set(statuses.map(\.id)).isSubset(of: Set(tunnels.compactMap(\.daemonProfileID))) else {
            markDaemonStatusesDegraded()
            return
        }
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

    private func daemonProfiles() -> [UUID: (name: String, configuration: TunnelConfiguration)] {
        Dictionary(uniqueKeysWithValues: tunnels.compactMap { tunnel in
            guard let profileID = tunnel.daemonProfileID,
                  let configuration = tunnel.tunnelConfiguration
            else { return nil }
            return (profileID, (name: tunnel.name, configuration: configuration))
        })
    }

    private static func activeDaemonTunnels(
        from statuses: [DaemonControlProfileStatus],
        localProfiles: [UUID: (name: String, configuration: TunnelConfiguration)],
        excluding activatingProfileID: UUID
    ) -> [(name: String, configuration: TunnelConfiguration)]? {
        guard !statuses.contains(where: { $0.id == activatingProfileID }) else { return nil }
        var activeTunnels: [(name: String, configuration: TunnelConfiguration)] = []
        for status in statuses {
            guard let profile = localProfiles[status.id] else { return nil }
            activeTunnels.append((name: profile.name, configuration: profile.configuration))
        }
        return activeTunnels
    }

    private static func activationError(
        for error: MacOSDaemonSingleSplitProfileError, activating name: String
    ) -> TunnelsManagerActivationAttemptError {
        switch error {
        case .unsupportedConfiguration:
            return .daemonModeUnsupportedProfile
        case .routePlan(.routeOwnership(.routeConflict(let conflict))):
            return .routeConflict(
                tunnelName: name, route: conflict.route.stringRepresentation, ownerName: conflict.ownerName)
        case .routePlan(.routeOwnership(.missingEndpointExclusion(let tunnelName, let endpoint))):
            return .missingEndpointExclusion(tunnelName: tunnelName, endpoint: endpoint)
        case .routePlan:
            return .daemonModeUnsupportedProfile
        }
    }

    private static func isIPv4FullTunnel(_ configuration: TunnelConfiguration) -> Bool {
        configuration.peers.contains { peer in
            peer.allowedIPs.contains { allowedIP in
                allowedIP.address.rawValue.count == 4 && allowedIP.networkPrefixLength == 0
            }
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
