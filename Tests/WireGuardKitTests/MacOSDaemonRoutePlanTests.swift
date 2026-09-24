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

    func testNormalizesSingleIPv4InterfaceAddressToHostRoute() throws {
        let plan = try MacOSDaemonRoutePlan.build(
            activating: "CM", configuration: configuration(allowed: ["10.25.0.0/24"], address: ["10.25.0.2/24"]),
            resolvedEndpoints: [nil], activeTunnels: []).get()

        XCTAssertEqual(plan.localAddress, "10.25.0.2/32")
    }

    func testBuildsSingleIPv4SplitPlanWithLiteralEndpoint() throws {
        let endpoint = Endpoint(from: "198.51.100.10:51820")!
        let plan = try MacOSDaemonRoutePlan.buildSingleIPv4Split(
            activating: "CM",
            configuration: configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint)
        ).get()

        XCTAssertEqual(plan.localAddress, "192.0.2.2/32")
        XCTAssertEqual(plan.routes, [
            .init(destination: "10.25.0.0/24", owner: .tunnel),
            .init(destination: "198.51.100.10/32", owner: .physicalEndpoint)
        ])
    }

    func testRejectsUnsupportedSingleIPv4SplitProfiles() {
        let endpoint = Endpoint(from: "198.51.100.10:51820")!
        let hostname = Endpoint(from: "vpn.example.test:51820")!
        var dnsConfiguration = configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint)
        dnsConfiguration.interface.dns = [DNSServer(from: "192.0.2.53")!]
        var excludedConfiguration = configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint)
        excludedConfiguration.peers[0].excludeIPs = [IPAddressRange(from: "10.25.0.1/32")!]
        var twoPeerConfiguration = configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint)
        twoPeerConfiguration.peers.append(PeerConfiguration(publicKey: PrivateKey().publicKey))

        let configurations = [
            configuration(allowed: ["0.0.0.0/0"], endpoint: endpoint),
            configuration(allowed: ["128.0.0.0/1"], endpoint: endpoint),
            configuration(allowed: ["2001:db8::/64"], endpoint: endpoint),
            configuration(allowed: ["240.0.0.0/24"], endpoint: endpoint),
            configuration(allowed: ["10.25.0.0/24"], endpoint: hostname),
            configuration(allowed: ["10.25.0.0/24"], endpoint: Endpoint(from: "127.0.0.1:51820")!),
            configuration(allowed: ["10.25.0.0/24"], endpoint: endpoint, address: ["2001:db8::2/64"]),
            dnsConfiguration,
            excludedConfiguration,
            twoPeerConfiguration
        ]
        for configuration in configurations {
            XCTAssertEqual(
                MacOSDaemonRoutePlan.buildSingleIPv4Split(
                    activating: "CM", configuration: configuration),
                .failure(.unsupportedConfiguration)
            )
        }
    }

    func testSingleIPv4SplitRejectsUnsuitableLocalAddress() {
        let result = MacOSDaemonRoutePlan.buildSingleIPv4Split(
            activating: "CM",
            configuration: configuration(
                allowed: ["10.25.0.0/24"],
                endpoint: Endpoint(from: "198.51.100.10:51820")!,
                address: ["127.0.0.1/24"]
            )
        )

        XCTAssertEqual(result, .failure(.routePlan(.invalidInterfaceAddress)))
    }

    func testRejectsMissingMultipleIPv6AndUnsuitableInterfaceAddresses() {
        for addresses in [
            [], ["10.25.0.2/24", "10.25.0.3/24"], ["2001:db8::2/64"],
            ["0.0.0.0/32"], ["0.1.2.3/32"], ["127.0.0.1/32"],
            ["169.254.1.1/32"], ["224.0.0.1/32"], ["255.255.255.255/32"]
        ] {
            let result = MacOSDaemonRoutePlan.build(
                activating: "CM", configuration: configuration(allowed: ["10.25.0.0/24"], address: addresses),
                resolvedEndpoints: [nil], activeTunnels: [])

            XCTAssertEqual(result, .failure(.invalidInterfaceAddress))
        }
    }

    func testIPv4AndIPv6RemainIndependentAndSerializationHasNoKeys() throws {
        let active = configuration(allowed: ["::/0"])
        let activating = configuration(allowed: ["0.0.0.0/0"])
        let plan = try MacOSDaemonRoutePlan.build(
            activating: "IPv4", configuration: activating, resolvedEndpoints: [nil], activeTunnels: [("IPv6", active)]).get()

        XCTAssertTrue(plan.routes.contains(.init(destination: "0.0.0.0/0", owner: .tunnel)))
        XCTAssertFalse(plan.routes.contains(.init(destination: "::/0", owner: .tunnel)))
        let serialized = String(data: try JSONEncoder().encode(plan), encoding: .utf8)!
        let JSON = try JSONSerialization.jsonObject(with: Data(serialized.utf8)) as? [String: Any]
        XCTAssertEqual(JSON?["local_address"] as? String, "192.0.2.2/32")
        XCTAssertFalse(serialized.contains(activating.interface.privateKey.base64Key))
        XCTAssertFalse(serialized.contains(activating.peers[0].preSharedKey!.base64Key))
        XCTAssertFalse(serialized.contains(active.peers[0].publicKey.base64Key))
    }

    private func configuration(
        allowed: [String], excluded: [String] = [], endpoint: Endpoint? = nil,
        address: [String] = ["192.0.2.2/24"]
    ) -> TunnelConfiguration {
        var interface = InterfaceConfiguration(privateKey: PrivateKey())
        interface.addresses = address.map { IPAddressRange(from: $0)! }
        var peer = PeerConfiguration(publicKey: PrivateKey().publicKey)
        peer.preSharedKey = PreSharedKey(rawValue: PrivateKey().rawValue)!
        peer.allowedIPs = allowed.map { IPAddressRange(from: $0)! }
        peer.excludeIPs = excluded.map { IPAddressRange(from: $0)! }
        peer.endpoint = endpoint
        return TunnelConfiguration(name: nil, interface: interface, peers: [peer])
    }
}
