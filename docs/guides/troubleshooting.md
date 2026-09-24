# Troubleshooting

## `xcodebuild` says Command Line Tools are active

The local `xcodebuild` invocation requires a full Xcode developer directory. Select an installed Xcode with `xcode-select` according to local administration policy, then rerun `xcodebuild -list -project WireGuard.xcodeproj`.

## `swift test` cannot resolve `XCTest`

This has been observed with Command Line Tools only. Install/select complete Xcode and rerun the test command; do not treat a package build without XCTest as test coverage.

## A corporate hostname endpoint changes address

`ExcludeIPs` only accepts CIDRs. A hostname endpoint that resolves to a new address is not covered automatically by another profile's literal endpoint exclusion. Refresh the routing policy and verify the system route before relying on concurrent full-tunnel operation.

## The network changes while an experimental daemon session is active

The opt-in daemon records the physical gateway and interface at start; it does
not rebind an active endpoint host route by default after Wi-Fi, Ethernet,
sleep, or wake changes. Stop the session and start it again after the new
network is stable.

`--allow-route-plan-network-rebind` is a separate root-controlled experiment
for constrained route-plan sessions. It may change only the daemon-owned
endpoint `/32` in place after a physical-RIB check; failed or unclear changes
mark that session degraded. It has not passed the Wi-Fi, Ethernet, and
sleep/wake runtime matrix. Do not rely on it for a corporate endpoint until
that matrix is recorded as passed.
