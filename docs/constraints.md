---
type: constraints
active_ids:
  - C-DAEMON-EXPERIMENT
  - C-DAEMON-IPC-EXPERIMENT
  - C-DAEMON-THREE-SESSION-EXPERIMENT
  - C-DAEMON-IPV4-ROUTE-EXPERIMENT
  - C-DAEMON-ENDPOINT-ROUTE-EXPERIMENT
  - C-DAEMON-PRECEDENCE-EXPERIMENT
  - C-DAEMON-INSTALL-EXPERIMENT
  - C-DAEMON-PROFILE-STORE-EXPERIMENT
  - C-DAEMON-STOCK-GUI-READONLY-EXPERIMENT
---

# Constraints

- Keep the fork as an isolated patch series over `amnezia-vpn/amneziawg-apple`; replay it with rebase and inspect it with `git range-diff`.
- Use native NetworkExtension routing. Do not add a routing daemon or persistent shell `route` workaround in production.
- `C-DAEMON-EXPERIMENT`: Decision 0003 establishes the root-only synthetic-helper boundary. It is not a production routing daemon, installer, or GUI backend; configuration scope is controlled by the later specific daemon constraints.
- `C-DAEMON-IPC-EXPERIMENT`: Decision 0004 establishes the root-created, UID-authenticated Unix-socket control boundary and in-memory UUID state. It does not itself authorize a VPN backend, profile persistence, routes, DNS, or PF state.
- `C-DAEMON-THREE-SESSION-EXPERIMENT`: Decision 0006 permits up to three independent root-owned, in-memory UAPI-configured `utun` children only for manual disposable-runner tests with synthetic keys. The daemon-control POC itself may not persist configurations or alter routes, addresses, DNS, PF, GUI, installer, or automatic startup.
- `C-DAEMON-IPV4-ROUTE-EXPERIMENT`: Decision 0007 permits one TEST-NET IPv4 address pair and one exact TEST-NET `/32` route owned by one synthetic daemon `utun`, only in a manual disposable-runner test. It may not alter physical routes, IPv6, DNS, GUI, persistence, or automatic startup.
- `C-DAEMON-ENDPOINT-ROUTE-EXPERIMENT`: Decision 0008 permits one `203.0.113.10/32` route through the current effective non-`utun` IPv4 gateway, only in a manual disposable-runner test. It must reject an existing exact host route and remove only a route that still matches its recorded target, gateway, and interface. It may not send traffic or configure real endpoints, DNS, GUI, persistent state, default routes, or `/1` routes.
- `C-DAEMON-PRECEDENCE-EXPERIMENT`: Decision 0009 permits one synthetic `198.51.100.0/24` fallback route on an owned daemon `utun` and its more-specific `198.51.100.10/32` physical route, only in a manual disposable-runner test. It may not configure `0/0`, `/1`, DNS, GUI, persistence, traffic, real endpoints, or automatic startup. Each route must retain and verify its own owner before cleanup.
- `C-DAEMON-INSTALL-EXPERIMENT`: Decision 0010 permits a manually invoked, root-owned LaunchDaemon install/status/uninstall POC on a disposable macOS runner only. It installs fixed synthetic binaries and a UID-authenticated control socket; it must refuse uninstall unless the daemon proves it has no sessions; update is unsupported. It may not persist profiles, configure routes/DNS/PF, install automatically, update itself, publish releases, or receive real keys.
- `C-DAEMON-PROFILE-STORE-EXPERIMENT`: Decision 0011 permits an isolated macOS user profile store with non-secret UUID/name metadata in user Application Support and configuration text in the ordinary login Keychain. It has no NetworkExtension ACL and is not connected to lifecycle control, routes, DNS, PF, installer, automatic startup, or runtime.
- `C-DAEMON-STOCK-GUI-READONLY-EXPERIMENT`: Decision 0012 permits one separate unsigned macOS target to reuse the stock Manager UI for a launch-time, read-only view of `DaemonProfileStore` records and bounded daemon `list` status. It may not instantiate `NETunnelProviderManager`, send provider messages, forward configuration text to the daemon, modify the store, start or stop sessions, alter routes/DNS/PF, install itself, or publish a release.
- Do not alter the AmneziaWG protocol or `amneziawg-go` for macOS UI multi-tunnel support.
- Keep macOS multi-tunnel changes separate from iOS single-tunnel behavior.
- A full route may coexist with a more-specific split route. Reject duplicate or ambiguous ownership within the same IP family before activation.
- Keep corporate endpoint and local-LAN routes outside a full tunnel through NetworkExtension excluded routes.
- Do not commit VPN profiles, private or preshared keys, header-protection keys, signing identities, provisioning profiles, or Apple credentials.
- Do not force-push shared branches. The update script creates a local `update/*` branch and never pushes.

The target route ownership is documented in the [architecture record](architecture/README.md).
