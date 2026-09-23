# Building for macOS

The project contains the `WireGuardmacOS` target in `WireGuard.xcodeproj`. A complete Xcode installation is required; Command Line Tools alone cannot run `xcodebuild` for this project.

Create a local signing configuration from the supplied template:

```sh
cp Sources/WireGuardApp/Config/Developer.xcconfig.template Sources/WireGuardApp/Config/Developer.xcconfig
```

For a macOS build, set only the Team ID in the local file. The tracked template fixes the macOS fork namespace:

```text
App:             com.renosaza.amneziawg
Network Extension: com.renosaza.amneziawg.network-extension
Login helper:     com.renosaza.amneziawg.login-item-helper
App Group:        <Team ID>.group.com.renosaza.amneziawg
```

Register the app ID and App Group in the fork's Apple Developer account with the Network Extensions capability. Do not commit the local signing file, certificates, provisioning profiles, or key material. Before committing, verify with `git ls-files` that the local signing file is not tracked.

Build the Go bridge and open the project:

```sh
make -C Sources/WireGuardKitGo
open WireGuard.xcodeproj
```

For a local compile check after signing is configured:

```sh
xcodebuild -project WireGuard.xcodeproj -target WireGuardmacOS -configuration Debug -sdk macosx build
```

Changing an installed app's bundle ID or App Group isolates its Keychain and shared-container data. Re-import tunnels before using the fork; the change also requires a new Network Extension approval. Keep these values unchanged for subsequent fork releases.

This workstation has only Command Line Tools selected, so `xcodebuild -list` and the macOS app build were not runnable here.
