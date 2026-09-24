// SPDX-License-Identifier: MIT

import Darwin
import Dispatch
import Foundation

enum DaemonControlProfileState: String, Equatable {
    case running
    case degraded
}

struct DaemonControlCapabilities: Equatable {
    static let ipv4FullRoute = "ipv4-full-route"

    let values: Set<String>

    var supportsIPv4FullRoute: Bool {
        values.contains(Self.ipv4FullRoute)
    }
}

struct DaemonControlProfileStatus: Equatable {
    let id: UUID
    let state: DaemonControlProfileState
}

enum DaemonControlClientError: Error, Equatable {
    case invalidSocketPath
    case invalidTimeout
    case invalidConfiguration
    case connectionFailed
    case timedOut
    case invalidResponse
    case incompatibleDaemon
    case daemonRejected

    var diagnosticCode: String {
        switch self {
        case .invalidSocketPath: return "invalid_socket_path"
        case .invalidTimeout: return "invalid_timeout"
        case .invalidConfiguration: return "invalid_configuration"
        case .connectionFailed: return "connection_failed"
        case .timedOut: return "timed_out"
        case .invalidResponse: return "invalid_response"
        case .incompatibleDaemon: return "incompatible_daemon"
        case .daemonRejected: return "daemon_rejected"
        }
    }
}

/// macOS client for the version 1 daemon-control POC protocol.
final class DaemonControlClient {
    /// Assigned by the daemon GUI only. The client never records request or response bytes.
    static var diagnosticHandler: ((String, [String: String]) -> Void)?
    private let socketPath: String
    private let timeout: TimeInterval

    init(socketPath: String, timeout: TimeInterval = 2) throws {
        guard DaemonControlProtocol.isValidSocketPath(socketPath) else {
            throw DaemonControlClientError.invalidSocketPath
        }
        guard timeout.isFinite, timeout > 0, timeout <= 30 else { throw DaemonControlClientError.invalidTimeout }
        self.socketPath = socketPath
        self.timeout = timeout
    }

    func list() throws -> [DaemonControlProfileStatus] {
        do {
            try verifyCompatibility()
            let statuses = try DaemonControlProtocol.decodeListResponse(request(operation: "list", profileID: nil))
            let states = Dictionary(grouping: statuses, by: \.state.rawValue).map {
                "\($0.key):\($0.value.count)"
            }.sorted().joined(separator: ",")
            record("list", fields: ["profiles": "\(statuses.count)", "states": states])
            return statuses
        } catch {
            recordError(operation: "list", error: error)
            throw error
        }
    }

    func status(profileID: UUID) throws -> DaemonControlProfileStatus {
        do {
            try verifyCompatibility()
            let status = try DaemonControlProtocol.decodeStatusResponse(
                request(operation: "status", profileID: profileID), expectedProfileID: profileID)
            record("status", fields: ["state": status.state.rawValue])
            return status
        } catch {
            recordError(operation: "status", error: error)
            throw error
        }
    }

    func capabilities() throws -> DaemonControlCapabilities {
        do {
            let capabilities = try DaemonControlProtocol.decodeHelloResponse(request(operation: "hello", profileID: nil))
            record("hello", fields: [
                "capabilities": capabilities.values.sorted().joined(separator: ",")
            ])
            return capabilities
        } catch {
            recordError(operation: "hello", error: error)
            throw error
        }
    }

    func start<RoutePlan: Encodable>(
        profileID: UUID,
        uapiConfiguration: String,
        routePlan: RoutePlan
    ) throws -> DaemonControlProfileStatus {
        do {
            try verifyCompatibility()
            let response = try request(
                operation: "start",
                profileID: profileID,
                uapiConfiguration: uapiConfiguration,
                routePlan: routePlan
            )
            let status = try DaemonControlProtocol.decodeStartResponse(response, expectedProfileID: profileID)
            record("start", fields: ["state": status.state.rawValue])
            return status
        } catch {
            recordError(operation: "start", error: error)
            throw error
        }
    }

    func stop(profileID: UUID) throws {
        do {
            try verifyCompatibility()
            let response = try request(operation: "stop", profileID: profileID)
            try DaemonControlProtocol.decodeStopResponse(response, expectedProfileID: profileID)
            record("stop", fields: ["state": "stopped"])
        } catch {
            recordError(operation: "stop", error: error)
            throw error
        }
    }

    private func verifyCompatibility() throws {
        _ = try capabilities()
    }

    private func record(_ event: String, fields: [String: String]) {
        Self.diagnosticHandler?(event, fields)
    }

    private func recordError(operation: String, error: Error) {
        let code = (error as? DaemonControlClientError)?.diagnosticCode ?? "unknown_error"
        record("protocol_error", fields: ["operation": operation, "class": code])
    }

