# Coastal temperature diagnosis and delivery format

Written by AI on 2026.10.17 by model GPT-6.1 Sol. Phase 1 research only.

## Verified cause

The current coastal rule moves fog-coast temperature inland, then warms that inland temperature to sea level. This removes the marine-layer cooling twice. It is a real source error, not an apparent-temperature curve problem.

Read in full: Josh's records 098–100 and 116, `.agent/v6-contract.md`, `research/climate-v6/README.md`, `static.py`, processing/assembly in `pipeline.py`, browser scoring/sampling/loading, Worker place-year decoding, and the earlier fine-data/Bay Area/coastal reports.

**The contract and implementation disagree.** The contract describes a source-to-target-grid height shift. Actual `static.py` takes a coastal point's source height, clips it to 0–600 m, and adds `0.0065 × height` degrees: effectively sea level, regardless of the selected output grid's terrain. It applies to points with land fraction <0.95 or a sea neighbour, including points that retain their own source. For land fractions <0.8 it first substitutes the nearest ≥0.8-land point within a 3×3 neighbourhood constrained to one 12×12 tile. That tile restriction is a processing limitation, not a climatic boundary.

Morning read-only verification on 2026-10-17: the live `https://nice.jjpcodes.com/data/v6/points.bin` SHA-256 exactly matches SSD `v6/points.bin`: `e3f433b6a74d8d860990f2adafaddce4eb127b776e36436657d5b950939c8227`. That morning capture uses chunks 80 and 82, build `2026-10-17T06:05:03.100280+00:00`; SSD climate had advanced to chunks 76,78,80,82,84. Comparing that later SSD snapshot directly with the morning live facts is invalid. The diagnosis/decomposition tables below retain the morning capture.

**Later live capture:** `research/fine/published.py` fetched and verified all published binary facts/default scores against the API at every evaluation query in 39.8 s. `/tmp/nwf-agents/round4/fine/published-check/live_points.json` now records the internally consistent live build `2026-10-17T13:42:48.141673+00:00`, chunks76,78,80,82,84 (2015–24), with the same static-points hash. San Francisco June–August highs are now27.3/27.3/27.3°C, Lima June–September25.7/25.7/25.7/26.0°C: the coastal error remains. The earlier mixed capture is preserved as `live_points-morning-with-late-additions.json`: its late updated Chania and Agadir-airport entries came from this newer publication while its other23 entries came from the morning build. Parent has frozen the new consistent capture as the default `live_points.json` for current-publication comparisons. The matched-day product algorithm uses the selected source/shift, which are unchanged.

| Click | Actual selected 0.25° point | Land share / terrain | Temperature source | Source land / terrain | Added warming | Source distance from click |
|---|---|---|---|---|---:|---:|
| San Francisco card | 38.00, −122.50 (north of SF) | 0.512 / 71 m | 38.25, −122.50 (inland) | 0.985 / 139 m | +0.91°C | about 53 km |
| San Francisco browser score | 37.75, −122.25 (across the Bay) | 0.687 / 80 m | 37.75, −122.00 (inland) | 0.954 / 154 m | +1.00°C | 37.0 km |
| Lima | −12.00, −77.00 | 0.758 / 420 m | −12.00, −76.75 (inland) | 0.996 / 1261 m | +3.90°C (600 m cap) | 30.6 km |
| Valparaíso | −33.00, −71.50 | 0.657 / 202 m | −33.00, −71.25 (inland) | 0.997 / 528 m | +3.43°C | 34.2 km |
| Málaga | 36.75, −4.50 | 0.827 / 343 m | itself | 0.827 / 343 m | +2.23°C | — |

The same rule can improve Málaga while harming an inversion coast. At Lima, a fixed mountain lapse rate is especially inappropriate: warm inland/slope air above the cool marine layer is not a freely mixed air mass that should be warmed down to the coast.

## Numerical decomposition

