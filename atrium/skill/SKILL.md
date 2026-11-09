---
name: atrium-mapping
description: Write or revise atrium.yaml, the Atrium layout file for a Home Assistant — the custom screens (Home readouts, Security, Energy, Music, TV remotes, Home Screen widgets) and the overrides on the automatic rooms (hide, rank, rename, thermostat). Use when setting Atrium up for a house, when a custom screen is empty or wrong, when a television's remote lacks a key or an input (an HDMI matrix, a receiver), when the rooms need tidying, or when the add-on log reports mapping problems.
---

# Atrium mapping

Atrium draws the rooms by itself from Home Assistant's areas, floors, devices and entities on every build: a new
device is in the app a moment later, with nothing written anywhere. What it cannot know goes in one file,
`atrium.yaml`, in the add-on's own folder (`/addon_configs/df84f1fd_atrium/atrium.yaml` on the Home Assistant
machine; the Atrium add-on itself sees it as `/config/atrium.yaml`; part of Atrium's backups):

- **The custom screens**, which need judgment: `home` (weather, inside and outside temperature, alarm, people),
  `security` (cameras, the doorbell, entries, hazards), `energy` (solar, grid, consumers, water), `music`
  (the speakers) and `remotes` (each television's keys, inputs and buttons). The add-on guesses each from what
  Home Assistant has; a section in the file replaces that guess wholesale. Start from the guesses: `atrium_auto`
  shows them in the file's shape.
- **Overrides** on the automatic rooms, only where they look wrong: hide a room or an entity, rank rooms and
  floors, rename, move a room to a floor, pick the thermostat, borrow an entity, drop an integration.

The contract (keys, defaults, what a room shows by itself) is `mapping.md` next to this file (the same text as
`docs/mapping.md` in the repository). Read it before writing anything.

The add-on re-reads the file whenever it changes and reports problems — an entity that does not exist, an unknown
floor — in its log (`ha apps logs df84f1fd_atrium` for the Store install; look for lines starting `mapping:`)
and on its set-up page.

## Where Atrium is

Atrium is an MCP server and an HTTP API; use whichever you can reach. Its tools are this skill (`atrium_skill`),
the contract (`atrium_contract`), the house as Home Assistant has it (`atrium_home`, filter by domain or area),
the add-on's own guesses for the custom screens (`atrium_auto`; `atrium_draft` runs the script below for the
same with `# check` marks), the current file
(`atrium_mapping_get`), saving with checks (`atrium_mapping_put`) and the status (`atrium_layout_status`). With
it, drafting and delivering are tool calls; `skills/atrium-mapping/draft.py` is only for when you cannot reach
Atrium at all.

- **From inside Home Assistant** (you are the Claude Code app, or another app the owner listed under Atrium's
  *Apps that may write the layout*): Atrium is at `http://df84f1fd-atrium:8099` on the apps' network — the add-on
  slug with a dash. No token: Atrium knows your app by its address. `claude mcp add --transport http atrium
  http://df84f1fd-atrium:8099/mcp`, or plain HTTP: `curl -sS -X PUT --data-binary @atrium.yaml
  http://df84f1fd-atrium:8099/atrium/mapping` (answers `{"rooms": N, "problems": [...], "path": ...}`), and
  `GET` the same path to read it back. No extra flags: Atrium's port is IPv4-only, so curl falls back to IPv4 by
  itself. A 403 is not only "not listed": its body (and Atrium's log) names the cause, and you tell the owner the
  remedy. *no add-on is listed under writers* or *`<slug>` is at `<address>`, not `<peer>`*: the owner lists your
  app's slug (`<repository-hash>_<name>`; `d7e97e69_claudecode` for the Claude Code app installed from its own
  repository) under Atrium's **Apps that may write the layout**. *`<slug>` does not resolve*: the listed slug is
  misspelt, or that app is not started. *Supervisor status 403*: Atrium is older than 0.9.1 and lacks
  `hassio_api`; the owner updates it. *Supervisor status 404* or *is "stopped"*: the listed app is not installed
  or not running. *IPv6 peer*: retry over IPv4 (`curl -4`). *the Supervisor did not answer*: try again shortly.
- **From a device on the owner's tailnet** (your Tailscale login is the owner's or an admin's): the same at
  `https://atrium.<tailnet>.ts.net` — `claude mcp add --transport http atrium https://atrium.<tailnet>.ts.net/mcp`.
- **Neither**: run `draft.py` against Home Assistant with a long-lived access token (below), review, and give the
  owner the file to put at `/addon_configs/df84f1fd_atrium/atrium.yaml` (the Terminal app sees that path; the
  add-on re-reads it on change) or to paste into **Edit** on Atrium's set-up page. Never ask for or store a token
  to reach Atrium's ingress for the owner; never make a session for the owner's user with the Supervisor token.

## 1. Draft

