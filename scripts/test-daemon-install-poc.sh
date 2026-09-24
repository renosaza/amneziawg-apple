#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
installer="$script_dir/install-daemon-control-poc.sh"
workflow="$script_dir/../.github/workflows/macos-daemon-install-poc.yml"

bash -n "$installer"
grep -Fq 'readonly label=com.amneziawg.daemon-control-poc' "$installer"
grep -Fq 'readonly socket_dir=/private/var/db/amneziawg-daemon-control-poc' "$installer"
grep -Fq 'cannot atomically quiesce an idle daemon' "$installer"
grep -Fq 'safe_root_ancestor' "$installer"
grep -Fq 'incomplete rollback; launchd refused to stop' "$installer"
grep -Fq 'cleanup_staging' "$installer"
grep -Fq 'source binary changed while staging' "$installer"
grep -Fq 'launchctl bootout system' "$installer"
grep -Fq 'recover_stale_socket' "$installer"
grep -Fq 'stale socket still has a listener' "$installer"
grep -Fq '/usr/sbin/lsof -n -U "$socket_path"' "$installer"
grep -Fq 'stale socket removal was not confirmed' "$installer"
grep -Fq 'verify_absent()' "$workflow"
grep -Fq 'trap cleanup EXIT' "$workflow"
grep -Fq 'cleanup left TEST-NET split route' "$workflow"
grep -Fq -- '--allow-route-plan-runtime' "$workflow"
