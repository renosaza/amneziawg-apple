#!/bin/bash
# SPDX-License-Identifier: MIT
#
# Installs only the synthetic daemon-control POC. Run manually with sudo.

set -euo pipefail

readonly label=com.amneziawg.daemon-control-poc
readonly libexec_dir=/usr/local/libexec
readonly daemon_path="$libexec_dir/amneziawg-daemon-control-poc"
readonly backend_path="$libexec_dir/amneziawg-go-daemon-control-poc"
readonly socket_dir=/private/var/run/amneziawg-daemon-control-poc
readonly socket_path="$socket_dir/control.sock"
readonly plist_path="/Library/LaunchDaemons/$label.plist"
stage_temp=
plist_temp=

fail() {
    printf 'daemon-control installer: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat >&2 <<'EOF_USAGE'
Usage:
  sudo scripts/install-daemon-control-poc.sh install --daemon /absolute/daemon-control-poc --amneziawg-go /absolute/amneziawg-go --uid <console-uid>
  sudo scripts/install-daemon-control-poc.sh status  --uid <console-uid>
  sudo scripts/install-daemon-control-poc.sh uninstall --uid <console-uid>

This is a synthetic POC installer. It never accepts VPN profiles or keys.
EOF_USAGE
    exit 2
}

require_macos_sudo() {
    [[ $(/usr/bin/uname -s) == Darwin ]] || fail 'macOS is required'
    [[ ${EUID:-$(/usr/bin/id -u)} == 0 ]] || fail 'run this command with sudo'
    [[ ${SUDO_UID:-} =~ ^[1-9][0-9]*$ ]] || fail 'run this command through sudo from a non-root administrator'
}

parse_uid() {
    [[ ${1:-} =~ ^[1-9][0-9]*$ ]] || fail 'UID must be a non-root decimal integer'
    allowed_uid=$1
    local console_uid
    console_uid=$(/usr/bin/stat -f %u /dev/console) || fail 'cannot read console UID'
    if [[ $console_uid == 0 ]]; then
        console_uid=$SUDO_UID # Headless disposable runners have no logged-in console user.
    fi
    [[ $allowed_uid == "$console_uid" && $allowed_uid == "$SUDO_UID" ]] || fail 'UID must match the invoking console user'
}

parse_sources() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --daemon) daemon_source=${2:-}; shift 2 ;;
            --amneziawg-go) backend_source=${2:-}; shift 2 ;;
            --uid) parse_uid "${2:-}"; shift 2 ;;
            *) usage ;;
        esac
    done
    [[ -n ${daemon_source:-} && -n ${backend_source:-} && -n ${allowed_uid:-} ]] || usage
}

