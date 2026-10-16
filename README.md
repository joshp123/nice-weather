---
written_by: AI (Claude Opus 5.5 and GPT-6.1 Sol), 2026-10-16
---

# Nice Weather Finder

A map of the world that shows where the weather is nice: the next 14 days, and every month of a typical year. Live at <https://nice.jjpcodes.com>.

Click anywhere to see the place's name, its rating and why: temperature, humidity, rain, cloud and wind in real units. Switch to **Cycling** for stricter wind and rain. See [AGENTS.md](AGENTS.md) for the product intent and engineering rules.

## Run it locally

```bash
devenv shell
npm install
npm run build
npm run serve          # http://127.0.0.1:5198
```

`npm run serve` runs the same Worker code as the live site, with `./artifacts` standing in for Sites storage.

## Data

| What | Command | How often | Output |
| --- | --- | --- | --- |
| Forecast | `go run ./cmd/niceweather forecast --out ./artifacts` | daily, ~4 min | `forecast/<run>/`: one PNG per day and layer, one points file per day |
| Typical year | `go run ./cmd/niceweather climate-convert --from <ERA5 product> --out ./artifacts` | rarely | `climate/`: one PNG per month and layer, one points file per month |
| Place names | `node hosting/sites/places.mjs .cache/geonames artifacts/places.tsv` | rarely | `places.tsv` from GeoNames cities |

Every PNG is an 8-bit greyscale map of the world on ECMWF's 0.25° grid (1440 × 721): 0–100 is a score or a share of nice days; 255 is sea or no data. Open one to see it. Each points file holds one fixed-size record per grid cell; its layout is in the matching `manifest.json` / `current.json`.

Sources: forecast from ECMWF IFS open data (CC BY 4.0); typical year from ERA5 2006–2025 (Copernicus); place names from GeoNames (CC BY 4.0); base map from OpenFreeMap and OpenStreetMap.

## Daily cloud refresh

The Site’s linked **Refresh Nice Weather** automation runs every day at **08:00 UTC**. It downloads the static Linux executable from `/data/runner/niceweather-linux-amd64`, obtains a one-hour upload grant through the owner-only `begin_forecast_refresh` Site tool, and builds and publishes the forecast. Its saved instructions are in [hosting/sites/refresh.md](hosting/sites/refresh.md).

The daily path needs Sites compute and storage, ECMWF downloads and one executable. It installs no packages and uses no local server, Node, Go toolchain, GDAL or ecCodes. The automation prompt and repository contain no publisher credential. The former mini-server timer is disabled and its Nix module has been removed.

## Publish

```bash
go run ./cmd/niceweather forecast --out ./artifacts --publish https://nice.jjpcodes.com --token-stdin
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs files artifacts https://nice.jjpcodes.com climate places.tsv
```

The forecast command reads its upload token from standard input with `--token-stdin`, or from `NICE_WEATHER_PUBLISH_TOKEN` when that flag is absent. It uploads the forecast first and its pointer last, so visitors never see a half-uploaded run. Old runs are then removed. To upload an existing completed run, use `niceweather publish --from artifacts/forecast/<run> --site https://nice.jjpcodes.com --token-stdin`.

The daily command needs one static executable and network access to ECMWF and the Site. It embeds its land mask and time-zone database and decodes ECMWF GRIB2 directly, including CCSDS packing. Build the Linux executable with:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o niceweather-linux-amd64 ./cmd/niceweather
```

When replacing the executable, upload it as `artifacts/runner/niceweather-linux-amd64` together with `hosting/sites/refresh.md` and `internal/providers/ifsgrib/LICENSE.libaec`, using the files publisher with the `runner` key.

GDAL and ecCodes are development tools, used to regenerate the embedded land mask (`go generate ./internal/dailyforecast`) and compare real GRIB messages (`go run ./cmd/compare-ifs-grib`). They are absent from the daily runtime. The app itself is deployed as a Sites Worker built by `hosting/sites/build.mjs`.
