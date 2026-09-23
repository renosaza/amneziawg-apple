#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

source_branch="${1:-$(git branch --show-current)}"
if [[ -z "$source_branch" ]] || ! git show-ref --verify --quiet "refs/heads/$source_branch"; then
    printf 'Pass an existing local patch-series branch.\n' >&2
    exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
    printf 'Refusing to update a dirty working tree. Commit or stash work first.\n' >&2
    exit 2
fi

git fetch amnezia --tags
git fetch wireguard --tags pull/63/head

new_base="$(git rev-parse amnezia/master)"
old_base="$(git merge-base "$source_branch" "$new_base")"
if [[ "$old_base" == "$new_base" ]]; then
    printf 'No newer Amnezia base for %s.\n' "$source_branch"
    exit 0
fi

update_branch="update/$(date -u +%Y%m%d)-${new_base:0:12}"
git switch -c "$update_branch" "$source_branch"

printf 'Old base: %s\nNew base: %s\nUpdate branch: %s\n' "$old_base" "$new_base" "$update_branch"
printf 'Rebasing patch series; resolve conflicts semantically, then run git rebase --continue.\n'
git rebase --onto "$new_base" "$old_base"

printf '\nPatch status:\n'
git log --format='  %h %s' "$new_base..HEAD"

printf '\nBuild:\n'
xcodebuild -list -project WireGuard.xcodeproj
xcodebuild -project WireGuard.xcodeproj -target WireGuardmacOS -configuration Debug -sdk macosx CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO build

printf '\nTests:\n'
make -C Sources/WireGuardKitGo
swift test

printf '\nRange diff:\n'
git range-diff "$old_base..$source_branch" "$new_base..HEAD"
