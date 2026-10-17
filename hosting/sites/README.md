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

The typical-day update also requires regenerated `v6/climate/scores.bin`, all 133 climate score PNGs (months, consecutive runs and year), and all 15 score PNGs for the current forecast (the fortnight and its 14 days). Keep these together with the app build: older images describe average days. Visible score numbers and score-image bytes use the floor of the continuous typical-day score, so rounding cannot promote a place into the next band. Day counts round to the nearest whole day.

The generated files are supplied separately under `/tmp`; published climate data is not changed by local verification. To inspect that complete set without copying it into `artifacts/`, use an overlay root containing `v6/climate/` and `v6/forecast/<run>/`:

```sh
NICE_WEATHER_GENERATED_DATA=/tmp/generated-weather-data devenv shell -- npm run serve
```

Deployment is blocked until the forecast owner updates `internal/dailyforecast/v6_scores.go`. That future score-image writer still uses the mean and bilinear cell interpolation; it must use the same typical-day rule, selected fine point's owning cell and floored score bytes before the next daily forecast is published. This change does not edit that owner's files.

Link-preview PNGs live under `v6/cards/typical-day/`. Render them with `research/taste/render_cards.py --out <overlay>/v6/cards/typical-day`; its positional argument is the number of largest named places. `--periods` accepts arbitrary runs. Preview text always includes the selected place's current default-taste judgement and counts. If that exact named place and period has no new PNG, the link uses a text preview. Coordinate links also use text previews, since a nearby town's image could describe another point. Legacy mean-score PNGs are not reused.
