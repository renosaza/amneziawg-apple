// SPDX-License-Identifier: MIT

import Darwin
import Foundation

@main
struct DaemonControlClientSelfTest {
    static func main() {
        do {
            try testFraming()
            try testSocketTransport()
            try testResponses()
            try testHelloProtocol()
            try testDaemonTunnelState()
            try testLifecycleProtocol()
            try testAmbiguousStartRecoveryProtocol()
            print("daemon-control Swift client self-check passed")
        } catch {
            fputs("daemon-control Swift client self-check failed: \(error)\n", stderr)
            exit(1)
        }
    }

    private static func testFraming() throws {
        let body = Data("{}".utf8)
        try expect(DaemonControlProtocol.isValidSocketPath("/var/run/amneziawg/control.sock"))
        try expect(!DaemonControlProtocol.isValidSocketPath("/var//run/control.sock"))
        try expectFailure { _ = try DaemonControlClient(socketPath: "/var/run/amneziawg/control.sock", timeout: .infinity) }
        let framed = try DaemonControlProtocol.frame(body)
        let unframed = try DaemonControlProtocol.unframe(framed)
        try expect(unframed == body)
        try expectFailure { _ = try DaemonControlProtocol.unframe(Data([0, 0, 0])) }
        try expectFailure { _ = try DaemonControlProtocol.unframe(Data([0, 0, 0, 0])) }
        try expectFailure { _ = try DaemonControlProtocol.unframe(Data([0, 0, 16, 1])) }
        try expectFailure { _ = try DaemonControlProtocol.unframe(Data([0, 0, 0, 2, 123])) }
    }

    private static func testResponses() throws {
        let id = UUID(uuidString: "11111111-2222-4333-8444-555555555555")!
        let emptyList = try DaemonControlProtocol.decodeListResponse(Data("{\"ok\":true}".utf8))
        try expect(emptyList.isEmpty)
        let list = try DaemonControlProtocol.decodeListResponse(Data("{\"ok\":true,\"profiles\":[{\"id\":\"11111111-2222-4333-8444-555555555555\",\"status\":\"running\"},{\"id\":\"22222222-2222-4333-8444-555555555555\",\"status\":\"degraded\"}]}".utf8))
        try expect(list.map(\.state) == [.running, .degraded])
        let status = try DaemonControlProtocol.decodeStatusResponse(Data("{\"ok\":true,\"profile\":{\"id\":\"11111111-2222-4333-8444-555555555555\",\"status\":\"running\"}}".utf8), expectedProfileID: id)
        try expect(status.id == id && status.state == .running)
        try expectFailure {
            _ = try DaemonControlProtocol.decodeListResponse(Data("{\"ok\":true,\"profiles\":[{\"id\":\"11111111-2222-4333-8444-555555555555\",\"status\":\"unknown\"}]}".utf8))
        }
        try expectFailure {
            _ = try DaemonControlProtocol.decodeStatusResponse(Data("{\"ok\":true,\"profile\":{\"id\":\"22222222-2222-4333-8444-555555555555\",\"status\":\"running\"}}".utf8), expectedProfileID: id)
        }
        try expectFailure {
            _ = try DaemonControlProtocol.decodeListResponse(Data("{\"ok\":false,\"error\":\"not_found\"}".utf8))
        }
    }

    private static func testSocketTransport() throws {
        var descriptors = [Int32](repeating: -1, count: 2)
        guard Darwin.socketpair(AF_UNIX, SOCK_STREAM, 0, &descriptors) == 0 else {
            throw SelfTestError.socketPairFailed
        }
        defer {
            _ = Darwin.close(descriptors[0])
            _ = Darwin.close(descriptors[1])
        }

        let response = Data("{\"ok\":true,\"profiles\":[]}".utf8)
        let deadline = DaemonControlProtocol.deadline(after: 1)
        try DaemonControlProtocol.writeFrame(response, to: descriptors[1], deadline: deadline)
        let received = try DaemonControlProtocol.readFrame(from: descriptors[0], deadline: deadline)
        try expect(received == response)
    }

    private static func testHelloProtocol() throws {
        let hello = try DaemonControlProtocol.makeRequest(operation: "hello", profileID: nil)
        let request = try requestObject(hello)
        try expect(request["version"] as? Int == DaemonControlProtocol.protocolVersion)
        try expect(request["operation"] as? String == "hello")
        try expect(request["profile_id"] == nil)
        try expect(request["config"] == nil)

        let noCapabilities = try DaemonControlProtocol.decodeHelloResponse(
            Data("{\"ok\":true,\"protocol_version\":1}".utf8)
        )
        try expect(!noCapabilities.supportsIPv4FullRoute)
        let fullRoute = try DaemonControlProtocol.decodeHelloResponse(
            Data("{\"ok\":true,\"protocol_version\":1,\"capabilities\":[\"ipv4-full-route\",\"future-capability\"]}".utf8)
        )
        try expect(fullRoute.supportsIPv4FullRoute)
        for response in [
            "{\"ok\":true}",
            "{\"ok\":true,\"protocol_version\":2}",
            "{\"ok\":true,\"protocol_version\":1,\"capabilities\":[\"ipv4-full-route\",\"ipv4-full-route\"]}",
            "{\"ok\":false,\"error\":\"invalid_request\"}",
            "{\"ok\":true,\"protocol_version\":1,\"profiles\":[]}" // Hello must not carry state.
        ] {
            try expectIncompatibleDaemon {
                _ = try DaemonControlProtocol.decodeHelloResponse(Data(response.utf8))
            }
        }
    }