    private func request(operation: String, profileID: UUID?, uapiConfiguration: String? = nil) throws -> Data {
        let request = try DaemonControlProtocol.makeRequest(
            operation: operation,
            profileID: profileID,
            uapiConfiguration: uapiConfiguration
        )
        return try exchange(request, containsConfiguration: uapiConfiguration != nil)
    }

    private func request<RoutePlan: Encodable>(
        operation: String,
        profileID: UUID?,
        uapiConfiguration: String,
        routePlan: RoutePlan
    ) throws -> Data {
        let request = try DaemonControlProtocol.makeRequest(
            operation: operation,
            profileID: profileID,
            uapiConfiguration: uapiConfiguration,
            routePlan: routePlan
        )
        return try exchange(request, containsConfiguration: true)
    }

    private func exchange(_ encodedRequest: Data, containsConfiguration: Bool) throws -> Data {
        let deadline = DaemonControlProtocol.deadline(after: timeout)
        let descriptor = try DaemonControlProtocol.connect(path: socketPath, deadline: deadline)
        defer { _ = Darwin.close(descriptor) }

        var request = encodedRequest
        defer {
            if containsConfiguration {
                DaemonControlProtocol.erase(&request)
            }
        }
        try DaemonControlProtocol.writeFrame(
            request,
            to: descriptor,
            deadline: deadline,
            sensitive: containsConfiguration
        )
        return try DaemonControlProtocol.readFrame(from: descriptor, deadline: deadline)
    }
}

enum DaemonControlProtocol {
    static let protocolVersion = 1
    static let maximumFrameBytes = 16 * 1024
    static let maximumConfigurationBytes = 2 * 1024
    private static let maximumSocketPathBytes = 103

    private struct Request<RoutePlan: Encodable>: Encodable {
        let version = DaemonControlProtocol.protocolVersion
        let operation: String
        let profileID: String?
        let config: String?
        let routePlan: RoutePlan?

        enum CodingKeys: String, CodingKey {
            case version
            case operation
            case profileID = "profile_id"
            case config
            case routePlan = "route_plan"
        }
    }

    private struct NoRoutePlan: Encodable {}

    private struct Response: Decodable {
        let ok: Bool
        let error: String?
        let protocolVersion: Int?
        let capabilities: [String]?
        let profile: Profile?
        let profiles: [Profile]?

        enum CodingKeys: String, CodingKey {
            case ok
            case error
            case protocolVersion = "protocol_version"
            case capabilities
            case profile
            case profiles
        }
    }

    private struct Profile: Decodable {
        let id: String
        let status: String
    }

    static func isValidSocketPath(_ path: String) -> Bool {
        guard path.utf8.count > 1,
              path.utf8.count <= maximumSocketPathBytes,
              path.hasPrefix("/"),
              !path.contains("\0"),
              !path.contains("\n"),
              !path.contains("\r")
        else {
            return false
        }
        return !path.dropFirst().split(separator: "/", omittingEmptySubsequences: false).contains { component in
            component.isEmpty || component == "." || component == ".."
        }
    }

    static func makeRequest(operation: String, profileID: UUID?, uapiConfiguration: String? = nil) throws -> Data {
        try makeRequest(
            operation: operation,
            profileID: profileID,
            uapiConfiguration: uapiConfiguration,
            routePlan: Optional<NoRoutePlan>.none
        )
    }

    static func makeRequest<RoutePlan: Encodable>(
        operation: String,
        profileID: UUID?,
        uapiConfiguration: String?,
        routePlan: RoutePlan?
    ) throws -> Data {
        if let uapiConfiguration = uapiConfiguration {
            guard !uapiConfiguration.isEmpty,
                  uapiConfiguration.utf8.count <= maximumConfigurationBytes,
                  !uapiConfiguration.contains("\0"),
                  !uapiConfiguration.contains("\r")
            else {
                throw DaemonControlClientError.invalidConfiguration
            }
        }
        if routePlan != nil && operation != "start" {
            throw DaemonControlClientError.invalidResponse
        }
        let data = try JSONEncoder().encode(Request(
            operation: operation,
            profileID: profileID?.uuidString.lowercased(),
            config: uapiConfiguration,
            routePlan: routePlan
        ))
        guard !data.isEmpty, data.count <= maximumFrameBytes else {
            throw DaemonControlClientError.invalidResponse
        }
        return data
    }

    static func decodeListResponse(_ data: Data) throws -> [DaemonControlProfileStatus] {
        let response = try decodeResponse(data)
        guard response.ok else {
            throw DaemonControlClientError.daemonRejected
        }
        guard response.error == nil, response.profile == nil else {
            throw DaemonControlClientError.invalidResponse
        }
        let profiles = response.profiles ?? []
        guard profiles.count <= 3 else { throw DaemonControlClientError.invalidResponse }
        let statuses = try profiles.map(decodeProfile)
        guard Set(statuses.map(\.id)).count == statuses.count else {
            throw DaemonControlClientError.invalidResponse
        }
        return statuses
    }

