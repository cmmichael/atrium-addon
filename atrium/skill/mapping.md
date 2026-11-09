# The layout mapping: `atrium.yaml`

Atrium draws the rooms itself, from Home Assistant, on every build: one room per area, on its floor, holding the
lights, switches, shades, fans, locks, thermostat and speaker Home Assistant has in that area (directly or through
the device). Every scene is a chip. Add a device in Home Assistant and it is in Atrium a moment later; nobody
writes anything. Entities in no area go to an **Unassigned** room; rooms on no floor go to an **Elsewhere** floor.

`atrium.yaml` carries what that cannot know. Two kinds of thing:

- **Overrides** on the automatic rooms: hide a room or an entity, rank rooms and floors, rename, move a room to
  another floor, pick the thermostat, borrow an entity from elsewhere, drop a whole integration.
- **The custom screens**, where the add-on's guess may be wrong: the Home screen's readouts (weather, inside and
  outside temperature, alarm, people), **Security** (which cameras, which is the doorbell, which entries and
  hazards), **Energy** (solar, grid, the consumers worth watching, water), **Music** (the speakers) and the
  **remotes** (each television's keys, inputs and buttons, mapped to Home Assistant). The add-on guesses each
  from what Home Assistant has (below); a section in the file replaces that guess wholesale, and an empty section
  (`security: {}`, `remotes: []`) turns it off.

The file lives in the add-on's own folder: `/addon_configs/df84f1fd_atrium/atrium.yaml` on the Home Assistant
machine (the Terminal add-on sees that path; it is part of the add-on's backups). Only that folder is visible to the
add-on, not Home Assistant's configuration or secrets. The owner's in-app preferences (hide, rename, reorder, move)
apply on top of both the automatic rooms and the file. The add-on re-reads the file whenever it changes, and
reports problems (an entity that does not exist, an unknown floor) in its log and on its set-up page.

It is written by the **atrium-mapping** skill (`skills/atrium-mapping/`), run by a Claude with access to the Home
Assistant, or by hand. Every value is a Home Assistant entity id unless said otherwise. Everything is optional.

```yaml
version: 2
name: Cedar House                 # what to call the house; default: Home Assistant's name

floors:                           # overrides by floor id; floors come from Home Assistant, by level
  - id: upstairs
    rank: 1                       # ranked floors first (lowest first); the rest keep Home Assistant's level order
  - id: ground
    name: Main floor              # rename
  - id: other                     # the floor for rooms on no floor ("Elsewhere" by default)
    name: Outbuildings

rooms:                            # overrides by area id; a room not listed is shown as Home Assistant has it
  - id: kitchen
    rank: 1                       # ranked rooms first within their floor (lowest first); the rest by name
    name: Kitchen                 # rename
    floor: ground                 # move to another floor
    icon: fork.knife              # an SF Symbol name (the web app maps the ones below); default: guessed from the name
    thermostat: climate.hall      # the thermostat to show here, when the automatic pick is wrong or elsewhere; none = no thermostat
    temperature: sensor.kitchen_temperature   # the room's readout; default: the area's own pick, else a room sensor there; none = no readout
    humidity: sensor.kitchen_humidity         # likewise
    entities: [light.hall_nightlight]         # shown here as well, wherever Home Assistant put them
    hide: [switch.kitchen_identify]           # entities to leave out of this room
  - id: garage
    hide: true                    # the whole room
  - id: unassigned                # the room for entities in no area; hide it, rank it, rename it like any other
    name: Not yet placed
  - id: outside                   # an id that is not an area makes a room of the file's own
    name: Outside
    floor: other
    entities: [light.path, switch.fountain]

hide:                             # house-wide: never show these anywhere
  - switch.*_identify             # a glob over entity ids
  - light.garage_workbench        # one entity
  - integration:tesla_fleet       # everything from an integration (its platform name)

scenes:                           # these chips first, in this order; every other scene follows by recency of use
  - entity: scene.movie
    name: "🎬 Film"               # default: the scene's name; a leading emoji becomes the chip's glyph
  - entity: script.goodnight      # scripts are not chips unless listed

home:                             # the Home screen's readouts
  weather: weather.home
  outdoor_temperature: sensor.porch_temperature
  indoor_temperature: sensor.hall_temperature
  alarm: alarm_control_panel.house
  people: [person.ana, person.ben]

security:                         # the Security screen; absent = no Security tab
  cameras: [camera.front_door, camera.driveway]
  doorbells: [camera.front_door]  # cameras that are doorbells (shown first, with the bell)
  camera_for:                     # which camera to show when an entry opens
    binary_sensor.front_door: camera.front_door
  locks: [lock.front_door, lock.back_door]
  entries: [binary_sensor.front_door, binary_sensor.garage_door]   # door, window and gate contacts
  hazards: [binary_sensor.smoke_hall, binary_sensor.leak_laundry]  # smoke, gas, CO, leak sensors
  doors:                          # doors whose lock is a button (UniFi Access); absent = the add-on's guess, [] = none
    - { unlock: button.front_door_unlock, contact: binary_sensor.front_door, name: Front door }

energy:                           # the Energy screen; absent = no Energy tab
  solar_power: sensor.pv_power                   # W or kW: the apps read the sensor's unit; positive when producing
  load_power: [sensor.house_power]               # W or kW; several are summed; positive when consuming
  grid_power: sensor.grid_power                  # signed: positive = importing, negative = exporting (the energy dashboard's convention)
  battery_power: { entity: sensor.battery_power, invert: true }   # signed: positive = discharging; invert: true for a sensor with the opposite sign
  battery_level: sensor.battery_soc              # percent
  solar_today: sensor.pv_energy_today            # kWh, Wh or MWh, by the sensor's unit
  used_today: sensor.house_energy_today          # kWh
  consumers: [sensor.oven_power, sensor.ev_charger_power]   # W, the "Using now" list
  water_flow: sensor.water_flow                  # any unit
  water_today: sensor.water_today

music:                            # the Music screen; absent = no Music tab
  speakers: [media_player.kitchen, media_player.living_room]

remotes:                          # televisions and the like; absent = one per television the add-on finds, [] = none
  - id: living_tv                 # any id; default: the remote entity's
    name: Living room TV
    room: living_room             # the area whose room shows the remote; default: the remote's own area
    remote: remote.living_tv      # its remote entity: the keys the integration knows come from this by themselves
    player: media_player.living_tv   # its media player: on/off, what is playing, play/pause, and its source list as the inputs
    volume: media_player.avr      # a receiver whose volume the remote drives instead of the television's (keys and level)
    inputs:                       # the input (or app) picker; default: the player's own source list, live
      - name: Apple TV            # an option of the file's own: an action, and when it is lit
        action: script.living_apple_tv
        when: { select.matrix_out_1: Apple TV }
      - name: PS5
        action: { select: select.matrix_out_1, option: PS5 }
    keys:                         # keys to add or change; any shape an action takes (below), or none to drop a key
      home: { remote: remote.living_google_tv, command: HOME }   # a key on another device
      art: { remote: remote.living_tv, command: KEY_POWER }      # a key of the file's own (a Frame's art mode)
      guide: none
    buttons:                      # named buttons under the keys: a scene, a script, a service
      - { name: Movie night, icon: film, action: scene.movie_night }
      - { name: Lights off, icon: lightbulb, action: { service: light.turn_off, entity: light.living_room } }
  - name: Receiver                # a media player alone: its inputs and whatever keys the file gives it
    player: media_player.avr
    inputs: media_player.avr

names:                            # display names, where Home Assistant's are not right for a screen
  light.kitchen_ceiling: Ceiling
  sensor.oven_power: Oven
```

### Widgets

```yaml
widgets:
  - id: evening
    name: Evening
    items:
      - scene: scene.movie_night            # tap runs it
      - entity: light.kitchen_island        # on or off; tap toggles (light, switch, fan, input_boolean)
      - lights: kitchen                     # the room's lights: how many are on; tap turns them off
      - readout: sensor.pool_temperature    # a number with its unit
      - alarm                               # the alarm's state; tap opens the app
      - solar                               # solar power now
      - { name: PS5, icon: gamecontroller, action: script.matrix_ps5 }   # any action (see Actions)
    small: [scene.movie_night]              # optional: what each size shows, by the ids above
    medium: [scene.movie_night, kitchen, PS5]
```

Each widget is one choice in the phone's widget gallery. The phone fits the items to the size it is given (one in
the small square, a row in the medium, a grid in the large, a line or two on the Lock Screen) in the order listed,
unless `small`, `medium`, `large` or `lock` say which items that size shows: a scene or entity by its id, a room's
lights by the room's id, `alarm`, `solar`, a button by its name. Without `widgets:` the add-on offers Scenes (the
house's scenes), Home (alarm, solar, the outdoor temperature, each room's lights) and one widget per room with
lights or scenes. An item Home Assistant does not have is dropped and reported.

