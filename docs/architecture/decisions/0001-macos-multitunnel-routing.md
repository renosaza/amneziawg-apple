---
id: 0001-macos-multitunnel-routing
status: accepted
scope: macos-multitunnel
date: 2026-09-23
supersedes: null
---

# Native macOS multi-tunnel routing

## Context

macOS must run a full-tunnel AmneziaWG profile with independent corporate split tunnels without routing their public endpoints through the full tunnel.

## Decision

Use the existing application and NetworkExtension layers. Allow simultaneous tunnel activation on macOS, retain iOS's single-tunnel policy, and keep per-session `utun` ownership and provider lifetime independent.

Route ownership is checked separately for IPv4 and IPv6 before activation. A full/fallback route can coexist with a more-specific route. Equal or otherwise ambiguous split-route ownership and two full routes in one family are activation errors. `ExcludeIPs` is the profile mechanism for LAN, corporate network, and corporate endpoint bypasses.

## Why

NetworkExtension owns system routing and can install excluded routes without a daemon or shell route mutation. The model preserves the upstream architecture and makes the patch series replayable.

## Consequences

Runtime acceptance requires route-table and traffic checks; a connected UI state is insufficient. Split DNS is deferred. The recorded base installs a global DNS match domain for every DNS-owning tunnel, so simultaneous DNS-owning tunnels must not be described as isolated until macOS validation and a split-DNS port exist. Cross-profile endpoint protection is also limited to literal CIDRs; hostname changes require explicit policy handling and route verification.

## Revisit when

Revisit when equivalent behavior lands in Amnezia upstream or when NetworkExtension changes its multi-session routing behavior.
