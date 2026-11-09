# Changelog

Home Assistant shows this file on the add-on's Changelog tab and before an update. Newest first; one entry per
release of the add-on. Earlier releases (0.9.0 and before) are in the source repository's git history.

## 0.9.22

- `energy.tariff`: the electricity tariff (tiers of a rate per kWh over the billing period, a fixed charge, a tax)
  and the meter that counts the period's net kWh, so the apps show the bill so far, the tier the house is in and
  how far the next one is, what running the house costs per hour right now, and where the bill is heading.

## 0.9.21

- `security.doors[].access`: the door's access event (UniFi Access), found on the button's device or named in the
  file, so the app's "What happened" can say who came in and how (the logbook drops those attributes; the app
  reads the event's history).

## 0.9.20

- `security.rings`: the ring entity behind each doorbell camera (the device's doorbell event, whose state is the
  last ring's time, or its binary sensor), so the apps can tell a ring from the camera's own state. A ring now
  brings the door's camera up in the app, with the doors' unlock tiles under it.

## 0.9.19

- `routines` (version 3): `leaving` and `arriving`, each with `actions` (the Lock up and I'm home buttons) and
  `check` (entities to flag beyond the usual). The apps build the leaving check from what they already show,
  open entries and garages, unlocked locks and doors, lights on, the water valve closed, the cars' doors and
  windows, and show an arriving card when someone gets home.

## 0.9.18

- `GET /atrium/health`: how the add-on and its house are doing, in one answer: the version and any newer one,
  uptime, when Home Assistant last spoke and whether it is reachable, the tailnet's state and address, the
  layout (rooms, problems, stale entities, when it was read), the house (when it was built, whether it is a
  stale copy, the entities Home Assistant reports unavailable), alerts (key in, phones registered). The app shows
  it under Settings → House health.

## 0.9.17

- The button under an alert reads by what the action does, from a fixed set iOS needs registered in advance:
  Lock, Unlock, Close, Arm, Turn on, Turn off, Done (an input_boolean or a to-do item), Do it. The add-on's log
  names the button it chose for each alert, and the app's diagnostics name the category when one is opened.

## 0.9.16

- An alert whose action opens a way in (an unlock, a garage or a valve opening) gets its own button, which the
  app marks destructive and keeps behind Face ID; every alert button already asked for the phone to be unlocked.
  A cleared alert logs as "cleared alert <id>".

## 0.9.15

- Alerts on the phones, with the app open or not. A Home Assistant automation fires an `atrium_alert` event
  (title, body, level, the entity, one action such as lock or close, `live: true` for a Live Activity in the
  Dynamic Island and on the Lock Screen, `clear: true` to withdraw) and the add-on sends it through Apple to every
  phone that has signed in (`to:` narrows it to some logins). The owner's APNs key goes in the options
  (`apns_team_id`, `apns_key_id`, `apns_key`); without it, alerts are logged only. The apps register with
  `POST /atrium/devices`; `GET /atrium/alerts` lists the latest. See DOCS, "Alerts".

## 0.9.14

- Remotes show readings about the set: its network throughput and its power draw. `remotes[].readouts` names
  the sensors; without it, the add-on takes the data-rate and power sensors on the television's device and on
  any device sharing its MAC address (the UniFi Network integration's client device for the set, whose RX and TX
  sensors Home Assistant keeps disabled until enabled). `readouts: []` turns them off.
- The app's diagnostics come here and nowhere else: `POST /atrium/telemetry` takes an app's lines (when it
  launched and why, background and foreground, how long each connection took and why it dropped; nothing about
  the house), the add-on writes them to its log and keeps the latest per device for `GET /atrium/telemetry`
  (owner and administrators). Off in the app under Settings → Diagnostics.

## 0.9.13

- Atrium updates itself from the app, on every route. `GET /atrium/update` says whether a newer add-on is
  published and `POST /atrium/update` (owner and administrators) asks Home Assistant to install it, through the
  update entity Home Assistant keeps for every add-on (`update.install`). No new privileges: the add-on still
  asks the Supervisor for nothing beyond its own info. The apps show the card whenever Home Assistant knows of a
  newer version, not only when the app needs one.

## 0.9.12

- Water: a meter whose data arrives days late (a water company's portal integration) is no longer captioned
  "today". The version 3 water card takes `date`, a timestamp or date sensor for the day the `today` figure
  covers, and the apps caption the figure with that day ("Thursday", "yesterday"); without it, a figure whose
  sensor last changed before today reads "as of Sunday" rather than "today".

## 0.9.11

- `climate.thermostats[].action: setpoint` (version 3): for a thermostat that reports "heating" whenever its
  program is on (underfloor heating, some thermostatic valves), the apps count it as heating only while its
  target is above the current temperature, and as cooling only while below. Opt-in per entity; the default
  stays Home Assistant's `hvac_action` as reported. The add-on passes the key through to `house.json`; the web
  app in this release reads it, and the iOS app from its next build.

## 0.9.10

- A room with no tiles of its own still shows when the version 3 file puts an appliance, air readings, a panel
  or seasonal lights in it: a laundry room is its washer and dryer.

## 0.9.9

- The layout file's version 3 (`docs/mapping.md`, "Version 3"): the house beyond rooms. New sections, all
  optional and none guessed: what needs attention, the water card with a shut-off the apps may only open, air
  quality per room with bands, thermostats that serve several rooms, appliances, seasonal lights, vehicles, the
  pool, the day (calendars, drives, reminders, schedules) and panels for the one-offs. The add-on resolves them
  into `house.json`; the apps gain the screens in their next releases. A version 2 file reads unchanged.

## 0.9.8

- Widgets: `house.json` now carries `widgets`, what the app's Home Screen and Lock Screen widgets show. Without
  a `widgets:` section in `atrium.yaml`: Scenes, Home (alarm, solar, outdoor temperature, each room's lights) and
  one widget per room with lights or scenes. With one: exactly the items named, and what each size shows. The
  atrium-mapping skill and `mapping.md` describe it. App 0.9 (build of 2026-11-09) draws them.
- Hello carries the add-on's slug, so the app can offer an administrator the update from its Home screen.
- Televisions: a set known to two integrations is one remote when their devices share a MAC address, whatever
  each named it; a television's built-in Chromecast or its Music Assistant player no longer becomes a remote of
  its own (it can play to the set, not drive it).

## 0.9.7

- Steadier around restarts, after a review. Just after the add-on starts, before Tailscale has said who signed
  it in, a caller it does not know is told to try again rather than refused (a refusal made the app sign out
  and forget the server). While Home Assistant restarts: the last layout is served without a new attempt on
  every request, the list of administrators from before still holds, and the house's name is asked for off the
  lock, so one slow answer no longer stalls everyone. The Supervisor's watchdog restarts the add-on if its
  port stops answering.
- The port (8126) takes a Home Assistant token on trust for one minute instead of five, and tells the house's
  name only to someone it knows. A link to Atrium clicked on another site now opens the web app. A layout saved
  by two writers at once can no longer lose half a file.
- Web app: a dropped connection is noticed within a minute (a heartbeat) and the page reconnects; a socket
  that closes before sign-in no longer leaves "connecting" forever; a control that fails goes back to its
  previous state; the remote's buttons have spoken names; fewer tiles redraw on each reading.

## 0.9.6

- Review follow-ups. Two televisions with the same default device name in different rooms are two remotes, not
  one. A remote button or input whose `action` was left out is reported as that. On the set-up page, a slug the
  Supervisor answers differently for is named as such instead of "Atrium does not list itself".
- Web app: keys typed into the search box while a remote is open no longer go to the television; a remote action
  that fails puts the tile back the way it was; and when two energy terms are unknown the Energy flow says
  nothing rather than drawing flows from zeros. The iOS app gets the last two from its next release.
||||||| 159ffc5
## 0.9.5

- A device that is gone is only replaced by a live one in the same room: two entities plainly named "Lamp" in
  different rooms are two lamps, and the one unplugged for the season stays in its room, shown unavailable. A
  restored entity with no room is still matched house-wide. The atrium-mapping draft follows the same rule.

## 0.9.4

- Devices that are gone: ten minutes after Home Assistant starts, an entity it still registers but no longer
  has (`restored`) is never Atrium's own pick for the doorbell, a thermostat, the solar sensor or a room's
  readouts, and one that a live entity of the same name has replaced (with its integration loaded) drops out of
  the rooms and scenes. Other restored entities stay, shown unavailable, so an offline integration loses nothing.
- The set-up page lists the stale entities with where to delete them in Home Assistant. Atrium deletes nothing.
- A layout that names a restored entity is accepted and reported under problems, not refused.

## 0.9.3

- Energy with a battery: `grid_power` (positive while importing), `battery_power` (positive while discharging)
  and `battery_level` in `atrium.yaml`; any power key also takes `{entity: …, invert: true}` for a sensor whose
  sign runs the other way, and `energy.inverted` in house.json records it. The web app shows measured values as
  measured, derives at most one missing term from `solar + battery + grid = load`, and draws the battery between
  solar and home in the Energy flow. The iOS app does the same from its next release.
- The atrium-mapping skill's draft guesses the grid and battery keys from entity names, each marked `# check`;
  Atrium's own guess never fills them.

## 0.9.2

- An administrator can allow an app to write the layout from the set-up page: under **Apps that may write the
  layout**, press **Allow an app…**, pick it from the installed apps (or type its slug and check it) and
  confirm. Atrium writes the slug into its own options and applies it at once, no restart. Only through the Home
  Assistant sidebar, where the Supervisor vouches for the administrator.
- The set-up page no longer assumes a particular slug for the Claude Code app; it recognises it by name, as
  slugs differ per repository (`<repository-hash>_<name>`).

## 0.9.1

- An app listed under **Apps that may write the layout** can now actually write it. Atrium's port on the apps'
  network is IPv4-only, so the Supervisor can confirm which app is calling, and Atrium asks the Supervisor with
  `hassio_api` on every request.
- Every refusal says why, in the log and in the 403 answer: the request came over IPv6, no app is listed, the
  slug does not resolve, the caller is not the listed app, or the Supervisor does not confirm it (and with which
  status). DOCS.md gives the remedy for each.
