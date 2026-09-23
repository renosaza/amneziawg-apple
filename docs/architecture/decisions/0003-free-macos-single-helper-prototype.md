---
id: 0003-free-macos-single-helper-prototype
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
supersedes: null
constraints:
  - C-DAEMON-EXPERIMENT
  - C-NO-SECRETS
---

# Root-only single-`utun` helper prototype

## Context

The synthetic multi-`utun` POC established that separate `amneziawg-go`
processes can exchange traffic on a disposable macOS runner. A future no-fee
architecture needs a separately inspectable privilege boundary before it can
be considered for application integration.

## Decision

Maintain a root-invoked command-line prototype that starts exactly one empty
`amneziawg-go -f utun` process, checks its UAPI `get=1` response, reports its
kernel-assigned interface, and later stops only the recorded process. It
accepts no WireGuard or AmneziaWG profile and therefore cannot receive a user
key, peer, address, route, DNS value, or PF rule.

The helper accepts only an absolute, root-owned, non-writable executable. Its
state directory and metadata files are root-owned and use `0700`/`0600`
permissions. Stop verifies PID start time and command before signalling; it
never removes a UAPI socket itself. Start refuses any host that already has a
`utun` interface.

## Consequences

This is not a launchd service, a production privilege boundary, a profile
store, or a routing daemon. It does not alter the production NetworkExtension
path. A future production proposal requires explicit approval, a reviewed IPC
authentication design, configuration validation, routing/DNS ownership,
installer design, and real runtime validation.
