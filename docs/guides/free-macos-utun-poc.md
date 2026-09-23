# Free macOS `utun` POC

This is an experimental, command-line-only check of whether three independent
`amneziawg-go` processes can own three macOS `utun` interfaces. It does not
configure profiles, keys, peers, addresses, routes, DNS, PF, or any GUI state.
It is not the NetworkExtension implementation and is not a production VPN
client.

This exception is limited to branch `free-macos-utun-poc`, approved to explore
a no-fee distribution path. The production fork remains governed by
[Constraints](../constraints.md): NetworkExtension owns production routing and
no routing daemon or persistent shell route workaround is accepted.

## Build the pinned backend

Use the AmneziaWG version pinned by this repository:

```sh
mkdir -p out
go build -C Sources/WireGuardKitGo -o ../../out/amneziawg-go github.com/amnezia-vpn/amneziawg-go/v3
scripts/test-utun-poc.sh
```

The second command is a dry-run check. It creates no network state.

## Manual runtime check

Run only on an idle disposable Mac: disconnect all VPNs first. The launcher
refuses to start when any `utun` interface already exists. It requires `sudo`
because the official CLI creates its UAPI sockets under `/var/run/amneziawg`.

```sh
sudo scripts/utun-poc.sh run "$PWD/out/amneziawg-go"
```

It reports a private temporary run directory containing the exact PIDs and
`utun` names. In another terminal, inspect that state without configuring
traffic:

```sh
sudo scripts/utun-poc.sh status /private/tmp/amneziawg-utun-poc.XXXXXX
```

Press Control-C in the `run` terminal to stop exactly those three child
processes and remove their name/PID/log files. The backend closes its own UAPI
sockets; the launcher never unlinks a socket. If the launcher was interrupted,
use `stop` with the printed run directory.

Success proves only PID-to-`utun`-to-UAPI isolation for empty devices. It does
not prove handshake, traffic, endpoint bypass, routing policy, or safe
distribution. Do not add real profiles or keys to this POC.
