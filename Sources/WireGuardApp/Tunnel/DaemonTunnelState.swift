// SPDX-License-Identifier: MIT

import Foundation

enum DaemonTunnelState: Equatable {
    case inactive
    case running
    case degraded

    static func current(for profileID: UUID, statuses: [DaemonControlProfileStatus]) -> DaemonTunnelState {
        guard let status = statuses.first(where: { $0.id == profileID }) else { return .inactive }
        switch status.state {
        case .running:
            return .running
        case .degraded:
            return .degraded
        }
    }

    static func isAuthoritativelyAbsent(
        profileID: UUID,
        statuses: [DaemonControlProfileStatus],
        knownProfileIDs: Set<UUID>
    ) -> Bool {
        !statuses.contains(where: { $0.id == profileID }) &&
            Set(statuses.map(\.id)).isSubset(of: knownProfileIDs)
    }
}
