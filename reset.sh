#!/bin/sh
# Removes Atrium from this Home Assistant completely, for a fresh install: the app (whichever way it was
# installed), its settings and its Tailscale sign-in, and any copy in the local apps folder.
# Run it in Home Assistant's Terminal (the Terminal & SSH app), where the `ha` command is:
#
#     curl -fsSL https://raw.githubusercontent.com/cmmichael/atrium-addon/main/reset.sh | sh
#
# Then install Atrium again with the button on https://github.com/cmmichael/atrium-addon.
set -u
for slug in df84f1fd_atrium local_atrium local_atrium_gateway; do
  if ha apps info "$slug" >/dev/null 2>&1; then
    echo "Removing $slug…"
    ha apps stop "$slug" >/dev/null 2>&1
    ha apps uninstall "$slug" >/dev/null && echo "removed $slug"
  fi
done
for d in /addons/atrium /addons/atrium_gateway /local_apps/atrium /local_apps/atrium_gateway; do
  if [ -d "$d" ]; then
    echo "Removing $d…"
    rm -rf "$d"
  fi
done
ha store reload >/dev/null 2>&1
echo
echo "Atrium is gone from Home Assistant, with its settings and Tailscale sign-in."
echo "In the Tailscale admin console, remove the machine \"atrium\" so the next install gets that name again."
echo "Then install it again from https://github.com/cmmichael/atrium-addon"
