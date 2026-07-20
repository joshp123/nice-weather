---
written_by: ai
---

# Nice Weather Finder

Nice Weather Finder answers one question: where in Europe will the weather be warm, bright, dry and calm enough to enjoy?

The map shows 14 forecast days and 12 typical months. Select a day, choose **Nice** or **Cycling**, then click anywhere on land for the score and weather behind the colour. Switch the map between **Overall**, **Comfort**, **Dry**, **Bright** and **Calm** to see which part of the judgement drives a place. Day-by-day view can also show wind direction at the forecast source's exact available times.

## Data

- **Detailed forecast:** DWD ICON-EU at about 7 km, used while its forecast remains available.
- **Outlook:** ECMWF IFS at about 28 km, extending the forecast to day 14.
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
  --listen 127.0.0.1:5198
```

Open <http://127.0.0.1:5198/>. Useful checks are:

- <http://127.0.0.1:5198/manifest.json>
- <http://127.0.0.1:5198/places/current.json>
- `/releases/<release-id>/points/<checksummed-file>.nwp` from the manifest; point details use exact HTTP byte ranges from this immutable file

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

The updater downloads current ICON-EU and IFS fields, reads CHELSA through GDAL, rasterises the pinned Natural Earth land source, aggregates local daytime weather and scores every land cell with complete source evidence. It builds under `artifacts/.building`, validates the complete release, moves it into `artifacts/releases`, then atomically changes `artifacts/current`. A failed build leaves the last good release in place. Temporary download staging is removed when the command exits.

Each validated climate month is checkpointed transactionally. A provider outage can stop a run without discarding completed months; the next run revalidates and reuses only checkpoints produced by the current product contract. The pinned Natural Earth archive is also cached after its size, SHA-256 and members validate.

A running server notices when `artifacts/current` changes. It validates the new immutable release and swaps the whole in-memory snapshot between requests, so publication does not require a restart and a request cannot combine old and new data.

## Architecture

Go owns source retrieval, normalisation, scoring, release generation and HTTP serving. Every weather source passes through one path:

```text
source field -> DailyMetrics -> component scores -> profile score -> point record and PNG
```

Each detailed day and climate month has one compact point file containing the weather and evaluated Nice and Cycling scores behind the rendered Web Mercator texels. An IFS outlook day adds a dense display-score file: the browser reads the native 30-byte weather record for the facts and an 8-byte display record for the judgement visible on the map. The 14-day view reads one dense 44-byte summary record. Go remains the only scorer. The browser decodes stored weather and stored scores; it does not recalculate them. Forecast days also contain checked, compressed u/v wind frames. Overall and calm differ by profile; comfort, dryness and brightness are shared facts.

ICON and climate stay on their native product grids. IFS outlook scores and the 14-day judgement use an evaluated-score plane matching the detailed forecast's 1104×680 Europe grid. Go interpolates scalar scores before applying colour; the IFS source remains honestly labelled at about 28 km and the denser score plane does not claim finer weather evidence. Natural Earth supplies the target land mask, water stays transparent and a partial coastal neighbourhood falls back to its nearest valid score only when its missing source neighbours are water. Missing inland evidence stays transparent. The score PNG and selected display record use the same snapped geometry, so a selected place describes the visible texel. Every point, image and wind request is release-scoped and content-addressed. The manifest records the literal file hash, binary layout, dimensions and source metadata.

Place search is a separate static index. Publication installs immutable `places/releases/<id>` files and writes `places/current.json` last. The browser fetches one three-character shard for a query and verifies manifest and shard checksums. There is no runtime search endpoint.

React owns selection and progressive disclosure. MapLibre renders the map; the browser does not recalculate scores. If WebGL is unavailable, the product says so explicitly instead of presenting approximate geography.

Key packages:

```text
cmd/niceweather       CLI entry point
internal/providers    guarded DWD and IFS clients
internal/pipeline     decode, normalise, aggregate, mask and render
internal/weather      canonical metrics and the only scoring policy
internal/delivery     binary point files, manifest and atomic releases
internal/update       complete live release job
internal/appserver    static immutable files and release pointers
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

With the server running, inspect the manifest, an exact byte-range response from a referenced point file and the matching PNG. Then test the map in a real browser at desktop and phone widths: change day, profile and forecast/typical mode; search for a place; click the map; and check that the source and resolution change with the period.

New agents should read [AGENTS.md](AGENTS.md), [docs/project-context.md](docs/project-context.md) and [.agent/execplan-pending.md](.agent/execplan-pending.md) before editing.
