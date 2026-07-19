---
written_by: ai
---

# Nice Weather Finder

Nice Weather Finder answers one question: where in Europe will the weather be warm, bright, dry and calm enough to enjoy?

The map shows 14 forecast days and 12 typical months. Select a day, choose **Nice** or **Cycling**, then click anywhere on land for the score and weather behind the colour. Switch the map between **Overall**, **Comfort**, **Dry**, **Bright** and **Calm** to see which part of the judgement drives a place. Day-by-day view can also show wind direction at the forecast source's exact available times.

## Data

- **Detailed forecast:** DWD ICON-EU at about 7 km, used while its forecast remains available.
- **Outlook:** ECMWF AIFS at about 28 km, extending the forecast to day 14.
- **Typical month:** CHELSA 2.1 monthly climate normals for 1981–2010. The source is about 1 km; this product resamples it to an honest 0.1° grid, about 11 km across Europe. This view scores a monthly average, not the chance of getting a nice day; the interface says so plainly.
- **Land:** Natural Earth 5.1.1 land polygons at 1:10 million scale. Forecast cells use all land touched by a cell; climate requires complete CHELSA evidence and treats coastline-only cells as best effort.

Forecast values describe the local daytime window. Source, forecast detail and resolution are shown for the selected place. A 16°C day is deliberately treated as mediocre: this is a warm-weather finder, not a generic forecast map.

## Run locally

Enter the development shell, install the frontend packages if needed, then build a sample release:

```bash
devenv shell
npm install
npm run build
go run ./cmd/niceweather fixture --out ./artifacts
go run ./cmd/niceweather serve \
  --web ./dist \
  --artifacts ./artifacts \
  --listen 127.0.0.1:5173
```

Open <http://127.0.0.1:5173/>. Useful checks are:

- <http://127.0.0.1:5173/manifest.json>
- <http://127.0.0.1:5173/places/current.json>
- `http://127.0.0.1:5173/api/point?release=<release-id>&period=<period-id>&profile=nice&lat=52.37&lon=4.90`

The fixture is deterministic sample data. It exercises all 14 forecast days, all 12 climate months, both profiles and a seven-place search index through the production artifact schemas. The manifest sets `dataStatus` to `sample`, so the interface cannot mistake it for current weather; a production update sets it to `live`.

## Build live weather

First check the current upstream files and their units without publishing:

```bash
go run ./cmd/niceweather update \
  --cache ./.cache/weather \
  --out ./artifacts \
  --smoke
```

Then build and atomically publish a complete live release:

```bash
go run ./cmd/niceweather update \
  --cache ./.cache/weather \
  --out ./artifacts
```

Build and publish the Europe place index from a pinned GeoNames extract:

```bash
go run ./cmd/niceweather places \
  --source ./.cache/geonames/2026-07-18 \
  --source-date 2026-07-18 \
  --out ./artifacts
```

The updater downloads current ICON-EU and AIFS fields, reads CHELSA through GDAL, rasterises the pinned Natural Earth land source, aggregates local daytime weather and scores every land cell with complete source evidence. It builds under `artifacts/.building`, validates the complete release, moves it into `artifacts/releases`, then atomically changes `artifacts/current`. A failed build leaves the last good release in place. Temporary download staging is removed when the command exits.

Each validated climate month is checkpointed transactionally. A provider outage can stop a run without discarding completed months; the next run revalidates and reuses only checkpoints produced by the current product contract. The pinned Natural Earth archive is also cached after its size, SHA-256 and members validate.

A running server notices when `artifacts/current` changes. It validates the new immutable release and swaps the whole in-memory snapshot between requests, so publication does not require a restart and a request cannot combine old and new data.

## Architecture

Go owns source retrieval, normalisation, scoring, release generation and HTTP serving. Every weather source passes through one path:

```text
source field -> DailyMetrics -> component scores -> profile score -> grid and PNG
```

Daily forecasts and climate months contain compact EPSG:4326 binary grids for constant-time point lookup, two overall Web Mercator PNGs and five component PNGs. Forecast days also contain checked, compressed u/v wind frames. The forecast window has its own evaluated range grid and map image per profile. Overall and calm differ by profile; comfort, dryness and brightness are shared facts. PNG pixels hold the canonical 0–100 judgement through a continuous colour ramp, which MapLibre interpolates without inventing another scoring model. The manifest binds every asset to its source, projection, hash, dimensions and resolution. The browser requests release-scoped, checksummed assets, so a tab cannot mix data from different publications.

Place search is a separate static index. Publication installs immutable `places/releases/<id>` files and writes `places/current.json` last. The browser fetches one three-character shard for a query and verifies manifest and shard checksums. There is no runtime search endpoint.

React owns selection and progressive disclosure. MapLibre renders the map; the browser does not recalculate scores. If WebGL is unavailable, the product says so explicitly instead of presenting approximate geography.

Key packages:

```text
cmd/niceweather       CLI entry point
internal/providers    guarded DWD and AIFS clients
internal/pipeline     decode, normalise, aggregate, mask and render
internal/weather      canonical metrics and the only scoring policy
internal/delivery     binary grids, manifest and atomic releases
internal/update       complete live release job
internal/appserver    static files and point lookup
internal/places       GeoNames index build, validation and publication
src                   React and MapLibre interface
```

Generated weather belongs in `artifacts`; the Vite build belongs in `dist`. Neither is source code.

## Verify

Run checks sequentially because parallel development shells can race while loading the environment:

```bash
go test ./...
go vet ./...
npm run test
npm run build
```

With the server running, inspect the manifest, a point response and the referenced PNG. Validate the referenced grid on disk. Then test the map in a real browser at desktop and phone widths: change day, profile and forecast/typical mode; search for a place; click the map; and check that the source and resolution change with the period.

New agents should read [AGENTS.md](AGENTS.md), [docs/project-context.md](docs/project-context.md) and [.agent/execplan-pending.md](.agent/execplan-pending.md) before editing.
