# The layout mapping: `atrium.yaml`

Atrium's screens are driven by one declared layout, `atrium.yaml`, kept in the add-on's own folder:
`/addon_configs/df84f1fd_atrium/atrium.yaml` on the Home Assistant machine (the Terminal add-on sees that path; it
is part of the add-on's backups). Only that folder is visible to the add-on, not Home Assistant's configuration
or secrets. The add-on renders exactly what the file says, with
live names and states from Home Assistant, and the owner's in-app preferences (hide, rename, reorder, move) on
top. Nothing is guessed: a screen is empty until the mapping fills it.

The file is written by the **atrium-mapping** skill (`skills/atrium-mapping/`), run by a Claude with access to
the Home Assistant, or by hand. The add-on re-reads it whenever it changes, and reports problems (an entity that
does not exist, an unknown floor) in its log and on its set-up page.

Every value is a Home Assistant entity id unless said otherwise. Everything is optional except `rooms`; an absent
section leaves its screen or readout empty.

```yaml
version: 1
name: Cedar House                 # what to call the house; default: Home Assistant's name

floors:                           # in order, top of the list first; default: one floor, unnamed
  - id: ground                    # any id; Home Assistant's floor id when it is one
    name: Ground floor
  - id: upstairs
    name: Upstairs

rooms:                            # in order within their floor
  - id: kitchen                   # Home Assistant's area id when it is one (the in-app editor keys on it)
    name: Kitchen                 # default: the area's name
    floor: ground                 # a floor id above; default: the last floor, or an "Elsewhere" floor if none
    icon: fork.knife              # an SF Symbol name (the web app maps the ones below); default: guessed from the name
    thermostat: climate.hall      # the thermostat to show with this room, wherever Home Assistant put it
    temperature: sensor.kitchen_temperature
    humidity: sensor.kitchen_humidity
    entities:                     # what the room shows, in order: lights, switches, covers, fans, locks, speakers…
      - light.kitchen_ceiling
      - light.kitchen_island
      - switch.kettle
      - cover.kitchen_blind
      - media_player.kitchen

scenes:                           # the chips on the Home screen
  - entity: scene.movie
    name: "🎬 Film"               # default: the scene's name; a leading emoji becomes the chip's glyph
  - entity: script.goodnight

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
  solar_power: sensor.pv_power                   # W
  load_power: [sensor.house_power]               # W; several are summed
  solar_today: sensor.pv_energy_today            # kWh
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

## Rules the add-on applies

- An entity that Home Assistant does not have is dropped from the layout and reported; the rest still renders.
- A room with nothing left to show is not shown (it is offered back in the editor).
- Names come from the file's `names`, else Home Assistant's friendly name; the in-app rename wins over both.
- The in-app preferences (`/data/config.json`) are keyed by the same ids and apply on top: hidden rooms and
  entities, renames, moves to another room, order, the room's thermostat, floors of the owner's own. Changing
  the mapping never loses them.
- `tabs` in the preferences can still hide a tab that the mapping fills.

## Icons

`icon` is an SF Symbol name. The ones both apps draw: `fork.knife`, `fork.knife.circle`, `sofa`, `car`, `dumbbell`,
`desktopcomputer`, `shower`, `washer`, `cabinet`, `stairs`, `door.left.hand.open`, `gamecontroller`, `server.rack`,
`bed.double`, `tree`, `figure.pool.swim`, `square.split.bottomrightquarter` (the default).
