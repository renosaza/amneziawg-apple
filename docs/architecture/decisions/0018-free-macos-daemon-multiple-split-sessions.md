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
`--allow-full-route-runtime`. Only when both flags are present does `hello`
advertise `ipv4-full-route`; an omitted capability remains split-only. The
unsigned Manager can use that explicit capability for one literal-IPv4,
one-peer IPv4 `0.0.0.0/0` profile with no DNS or IPv6, provided `ExcludeIPs`
contains its own endpoint `/32`. It keeps the existing route ownership and
uncertain-start handling; it does not enable either daemon flag itself.
For a logical IPv4 `0.0.0.0/0`, the daemon requires a canonical route plan
whose tunnel routes are the complement of its neutral `excluded` prefixes and
its `physicalEndpoint` `/32`.
It installs those non-default tunnel prefixes, preserving the physical default
route. The daemon also excludes `0/8`, `127/8`, `169.254/16`, and `224/3`
from this root-controlled complement, so it only claims ordinary IPv4 unicast
traffic. It does not add a physical route for an exclusion. Therefore LAN keeps
its connected physical route and a corporate split route can start or stop in
either order without a same-prefix ownership handoff. The full plan is refused
if an active peer endpoint falls inside its effective tunnel prefixes, or if a
candidate peer endpoint does so for an active full plan. This prevents a
corporate endpoint from being nested through the full tunnel.

This does not add IPv6, DNS, hostname resolution, or multi-profile full-route
Manager activation. The backend records and verifies a
physical endpoint `/32` before installing the complement routes. A network
change while a session is active is unsupported by default: stop it and start
it again after the network has settled. The separate root-controlled
`--allow-route-plan-network-rebind` flag polls every two seconds and, only for
route-plan sessions, may update the daemon-owned endpoint `/32` with
`RTM_CHANGE` after a new physical RIB route is unambiguous. It never deletes
the old host route first. A rejected or uncertain kernel result leaves the
session degraded and does not modify sibling sessions. A successful change is
verified before an endpoint-only UAPI refresh that retains no private key.
This is experimental and has no Wi-Fi/Ethernet/sleep runtime proof yet; do not
treat an existing daemon session as safe after a network transition. An exact endpoint host route
is reused without daemon ownership only when its effective lookup and the live physical RIB route
prove the same gateway and non-`utun` interface; every other exact host route, including one through
`utun`, is rejected rather than replaced. No default or
full-route PF_ROUTE mutation has been run on a developer machine or CI runner.
This does not add multi-profile Manager activation. The Manager remains deliberately
single-profile until its lifecycle and routing validation can use this daemon
contract safely.
