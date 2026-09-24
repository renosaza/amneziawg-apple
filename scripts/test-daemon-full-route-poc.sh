#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
workflow="$script_dir/../.github/workflows/macos-daemon-full-route-poc.yml"

grep -Fxq '  workflow_dispatch:' "$workflow"
grep -Fq 'timeout-minutes: 10' "$workflow"
grep -Fq 'trap cleanup EXIT' "$workflow"
grep -Fq 'sleep 75' "$workflow"
grep -Fq -- '--allow-full-route-runtime' "$workflow"
grep -Fq -- '-manual-full-route-plan-start' "$workflow"
grep -Fq -- '-manual-route-plan-start-three' "$workflow"
grep -Fq -- '-manual-route-plan-assert-full-three' "$workflow"
grep -Fq -- '-manual-route-plan-assert-three' "$workflow"
grep -Fq 'cleanup left TEST-NET endpoint host route' "$workflow"
grep -Fq '[[ $first_interface != "$full_interface" ]]' "$workflow"
grep -Fq '[[ $second_interface != "$full_interface" ]]' "$workflow"
