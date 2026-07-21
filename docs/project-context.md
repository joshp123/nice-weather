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

Map colours use six stable judgements: Awful, Poor, Mixed, Good, Very good and Excellent. Awful is reserved for genuinely miserable conditions; it is not a dramatic synonym for merely suboptimal weather. A restrained continuous ramp carries the underlying score while keeping geography legible.

## Current data contract

The live release contains 14 complete local forecast days and 12 climate months:

| Period | Source | Product grid | Time step | Label |
| --- | --- | ---: | ---: | --- |
| Detailed forecast | DWD ICON-EU | about 7 km | hourly, then three-hourly | Detailed forecast |
| Outlook | ECMWF IFS | about 28 km | 6 hours | Outlook |
| Typical month | CHELSA 2.1, 1981–2010 | about 11 km | monthly normal | Typical month · 1981–2010 |

ICON-EU is used only for days it covers. IFS continues the same 14-day product without pretending that its native resolution is finer. CHELSA source rasters are about 1 km, but climate delivery is deliberately resampled to 0.1° and reported as about 11 km across Europe. Monthly precipitation is used as a daily average; CHELSA does not provide wet-time frequency, so that evidence remains absent rather than being invented. Because nonlinear daily scores cannot be reconstructed from monthly means, the product labels this view as a monthly average rather than claiming a probability of nice weather. A future climate replacement must score recent observed days before aggregating their distribution.

The chosen replacement source is AgERA5 v2.0. Its official 0.1° daily product supplies daytime temperature, dew point, precipitation amount and duration, cloud, solar energy and 10 m wind from 1979 onward. It is topography-corrected/interpolated ERA5 rather than independent 11 km atmospheric detail. Its “local time” uses eight longitude zones in three-hour steps, not IANA civil time. The target climate period is 2006–2025: score every real day, then publish the chance of a Good-or-better Overall, Comfort, Dry, Bright and Calm day, alongside Awful frequency, rating counts and poor-weather persistence. `internal/climate` provides a strict complete-period single-cell conformance seam for those semantics; it is not yet the worldwide extraction or grouping pipeline. Authenticated CDS and ARCO access work, and a complete real 2024 source block now passes. Publication masks water before processing. Because AgERA5 bias-corrects minimum and maximum temperature independently, its adapter orders the rare crossed pair and reports the repair count and largest inversion; the generic weather validator remains strict. The next gate is one real worldwide month.

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

Map pixels and point details use the same score. The frontend only selects a period and profile; client-side scoring is forbidden. The 14-day product keeps a coastal target cell when its centre is water but a complete finer detailed-forecast cell exists inside that same honest 28 km target cell; it never reaches into a neighbouring target cell.

The score has four orthogonal components: thermal comfort, dryness, brightness and calm. Thermal comfort combines temperature with moisture: hot-dry and hot-humid conditions are deliberately different, as are cold-dry and cold-damp conditions. Deal-breaker rules stop cold, oppressive, very wet or very windy days from winning through averages alone.

## Backend boundaries

- `internal/providers/dwd` discovers complete ICON-EU runs and downloads bounded, validated bzip2/GRIB files.
- `internal/providers/ifs` reads ECMWF indexes, retrieves exact byte ranges and validates complete GRIB2 messages.
- `internal/pipeline` decodes through ecCodes/GDAL, normalises units and accumulations, aggregates local days, applies the land mask and renders output.
- `internal/weather` owns canonical facts, validation, scoring, ratings and deterministic summaries.
- `internal/delivery` owns the manifest, compact binary point files, image metadata, validation and atomic `current`/`previous` publication.
- `internal/update` plans 14 days, owns the pinned Natural Earth land source, builds all 12 CHELSA months and publishes one complete release.
- `internal/appserver` serves one immutable release snapshot, detects publication changes and swaps to a fully validated replacement between requests. Point selection happens in the browser through two constant-size byte-range reads from the release-scoped point file.

There is no provider fallback soup, live tile rendering, client-side score policy or compatibility layer for the discarded prototype.

## Artifact contract

Runtime output is separate from the frontend build:

```text
dist/                         Vite application
artifacts/current/manifest.json
artifacts/current/images/     overall and component map PNGs per period
artifacts/current/points/     immutable native-weather, display-score and window records
artifacts/current/wind/       immutable gzip-compressed u/v frames per forecast day
artifacts/.building/           unpublished release under construction
artifacts/releases/           validated release directories
artifacts/places/current.json  revalidated place-index pointer
artifacts/places/releases/     immutable place manifests and shards
.cache/weather/staging/       bounded, temporary update work
.cache/weather/land/          validated Natural Earth source cache
.cache/weather/climate/       validated monthly climate checkpoints
```

A live update starts in `artifacts/.building`. After the forecast window, daily forecasts, climate months, images, point files, hashes and dimensions validate, the updater atomically moves that directory into `artifacts/releases` and changes `artifacts/current`. The running server detects the new target, validates the complete release and swaps its immutable snapshot between requests. No restart is required, and an in-flight request continues to use one coherent release.

Manifest `dataStatus` is a required `live` or `sample` value. The fixture publishes `sample`; the updater publishes `live`. The interface shows a clear sample-data warning. Older releases and climate checkpoints are rejected. Each point file has a fixed header, a literal whole-file SHA-256 and a separate embedded payload checksum. Its path contains the whole-file hash. The browser accepts only exact `206 Partial Content` responses with the expected range, total size, byte count, content type and no content encoding. It does not claim to verify a whole-file hash after reading partial ranges.