### Actions

An action, in `keys`, `inputs` and `buttons`, is one Home Assistant service call, in a short form:

- an entity id alone: a `script` or `scene` is turned on, a `button` pressed, an `automation` triggered, a
  `switch`, `light` or `input_boolean` toggled;
- `{remote: remote.x, command: KEY_X}`: `remote.send_command`, for a key the integration has a name for;
- `{select: select.x, option: PS5}`: `select.select_option`, for a matrix or a receiver's input select;
- `{service: domain.service, entity: …, data: {…}}`: anything else.

The keys the apps draw, when a remote has them: `power_on`/`power_off` (or `power`), `up` `down` `left` `right`
`select` (the d-pad), `back` `home` `menu` `info` `guide` `exit`, `play` `pause` `play_pause` `rewind`
`fast_forward` `previous` `next`, `volume_up` `volume_down` `mute`, `channel_up` `channel_down`, `0`–`9`. Any other
key name in the file is kept for a future screen and does no harm.

**Put the logic in Home Assistant, not in the file.** When one button means several things — "Apple TV" switches
the matrix, wakes the receiver, picks its HDMI input and sets the television to the right source — write a Home
Assistant script that does it, and point the input or button at the script. The file maps buttons; Home Assistant
does the work, in its own language, where it can be tested from the Home Assistant UI and reused by automations
and voice assistants. The same goes for a key an integration lacks: a script that sends the right sequence.