    private static func testDaemonTunnelState() throws {
        let profileID = UUID(uuidString: "11111111-2222-4333-8444-555555555555")!
        let otherProfileID = UUID(uuidString: "22222222-2222-4333-8444-555555555555")!

        try expect(DaemonTunnelState.current(for: profileID, statuses: []) == .inactive)
        try expect(
            DaemonTunnelState.current(
                for: profileID,
                statuses: [DaemonControlProfileStatus(id: profileID, state: .running)]
            ) == .running
        )
        try expect(
            DaemonTunnelState.current(
                for: profileID,
                statuses: [DaemonControlProfileStatus(id: otherProfileID, state: .degraded)]
            ) == .inactive
        )
        try expect(
            DaemonTunnelState.current(
                for: profileID,
                statuses: [DaemonControlProfileStatus(id: profileID, state: .degraded)]
            ) == .degraded
        )
        try expect(DaemonTunnelState.isAuthoritativelyAbsent(
            profileID: profileID, statuses: [], knownProfileIDs: [profileID]
        ))
        try expect(!DaemonTunnelState.isAuthoritativelyAbsent(
            profileID: profileID,
            statuses: [DaemonControlProfileStatus(id: profileID, state: .running)],
            knownProfileIDs: [profileID]
        ))
        try expect(!DaemonTunnelState.isAuthoritativelyAbsent(
            profileID: profileID,
            statuses: [DaemonControlProfileStatus(id: otherProfileID, state: .running)],
            knownProfileIDs: [profileID]
        ))
    }

    private static func testLifecycleProtocol() throws {
        var descriptors = [Int32](repeating: -1, count: 2)
        guard Darwin.socketpair(AF_UNIX, SOCK_STREAM, 0, &descriptors) == 0 else {
            throw SelfTestError.socketPairFailed
        }
        defer {
            _ = Darwin.close(descriptors[0])
            _ = Darwin.close(descriptors[1])
        }

        let profileID = UUID(uuidString: "11111111-2222-4333-8444-555555555555")!
        let testConfiguration = "private_key=test-only\n"
        let deadline = DaemonControlProtocol.deadline(after: 1)
        let routePlan = SyntheticRoutePlan(
            localAddress: "192.0.2.2/32",
            routes: [
                .init(destination: "10.25.0.0/24", owner: "tunnel"),
                .init(destination: "198.51.100.10/32", owner: "physicalEndpoint")
            ]
        )
        let start = try DaemonControlProtocol.makeRequest(
            operation: "start",
            profileID: profileID,
            uapiConfiguration: testConfiguration,
            routePlan: routePlan
        )
        try DaemonControlProtocol.writeFrame(start, to: descriptors[0], deadline: deadline, sensitive: true)
        let startRequest = try requestObject(try DaemonControlProtocol.readFrame(from: descriptors[1], deadline: deadline))
        try expect(startRequest["version"] as? Int == 1)
        try expect(startRequest["operation"] as? String == "start")
        try expect(startRequest["profile_id"] as? String == profileID.uuidString.lowercased())
        try expect(startRequest["config"] as? String == testConfiguration)
        let routePlanObject = startRequest["route_plan"] as? [String: Any]
        try expect(routePlanObject?["local_address"] as? String == "192.0.2.2/32")
        let routes = routePlanObject?["routes"] as? [[String: Any]]
        try expect(routes?.count == 2)
        try expect(String(describing: routePlanObject).contains("test-only") == false)

        let startResponse = Data("{\"ok\":true,\"profile\":{\"id\":\"11111111-2222-4333-8444-555555555555\",\"status\":\"running\"}}".utf8)
        try DaemonControlProtocol.writeFrame(startResponse, to: descriptors[1], deadline: deadline)
        let started = try DaemonControlProtocol.decodeStartResponse(
            DaemonControlProtocol.readFrame(from: descriptors[0], deadline: deadline),
            expectedProfileID: profileID
        )
        try expect(started.id == profileID && started.state == .running)

        let stop = try DaemonControlProtocol.makeRequest(operation: "stop", profileID: profileID)
        try DaemonControlProtocol.writeFrame(stop, to: descriptors[0], deadline: deadline)
        let stopRequest = try requestObject(try DaemonControlProtocol.readFrame(from: descriptors[1], deadline: deadline))
        try expect(stopRequest["operation"] as? String == "stop")
        try expect(stopRequest["profile_id"] as? String == profileID.uuidString.lowercased())
        try expect(stopRequest["config"] == nil)
        try expect(stopRequest["route_plan"] == nil)

        let stopResponse = Data("{\"ok\":true,\"profile\":{\"id\":\"11111111-2222-4333-8444-555555555555\",\"status\":\"stopped\"}}".utf8)
        try DaemonControlProtocol.writeFrame(stopResponse, to: descriptors[1], deadline: deadline)
        try DaemonControlProtocol.decodeStopResponse(
            DaemonControlProtocol.readFrame(from: descriptors[0], deadline: deadline),
            expectedProfileID: profileID
        )

        try expectFailure {
            _ = try DaemonControlProtocol.makeRequest(
                operation: "start",
                profileID: profileID,
                uapiConfiguration: String(repeating: "x", count: DaemonControlProtocol.maximumConfigurationBytes + 1)
            )
        }
        let rejectedResponse = Data("{\"ok\":false,\"error\":\"private_key=test-only\",\"config\":\"private_key=test-only\"}".utf8)
        do {
            _ = try DaemonControlProtocol.decodeStartResponse(rejectedResponse, expectedProfileID: profileID)
            throw SelfTestError.expectedFailure
        } catch let error as DaemonControlClientError {
            try expect(error == .daemonRejected)
        }
    }

