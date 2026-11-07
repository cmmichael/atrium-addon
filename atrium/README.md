# Atrium (Home Assistant add-on)

See [DOCS.md](DOCS.md). Install it from the add-on repository https://github.com/cmmichael/atrium-addon (Settings →
Add-ons → Add-on Store → ⋮ → Repositories), where `tools/publish-addon.sh` publishes this folder on each release.
Without a repository, copy this folder into the Supervisor's local add-ons folder (`/addons` on most installs;
`/local_apps` on some), then Add-on Store → ⋮ → Check for updates: it appears under Local add-ons.

Developing: `docker run --rm -v "$PWD/server":/src -w /src golang:1.27 go test ./...`. The `web/` folder here is the
built web app (`tools/build-web.sh` from the repository root); rebuild it before tagging a release.
