# The layout mapping: `atrium.yaml`

Atrium draws the rooms itself, from Home Assistant, on every build: one room per area, on its floor, holding the
lights, switches, shades, fans, locks, thermostat and speaker Home Assistant has in that area (directly or through
the device). Every scene is a chip. Add a device in Home Assistant and it is in Atrium a moment later; nobody
writes anything. Entities in no area go to an **Unassigned** room; rooms on no floor go to an **Elsewhere** floor.

`atrium.yaml` carries what that cannot know. Two kinds of thing:

- **Overrides** on the automatic rooms: hide a room or an entity, rank rooms and floors, rename, move a room to
  another floor, pick the thermostat, borrow an entity from elsewhere, drop a whole integration.
- **The custom screens**, which need judgment: the Home screen's readouts (weather, inside and outside
  temperature, alarm, people), **Security** (which cameras, which is the doorbell, which entries and hazards),
  **Energy** (solar, grid, the consumers worth watching, water) and **Music** (the speakers). Each is empty until
  the file fills it; an absent section means no tab.

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
    thermostat: climate.hall      # the thermostat to show here, when the automatic pick is wrong or elsewhere
    temperature: sensor.kitchen_temperature   # the room's readout; default: the area's own pick, else a room sensor there
    humidity: sensor.kitchen_humidity
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

scenes:                           # these chips first, in this order; every other scene follows by name
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

energy:                           # the Energy screen; absent = no Energy tab
  solar_power: sensor.pv_power                   # W or kW: the apps read the sensor's unit
  load_power: [sensor.house_power]               # W or kW; several are summed
  solar_today: sensor.pv_energy_today            # kWh, Wh or MWh, by the sensor's unit
  used_today: sensor.house_energy_today          # kWh
  consumers: [sensor.oven_power, sensor.ev_charger_power]   # W, the "Using now" list
  water_flow: sensor.water_flow                  # any unit
  water_today: sensor.water_today

music:                            # the Music screen; absent = no Music tab
  speakers: [media_player.kitchen, media_player.living_room]

names:                            # display names, where Home Assistant's are not right for a screen
  light.kitchen_ceiling: Ceiling
  sensor.oven_power: Oven
```

## What a room shows by itself

From the entities Home Assistant has in the area (not disabled, not hidden, not a config or diagnostic entity):
every `light`, `cover`, `fan` and `lock`; every `switch` except a kiosk's (browser_mod); a `climate` whose range
spans room temperature (so a fridge is not a thermostat); a `media_player` that is a speaker (device class
`speaker`, or listed under `music`). Locks, doors and covers from car integrations are left out. In that order,
then by name. The room's temperature readout is the area's own temperature entity, else a temperature sensor there
reading a plausible room temperature; humidity likewise.

A version 1 file (rooms listed in full) still reads: its rooms become overrides that borrow what they list.

## Rules the add-on applies

- An entity that Home Assistant does not have is dropped and reported; the rest still renders.
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
