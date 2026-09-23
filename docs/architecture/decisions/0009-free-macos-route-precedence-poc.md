---
id: 0009-free-macos-route-precedence-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
constraints:
  - C-DAEMON-PRECEDENCE-EXPERIMENT
---

# Synthetic fallback and specific-route precedence experiment

## Decision

The manual disposable-runner POC may model fallback-plus-specific precedence
without touching the runner's default route. It creates a static
`198.51.100.0/24` fallback through one owned synthetic daemon `utun`, and a
more-specific `198.51.100.10/32` route through the previously discovered
physical IPv4 gateway and interface.

The physical route is created first. The fallback route then verifies effective
lookup of `198.51.100.1` through its owned `utun`, while the physical route
verifies effective lookup of `198.51.100.10` through its recorded gateway and
interface. Each route preflights an equivalent route, retains unknown-write
recovery state, and deletes only after it still proves ownership.

The fallback preflight rejects an effective route more specific than `/24` for
its probe. If a concurrent more-specific route shadows the probe after add,
cleanup may use an exact RIB `/24` match only to locate its own route for
deletion; RIB data alone never proves effective route precedence.

## Consequences

This is evidence for longest-prefix precedence only. It does not create a
full-tunnel route, `0/0`, `/1`, traffic, DNS, GUI integration, persistent
state, real endpoint routing, or a production routing daemon. A full-tunnel
runtime experiment needs a separately reviewed exclusion and watchdog design.
