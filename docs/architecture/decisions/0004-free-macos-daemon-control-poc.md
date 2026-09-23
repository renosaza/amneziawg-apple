---
id: 0004-free-macos-daemon-control-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
supersedes: null
constraints:
  - C-DAEMON-IPC-EXPERIMENT
  - C-NO-SECRETS
---

# Authenticated daemon-control experiment

## Context

Decision 0003 deliberately leaves a production helper blocked on an
authenticated lifecycle protocol. The no-fee macOS path needs an inspectable
privilege boundary before it can receive any real backend, configuration, or
routing work.

## Decision

Maintain an isolated Go POC that serves exactly one length-bounded JSON
request on each Unix-domain socket connection. It accepts only an explicitly
configured, non-root macOS UID, obtained on Darwin with
`getsockopt(SOL_LOCAL, LOCAL_PEERCRED)`. Its root-created socket lives in a
root-owned non-writable directory, is `0600`, and is owned by that allowed
UID. The listener removes only the exact socket it created with those
attributes.

The fake backend owns only an in-memory set of UUID profile IDs with
`start`, `stop`, `status`, and `list`. It does not receive, persist, print, or
forward configurations, keys, addresses, endpoints, routes, DNS or PF state.

## Consequences

This is not a launchd service, a production helper, or an application backend.
It neither starts `amneziawg-go` nor changes the NetworkExtension path.
Future work needs an independently reviewed configuration boundary, transaction
and cleanup semantics, real routing ownership, installer design, and runtime
validation.