    private static func testAmbiguousStartRecoveryProtocol() throws {
        var descriptors = [Int32](repeating: -1, count: 2)
        guard Darwin.socketpair(AF_UNIX, SOCK_STREAM, 0, &descriptors) == 0 else {
            throw SelfTestError.socketPairFailed
        }
        defer {
            _ = Darwin.close(descriptors[0])
            _ = Darwin.close(descriptors[1])
        }

        let profileID = UUID(uuidString: "33333333-2222-4333-8444-555555555555")!
        let deadline = DaemonControlProtocol.deadline(after: 1)
        let list = try DaemonControlProtocol.makeRequest(operation: "list", profileID: nil)
        try DaemonControlProtocol.writeFrame(list, to: descriptors[0], deadline: deadline)
        let listRequest = try requestObject(try DaemonControlProtocol.readFrame(from: descriptors[1], deadline: deadline))
        try expect(listRequest["operation"] as? String == "list")
        try expect(listRequest["config"] == nil && listRequest["route_plan"] == nil)

        let running = Data("{\"ok\":true,\"profiles\":[{\"id\":\"33333333-2222-4333-8444-555555555555\",\"status\":\"running\"}]}".utf8)
        try DaemonControlProtocol.writeFrame(running, to: descriptors[1], deadline: deadline)
        let statuses = try DaemonControlProtocol.decodeListResponse(
            DaemonControlProtocol.readFrame(from: descriptors[0], deadline: deadline)
        )
        try expect(statuses == [.init(id: profileID, state: .running)])

        let stop = try DaemonControlProtocol.makeRequest(operation: "stop", profileID: profileID)
        try DaemonControlProtocol.writeFrame(stop, to: descriptors[0], deadline: deadline)
        let stopRequest = try requestObject(try DaemonControlProtocol.readFrame(from: descriptors[1], deadline: deadline))
        try expect(stopRequest["config"] == nil && stopRequest["route_plan"] == nil)
        let stopped = Data("{\"ok\":true,\"profile\":{\"id\":\"33333333-2222-4333-8444-555555555555\",\"status\":\"stopped\"}}".utf8)
        try DaemonControlProtocol.writeFrame(stopped, to: descriptors[1], deadline: deadline)
        try DaemonControlProtocol.decodeStopResponse(
            DaemonControlProtocol.readFrame(from: descriptors[0], deadline: deadline),
            expectedProfileID: profileID
        )
    }

    private struct SyntheticRoutePlan: Encodable {
        struct Route: Encodable {
            let destination: String
            let owner: String
        }

        let localAddress: String
        let routes: [Route]

        enum CodingKeys: String, CodingKey {
            case localAddress = "local_address"
            case routes
        }
    }

    private static func requestObject(_ data: Data) throws -> [String: Any] {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw SelfTestError.invalidRequest
        }
        return object
    }

    private static func expect(_ condition: @autoclosure () -> Bool) throws {
        guard condition() else { throw SelfTestError.expectationFailed }
    }

    private static func expectFailure(_ body: () throws -> Void) throws {
        do {
            try body()
        } catch {
            return
        }
        throw SelfTestError.expectedFailure
    }

    private static func expectIncompatibleDaemon(_ body: () throws -> Void) throws {
        do {
            try body()
            throw SelfTestError.expectedFailure
        } catch let error as DaemonControlClientError {
            try expect(error == .incompatibleDaemon)
        }
    }

    private enum SelfTestError: Error {
        case expectationFailed
        case expectedFailure
        case socketPairFailed
        case invalidRequest
    }
}
