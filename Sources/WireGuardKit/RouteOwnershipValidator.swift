// SPDX-License-Identifier: MIT

import Foundation
import Network

public struct RouteOwnershipConflict: Equatable {
    public let route: IPAddressRange
    public let ownerName: String

    public init(route: IPAddressRange, ownerName: String) {
        self.route = route
        self.ownerName = ownerName
    }
}

public enum RouteOwnershipValidationError: Equatable {
    case routeConflict(RouteOwnershipConflict)
    case missingEndpointExclusion(tunnelName: String, endpoint: String)
}

public enum RouteOwnershipValidator {
    public static func validationError(
        activating name: String, configuration: TunnelConfiguration,
        against activeTunnels: [(name: String, configuration: TunnelConfiguration)]
    ) -> RouteOwnershipValidationError? {
        if let conflict = conflict(configuration: configuration, against: activeTunnels) {
            return .routeConflict(conflict)
        }

        for activeTunnel in activeTunnels {
            if let endpoint = unexcludedEndpoint(
                of: activeTunnel.configuration, requiredBy: configuration)
            {
                return .missingEndpointExclusion(tunnelName: name, endpoint: endpoint)
            }
            if let endpoint = unexcludedEndpoint(
                of: configuration, requiredBy: activeTunnel.configuration)
            {
                return .missingEndpointExclusion(
                    tunnelName: activeTunnel.name, endpoint: endpoint)
            }
        }
        return nil
    }

    public static func conflict(
        configuration: TunnelConfiguration,
        against activeTunnels: [(name: String, configuration: TunnelConfiguration)]
    ) -> RouteOwnershipConflict? {
        let candidate = routes(for: configuration)

        for activeTunnel in activeTunnels {
            let activeRoutes = routes(for: activeTunnel.configuration)
            for candidateRoute in candidate {
                for activeRoute in activeRoutes where intersects(candidateRoute, activeRoute) {
                    if hasDefaultRoute(configuration, familyOf: candidateRoute)
                        != hasDefaultRoute(activeTunnel.configuration, familyOf: activeRoute)
                    {
                        continue
                    }
                    return RouteOwnershipConflict(
                        route: moreSpecific(candidateRoute, activeRoute), ownerName: activeTunnel.name)
                }
            }
        }
        return nil
    }

    private static func routes(for configuration: TunnelConfiguration) -> [IPAddressRange] {
        configuration.peers.flatMap { peer in
            peer.allowedIPs.flatMap { allowedIP in
                peer.excludeIPs.reduce([allowedIP]) { routes, excludedIP in
                    routes.flatMap { subtract($0, excludedIP) }
                }
            }
        }
    }

    private static func hasDefaultRoute(_ configuration: TunnelConfiguration, familyOf route: IPAddressRange) -> Bool {
        configuration.peers.contains { peer in
            peer.allowedIPs.contains { $0.networkPrefixLength == 0 && sameFamily($0, route) }
        }
    }

    private static func unexcludedEndpoint(
        of endpointConfiguration: TunnelConfiguration, requiredBy fullTunnelConfiguration: TunnelConfiguration
    ) -> String? {
        for peer in endpointConfiguration.peers {
            guard let endpoint = peer.endpoint else { continue }
            if case .name = endpoint.host {
                guard fullTunnelConfiguration.peers.contains(where: { peer in
                    peer.allowedIPs.contains { $0.networkPrefixLength == 0 }
                }) else { continue }
                return "\(endpoint.stringRepresentation) (hostname; use a literal IP endpoint)"
            }
            guard let endpointRange = endpointRange(endpoint) else { continue }
            guard hasDefaultRoute(fullTunnelConfiguration, familyOf: endpointRange) else { continue }
            if !fullTunnelConfiguration.peers.contains(where: { peer in
                peer.excludeIPs.contains { contains($0, endpointRange) }
            }) {
                return endpointRange.stringRepresentation
            }
        }
        return nil
    }

    private static func endpointRange(_ endpoint: Endpoint) -> IPAddressRange? {
        switch endpoint.host {
        case .ipv4(let address):
            return IPAddressRange(address: address, networkPrefixLength: 32)
        case .ipv6(let address):
            return IPAddressRange(address: address, networkPrefixLength: 128)
        case .name:
            return nil
        @unknown default:
            return nil
        }
    }

    private static func subtract(_ route: IPAddressRange, _ exclusion: IPAddressRange) -> [IPAddressRange] {
        guard sameFamily(route, exclusion), intersects(route, exclusion) else { return [route] }
        if contains(exclusion, route) { return [] }
        guard contains(route, exclusion) else { return [route] }

        let prefix = route.networkPrefixLength
        let left = IPAddressRange(address: route.maskedAddress(), networkPrefixLength: prefix + 1)
        var rightAddress = Data(route.maskedAddress().rawValue)
        let byteIndex = Int(prefix / 8)
        rightAddress[byteIndex] |= UInt8(1 << (7 - (prefix % 8)))
        let right: IPAddressRange
        if route.address is IPv4Address {
            right = IPAddressRange(address: IPv4Address(rightAddress)!, networkPrefixLength: prefix + 1)
        } else {
            right = IPAddressRange(address: IPv6Address(rightAddress)!, networkPrefixLength: prefix + 1)
        }
        return subtract(left, exclusion) + subtract(right, exclusion)
    }

    private static func intersects(_ lhs: IPAddressRange, _ rhs: IPAddressRange) -> Bool {
        sameFamily(lhs, rhs) && (contains(lhs, rhs) || contains(rhs, lhs))
    }

    private static func contains(_ lhs: IPAddressRange, _ rhs: IPAddressRange) -> Bool {
        guard lhs.networkPrefixLength <= rhs.networkPrefixLength else { return false }
        let lhsAddress = lhs.maskedAddress().rawValue
        let rhsAddress = rhs.maskedAddress().rawValue
        let fullBytes = Int(lhs.networkPrefixLength / 8)
        guard lhsAddress.prefix(fullBytes) == rhsAddress.prefix(fullBytes) else { return false }
        let remainingBits = Int(lhs.networkPrefixLength % 8)
        guard remainingBits > 0 else { return true }
        let mask = UInt8.max << (8 - remainingBits)
        return lhsAddress[fullBytes] & mask == rhsAddress[fullBytes] & mask
    }

    private static func sameFamily(_ lhs: IPAddressRange, _ rhs: IPAddressRange) -> Bool {
        (lhs.address is IPv4Address) == (rhs.address is IPv4Address)
    }

    private static func moreSpecific(_ lhs: IPAddressRange, _ rhs: IPAddressRange) -> IPAddressRange {
        let route = lhs.networkPrefixLength >= rhs.networkPrefixLength ? lhs : rhs
        return IPAddressRange(address: route.maskedAddress(), networkPrefixLength: route.networkPrefixLength)
    }
}
