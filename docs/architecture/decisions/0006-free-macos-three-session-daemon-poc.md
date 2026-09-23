---
id: 0006-free-macos-three-session-daemon-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
supersedes: 0005-free-macos-single-profile-daemon-poc
constraints:
  - C-DAEMON-THREE-SESSION-EXPERIMENT
  - C-NO-SECRETS
---

# Three-session daemon lifecycle experiment

## Decision

Extend the authenticated daemon-control POC from one to at most three
in-memory UAPI configurations. Each UUID starts its own root-owned
`amneziawg-go -f utun` child, waits for its own newly-created `utun` and UAPI
socket, and retains its own child handle for status and stop. Starting or
stopping one UUID must not operate on another UUID's child.

## Consequences

This remains limited to manually dispatched, disposable-runner tests with
synthetic keys. The control protocol keeps configurations in memory only and
does not return them. It adds no profile persistence, route, address, DNS, PF,
GUI, installer, automatic startup, or traffic configuration. Lifecycle/status
checks prove separate child ownership; traffic testing needs interface
addresses and routes and is outside this experiment.

The manual GitHub Actions workflow starts three synthetic children, verifies
each status, stops the middle child, verifies the other two remain running, and
cleans up the remaining children. It must remain manual-only and must never
publish artifacts or releases.
