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

test_dir=$(mktemp -d /private/tmp/amneziawg-utun-poc.test.XXXXXX)
test_pid=
trap 'kill "$test_pid" 2>/dev/null || true; rm -f "$test_dir/binary" "$test_dir/pid.1" "$test_dir/start.1" "$test_dir/name.1" "$test_dir/name.2" "$test_dir/name.3"; rmdir "$test_dir" 2>/dev/null || true' EXIT
UTUN_POC_LIB=1 source "$script"
/usr/bin/yes -f utun >/dev/null &
test_pid=$!
printf '%s\n' /usr/bin/yes > "$test_dir/binary"
printf '%s\n' "$test_pid" > "$test_dir/pid.1"
ps -p "$test_pid" -o lstart= > "$test_dir/start.1"
process_is_owned "$test_dir" 1
printf 'utun10\n' > "$test_dir/name.1"
printf 'utun11\n' > "$test_dir/name.2"
printf 'utun12\n' > "$test_dir/name.3"
names_are_ready "$test_dir"
kill "$test_pid"
wait "$test_pid" 2>/dev/null || true
! process_is_owned "$test_dir" 1
