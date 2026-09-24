# macOS runtime validation

Revision: `82d7bec34c192ebf70b42e2b931e81893478f7fd` (`origin/master`, 2026-09-25).

## Result

PRs #62, #63 and #64 are merged. GitHub Actions run `36060320815` for this revision succeeded.

The signed v4 diagnostic package built from this revision was installed from the local artifact and its daemon hash matched the artifact manifest. The v4 test GUI opened. A single CM activation attempt then showed **Daemon state needs attention** while CM remained **Reactivating**.

Read-only checks after that attempt found the official full tunnel still carried `1.1.1.1` through `utun4`; the CM endpoint and `10.25.0.1` both resolved through the physical interface, so no CM route was installed. The POC idle check succeeded.

The v4 daemon diagnostics have not yet been collected. Do not retry CM before collecting the tail of `/private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl`; an earlier v2 observation of an unknown physical-endpoint lookup does not establish the v4 cause.

## Handoff

Next action, performed by the local user because it requires `sudo`:

```sh
sudo tail -n 25 /private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl
```

Inspect that output for the v4 attempt, then diagnose before another CM activation. Keep the official tunnel active and preserve the requirement that corporate endpoints use the physical uplink.

## Pending matrix

Run these checks on a signed macOS build with synthetic or user-managed profiles, never committing their keys. The experimental unsigned daemon package has a separate [redacted operator checklist](../guides/real-profile-smoke.md); its synthetic CI evidence is not real-profile validation.

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
