#!/bin/bash
# SPDX-License-Identifier: MIT

set -euo pipefail

workflow="$(cd "$(dirname "$0")/.." && pwd)/.github/workflows/unsigned-macos-release.yml"
[[ -f "$workflow" ]] || { echo "unsigned macOS release workflow is missing" >&2; exit 1; }

grep -Fq 'workflow_dispatch:' "$workflow"
! grep -Fq 'push:' "$workflow"
grep -Fq 'github.ref == format' "$workflow"
grep -Fq 'SPARKLE_ED25519_PRIVATE_KEY: ${{ secrets.SPARKLE_ED25519_PRIVATE_KEY }}' "$workflow"
grep -Fq -- '--ed-key-file -' "$workflow"
grep -Fq 'Sparkle tool checksum mismatch' "$workflow"
grep -Fq 'SPARKLE_UPDATE_OUTPUT' "$workflow"
! grep -Fq 'gh release' "$workflow"
! grep -Fq 'deploy-pages' "$workflow"
! grep -Fq 'upload-pages-artifact' "$workflow"
! grep -Fq 'contents: write' "$workflow"
! grep -Fq -- '--allow-route-plan-runtime' "$workflow"
! grep -Fq 'install-daemon-control-poc.sh install' "$workflow"

echo "unsigned macOS release workflow self-check passed"
