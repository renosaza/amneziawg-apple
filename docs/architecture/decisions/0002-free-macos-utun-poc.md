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

Maintain branch-local POCs for three foreground `amneziawg-go -f utun`
processes and for three synthetic loopback pairs. They record kernel-assigned
`utun` names and UAPI sockets, refuse hosts with pre-existing `utun` devices,
and use no user profile, default route, DNS, PF, or GUI operation. The traffic
proof may install only its TEST-NET `/32` peer routes and cleans up those exact
routes before stopping its own child processes.

## Consequences

This does not change the production architecture or permit a routing daemon.
Any move beyond PID-to-`utun` isolation requires a separately reviewed design,
synthetic runtime validation on an idle machine, and an explicit update to the
production constraints.
