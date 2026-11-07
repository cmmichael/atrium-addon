# Atrium for Home Assistant

Your house in the Atrium app and on the web, over your tailnet, signed in by Tailscale. No passwords, no tokens, no
port forwarding; nothing to type.

[![Add to Home Assistant](https://my.home-assistant.io/badges/supervisor_addon.svg)](https://my.home-assistant.io/redirect/supervisor_addon/?addon=df84f1fd_atrium&repository_url=https%3A%2F%2Fgithub.com%2Fcmmichael%2Fatrium-addon)

1. Press the button. Your Home Assistant opens Atrium's page (adding this repository first): press **Install**, then
   **Start**.
2. Press **Open Web UI**. Atrium asks you to **Sign in to Tailscale**, once, and then shows your house and its address
   on the tailnet.
3. On your iPhone or iPad, turn Tailscale on and open Atrium. It finds the house by itself.

Turn on **Show in sidebar** to keep the set-up page in Home Assistant's sidebar. Everything else, including who may
use it, is in [atrium/DOCS.md](atrium/DOCS.md).

The layout is a file, `/config/atrium.yaml`, written for your house by the
[atrium-mapping skill](skills/atrium-mapping/SKILL.md): point a Claude that can reach your Home Assistant at it.

Starting over? In Home Assistant's Terminal, `curl -fsSL https://raw.githubusercontent.com/cmmichael/atrium-addon/main/reset.sh | sh`
removes Atrium completely (settings and Tailscale sign-in included); then press the button again.

Published from the Atrium repository by `tools/publish-addon.sh`; the source of truth is there. Current version:
**0.5.2** (atrium@1c0c3fc).
