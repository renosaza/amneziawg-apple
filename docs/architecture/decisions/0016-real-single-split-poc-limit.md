---
id: 0016-real-single-split-poc-limit
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
---

# Single split-route POC limit

The daemon-control route-plan experiment owns one TEST-NET IPv4 local `/32`,
one TEST-NET tunnel prefix, and one TEST-NET physical endpoint `/32`. It fails
closed when the endpoint resolves through a `utun`; full-tunnel plus split
tunnel physical-gateway discovery is not implemented or authorized here.
