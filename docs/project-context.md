---
written_by: ai
---

# Project context

This is the short technical handoff for Nice Weather Finder. `AGENTS.md` sets the project rules; `.agent/execplan-pending.md` records implementation and review evidence.

## Product decision

The product is a map for choosing where to go, not a weather dashboard. It is Europe-first and tuned for escaping Dutch grey and cold weather.

“Nice” means comfortably warm, bright, dry and not too windy. “Cycling” makes rain, sustained wind and gusts more costly. The warm bias is intentional: 22–27°C is ideal and 16°C cannot score above Mixed.

The permanent interface stays small:

- a full-screen geographic map;
- a compact bottom dock for day, profile and forecast/typical mode;
- place search and current location;
- a point panel, opened on demand, that explains the score and data source.

Map colours use five stable judgements: Poor, Mixed, Good, Very good and Excellent. The map must remain recognisable; weather colour must not hide geography.

## Current data contract

The live release contains 14 complete local forecast days and 12 climate months:

| Period | Source | Product grid | Time step | Label |
| --- | --- | ---: | ---: | --- |
| Detailed forecast | DWD ICON-EU | about 7 km | hourly, then three-hourly | Detailed forecast |
| Outlook | ECMWF AIFS | about 28 km | 6 hours | Outlook |
| Typical month | CHELSA 2.1, 1981–2010 | about 11 km | monthly normal | Typical month · 1981–2010 |

ICON-EU is used only for days it covers. AIFS continues the same 14-day product without pretending that its native resolution is finer. CHELSA source rasters are about 1 km, but climate delivery is deliberately resampled to 0.1° and reported as about 11 km across Europe. Monthly precipitation is used as a daily average; CHELSA does not provide wet-time frequency, so that evidence remains absent rather than being invented.

Natural Earth 5.1.1 land polygons at 1:10 million scale are the explicit land authority. Forecast masks use all-touched rasterisation at the provider grid. Climate uses two masks: centre-on-land and coastline-touch-only cells are published only when all eight CHELSA fields are present. A missing field leaves either kind of cell transparent; no weather value is filled or inferred. The build rejects a month when missing centre support exceeds 2%.

Forecast cells aggregate the local 10:00–20:00 window. A timezone lookup is performed for each grid cell; UTC is used where no land timezone is found. The climate layer describes typical conditions, not a forecast.

## One scoring path

All providers translate their fields to `weather.DailyMetrics`. Optional evidence is explicit. Missing solar, cloud, gust, wet-time, humidity or dew-point values are not silently turned into zeros.

```text
provider fields
    -> normalised forecast intervals or monthly climate values
    -> weather.DailyMetrics
    -> weather.Score(profile)
    -> delivery records and profile PNGs
```

Map pixels and point details use the same score. The frontend only selects a period and profile; client-side scoring is forbidden.

The score has five explainable components: warmth, dryness, sun, wind and comfort. Deal-breaker rules stop cold, very wet or very windy days from winning through averages alone.

## Backend boundaries

- `internal/providers/dwd` discovers complete ICON-EU runs and downloads bounded, validated bzip2/GRIB files.
- `internal/providers/aifs` reads ECMWF indexes, retrieves exact byte ranges and validates complete GRIB2 messages.
- `internal/pipeline` decodes through ecCodes/GDAL, normalises units and accumulations, aggregates local days, applies the land mask and renders output.
- `internal/weather` owns canonical facts, validation, scoring, ratings and deterministic summaries.
- `internal/delivery` owns the manifest, compact binary grid, image metadata, validation and atomic `current`/`previous` publication.
- `internal/update` plans 14 days, owns the pinned Natural Earth land source, builds all 12 CHELSA months and publishes one complete release.
- `internal/appserver` serves one immutable release snapshot, detects publication changes and swaps to a fully validated replacement between requests. Point lookups remain constant time.

There is no provider fallback soup, live tile rendering, client-side score policy or compatibility layer for the discarded prototype.

## Artifact contract

Runtime output is separate from the frontend build:

```text
dist/                         Vite application
artifacts/current/manifest.json
artifacts/current/images/     one map PNG per period and profile
artifacts/current/grids/      one compact point-lookup grid per period
artifacts/.building/           unpublished release under construction
artifacts/releases/           validated release directories
.cache/weather/staging/       bounded, temporary update work
.cache/weather/land/          validated Natural Earth source cache
.cache/weather/climate/       validated monthly climate checkpoints
```

A live update starts in `artifacts/.building`. After the manifest, all 26 periods, both profile images for every period, grids, hashes and dimensions validate, the updater atomically moves that directory into `artifacts/releases` and changes `artifacts/current`. The running server detects the new target, validates and opens the complete release, then swaps its immutable snapshot between requests. No restart is required, and an in-flight request continues to use one coherent release.

Manifest `dataStatus` is a required `live` or `sample` value. The fixture publishes `sample`; the updater publishes `live`. The interface shows a clear sample-data warning. Product contract `nice-weather-v5` binds scoring, masks, grid and display projection semantics. Image requests include both the release ID and image SHA-256; the server exposes only the current and published previous release.

Public runtime routes:

```text
GET /                         React application
GET /manifest.json            release catalogue
GET /releases/<id>/images/... immutable score overlay for that release
GET /api/point?...            score and weather at a coordinate
GET /api/search?q=...         small local European place index
```

## Frontend boundaries

React owns product state, controls, loading and disclosure. MapLibre owns geographic interaction and compositing. The overlay is a single Web Mercator image source whose release-scoped URL changes without rebuilding the map. The old raster is removed before a new selection loads and remains hidden until the exact new source has rendered; failed or stale assets cannot remain under new controls.

A DOM/SVG fallback uses the same Web Mercator geometry. It preserves a recognisable, clickable map when WebGL is unavailable and loads its weather image only when it becomes the active renderer.

The app must work at phone width and on a 57-inch display. The dock is width-capped; the map keeps the remaining space. Avoid permanent lists, cards inside cards, alternate control styles and raw model jargon in the primary interface.

## Commands

```bash
# Deterministic sample release
go run ./cmd/niceweather fixture --out ./artifacts

# Verify upstream data, metadata and units without publishing
go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts --smoke

# Build and publish current live weather
go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts

# Serve the built frontend and current weather release
go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5173
```

Local product URL: <http://127.0.0.1:5173/>

## Verification order

Check boundaries before judging the full map:

1. Run `go test ./...` and deterministic scoring cases.
2. Inspect update logs, `manifest.json`, period counts, source metadata and resolutions.
3. Verify binary-grid lookup against a known record on disk and inspect exact PNG pixels, including transparent sea.
4. Run `go vet ./...`, `npm run test` and `npm run build`.
5. Start the Go server and fetch the manifest, a point response and every referenced asset type.
6. In the internal browser and Chrome or Safari, change day, profile and basis; search; click the map; and inspect desktop and mobile layouts.
7. Record manifest latency, score-layer readiness, transferred bytes, long tasks, frame timing, JavaScript heap and browser errors.

Target localhost performance remains: manifest under 500 ms and first score layer ready under 1.5 seconds. Map tile or overlay failures are correctness bugs.

## Deployment direction

For now the service binds to loopback. The eventual mini-PC deployment should run the updater from a Nix-managed timer, serve only completed releases and expose the application at `nice.jjpcodes`. Keep refresh controls private to local or Tailnet administration. World coverage can follow only after the Europe data and interaction contracts remain fast and trustworthy in production.