parse_uid_only() {
    [[ $# == 2 && $1 == --uid ]] || usage
    parse_uid "$2"
}

mode_bits() {
    /usr/bin/stat -f %Lp "$1"
}

owner_uid() {
    /usr/bin/stat -f %u "$1"
}

group_id() {
    /usr/bin/stat -f %g "$1"
}

not_group_or_other_writable() {
    local mode=$1
    (( (8#$mode & 8#022) == 0 ))
}

safe_source_ancestor() {
    local directory=$1 owner mode
    [[ -d $directory && ! -L $directory ]] || return 1
    owner=$(owner_uid "$directory") || return 1
    mode=$(mode_bits "$directory") || return 1
    [[ $owner == 0 || $owner == "$SUDO_UID" ]] && not_group_or_other_writable "$mode"
}

safe_root_ancestor() {
    local directory=$1 owner mode
    [[ -d $directory && ! -L $directory ]] || return 1
    owner=$(owner_uid "$directory") || return 1
    mode=$(mode_bits "$directory") || return 1
    [[ $owner == 0 ]] && not_group_or_other_writable "$mode"
}

validate_root_chain() {
    local directory=$1
    while :; do
        safe_root_ancestor "$directory" || fail "destination has an unsafe ancestor: $directory"
        [[ $directory == / ]] && return
        directory=$(dirname "$directory")
    done
}

validate_destination_parents() {
    validate_root_chain /usr/local
    validate_root_chain /Library/LaunchDaemons
    validate_root_chain /private/var/run
    validate_root_chain /private/var/root
}

validate_source_binary() {
    local path=$1 directory owner mode
    [[ $path == /* && $path != *$'\n'* && $path != *$'\r'* && $path == "$(cd "$(dirname "$path")" && pwd)/$(basename "$path")" ]] || fail 'source binary path must be clean and absolute'
    [[ -f $path && ! -L $path && -x $path ]] || fail "source binary is not an executable regular file: $path"
    owner=$(owner_uid "$path") || fail "cannot inspect source binary: $path"
    mode=$(mode_bits "$path") || fail "cannot inspect source binary: $path"
    [[ $owner == 0 || $owner == "$SUDO_UID" ]] && not_group_or_other_writable "$mode" || fail "source binary has unsafe ownership or permissions: $path"
    directory=$(dirname "$path")
    while :; do
        safe_source_ancestor "$directory" || fail "source binary has an unsafe ancestor: $directory"
        [[ $directory == / ]] && break
        directory=$(dirname "$directory")
    done
}

root_artifact() {
    local path=$1 expected_mode=$2
    [[ -f $path && ! -L $path ]] || return 1
    [[ $(owner_uid "$path") == 0 && $(group_id "$path") == 0 && $(mode_bits "$path") == "$expected_mode" ]]
}

root_directory() {
    local path=$1 expected_mode=$2
    [[ -d $path && ! -L $path ]] || return 1
    [[ $(owner_uid "$path") == 0 && $(group_id "$path") == 0 && $(mode_bits "$path") == "$expected_mode" ]]
}

validate_installed() {
    root_directory "$libexec_dir" 755 || fail 'libexec directory is not root:wheel 0755'
    root_artifact "$daemon_path" 755 || fail 'daemon binary is not root:wheel 0755'
    root_artifact "$backend_path" 755 || fail 'backend binary is not root:wheel 0755'
    root_directory "$socket_dir" 755 || fail 'socket directory is not root:wheel 0755'
    root_artifact "$plist_path" 644 || fail 'launchd plist is not root:wheel 0644'
}

validate_socket() {
    [[ -S $socket_path && ! -L $socket_path ]] || fail 'daemon socket is unavailable; preserving the installation'
    [[ $(owner_uid "$socket_path") == "$allowed_uid" && $(mode_bits "$socket_path") == 600 ]] || fail 'daemon socket ownership changed; preserving the installation'
}

check_idle() {
    validate_socket
    /usr/bin/sudo -u "#$allowed_uid" -- "$daemon_path" -check-idle -socket "$socket_path" || fail 'cannot prove the daemon is idle; preserving it'
}

prepare_stop() {
    validate_socket
    /usr/bin/sudo -u "#$allowed_uid" -- "$daemon_path" -prepare-stop -socket "$socket_path" || fail 'cannot atomically quiesce an idle daemon; preserving it'
}

service_loaded() {
    /bin/launchctl print "system/$label" >/dev/null 2>&1
}

wait_for_socket_removal() {
    local attempt
    for attempt in {1..20}; do
        [[ ! -e $socket_path ]] && return 0
        /bin/sleep 0.1
    done
    fail 'daemon socket remained after stop; preserving installed files'
}

stop_idle_service() {
    if [[ -e $socket_path ]]; then
        prepare_stop
    elif service_loaded; then
        fail 'daemon is loaded without its expected socket; preserving it'
    fi
    if service_loaded; then
        if ! /bin/launchctl bootout system "$plist_path"; then
            /bin/launchctl kickstart -k "system/$label" >/dev/null 2>&1 || true
            fail 'launchd refused to stop the daemon'
        fi
        wait_for_socket_removal
    fi
}

cleanup_staging() {
    for path in "$stage_temp" "$plist_temp"; do
        [[ -n $path && -f $path && ! -L $path ]] || continue
        [[ $(owner_uid "$path") == 0 && $(group_id "$path") == 0 ]] || continue
        /bin/rm -f "$path"
    done
    stage_temp= plist_temp=
}

stage_binary() {
    local source=$1 destination=$2 before after copied
    before=$(/usr/bin/shasum -a 256 "$source" | /usr/bin/awk '{print $1}') || fail 'cannot hash source binary'
    stage_temp=$(/usr/bin/mktemp "$libexec_dir/.${label}.XXXXXX") || fail 'cannot create root-owned staging file'
    /usr/bin/install -o root -g wheel -m 0755 "$source" "$stage_temp" || fail 'cannot stage binary'
    after=$(/usr/bin/shasum -a 256 "$source" | /usr/bin/awk '{print $1}') || fail 'cannot rehash source binary'
    copied=$(/usr/bin/shasum -a 256 "$stage_temp" | /usr/bin/awk '{print $1}') || fail 'cannot hash staged binary'
    [[ $before == "$after" && $before == "$copied" ]] || fail 'source binary changed while staging'
    root_artifact "$stage_temp" 755 || fail 'staged binary failed ownership validation'
    /bin/mv -f "$stage_temp" "$destination" || fail 'cannot activate staged binary'
    stage_temp=
    root_artifact "$destination" 755 || fail 'installed binary failed ownership validation'
}

write_plist() {
    plist_temp=$(/usr/bin/mktemp "/Library/LaunchDaemons/.${label}.XXXXXX") || fail 'cannot stage launchd plist'
    cat > "$plist_temp" <<EOF_PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array>
    <string>$daemon_path</string>
    <string>-socket</string><string>$socket_path</string>
    <string>-uid</string><string>$allowed_uid</string>
    <string>-binary</string><string>$backend_path</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOF_PLIST
    /usr/sbin/chown root:wheel "$plist_temp"
    /bin/chmod 0644 "$plist_temp"
    /usr/bin/plutil -lint "$plist_temp" >/dev/null || fail 'generated plist is invalid'
    root_artifact "$plist_temp" 644 || fail 'staged plist failed ownership validation'
    /bin/mv -f "$plist_temp" "$plist_path" || fail 'cannot activate launchd plist'
    plist_temp=
    root_artifact "$plist_path" 644 || fail 'installed plist failed ownership validation'
}

prepare_directories() {
    /usr/bin/install -d -o root -g wheel -m 0755 "$libexec_dir" "$socket_dir"
    root_directory "$libexec_dir" 755 || fail 'cannot secure libexec directory'
    root_directory "$socket_dir" 755 || fail 'cannot secure socket directory'
}

rollback_fresh_install() {
    set +e
    cleanup_staging
    if service_loaded && ! /bin/launchctl bootout system "$plist_path"; then
        printf 'daemon-control installer: incomplete rollback; launchd refused to stop the daemon, preserving files\n' >&2
        return 1
    fi
    if service_loaded || [[ -e $socket_path ]]; then
        printf 'daemon-control installer: incomplete rollback; daemon state remains, preserving files\n' >&2
        return 1
    fi
    /bin/rm -f "$plist_path" "$daemon_path" "$backend_path"
    /bin/rmdir "$socket_dir" >/dev/null 2>&1 || true
}

install_fresh() {
    validate_source_binary "$daemon_source"
    validate_source_binary "$backend_source"
    validate_destination_parents
    [[ ! -e $plist_path && ! -L $plist_path && ! -e $daemon_path && ! -L $daemon_path && ! -e $backend_path && ! -L $backend_path && ! -e $socket_dir && ! -L $socket_dir ]] || fail 'installation already exists; uninstall it before a fresh install'
    trap 'rollback_fresh_install' EXIT
    prepare_directories
    stage_binary "$daemon_source" "$daemon_path"
    stage_binary "$backend_source" "$backend_path"
    write_plist
    /bin/launchctl bootstrap system "$plist_path" || fail 'launchd refused to start the daemon'
    for _ in {1..20}; do
        if [[ -S $socket_path ]]; then
            check_idle
            trap - EXIT
            cleanup_staging
            printf 'daemon-control POC installed: %s\n' "$label"
            return
        fi
        /bin/sleep 0.1
    done
    fail 'daemon did not create its control socket'
}

status() {
    validate_installed
    service_loaded || fail 'daemon is not loaded'
    check_idle
    printf 'daemon-control POC is loaded and idle: %s\n' "$label"
}

uninstall() {
    validate_installed
    stop_idle_service
    [[ ! -e $socket_path ]] || fail 'control socket remained; preserving installed files'
    /bin/rmdir "$socket_dir" || fail 'socket directory is not empty; preserving installed files'
    /bin/rm "$plist_path" "$daemon_path" "$backend_path"
    printf 'daemon-control POC uninstalled: %s\n' "$label"
}

command=${1:-}
shift || true
require_macos_sudo
case $command in
    install)
        parse_sources "$@"
        install_fresh
        ;;
    update)
        fail 'update is intentionally unsupported by this install-only POC; uninstall then install a reviewed build'
        ;;
    status)
        parse_uid_only "$@"
        status
        ;;
    uninstall)
        parse_uid_only "$@"
        uninstall
        ;;
    *) usage ;;
esac
