---
id: 0002-free-macos-utun-poc
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
supersedes: null
---

# Isolated no-fee `utun` experiment

## Context

The current macOS application uses a Packet Tunnel app extension, which needs
Apple NetworkExtension provisioning. The project needs evidence about a
separate no-fee path before any production architecture decision.

## Decision

Maintain a branch-local POC that starts three empty foreground
`amneziawg-go -f utun` processes. It records each process's kernel-assigned
`utun` name and UAPI socket, refuses hosts with pre-existing `utun` devices,
and performs no profile, address, route, DNS, PF, or GUI operation.

## Consequences

This does not change the production architecture or permit a routing daemon.
Any move beyond PID-to-`utun` isolation requires a separately reviewed design,
synthetic runtime validation on an idle machine, and an explicit update to the
production constraints.
