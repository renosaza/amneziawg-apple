// SPDX-License-Identifier: MIT
// Copyright © 2018-2023 WireGuard LLC. All Rights Reserved.

import Foundation
import NetworkExtension

#if SWIFT_PACKAGE
import WireGuardKitGo
import WireGuardKitC
#endif

public enum WireGuardAdapterError: Error {
    /// Failure to locate tunnel file descriptor.
    case cannotLocateTunnelFileDescriptor

    /// Failure to perform an operation in such state.
    case invalidState

    /// Failure to resolve endpoints.
    case dnsResolution([DNSResolutionError])

    /// Failure to set network settings.
    case setNetworkSettings(Error)

    /// Failure to start WireGuard backend.
    case startWireGuardBackend(Int32)
}

/// Enum representing internal state of the `WireGuardAdapter`
private enum State {
    /// The tunnel is stopped
    case stopped

    /// The tunnel is up and running
    case started(_ handle: Int32, _ settingsGenerator: PacketTunnelSettingsGenerator)

    /// The tunnel is temporarily shutdown due to device going offline
    case temporaryShutdown(_ settingsGenerator: PacketTunnelSettingsGenerator)
}

public class WireGuardAdapter {
    public typealias LogHandler = (WireGuardLogLevel, String) -> Void

    /// Network routes monitor.
    private var networkMonitor: NWPathMonitor?

    /// Timestamp of last applied `NEPacketTunnelNetworkSettings` update.
    /// Used to suppress transient `.unsatisfied` events immediately after installing routes (common on Wi‑Fi with kill-switch routes).
    private var lastNetworkSettingsUpdateAt: Date?

    /// Tracks whether the tunnel has ever had a successful handshake during the lifetime of this adapter instance.
    private var everHadHandshake = false

    /// Packet tunnel provider.
    private weak var packetTunnelProvider: NEPacketTunnelProvider?

    /// Log handler closure.
    private let logHandler: LogHandler

    /// Private queue used to synchronize access to `WireGuardAdapter` members.
    private let workQueue = DispatchQueue(label: "WireGuardAdapterWorkQueue")

    /// Adapter state.
    private var state: State = .stopped

    /// Tunnel device file descriptors claimed by adapters in this process.
    /// A macOS extension process can host multiple tunnel sessions, each of
    /// which must attach its backend to a distinct utun device.
    private static var claimedTunnelFileDescriptors = Set<Int32>()
    private static let claimedTunnelFileDescriptorsLock = NSLock()

    /// The utun file descriptor claimed by this adapter, if any.
    private var claimedTunnelFileDescriptor: Int32?

    /// Tunnel device file descriptor.
    private var tunnelFileDescriptor: Int32? {
        if let claimedTunnelFileDescriptor {
            return claimedTunnelFileDescriptor
        }
        return utunFileDescriptors.first
    }