    static func decodeHelloResponse(_ data: Data) throws -> DaemonControlCapabilities {
        guard let response = try? decodeResponse(data),
              response.ok,
              response.error == nil,
              response.protocolVersion == protocolVersion,
              response.profile == nil,
              response.profiles == nil
        else {
            throw DaemonControlClientError.incompatibleDaemon
        }
        let capabilities = response.capabilities ?? []
        guard capabilities.count <= 8,
              Set(capabilities).count == capabilities.count,
              capabilities.allSatisfy({ capability in
                  !capability.isEmpty && capability.allSatisfy {
                      $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-")
                  }
              })
        else {
            throw DaemonControlClientError.incompatibleDaemon
        }
        return DaemonControlCapabilities(values: Set(capabilities))
    }

    static func decodeStatusResponse(_ data: Data, expectedProfileID: UUID) throws -> DaemonControlProfileStatus {
        let status = try decodeRunningResponse(data, expectedProfileID: expectedProfileID)
        return status
    }

    static func decodeStartResponse(_ data: Data, expectedProfileID: UUID) throws -> DaemonControlProfileStatus {
        let status = try decodeRunningResponse(data, expectedProfileID: expectedProfileID)
        return status
    }

    static func decodeStopResponse(_ data: Data, expectedProfileID: UUID) throws {
        let profile = try decodeProfileResponse(data, expectedProfileID: expectedProfileID)
        guard profile.status == "stopped" else {
            throw DaemonControlClientError.invalidResponse
        }
    }

    static func erase(_ data: inout Data) {
        // Best effort only: Swift and JSONEncoder may retain independent copies.
        guard !data.isEmpty else { return }
        data.resetBytes(in: 0 ..< data.count)
    }

    private static func decodeRunningResponse(_ data: Data, expectedProfileID: UUID) throws -> DaemonControlProfileStatus {
        let profile = try decodeProfileResponse(data, expectedProfileID: expectedProfileID)
        let status = try decodeProfile(profile)
        guard status.id == expectedProfileID else {
            throw DaemonControlClientError.invalidResponse
        }
        guard status.state == .running else {
            throw DaemonControlClientError.invalidResponse
        }
        return status
    }

    private static func decodeProfileResponse(_ data: Data, expectedProfileID: UUID) throws -> Profile {
        let response = try decodeResponse(data)
        guard response.ok else { throw DaemonControlClientError.daemonRejected }
        guard response.error == nil, response.profiles == nil, let profile = response.profile else {
            throw DaemonControlClientError.invalidResponse
        }
        guard UUID(uuidString: profile.id.lowercased()) == expectedProfileID else {
            throw DaemonControlClientError.invalidResponse
        }
        return profile
    }

    static func frame(_ body: Data) throws -> Data {
        guard !body.isEmpty, body.count <= maximumFrameBytes else {
            throw DaemonControlClientError.invalidResponse
        }
        var length = UInt32(body.count).bigEndian
        var framed = Data(bytes: &length, count: MemoryLayout<UInt32>.size)
        framed.append(body)
        return framed
    }

    static func unframe(_ framed: Data) throws -> Data {
        guard framed.count >= MemoryLayout<UInt32>.size else {
            throw DaemonControlClientError.invalidResponse
        }
        let length = framed.prefix(MemoryLayout<UInt32>.size).reduce(UInt32.zero) { ($0 << 8) | UInt32($1) }
        guard length > 0, length <= maximumFrameBytes,
              framed.count == MemoryLayout<UInt32>.size + Int(length)
        else {
            throw DaemonControlClientError.invalidResponse
        }
        return framed.dropFirst(MemoryLayout<UInt32>.size)
    }

    static func deadline(after timeout: TimeInterval) -> UInt64 {
        let nanoseconds = UInt64((timeout * 1_000_000_000).rounded(.up))
        return DispatchTime.now().uptimeNanoseconds &+ nanoseconds
    }

    static func connect(path: String, deadline: UInt64) throws -> Int32 {
        let descriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw DaemonControlClientError.connectionFailed }

