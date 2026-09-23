#!/bin/bash
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
go run "$script_dir/utun-poc-traffic.go" -self-check
