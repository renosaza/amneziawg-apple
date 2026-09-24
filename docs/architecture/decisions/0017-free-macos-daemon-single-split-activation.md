---
id: 0017-free-macos-daemon-single-split-activation
type: decision
status: accepted
scope: experimental-macos-gui
date: 2026-09-24
constraints:
  - C-DAEMON-SINGLE-SPLIT-ACTIVATION-EXPERIMENT
  - C-NO-SECRETS
---

# Unsigned daemon single-split activation boundary

## Decision

The existing unsigned macOS Manager target may start and stop one stored daemon
profile through the version-checked daemon-control socket. The client accepts
only one IPv4 interface address, one peer, one literal IPv4 endpoint, and one
IPv4 `AllowedIPs` prefix from `/2` through `/32`. The literal endpoint is
preserved directly in generated UAPI; hostname resolution is not invoked by
this slice. DNS, IPv6, default and `/1` routes, `ExcludeIPs`, on-demand, and
concurrent profiles are rejected before IPC.

The Manager builds `MacOSDaemonRoutePlan` through `RouteOwnershipValidator` and
uses `PacketTunnelSettingsGenerator` for the in-memory UAPI configuration. It
sends both only after the keyless `hello` compatibility request succeeds. The
configuration remains in the ordinary login Keychain profile store; it is never
logged, returned by errors, or written to a temporary file. Erasure of the
encoded IPC frame is best effort only.

The profile moves to `active` only after the daemon returns the expected
`running` response. Its one-session check is a GUI guard only; it does not
replace future daemon-side atomic enforcement. Immediately before `start`, the
same serial queue lists daemon sessions and refuses activation if any session is
present; an unavailable list leaves the profile `reasserting`. A start failure
returns it to `inactive` only after a running daemon result is stopped and that
stop is confirmed. A missing result after an ambiguous start is still
`reasserting`, because the original request may commit later; every
inconclusive result gives the user a recovery action.
If the Manager record disappears after a successful response, it stops by the
stable UUID, retries once if confirmation is unavailable, and logs only the
profile UUID if cleanup remains unconfirmed. `refreshStatuses()` also performs
one asynchronous bounded list
refresh, without replacing activation or deactivation transitions. Stop does
not change other record statuses. The signed Network Extension target and iOS
are unchanged.

## Consequences

This is a client lifecycle slice only. It applies no native route, address,
DNS, PF, gateway, or interface configuration and it does not make the existing
synthetic, runtime-gated route-plan POC (Decision 0016) a general VPN backend. It
accepts only the synthetic manual-runtime scenario, so ordinary stored profiles remain
unavailable. This GUI patch must remain a draft until an installed, separately reviewed
native backend accepts the same constrained contract.
