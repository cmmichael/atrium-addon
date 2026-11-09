# Changelog

Home Assistant shows this file on the add-on's Changelog tab and before an update. Newest first; one entry per
release of the add-on. Earlier releases (0.9.0 and before) are in the source repository's git history.

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
