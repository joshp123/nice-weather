---
written_by: ai
---

# Project context

This is the current technical handoff for Nice Weather Finder. `AGENTS.md` defines the project rules. `.agent/execplan-pending.md` records product decisions, measurements and review evidence.

## Product

Nice Weather Finder is a worldwide decision map for choosing where to position yourself for pleasant weather. It is not a general forecast dashboard.

The resting screen contains:

- a full-screen geographic map;
- one search field with worldwide place search and forecast-ranked suggestions;
- one compact bottom control for time, lens and preference;
- one selected-place sheet, opened on demand;
- one short data-status line.

The map is the product. Details, scoring theory and source metadata use progressive disclosure. Do not add permanent lists, nested cards, alternate button families or raw provider language to the main screen.

“Nice” means comfortably warm, bright, dry and calm. “Cycling” applies a stricter rain and wind judgement. The warm bias is intentional: 22–27°C is ideal and 16°C cannot score above Mixed.

The six ordered judgements are Awful, Poor, Mixed, Good, Very good and Excellent. Awful describes genuinely miserable weather. It is not a dramatic label for an ordinary compromise.

## Current live data

| View | Source | Coverage | Honest weather detail |
| --- | --- | --- | ---: |
| Next 14 days | ECMWF IFS | worldwide | about 28 km |
| Individual forecast days | DWD ICON-EU where complete; IFS elsewhere | worldwide, with European refinement | about 7 km or 28 km |
| Recent climate | ERA5 hourly evidence, 2006–2025 | worldwide land; replacement build in progress | source 0.25° grid, about 28 km |

The forecast pointer currently identifies one complete IFS baseline and the newest complete ICON-EU refinement. A newer ICON-EU run may replace an older refinement under the same IFS baseline only when every global evidence hash is unchanged and no existing European detail day is lost. Forecast publication cannot move the date horizon backwards.

A daily forecast record comes wholly from one provider. Providers are not blended field by field. IFS supplies the worldwide baseline. ICON-EU replaces complete daily records inside its proven support.

Forecast metrics use the local daytime window. The source run, forecast range and native detail remain available without burdening the main answer.

The previous AgERA5 publication is rejected and inactive because its coarse longitude windows created a false north-south seam. The replacement uses hourly ERA5 evidence from 2006 through 2025. It builds exact IANA civil-local daytime records, scores each real day, then publishes:

- chance of a Good-or-better day;
- chance of an Awful day;
- chance that a seven-day window contains at least three consecutive Poor or Awful days;
- physical Comfort, Dry, Clear sky and Calm evidence with units.

ERA5 is stored on its source 0.25° grid and described as about 28 km weather detail. Each grid point uses its exact valid civil dates and an IANA civil-local 10:00–20:00 daytime window. November is the first complete production canary. No seasonal control appears as available until its history, land authority, scalar tiles and point evidence pass publication checks.

Natural Earth 5.1.1 centre-on-land cells are the climate land authority. Water is transparent. A named coastal place can use the nearest valid land cell; the place sheet says when this happens. Missing inland evidence must remain missing and fails publication checks.

The worldwide place index comes from pinned GeoNames extracts. It keeps European settlements and globally useful populated or administratively significant places. Search matches canonical names, local names, alternate names and transliterations. There is no selected-city list.

## One weather meaning

All providers translate source fields into `weather.DailyMetrics`. Missing evidence is explicit. It is never replaced with zero.

```text
provider fields
    -> normalised, time-weighted daily metrics
    -> canonical component scores
    -> canonical Nice and Cycling scores
    -> immutable point blocks and scalar tiles
```

The browser does not score or rescale weather. Map colour and point evidence use the same stored canonical result.

The orthogonal components are:

- **Comfort**: temperature and moisture together, including hot-humid and cold-damp discomfort;
- **Dry**: rain amount and meaningful wet time;
- **Clear sky**: cloud cover only; daylight and solar energy can appear as unscored context;
- **Calm**: wind comfort under the selected preference.

Deal-breakers stop cold, oppressive, very wet or very windy days from winning through an average. A physically valid calculation that gives a plainly implausible product answer still requires product review before changing score policy.

## Backend boundaries

- `internal/providers/dwd` discovers and validates complete ICON-EU source runs.
- `internal/providers/ifs` retrieves exact ECMWF IFS GRIB messages and validates their identity, units and time semantics.
- `internal/pipeline` normalises fields, aggregates local days, applies land support and invokes the canonical scorer.
- `internal/weather` owns weather facts, scoring, ratings and summaries.
- `internal/update` owns the complete worldwide forecast transaction and the global climate orchestration.
- `internal/forecastruns` validates immutable forecast catalogs and publishes `current.json` last.
- `internal/climatebuild`, `internal/climateassembly` and `internal/climatetiles` build, validate and publish daily-distribution climate months.
- `internal/places` owns the worldwide GeoNames index and static search shards.
- `internal/destinations` builds the forecast-bound best-weather shortlist offline.
- `internal/appserver` serves the frontend, immutable artifacts, pointer files and strict byte ranges.