## Energy: solar, battery, grid and home

The four powers are tied by **solar + battery + grid = load**, with the battery positive when discharging and the
grid positive when importing. Every power key (`solar_power`, each `load_power` entry, `grid_power`,
`battery_power`) is an entity id or `{entity: …, invert: true}` for a sensor that reports the opposite sign; the
apps negate it. Units come from each sensor (W or kW). The apps show measured values as measured. When exactly
one of solar, load, battery or grid is missing they derive it from the other three; a house with neither
`battery_power` nor `battery_level` has no battery (that term is zero, and no battery node is drawn), so a file
with only solar and load still shows the grid as load minus solar; one with `battery_level` but no
`battery_power` has two unknowns, so nothing is derived until `battery_power` or `grid_power` is named. Flows, for the arrows and the caption: solar
feeds the home first, then charges the battery, then exports; a discharging battery feeds the home; the grid
covers the rest, including a battery charging from it. The battery is a fourth node between solar and home. The
add-on never guesses `grid_power`, `battery_power` or `battery_level`; the skill's draft may, marked `# check`.

## What a room shows by itself

From the entities Home Assistant has in the area (not disabled, not hidden, not a config or diagnostic entity):
every `light`, `cover`, `fan` and `lock`; every `switch` except a kiosk's (browser_mod); a `climate` whose range
spans room temperature (so a fridge is not a thermostat); a `media_player` that is a speaker (device class
`speaker`, or listed under `music`). Locks, doors and covers from car integrations are left out. In that order,
then by name. The room's temperature readout is the area's own temperature entity, else a temperature sensor there
reading a plausible room temperature; humidity likewise. `none` turns a readout (or the thermostat) off when the
automatic pick is wrong and there is nothing better: a water-pipe sensor in the garage, say.

Left out of rooms altogether: anything from a car integration, a wall tablet or browser (browser_mod, Fully
Kiosk), an appliance integration's own lights, switches and climates (Miele, Home Connect, LG ThinQ…), and a
camera's or doorbell's own speaker. Music Assistant's copies of players that already exist are not speakers.

## What the add-on guesses for the custom screens

- **home**: the first `weather.*` (`weather.forecast_home` or `weather.home` when there is one), the first
  `alarm_control_panel.*`, every `person.*`, and as the outdoor temperature a temperature sensor with "outdoor",
  "outside" or "exterior" in its name.
