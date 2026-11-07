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

Atrium shows exactly what one file says: `atrium.yaml` in its own folder (`/addon_configs/df84f1fd_atrium/` on the
Home Assistant machine, included in backups; Atrium sees nothing else of Home Assistant's files) — which
rooms, on which floors, with which lights, shades, thermostat and speakers; the Home screen's readouts; what the
Security, Energy and Music screens show. Nothing is guessed, so nothing wrong ever appears; a screen is empty until
the file fills it.

The file is written for your house by the **atrium-mapping** skill, by a Claude:

- **Recommended:** if you run the Claude Code app in Home Assistant, tell it to run the atrium-mapping skill from
  github.com/cmmichael/atrium-addon. It reads your areas and devices, decides the judgment calls (which thermostat
  heats which room, which camera is the doorbell, which sensors are solar and grid) and writes the layout to Atrium
  itself, once you list it under **Apps that may write the layout** in Atrium's Configuration (its slug is
  `d7e97e69_claudecode`; the set-up page shows this). A listed app is trusted by its address on the apps'
  network, checked with the Supervisor on every request; an app with host networking and `NET_ADMIN` could pose
  as one, but such an app can already do anything on the machine.
- **Otherwise:** point your Claude at Atrium. Atrium is an MCP server on your tailnet
  (`https://atrium.<your-tailnet>.ts.net/mcp`): the contract, the skill, your house, a draft and saving are its
  tools, and Tailscale is the sign-in. In Claude Code: `claude mcp add --transport http atrium <that address>`,
  then ask it to make the layout.

The set-up page shows the file's path and opens it in an editor, so you can change a line yourself; every way in
is checked the same, and the page says what the file names that Home Assistant does not have. The skill is in the add-on repository under
`skills/atrium-mapping`, with the file's full contract (`mapping.md`). You can also write the file by hand.

Atrium re-reads the file whenever it changes and reports, in its log and on its set-up page, anything in it that
Home Assistant does not have. In the app you can still hide, rename, move and reorder rooms and things; those
preferences are kept separately and survive a new layout.

## Good to know

- Atrium is its own machine on your tailnet (named `atrium` unless you change it), using Tailscale's `tsnet`
  library. Tailscale is not installed on the Home Assistant machine, and the machine itself is not exposed.
- Only Home Assistant's API and local files are reachable through Atrium; its login pages and the Supervisor are not.
  Web pages from other sites cannot use it.
- With HTTPS certificates enabled for your tailnet (Tailscale admin console → DNS), the address is `https://`;
  otherwise plain HTTP inside the tailnet's own encryption.
- Nothing inside Home Assistant is changed: no custom integration, no change to its sign-in.
- The in-app preferences live in `/data/config.json` and can be read and written at `/atrium/config` (JSON, `GET`
  and `PUT`, administrators only): `name`, `tabs`, `floors`, `rooms` (by room id: `hidden`, `name`, `floor`,
  `order`, `thermostat`, `entities`) and `entities` (by entity id: `hidden`, `name`, `room`).
