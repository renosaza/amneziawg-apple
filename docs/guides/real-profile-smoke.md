---
type: guide
status: draft
scope: experimental-macos-daemon
date: 2026-09-24
constraints:
  - C-DAEMON-ROUTE-PLAN-PROTOCOL-EXPERIMENT
  - C-DAEMON-SINGLE-SPLIT-ACTIVATION-EXPERIMENT
  - C-NO-SECRETS
---

# Real macOS three-profile smoke test

This is an operator checklist for the experimental unsigned daemon package.
The CI checks use disposable TEST-NET addresses and synthetic traffic only.
They prove native `utun` and route lifecycle for that test shape; they do not
prove a real profile's handshake, traffic, endpoint bypass, or macOS network
change behavior.

Do this on a Mac you control, with profiles imported locally through the GUI.
Never put an exported profile, key material, full diagnostic output, or a
private endpoint into an issue, CI log, or repository.

## Prepare

1. Obtain a reviewed unsigned CI package, unpack it, inspect `MANIFEST.json`,
   and use macOS's ordinary Control-click Open / Privacy & Security approval.
2. From the unpacked directory, install its matching daemon with the current
   console UID. The root action is explicit and the installer is update-free:

   ```sh
   sudo ./Daemon/install-daemon-control-poc.sh install \
     --daemon "$PWD/Daemon/amneziawg-daemon-control-poc" \
     --amneziawg-go "$PWD/Daemon/amneziawg-go-daemon-control-poc" \
     --uid "$(id -u)" \
     --allow-route-plan-runtime \
     --allow-full-route-runtime
   ```

3. Import three locally controlled profiles: one IPv4 full profile and two
   non-overlapping IPv4 split profiles. The full profile must include its own
   literal peer endpoint `/32` in `ExcludeIPs`; corporate split endpoint
   addresses must remain outside the full route as well. Keep DNS and IPv6
   absent for this experimental path.

The current GUI rejects hostname endpoints, multiple peers, DNS, IPv6, and
on-demand. It accepts one literal IPv4 peer per profile. Do not proceed after
a network transition; stop the sessions and start them again after the uplink
has settled.

## Smoke matrix

Run each phase in the GUI and wait for its own status, handshake age, and RX/TX
counters to update before continuing.

| Phase | Expected result |
| --- | --- |
| full only | Public IPv4 uses its `utun`; local LAN remains physical. |
| full + split A | Split A destination uses a different `utun`; its public endpoint stays physical. |
| full + split B | Same independent ownership for split B. |
| all three | Three active profiles, three distinct `utun` names, and handshakes/RX/TX for each. |
| stop each profile | The other two remain active and retain traffic; restart the stopped profile. |

For every phase, read the selected routes locally. Substitute only local values
for the six placeholders; do not publish the resulting report:

```sh
./Daemon/real-profile-diagnostics.sh --route \
  <split-A-destination-IPv4> \
  <split-B-destination-IPv4> \
  <split-A-public-endpoint-IPv4> \
  <split-B-public-endpoint-IPv4> \
  <local-LAN-IPv4> \
  <ordinary-public-IPv4>
./Daemon/real-profile-diagnostics.sh --utuns
./Daemon/real-profile-diagnostics.sh --daemon-status
```

Expected ownership is split A `utun`, split B `utun`, physical, physical,
physical, and full-profile `utun`, respectively. The two split endpoint routes
must be physical even while the full profile is active. A successful handshake
alone is insufficient evidence.

The helper only accepts literal IPv4 arguments and prints `destination`,
`gateway`, and `interface` fields. It never reads UAPI, the Keychain, or a
profile. It makes no route, DNS, PF, daemon, or configuration change.

## Report only redacted observations

Use this form for a private maintainer report:

```text
Package commit: <public commit SHA>
macOS: <version>
Daemon state: <state only>
Profiles: full/split-A/split-B active: yes/no
utun names distinct: yes/no
Route owners: split-A=<utun|physical>, split-B=<utun|physical>,
  endpoint-A=<physical|utun>, endpoint-B=<physical|utun>,
  LAN=<physical|utun>, public=<utun|physical>
Handshake and RX/TX observed for all three: yes/no
Stop/restart each profile preserved the other two: yes/no
Network changed during test: yes/no
```

Do not include names, keys, configuration text, complete route output, exact
endpoints, internal destinations, or external IP addresses. A sleep, wake,
Wi-Fi roam, or Ethernet change is currently a known unsupported runtime state,
not a passing test.
