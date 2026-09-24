---
id: 0013-free-macos-stock-gui-readonly
type: decision
status: accepted
scope: experimental-macos-gui
date: 2026-09-24
supersedes: null
constraints:
  - C-DAEMON-STOCK-GUI-PROFILE-MUTATION-EXPERIMENT
  - C-NO-SECRETS
---

# Unsigned macOS stock-GUI profile-management experiment

## Context

The user requires the existing AmneziaWG Manager interface rather than a
second client. `NETunnelProviderManager` requires the Network Extension
entitlement, which an unsigned app target cannot use. The stored daemon
profiles need a bounded way to appear in the existing macOS list, menu, and
detail screens and use the existing import/editor/delete flows without touching
Network Extension preferences.

## Decision

Add a separate `WireGuardmacOSDaemon` application target. It shares the stock
macOS UI sources but has no Network Extension entitlement, app extension,
login-item helper, or target dependency on either. Its `DAEMON_MODE` launch
path creates `DaemonTunnelsManager`, which creates daemon-backed
`TunnelContainer` records from `DaemonProfileStore` and maps the bounded
read-only `list` response from `DaemonControlClient` to display status.

Daemon-backed records do not instantiate `NETunnelProviderManager`. Their
runtime detail lookup returns the already loaded configuration, so UI polling
cannot send a provider message. Add/import and edit-save/rename validate and
persist through `DaemonProfileStore`; an existing record keeps its UUID. The
in-memory list changes only after the store operation succeeds. Delete is
allowed only for an inactive record so an active or degraded daemon session
cannot lose its profile. On-demand fails before a Network Extension or daemon
lifecycle operation. Decision 0017 adds the later, constrained start/stop
exception; this target otherwise does not send configuration text to the
daemon or change routes, addresses, DNS, or PF.

## Consequences

This remains a launch-time status snapshot. Profile changes affect stored
configuration for a later lifecycle slice but never an already running daemon
session. It does not provide daemon lifecycle, routing, automatic startup,
release automation, or runtime validation. The signed `WireGuardmacOS` target
keeps its existing Network Extension path unchanged. A later lifecycle slice
must be separately reviewed before any configuration is sent to the daemon.