There is no provider fallback soup, browser-side scorer or live server-side weather calculation. Server startup uses the artifact root directly. The retired monolithic `artifacts/current` release, `/manifest.json` route and `/releases/...` routes do not exist in the active contract.

## Artifact contract

```text
dist/                                      Vite production frontend
artifacts/forecast/current.json            current forecast pointer
artifacts/forecast/runs/<id>/               immutable forecast index, catalogs, tiles and point blocks
artifacts/climate/current.json              current climate index pointer
artifacts/climate/indexes/<id>.json         immutable coherent month set
artifacts/climate/releases/<month-id>/      immutable month catalog, tiles and point blocks
artifacts/places/current.json               current place-index pointer
artifacts/places/releases/<id>/             immutable place manifest and search shards
artifacts/best-weather/current.json         forecast/place-bound shortlist pointer
artifacts/best-weather/releases/<id>.json   immutable destination shortlist
```

Pointer files are small and non-cacheable. Versioned artifacts are release-scoped, checksum-bound, immutable and cacheable. A publisher installs complete immutable content first and changes its pointer last. The server validates and hot-swaps forecast snapshots. Climate, place and shortlist clients revalidate their static pointers and immutable files before changing visible state.

Scalar `.nws` tiles contain evaluated values, not final RGB colours. The browser verifies the declared SHA-256, uploads one-channel textures and applies the canonical continuous palette in WebGL. Viewport loading bounds network, memory and GPU use. The current forecast baseline uses z3 scalar tiles; European detail uses z5. Climate uses z4.

Point evidence is stored in compact spatial blocks. The client fetches only the selected block. Exact immutable files support GET, HEAD and HTTP Range with strict content type, length and cache headers.

## Frontend boundaries

React owns typed application state, accessibility and progressive disclosure. MapLibre owns navigation, labels, borders and compositing. The scalar custom layer owns weather colour only.

The interface has one control family and one visual hierarchy. Overall, Comfort, Dry, Clear sky and Calm are coordinated views of one stored judgement. Forecast mode uses real dates. Climate mode uses monthly distributions rather than a fake calendar.

The scalar renderer indexes each forecast or climate catalog once. Per-frame linear scans are forbidden. Ordinary map movement must not produce a task over 100 ms.

WebGL is required for the decision map. If it fails, the product says that the map is unavailable instead of showing approximate or stale geography.

## Product checks

Use these scenarios before changing meaning or claiming completion:

- Málaga in November should often be Nice and rarely Awful;
- Amsterdam and London in January should show a high risk of genuinely awful weather;
- Singapore-like hot humidity must differ from hot-dry weather without making every valid tropical day Awful;
- Cairo-like hot-dry weather, Tromsø winter and summer, Alpine cells, coastlines, Canberra, São Paulo and Fiji must remain credible;
- selected-place evidence and the visible scalar value must agree exactly.

Browser acceptance covers `393×852`, `430×932`, `1366×768`, `1728×1117`, `2560×1440` and ultrawide. Reject clipping, overlap, unbounded chrome, illegible colour, unexplained blank land, reload-dependent recovery or a sample release that looks live.

The localhost performance gates are:

- useful map under two seconds;
- visible control response under 100 ms;
- near-display frame rate during ordinary movement;
- no ordinary long task above 100 ms;
- bounded heap, weather transfer and GPU textures;
- no weather request or application animation while idle;
- no browser warning, error or failed weather request.

Tests and metrics are evidence. Josh's walkthrough is the final product veto.

## Local and deployment boundary

The sole local URL is <http://127.0.0.1:5198/>. A data refresh hot-swaps. Restart only after changing the server binary or when the process itself has stopped.

The intended public boundary is an opaque immutable artifact origin: private S3, CloudFront origin access control, the existing `jjpcodes.com` Route 53 zone and `nice.jjpcodes.com`. The mini-PC should publish outbound only. It does not need a public inbound port or a Tailscale Funnel.

Deployment remains a separate approval gate. Do not create AWS resources, alter DNS, expose the mini-PC or publish the site without Josh's explicit sign-off. The static origin contract must remain independent of React, today's tile schema and future overlays.

## Nullschool evidence

Earth Nullschool's current About page names its sources. Direct live inspection found GFS weather packs at 0.5° by default and 0.25° in HD mode, with separate scalar and vector fields. Its apparent detail comes from honest interpolation, moving wind context and sharp geographic rendering, not sub-kilometre global weather.

The useful principles are independent scalar and vector layers, direct time movement, smooth value interpolation and preserved geographic context. Particle animation is not a requirement. Any overlay must improve a real decision and pass the same mobile performance budget.
