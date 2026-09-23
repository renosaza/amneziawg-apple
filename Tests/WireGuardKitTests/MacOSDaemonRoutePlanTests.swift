// SPDX-License-Identifier: MIT

import XCTest
@testable import WireGuardKit

final class MacOSDaemonRoutePlanTests: XCTestCase {
    func testAllowsFullTunnelWithActiveSplitTunnel() throws {
        let result = MacOSDaemonRoutePlan.build(
            activating: "sticky", configuration: configuration(allowed: ["0.0.0.0/0"]), resolvedEndpoints: [nil],
            activeTunnels: [("CM", configuration(allowed: ["10.25.0.0/24"]))])

        let plan = try result.get()
        XCTAssertEqual(plan.routes, [.init(destination: "0.0.0.0/0", owner: .tunnel)])
    }

    func testRejectsOverlappingSplitOwnership() {
        let result = MacOSDaemonRoutePlan.build(
            activating: "A", configuration: configuration(allowed: ["10.25.0.0/24"]), resolvedEndpoints: [nil],
            activeTunnels: [("B", configuration(allowed: ["10.25.0.0/16"]))])

        XCTAssertEqual(result, .failure(.routeOwnership(.routeConflict(
            RouteOwnershipConflict(route: IPAddressRange(from: "10.25.0.0/24")!, ownerName: "B")))))
    }

    func testExcludedEndpointIsPhysicalRoute() throws {
        let endpoint = Endpoint(from: "192.0.2.10:51820")!
        let result = MacOSDaemonRoutePlan.build(
            activating: "sticky",
            configuration: configuration(
                allowed: ["0.0.0.0/0"], excluded: ["192.0.2.10/32"], endpoint: endpoint),
            resolvedEndpoints: [endpoint],
            activeTunnels: [("CM", configuration(allowed: ["10.25.0.0/24"]))])

        let plan = try result.get()
        XCTAssertTrue(plan.routes.contains(.init(destination: "192.0.2.10/32", owner: .physicalEndpoint)))
        XCTAssertFalse(plan.routes.contains(.init(destination: "192.0.2.10/32", owner: .tunnel)))
    }

    func testActiveEndpointRequiresExclusionButIsNotAddedToCandidatePlan() {
        let endpoint = Endpoint(from: "192.0.2.10:51820")!
        let result = MacOSDaemonRoutePlan.build(
            activating: "sticky", configuration: configuration(allowed: ["0.0.0.0/0"]), resolvedEndpoints: [nil],
            activeTunnels: [("CM", configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint))])

        XCTAssertEqual(result, .failure(.routeOwnership(
            .missingEndpointExclusion(tunnelName: "sticky", endpoint: "192.0.2.10/32"))))
    }

    func testRejectsUnalignedResolvedEndpoint() {
        let result = MacOSDaemonRoutePlan.build(
            activating: "CM", configuration: configuration(allowed: ["10.25.0.0/24"], endpoint: Endpoint(from: "192.0.2.10:51820")!),
            resolvedEndpoints: [Endpoint(from: "192.0.2.11:51820")!], activeTunnels: [])

        XCTAssertEqual(result, .failure(.invalidResolvedEndpoints))
    }

    func testIPv4AndIPv6RemainIndependentAndSerializationHasNoKeys() throws {
        let active = configuration(allowed: ["::/0"])
        let activating = configuration(allowed: ["0.0.0.0/0"])
        let plan = try MacOSDaemonRoutePlan.build(
            activating: "IPv4", configuration: activating, resolvedEndpoints: [nil], activeTunnels: [("IPv6", active)]).get()

        XCTAssertTrue(plan.routes.contains(.init(destination: "0.0.0.0/0", owner: .tunnel)))
        XCTAssertFalse(plan.routes.contains(.init(destination: "::/0", owner: .tunnel)))
        let serialized = String(data: try JSONEncoder().encode(plan), encoding: .utf8)!
        XCTAssertFalse(serialized.contains(activating.interface.privateKey.base64Key))
        XCTAssertFalse(serialized.contains(activating.peers[0].preSharedKey!.base64Key))
        XCTAssertFalse(serialized.contains(active.peers[0].publicKey.base64Key))
    }

    private func configuration(
        allowed: [String], excluded: [String] = [], endpoint: Endpoint? = nil
    ) -> TunnelConfiguration {
        var peer = PeerConfiguration(publicKey: PrivateKey().publicKey)
        peer.preSharedKey = PreSharedKey(rawValue: PrivateKey().rawValue)!
        peer.allowedIPs = allowed.map { IPAddressRange(from: $0)! }
        peer.excludeIPs = excluded.map { IPAddressRange(from: $0)! }
        peer.endpoint = endpoint
        return TunnelConfiguration(name: nil, interface: InterfaceConfiguration(privateKey: PrivateKey()), peers: [peer])
    }
}
