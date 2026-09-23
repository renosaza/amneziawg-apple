# macOS runtime validation

Revision: `multitunnel-fork` after the patch series recorded in `docs/UPSTREAM.md`.

## Result

Runtime validation was not run. This machine has Command Line Tools only: `xcodebuild -version` reports that the active developer directory is `/Library/Developer/CommandLineTools`, so it cannot build or launch the macOS application. No local VPN profiles were supplied or used.

## Pending matrix

Run these checks on a signed macOS build with synthetic or user-managed profiles, never committing their keys:

- sticky only: confirm ordinary traffic uses its `utun` and LAN uses the physical interface;
- sticky + CM; sticky + pdkkfc; then all three concurrently: confirm independent handshakes and RX/TX;
- stop and restart each one while the other two remain active;
- reject duplicate split ownership and two full tunnels in the same address family;
- repeat after sleep/wake and Wi-Fi-to-Ethernet or Wi-Fi-to-Wi-Fi changes.

From the three-tunnel state, inspect routes with:

```sh
route -n get 10.25.0.1
route -n get 10.1.1.2
route -n get 77.105.177.18
route -n get 95.154.96.103
route -n get 192.168.31.1
route -n get 1.1.1.1
```

Expected owners: CM `utun`, pdkkfc `utun`, physical uplink, physical uplink, physical interface, and sticky `utun`, respectively. Also confirm the corporate endpoint connections use the physical uplink before treating handshakes as sufficient evidence.