The following use the card-selected points for San Francisco and the city clicks for Lima/Valparaíso. They are measured means of daily 10:00–20:00 local air maxima from the live product's hourly chunks 80 and 82. Local calendar timestamps use the same IANA zones and linear hourly interpolation as the pipeline; local dates 2–361 of each chunk are included. They compare **the original selected point** with its substituted source; they are not station-coordinate ERA5 and are not held-out station errors. Rounded published facts have ⅓°C encoding. Read/decompress/reduce of all three places and both chunks took **0.17 s**, warm SSD cache; this is a diagnostic microbenchmark only.

| Location / month | Original selected point | Inland source alone | Source + current warming | Live place card high |
|---|---:|---:|---:|---:|
| San Francisco card June | 22.71 | 26.60 | 27.51 | 27.7 |
| San Francisco card July | 21.44 | 25.90 | 26.81 | 26.7 |
| San Francisco card August | 23.57 | 27.82 | 28.73 | 28.7 |
| Lima June | 19.23 | 21.69 | 25.59 | 25.7 |
| Lima July | 18.31 | 21.14 | 25.04 | 25.0 |
| Lima August | 18.38 | 21.59 | 25.49 | 25.3 |
| Lima September | 18.59 | 21.36 | 25.26 | 25.3 |
| Valparaíso January | 19.86 | 24.79 | 28.22 | 28.3 |
| Valparaíso February | 20.16 | 24.63 | 28.06 | 28.0 |
| Valparaíso July | 13.92 | 14.41 | 17.84 | 18.0 |

The San Francisco card and browser select different points. The card rounds the click first, then picks the first row/column on a tie; the browser uses distance to the actual click. The card selects 38.00, −122.50; the browser selects 37.75, −122.25. Using the actual card selection reconciles reconstructed monthly highs with live facts within the ⅓°C encoding. At the browser-selected SF point, July is 22.66°C native →25.78°C inland →26.78°C after +1.00°C warming. The dominant 4–8°C inland/height effects are independently visible before any scoring.

Live San Francisco monthly high/score (January→December):

- High °C: 15.7, 19.3, 18.3, 21.0, 24.3, 27.7, 26.7, 28.7, 28.0, 23.3, 19.7, 13.7.
- Score: 30, 60, 46, 63, 73, 90, 95, 93, 87, 74, 64, 14.

Live Lima high/score:

- High °C: 26.0, 25.3, 26.0, 26.3, 26.7, 25.7, 25.0, 25.3, 25.3, 25.7, 25.7, 26.3.
- Score: 57, 51, 52, 73, 82, 82, 79, 75, 73, 75, 73, 67.

Live Valparaíso high/score:

- High °C: 28.3, 28.0, 27.3, 24.7, 22.0, 18.0, 18.0, 19.3, 22.3, 25.0, 27.3, 28.3.
- Score: 98, 99, 99, 91, 81, 51, 53, 64, 82, 86, 96, 98.

A useful unfitted control is removing both inland substitution and blanket sea-level warming. It fixes much of Lima/Valparaíso's obvious overheating immediately; SF still needs station-informed coastal detail. The earlier Bay Area report's downtown observed July mean high is 18.8°C versus native selected-grid 22.66°C here, with different evaluation years. That is evidence of residual unresolved coast structure, not a paired-date error measurement.

Dew point takes the substituted temperature source and receives the same sea-level warming in `pipeline.py`; this changes both Steadman apparent temperature and the humidity multiplier. It is not just an air-temperature issue. Sunshine and wind retain the original selected grid's ERA5 signal. A station-derived air-temperature residual should not inherit that inland/dew warming without independent validation. Live San Francisco June–August sunshine shares are 87%,89%,89%; air-temperature correction alone cannot establish fog or perceived-comfort truth.

## What the current browser and card actually consume