    /// File descriptors of all utun devices opened by this process.
    private var utunFileDescriptors: [Int32] {
        var fileDescriptors = [Int32]()
        var ctlInfo = ctl_info()
        withUnsafeMutablePointer(to: &ctlInfo.ctl_name) {
            $0.withMemoryRebound(to: CChar.self, capacity: MemoryLayout.size(ofValue: $0.pointee)) {
                _ = strcpy($0, "com.apple.net.utun_control")
            }
        }
        for fd: Int32 in 0...1024 {
            var addr = sockaddr_ctl()
            var ret: Int32 = -1
            var len = socklen_t(MemoryLayout.size(ofValue: addr))
            withUnsafeMutablePointer(to: &addr) {
                $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                    ret = getpeername(fd, $0, &len)
                }
            }
            if ret != 0 || addr.sc_family != AF_SYSTEM {
                continue
            }
            if ctlInfo.ctl_id == 0 {
                ret = ioctl(fd, CTLIOCGINFO, &ctlInfo)
                if ret != 0 {
                    continue
                }
            }
            if addr.sc_id == ctlInfo.ctl_id {
                fileDescriptors.append(fd)
            }
        }
        return fileDescriptors
    }

    /// Atomically claims the utun device belonging to this adapter's tunnel.
    /// Network settings are installed before backend startup, so the matching
    /// device already carries one of the configured interface addresses. Do
    /// not claim an arbitrary available utun when that identity is absent.
    private func claimTunnelFileDescriptor(for tunnelConfiguration: TunnelConfiguration) -> Int32? {
        if let claimedTunnelFileDescriptor {
            return claimedTunnelFileDescriptor
        }

        Self.claimedTunnelFileDescriptorsLock.lock()
        defer { Self.claimedTunnelFileDescriptorsLock.unlock() }

        let candidates = utunFileDescriptors.filter { !Self.claimedTunnelFileDescriptors.contains($0) }
        let matching = candidates.first { fileDescriptor in
            guard let interfaceName = Self.interfaceName(forTunnelFileDescriptor: fileDescriptor) else { return false }
            return Self.interface(interfaceName, hasAnyAddressOf: tunnelConfiguration.interface.addresses)
        }
        guard let fileDescriptor = matching else { return nil }

        Self.claimedTunnelFileDescriptors.insert(fileDescriptor)
        claimedTunnelFileDescriptor = fileDescriptor
        return fileDescriptor
    }

    /// Releases the utun device claimed by this adapter.
    private func releaseTunnelFileDescriptor() {
        Self.claimedTunnelFileDescriptorsLock.lock()
        defer { Self.claimedTunnelFileDescriptorsLock.unlock() }

        if let claimedTunnelFileDescriptor {
            Self.claimedTunnelFileDescriptors.remove(claimedTunnelFileDescriptor)
            self.claimedTunnelFileDescriptor = nil
        }
    }

    /// Returns whether an interface carries an address from the tunnel configuration.
    private class func interface(_ interfaceName: String, hasAnyAddressOf addresses: [IPAddressRange]) -> Bool {
        var interfaceAddresses: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&interfaceAddresses) == 0 else { return false }
        defer { freeifaddrs(interfaceAddresses) }

        let addressStrings = Set(addresses.map { "\($0.address)" })
        var cursor = interfaceAddresses
        while let interfaceAddress = cursor?.pointee {
            cursor = interfaceAddress.ifa_next
            guard let address = interfaceAddress.ifa_addr,
                  String(cString: interfaceAddress.ifa_name) == interfaceName else {
                continue
            }

            var addressString: String?
            switch Int32(address.pointee.sa_family) {
            case AF_INET:
                var address4 = UnsafeRawPointer(address).assumingMemoryBound(to: sockaddr_in.self).pointee
                var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
                if inet_ntop(AF_INET, &address4.sin_addr, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil {
                    addressString = String(cString: buffer)
                }
            case AF_INET6:
                var address6 = UnsafeRawPointer(address).assumingMemoryBound(to: sockaddr_in6.self).pointee
                var buffer = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
                if inet_ntop(AF_INET6, &address6.sin6_addr, &buffer, socklen_t(INET6_ADDRSTRLEN)) != nil {
                    addressString = String(cString: buffer)
                }
            default:
                continue
            }

            if let addressString, addressStrings.contains(addressString) {
                return true
            }
        }
        return false
    }

    /// Returns a WireGuard version.
    class var backendVersion: String {
        guard let ver = wgVersion() else { return "unknown" }
        let str = String(cString: ver)
        free(UnsafeMutableRawPointer(mutating: ver))
        return str
    }

    /// Returns the tunnel device interface name, or nil on error.
    /// - Returns: String.
    public var interfaceName: String? {
        guard let tunnelFileDescriptor = self.tunnelFileDescriptor else { return nil }
        return Self.interfaceName(forTunnelFileDescriptor: tunnelFileDescriptor)
    }

    /// Returns the interface name of a utun device file descriptor, or nil on error.
    private class func interfaceName(forTunnelFileDescriptor tunnelFileDescriptor: Int32) -> String? {
        var buffer = [UInt8](repeating: 0, count: Int(IFNAMSIZ))

        return buffer.withUnsafeMutableBufferPointer { mutableBufferPointer in
            guard let baseAddress = mutableBufferPointer.baseAddress else { return nil }

            var ifnameSize = socklen_t(IFNAMSIZ)
            let result = getsockopt(
                tunnelFileDescriptor,
                2 /* SYSPROTO_CONTROL */,
                2 /* UTUN_OPT_IFNAME */,
                baseAddress,
                &ifnameSize)

            if result == 0 {
                return String(cString: baseAddress)
            } else {
                return nil
            }
        }
    }

    // MARK: - Initialization

    /// Designated initializer.
    /// - Parameter packetTunnelProvider: an instance of `NEPacketTunnelProvider`. Internally stored
    ///   as a weak reference.
    /// - Parameter logHandler: a log handler closure.
    public init(with packetTunnelProvider: NEPacketTunnelProvider, logHandler: @escaping LogHandler) {
        self.packetTunnelProvider = packetTunnelProvider
        self.logHandler = logHandler

        setupLogHandler()
    }

    deinit {
        // Force remove logger to make sure that no further calls to the instance of this class
        // can happen after deallocation.
        wgSetLogger(nil, nil)

        // Cancel network monitor
        networkMonitor?.cancel()

        // Shutdown the tunnel
        if case .started(let handle, _) = self.state {
            wgTurnOff(handle)
        }

        releaseTunnelFileDescriptor()
    }

    // MARK: - Public methods

    /// Returns a runtime configuration from WireGuard.
    /// - Parameter completionHandler: completion handler.
    public func getRuntimeConfiguration(completionHandler: @escaping (String?) -> Void) {
        workQueue.async {
            guard case .started(let handle, _) = self.state else {
                self.logHandler(.verbose, "getRuntimeConfiguration: adapter not started (state=\(self.state))")
                completionHandler(nil)
                return
            }

            if let settings = wgGetConfig(handle) {
                completionHandler(String(cString: settings))
                free(settings)
            } else {
                completionHandler(nil)
            }
        }
    }

    /// Start the tunnel tunnel.
    /// - Parameters:
    ///   - tunnelConfiguration: tunnel configuration.
    ///   - completionHandler: completion handler.
    public func start(tunnelConfiguration: TunnelConfiguration, completionHandler: @escaping (WireGuardAdapterError?) -> Void) {
        workQueue.async {
            guard case .stopped = self.state else {
                completionHandler(.invalidState)
                return
            }

            let networkMonitor = NWPathMonitor()
            networkMonitor.pathUpdateHandler = { [weak self] path in
                self?.didReceivePathUpdate(path: path)
            }
            networkMonitor.start(queue: self.workQueue)

            do {
                self.logHandler(.verbose, "Adapter start requested")
                let settingsGenerator = try self.makeSettingsGenerator(with: tunnelConfiguration)
                let networkSettings = settingsGenerator.generateNetworkSettings()
                self.logResolvedEndpoints(settingsGenerator.resolvedEndpoints, context: "start")
                self.logNetworkSettingsSummary(networkSettings, context: "start")
                try self.setNetworkSettings(networkSettings)

                let (wgConfig, resolutionResults) = settingsGenerator.uapiConfiguration()
                self.logEndpointResolutionResults(resolutionResults)

                self.state = .started(
                    try self.startWireGuardBackend(wgConfig: wgConfig, tunnelConfiguration: tunnelConfiguration),
                    settingsGenerator
                )
                self.networkMonitor = networkMonitor
                completionHandler(nil)
            } catch let error as WireGuardAdapterError {
                networkMonitor.cancel()
                completionHandler(error)
            } catch {
                fatalError()
            }
        }
    }

    /// Stop the tunnel.
    /// - Parameter completionHandler: completion handler.
    public func stop(completionHandler: @escaping (WireGuardAdapterError?) -> Void) {
        workQueue.async {
            self.logHandler(.verbose, "Adapter stop requested (state=\(self.state))")
            switch self.state {
            case .started(let handle, _):
                wgTurnOff(handle)

            case .temporaryShutdown:
                break

            case .stopped:
                completionHandler(.invalidState)
                return
            }

            self.networkMonitor?.cancel()
            self.networkMonitor = nil

            self.releaseTunnelFileDescriptor()
            self.state = .stopped

            completionHandler(nil)
        }
    }

    /// Update runtime configuration.
    /// - Parameters:
    ///   - tunnelConfiguration: tunnel configuration.
    ///   - completionHandler: completion handler.
    public func update(tunnelConfiguration: TunnelConfiguration, completionHandler: @escaping (WireGuardAdapterError?) -> Void) {
        workQueue.async {
            if case .stopped = self.state {
                completionHandler(.invalidState)
                return
            }

            // Tell the system that the tunnel is going to reconnect using new WireGuard
            // configuration.
            // This will broadcast the `NEVPNStatusDidChange` notification to the GUI process.
            self.packetTunnelProvider?.reasserting = true
            defer {
                self.packetTunnelProvider?.reasserting = false
            }

            do {
                let settingsGenerator = try self.makeSettingsGenerator(with: tunnelConfiguration)
                let networkSettings = settingsGenerator.generateNetworkSettings()
                self.logResolvedEndpoints(settingsGenerator.resolvedEndpoints, context: "update")
                self.logNetworkSettingsSummary(networkSettings, context: "update")
                try self.setNetworkSettings(networkSettings)

                switch self.state {
                case .started(let handle, _):
                    let (wgConfig, resolutionResults) = settingsGenerator.uapiConfiguration()
                    self.logEndpointResolutionResults(resolutionResults)

                    wgSetConfig(handle, wgConfig)
                    #if os(iOS)
                    wgDisableSomeRoamingForBrokenMobileSemantics(handle)
                    #endif

                    self.state = .started(handle, settingsGenerator)

                case .temporaryShutdown:
                    self.state = .temporaryShutdown(settingsGenerator)

                case .stopped:
                    fatalError()
                }

                completionHandler(nil)
            } catch let error as WireGuardAdapterError {
                completionHandler(error)
            } catch {
                fatalError()
            }
        }
    }

    // MARK: - Private methods

    /// Setup WireGuard log handler.
    private func setupLogHandler() {
        let context = Unmanaged.passUnretained(self).toOpaque()
        wgSetLogger(context) { context, logLevel, message in
            guard let context = context, let message = message else { return }

            let unretainedSelf = Unmanaged<WireGuardAdapter>.fromOpaque(context)
                .takeUnretainedValue()

            let swiftString = String(cString: message).trimmingCharacters(in: .newlines)
            let tunnelLogLevel = WireGuardLogLevel(rawValue: logLevel) ?? .verbose

            unretainedSelf.logHandler(tunnelLogLevel, swiftString)
        }
    }

    /// Set network tunnel configuration.
    /// This method ensures that the call to `setTunnelNetworkSettings` does not time out, as in
    /// certain scenarios the completion handler given to it may not be invoked by the system.
    ///
    /// - Parameters:
    ///   - networkSettings: an instance of type `NEPacketTunnelNetworkSettings`.
    /// - Throws: an error of type `WireGuardAdapterError`.
    /// - Returns: `PacketTunnelSettingsGenerator`.
    private func setNetworkSettings(_ networkSettings: NEPacketTunnelNetworkSettings) throws {
        self.logHandler(.verbose, "setNetworkSettings: applying")
        var systemError: Error?
        let condition = NSCondition()

        // Activate the condition
        condition.lock()
        defer { condition.unlock() }

        self.packetTunnelProvider?.setTunnelNetworkSettings(networkSettings) { error in
            systemError = error
            condition.signal()
        }

        // Packet tunnel's `setTunnelNetworkSettings` times out in certain
        // scenarios & never calls the given callback.
        let setTunnelNetworkSettingsTimeout: TimeInterval = 5 // seconds

        if condition.wait(until: Date().addingTimeInterval(setTunnelNetworkSettingsTimeout)) {
            if let systemError = systemError {
                throw WireGuardAdapterError.setNetworkSettings(systemError)
            }
        } else {
            self.logHandler(.error, "setTunnelNetworkSettings timed out after 5 seconds; proceeding anyway")
        }

        self.lastNetworkSettingsUpdateAt = Date()
    }

    /// Resolve peers of the given tunnel configuration.
    /// - Parameter tunnelConfiguration: tunnel configuration.
    /// - Throws: an error of type `WireGuardAdapterError`.
    /// - Returns: The list of resolved endpoints.
    private func resolvePeers(for tunnelConfiguration: TunnelConfiguration) throws -> [Endpoint?] {
        let endpoints = tunnelConfiguration.peers.map { $0.endpoint }
        let resolutionResults = DNSResolver.resolveSync(endpoints: endpoints)
        let resolutionErrors = resolutionResults.compactMap { result -> DNSResolutionError? in
            if case .failure(let error) = result {
                return error
            } else {
                return nil
            }
        }
        assert(endpoints.count == resolutionResults.count)
        guard resolutionErrors.isEmpty else {
            throw WireGuardAdapterError.dnsResolution(resolutionErrors)
        }

        let resolvedEndpoints = resolutionResults.map { result -> Endpoint? in
            // swiftlint:disable:next force_try
            return try! result?.get()
        }

        return resolvedEndpoints
    }

    /// Start WireGuard backend.
    /// - Parameters:
    ///   - wgConfig: WireGuard configuration
    ///   - tunnelConfiguration: tunnel configuration
    /// - Throws: an error of type `WireGuardAdapterError`
    /// - Returns: tunnel handle
    private func startWireGuardBackend(wgConfig: String, tunnelConfiguration: TunnelConfiguration) throws -> Int32 {
        guard let tunnelFileDescriptor = self.claimTunnelFileDescriptor(for: tunnelConfiguration) else {
            throw WireGuardAdapterError.cannotLocateTunnelFileDescriptor
        }

        self.logHandler(.verbose, "Starting WireGuard backend (config bytes: \(wgConfig.utf8.count), fd: \(tunnelFileDescriptor))")
        let handle = wgTurnOn(wgConfig, tunnelFileDescriptor)
        if handle < 0 {
            releaseTunnelFileDescriptor()
            throw WireGuardAdapterError.startWireGuardBackend(handle)
        }
        self.logHandler(.verbose, "WireGuard backend started with handle \(handle)")
        #if os(iOS)
        wgDisableSomeRoamingForBrokenMobileSemantics(handle)
        #endif
        return handle
    }

    /// Resolves the hostnames in the given tunnel configuration and return settings generator.
    /// - Parameter tunnelConfiguration: an instance of type `TunnelConfiguration`.
    /// - Throws: an error of type `WireGuardAdapterError`.
    /// - Returns: an instance of type `PacketTunnelSettingsGenerator`.
    private func makeSettingsGenerator(with tunnelConfiguration: TunnelConfiguration) throws -> PacketTunnelSettingsGenerator {
        return PacketTunnelSettingsGenerator(
            tunnelConfiguration: tunnelConfiguration,
            resolvedEndpoints: try self.resolvePeers(for: tunnelConfiguration)
        )
    }

    /// Log DNS resolution results.
    /// - Parameter resolutionErrors: an array of type `[DNSResolutionError]`.
    private func logEndpointResolutionResults(_ resolutionResults: [EndpointResolutionResult?]) {
        for case .some(let result) in resolutionResults {
            switch result {
            case .success((let sourceEndpoint, let resolvedEndpoint)):
                if sourceEndpoint.host == resolvedEndpoint.host {
                    self.logHandler(.verbose, "DNS64: mapped \(sourceEndpoint.host) to itself.")
                } else {
                    self.logHandler(.verbose, "DNS64: mapped \(sourceEndpoint.host) to \(resolvedEndpoint.host)")
                }
            case .failure(let resolutionError):
                self.logHandler(.error, "Failed to resolve endpoint \(resolutionError.address): \(resolutionError.errorDescription ?? "(nil)")")
            }
        }
    }

    private func logNetworkSettingsSummary(_ networkSettings: NEPacketTunnelNetworkSettings, context: String) {
        let ipv4Included = networkSettings.ipv4Settings?.includedRoutes?.count ?? 0
        let ipv4Excluded = networkSettings.ipv4Settings?.excludedRoutes?.count ?? 0
        let ipv6Included = networkSettings.ipv6Settings?.includedRoutes?.count ?? 0
        let ipv6Excluded = networkSettings.ipv6Settings?.excludedRoutes?.count ?? 0
        let mtu = networkSettings.mtu?.stringValue ?? "nil"
        let dnsServers = networkSettings.dnsSettings?.servers.joined(separator: ", ") ?? "nil"
        self.logHandler(.verbose, "Network settings (\(context)): remote=\(networkSettings.tunnelRemoteAddress) mtu=\(mtu) dns=\(dnsServers) ipv4 inc/exc=\(ipv4Included)/\(ipv4Excluded) ipv6 inc/exc=\(ipv6Included)/\(ipv6Excluded)")
    }

    private func logResolvedEndpoints(_ endpoints: [Endpoint?], context: String) {
        let endpointStrings = endpoints.map { $0?.stringRepresentation ?? "nil" }
        self.logHandler(.verbose, "Resolved endpoints (\(context)): \(endpointStrings)")
    }

    /// Helper method used by network path monitor.
    /// - Parameter path: new network path
    private func didReceivePathUpdate(path: Network.NWPath) {
        self.logHandler(.verbose, "Network change detected with \(path.status) route and interface order \(path.availableInterfaces)")
        let lastUpdate = self.lastNetworkSettingsUpdateAt.map { String(describing: $0) } ?? "nil"
        self.logHandler(.verbose, "Path update state: state=\(self.state) everHadHandshake=\(self.everHadHandshake) lastNetworkSettingsUpdateAt=\(lastUpdate)")

        #if os(macOS)
        if case .started(let handle, _) = self.state {
            wgBumpSockets(handle)
        }
        #elseif os(iOS)
        switch self.state {
        case .started(let handle, let settingsGenerator):
            self.updateEverHadHandshake(handle: handle)
            if path.status.isSatisfiable {
                let (wgConfig, resolutionResults) = settingsGenerator.endpointUapiConfiguration()
                self.logEndpointResolutionResults(resolutionResults)

                wgSetConfig(handle, wgConfig)
                wgDisableSomeRoamingForBrokenMobileSemantics(handle)
                wgBumpSockets(handle)
            } else {
                if self.shouldPauseBackendOnUnsatisfiedPath() {
                    self.logHandler(.verbose, "Connectivity offline, pausing backend.")

                    self.state = .temporaryShutdown(settingsGenerator)
                    wgTurnOff(handle)
                } else {
                    let remaining = self.remainingUnsatisfiedGraceSeconds()
                    if remaining > 0 {
                        self.logHandler(.verbose, "Connectivity unsatisfied right after applying routes, not pausing backend for ~\(Int(ceil(remaining)))s.")
                    } else {
                        self.logHandler(.verbose, "Connectivity unsatisfied right after applying routes, not pausing backend.")
                    }
                }
            }

        case .temporaryShutdown(let settingsGenerator):
            guard path.status.isSatisfiable else { return }

            self.logHandler(.verbose, "Connectivity online, resuming backend.")

            do {
                let networkSettings = settingsGenerator.generateNetworkSettings()
                self.logNetworkSettingsSummary(networkSettings, context: "resume")
                try self.setNetworkSettings(networkSettings)

                let (wgConfig, resolutionResults) = settingsGenerator.uapiConfiguration()
                self.logEndpointResolutionResults(resolutionResults)

                self.state = .started(
                    try self.startWireGuardBackend(wgConfig: wgConfig, tunnelConfiguration: settingsGenerator.tunnelConfiguration),
                    settingsGenerator
                )
            } catch {
                self.logHandler(.error, "Failed to restart backend: \(error.localizedDescription)")
            }

        case .stopped:
            // no-op
            break
        }
        #else
        #error("Unsupported")
        #endif
    }

    // MARK: - iOS offline detection helpers

    private var unsatisfiedGracePeriodAfterNetworkSettings: TimeInterval {
        // Long enough to cover the route-flip window on Wi‑Fi (en0 → utun*) while the first handshake completes,
        // short enough to still pause reasonably quickly on genuine offline transitions.
        12
    }

    private func shouldPauseBackendOnUnsatisfiedPath() -> Bool {
        // `.unsatisfied` is commonly reported right after installing kill-switch routes (Wi‑Fi is especially prone).
        // Suppress pausing for a short grace period after the last network settings update.
        if let lastUpdateAt = self.lastNetworkSettingsUpdateAt,
           Date().timeIntervalSince(lastUpdateAt) < self.unsatisfiedGracePeriodAfterNetworkSettings {
            return false
        }

        // If we have never completed a handshake, treat `.unsatisfied` as transient during bootstrap.
        if !self.everHadHandshake {
            return false
        }

        // Outside the grace period, treat `.unsatisfied` as a real offline transition.
        // (We still keep `everHadHandshake` for future heuristics and for logging/debugging.)
        return true
    }

    private func remainingUnsatisfiedGraceSeconds() -> TimeInterval {
        guard let lastUpdateAt = self.lastNetworkSettingsUpdateAt else { return 0 }
        let elapsed = Date().timeIntervalSince(lastUpdateAt)
        return max(0, self.unsatisfiedGracePeriodAfterNetworkSettings - elapsed)
    }

    private func updateEverHadHandshake(handle: Int32) {
        guard !self.everHadHandshake else { return }
        guard let settings = wgGetConfig(handle) else { return }
        let config = String(cString: settings)
        free(settings)

        for line in config.split(separator: "\n", omittingEmptySubsequences: true) {
            guard line.hasPrefix("last_handshake_time_sec=") else { continue }
            let valueString = String(line.dropFirst("last_handshake_time_sec=".count))
            if let value = Int64(valueString), value > 0 {
                self.everHadHandshake = true
                self.logHandler(.verbose, "Observed first handshake at last_handshake_time_sec=\(value)")
                break
            }
        }
    }
}

/// A enum describing WireGuard log levels defined in `api-apple.go`.
public enum WireGuardLogLevel: Int32 {
    case verbose = 0
    case error = 1
}

private extension Network.NWPath.Status {
    /// Returns `true` if the path is potentially satisfiable.
    var isSatisfiable: Bool {
        switch self {
        case .requiresConnection, .satisfied:
            return true
        case .unsatisfied:
            return false
        @unknown default:
            return true
        }
    }
}
