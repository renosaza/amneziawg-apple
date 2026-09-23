// SPDX-License-Identifier: MIT

import XCTest
@testable import WireGuardKit

final class ExcludeIPsTests: XCTestCase {
    func testExcludeIPsAffectEqualityAndHashing() {
        let publicKey = PrivateKey().publicKey
        var first = PeerConfiguration(publicKey: publicKey)
        var second = PeerConfiguration(publicKey: publicKey)
        first.excludeIPs = [IPAddressRange(from: "192.0.2.0/24")!]

        XCTAssertNotEqual(first, second)

        second.excludeIPs = first.excludeIPs
        XCTAssertEqual(first, second)
        XCTAssertEqual(Set([first, second]).count, 1)
    }

    func testExcludeIPsRoundTrip() throws {
        let privateKey = PrivateKey()
        let peerKey = PrivateKey().publicKey
        let config = """
        [Interface]
        PrivateKey = \(privateKey.base64Key)
        Address = 192.0.2.2/32

        [Peer]
        PublicKey = \(peerKey.base64Key)
        AllowedIPs = 0.0.0.0/0
        ExcludeIPs = 192.0.2.0/24
        ExcludeIPs = 2001:db8::/32
        """

        let parsed = try TunnelConfiguration(fromWgQuickConfig: config)
        XCTAssertEqual(parsed.peers[0].excludeIPs.map(\.stringRepresentation), ["192.0.2.0/24", "2001:db8::/32"])

        let reparsed = try TunnelConfiguration(fromWgQuickConfig: parsed.asWgQuickConfig())
        XCTAssertEqual(reparsed, parsed)
    }

    func testExcludeIPsRejectInvalidCIDR() {
        let privateKey = PrivateKey()
        let peerKey = PrivateKey().publicKey
        let config = """
        [Interface]
        PrivateKey = \(privateKey.base64Key)

        [Peer]
        PublicKey = \(peerKey.base64Key)
        ExcludeIPs = 192.0.2.0/33
        """

        XCTAssertThrowsError(try TunnelConfiguration(fromWgQuickConfig: config))
    }

    func testCIDRPrefixMustMatchAddressFamily() {
        XCTAssertNil(IPAddressRange(from: "192.0.2.0/33"))
        XCTAssertNil(IPAddressRange(from: "2001:db8::/129"))
        XCTAssertNotNil(IPAddressRange(from: "192.0.2.0/32"))
        XCTAssertNotNil(IPAddressRange(from: "2001:db8::/128"))
    }
}
