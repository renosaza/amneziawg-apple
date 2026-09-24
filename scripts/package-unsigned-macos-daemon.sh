#!/bin/bash
# SPDX-License-Identifier: MIT
#
# Builds a reviewable macOS CI artifact for the experimental unsigned daemon GUI.

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
output=${1:-"$root/out"}
configuration=Release
working=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-package-build.XXXXXX")
build_products="$working/build-products"
sparkle_update_output=${SPARKLE_UPDATE_OUTPUT:-}
sparkle_feed_url=${SPARKLE_FEED_URL:-}
sparkle_public_ed_key=${SPARKLE_PUBLIC_ED_KEY:-}

fail() {
    printf 'unsigned macOS package: %s\n' "$*" >&2
    exit 1
}

[[ $(/usr/bin/uname -s) == Darwin ]] || fail 'macOS is required'
[[ -f "$root/Sources/WireGuardApp/Config/Developer.xcconfig" ]] || fail 'create Developer.xcconfig from its tracked template first'

if [[ -n $sparkle_feed_url || -n $sparkle_public_ed_key ]]; then
    [[ $sparkle_feed_url =~ ^https://[^[:space:]]+$ ]] || fail 'SPARKLE_FEED_URL must be an HTTPS URL'
    SPARKLE_PUBLIC_ED_KEY="$sparkle_public_ed_key" python3 - <<'PY'
import base64
import os

try:
    assert len(base64.b64decode(os.environ["SPARKLE_PUBLIC_ED_KEY"], validate=True)) == 32
except (AssertionError, ValueError):
    raise SystemExit("SPARKLE_PUBLIC_ED_KEY must be a base64 Ed25519 public key")
PY
else
    sparkle_feed_url=""
    sparkle_public_ed_key=""
fi

if [[ -n $sparkle_update_output ]]; then
    [[ $sparkle_update_output != *$'\n'* ]] || fail 'SPARKLE_UPDATE_OUTPUT contains a newline'
    mkdir -p "$sparkle_update_output"
fi

mkdir -p "$output"
trap 'rm -rf "$working"' EXIT

protocol_from_go=$(/usr/bin/sed -nE 's/^[[:space:]]*protocolVersion[[:space:]]*=[[:space:]]*([0-9]+).*/\1/p' "$root/scripts/daemon-control/server.go")
protocol_from_swift=$(/usr/bin/sed -nE 's/^[[:space:]]*static let protocolVersion = ([0-9]+).*/\1/p' "$root/Sources/WireGuardApp/Tunnel/DaemonControlClient.swift")
[[ $protocol_from_go =~ ^[0-9]+$ && $protocol_from_go == "$protocol_from_swift" ]] || fail 'daemon protocol versions disagree'

make -C "$root/Sources/WireGuardKitGo"
mkdir -p "$build_products/$configuration"
make -C "$root/Sources/WireGuardKitGo" version-header CONFIGURATION_BUILD_DIR="$build_products/$configuration"
go build -C "$root/Sources/WireGuardKitGo" -o "$working/amneziawg-go-daemon-control-poc" github.com/amnezia-vpn/amneziawg-go/v3
go build -C "$root/scripts/daemon-control" -o "$working/amneziawg-daemon-control-poc" ./cmd/daemon-control-poc
xcodebuild -project "$root/WireGuard.xcodeproj" \
    -target WireGuardmacOSDaemon \
    -configuration "$configuration" \
    -sdk macosx \
    SYMROOT="$build_products" \
    CODE_SIGNING_ALLOWED=NO \
    CODE_SIGNING_REQUIRED=NO \
    SPARKLE_FEED_URL="$sparkle_feed_url" \
    SPARKLE_PUBLIC_ED_KEY="$sparkle_public_ed_key" \
    build

app_source="$build_products/$configuration/AmneziaWG.app"
[[ -d "$app_source" && -x "$app_source/Contents/MacOS/AmneziaWG" ]] || fail 'daemon GUI product is missing'

version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app_source/Contents/Info.plist")
build=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$app_source/Contents/Info.plist")
bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app_source/Contents/Info.plist")
commit=$(git -C "$root" rev-parse HEAD)
short_commit=${commit:0:12}
archive_name="AmneziaWG-unsigned-macos-${version}-${short_commit}.zip"
stage=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-package.XXXXXX")
package_root="$stage/AmneziaWG-unsigned-macos-${version}-${short_commit}"
cleanup() {
    rm -rf "$working" "$stage"
}
trap cleanup EXIT

mkdir -p "$package_root/Daemon"
ditto "$app_source" "$package_root/AmneziaWG.app"
# This is an ad-hoc signature only. It establishes no developer identity and
# does not avoid Gatekeeper approval on another Mac.
codesign --force --deep --sign - --timestamp=none "$package_root/AmneziaWG.app"
codesign --verify --deep --strict "$package_root/AmneziaWG.app"
install -m 0755 "$working/amneziawg-daemon-control-poc" "$package_root/Daemon/amneziawg-daemon-control-poc"
install -m 0755 "$working/amneziawg-go-daemon-control-poc" "$package_root/Daemon/amneziawg-go-daemon-control-poc"
install -m 0755 "$root/scripts/install-daemon-control-poc.sh" "$package_root/Daemon/install-daemon-control-poc.sh"

cat > "$package_root/INSTALL.md" <<EOF_INSTALL
# Experimental unsigned AmneziaWG package

This package contains an ad-hoc-signed GUI and the matching root-controlled
daemon-control POC from commit \`$commit\`. It is not a signed NetworkExtension
client and is not a general release.

1. Verify the archive source and inspect \`MANIFEST.json\` before opening it.
2. Move \`AmneziaWG.app\` to Applications. macOS will require the usual
   Gatekeeper approval for an ad-hoc signature: Control-click the app, choose
   Open, then approve it in Privacy & Security if requested.
3. In Terminal from this extracted directory, install the matching daemon:

   \`sudo ./Daemon/install-daemon-control-poc.sh install --daemon "\$PWD/Daemon/amneziawg-daemon-control-poc" --amneziawg-go "\$PWD/Daemon/amneziawg-go-daemon-control-poc" --uid "\$(id -u)" --allow-route-plan-runtime\`

The installer is intentionally root-owned, explicit, and update-free. It
refuses replacement while sessions exist. To roll it back after stopping every
profile, run:

\`sudo ./Daemon/install-daemon-control-poc.sh uninstall --uid "\$(id -u)"\`

The current experimental GUI accepts only constrained IPv4 split profiles. It
does not support full/default routes, IPv6, DNS, ExcludeIPs, hostnames,
on-demand, or automatic updates in this CI artifact. Never put VPN
configurations or keys in issue reports, CI logs, or this package directory.
EOF_INSTALL

sparkle_framework="$package_root/AmneziaWG.app/Contents/Frameworks/Sparkle.framework"
while IFS= read -r -d '' link; do
    case "$link" in
        "$sparkle_framework"/*) ;;
        *) fail "package payload contains an unexpected symbolic link: $link" ;;
    esac
done < <(find "$package_root" -type l -print0)

app_sha256=$(/usr/bin/shasum -a 256 "$package_root/AmneziaWG.app/Contents/MacOS/AmneziaWG" | /usr/bin/awk '{print $1}')
daemon_sha256=$(/usr/bin/shasum -a 256 "$package_root/Daemon/amneziawg-daemon-control-poc" | /usr/bin/awk '{print $1}')
backend_sha256=$(/usr/bin/shasum -a 256 "$package_root/Daemon/amneziawg-go-daemon-control-poc" | /usr/bin/awk '{print $1}')
installer_sha256=$(/usr/bin/shasum -a 256 "$package_root/Daemon/install-daemon-control-poc.sh" | /usr/bin/awk '{print $1}')
PACKAGE_ROOT="$package_root" APP_BUNDLE_ID="$bundle_id" APP_VERSION="$version" APP_BUILD="$build" COMMIT="$commit" PROTOCOL_VERSION="$protocol_from_go" APP_SHA256="$app_sha256" DAEMON_SHA256="$daemon_sha256" BACKEND_SHA256="$backend_sha256" INSTALLER_SHA256="$installer_sha256" \
    python3 - <<'PY' > "$package_root/MANIFEST.json"
import hashlib
import json
import os
from pathlib import Path

root = Path(os.environ["PACKAGE_ROOT"])
sparkle_framework = root / "AmneziaWG.app/Contents/Frameworks/Sparkle.framework"
sparkle_root = sparkle_framework.resolve(strict=True)
payload_sha256 = {}
for path in sorted(root.rglob("*")):
    if path.is_symlink():
        try:
            path.resolve(strict=True).relative_to(sparkle_root)
        except (FileNotFoundError, ValueError):
            raise SystemExit(f"package payload contains an unexpected symbolic link: {path.relative_to(root)}")
        continue
    if path.is_file() and path.relative_to(root) != Path("MANIFEST.json"):
        digest = hashlib.sha256()
        with path.open("rb") as artifact:
            for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
                digest.update(chunk)
        payload_sha256[str(path.relative_to(root))] = digest.hexdigest()

print(json.dumps({
    "app_bundle_id": os.environ["APP_BUNDLE_ID"],
    "app_version": os.environ["APP_VERSION"],
    "app_build": os.environ["APP_BUILD"],
    "commit": os.environ["COMMIT"],
    "daemon_protocol_version": int(os.environ["PROTOCOL_VERSION"]),
    "sha256": {
        "app_executable": os.environ["APP_SHA256"],
        "daemon_control": os.environ["DAEMON_SHA256"],
        "amneziawg_go": os.environ["BACKEND_SHA256"],
        "installer": os.environ["INSTALLER_SHA256"],
    },
    "payload_sha256": payload_sha256,
    "kind": "experimental-unsigned-daemon-gui",
}, indent=2, sort_keys=True))
PY

ditto -c -k --sequesterRsrc --keepParent "$package_root" "$output/$archive_name"
if [[ -n $sparkle_update_output ]]; then
    update_archive="AmneziaWG-${version}-${build}.app.zip"
    ditto -c -k --sequesterRsrc --keepParent "$package_root/AmneziaWG.app" "$sparkle_update_output/$update_archive"
    printf '%s\n' "$sparkle_update_output/$update_archive"
fi
printf '%s\n' "$output/$archive_name"
