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
not at all when a live entity of the same name (or on a device of the same name) in the same room has taken its
place. Rooms
keep other restored entities, shown unavailable, since a device that is off for the season is still the room's.
The set-up page lists the entities that are stale (restored, though their integration is loaded) and where to
delete them in Home Assistant: **Settings → Devices & services → Entities**. Atrium never deletes anything.

## Beyond rooms: version 3 of the layout

A `version: 3` file (add-on 0.9.9 and later) can also describe what a house has besides rooms and scenes: the
water meter and its shut-off, air quality per room, appliances, thermostats that serve several rooms, what counts
as needing attention, seasonal lights, vehicles, a pool, the day's calendars, drives, reminders and schedules, and
panels for anything else. None of it is guessed; the file names every entity, the add-on checks each against Home
Assistant and the apps draw what they are given. The shapes are in the add-on repository's `docs/mapping.md`
("Version 3"); the atrium-mapping skill writes them.

## Televisions

Every television Home Assistant knows (Samsung, Google TV and Android TV, Roku, Apple TV, Sony, LG) gets a
remote in its room: power, inputs, a d-pad, transport and volume, with the keys the integration provides. A set
known to two integrations (Samsung's and a custom one, say) is one remote, matched by its MAC address or its
name; its built-in Chromecast and its Music Assistant player are ways to play to it, not remotes.
Where the room is more than one box — a receiver for the sound, an HDMI matrix for the inputs — write a Home
Assistant script for each input and map it in `atrium.yaml` under `remotes` (the atrium-mapping skill does this).

## Widgets

The app's Home Screen and Lock Screen widgets show what the layout says: without a word written, a Scenes
widget, a Home widget (alarm, solar, the outdoor temperature, each room's lights) and one per room. A `widgets:`
section in `atrium.yaml` names your own: a few scenes and a room's lights for the evening, the pool's readouts,
whatever a glance should show; the atrium-mapping skill writes it. The widget fits the items to its size.

## Cameras

Cameras play live in the app and on the web: UniFi Protect through Atrium (so anywhere Atrium works), Nest over
WebRTC from Google. UniFi Protect's events of the last day are under each camera as clips. Nest cameras have no
still picture, so their tiles show "Live view" until one is opened.

## Alerts

Home Assistant decides what deserves attention; Atrium puts it on every phone, with the app open or not, as a
notification and, when asked, as a Live Activity in the Dynamic Island and on the Lock Screen until the matter is
settled. An automation fires one event:

```yaml
alias: Front door unlocked at night
triggers:
  - trigger: state
    entity_id: lock.front_door
    to: unlocked
    for: "00:10:00"
conditions:
  - condition: time
    after: "21:00:00"
actions:
  - event: atrium_alert
    event_data:
      id: front-door-night            # the same id again replaces the notification; clear: true withdraws it
      title: Front door unlocked
      body: Unlocked for ten minutes
      level: warning                  # info | warning | critical: warning and critical show through Focus modes
      icon: lock.open                 # an SF Symbol, for the Live Activity
      entity: lock.front_door         # tapping the notification opens it in the app
      action: { title: Lock, service: lock.lock, entity: lock.front_door }   # one button under the notification
      live: true                      # also a Live Activity, until cleared (or eight hours)
```

A second automation, when the door locks, fires `atrium_alert` with the same `id` and `clear: true`, and the
notification and the Live Activity go. `to: [someone@example.com]` sends to those logins' phones only.
`service` with `entity` and optional `data` is any Home Assistant action; the phone runs it through Home
Assistant with its own sign-in when the button is pressed, without opening the app. iOS shows the button under a
long press (or a pull-down) on the notification, and its label comes from a fixed set chosen by what the action
does, not from `title`: Lock, Unlock (red, and the phone must be unlocked first), Close, Arm, Turn on, Turn off,
Done (an `input_boolean` or a to-do item) or Do it.

Apple carries the notifications, so the add-on needs an APNs key from the Apple developer account that publishes
the app: developer.apple.com → Certificates, Identifiers & Profiles → Keys → +, tick *Apple Push Notifications
service (APNs)*, download the `.p8` file (once; it cannot be downloaded again). In the add-on's Configuration tab
set `apns_team_id` (Membership details), `apns_key_id` (the key's id) and `apns_key` (the whole contents of the
`.p8` file, `-----BEGIN PRIVATE KEY-----` to the end). Apple sees the device token, the title and the body; nothing
else leaves the house, and without a key the events are logged and not sent. The phones register themselves the
first time the app connects after allowing notifications; `GET /atrium/devices` lists them for administrators,
`GET /atrium/alerts` the latest alerts.

## Good to know

- Atrium is its own machine on your tailnet (named `atrium` unless you change it), using Tailscale's `tsnet`
  library. Tailscale is not installed on the Home Assistant machine, and the machine itself is not exposed.
- Only Home Assistant's API and local files are reachable through Atrium; its login pages and the Supervisor are not.
  Web pages from other sites cannot use it.
- With HTTPS certificates enabled for your tailnet (Tailscale admin console → DNS), the address is `https://`;
  otherwise plain HTTP inside the tailnet's own encryption.
- When a newer add-on is published (or the app needs one), the app's Home screen says so, and the owner or an
  administrator can press **Update now** there, over Tailscale too. The add-on asks Home Assistant's own update
  entity for the add-on to install it (`update.install`), as pressing Update in Home Assistant would; Atrium
  restarts and the app reconnects. The add-on asks the Supervisor for nothing more than before.
- Nothing inside Home Assistant is changed: no custom integration, no change to its sign-in.
- The app sends its diagnostics here and nowhere else (Settings → Diagnostics in the app turns it off): when it
  launched and why, when it went to the background and came back, how long each connection took and why it
  dropped, the device model and the iOS and app versions. Nothing about the house is in them. They appear in
  this add-on's log as `app <login> (<device>) …` lines, and `GET /atrium/telemetry` lists the latest per device
  for administrators.
- The in-app preferences live in `/data/config.json` and can be read and written at `/atrium/config` (JSON, `GET`
  and `PUT`, administrators only): `name`, `tabs`, `floors`, `rooms` (by room id: `hidden`, `name`, `floor`,
  `order`, `thermostat`, `entities`) and `entities` (by entity id: `hidden`, `name`, `room`).
