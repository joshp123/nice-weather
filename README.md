---
written_by: AI (Claude Opus 5.5 and GPT-6.1 Sol), 2026-10-16, updated 2026-10-17 for v6
---

# Nice Weather

A map of the world that shows where the weather is nice: the next 14 days, every month and season of a typical year, by your own taste. Live at <https://nice.jjpcodes.com>. How a day is scored, and where it is wrong: <https://nice.jjpcodes.com/method>.

Click or search anywhere for the place's score and why. Every view has a shareable URL, such as `/lisbon-portugal/july` or `/@36.72,-4.42,7z/november`. See [AGENTS.md](AGENTS.md) for the product intent and engineering rules, and [.agent/v6-contract.md](.agent/v6-contract.md) for the data formats.

## Run it locally

```bash
devenv shell
npm install
npm run build
npm run serve          # http://127.0.0.1:5198
```

`npm run serve` runs the same Worker code as the live site, with `./artifacts` standing in for Sites storage (`artifacts/v6` holds the v6 files).

## Data

| What | Command | How often | Output |
| --- | --- | --- | --- |
| Typical year | `research/climate-v6/run.sh run` (after `/tmp/nwf-agents/overnight/fetch_era5.py <chunk> <processes>` downloads ERA5 hours to the SSD) | rarely | `v6/cells.bin`, `points.bin`, `climate/month-MM.bin`, `offsets-MM.png`, `facts.bin` |
| Default scores for link previews | `uv run --with numpy --with pillow python -I research/taste/default_scores.py artifacts/v6` | after the typical year | `v6/climate/scores.bin` |
| Preview images | `uv run --with playwright python -I research/taste/render_cards.py 150` (with `npm run serve` running) | after a release | `v6/cards/*.png` |
| Forecast | `go run ./cmd/niceweather forecast --out ./artifacts` | daily, ~10 min | legacy `forecast/<run>/` and `v6/forecast/<run>/days.bin` |
| Place names | `node hosting/sites/places.mjs .cache/geonames artifacts/places.tsv` | rarely | `places.tsv` from GeoNames cities |

Sources: forecast from ECMWF IFS open data (CC BY 4.0); typical year from ERA5 hourly (Copernicus), read from Earthmover's free public archive; place names from GeoNames (CC BY 4.0); base map from OpenFreeMap and OpenStreetMap.

## Daily cloud refresh

The Site’s linked **Refresh Nice Weather** automation runs every day at **08:00 UTC**. It downloads the static Linux executable from `/data/runner/niceweather-linux-amd64`, obtains a one-hour upload grant through the owner-only `begin_forecast_refresh` Site tool, and builds and publishes the forecast. Its saved instructions are in [hosting/sites/refresh.md](hosting/sites/refresh.md).

The daily path needs Sites compute and storage, ECMWF downloads and one executable. It installs no packages and uses no local server, Node, Go toolchain, GDAL or ecCodes. The automation prompt and repository contain no publisher credential. The former mini-server timer is disabled and its Nix module has been removed.

## Publish

```bash
go run ./cmd/niceweather forecast --out ./artifacts --publish https://nice.jjpcodes.com --token-stdin
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs files artifacts https://nice.jjpcodes.com v6 places.tsv
```

The forecast command reads its upload token from standard input with `--token-stdin`, or from `NICE_WEATHER_PUBLISH_TOKEN` when that flag is absent. It uploads the forecast first and its pointer last, so visitors never see a half-uploaded run. Old runs are then removed. To upload an existing completed run, use `niceweather publish --from artifacts/forecast/<run> --site https://nice.jjpcodes.com --token-stdin`.

The daily command needs one static executable and network access to ECMWF and the Site. It embeds its land mask and time-zone database and decodes ECMWF GRIB2 directly, including CCSDS packing. Build the Linux executable with:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o niceweather-linux-amd64 ./cmd/niceweather
```

When replacing the executable, upload it as `artifacts/runner/niceweather-linux-amd64` together with `hosting/sites/refresh.md` and `internal/providers/ifsgrib/LICENSE.libaec`, using the files publisher with the `runner` key.

GDAL and ecCodes are development tools, used to regenerate the embedded land mask (`go generate ./internal/dailyforecast`) and compare real GRIB messages (`go run ./cmd/compare-ifs-grib`). They are absent from the daily runtime. The app itself is deployed as a Sites Worker built by `hosting/sites/build.mjs`.
