---
type: guide
status: draft
---

# Daemon diagnostics

The experimental root daemon has opt-in JSONL diagnostics for a local runtime
test. They are disabled by default. The events never include UAPI input,
profile configuration, private keys, preshared keys, public peer keys, or raw
backend errors.

Install a reviewed daemon build with `--diagnostics` to record lifecycle and
route ownership events. Add `--diagnostics-endpoints` only when endpoint IP
addresses are needed for the investigation:

```bash
sudo scripts/install-daemon-control-poc.sh install \
  --daemon /absolute/path/to/daemon-control-poc \
  --amneziawg-go /absolute/path/to/amneziawg-go \
  --uid "$(id -u)" \
  --allow-route-plan-runtime \
  --diagnostics
```

The installed daemon writes only structured events to:

```text
/private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl
```

The file is root-owned and mode `0600`. It rotates at 512 KiB and retains one
previous file as `diagnostics.jsonl.1`.

Collect the latest events after reproducing the issue:

```bash
sudo tail -n 200 /private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl
sudo tail -n 200 /private/var/db/amneziawg-daemon-control-poc/diagnostics.jsonl.1
```

Useful fields are `operation_id`, `session`, `stage`, `class`, `code`,
`route_owner`, `interface`, `prefix`, `result`, and `pending`. `session` is a
profile UUID or a daemon-created `utun` name. Endpoint IPs are omitted unless
`--diagnostics-endpoints` was explicitly selected.

Do not paste a whole profile, daemon stdout/stderr, or unrelated system logs
into an issue. Share only the relevant JSONL event lines after checking them.
