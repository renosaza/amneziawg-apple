---
id: 0007-free-macos-ipv4-route-poc
type: decision
status: accepted
scope: experimental-macos-cli
date: 2026-09-23
constraints:
  - C-DAEMON-IPV4-ROUTE-EXPERIMENT
---

# Native synthetic IPv4 route experiment

Use Darwin `SIOCAIFADDR`/`SIOCDIFADDR` and PF_ROUTE to assign one TEST-NET IPv4
point-to-point address pair and one exact TEST-NET `/32` route to an owned
synthetic daemon `utun`. Verify the route's interface index before returning
and remove only that route/address during cleanup. The manual workflow remains
dispatch-only; it does not configure physical routes, IPv6, DNS, or traffic.

Before assignment, reject an IPv4 local address already present on any
interface. Before `SIOCDIFADDR`, require the current address and peer to still
belong to the recorded `utun`; an unavailable or conflicting ioctl view leaves
the address intact and reports the error. If this POC raised `IFF_UP`, restore
only that flag in a separate cleanup phase even when address cleanup fails, so
the remaining address ownership can be retried safely.
