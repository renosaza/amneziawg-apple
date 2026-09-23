// SPDX-License-Identifier: MIT

import XCTest
@testable import WireGuardKit

final class NetworkChangeEndpointTests: XCTestCase {
    func testNetworkChangeResetsLiteralPeerEndpoint() {
        let endpoint = Endpoint(from: "192.0.2.1:51820")!
        var peer = PeerConfiguration(publicKey: PrivateKey().publicKey)
        peer.endpoint = endpoint
        let configuration = TunnelConfiguration(
            name: nil,
            interface: InterfaceConfiguration(privateKey: PrivateKey()),
            peers: [peer]
        )
        let generator = PacketTunnelSettingsGenerator(
            tunnelConfiguration: configuration,
            resolvedEndpoints: [endpoint]
        )

        let (uapiConfiguration, resolutionResults, endpointsChanged) = generator.endpointUapiConfigurationAfterNetworkChange()

        XCTAssertTrue(uapiConfiguration.contains("public_key=\(peer.publicKey.hexKey)\n"))
        XCTAssertTrue(uapiConfiguration.contains("endpoint=192.0.2.1:51820\n"))
        XCTAssertNil(resolutionResults[0])
        XCTAssertFalse(endpointsChanged)
    }
}
