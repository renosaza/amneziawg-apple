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

This is not a public release or updater. The daemon installer intentionally
does not update an installed root binary. The experimental GUI currently
supports constrained IPv4 split profiles only; full/default routes, IPv6, DNS,
`ExcludeIPs`, hostname endpoints, on-demand, and automatic updates remain
unsupported.