1. `offsets-MM.png` is one global 1440×721 byte grid per month. It carries **one scalar felt-temperature shift**, added equally to every daytime and evening felt value. Byte scale is 0.1°C, valid offsets −12.8…+12.6°C; 255 is sea. Twelve decoded grids cost **12,458,880 bytes**. Current compressed SSD files are about 172–176 KiB each, roughly 2.1 MB total.
2. Browser GPU scores each 1° cell's eight representative days at **11 uniform shifts**, −15…+15°C. Fine resolution then selects its offset and interpolates both shift levels and four coarse-cell scores. The map finally smooths the fine score grid. The place-score sampler uses the same fine offset and level/cell interpolation, without that display smoothing.
3. The Worker place-card yearly chart independently range-reads `facts.bin` (**74,753,280 bytes**) and `scores.bin` on the 0.25° grid. Facts store air high and **felt at 21:00**, not air low. These files are already derived from corrected hourly values, not dynamically corrected by Worker offsets.
4. Place-grid selection is subtly different: browser measures distance from the actual click to candidate pixels; Worker rounds the click to a grid centre then measures integer distance around that centre. They must share one fine-pixel choice in a finer format. Do not promise exact new map/card agreement by changing only one path.

## Resolution proposal

A global 0.25° output cannot preserve 1 km corrections. Downtown San Francisco already selects a point across the Bay; no perfect 1 km input survives that lookup. Fine spatial detail is useful to visitors when they zoom or click, even if a world overview cannot display it.

Retain the compact coarse day records. Retain a coarse global overview for fast first paint. Store fine monthly correction/anchor data as a tiled 30-arc-second raster (nominal 1 km north–south), load only visible fine tiles at close zoom, and have the place card range-read the **same containing fine pixel**. Do not fetch or decode the whole world at 1 km in the browser.

Smallest honest data-format extension: add one two-channel tiled monthly field for independent high and low air-temperature residuals, plus grid/scale/no-data metadata. Two signed Int16 values at 0.1°C per pixel give 4 bytes/month/pixel and preserve source precision well below measured model errors. A 256×256 tile is **262,144 bytes/month**, or **3,145,728 bytes** for all twelve months uncompressed. A two-byte RG8 representation could halve this after measured residual ranges establish that the present ±12.7°C span is sufficient; do not assume that for high terrain.

CHELSA's native 43,200×20,880 geometry would be **43.30 GB raw** for 24 Int16 bands (high+low × 12), including sea/nodata; compression and land-only tiled storage reduce it, but must be measured. A 0.25° pair of Int16 overview fields is only **49.84 MB raw for all months**. A 1/24° (~4.6 km north–south) derivative is 25 times smaller than 30 arc seconds and is worth measuring as a transfer compromise. Keep the highest justified source resolution on SSD; choose published overviews/tile levels by paired station and within-town tests, not source labels.

This is a format proposal, not a measured browser-performance result. The current GPU API is hard-coded to a single scalar shift. Two independent shifts would require either a 2D score lookup (11×11 levels is ~11 times the current scoring work) or direct per-visible-pixel scoring. One 256² tile × 8 days × 11 hours is about **5.77 million hourly grade evaluations/month** before coarse-cell interpolation. A visible tile implementation needs a real GPU benchmark before promising the existing 0.2 s redraw target. Merely adding a second offset PNG would leave scoring wrong.

### Important temporal limitation

Station daily lows normally describe pre-dawn temperatures; they are not the 21:00 walk-home temperature. The existing 15-byte records contain felt temperatures, not the underlying air-temperature diurnal range. Therefore `(high correction + low correction)/2` or a simple linear clock interpolation is an additional model, not validation of the walk home. Keep separate high/low normal anchors during this research, apply and validate their hourly interpolation while ERA5 air temperatures are still available, and regenerate coarse records/facts consistently. For a dynamic fine correction, explicitly carry or derive the validated diurnal weights needed to apply the two residuals at the ten stored felt hours. A monthly per-cell ten-byte weight table would be about **2.24 MB/year** at 18,667 cells, before compression, if a fixed climatological diurnal shape proves adequate. Neither this approximation nor the final feels-like correction is validated by extrema-only station errors.

Phase 2 must regenerate map scores, place facts and default images from the same corrected hourly model. No changes were made to current products, site code, climate processor or ERA5 downloads during this diagnosis.

## Independent hourly observations acquired

