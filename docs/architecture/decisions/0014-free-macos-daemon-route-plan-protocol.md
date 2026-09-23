---
id: 0014-free-macos-daemon-route-plan-protocol
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
constraints:
  - C-DAEMON-ROUTE-PLAN-PROTOCOL-EXPERIMENT
---

# Daemon route-plan protocol boundary

## Decision

The daemon-control `start` request may carry the JSON form of the pure
`MacOSDaemonRoutePlan`. The IPC boundary accepts only canonical IPv4 CIDRs,
at most 16 entries, `tunnel` and `physicalEndpoint` owners, and literal `/32`
physical endpoints. It rejects IPv6, `/0`, `/1`, unknown JSON fields, and
duplicate destinations. The plan is bound to the request's validated profile
UUID and is rejected for every non-`start` operation.

The daemon validates the plan before it starts a backend child. It neither
stores nor forwards the plan, and does not alter a native route, address, DNS,
PF state, interface, or gateway.

## Consequences

This is an IPC schema boundary only. Route ownership and overlap policy remain
in `WireGuardKit`; a future consumer needs a separately reviewed lifecycle and
native-routing change.
