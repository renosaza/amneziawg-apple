---
id: 0015-synthetic-split-route-poc-limit
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
---

# Synthetic split-route POC limit

The manual daemon test verifies TEST-NET `/24` plus a more-specific `/32` on
separate synthetic sessions through stop and restart. This proves only the
disposable POC route lifecycle; it does not authorize production endpoint or
route policy.
