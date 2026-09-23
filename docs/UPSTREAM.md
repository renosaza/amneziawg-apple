# Upstream state

Current Amnezia upstream base: 9d5ee60edefa95b933a738dd7cda671dd18021fc

WireGuard multi-tunnel reference: [WireGuard/wireguard-apple PR #63](https://github.com/WireGuard/wireguard-apple/pull/63)

Reference head checked: 6632859499b08eae8ff56bdb836af163bde1e2f0

Last upstream sync: 2026-09-23

## WireGuard reference commits

- `7bff8ab` — BSD type definitions.
- `10ba197` — clear restart intent during deactivation.
- `8be76e9` — adapter-specific `utun` attachment.
- `356b80c` — macOS concurrent activation.
- `ff25182` — provider exits only after the last session.
- `dda35a1` — macOS multi-tunnel menu-bar UI.
- `93b9f55` — opt-in split DNS.
- `6f09678` — activate app before a windowless modal alert.
- `56255e6` — unreadable-tunnel editor guard.
- `6632859` — endpoint reset after a macOS network change.

The reference head and the listed ten commits were checked on the date above. They are not ancestors of the recorded Amnezia base. PR #63 is open at the checked reference head; `wireguard/master` is older than PR #63, so the pull-request ref is the comparison source.

## Local-only patches

- `a93da9d` — `ExcludeIPs` wg-quick import/export and model preservation (`Local-Patch: exclude-ips`).
- `bcb02a9` — macOS route ownership validation (`Local-Patch: multi-tunnel-routing-policy`).
- `2ad467f` — local fork signing identifiers and secret hygiene (`Local-Patch: fork-signing-and-secret-hygiene`).
- `449619c` — stop tracking the machine-local signing configuration (`Local-Patch: stop-tracking-local-signing-config`).
- `45a3127`, `e24fd67`, `7bbccc5`, `89e1bf6` — macOS CI prerequisites and SwiftPM test coverage (`Local-Patch: ci-and-test-coverage`).
- `a119620` — macOS alert for an invalid `ExcludeIPs` value during import (`Local-Patch: exclude-ips-validation-alert`).
- `20c0bc3` — enforce the configured handshake freshness cutoff (`Local-Patch: handshake-freshness-cutoff`).

## Imported patch commits

- `36e2612` — `8be76e9`, adapter-specific `utun` attachment.
- `0e87a52` — `356b80c`, macOS concurrent activation.
- `14566df` — `ff25182`, provider exits only after the last session.
- `d206f26` — `dda35a1`, macOS multi-tunnel menu-bar UI.
- `eb534ec` — `6632859`, endpoint reset after a network change.

## Known divergence

Fork-specific routing policy, `ExcludeIPs` import/export, and route-conflict validation are maintained as a separate local patch series once committed. Split DNS (`93b9f55`) is deferred: simultaneous DNS-owning tunnels still use NetworkExtension's global match domain behavior, with no isolation claim. Cross-profile hostname endpoint bypass is also not established; use literal endpoint CIDRs and verify routes until an explicit resolved-endpoint policy exists. Do not duplicate a capability that later lands in Amnezia upstream.

## Latest verified CI

The `CI` workflow succeeded on 2026-09-23 for `08b91030a5aaed483981b5ed6d1e8c067098a306`, the content-equivalent pre-review tip: [run 35828102445](https://github.com/renosaza/amneziawg-apple/actions/runs/35828102445). Runtime validation remains unrun as recorded in `docs/work/runtime-validation.md`.
