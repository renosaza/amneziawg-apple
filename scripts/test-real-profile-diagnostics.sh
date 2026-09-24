#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
"$script_dir/real-profile-diagnostics.sh" --self-check

if "$script_dir/real-profile-diagnostics.sh" --route '192.0.2.1;id' >/dev/null 2>&1; then
    echo 'diagnostic helper accepted an unsafe route argument' >&2
    exit 1
fi

if "$script_dir/real-profile-diagnostics.sh" --app-diagnostics '1;id' >/dev/null 2>&1; then
    echo 'diagnostic helper accepted an unsafe diagnostics duration' >&2
    exit 1
fi

"$script_dir/real-profile-diagnostics.sh" --installed-artifacts >/dev/null
