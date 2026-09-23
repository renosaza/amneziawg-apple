# Synthetic macOS `utun` traffic POC

This branch-local experiment is designed to prove three separate loopback AmneziaWG pairs:
six `amneziawg-go` processes, six kernel-assigned `utun` interfaces, three
handshakes, and encrypted ICMP traffic in both directions. It uses only
deterministic synthetic keys, `127.0.0.1` UDP endpoints, and TEST-NET-1
addresses `192.0.2.0/24`.

It does not load a user profile, change a default route, configure DNS or PF,
or modify the application GUI. It is not a distribution-ready VPN client.

## Local invocation

Use only a disposable Mac. The harness records existing interfaces and routes
before creating anything. It refuses a test destination already routed through
`utun` or an existing host route, and later verifies that the recorded baseline
interfaces and test-destination routes are unchanged.

```sh
mkdir -p out
go build -C Sources/WireGuardKitGo -o ../../out/amneziawg-go github.com/amnezia-vpn/amneziawg-go/v3
scripts/test-utun-poc-traffic.sh
sudo out/utun-poc-traffic -binary "$PWD/out/amneziawg-go"
```

For each pair, the harness configures two TEST-NET addresses and, only when
macOS did not add the peer route itself, an exact `/32` host route through the
matching `utun`. It sends one ICMP request from each client to its server and
requires nonzero UAPI handshake, RX and TX counters on all six devices.

On success or failure it deletes only routes that it created and terminates
only its six child processes. It never removes UAPI sockets itself. Do not run
it on a Mac with a real VPN.

## Disposable CI

`.github/workflows/utun-poc.yml` runs only by manual dispatch after reviewed
code has been merged to the default branch. It builds the pinned backend, runs
the no-network self-check, then runs the proof with `sudo` on a fresh GitHub
macOS runner. Existing unrelated `utun` interfaces are preserved; a route
collision on a test destination causes a failure before any change. No CI run
has been recorded from this branch yet.
