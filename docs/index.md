# Documentation index

- [Constraints](constraints.md): repository safety and upstream rules.
- [Architecture](architecture/README.md): macOS multi-tunnel boundaries and routing invariant.
- [Decision 0001](architecture/decisions/0001-macos-multitunnel-routing.md): accepted routing and ownership policy.
- [Decision 0002](architecture/decisions/0002-free-macos-utun-poc.md): limited no-fee `utun` experiment.
- [Decision 0012](architecture/decisions/0012-macos-daemon-route-plan.md): pure route-plan experiment.
- [Decision 0003](architecture/decisions/0003-free-macos-single-helper-prototype.md): root-only single-`utun` helper prototype.
- [Decision 0004](architecture/decisions/0004-free-macos-daemon-control-poc.md): authenticated fake daemon-control boundary.
- [Decision 0005](architecture/decisions/0005-free-macos-single-profile-daemon-poc.md): one-profile daemon lifecycle experiment.
- [Decision 0006](architecture/decisions/0006-free-macos-three-session-daemon-poc.md): three-session daemon lifecycle experiment.
- [Decision 0007](architecture/decisions/0007-free-macos-ipv4-route-poc.md): native synthetic IPv4 route experiment.
- [Decision 0008](architecture/decisions/0008-free-macos-physical-endpoint-route-poc.md): synthetic physical endpoint-route experiment.
- [Decision 0009](architecture/decisions/0009-free-macos-route-precedence-poc.md): synthetic fallback and specific-route precedence experiment.
- [Decision 0010](architecture/decisions/0010-free-macos-daemon-install-poc.md): manual LaunchDaemon installation experiment.
- [Decision 0011](architecture/decisions/0011-free-macos-daemon-profile-store.md): isolated macOS daemon-profile storage experiment.
- [Decision 0013](architecture/decisions/0013-free-macos-stock-gui-readonly.md): unsigned stock-Manager profile-management experiment.
- [Decision 0016](architecture/decisions/0016-real-single-split-poc-limit.md): synthetic native route-plan POC limit.
- [Decision 0017](architecture/decisions/0017-free-macos-daemon-single-split-activation.md): unsigned Manager single-IPv4-split activation boundary.
- [Decision 0018](architecture/decisions/0018-free-macos-daemon-multiple-split-sessions.md): experimental daemon ownership checks for multiple disjoint IPv4 split sessions.
- [Building](BUILDING.md): local macOS build and signing prerequisites.
- [Local development](guides/local-development.md): verified development checks.
- [Troubleshooting](guides/troubleshooting.md): verified local toolchain limitation.
- [Updating](UPDATING.md): upstream replay procedure.
- [Upstream state](UPSTREAM.md): recorded Amnezia base and WireGuard reference.
- [Runtime validation](work/runtime-validation.md): actual local runtime-check status and pending matrix.
- [Free macOS `utun` POC](guides/free-macos-utun-poc.md): synthetic-only CLI experiment.
- [Synthetic `utun` traffic POC](guides/free-macos-utun-traffic-poc.md): manual disposable-runner proof.
- [Single-`utun` helper POC](guides/free-macos-single-helper-poc.md): isolated root-helper start/status/stop proof.

Current task status belongs in the repository's GitHub issues and pull requests. This directory records durable constraints and verified procedures only.