```sh
HA_TOKEN=… python3 draft.py --url http://homeassistant:8123 > /tmp/atrium.draft.yaml
# inside an add-on:  python3 draft.py --url http://supervisor/core -o /tmp/atrium.draft.yaml
```

Standard library only (Home Assistant OS has no pip). It reads the registries and every state over the WebSocket
API and writes a draft of the custom screens — the whole-house readouts, security, energy, music — plus a room
override wherever an area has more than one thermostat, and scripts offered as scene chips. It writes no rooms:
Atrium has them. A summary line (counts) goes to stderr. Every guess carries a trailing `# check`.

If you are revising an existing `atrium.yaml`, read it first and keep the owner's decisions; use a fresh
draft only to find entities that are new since.

## 2. Review — this is where the judgment goes

Go through the draft with the registry in mind (`atrium_home`, or `get_states` in a `ha` shell, shows what Home
Assistant has). Then look at the rooms as Atrium draws them (`atrium_layout_status` for the count; the set-up
page or the app for the picture) and add overrides only where they are wrong. Check, in this order:

- **Every `# check` line.** Decide, then delete the comment.
- **Restored entities.** `atrium_home` marks an entity `restored` when Home Assistant registers it but its
  integration has not provided it since Home Assistant started (its state is unavailable). Once Home Assistant has
  run ten minutes those are devices that are gone or renamed, and the draft already leaves them out of its guesses
  and skips ones with a live twin of the same name in the same area; do not name one in the file unless the device is expected
  back (the add-on accepts it and reports it as a problem, and the app shows it unavailable). The set-up page
  lists the stale ones for the owner to delete in Home Assistant; never delete, rename or recreate entities
  yourself to tidy them.
- **Rooms that are not rooms.** Storage, utility, plant rooms, "Server", "Driveway": `hide: true` them, or borrow
  their one useful entity into the room it serves with `entities:`. The **Unassigned** room (entities in no
  area) is worth a look: assign them an area in Home Assistant if they belong somewhere, or hide the room.
- **Thermostats.** A thermostat sits where the installer put it, not where it heats: set `thermostat:` on the
  room it belongs with. A second `climate.*` in an area is usually a fridge, a wine cooler or a car: hide it.
  Underfloor heating and some thermostatic valves report `hvac_action: heating` whenever their program is on,
  not only while they heat: ask the owner, or compare `hvac_action` with `current_temperature` and `temperature`
  in a live state (a "heating" floor already a degree above its target gives it away). For each such entity
  write `{ entity: climate.x, action: setpoint }` under `climate.thermostats` (version 3), and the apps count it
  as heating only while its target is above the current temperature.
- **Readouts.** Each room's temperature is the area's own pick, else a temperature sensor there with a plausible
  reading — which can be a pipe or an appliance. Where it is wrong, name the right sensor, or `temperature: none`
  (likewise `humidity`, and `thermostat: none` for a room that should show no thermostat at all).
- **Rank.** The rooms a person uses most go first: `rank: 1`, `rank: 2`… on a few rooms per floor, and on the
  floors. Leave the rest alone.
- **Speakers.** Only real speakers belong in `music.speakers`: not TVs, receivers' zones that mirror a speaker,
  phones, or Music Assistant copies of players that already exist. One entity per physical speaker.
- **The doorbell.** `security.doorbells` lists cameras; make sure it is the camera at the door, not a second
  channel of it, and that `camera_for` maps each entry to the camera that sees it.
