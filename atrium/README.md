# Atrium (Home Assistant add-on)

See [DOCS.md](DOCS.md). Install it with the **Add to Home Assistant** button on https://github.com/cmmichael/atrium-addon
(a my.home-assistant.io link that opens the add-on's page in your own Home Assistant, adding the repository first),
where `tools/publish-addon.sh` publishes this folder on each release.
Without a repository, copy this folder into the Supervisor's local add-ons folder (`/addons` on most installs;
`/local_apps` on some), then Settings → Apps → App Store → ⋮ → Check for updates: it appears under Local apps.

Developing: `docker run --rm -v "$PWD/server":/src -w /src golang:1.27 go test ./...`. The `web/` folder here is the
built web app (`tools/build-web.sh` from the repository root); rebuild it before tagging a release.