- **security**: every camera; a camera is a doorbell when its name says so or its device has a doorbell event or
  sensor; every lock; entries are garage, gate and door covers plus door, window, garage-door, opening and gate
  binary sensors; hazards are smoke, gas, CO, moisture and safety sensors; `camera_for` pairs an entry with the
  camera on the same device, else in the same area. Cars are left out of all of it.
- **energy**: a power sensor named solar/pv/production, one named consumption/house/home/load/main panel/total,
  the day's energy sensors likewise, the twelve circuits drawing the most right now as consumers, the first
  flow-rate sensor as water flow and a daily water sensor as water today. Never the grid or battery keys: a sign
  convention cannot be read off a name.
- **music**: media players with device class `speaker`, less the exclusions above. When Music Assistant is there,
  each speaker is paired with its Music Assistant player by device name, for search, favourites, play and moving
  music between rooms.
- **doors**: a button named unlock (or any UniFi Access button) on the same device as a door contact is a door
  with a lock; shown with the locks, unlocking asks for Face ID.
- **remotes**: one per device with a `remote` entity or a television (`media_player` of device class `tv`),
  paired on the device: the remote's keys from the integration's own command names (Samsung, Android/Google TV,
  Roku, Apple TV, Sony Bravia; LG webOS through its button service), power and play/pause from the media
  player, its `source_list` as the inputs, its volume level where it reports one. A television known to two
  integrations is one remote, the core integration's. A remote's own power key is not used by default (on a Frame
  it toggles art mode): power is the media player's `turn_on`/`turn_off`. A receiver alone is not a remote.
- **cameras**: how each one streams comes from Home Assistant (HLS through the add-on for UniFi Protect, WebRTC
  for Nest); a WebRTC-only camera has no still. Clips come from the camera's media source when it has one
  (UniFi Protect). A UniFi doorbell's second "package" channel is not listed.

`atrium_auto` (MCP) shows the guesses as they stand, in this file's shape, so they can be copied and corrected.
None of the guesses picks a restored entity once Home Assistant has settled (see the rules below).

A version 1 file (rooms listed in full) still reads: its rooms become overrides that borrow what they list.

## Rules the add-on applies

- An entity that Home Assistant does not have is dropped and reported; the rest still renders.
- A **restored** entity (one Home Assistant registers but whose integration has not provided it since Home
  Assistant started: `restored: true`, state unavailable) is kept when the file names it, and reported once Home
  Assistant has been running ten minutes, since by then it is not coming back by itself. Rooms keep restored
  entities too (shown unavailable: a device that is off for the season is still the room's). What the add-on
  decides by itself, once those ten minutes have passed, never picks a restored entity for a slot that takes one
  (the doorbell, a thermostat, the solar sensor, the outdoor temperature, a room's readouts), and leaves out a
  restored entity that has a live twin: another entity of its domain, not restored, in the same area, with the
  same name or on another device of the same name, which is the same device back under a new id (only while its
  integration is loaded: an offline hub's entities are all restored, and say nothing). The area counts when both
  are known: two entities plainly named "Lamp" in different rooms are two lamps, one of them unplugged for the
  season; a restored entity with no area is matched house-wide. Before the ten minutes nothing is
  decided from restored entities, since integrations are still loading. A restored entity whose integration is
  loaded is **stale**: the set-up page lists those, with where to delete them in Home Assistant; the add-on never
  deletes anything itself.
- A room with nothing to show is not shown (it is offered back in the editor).
- Names come from the file's `names`, else Home Assistant's friendly name shortened for the room ("Kitchen Island
  lights" → "Island lights"); the in-app rename wins over both.
- Order: the owner's in-app order first, then the file's `rank`, then Home Assistant's (floors by level, rooms by
  name, Unassigned last).
- The in-app preferences (`/data/config.json`) are keyed by the same ids and apply on top: hidden rooms and
  entities, renames, moves to another room, order, the room's thermostat, floors of the owner's own. Changing
  the file never loses them.
- `tabs` in the preferences can still hide a tab that the file fills.

## Icons

`icon` is an SF Symbol name. The ones both apps draw: `fork.knife`, `fork.knife.circle`, `sofa`, `car`, `dumbbell`,
`desktopcomputer`, `shower`, `washer`, `cabinet`, `stairs`, `door.left.hand.open`, `gamecontroller`, `server.rack`,
`bed.double`, `tree`, `figure.pool.swim`, `square.split.bottomrightquarter` (the default).
