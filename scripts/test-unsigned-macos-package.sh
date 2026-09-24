#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 /path/to/AmneziaWG-unsigned-macos.zip" >&2
    exit 64
fi

archive=$1
[[ -f "$archive" ]] || { echo "package archive is missing" >&2; exit 1; }

stage=$(mktemp -d "${TMPDIR:-/tmp}/amneziawg-package-check.XXXXXX")
trap 'rm -rf "$stage"' EXIT
ditto -x -k "$archive" "$stage"

package=$(find "$stage" -mindepth 1 -maxdepth 1 -type d -name 'AmneziaWG-unsigned-macos-*' -print -quit)
[[ -n $package ]] || { echo "package root is missing" >&2; exit 1; }
[[ $(find "$stage" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ') == 1 ]] || { echo "package has unexpected roots" >&2; exit 1; }

app="$package/AmneziaWG.app"
daemon="$package/Daemon/amneziawg-daemon-control-poc"
backend="$package/Daemon/amneziawg-go-daemon-control-poc"
installer="$package/Daemon/install-daemon-control-poc.sh"
manifest="$package/MANIFEST.json"
[[ -x "$app/Contents/MacOS/AmneziaWG" && -x "$daemon" && -x "$backend" && -x "$installer" && -f "$manifest" && -f "$package/INSTALL.md" ]] || {
    echo "package contents are incomplete" >&2
    exit 1
}
codesign --verify --deep --strict "$app"
if find "$package" \( -name '*.conf' -o -name 'Developer.xcconfig' -o -name 'profiles.json' \) -print -quit | grep -q .; then
    echo "package contains a configuration artifact" >&2
    exit 1
fi

python3 - "$manifest" "$app/Contents/Info.plist" "$app/Contents/MacOS/AmneziaWG" "$daemon" "$backend" "$installer" <<'PY'
import hashlib
import json
import plistlib
import re
import sys
from pathlib import Path

manifest = json.load(open(sys.argv[1], encoding="utf-8"))
info = plistlib.load(open(sys.argv[2], "rb"))
assert manifest["kind"] == "experimental-unsigned-daemon-gui"
assert manifest["app_bundle_id"] == "com.renosaza.amneziawg.daemon-gui"
assert manifest["app_bundle_id"] == info["CFBundleIdentifier"]
assert manifest["app_version"] == info["CFBundleShortVersionString"]
assert manifest["app_build"] == str(info["CFBundleVersion"])
assert re.fullmatch(r"[0-9a-f]{40}", manifest["commit"])
assert manifest["daemon_protocol_version"] == 1
for key, path in zip(("app_executable", "daemon_control", "amneziawg_go", "installer"), sys.argv[3:]):
    with open(path, "rb") as artifact:
        digest = hashlib.sha256()
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
        assert manifest["sha256"][key] == digest.hexdigest()

root = Path(sys.argv[1]).parent
payload_sha256 = {}
for path in sorted(root.rglob("*")):
    assert not path.is_symlink(), f"package contains a symbolic link: {path.relative_to(root)}"
    assert path.is_dir() or path.is_file(), f"package contains an unexpected path: {path.relative_to(root)}"
    if path.is_file() and path.name != "MANIFEST.json":
        digest = hashlib.sha256()
        with path.open("rb") as artifact:
            for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
                digest.update(chunk)
        payload_sha256[str(path.relative_to(root))] = digest.hexdigest()
assert manifest["payload_sha256"] == payload_sha256
PY

echo "unsigned macOS package self-check passed"
