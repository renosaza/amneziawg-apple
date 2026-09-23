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

Save stages a versioned pending Keychain item before committing metadata, then
promotes it to the UUID's active item. A `0600` non-secret journal completes or
rolls back an interrupted operation under an advisory file lock; a later store
call recovers an interrupted transaction without guessing at secret content.
If a pending item remains without a journal, the store either discards it while
preserving an existing active item, restores it only when matching metadata has
no active item, or fails closed for an unowned active secret. Metadata and
journal replacement and removal use checked directory-descriptor `fsync` calls.
Failures before a metadata commit restore the previous state. After a metadata
commit, the journal completes the operation on a later call instead of trying
to guess whether a Keychain mutation reached durable storage. Rename changes
metadata only. Metadata and journal reads use `O_NOFOLLOW` descriptors and
validate the opened inode's owner and mode. The store does not log
configuration text or key material. Its automated tests inject an in-memory
secret store, exercise recovery and `ExcludeIPs`, and use a unique-service
login-Keychain item with cleanup.

## Consequences

This only establishes storage primitives. It does not import a real profile,
start a daemon session from stored data, expose storage in the GUI, add routes,
addresses, DNS, PF, installer integration, automatic startup, release
automation, or runtime validation. Those changes remain prohibited until a
separate safe-routing and lifecycle decision is reviewed.
