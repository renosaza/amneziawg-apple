#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$script_dir/.." && pwd)
project="$repo_dir/WireGuard.xcodeproj/project.pbxproj"
manager="$repo_dir/Sources/WireGuardApp/Tunnel/DaemonTunnelsManager.swift"

plutil -lint "$project"

python3 - "$project" <<'PY'
from pathlib import Path
import sys

project = Path(sys.argv[1]).read_text()
target = project.split('7A0000102F00000100000001 /* WireGuardmacOSDaemon */ = {', 1)[1].split('\n\t\t};', 1)[0]
assert 'Embed App Extensions' not in target
assert 'Embed Login Item Helper' not in target
assert 'dependencies = (\n\t\t\t);' in target
for configuration in ('7A0000162F00000100000001', '7A0000172F00000100000001'):
    block = project.split(f'{configuration} /*', 1)[1].split('\n\t\t};', 1)[0]
    assert 'CODE_SIGN_ENTITLEMENTS' not in block
    assert 'DAEMON_MODE' in block
PY

if grep -Eq 'NETunnelProviderManager|sendProviderMessage|DaemonControlClient.*\.start|DaemonControlClient.*\.stop' "$manager"; then
    echo 'daemon GUI manager must remain read-only' >&2
    exit 1
fi
grep -Eq 'DaemonControlClient\(socketPath: controlSocketPath\)\.list\(\)' "$manager"

echo "unsigned daemon GUI target self-check passed"
