#!/bin/bash
# SPDX-License-Identifier: MIT
#
# Starts three empty, foreground amneziawg-go utun devices for a macOS-only POC.
# It deliberately does not configure peers, addresses, routes, DNS, or PF.

set -euo pipefail

readonly UAPI_DIR=/var/run/amneziawg
readonly RUN_PREFIX=amneziawg-utun-poc.

usage() {
    cat <<'EOF'
Usage:
  scripts/utun-poc.sh dry-run <amneziawg-go>
  sudo scripts/utun-poc.sh run <amneziawg-go>
  sudo scripts/utun-poc.sh status <run-directory>
  sudo scripts/utun-poc.sh stop <run-directory>

run starts three empty foreground devices and waits until interrupted.
EOF
}

fail() {
    echo "utun-poc: $*" >&2
    exit 1
}

require_macos() {
    [[ $(uname -s) == Darwin ]] || fail "macOS is required"
}

require_root() {
    [[ $(id -u) == 0 ]] || fail "run, status, and stop require sudo"
}

require_binary() {
    [[ $# == 1 && -x $1 && -f $1 ]] || fail "amneziawg-go must be an executable file"
}

safe_run_directory() {
    [[ $# == 1 && -d $1 ]] || fail "run directory does not exist"
    [[ $1 == /private/tmp/$RUN_PREFIX* && ! -L $1 ]] || fail "refusing an unexpected run directory"
    [[ ${1##*/} == "$RUN_PREFIX"* ]] || fail "refusing an unexpected run directory"
    [[ $(stat -f %u "$1") == 0 ]] || fail "run directory must be root-owned"
    [[ $(stat -f %Lp "$1") == 700 ]] || fail "run directory permissions must be 700"
}

pid_from_file() {
    local file=$1 pid
    [[ -f $file ]] || return 1
    IFS= read -r pid < "$file" || return 1
    [[ $pid =~ ^[1-9][0-9]*$ ]] || return 1
    printf '%s\n' "$pid"
}

stop_devices() {
    local dir=$1 n pid command name i remaining=0
    for n in 1 2 3; do
        pid=$(pid_from_file "$dir/pid.$n" || true)
        [[ -n $pid ]] || continue
        command=$(ps -p "$pid" -o command= 2>/dev/null || true)
        if [[ $command == *" -f utun"* ]]; then
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    for ((i = 0; i < 30; i++)); do
        remaining=0
        for n in 1 2 3; do
            pid=$(pid_from_file "$dir/pid.$n" || true)
            [[ -n $pid ]] && kill -0 "$pid" 2>/dev/null && remaining=1
        done
        [[ $remaining == 0 ]] && break
        sleep 0.1
    done
    [[ $remaining == 0 ]] || fail "a POC process did not stop; preserving $dir"
    for n in 1 2 3; do
        name=$(tr -d '\r\n' < "$dir/name.$n" 2>/dev/null || true)
        [[ $name =~ ^utun[0-9]+$ ]] && rm -f "$UAPI_DIR/$name.sock"
        rm -f "$dir/pid.$n" "$dir/name.$n" "$dir/log.$n"
    done
    rmdir "$dir" 2>/dev/null || true
}

check_no_utuns() {
    local interfaces
    interfaces=$(/sbin/ifconfig -l)
    [[ $interfaces != *utun* ]] || fail "existing utun interface found; use an idle Mac"
}

launch_device() {
    local binary=$1 dir=$2 n=$3
    WG_TUN_NAME_FILE="$dir/name.$n" LOG_LEVEL=error "$binary" -f utun >"$dir/log.$n" 2>&1 &
    printf '%s\n' "$!" > "$dir/pid.$n"
}

wait_for_devices() {
    local dir=$1 n i name previous
    for ((i = 0; i < 100; i++)); do
        for n in 1 2 3; do
            [[ -s $dir/name.$n ]] || break
        done
        [[ $n == 3 && -s $dir/name.3 ]] && break
        sleep 0.05
    done
    for n in 1 2 3; do
        kill -0 "$(pid_from_file "$dir/pid.$n")" 2>/dev/null || fail "device $n exited; inspect $dir/log.$n"
        IFS= read -r name < "$dir/name.$n" || fail "device $n did not report a utun name"
        [[ $name =~ ^utun[0-9]+$ ]] || fail "device $n reported invalid interface name"
        [[ -S $UAPI_DIR/$name.sock ]] || fail "device $n did not create its UAPI socket"
        for previous in 1 2; do
            [[ $previous -lt $n ]] || break
            [[ $name != $(tr -d '\r\n' < "$dir/name.$previous") ]] || fail "two processes claimed $name"
        done
    done
}

show_status() {
    local dir=$1 n pid name
    for n in 1 2 3; do
        pid=$(pid_from_file "$dir/pid.$n") || fail "missing PID for device $n"
        IFS= read -r name < "$dir/name.$n" || fail "missing interface name for device $n"
        kill -0 "$pid" 2>/dev/null || fail "device $n is not running"
        [[ -S $UAPI_DIR/$name.sock ]] || fail "device $n UAPI socket is missing"
        printf 'device %s: pid=%s utun=%s uapi=%s/%s.sock\n' "$n" "$pid" "$name" "$UAPI_DIR" "$name"
    done
}

case ${1:-} in
    dry-run)
        require_macos
        require_binary "${2:-}"
        echo 'No process, utun, route, DNS, PF, or profile will be created.'
        for n in 1 2 3; do
            printf 'WG_TUN_NAME_FILE=<run-dir>/name.%s LOG_LEVEL=error %q -f utun\n' "$n" "$2"
        done
        ;;
    run)
        require_macos
        require_root
        require_binary "${2:-}"
        check_no_utuns
        run_dir=$(mktemp -d "/private/tmp/${RUN_PREFIX}XXXXXX")
        chmod 700 "$run_dir"
        trap 'stop_devices "$run_dir" || true' EXIT INT TERM
        for n in 1 2 3; do launch_device "$2" "$run_dir" "$n"; done
        wait_for_devices "$run_dir"
        echo "run directory: $run_dir"
        show_status "$run_dir"
        echo 'No addresses, peers, routes, DNS, or PF rules were configured. Press Ctrl-C to clean up.'
        while :; do
            for n in 1 2 3; do kill -0 "$(pid_from_file "$run_dir/pid.$n")" 2>/dev/null || fail "device $n exited"; done
            sleep 1
        done
        ;;
    status)
        require_macos
        require_root
        safe_run_directory "${2:-}"
        show_status "$2"
        ;;
    stop)
        require_macos
        require_root
        safe_run_directory "${2:-}"
        stop_devices "$2"
        ;;
    *)
        usage >&2
        exit 2
        ;;
esac
