#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$script_dir/.." && pwd)
project="$repo_dir/WireGuard.xcodeproj/project.pbxproj"
manager="$repo_dir/Sources/WireGuardApp/Tunnel/DaemonTunnelsManager.swift"
updater="$repo_dir/Sources/WireGuardApp/UI/macOS/DaemonUpdater.swift"
developer_config="$repo_dir/Sources/WireGuardApp/Config/Developer.xcconfig.template"

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

if grep -Eq 'NETunnelProviderManager|sendProviderMessage' "$manager"; then
    echo 'daemon GUI manager must not use Network Extension' >&2
    exit 1
fi
# Decision 0017 permits only this bounded daemon-control lifecycle surface.
grep -Eq 'MacOSDaemonRoutePlan\.buildSingleIPv4Split' "$manager"
grep -Eq 'activeDaemonTunnels\(' "$manager"
grep -Eq 'activeTunnels: activeTunnels' "$manager"
if grep -Eq 'guard statuses\.isEmpty else' "$manager"; then
    echo 'daemon GUI manager must validate new routes against running daemon profiles' >&2
    exit 1
fi
grep -Eq 'client\.start\(' "$manager"
grep -Eq 'DaemonControlClient\(socketPath: Self\.controlSocketPath\)\.stop\(' "$manager"
grep -Eq 'DaemonControlClient\(socketPath: controlSocketPath\)\.list\(\)' "$manager"
grep -Eq -- '--daemon-self-check' "$repo_dir/Sources/WireGuardApp/UI/macOS/AppDelegate.swift"
grep -Eq 'store\.save\(' "$manager"
grep -Eq 'store\.delete\(' "$manager"
grep -Eq 'guard tunnel\.status == \.inactive else' "$manager"
grep -Eq 'uncertainDaemonProfileIDs\.remove\(profileID\)' "$manager"
grep -Eq 'DaemonTunnelState\.isAuthoritativelyAbsent' "$manager"

grep -Fq 'sparkle-project/Sparkle' "$project"
grep -Fq 'kind = exactVersion' "$project"
grep -Fq 'version = 2.10.0' "$project"
python3 - "$project" <<'PY'
from pathlib import Path
import sys

project = Path(sys.argv[1]).read_text()
target = project.split('7A0000102F00000100000001 /* WireGuardmacOSDaemon */ = {', 1)[1].split('\n\t\t};', 1)[0]
assert '7A0002052F00000100000001 /* Sparkle */' in target
for configuration in ('7A0000162F00000100000001', '7A0000172F00000100000001'):
    block = project.split(f'{configuration} /*', 1)[1].split('\n\t\t};', 1)[0]
    assert 'INFOPLIST_KEY_SUEnableAutomaticChecks = YES' in block
    assert 'INFOPLIST_KEY_SURequireSignedFeed = YES' in block
    assert 'INFOPLIST_KEY_SUVerifyUpdateBeforeExtraction = YES' in block
    assert 'SPARKLE_FEED_URL = ""' not in block
    assert 'SPARKLE_PUBLIC_ED_KEY = ""' not in block
PY

grep -Fxq 'SPARKLE_FEED_URL =' "$developer_config"
grep -Fxq 'SPARKLE_PUBLIC_ED_KEY =' "$developer_config"
grep -Fq 'INFOPLIST_KEY_SUFeedURL="$sparkle_feed_url"' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"
grep -Fq 'INFOPLIST_KEY_SUPublicEDKey="$sparkle_public_ed_key"' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"
grep -Fq 'info["SUFeedURL"] = os.environ["SPARKLE_FEED_URL"]' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"
grep -Fq 'info["SUPublicEDKey"] = os.environ["SPARKLE_PUBLIC_ED_KEY"]' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"
grep -Fq 'info["SURequireSignedFeed"] = True' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"
grep -Fq 'info["SUVerifyUpdateBeforeExtraction"] = True' "$repo_dir/scripts/package-unsigned-macos-daemon.sh"

grep -Fq 'url.scheme?.lowercased() == "https"' "$updater"
grep -Fq 'Data(base64Encoded: publicKey)?.count == 32' "$updater"
grep -Fq 'SPUStandardUpdaterController' "$updater"
grep -Fq 'static func isCompatibleDaemon' "$updater"
grep -Fq 'DaemonUpdater.isCompatibleDaemon' "$repo_dir/Sources/WireGuardApp/UI/macOS/AppDelegate.swift"
grep -Fq 'DaemonTunnelsManager.controlSocketPath' "$repo_dir/Sources/WireGuardApp/UI/macOS/AppDelegate.swift"
if grep -Eq 'PrivateKey|PresharedKey|HeaderProtectionKey' "$updater"; then
    echo 'updater source must not contain VPN key material' >&2
    exit 1
fi

echo "unsigned daemon GUI target self-check passed"
