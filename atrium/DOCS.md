# Atrium

Your house in the Atrium app and on the web, from anywhere. Atrium puts Home Assistant on your tailnet and lets
Tailscale say who is calling: no passwords, no tokens, no port forwarding, nothing to type.

## Set up

1. **Install**, then **Start**.
2. **Open Web UI** and press **Sign in to Tailscale**. Once.
3. The page shows a code and an address. Open the Atrium app on your iPhone or iPad, with Tailscale on, and scan
   the code (or enter the address). The same address opens Atrium in any browser on your tailnet.

Turn on **Show in sidebar** to keep the set-up page in Home Assistant's sidebar. Its **Open the app here** button
opens Atrium through Home Assistant itself, for a computer that cannot be on your tailnet (administrators).

## Who can use it

Whoever signed Atrium in. To let others in, add their Tailscale logins under **Who may use Atrium** in the
Configuration tab. Your tailnet's own rules decide which devices can reach Atrium at all; to take someone's access
away, remove their login here or their device from your tailnet.

Everyone acts with Atrium's own access to Home Assistant, which is an administrator's. To make someone's actions
their own, add them under **People** with a long-lived access token from their Home Assistant profile.

### Without Tailscale

The app can also sign in to Home Assistant directly, with a Home Assistant account: at home, over a VPN such as
WireGuard, or through any address your Home Assistant has. Then everything it does is that person's own, and it
fetches only the layout and camera clips from Atrium, one of two ways:

- **Administrators**: through Home Assistant itself (the same way the sidebar panel works), from any address.
- **Everyone else**: on Atrium's port **8126** on the Home Assistant machine, which answers only someone showing a
  Home Assistant sign-in, and only with the layout and clips. That works at home and over a VPN into your home
  network, not from outside. To turn it off, clear the port under **Network** in the Configuration tab.

## The layout

Atrium draws the rooms itself from Home Assistant: one room per area, on its floor, with the lights, switches,
shades, fans, locks, thermostat and speaker that area has. Add a device in Home Assistant and it is in Atrium a
moment later; nothing to write. Devices in no area sit in an **Unassigned** room.

The Security, Energy and Music screens, and the Home screen's readouts, are guessed from what Home Assistant
has: cameras, doorbells, door and window contacts, smoke and leak sensors, solar and grid sensors, speakers.
Where a guess is wrong — or a room should be hidden, ranked, renamed, moved, or given the right thermostat —
one file says so: `atrium.yaml` in Atrium's own folder (`/addon_configs/df84f1fd_atrium/` on the Home Assistant
machine, included in backups; Atrium sees nothing else of Home Assistant's files). A Claude writes it for your
house with the **atrium-mapping** skill:

- **Recommended:** if you run the Claude Code app in Home Assistant, tell it to run the atrium-mapping skill from
  github.com/cmmichael/atrium-addon. It reads your house, decides the judgment calls (which camera is the doorbell, which
  sensors are solar and grid, which rooms matter most) and writes the file to Atrium itself, once you list it under **Apps that may write the layout**: on the set-up page, press **Allow an app…**, pick it from the installed apps (or type its slug and check it) and confirm; it applies at once, no restart. The same list is in Atrium's Configuration tab. An app's slug is
  `<repository-hash>_<name>`, `d7e97e69_claudecode` for the Claude Code app installed from its own repository; the
  Supervisor shows it in the app's address bar. A listed app is trusted by its IPv4 address on the apps'
  network, confirmed with the Supervisor on every request (`hassio_api`, at the default role); an app with host
  networking and `NET_ADMIN` could pose as one, but such an app can already do anything on the machine.

  To check it works, from inside the listed app: `curl -sS -X PUT --data-binary @atrium.yaml
  http://<atrium-hostname>:8099/atrium/mapping` (the hostname is Atrium's slug with a dash, `df84f1fd-atrium` for the
  Store install) answers `{"rooms": N, "problems": [...], "path": ...}`. A **403** names its cause in the body (and in
  Atrium's log), and each has one remedy:
  - *IPv6 peer*: the request came over IPv6; Atrium knows apps by their IPv4 address only. Atrium's port is IPv4-only
    so clients fall back by themselves; if one does not, use the IPv4 address.
  - *no add-on is listed under writers*: add the app's slug under **Apps that may write the layout**.
  - *`<slug>` does not resolve*: the listed slug is misspelt, or that app is not installed or not started.
  - *`<slug>` is at `<address>`, not `<peer>`*: the request came from an app that is not the listed one.
  - *the Supervisor does not confirm it: Supervisor status 403*: Atrium lacks `hassio_api` (an Atrium older than 0.9.1;
    update it). *Status 404*: the slug is not installed. *is "stopped"*: the app is not running.
  - *the Supervisor did not answer*: the Supervisor is busy or restarting; try again.
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

**Devices that are gone.** Home Assistant keeps an entity registered after its device is removed or renamed, and
at start writes it an unavailable placeholder (`restored`). Ten minutes after Home Assistant starts, Atrium stops
choosing such entities by itself: never as the doorbell, a thermostat, the solar sensor or a room's readout, and
not at all when a live entity of the same name (or on a device of the same name) has taken its place. Rooms
keep other restored entities, shown unavailable, since a device that is off for the season is still the room's.
The set-up page lists the entities that are stale (restored, though their integration is loaded) and where to
delete them in Home Assistant: **Settings → Devices & services → Entities**. Atrium never deletes anything.

## Televisions

Every television Home Assistant knows (Samsung, Google TV and Android TV, Roku, Apple TV, Sony, LG) gets a
remote in its room: power, inputs, a d-pad, transport and volume, with the keys the integration provides.
Where the room is more than one box — a receiver for the sound, an HDMI matrix for the inputs — write a Home
Assistant script for each input and map it in `atrium.yaml` under `remotes` (the atrium-mapping skill does this).

## Cameras

Cameras play live in the app and on the web: UniFi Protect through Atrium (so anywhere Atrium works), Nest over
WebRTC from Google. UniFi Protect's events of the last day are under each camera as clips. Nest cameras have no
still picture, so their tiles show "Live view" until one is opened.

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
