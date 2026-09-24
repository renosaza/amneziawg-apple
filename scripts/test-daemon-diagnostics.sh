#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

root_dir=$(cd "$(dirname "$0")/.." && pwd)
client="$root_dir/Sources/WireGuardApp/Tunnel/DaemonControlClient.swift"
manager="$root_dir/Sources/WireGuardApp/Tunnel/DaemonTunnelsManager.swift"

grep -Fq 'diagnosticHandler' "$client"
grep -Fq 'recordError(operation:' "$client"
grep -Fq 'DaemonDiagnosticLogging' "$manager"
grep -Fq 'Daemon diagnostic:' "$manager"
grep -Fq 'maximumLogBytes' "$manager"
grep -Fq '.posixPermissions: 0o600' "$manager"
grep -Fq 'recordRoutePlan' "$manager"
grep -Fq 'recordValidation' "$manager"
grep -Fq 'recordStage' "$manager"
grep -Fq 'newOperationID' "$manager"
grep -Fq 'DaemonDiagnostics.recordRoutePlan(builtPlan)' "$manager"

# Diagnostics must never accept or render the request data that carries key material.
diagnostics=$(awk '/private enum DaemonDiagnostics \{/,/^final class DaemonTunnelsManager/' "$manager")
if grep -Eq 'uapiConfiguration|PrivateKey|PreSharedKey|HeaderProtectionKey|route\.destination|localAddress|raw error' <<<"$diagnostics"; then
    echo 'daemon diagnostics include sensitive configuration data' >&2
    exit 1
fi

echo 'daemon diagnostics privacy boundary passed'
