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

The full-route path remains experimental and disabled by default. It needs
both root-controlled launch flags, `--allow-route-plan-runtime` and
`--allow-full-route-runtime`; IPC and the unsigned Manager do not enable it.
For a logical IPv4 `0.0.0.0/0`, the daemon requires a canonical route plan
whose tunnel routes are the complement of its neutral `excluded` prefixes.
It installs those non-default tunnel prefixes, preserving the physical default
route. The daemon also excludes `0/8`, `127/8`, `169.254/16`, and `224/3`
from this root-controlled complement, so it only claims ordinary IPv4 unicast
traffic. It does not add a physical route for an exclusion. Therefore LAN keeps
its connected physical route and a corporate split route can start or stop in
either order without a same-prefix ownership handoff. The full plan is refused
if an active peer endpoint falls inside its effective tunnel prefixes, or if a
candidate peer endpoint does so for an active full plan. This prevents a
corporate endpoint from being nested through the full tunnel.

This does not add IPv6, DNS, hostname resolution, automatic gateway rebinding,
or Manager activation of a full profile. The backend records and verifies a
physical endpoint `/32` before installing the complement routes. A network
change while a session is active is unsupported: stop it and start it again
after the network has settled. Until a rebind design is implemented and
runtime-validated, do not treat an existing daemon session as safe after a
Wi-Fi, Ethernet, sleep, or wake transition. An exact endpoint host route,
including one through `utun`, is rejected rather than replaced. No default or
full-route PF_ROUTE mutation has been run on a developer machine or CI runner.
This does not add multi-profile Manager activation. The Manager remains deliberately
single-profile until its lifecycle and routing validation can use this daemon
contract safely.
