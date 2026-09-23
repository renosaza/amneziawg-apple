---
id: 0013-free-macos-stock-gui-readonly
type: decision
status: accepted
scope: experimental-macos-gui
date: 2026-09-24
supersedes: null
constraints:
  - C-DAEMON-STOCK-GUI-READONLY-EXPERIMENT
  - C-NO-SECRETS
---

# Unsigned macOS stock-GUI read-only experiment

## Context

The user requires the existing AmneziaWG Manager interface rather than a
second client. `NETunnelProviderManager` requires the Network Extension
entitlement, which an unsigned app target cannot use. The stored daemon profile
metadata needs a bounded way to appear in the existing macOS list, menu, and
detail screens without touching Network Extension preferences.

## Decision

Add a separate `WireGuardmacOSDaemon` application target. It shares the stock
macOS UI sources but has no Network Extension entitlement, app extension,
login-item helper, or target dependency on either. Its `DAEMON_MODE` launch
path creates `DaemonTunnelsManager`, which creates daemon-backed
`TunnelContainer` records from `DaemonProfileStore` and maps the bounded
read-only `list` response from `DaemonControlClient` to display status.

Daemon-backed records do not instantiate `NETunnelProviderManager`. Their
runtime detail lookup returns the already loaded configuration, so UI polling
cannot send a provider message. Add, import, edit-save, rename, delete,
on-demand, start, and stop requests fail with a generic unavailable error
before a Network Extension or daemon lifecycle operation. The target does not
send configuration text to the daemon, change routes, addresses, DNS, PF, or
persistent profile state.

## Consequences

This is a launch-time snapshot only; status is not refreshed and it does not
provide daemon lifecycle, configuration mutation, routing, automatic startup,
release automation, or runtime validation. The signed `WireGuardmacOS` target
keeps its existing Network Extension path unchanged. A later lifecycle slice
must be separately reviewed before any configuration is sent to the daemon.