- **Energy.** Find the house's solar, load, grid and battery sensors. `solar_power` is positive when producing;
  `load_power` positive when consuming (several entries are summed, so list the mains legs or the one house
  total, not both); `grid_power` is signed, positive when importing; `battery_power` signed, positive when
  discharging; `battery_level` is a percentage. Check each sign against the live state (a house importing at
  night must show a positive grid value); for a sensor that reports the opposite sign write
  `{entity: sensor.x, invert: true}`. `solar + battery + grid = load`: the apps derive one missing term from the
  other three, so a house with a battery needs at least three of the four, and a house without one leaves the
  battery keys out (never name a phone's battery). `solar_today` and `used_today` are the day's totals. Units
  are read from each sensor (W or kW; kWh, Wh or MWh), so pick the right quantity, not the right unit.
  `consumers` is the "Using now" list: keep the circuits worth watching (oven, EV, pool pump), drop the rest. If
  the house has water metering, fill in `water_flow` and `water_today`; otherwise delete those lines.
- **People.** `home.people` should be the household, not every `person.*` (guests, test users).
- **Whole integrations.** A car, a printer, a test integration: one `hide: [integration:<platform>]` line is
  better than hiding its entities one by one.
- **Names.** `names:` only where Home Assistant's name is wrong on a screen (Atrium already shortens "Kitchen
  Island lights" to "Island lights" in the kitchen). Add names for consumers ("Oven" not "Oven power") and
  cameras; do not rename everything.
- **Floors.** Rank them as the family says them (top of the list first). Rooms on no floor sit on an `other`
  floor ("Elsewhere"): give them a floor in Home Assistant, or rename `other` to something sensible.
- **Entries and hazards.** Door, window and gate contacts in `entries`; smoke, gas, CO and leak sensors in
  `hazards`. Drop anything that is a car.
- **Remotes.** `atrium_auto` lists one per television it found, with its remote and player entities. Check each
  against the room: is that the television, and the right integration's copy? Then the three things the add-on
  cannot know, and only where they apply:
  - **Volume** through a receiver: `volume: media_player.<avr>` when the room's sound goes through one.
  - **Inputs** that are not the television's own source list: an HDMI matrix, a receiver's inputs, a streamer on
    HDMI 2. Write them as `inputs:` options, each pointing at what does the switching, with `when:` naming the
    entity and state that mean it is selected.
  - **Keys** the integration lacks, or that belong to another device (navigation on the Google TV, volume on the
    receiver): `keys:` entries, one line each; `none` drops a key that does nothing on this set.

  **Put the logic in Home Assistant.** When choosing an input means several steps (switch the matrix, wake the
  receiver, pick its HDMI, set the television's source), write a Home Assistant **script** that does them — in
  Home Assistant's own YAML, where it can be run from the Home Assistant UI and used by automations too — and point
  the Atrium input or button at the script. You have the tools for that (the Home Assistant config, `ha` and the
  WebSocket API); the layout file only names the script. A television with nothing special needs no entry at
  all: a Frame TV, a Google TV or a Roku on its own is complete as guessed.

## 3. Deliver and verify

Hand the file to Atrium yourself: `atrium_mapping_put` (MCP) or `PUT /atrium/mapping` (HTTP), from wherever you
reached it above. It is checked first (a bad file is refused with the reason and nothing changes), saved,
rendered, and the answer lists what Home Assistant does not have. The owner does nothing.

Then:

1. `ha apps logs df84f1fd_atrium` — read the `mapping:` lines. The good case is one line, `mapping: N rooms,
   no problems`. Each other line names something that could not be used (an entity Home Assistant does not
   have, a floor that is not a floor), a restored entity the file names (kept, but unavailable), or why the whole file was refused (bad YAML, an unknown key, a room
   without an id; the last good file stays in use until it is fixed). Fix the file — never add an id to
   Home Assistant to satisfy it — and save again; the add-on re-reads on change.
2. Open the add-on's set-up page (its ingress panel in the Home Assistant sidebar): it shows whether the file
   was found, whether it fills the custom screens, the room count, the parse error if any, and the problem list.
3. If the app is open, its screens update by themselves.

## Rules

- **Version 3** (`docs/mapping.md`, "Version 3": water, air, appliances, vehicles, pool, day, panels…) needs
  add-on 0.9.9 or later; an older add-on refuses a `version: 3` file. Write `version: 2` until the house's add-on
  reads 3, then add the sections.
- **Widgets.** Without `widgets:` the phone offers Scenes, Home and one widget per room. Write `widgets:` when
  the owner wants a particular set on the Home Screen: a few scenes and a room's lights for the evening, the pool
  and the solar readouts, the alarm. Items are `{scene: …}`, `{entity: …}` (a toggle), `{lights: <room>}`,
  `{readout: <sensor>}`, `alarm`, `solar`, or `{name, icon, action}`; `small`/`medium`/`large`/`lock` pick what
  each size shows. Keep a widget to what fits a glance: eight items at most.
- **Never invent an entity id.** Every id in the file must come from Home Assistant (`atrium_home`, the draft,
  or a `get_states` listing you looked at). If unsure whether an entity exists, look it up; do not guess a name.
- Write only what the automatic rooms get wrong and what the custom screens need. Do not list rooms or their
  entities to "be complete": that is the add-on's job, and a listing goes stale the day a device is added.
- Remove every `# check` comment once you have checked it; the finished file has none.
- No secrets in the file: no tokens, passwords, URLs with credentials. The token stays in the environment.
- The owner's in-app preferences (hide, rename, reorder, move) live elsewhere and survive a new file; do not
  try to reproduce them in the file.
- Keep the key order of the contract (`version`, `name`, `floors`, `rooms`, `hide`, `scenes`, `home`, `security`,
  `energy`, `music`, `names`) so the file stays easy to compare across revisions.

## Alerts (add-on 0.9.15 and later)

The layout file does not hold alerts; Home Assistant automations do, and they reach every phone through the
add-on by firing an `atrium_alert` event (`id`, `title`, `body`, `level`, `entity`, one `action`, `live`, `clear`,
`to`; the add-on's DOCS, "Alerts", has the shape and an example). When the owner asks for an alert, write the
automation in Home Assistant and have the settling condition fire the same `id` with `clear: true`. What deserves
one: an alarm state, a door or garage left unlocked or open late unless the sensor has been unavailable or the
door has been that way all day, bins not out on collection day, the water shut off, a leak, smoke. Keep them rare.
