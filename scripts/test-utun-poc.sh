#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
script="$script_dir/utun-poc.sh"

bash -n "$script"
output=$("$script" dry-run /usr/bin/true)
[[ $(grep -c ' -f utun' <<<"$output") == 3 ]]
grep -q 'No process, utun, route, DNS, PF, or profile will be created.' <<<"$output"
if "$script" dry-run /does/not/exist >/dev/null 2>&1; then
    echo 'missing binary unexpectedly succeeded' >&2
    exit 1
fi
