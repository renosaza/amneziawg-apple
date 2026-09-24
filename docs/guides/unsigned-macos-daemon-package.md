---
type: guide
status: draft
scope: experimental-macos-daemon
date: 2026-09-24
constraints:
  - C-DAEMON-INSTALL-EXPERIMENT
  - C-DAEMON-SINGLE-SPLIT-ACTIVATION-EXPERIMENT
  - C-NO-SECRETS
---

# Unsigned macOS daemon package

The CI workflow builds a private CI artifact named
`AmneziaWG-unsigned-macos-<version>-<commit>.zip`. It contains the separate
`WireGuardmacOSDaemon` GUI, the compatible daemon-control binary,
`amneziawg-go`, the reviewed installer, `MANIFEST.json`, and the same concise
installation instructions.

The GUI bundle identifier is `com.renosaza.amneziawg.daemon-gui`. It is ad-hoc
signed only, so it has no Apple developer identity. On another Mac, obtain it
from the trusted CI run, inspect its manifest, then use macOS's normal
Control-click Open / Privacy & Security approval flow. Do not disable Gatekeeper
globally and do not treat ad-hoc signing as identity verification.

The package does not include a VPN profile, key, configuration, certificate,
or provisioning material. The root installer requires an explicit command and
the current console UID. It installs the two included binaries under
`/usr/local/libexec`, uses a root-owned LaunchDaemon, and exposes its socket
only to that UID. It refuses removal while a daemon session is active.

Install only after reviewing the package and stopping old sessions:

```sh
sudo ./Daemon/install-daemon-control-poc.sh install \
  --daemon "$PWD/Daemon/amneziawg-daemon-control-poc" \
  --amneziawg-go "$PWD/Daemon/amneziawg-go-daemon-control-poc" \
  --uid "$(id -u)" \
  --allow-route-plan-runtime
```

To roll back, stop every profile in the GUI, then run:

```sh
sudo ./Daemon/install-daemon-control-poc.sh uninstall --uid "$(id -u)"
```

## GUI update candidate

The daemon GUI can use Sparkle 2 only when its release build supplies both a
HTTPS `SUFeedURL` and a base64 EdDSA public key through `SPARKLE_FEED_URL` and
`SPARKLE_PUBLIC_ED_KEY`. Development and CI builds leave both values empty, so
they do not contact an update feed or show an update menu item.

Even with those release values, the GUI starts Sparkle only after the installed
daemon's `hello` response confirms protocol version 1. If the daemon is absent
or incompatible, update checks stay disabled; install the matching reviewed
daemon package manually before using the GUI updater.

The dispatch-only
[`Build unsigned macOS daemon release candidate`](../../.github/workflows/unsigned-macos-release.yml)
workflow builds a signed appcast and both archives for review. It never creates
a GitHub Release, deploys GitHub Pages content, or updates a root daemon. It
requires an existing tag whose exact form is
`v<MARKETING_VERSION>-<CFBundleVersion>`. `CFBundleVersion` is the macOS
`VERSION_ID_MACOS` from `Sources/WireGuardApp/Config/Version.xcconfig`; increase
it for every published GUI update.

One trusted release Mac must perform this bootstrap once with the Sparkle 2.10.0
publishing tools:

```sh
./bin/generate_keys --account com.renosaza.amneziawg.daemon-gui
./bin/generate_keys --account com.renosaza.amneziawg.daemon-gui -p
./bin/generate_keys --account com.renosaza.amneziawg.daemon-gui -x /secure/offline/sparkle-ed25519-private-key
```

Store the printed base64 public key as the repository Actions variable
`SPARKLE_PUBLIC_ED_KEY`. Put the contents of the exported private-key file in
the repository Actions secret `SPARKLE_ED25519_PRIVATE_KEY`, then remove the
temporary exported file from the release Mac using its secure local procedure.
Keep an offline recovery copy. Neither value belongs in the repository, an
issue, CI log, profile, or command-line argument.

Create and push the reviewed tag, then dispatch the workflow from that tag and
inspect its private candidate artifact. The candidate's appcast enclosure uses
the future GitHub Release URL and is signed by `generate_appcast` through
standard input only. The workflow never re-signs a downloaded appcast, so an
untrusted network response cannot enter a signed feed.

Public GitHub Release and GitHub Pages publication remain blocked by
`C-DAEMON-INSTALL-EXPERIMENT` and
`C-DAEMON-SINGLE-SPLIT-ACTIVATION-EXPERIMENT`. They need a separately reviewed
runtime and compatibility gate. Do not edit a signed appcast by hand. Lost or
rotated Ed25519 keys also require a separately reviewed migration: existing
ad-hoc clients cannot safely trust a new key through an unsigned feed.

Sparkle replaces only the GUI `.app`. The root installer intentionally does
not update `/usr/local/libexec` binaries or the LaunchDaemon. A GUI release
that needs a new daemon must ship separate reviewed manual-install instructions
and preserve the protocol compatibility check. Gatekeeper approval remains the
normal Control-click Open / Privacy & Security flow for ad-hoc builds.

The experimental GUI currently supports constrained IPv4 split profiles only;
full/default routes, IPv6, DNS, `ExcludeIPs`, hostname endpoints, and on-demand
remain unsupported.
