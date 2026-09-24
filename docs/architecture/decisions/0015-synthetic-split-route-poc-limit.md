---
id: 0015-synthetic-split-route-poc-limit
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
---

# Synthetic split-route POC limit

The manual daemon test uses disjoint TEST-NET `/24` and `/32` routes. It does
not verify or authorize cross-session overlapping `/24` and `/32` ownership
during stop and restart. That case remains unsupported until separately tested.
