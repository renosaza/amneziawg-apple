// SPDX-License-Identifier: MIT

import XCTest
@testable import WireGuardKit

final class RouteOwnershipValidatorTests: XCTestCase {
    func testAllowsFullTunnelWithMoreSpecificSplitTunnel() {
        XCTAssertNil(RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["0.0.0.0/0"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"]))]))
    }

    func testExclusionsRemoveFullTunnelOwnership() {
        XCTAssertNil(RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["0.0.0.0/0"], excluded: ["10.25.0.0/24"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"]))]))
    }

    func testRejectsEqualSplitRoutes() {
        let conflict = RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["10.25.0.0/24"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"]))])

        XCTAssertEqual(conflict?.route.stringRepresentation, "10.25.0.0/24")
        XCTAssertEqual(conflict?.ownerName, "CM")
    }

    func testRejectsOverlappingSplitRoutes() {
        XCTAssertNotNil(RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["10.25.0.0/24"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/16"]))]))
    }

    func testRejectsTwoFullTunnelsInOneAddressFamily() {
        XCTAssertNotNil(RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["0.0.0.0/0"]),
            against: [("full-A", configuration(allowed: ["0.0.0.0/0"]))]))
    }

    func testTreatsIPv4AndIPv6Independently() {
        XCTAssertNil(RouteOwnershipValidator.conflict(
            configuration: configuration(allowed: ["0.0.0.0/0"]),
            against: [("IPv6", configuration(allowed: ["::/0"]))]))
    }

    func testRequiresFullTunnelToExcludeActiveStaticEndpoint() {
        let error = RouteOwnershipValidator.validationError(
            activating: "sticky", configuration: configuration(allowed: ["0.0.0.0/0"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"], endpoint: "192.0.2.10:51820"))])

        XCTAssertEqual(error, .missingEndpointExclusion(tunnelName: "sticky", endpoint: "192.0.2.10/32"))
    }

    func testAllowsFullTunnelThatExcludesActiveStaticEndpoint() {
        XCTAssertNil(RouteOwnershipValidator.validationError(
            activating: "sticky",
            configuration: configuration(allowed: ["0.0.0.0/0"], excluded: ["192.0.2.10/32"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"], endpoint: "192.0.2.10:51820"))]))
    }

    func testRejectsSplitTunnelWhenActiveFullTunnelMissesItsStaticEndpoint() {
        let error = RouteOwnershipValidator.validationError(
            activating: "CM", configuration(allowed: ["10.25.0.0/24"], endpoint: "192.0.2.10:51820"),
            against: [("sticky", configuration(allowed: ["0.0.0.0/0"]))])

        XCTAssertEqual(error, .missingEndpointExclusion(tunnelName: "sticky", endpoint: "192.0.2.10/32"))
    }

    func testRejectsFullTunnelAlongsideHostnameEndpoint() {
        let error = RouteOwnershipValidator.validationError(
            activating: "sticky", configuration: configuration(allowed: ["0.0.0.0/0"]),
            against: [("CM", configuration(allowed: ["10.25.0.0/24"], endpoint: "vpn.example.test:51820"))])

        XCTAssertEqual(
            error,
            .missingEndpointExclusion(
                tunnelName: "sticky", endpoint: "vpn.example.test:51820 (hostname; use a literal IP endpoint)"))
    }

    func testRejectsHostnameEndpointWhenFullTunnelIsAlreadyActive() {
        let error = RouteOwnershipValidator.validationError(
            activating: "CM", configuration(allowed: ["10.25.0.0/24"], endpoint: "vpn.example.test:51820"),
            against: [("sticky", configuration(allowed: ["0.0.0.0/0"]))])

        XCTAssertEqual(
            error,
            .missingEndpointExclusion(
                tunnelName: "sticky", endpoint: "vpn.example.test:51820 (hostname; use a literal IP endpoint)"))
    }

    func testAllowsSingleTunnelWithHostnameEndpoint() {
        XCTAssertNil(RouteOwnershipValidator.validationError(
            activating: "CM",
            configuration: configuration(allowed: ["10.25.0.0/24"], endpoint: "vpn.example.test:51820"),
            against: []))
    }

    private func configuration(
        allowed: [String], excluded: [String] = [], endpoint: String? = nil
    ) -> TunnelConfiguration {
        var peer = PeerConfiguration(publicKey: PrivateKey().publicKey)
        peer.allowedIPs = allowed.map { IPAddressRange(from: $0)! }
        peer.excludeIPs = excluded.map { IPAddressRange(from: $0)! }
        if let endpoint {
            peer.endpoint = Endpoint(from: endpoint)
        }
        let interface = InterfaceConfiguration(privateKey: PrivateKey())
        return TunnelConfiguration(name: nil, interface: interface, peers: [peer])
    }
}
