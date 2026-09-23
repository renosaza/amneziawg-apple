---
id: 0005-free-macos-single-profile-daemon-poc
type: decision
status: deprecated
scope: experimental-macos-cli
date: 2026-09-23
supersedes: null
constraints:
  - C-DAEMON-SINGLE-PROFILE-EXPERIMENT
  - C-NO-SECRETS
---

# One-profile daemon lifecycle experiment

Superseded by [Decision 0006](0006-free-macos-three-session-daemon-poc.md).

## Decision

Extend the authenticated daemon-control POC with one in-memory UAPI
configuration and one root-owned `amneziawg-go -f utun` child. The control
socket accepts only a bounded, line-validated UAPI payload for `start`; it is
never written to disk, command arguments, logs, or responses. The helper waits
for a newly created `utun` and UAPI socket, and keeps the spawned child handle
in memory for bounded stop and cleanup.

## Consequences

This permits only manual disposable-runner tests with synthetic keys. It adds
no route, address, DNS, PF, persistence, GUI, installer, automatic startup,
or multi-profile behavior. A failed or stopped session must leave no helper
state directory or UAPI socket owned by the experiment.

The manually dispatched GitHub Actions workflow is the runtime test gate. It
starts one synthetic-key child, probes its redacted status, and stops it on a
fresh macOS runner. It must never be changed to push, pull-request, scheduled,
or release triggering execution.
