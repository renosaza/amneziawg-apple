---
id: 0011-free-macos-daemon-profile-store
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
supersedes: null
constraints:
  - C-DAEMON-PROFILE-STORE-EXPERIMENT
  - C-NO-SECRETS
---

# Isolated macOS daemon-profile storage experiment

## Context

The unsigned daemon experiments retain profile UUIDs only in memory. A later,
separately reviewed lifecycle integration needs a stable user-side profile
identity without placing configuration secrets in Application Support or a
NetworkExtension-owned Keychain item.

## Decision

Add a macOS-only `DaemonProfileStore`. Its `profiles.json` is atomically
replaced with mode `0600` under the user's Application Support directory and
contains only profile UUIDs and names. Each full wg-quick configuration is
validated with the existing `TunnelConfiguration` parser and stored under its
UUID in an ordinary login Keychain generic-password item. The item uses a
fixed service name and no NetworkExtension access-group or trusted-application
ACL.

Save and delete restore the previous Keychain value if their metadata commit
fails. Rename changes metadata only. The store does not log configuration
text or key material. Its automated test injects an in-memory secret store
and uses generated synthetic keys.

## Consequences

This only establishes storage primitives. It does not import a real profile,
start a daemon session from stored data, expose storage in the GUI, add routes,
addresses, DNS, PF, installer integration, automatic startup, release
automation, or runtime validation. Those changes remain prohibited until a
separate safe-routing and lifecycle decision is reviewed.
