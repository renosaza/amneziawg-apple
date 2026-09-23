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

    private enum SelfTestError: Error {
        case expectationFailed
        case expectedFailure
        case socketPairFailed
    }
}