Product contract `nice-weather-v18` separates native weather evidence from the display score when their geometry differs. ICON and climate images remain one-for-one with their native point planes. Each IFS outlook keeps its native 28 km weather file and adds an 8-byte-per-texel display-score file on the detailed forecast's canonical 1104-column score grid. Its 1104×680 regular Europe grid becomes a 1104×1179 Web Mercator point and image plane. The 14-day record uses that same dense geometry and contains both summaries plus the exact 14 daily judgements behind the visible texel.

The dense field interpolates canonical scalar scores in Go, before palette rendering. It uses bilinear interpolation only when all four source records are valid. A Natural Earth mask is rasterised at target density; target water remains transparent, while partial coastal neighbourhoods use the nearest valid contributor deterministically. This improves the decision surface without changing the honest `about 28 km` IFS source label or creating browser-side scoring.

Runtime routes:

```text
GET /                         React application
GET /manifest.json            release catalogue
GET /releases/<id>/images/... immutable score overlay for that release
GET /releases/<id>/points/... immutable point records with byte-range support
GET /releases/<id>/wind/...   immutable compressed wind-vector frame
GET /places/current.json      revalidated place-index pointer
GET /places/releases/<id>/... immutable place manifest and search shards
```

The place builder consumes pinned GeoNames extracts, validates the complete index and installs it beneath `artifacts/places/releases`. It writes `places/current.json` only after the immutable release is in place. The browser verifies the manifest and requested shard by SHA-256. The server does not implement search.

## Frontend boundaries

React owns product state, controls, loading and disclosure. MapLibre owns geographic interaction and compositing. The scalar overlay is a single Web Mercator image source whose release-scoped URL changes without rebuilding the map. The old raster is removed before a new selection loads and remains hidden until the exact new source has rendered; failed or stale assets cannot remain under new controls.

Day-by-day view can add a static wind-direction layer from the existing layer menu. It is off by default and causes no wind-file request until enabled. Only the selected exact provider frame is fetched. The catalogue preserves every strictly increasing source timestamp; ICON can change from hourly to three-hourly output within one day, while IFS supplies its available six-hourly frames. No timestamp is inferred or interpolated. Sparse direction marks are sampled from the decoded grid in the viewport and sit above the scalar score but below borders and labels. They do not reserve symbol-collision space, so place and country labels remain visible. There is no particle animation or second scoring path.

WebGL is required for the decision map. If it is unavailable, the interface shows a clear unavailable state instead of presenting simplified or potentially misleading geography.

The app must work at phone width and on a 57-inch display. The dock is width-capped; the map keeps the remaining space. Avoid permanent lists, cards inside cards, alternate control styles and raw model jargon in the primary interface.

## Commands

```bash
# Deterministic sample release
go run ./cmd/niceweather fixture --out ./artifacts

# Verify upstream data, metadata and units without publishing
go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts --smoke

# Build and publish current live weather
go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts

# Build and publish the Europe place index from pinned GeoNames extracts
go run ./cmd/niceweather places --source ./.cache/geonames/2026-07-18 --source-date 2026-07-18 --out ./artifacts

# Serve the built frontend and current weather release
go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5198
```

Local product URL: <http://127.0.0.1:5198/>

## Verification order

Check boundaries before judging the full map:

1. Run `go test ./...` and deterministic scoring cases.
2. Inspect update logs, `manifest.json`, period counts, source metadata and resolutions.
3. Verify the binary point header and selected record against exact PNG pixels, including transparent sea.
4. Run `go vet ./...`, `npm run test` and `npm run build`.
5. Start the Go server on `5198` and fetch the manifest, exact point-file ranges and every referenced asset type.
6. In the internal browser and Chrome or Safari, change day, profile and basis; search; click the map; and inspect desktop and mobile layouts.
7. Record manifest latency, score-layer readiness, transferred bytes, long tasks, frame timing, JavaScript heap and browser errors.

Target localhost performance remains: manifest under 500 ms and first score layer ready under 1.5 seconds. Map tile or overlay failures are correctness bugs.

## Deployment direction

For now the service binds to loopback. The eventual mini-PC deployment should run the updater from a Nix-managed timer, serve only completed releases and expose the application at `nice.jjpcodes`. Keep refresh controls private to local or Tailnet administration. World coverage can follow only after the Europe data and interaction contracts remain fast and trustworthy in production.

## Nullschool evidence

Current Earth Nullschool does not use sub-kilometre global weather. Direct Chrome requests show separate GFS `.epak` fields at 0.5° by default and 0.25° in HD mode. Wind and temperature are separate packs; changing the scalar temperature overlay leaves wind particles active. Its global weather updates every three hours. The 0.25° wind pack was about 1.08 MB compressed and 2.08 MB decoded; the matching temperature pack was about 472 kB compressed and 1.04 MB decoded. On this Mac it sustained about 96 fps with roughly 16 MB JavaScript heap at a global view, excluding GPU memory.

The apparent detail comes from bilinear scalar interpolation, animated vectors and sharp geography. Nullschool's 2026 basemap uses projection-agnostic Natural Earth tiles through six zoom levels; its creator says this is nearly seven times more geographic detail than the classic map. Nice Weather's 7 km ICON-EU days already have finer native weather than Nullschool HD GFS. The useful lessons are smooth scalar presentation, direct time manipulation and independent scalar/vector layers—not copying particles or pretending interpolation creates resolution.
