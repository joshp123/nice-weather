---
written_by: AI (Claude Opus 5.5 and GPT-6.1 Sol), 2026-10-16, updated 2026-10-17
---

# Nice Weather

A map of the world that shows where the weather is nice: the next 14 days, every month and season of a typical year, by your own taste. Live at <https://nice.jjpcodes.com>. The scoring curves, sources and credits are at <https://nice.jjpcodes.com/method>.

Click or search anywhere for the place's score and why. Share a view such as `/portugal/lisbon/july` or `/@36.72,-4.42,7z/november` with its own link preview, or copy an image of the map.

## Run locally

```sh
devenv shell
npm install
npm run build
npm run serve          # http://127.0.0.1:5198
```

`src/` is the Vite app. `hosting/sites/worker.mjs` serves the app, search, place details and link previews. Local serving runs that same Worker, with `artifacts/` standing in for Sites storage. All weather data the app reads is under `artifacts/v6/`.

## Data pipeline

| What | Command | Output |
| --- | --- | --- |
| Typical year | `research/climate-v6/run.sh run`, after `research/era5/fetch_era5.py <chunk> <processes>` downloads ERA5 hours | `v6/cells.bin`, `points.bin`, monthly day records, temperature offsets and place facts |
| Default scores and map images | `uv run --with numpy --with pillow python -I src/engine/default_scores.py artifacts/v6 --images artifacts/v6/climate` | `v6/climate/scores.bin` and 133 score PNGs |
| Preview cards | `uv run --with playwright python -I research/taste/render_cards.py 150`, with the local server running | `v6/cards/` |
| Daily forecast and outlooks | `go run ./cmd/niceweather forecast --out ./artifacts` | `v6/forecast/<run>/days.bin`, 15 score PNGs, three outlook manifests and six posters, then `v6/forecast/current.json` |
| Place names | `node hosting/sites/places.mjs .cache/geonames artifacts/places.tsv` | GeoNames names, regions, coordinates and search aliases |
| Map labels | `python research/site/labels.py artifacts/places.tsv artifacts/v6` | `v6/labels-1.json` and `labels-2.json` |

The scoring reference is [research/taste/nice_v6.py](research/taste/nice_v6.py), implemented in the browser by `src/engine/taste.ts` and the forecast runner by `internal/dailyforecast/v6_scores.go`. [Fable's calibration](research/taste/fable-results.md) explains the rule and its anchor places. The methodology charts use `research/site/scoring-explainer-data.mjs`. See [research/README.md](research/README.md) for the retained pipeline tools.

Sources: ECMWF IFS open data (CC BY 4.0); ERA5 hourly from Copernicus, read from Earthmover's free public archive; GeoNames (CC BY 4.0); OpenFreeMap and OpenStreetMap. The [clear-sky calibration](internal/clearsky/README.md) documents the runner’s solar model. Bundled third-party decoder and clear-sky notices remain beside their code.

## Daily cloud refresh

The Site's linked **Refresh Nice Weather** automation runs at **08:00 UTC**. It downloads the static Linux executable from `/data/runner/niceweather-linux-amd64`, obtains a one-hour upload grant through the owner-only `begin_forecast_refresh` Site tool, and runs:

```sh
niceweather forecast --out ./artifacts --publish https://nice.jjpcodes.com --token-stdin
```

Its [instructions](hosting/sites/refresh.md) contain no credentials. The program reads its upload token from private standard input, downloads ECMWF once, builds 14 local days, uploads the v6 files, and writes the pointer last. The current and previous runs are retained. To publish an already completed run, use `niceweather publish --from artifacts/v6/forecast/<run> --site https://nice.jjpcodes.com --token-stdin`.

No packages, Go toolchain, Node, GDAL or ecCodes are needed by the daily cloud job. The executable includes its time zones, clear-sky model and GRIB2/CCSDS decoder. Build it inside `devenv shell`:

```sh
mkdir -p artifacts/runner
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o artifacts/runner/niceweather-linux-amd64 ./cmd/niceweather
cp hosting/sites/refresh.md artifacts/runner/refresh.md
cp internal/providers/ifsgrib/LICENSE.libaec artifacts/runner/LICENSE.libaec
cp internal/clearsky/LICENSE.pvlib artifacts/runner/LICENSE.pvlib
cp outlook/assets/OFL.txt artifacts/runner/LICENSE.Barlow
```

Upload the executable under `runner/niceweather-linux-amd64`, with the refresh instructions and three notices from that build recipe. [hosting/sites/README.md](hosting/sites/README.md) describes app packaging and data uploads. The local Go checks are `go build ./... && go test ./...`, inside `devenv shell`.

For a local check without publishing, build a native executable and run `niceweather forecast --out /tmp/weather-forecast-check`. Its fourteen daily images use each selected land point's owning weather cell and temperature correction. The fortnight image describes the typical day, using the same threshold median as the app. Score bytes are floored. The runner writes no climate scores or place facts.

`/outlook/world`, `/outlook/europe` and `/outlook/usa` provide tomorrow's ready-to-post sentence, downloadable poster and dated share link. The runner makes these before publication; no social account is used. To render an existing forecast without downloading weather again, run `go run ./tools/outlook --out /tmp/weather-outlook-check`. See [outlook/README.md](outlook/README.md) for selection, styling and posting options.
