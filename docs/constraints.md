# Constraints

- Keep the fork as an isolated patch series over `amnezia-vpn/amneziawg-apple`; replay it with rebase and inspect it with `git range-diff`.
- Use native NetworkExtension routing. Do not add a routing daemon or persistent shell `route` workaround.
- Do not alter the AmneziaWG protocol or `amneziawg-go` for macOS UI multi-tunnel support.
- Keep macOS multi-tunnel changes separate from iOS single-tunnel behavior.
- A full route may coexist with a more-specific split route. Reject duplicate or ambiguous ownership within the same IP family before activation.
- Keep corporate endpoint and local-LAN routes outside a full tunnel through NetworkExtension excluded routes.
- Do not commit VPN profiles, private or preshared keys, header-protection keys, signing identities, provisioning profiles, or Apple credentials.
- Do not force-push shared branches. The update script creates a local `update/*` branch and never pushes.

The target route ownership is documented in the [architecture record](architecture/README.md).
