---
id: 0018-free-macos-daemon-multiple-split-sessions
type: decision
status: accepted
scope: experimental-macos-daemon
date: 2026-09-24
constraints:
  - C-DAEMON-ROUTE-PLAN-PROTOCOL-EXPERIMENT
  - C-NO-SECRETS
---

# Multiple isolated IPv4 split sessions in the experimental daemon

## Decision

The root-controlled, opt-in daemon can reserve up to its existing three
sessions when each request supplies its own constrained IPv4 route plan. Each
plan still contains exactly one local `/32`, one tunnel IPv4 split prefix, and
one physical endpoint `/32` matching one peer's UAPI fields.

Reservations are atomic with start. A second plan is rejected before the
backend starts if it reuses a local address or physical endpoint, or its
tunnel prefix overlaps an active or partially-started plan. A failed start
without an owned session releases its reservation. A partial start and a
failed stop keep it until a confirmed stop succeeds. A legacy no-plan session
and planned sessions cannot coexist.

## Consequences

This adds daemon-side ownership checks only. It does not add full/default
routes, IPv6, DNS, `ExcludeIPs`, hostname resolution, or automatic gateway
rebinding. The backend selects one physical IPv4 RIB base route at start and
records its gateway and interface for the endpoint host route. A network
change while a session is active is unsupported: stop it and start it again
after the network has settled. Until a rebind design is implemented and
runtime-validated, do not treat an existing daemon session as safe after a
Wi-Fi, Ethernet, sleep, or wake transition. An exact endpoint host route,
including one through `utun`, is rejected rather than replaced.
This does not add multi-profile Manager activation. The Manager remains deliberately
single-profile until its lifecycle and routing validation can use this daemon
contract safely.
