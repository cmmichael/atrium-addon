# Atrium

Your house in the Atrium app and on the web, from anywhere. Atrium puts Home Assistant on your tailnet and lets
Tailscale say who is calling: no passwords, no tokens, no port forwarding, nothing to type.

## Set up

1. **Install**, then **Start**.
2. **Open Web UI** and press **Sign in to Tailscale**. Once.
3. The page shows a code and an address. Open the Atrium app on your iPhone or iPad, with Tailscale on, and scan
   the code (or enter the address). The same address opens Atrium in any browser on your tailnet.

Turn on **Show in sidebar** to keep the set-up page in Home Assistant's sidebar.

## Who can use it

Whoever signed Atrium in. To let others in, add their Tailscale logins under **Who may use Atrium** in the
Configuration tab. Your tailnet's own rules decide which devices can reach Atrium at all; to take someone's access
away, remove their login here or their device from your tailnet.

Everyone acts with Atrium's own access to Home Assistant, which is an administrator's. To make someone's actions
their own, add them under **People** with a long-lived access token from their Home Assistant profile.

## The layout

Atrium reads your floors, areas, devices and entities and lays the house out by itself. A new light appears in its
room on its own; one that is removed disappears; one that comes back finds its old settings. Each room shows its
lights, switches, shades, fans, thermostat, locks and speakers.

In the app you can hide, rename, move and reorder rooms and things, and pick a room's thermostat. These settings
are kept in the add-on, so every device shows the same house.

## Good to know

- Atrium is its own machine on your tailnet (named `atrium` unless you change it), using Tailscale's `tsnet`
  library. Tailscale is not installed on the Home Assistant machine, and the machine itself is not exposed.
- Only Home Assistant's API and local files are reachable through Atrium; its login pages and the Supervisor are not.
  Web pages from other sites cannot use it.
- With HTTPS certificates enabled for your tailnet (Tailscale admin console → DNS), the address is `https://`;
  otherwise plain HTTP inside the tailnet's own encryption.
- Nothing inside Home Assistant is changed: no custom integration, no change to its sign-in.
- The layout settings live in `/data/config.json` and can be read and written at `/atrium/config` (JSON, `GET` and
  `PUT`, administrators only): `name`, `tabs`, `floors`, `rooms` (by area id: `hidden`, `name`, `floor`, `order`,
  `thermostat`, `entities`) and `entities` (by entity id: `hidden`, `name`, `room`, `show`).
