---
type: guide
scope: experimental-macos-cli
constraints:
  - C-DAEMON-EXPERIMENT
  - C-NO-SECRETS
---

# Root-only single-`utun` helper POC

This disposable-runner experiment starts one empty `amneziawg-go -f utun`
process, queries its UAPI socket, and stops it. It accepts no VPN
configuration, creates no peer, address, route, DNS, or PF state, and does
not connect to the application GUI.

Run it only on an idle disposable Mac. The helper records existing `utun`
interfaces and only accepts a newly assigned name; it does not modify the
baseline interfaces. This is still not a runtime test to perform beside a
real VPN.

```sh
mkdir -p out
go build -C Sources/WireGuardKitGo -o ../../out/amneziawg-go github.com/amnezia-vpn/amneziawg-go/v3
go build -o out/macos-utun-helper-poc scripts/macos-utun-helper-poc.go
scripts/test-macos-utun-helper-poc.sh
sudo install -d -o root -g wheel -m 0755 /usr/local/libexec
sudo install -o root -g wheel -m 0755 out/amneziawg-go /usr/local/libexec/amneziawg-go-helper-poc
sudo out/macos-utun-helper-poc start -binary /usr/local/libexec/amneziawg-go-helper-poc
```

The final command prints `state_dir`. Use that exact path for the two remaining
operations:

```sh
sudo out/macos-utun-helper-poc status -state-dir /var/run/amneziawg-helper-poc.XXXXXX
sudo out/macos-utun-helper-poc stop -state-dir /var/run/amneziawg-helper-poc.XXXXXX
```

The manual workflow `macOS utun helper POC` performs the same start, UAPI
status, and stop sequence on a fresh GitHub macOS runner. It is manual-only
and should be used only after review on the default branch.
