#!/bin/bash
# SPDX-License-Identifier: MIT
#
# Read-only diagnostics for a locally managed macOS daemon-profile smoke test.

set -euo pipefail

readonly launchd_label=com.amneziawg.daemon-control-poc
readonly installed_daemon=/usr/local/libexec/amneziawg-daemon-control-poc
readonly installed_backend=/usr/local/libexec/amneziawg-go-daemon-control-poc
readonly diagnostic_log="$HOME/Library/Logs/AmneziaWGDaemon/daemon-diagnostics.log"

usage() {
    cat >&2 <<'EOF_USAGE'
Usage:
  scripts/real-profile-diagnostics.sh --route <IPv4> [<IPv4> ...]
  scripts/real-profile-diagnostics.sh --utuns
  scripts/real-profile-diagnostics.sh --daemon-status
  scripts/real-profile-diagnostics.sh --app-diagnostics [max-lines]
  scripts/real-profile-diagnostics.sh --installed-artifacts
  scripts/real-profile-diagnostics.sh --self-check

The helper reads selected route fields, utun names, LaunchDaemon state, or opt-in app diagnostics.
It never reads VPN profiles, Keychain items, UAPI sockets, or configuration text.
EOF_USAGE
    exit 64
}

is_literal_ipv4() {
    local address=$1 part
    local -a parts
    [[ $address != *$'\n'* && $address != *$'\r'* ]] || return 1
    IFS=. read -r -a parts <<< "$address"
    [[ ${#parts[@]} == 4 ]] || return 1
    for part in "${parts[@]}"; do
        [[ $part =~ ^(0|[1-9][0-9]{0,2})$ ]] || return 1
        (( 10#$part <= 255 )) || return 1
    done
}

self_check() {
    is_literal_ipv4 0.0.0.0
    is_literal_ipv4 192.0.2.1
    ! is_literal_ipv4 01.2.3.4
    ! is_literal_ipv4 256.0.0.1
    ! is_literal_ipv4 192.0.2.1/24
    ! is_literal_ipv4 '192.0.2.1;id'
    printf 'real-profile diagnostics self-check passed\n'
}

show_route() {
    local address=$1
    printf 'route %s\n' "$address"
    /sbin/route -n get "$address" | /usr/bin/awk '
        /^[[:space:]]*(destination|gateway|interface):/ {
            sub(/^[[:space:]]+/, "")
            print
        }
    '
}

show_utuns() {
    /sbin/ifconfig -l | /usr/bin/tr ' ' '\n' | /usr/bin/awk '/^utun[0-9]+$/'
}

show_daemon_status() {
    # Only job state and PID are printed; launchctl's complete output is not.
    /usr/bin/sudo /bin/launchctl print "system/$launchd_label" | /usr/bin/awk '
        /^[[:space:]]*(state|pid) =/ {
            sub(/^[[:space:]]+/, "")
            print
        }
    '
}

show_app_diagnostics() {
    local max_lines=${1:-200}
    [[ $max_lines =~ ^([1-9]|[1-9][0-9]|1[0-9][0-9]|200)$ ]] || usage
    [[ -f $diagnostic_log && ! -L $diagnostic_log ]] || return 0
    # The file has only records produced by DaemonDiagnostics. The fixed line limit bounds output.
    /usr/bin/tail -n "$max_lines" "$diagnostic_log" | /usr/bin/awk '/Daemon diagnostic:/'
}

show_installed_artifacts() {
    local artifact
    for artifact in "$installed_daemon" "$installed_backend"; do
        if [[ -f $artifact && ! -L $artifact ]]; then
            /usr/bin/basename "$artifact"
            /usr/bin/shasum -a 256 "$artifact" | /usr/bin/awk '{print "sha256=" $1}'
        else
            printf '%s\n' "$(/usr/bin/basename "$artifact") absent"
        fi
    done
}

case ${1:-} in
    --self-check)
        [[ $# == 1 ]] || usage
        self_check
        ;;
    --route)
        shift
        (( $# >= 1 && $# <= 6 )) || usage
        for address in "$@"; do
            is_literal_ipv4 "$address" || { printf 'invalid literal IPv4 address: %s\n' "$address" >&2; exit 64; }
            show_route "$address"
        done
        ;;
    --utuns)
        [[ $# == 1 ]] || usage
        show_utuns
        ;;
    --daemon-status)
        [[ $# == 1 ]] || usage
        show_daemon_status
        ;;
    --app-diagnostics)
        shift
        (( $# <= 1 )) || usage
        show_app_diagnostics "${1:-15}"
        ;;
    --installed-artifacts)
        [[ $# == 1 ]] || usage
        show_installed_artifacts
        ;;
    *)
        usage
        ;;
esac
