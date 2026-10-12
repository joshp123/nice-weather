---
written_by: AI, GPT-6 (Codex), 2026-10-12
---

# Project context

This is the current technical handoff for Nice Weather Finder. `AGENTS.md` defines the project rules. `.agent/execplan-pending.md` records product decisions, measurements and review evidence.

`product-reset.md` records Josh's October 2026 direction, the inspected reference products and the next product milestone. The complete annual journey replaces the earlier November-only milestone.

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
| Next 14 days | DWD ICON-EU where complete; IFS elsewhere | worldwide, with European refinement | about 7–28 km |
| Individual forecast days | DWD ICON-EU where complete; IFS elsewhere | worldwide, with European refinement | about 7 km or 28 km |
| Recent climate | ERA5 hourly evidence, 2006–2025 | worldwide land; replacement build in progress | source 0.25° grid, about 28 km |

The forecast pointer currently identifies one complete IFS baseline and the newest complete ICON-EU refinement. A newer ICON-EU run may replace an older refinement under the same IFS baseline only when every global evidence hash is unchanged and no existing European detail day is lost. Forecast publication cannot move the date horizon backwards.

A daily forecast record comes wholly from one provider. Providers are not blended field by field. IFS supplies the worldwide baseline. ICON-EU replaces complete daily records inside its proven support.

Forecast metrics use the local daytime window. The map, fortnight strip, opened day and physical evidence use the same stored XYZ daily record. European fortnight summaries use complete ICON-EU days and the corresponding global parent cell for remaining days. Regional summaries stay sparse; the world is never upsampled to imply finer detail.

There is one point representation. The duplicate native `.nwf` output and codec are removed. An index identifies the visible snapshot separately from each immutable daily catalog identity, so existing daily records can be reused without changing their headers. `forecast-products` republishes derived summaries from those files without reacquiring source data.

The previous AgERA5 publication is rejected and inactive because its coarse longitude windows created a false north-south seam. The replacement uses hourly ERA5 evidence from 2006 through 2025. It builds exact IANA civil-local daytime records, scores each real day, then publishes:

- chance of a Good-or-better day;
- chance of an Awful day;
- chance that a seven-day window contains at least three consecutive Poor or Awful days;
- physical Comfort, Dry, Clear sky and Calm evidence with units.

ERA5 uses its source 0.25° grid and is described as about 28 km weather detail. Each grid point uses its exact valid civil dates and an IANA civil-local 10:00–20:00 daytime window. `era5-climate-year` reads each native land tile once from the pinned lossless temporal layout, reduces all twenty years into twelve monthly distributions, and resumes durable tile checkpoints. One following source tile downloads while the current tile reduces; temporary hourly storage stays near 1.4 GB. Worldwide month publication starts after every land tile is complete. The annual journey requires all twelve accepted months.

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
- `internal/update` owns the complete worldwide forecast transaction.
- `internal/forecastruns` validates immutable forecast catalogs and publishes `current.json` last.
- `internal/era5annual` owns the annual source/checkpoint/publication loop. `internal/providers/era5hourly` aggregates exact civil days, `internal/era5climate` reduces their canonical distributions and physical quantiles, `internal/era5publication` builds land authority, and `internal/era5climateproduct` publishes immutable monthly artifacts.
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

Scalar `.nws` tiles contain evaluated values, not final RGB colours. The browser verifies the declared SHA-256, uploads one-channel textures and applies six discrete suitability colours in WebGL. Viewport loading bounds network, memory and GPU use. The current forecast baseline uses z3 scalar tiles; European detail uses z5. Climate uses z4.

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
- no application animation while idle; small forecast-pointer checks run every five minutes while visible and when resuming the page;
- no browser warning, error or failed weather request.

Tests and metrics are evidence. Josh's walkthrough is the final product veto.

## Local and deployment boundary

The sole local URL is <http://127.0.0.1:5198/>. A data refresh hot-swaps. Restart only after changing the server binary or when the process itself has stopped.

ChatGPT Sites is the chosen public delivery surface. The small Vite interface can use Sites static assets. The weather corpus needs Sites-managed object storage and a thin same-origin request layer that preserves GET, HEAD, exact byte ranges, content types and immutable caching. Acquisition, scoring and validation remain offline; publish only accepted compact artifacts, with immutable objects first and pointers last.

Sites serves the website and weather artifacts through its included managed hosting, without a personal Cloudflare account or paid plan. The canonical address is `https://nice-weather.jjpcodes.chatgpt.site`. The optional weather custom domain and its three Route53 records were removed to avoid metered DNS queries. Do not expose the mini-PC. Inspect the actual hosted application before claiming deployment complete.

## Nullschool evidence

Earth Nullschool's current About page names its sources. Direct live inspection found GFS weather packs at 0.5° by default and 0.25° in HD mode, with separate scalar and vector fields. Its apparent detail comes from honest interpolation, moving wind context and sharp geographic rendering, not sub-kilometre global weather.

The useful principles are independent scalar and vector layers, direct time movement, smooth value interpolation and preserved geographic context. Particle animation is not a requirement. Any overlay must improve a real decision and pass the same mobile performance budget.