`research/fine/hourly_stations.py` retrieves public NOAA station/year GHCNh Parquet, using the official legacy ISD public CSV archive where GHCNh aliases omit useful reports. The raw files, metadata and daily/monthly exports are in `/tmp/nwf-agents/round4/fine/hourly/`. Its `summary.json` records per-file URLs, actual coordinates/elevation, QC counts, coverage and acquisition/processing time. `daily_sampled_extrema.csv` retains local date, sampled high/low, sampled 10–20 h high, air/dew/wind at 21:00 and computed shade Steadman at 21:00. Missing values remain missing.

QC accepts unflagged observations and documented non-suspect NOAA codes 0/1/4/5/9. UTC timestamps are rounded to the nearest hour by choosing the report closest to that hour; the maximum time displacement is 30 minutes. IANA zones establish the local calendar and 21:00 observation. Strict extrema summaries require at least 18 local-hour temperature samples per day. Even those are hourly-sampled extrema, not instrument-recorded true daily extrema; compare ERA5 at the same sampled times or retain that sampling bias explicitly. Instrument daily highs/lows from GHCNd remain the first extrema benchmark.

The archived daily-station metadata conflict at Chania remains explicit. US military hourly `GRW00033209` (LGSA) is 35.5333,24.15. HNMS's March 2025 [station table](https://api.emy.gr/api/cms/assets/329203e1-787c-40dc-9c7e-7b1d6b6d5f2a) places Greek SOUDA at 35.55,24.12; GHCNh Greek `GRMU0016746` agrees. Parent supplied DWD/GCOS 35.528611,24.145278 as the modern assumed coordinate for the daily station; `live_points.json` now keeps this diagnostic query and the original query separately. Instrument identity has not been established from a station-history record.

Recovered legacy records are especially useful at Lima (airport `84628099999`, about −12.021889,−77.114319), Funchal city (`08522099999`, 32.6333333,−16.9, 56 m), Gran Canaria airport (`60030099999`) and Cape Town airport (`68816099999`). This is superior to silently substituting a distant GHCNh station when the denser NOAA archive still contains the intended site. Some legacy files stop during 2025; per-year/day coverage is retained rather than calling that a full year.

Agadir lacked a daily evaluation station. The additional hourly evaluation site is explicitly **Al Massira airport**, about 21 km southeast of the city: ISD `60252099999`, 30.324997,−9.413067, 76.2 m. `hourly/evaluation_additions.json` names that proxy and its actual coordinates; `live_points.json` has the matching airport query plus the original city query. Gridded predictions must be sampled at the airport for that station comparison.

As an independent spot check before parent date matching, Lima's dense 2020–25 observations with ≥18 temperature hours/day yield these mean July values (186 days): sampled high 18.90°C, sampled low 16.17°C, observed air at 21:00 17.10°C, observed-dew/wind Steadman at 21:00 15.23°C. August is 18.55/15.74/16.77/14.63°C (176 days); September is 18.65/15.35/16.44/14.28°C (148 days). These demonstrate the current fog-coast overheating but are not the final paired-date candidate errors.

### Affine candidate and format cost

The additional untuned affine experiment maps the native available-period
monthly low/high to fine low/high plus a common warming anomaly. Each hour is
`targetLow + ratio * (nativeAir - nativeNormalLow)`, where `ratio` is the target
normal high-minus-low divided by the native normal range. This preserves the
ordering of hourly temperatures when the two target anchors are ordered. It is
a separate model candidate; the daily normalized-weight experiment remains
visible alongside it. The evaluation reports all monthly ratios, corrected
hourly extremes and non-positive range counts, without clipping bad results.

An affine correction cannot be applied exactly to the existing felt-only day
records: its air correction depends on the underlying air temperature. There
are eight unique clock hours across the ten stored felt slots (10,12,14,15,16,
18,20,21). Eight extra temperature bytes per representative-day record cost
14,336,256 bytes for twelve months at18,667 cells and eight days/month. Ten bytes
with duplicated12/18h entries would cost17,920,320 bytes. Monthly normal anchors
also need to be carried. This is a storage estimate, not a verified production
format: the research evaluates native0.25° air histories, while browser records
represent1° cell land means. Any proposed coarse-profile approximation needs
comparison against corrected hourly point scores before claiming equivalent
map/card accuracy or runtime.
