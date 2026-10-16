---
written_by: AI (Claude Opus 5.5), 2026-10-16
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

## Publish

```bash
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs forecast artifacts https://nice.jjpcodes.com
NICE_WEATHER_PUBLISH_TOKEN=… node hosting/sites/publish.mjs files artifacts https://nice.jjpcodes.com climate places.tsv
```

The forecast is uploaded first and its pointer last, so visitors never see a half-uploaded run. Old runs are then removed. The app itself is deployed as a Sites Worker built by `hosting/sites/build.mjs`.