        do {
            try setNonBlocking(descriptor)
            var address = sockaddr_un()
            address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
            address.sun_family = sa_family_t(AF_UNIX)
            var pathData = Data(path.utf8)
            pathData.append(0)
            withUnsafeMutableBytes(of: &address.sun_path) { destination in
                destination.copyBytes(from: pathData)
            }
            let result = withUnsafePointer(to: &address) { pointer in
                pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                    Darwin.connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
                }
            }
            if result != 0 {
                guard errno == EINPROGRESS else { throw DaemonControlClientError.connectionFailed }
                try wait(for: descriptor, events: Int16(POLLOUT), deadline: deadline)
                var socketError: Int32 = 0
                var socketErrorLength = socklen_t(MemoryLayout<Int32>.size)
                guard getsockopt(descriptor, SOL_SOCKET, SO_ERROR, &socketError, &socketErrorLength) == 0, socketError == 0 else {
                    throw DaemonControlClientError.connectionFailed
                }
            }
            return descriptor
        } catch {
            _ = Darwin.close(descriptor)
            throw error
        }
    }

    static func writeFrame(_ body: Data, to descriptor: Int32, deadline: UInt64, sensitive: Bool = false) throws {
        var framed = try frame(body)
        defer {
            if sensitive {
                erase(&framed)
            }
        }
        try writeAll(framed, to: descriptor, deadline: deadline)
    }

    static func readFrame(from descriptor: Int32, deadline: UInt64) throws -> Data {
        let header = try readExactly(MemoryLayout<UInt32>.size, from: descriptor, deadline: deadline)
        let length = header.reduce(UInt32.zero) { ($0 << 8) | UInt32($1) }
        guard length > 0, length <= maximumFrameBytes else {
            throw DaemonControlClientError.invalidResponse
        }
        return try readExactly(Int(length), from: descriptor, deadline: deadline)
    }

    private static func decodeResponse(_ data: Data) throws -> Response {
        do {
            return try JSONDecoder().decode(Response.self, from: data)
        } catch {
            throw DaemonControlClientError.invalidResponse
        }
    }

    private static func decodeProfile(_ profile: Profile) throws -> DaemonControlProfileStatus {
        guard let id = UUID(uuidString: profile.id.lowercased()),
              let state = DaemonControlProfileState(rawValue: profile.status)
        else {
            throw DaemonControlClientError.invalidResponse
        }
        return DaemonControlProfileStatus(id: id, state: state)
    }

    private static func setNonBlocking(_ descriptor: Int32) throws {
        let flags = fcntl(descriptor, F_GETFL)
        guard flags >= 0, fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0 else {
            throw DaemonControlClientError.connectionFailed
        }
    }

    private static func writeAll(_ data: Data, to descriptor: Int32, deadline: UInt64) throws {
        try data.withUnsafeBytes { bytes in
            guard let baseAddress = bytes.baseAddress else { throw DaemonControlClientError.invalidResponse }
            var offset = 0
            while offset < bytes.count {
                try wait(for: descriptor, events: Int16(POLLOUT), deadline: deadline)
                let written = Darwin.write(descriptor, baseAddress.advanced(by: offset), bytes.count - offset)
                if written > 0 {
                    offset += written
                } else if written == 0 {
                    throw DaemonControlClientError.connectionFailed
                } else if written < 0, errno != EAGAIN, errno != EWOULDBLOCK, errno != EINTR {
                    throw DaemonControlClientError.connectionFailed
                }
            }
        }
    }

    private static func readExactly(_ count: Int, from descriptor: Int32, deadline: UInt64) throws -> Data {
        var data = Data(repeating: 0, count: count)
        try data.withUnsafeMutableBytes { bytes in
            guard let baseAddress = bytes.baseAddress else { throw DaemonControlClientError.invalidResponse }
            var offset = 0
            while offset < count {
                try wait(for: descriptor, events: Int16(POLLIN), deadline: deadline)
                let readCount = Darwin.read(descriptor, baseAddress.advanced(by: offset), count - offset)
                if readCount > 0 {
                    offset += readCount
                } else if readCount == 0 {
                    throw DaemonControlClientError.invalidResponse
                } else if errno != EAGAIN, errno != EWOULDBLOCK, errno != EINTR {
                    throw DaemonControlClientError.connectionFailed
                }
            }
        }
        return data
    }

    private static func wait(for descriptor: Int32, events: Int16, deadline: UInt64) throws {
        while true {
            let now = DispatchTime.now().uptimeNanoseconds
            guard now < deadline else { throw DaemonControlClientError.timedOut }
            let remainingNanoseconds = deadline - now
            let timeoutMilliseconds = Int(min((remainingNanoseconds + 999_999) / 1_000_000, UInt64(Int32.max)))
            var pollDescriptor = pollfd(fd: descriptor, events: events, revents: 0)
            let result = Darwin.poll(&pollDescriptor, 1, Int32(timeoutMilliseconds))
            if result > 0 {
                if pollDescriptor.revents & events != 0 { return }
                throw DaemonControlClientError.connectionFailed
            }
            if result == 0 { throw DaemonControlClientError.timedOut }
            if errno != EINTR { throw DaemonControlClientError.connectionFailed }
        }
    }
}
