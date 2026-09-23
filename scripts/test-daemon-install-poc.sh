#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
installer="$script_dir/install-daemon-control-poc.sh"

bash -n "$installer"
grep -Fq 'readonly label=com.amneziawg.daemon-control.poc' "$installer"
grep -Fq 'readonly socket_dir=/private/var/run/amneziawg-daemon-control-poc' "$installer"
grep -Fq 'daemon has active sessions' "$installer"
grep -Fq 'safe_root_ancestor' "$installer"
grep -Fq 'incomplete rollback; launchd refused to stop' "$installer"
grep -Fq 'cleanup_staging' "$installer"
grep -Fq 'source binary changed while staging' "$installer"
grep -Fq 'launchctl bootout system' "$installer"
