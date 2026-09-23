---
id: 0008-free-macos-physical-endpoint-route-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
constraints:
  - C-DAEMON-ENDPOINT-ROUTE-EXPERIMENT
---

# Synthetic physical endpoint-route experiment

## Decision

The dispatch-only disposable-runner daemon POC may add one
`203.0.113.10/32` static route through the current effective IPv4 gateway and
non-`utun` interface. It discovers that gateway and interface using a native
PF_ROUTE destination-only lookup; no interface name or gateway is configured.

Before adding the route, the POC rejects an existing exact host route and any
lookup without complete, non-loopback, non-`utun` IPv4 gateway metadata. After
adding, and again before deletion, it requires the exact target, static host
route, recorded gateway, and recorded interface to match. It leaves the route
in place and returns an error when ownership cannot be proven.

It also reads the next TEST-NET-3 address before and after adding the host
route. That lookup must retain the recorded physical gateway and interface;
otherwise the POC rolls back only its still-owned host route.

## Consequences

The workflow sends no traffic. This does not permit real endpoint routing,
default or `/1` routes, DNS, GUI integration, persistent state, or a production
routing daemon. Runtime validation remains manual and limited to a fresh
GitHub macOS runner after independent review.
