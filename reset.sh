#!/bin/sh
# Removes Atrium from this Home Assistant completely, for a fresh install: the app (whichever way it was
# installed), its settings, its Tailscale sign-in, its layout file, and any copy in the local apps folder.
# To keep the layout (atrium.yaml) for the next install: KEEP_LAYOUT=1 sh reset.sh
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
# Uninstalling keeps the app's own config folder (that is what it is for), so the layout file is removed here.
if [ -d /addon_configs/df84f1fd_atrium ]; then
  if [ -n "${KEEP_LAYOUT:-}" ]; then
    echo "Keeping the layout in /addon_configs/df84f1fd_atrium"
  else
    echo "Removing the layout (/addon_configs/df84f1fd_atrium)…"
    rm -rf /addon_configs/df84f1fd_atrium
  fi
fi
ha store reload >/dev/null 2>&1
echo
echo "Atrium is gone from Home Assistant, with its settings, layout and Tailscale sign-in."
echo "In the Tailscale admin console, remove the machine \"atrium\" so the next install gets that name again."
echo "Then install it again from https://github.com/cmmichael/atrium-addon"
