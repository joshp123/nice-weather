Written by AI (GPT-6.1 Sol), 2026-10-17.

# Sites hosting

Nice Weather is public at <https://nice.jjpcodes.com>. The project and storage binding are in [.openai/hosting.json](.openai/hosting.json). Sites hosts the Worker, its embedded Vite build and the weather files. No paid hosting service is used.

Build and package from a clean checkout of the committed product:

```sh
devenv shell -- npm run build
devenv shell -- node hosting/sites/build.mjs dist /path/to/sites-source-checkout
```

Use the Sites skill's source helper to open the existing Site source, commit and upload the generated Worker, and package its `dist`. Save and deploy that exact source revision, preserving the public audience.

The Worker serves the app, place search, place details and link previews. `/data/` reads Sites storage. Place paths are `/country/city[/period]`; unknown paths return HTTP 404.

The daily [forecast runner](refresh.md) writes only `v6/forecast/`. Its temporary owner-issued grant can upload that prefix and prune forecast runs other than the current and previous runs. Data files upload before `current.json`.

For climate, places and runner uploads:

```sh
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs files artifacts https://nice.jjpcodes.com v6 places.tsv
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs files artifacts https://nice.jjpcodes.com runner
```

The publisher credential is supplied privately through the environment at use time. Never store it in the repository or a command argument. The Worker's owner-only `DELETE /__publish/<prefix>` removes objects under a storage prefix and reports the removed file and byte counts; forecast grants cannot delete arbitrary prefixes.

`npm run serve` runs this Worker locally on port 5198, with `artifacts/` as storage and `dist/` as web assets. Local serving does not delete forecast files.
