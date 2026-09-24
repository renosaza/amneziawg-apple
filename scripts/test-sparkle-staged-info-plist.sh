#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

stage=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-sparkle-plist.XXXXXX")
trap 'rm -rf "$stage"' EXIT
plist="$stage/Info.plist"
python3 - "$plist" <<'PY'
import plistlib
import sys

with open(sys.argv[1], "wb") as destination:
    plistlib.dump({"CFBundleIdentifier": "example"}, destination)
PY
SPARKLE_FEED_URL=https://example.invalid/appcast.xml \
SPARKLE_PUBLIC_ED_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
    python3 "$(cd "$(dirname "$0")" && pwd)/stage-sparkle-info-plist.py" "$plist"
python3 - "$plist" <<'PY'
import base64
import plistlib
import sys

info = plistlib.load(open(sys.argv[1], "rb"))
assert info["SUFeedURL"] == "https://example.invalid/appcast.xml"
assert info["SUPublicEDKey"] == "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
assert len(base64.b64decode(info["SUPublicEDKey"], validate=True)) == 32
assert info["SUEnableAutomaticChecks"] is True
assert info["SURequireSignedFeed"] is True
assert info["SUVerifyUpdateBeforeExtraction"] is True
PY
echo "Sparkle staged Info.plist self-check passed"
