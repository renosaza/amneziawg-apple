# Local development

The project has a Swift Package test target named `WireGuardKitTests` and an Xcode target named `WireGuardmacOS`.

With a complete Xcode installation and Go available:

```sh
make -C Sources/WireGuardKitGo
swift test
xcodebuild -project WireGuard.xcodeproj -target WireGuardmacOS -configuration Debug -sdk macosx CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO build
```

The signing-disabled command is a compile check. A runnable app still needs local fork-owned signing configuration described in [BUILDING.md](../BUILDING.md).

For a multi-tunnel change, also verify live routes and traffic on macOS. At minimum inspect the selected interface for each split destination, each public VPN endpoint, the local LAN, and a public internet address; unit tests and a successful build do not establish route ownership.
