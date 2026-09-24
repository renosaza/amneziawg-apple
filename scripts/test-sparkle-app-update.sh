#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 /path/to/AmneziaWG-<version>-<build>.app.zip" >&2
    exit 64
fi

archive=$1
[[ -f "$archive" ]] || { echo "Sparkle update archive is missing" >&2; exit 1; }

stage=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-sparkle-update-check.XXXXXX")
trap 'rm -rf "$stage"' EXIT
ditto -x -k "$archive" "$stage"

app="$stage/AmneziaWG.app"
[[ -d "$app" && -x "$app/Contents/MacOS/AmneziaWG" ]] || {
    echo "Sparkle update archive must contain exactly AmneziaWG.app" >&2
    exit 1
}
[[ $(find "$stage" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ') == 1 ]] || {
    echo "Sparkle update archive has unexpected roots" >&2
    exit 1
}
codesign --verify --deep --strict "$app"

if find "$app" \( -name '*.conf' -o -name 'Developer.xcconfig' -o -name 'profiles.json' \) -print -quit | grep -q .; then
    echo "Sparkle update archive contains a configuration artifact" >&2
    exit 1
fi

python3 - "$app/Contents/Info.plist" <<'PY'
import base64
import plistlib
import sys

info = plistlib.load(open(sys.argv[1], "rb"))
assert info["CFBundleIdentifier"] == "com.renosaza.amneziawg.daemon-gui"
assert str(info["CFBundleVersion"]).isdigit()
assert info["SUFeedURL"].startswith("https://")
assert len(base64.b64decode(info["SUPublicEDKey"], validate=True)) == 32
assert info["SUEnableAutomaticChecks"] is True
assert info["SURequireSignedFeed"] is True
assert info["SUVerifyUpdateBeforeExtraction"] is True
PY

echo "Sparkle app update archive self-check passed"
