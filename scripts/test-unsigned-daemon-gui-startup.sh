#!/bin/bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 /path/to/AmneziaWG\ Daemon.app" >&2
    exit 64
fi

app=$1
executable="$app/Contents/MacOS/AmneziaWG Daemon"
[[ -d "$app" && -x "$executable" ]] || { echo "unsigned daemon app is missing" >&2; exit 1; }

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT

# This runs the application executable, so @NSApplicationMain and AppDelegate's
# DAEMON_MODE startup path execute without relying on a WindowServer session.
HOME="$home" "$executable" --daemon-self-check

[[ ! -e "$home/Library/Application Support/com.amneziawg.daemon-profile-store/profiles.json" ]] || {
    echo "daemon self-check unexpectedly created profile metadata" >&2
    exit 1
}

echo "unsigned daemon GUI startup self-check passed"
