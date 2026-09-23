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

The helper accepts only an absolute executable whose file and every ancestor
directory are root-owned, non-writable, and not symlinks. Its state directory
and metadata files are root-owned and use `0700`/`0600` permissions. Stop
verifies PID start time and command before signalling; it never removes a UAPI
socket itself. It records baseline `utun` interfaces and rejects a backend
that reports one of them.

## Consequences

This is not a launchd service, a production privilege boundary, a profile
store, or a routing daemon. It does not alter the production NetworkExtension
path. A future production proposal requires explicit approval, a reviewed IPC
authentication design, configuration validation, routing/DNS ownership,
installer design, and real runtime validation.

The PID identity check uses `ps lstart`, whose timestamp has finite granularity.
That is accepted only for this root-owned POC state directory; a production
helper needs an authenticated lifecycle protocol instead.
