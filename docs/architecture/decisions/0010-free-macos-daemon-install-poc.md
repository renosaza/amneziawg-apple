---
id: 0010-free-macos-daemon-install-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-24
supersedes: null
constraints:
  - C-DAEMON-INSTALL-EXPERIMENT
  - C-NO-SECRETS
---

# Manual LaunchDaemon installation experiment

## Context

The synthetic daemon-control POC has a root-only backend and a control socket,
but it has no repeatable way to install those two executables at stable,
root-owned paths. This experiment validates only a fresh-install boundary on a
disposable macOS runner.

## Decision

Maintain a manually invoked `sudo` script that installs fixed paths under
`/usr/local/libexec`, a `root:wheel` `0644` plist at
`/Library/LaunchDaemons/com.amneziawg.daemon-control.poc.plist`, and a
root-owned `0755` socket directory below `/var/run`. The launcher accepts one
non-root decimal UID. It must equal the graphical console UID; on a headless
runner it must equal the original `sudo` caller.

The script validates source binaries as regular, executable, non-symlink files
owned by root or the invoking user with no group or other write permission. It
also validates every ancestor before copying. Installed binaries are rechecked
as `root:wheel` `0755` before launch.

Before uninstall, the script runs the installed daemon-control binary
as the allowed UID. Its `-prepare-stop` mode sends an authenticated, keyless
`quiesce` request that atomically rejects nonempty state and prevents a new
session from racing the subsequent stop. Unknown socket or service state fails
closed. After that proof, `launchctl bootout` asks the daemon to clean its own
children and socket before files are removed.

The manual GitHub workflow builds synthetic binaries, installs them, verifies
idle status, uninstalls them, and checks the stable paths are gone. Fresh-install failure removes partial files so retry is possible. Update is intentionally unsupported. It does not
send traffic or publish artifacts.

## Consequences

This is not a production installer, an update mechanism, a GUI backend, an automatic startup
system, a release mechanism, or an entitlement/signing solution. Source-path
checks are local build hygiene, not signed-artifact verification. The POC
accepts no profile or key in installer arguments, plist values, or its own
output. A production proposal needs signed distribution, rollback semantics,
real configuration and lifecycle policy, and an independently reviewed
privilege boundary.
