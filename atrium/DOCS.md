# Atrium

Puts Home Assistant's API and the Atrium web app on your tailnet for the Atrium app, and lets Tailscale say who is calling. With it the app
needs no Home Assistant password and holds no token: open the app with Tailscale on, and you are in.

## How it works

- The add-on joins your tailnet as its own machine (default name `atrium`), using Tailscale's `tsnet` library. Tailscale
  does not need to be installed on the Home Assistant host, and the host itself is not exposed.
- For every connection it asks Tailscale who is at the other end. The login that signed the add-on in is always allowed;
  others must be listed under `allow` or `people`. Tagged machines are never allowed.
- An allowed request is forwarded to Home Assistant with a credential that stays inside the add-on: by default the
  add-on's own Home Assistant access (everything the app does appears as done by the Supervisor user), or the long-lived
  token of a person's own user when you give one under `people`.
- Only `/api/…` (including the WebSocket) and `/local/…` are forwarded. The login pages are not, and neither is the
  Supervisor API (`/api/hassio…`).
- The add-on builds the app's layout: it reads Home Assistant's floors, areas, devices and entities, applies your
  configuration (below), and serves the result at `/atrium/house`. The app renders that; it decides nothing itself.
- The add-on serves the web version of Atrium too: at `https://atrium.<tailnet>.ts.net/` on the tailnet (signed in by
  Tailscale), and as **Atrium** in Home Assistant's sidebar, for Home Assistant's administrators (the panel acts
  with the add-on's own access, so until each person gets an access of their own, only administrators may use it).
- Requests that a browser marks as another site's page are refused, and so are requests for any name but the
  add-on's own, so a page open in a browser on an allowed person's device cannot use it.

Nothing inside Home Assistant is changed: no custom integration, no change to its sign-in.

## The layout and its configuration

The layout is always built fresh from Home Assistant: a new light in a room appears in the app by itself, a removed
one disappears, and one that comes back finds its old settings waiting. Your configuration is a small set of
overrides, kept in `/data/config.json` and read and written at `/atrium/config` (JSON, `GET` and `PUT`):

| Key | |
| --- | --- |
| `name` | What to call the house. |
| `tabs` | `{"security": false}` hides a tab (`security`, `energy`, `music`). |
| `floors` | By floor id: `name`, `order`. An id that is not in Home Assistant is a floor of your own, e.g. `outside`. |
| `rooms` | By area id: `hidden`, `name`, `floor`, `order`, `thermostat` (a climate entity that belongs with the room wherever Home Assistant put it), `entities` (the order; unlisted ones follow in the default order). |
| `entities` | By entity id: `hidden`, `name`, `room` (show it in another area), `show` (show something the defaults leave out). |

By default a room shows its lights, switches (except a kiosk's), shades and covers, fans, thermostats whose range spans
room temperature, locks, and speakers. If the house publishes the twin's `/local/house-twin/entities.json`, it refines
the choice; it is not required.

## Set up

1. Add the repository https://github.com/cmmichael/atrium-addon in Settings → Add-ons → Add-on Store → ⋮ →
   Repositories, install **Atrium**, start it, and open its **Log**. The first time it prints a Tailscale sign-in link: open it and
   approve the machine. (Or put a Tailscale auth key in `auth_key` before starting.)
2. On the phone or tablet, turn Tailscale on and open Atrium. It finds `atrium` on your tailnet by itself.
3. For the web version in Home Assistant's sidebar, turn on **Show in sidebar** on the add-on's page (administrators
   only). On the tailnet, open `https://atrium.<your-tailnet>.ts.net/` in any browser.

HTTPS: if HTTPS certificates are enabled for your tailnet (Tailscale admin console → DNS), the add-on also listens on
`https://atrium.<your-tailnet>.ts.net`. Otherwise it serves plain HTTP inside the tailnet's own encryption.

## Options

| Option | |
| --- | --- |
| `hostname` | The machine's name on the tailnet. The app looks for `atrium`; with another name, type the address in the app once. |
| `auth_key` | Optional Tailscale auth key, instead of the sign-in link. A key that applies a tag leaves the machine without an owner, so list who may use it. |
| `allow` | Tailscale logins (e-mail addresses) that may use Atrium, besides the one that signed it in. |
| `people` | The same, each with an optional `token`: a long-lived access token created in that person's Home Assistant profile, so their actions are their own in Home Assistant. |
| `admins` | Logins that may change the layout configuration for everyone, besides the one that signed the add-on in. Needed when the machine is tagged (no owner). |

## Who can do what

Anyone allowed here can do what the credential behind them can: by default that is the add-on's access, which is an
administrator's. Use `people` with tokens of non-administrator users to limit someone. Your tailnet's access rules decide
which devices can reach this machine at all.

To take access away: remove the login here, or remove the device from your tailnet.
