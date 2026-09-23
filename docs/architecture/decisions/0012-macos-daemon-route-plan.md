---
id: 0012-macos-daemon-route-plan
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
constraints:
  - C-DAEMON-EXPERIMENT
---

# Pure macOS daemon route plan

## Decision

`MacOSDaemonRoutePlan` is a pure per-session `WireGuardKit` model. It receives
one activating configuration, its peer-aligned resolved endpoints, and active
configurations only for conflict validation. It describes normalized activating
tunnel CIDRs and literal endpoint CIDRs with only a destination and owner
kind. It does not receive keys, gateways, interfaces, profile names, endpoint
ports, or configuration text.

The plan is a deterministic route set. A future consumer must apply
longest-prefix selection: a physical endpoint `/32` or `/128` wins over an
overlapping tunnel CIDR. This model neither selects an interface nor mutates a
route table.

## Consequences

The plan does not use daemon IPC, alter native routes, activate a tunnel, or
authorize a production daemon integration. A later lifecycle change must be
reviewed separately before it consumes this model.
