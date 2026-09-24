---
id: 0014-free-macos-daemon-route-plan-protocol
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
constraints:
  - C-DAEMON-ROUTE-PLAN-PROTOCOL-EXPERIMENT
---

# Daemon route-plan protocol boundary

## Decision

The daemon-control `start` request may carry the JSON form of the pure
`MacOSDaemonRoutePlan`. The IPC boundary requires one canonical IPv4
`local_address` `/32`, accepts at most 160 canonical IPv4 route CIDRs,
`tunnel`, `excluded`, and `physicalEndpoint` owners, and one literal `/32`
physical endpoint. A split plan has one tunnel CIDR. A full plan has tunnel
CIDRs that are exactly the daemon's safe IPv4 complement of every `excluded`
CIDR and the `physicalEndpoint` `/32`. It rejects IPv6, unknown JSON fields,
and duplicate destinations. The plan is bound to the request's validated
profile UUID and is rejected for every non-`start` operation.

Every client operation first sends the keyless `hello` request. It contains no
profile ID, configuration, or route plan, and succeeds only when the daemon
returns the exact integer protocol version. A missing, malformed, rejected, or
mismatched response is incompatible; the client sends no configuration after
that result. The request version remains exact, so a daemon replacement after
`hello` also rejects an incompatible later operation before applying it.

Before it starts a backend child, the daemon checks that the one peer, one
`allowed_ip`, and one literal `endpoint` UAPI fields match the route plan;
`physicalEndpoint` cannot name an arbitrary extra destination. It atomically
reserves all full-plan tunnel prefixes, then rejects a second full route, a
split-prefix overlap, duplicate endpoint, or an endpoint that falls inside a
concurrent full route. Hostname or mismatched physical endpoints are rejected.

## Consequences

The authorized local UID supplies both UAPI configuration and route plan. The
plan is therefore the authoritative routing policy for that UID session, not
a cryptographic attestation of the profile store: a second client-controlled
`ExcludeIPs` field or digest would add no trust. The root daemon enforces the
canonical plan shape and peer/endpoint/ownership invariants above, but does
not claim that it can distinguish a malicious authorized user from that
user's intended profile. A root-owned profile store or signer would require a
separate security design before such an attestation claim is valid. The
fresh-install POC still has no update mechanism.
