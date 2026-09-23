# Troubleshooting

## `xcodebuild` says Command Line Tools are active

The local `xcodebuild` invocation requires a full Xcode developer directory. Select an installed Xcode with `xcode-select` according to local administration policy, then rerun `xcodebuild -list -project WireGuard.xcodeproj`.

## `swift test` cannot resolve `XCTest`

This has been observed with Command Line Tools only. Install/select complete Xcode and rerun the test command; do not treat a package build without XCTest as test coverage.

## A corporate hostname endpoint changes address

`ExcludeIPs` only accepts CIDRs. A hostname endpoint that resolves to a new address is not covered automatically by another profile's literal endpoint exclusion. Refresh the routing policy and verify the system route before relying on concurrent full-tunnel operation.
