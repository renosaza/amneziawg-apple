#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$script_dir/.." && pwd)
daemon_client_temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-daemon-client.XXXXXX")
trap 'rm -rf "$daemon_client_temp_dir"' EXIT

swiftc \
    "$repo_dir/Sources/WireGuardApp/Tunnel/DaemonControlClient.swift" \
    "$repo_dir/scripts/daemon-control/client_selftest.swift" \
    -o "$daemon_client_temp_dir/daemon-control-client-selftest"
"$daemon_client_temp_dir/daemon-control-client-selftest"
