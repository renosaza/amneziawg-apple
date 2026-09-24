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

## GUI updates

The daemon GUI can use Sparkle 2 only when its release build supplies both a
HTTPS `SUFeedURL` and a base64 EdDSA public key through `SPARKLE_FEED_URL` and
`SPARKLE_PUBLIC_ED_KEY`. Development and CI builds leave both values empty, so
they do not contact an update feed or show an update menu item.

Even with those release values, the GUI starts Sparkle only after the installed
daemon's `hello` response confirms protocol version 1. If the daemon is absent
or incompatible, update checks stay disabled; install the matching reviewed
daemon package manually before using the GUI updater.

Release blocker: `hello` currently proves protocol compatibility only. It does
not identify a daemon build, so it cannot detect a future same-protocol feature
drift. Do not configure a public `SUFeedURL` until the release process ties a
GUI artifact to a reviewed daemon artifact and verifies their compatibility.

Generate the EdDSA key once on a trusted release Mac with Sparkle's
`generate_keys` tool. Put the public value in the release build and retain the
private value only in that Mac's Keychain or an encrypted GitHub Actions secret.
Never commit it or put it on a command line. Publish the appcast over HTTPS,
upload a `ditto -c -k --sequesterRsrc --keepParent` archive to GitHub Releases,
and sign its enclosure with Sparkle `generate_appcast`. This target requires
signed feeds and validates an archive before extraction.

Sparkle replaces only the GUI `.app`. The root installer intentionally does
not update `/usr/local/libexec` binaries or the LaunchDaemon. A GUI release
that needs a new daemon must ship separate reviewed manual-install instructions
and preserve the protocol compatibility check. Gatekeeper approval remains the
normal Control-click Open / Privacy & Security flow for ad-hoc builds.

The experimental GUI currently supports constrained IPv4 split profiles only;
full/default routes, IPv6, DNS, `ExcludeIPs`, hostname endpoints, and on-demand
remain unsupported.
