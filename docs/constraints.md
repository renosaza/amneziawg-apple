---
type: constraints
active_ids:
  - C-DAEMON-EXPERIMENT
  - C-DAEMON-IPC-EXPERIMENT
  - C-DAEMON-THREE-SESSION-EXPERIMENT
---

# Constraints

- Keep the fork as an isolated patch series over `amnezia-vpn/amneziawg-apple`; replay it with rebase and inspect it with `git range-diff`.
- Use native NetworkExtension routing. Do not add a routing daemon or persistent shell `route` workaround in production.
- `C-DAEMON-EXPERIMENT`: Decision 0003 establishes the root-only synthetic-helper boundary. It is not a production routing daemon, installer, or GUI backend; configuration scope is controlled by the later specific daemon constraints.
- `C-DAEMON-IPC-EXPERIMENT`: Decision 0004 establishes the root-created, UID-authenticated Unix-socket control boundary and in-memory UUID state. It does not itself authorize a VPN backend, profile persistence, routes, DNS, or PF state.
- `C-DAEMON-THREE-SESSION-EXPERIMENT`: Decision 0006 permits up to three independent root-owned, in-memory UAPI-configured `utun` children only for manual disposable-runner tests with synthetic keys. It may not persist configurations or alter routes, addresses, DNS, PF, GUI, installer, or automatic startup.
- Do not alter the AmneziaWG protocol or `amneziawg-go` for macOS UI multi-tunnel support.
- Keep macOS multi-tunnel changes separate from iOS single-tunnel behavior.
- A full route may coexist with a more-specific split route. Reject duplicate or ambiguous ownership within the same IP family before activation.
- Keep corporate endpoint and local-LAN routes outside a full tunnel through NetworkExtension excluded routes.
- Do not commit VPN profiles, private or preshared keys, header-protection keys, signing identities, provisioning profiles, or Apple credentials.
- Do not force-push shared branches. The update script creates a local `update/*` branch and never pushes.

The target route ownership is documented in the [architecture record](architecture/README.md).
