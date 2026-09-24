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
routes, IPv6, DNS, `ExcludeIPs`, hostname resolution, gateway selection, or
multi-profile Manager activation. The Manager remains deliberately
single-profile until its lifecycle and routing validation can use this daemon
contract safely.
