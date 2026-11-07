---
name: atrium-mapping
description: Write or revise atrium.yaml, the Atrium layout (rooms, floors, scenes, security, energy, music) for a Home Assistant — use when setting Atrium up for a house, when its screens are empty or wrong, or when the add-on log reports mapping problems.
---

# Atrium mapping

Atrium renders exactly one declared layout: `atrium.yaml` in the add-on's own folder, which is
`/addon_configs/df84f1fd_atrium/atrium.yaml` on the Home Assistant machine (the Terminal add-on and other add-ons
with `addon_configs` access see that path; the Atrium add-on itself sees it as `/config/atrium.yaml`). It is
part of Atrium's backups. Home Assistant's own `/config` is not involved.
Nothing is guessed by the add-on; a screen stays empty until the mapping fills it. The contract (keys, structure,
defaults) is `mapping.md` next to this file (the same text as `docs/mapping.md` in the repository). Read it before
writing anything.

The add-on re-reads the file whenever it changes and reports problems — an entity that does not exist, an unknown
floor — in its log (`ha apps logs df84f1fd_atrium` for the Store install; look for lines starting `mapping:`)
and on its set-up page.

You need either a shell on the Home Assistant machine or network access to it, plus a token:

- a **long-lived access token**: Home Assistant → the user's profile (bottom left) → Security → Long-lived access
  tokens → Create; put it in `HA_TOKEN`, never in the file or the repository;
- or, inside an add-on, `SUPERVISOR_TOKEN` is already set and the API is `http://supervisor/core`.

## 1. Draft

```sh
HA_TOKEN=… python3 draft.py --url http://homeassistant:8123 > /tmp/atrium.draft.yaml
# inside an add-on:  python3 draft.py --url http://supervisor/core -o /tmp/atrium.draft.yaml
```

Standard library only (Home Assistant OS has no pip). It reads the area, floor, device and entity registries and
every state over the WebSocket API, and writes a draft that follows the contract: floors by level, one room per
area with something to control (lights, switches, covers, fans, locks, speakers), the room's thermostat, a room
temperature and humidity sensor, scenes, the whole-house readouts, security, energy, music, and the `names` that
drop the room's own name from an entity's ("Kitchen Ceiling" in Kitchen → "Ceiling"). A summary line (counts)
goes to stderr. Every guess carries a trailing `# check`.

If you are revising an existing `atrium.yaml`, read it first and keep the owner's decisions; use a fresh
draft only to find entities that are new since.

## 2. Review — this is where the judgment goes

Go through the draft with the registry in mind (the dump the script read is what Home Assistant has; `get_states`
in a `ha` shell or the Developer tools page shows the same). Check, in this order:

- **Every `# check` line.** Decide, then delete the comment.
- **Rooms that are not rooms.** Storage, utility, plant rooms, "Server", "Driveway": hide them (delete the room)
  or merge their entities into the room they serve. Keep the file to the rooms a person walks into.
- **Thermostats.** A thermostat sits where the installer put it, not where it heats: move `thermostat:` to the
  room it belongs with. A second `climate.*` in an area is usually a fridge, a wine cooler or a car; drop it.
- **Speakers.** Only real speakers belong in `music.speakers` and in rooms: not TVs, receivers' zones that mirror a
  speaker, phones, or Music Assistant copies of players that already exist. One entity per physical speaker.
- **The doorbell.** `security.doorbells` lists cameras; make sure it is the camera at the door, not a second
  channel of it, and that `camera_for` maps each entry to the camera that sees it.
- **Energy.** Find the house's solar, grid and load sensors. `solar_power` is positive when producing;
  `load_power` positive when consuming (several entries are summed, so list the mains legs or the one house
  total, not both); `solar_today` and `used_today` are the day's totals. Units are read from each sensor (W or
  kW; kWh, Wh or MWh), so pick the right quantity, not the right unit. `consumers` is the "Using now" list: keep
  the circuits worth watching (oven, EV, pool pump), drop the rest. If the house has water metering, fill in
  `water_flow` and `water_today`; otherwise delete those lines.
- **People.** `home.people` should be the household, not every `person.*` (guests, test users).
- **Names.** `names:` only where Home Assistant's name is wrong on a screen. Keep the shortened ones that read
  well; add names for consumers ("Oven" not "Oven power") and cameras; do not rename everything.
- **Floors.** Name and order them as the family says them (top of the list first). A room on no floor went to
  an `other` floor: give it a real floor or leave `other` renamed to something sensible.
- **Entries and hazards.** Door, window and gate contacts in `entries`; smoke, gas, CO and leak sensors in
  `hazards`. Drop anything that is a car.

## 3. Deliver and verify

Give the file to the add-on: `PUT /atrium/mapping` with the YAML as the body. It is checked first (a bad file is
refused with the reason and nothing changes), saved, rendered, and the answer lists what Home Assistant does not
have: `{"rooms": 26, "problems": [...], "path": "/addon_configs/df84f1fd_atrium/atrium.yaml"}`. `GET` reads it back.

- On the tailnet, from a device whose Tailscale login is the owner's or an admin's (no token needed):
  `curl -sS -X PUT --data-binary @atrium.yaml https://atrium.<tailnet>.ts.net/atrium/mapping`
- From the Home Assistant machine (no Tailscale there), through Home Assistant's ingress with an administrator's
  long-lived token: create an ingress session, then call the add-on's ingress path with it:
  ```sh
  T=…                                                            # the token
  S=$(curl -sS -X POST -H "Authorization: Bearer $T" http://homeassistant:8123/api/hassio/ingress/session | python3 -c 'import json,sys;print(json.load(sys.stdin)["session"])')
  P=$(curl -sS -H "Authorization: Bearer $T" http://homeassistant:8123/api/hassio/addons/df84f1fd_atrium/info | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["ingress_url"])')
  curl -sS -X PUT --data-binary @atrium.yaml -b "ingress_session=$S" "http://homeassistant:8123${P}atrium/mapping"
  ```
- Or put the file in place yourself: `/addon_configs/df84f1fd_atrium/atrium.yaml` (the Terminal add-on sees that
  path); the add-on re-reads it on change.

Then:

1. `ha apps logs df84f1fd_atrium` — read the `mapping:` lines. The good case is one line, `mapping: N rooms,
   no problems`. Each other line names something that could not be used (an entity Home Assistant does not
   have, a floor that is not declared) or why the whole file was refused (bad YAML, an unknown key, a room
   without an id; the last good mapping stays in use until the file is fixed). Fix the file — never add an id to
   Home Assistant to satisfy it — and save again; the add-on re-reads on change.
2. Open the add-on's set-up page (its ingress panel in the Home Assistant sidebar): it shows whether the file
   was found, the room count, the parse error if any, and the same problem list.
3. If the app is open, its screens update by themselves.

## Rules

- **Never invent an entity id.** Every id in the file must come from the registry dump the script read (or a
  `get_states` listing you looked at). If unsure whether an entity exists, look it up; do not guess a name.
- Keep the file small and readable: the rooms people use, the controls they touch, the readouts they care about.
  Fewer, right entries beat a complete list.
- Remove every `# check` comment once you have checked it; the finished file has none.
- No secrets in the file: no tokens, passwords, URLs with credentials. The token stays in the environment.
- The owner's in-app preferences (hide, rename, reorder, move) live elsewhere and survive a new mapping; do not
  try to reproduce them in the file.
- Keep the key order of the contract (`version`, `name`, `floors`, `rooms`, `scenes`, `home`, `security`,
  `energy`, `music`, `names`) so the file stays easy to compare across revisions.
