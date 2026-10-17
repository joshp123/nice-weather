Written by AI on 2026.10.17 by model GPT-6.1 Sol. Phase 1 research only.

# Fine temperature: publication plan and station evidence

Status: Phase 1 complete; stopped before the worldwide build. No worldwide
correction has been built. This work changed no production files, interface code, hosting code or
background ERA5-download code. No push or deployment.

## Decision

The current coastal correction must be replaced. It substitutes warmer inland
air and then adds a sea-level warming correction. That is inappropriate on
marine-inversion coasts. It also changes dew point, which a temperature-only
correction should not do without separate evidence.

Use **PRISM in the contiguous US**, then **WorldClim 1.4 elsewhere**. Where the
exact WorldClim pixel is missing, use its already-tested centred 5×5 valid-pixel
mean, explicitly marked as roughly 5 km support; if that is also absent, use
CHELSA v2.1. Where no eligible fine anchor exists, retain native ERA5 and mark it
as coarse coverage. This is one fixed source-availability rule, not a choice of
whichever source wins at each held-out station.

WorldClim 1.4's version-specific page grants **CC BY-SA 4.0**, unlike restricted
WorldClim 2.1. Publish the derived data with attribution and share-alike terms.
PRISM's current terms permit redistribution; CHELSA is CC0. Keep separate monthly
high/low anchors, adjust their climate period using ERA5 monthly mean change,
and preserve individual ERA5 day anomalies. Use the averaged uniform offset
only on days when the independent high/low shifts would cross. This physical
exception is tested below; do not use affine anomaly amplification globally.

Keep 30 arc-second source detail and serve close-view tiles, with coarser support
flags where a source/window genuinely is coarser. The current 25 km PNG cannot
carry these coastal differences. Both the browser and place-card reader need
the same correction model and fine pixel. This is a Phase 2 format proposal,
not a completed browser implementation or a whole-world accuracy proof.

A new global station-interpolation model is not justified ahead of this simpler
licensed baseline. The private foreign-GHCN-Daily residual experiment did not
beat it on the common cohort. Current GHCN-hourly has an explicit global CC0
grant if subsequent station refinement is needed; its daily-extrema sampling
and report-window issues still need handling. Daymet and SILO remain eligible
regional options, but are not selected without comparable evidence.

## Why San Francisco, Lima and Valparaíso overheat

The contract describes a source-to-target height difference. Actual
`research/climate-v6/static.py` instead adds
`0.0065 × clip(source_height, 0, 600)` degrees at selected coastal points,
effectively warming the source to sea level. Points with land fraction below
0.8 first take a mostly-land neighbour from a 3×3 neighbourhood constrained
inside the processing tile. Mountain lapse-rate reasoning does not hold through
a marine inversion.

These are daily 10:00–20:00 means reconstructed from the **morning published**
ERA5 chunks, not station error measurements. The site expanded its sample
during this research; the final station tables use the later consistent capture.

| Place / month | Original selected point | Inland source | Source + current warming | Morning card high |
|---|---:|---:|---:|---:|
| San Francisco, June | 22.71°C | 26.60°C | 27.51°C | 27.7°C |
| San Francisco, July | 21.44°C | 25.90°C | 26.81°C | 26.7°C |
| San Francisco, August | 23.57°C | 27.82°C | 28.73°C | 28.7°C |
| Lima, July | 18.31°C | 21.14°C | 25.04°C | 25.0°C |
| Lima, August | 18.38°C | 21.59°C | 25.49°C | 25.3°C |
| Valparaíso, January | 19.86°C | 24.79°C | 28.22°C | 28.3°C |

For San Francisco the card also picks a different fine point from the browser:
the card selects 38.00,−122.50 then takes its source at 38.25,−122.50; the browser
selects 37.75,−122.25 and takes 37.75,−122.00. The different tie-breaking explains
previous apparently inconsistent reconstructions. Both carry the underlying
inland-warming error. A finer implementation must use one containing-pixel
rule in the map and card.

The morning metadata was captured on 2026-10-17: chunks 80/82, build 06:05 UTC. Their
360-day local-calendar windows are 2019-09-25…2020-09-18 and
2021-09-22…2022-09-16. The live `points.bin` hash matches the SSD; the SSD's newer
climate generation has more chunks and is **not** used as today's published
baseline. Reconstruction agrees with live highs within the 1/3°C facts encoding.
See [DIAGNOSIS.md](DIAGNOSIS.md) for point indices, land fractions and provenance.

At 18:14 UTC a fresh consistent capture found chunks 76/78/80/82/84,
build 13:42 UTC, nominal 2015–2024. All 26 API checks agreed exactly with the captured
facts/scores and metadata stayed unchanged during acquisition. This later
capture is the final comparison's frozen **current published** baseline.
San Francisco's June–August card highs are now 27.3/27.3/27.3°C; Lima's
June–September highs 25.7/25.7/25.7/26.0°C. The source-selection error persists.
`published-check/` preserves the 90 MB public input snapshot, headers and hashes;
`research/fine/published.py` reproduces its capture. Matched-period comparisons
report observed-date coverage, including the absence of 2015 station dates in
our 2016–2025 evaluation.

## Publication rights checked against primary sources

Checked 2026-10-17. “Yes” means calculated corrections may be published on the
public site, subject to the stated conditions. Anonymous access was used.

| Source | Publication of derived values | Conditions / primary source |
|---|---|---|
| WorldClim 1.4 historical,30 arc-seconds | **Yes, CC BY-SA 4.0** | [Version-specific grant](https://www.worldclim.org/data/v1.4/worldclim14.html). Credit Hijmans et al., version/source, licence link and modifications; license derived correction data BY-SA and add no downstream restrictions. Application code need not adopt the data licence. |
| WorldClim 2.1 | **No without permission; private benchmark only** | [Current terms](https://worldclim.org/about.html) forbid redistribution/commercial reuse without prior permission. None of its values enter an eligible candidate. |
| CHELSA v2.1,30 arc-seconds | **Yes, CC0** | [Producer licence](https://www.chelsa-climate.org/datasets/chelsa_climatologies). Retain dataset citation and provenance. No temperature station bias correction is assumed. |
| PRISM,CONUS800 m | **Yes, current website grant** | [Current terms](https://prism.oregonstate.edu/terms/) allow redistribution. Prominent PRISM Group/Oregon State credit, source URL and access date. Current terms differ from the old noncommercial PDF. |
| DaymetV4,1 km | **Yes, NASA open-data/CC0 default** | [Dataset](https://daac.ornl.gov/DAYMET/guides/Daymet_Daily_V4.html), [NASA policy](https://www.earthdata.nasa.gov/engage/open-data-services-software/data-use-policy). Cite version/DOI; use anonymous pixel/grid routes rather than account-only Earthdata paths. |
| SILO,Australia 0.05° | **Yes, CC BY 4.0** | [Queensland grant](https://www.longpaddock.qld.gov.au/silo/about/access-data/). Attribute SILO and changes. Anonymous S3 grids; email-requiring point API excluded. |
| ERA5/Earthmover | **Yes, CC BY 4.0** | [Earthmover/AWS producer listing](https://registry.opendata.aws/earthmover-era5/), [ERA5 catalogue](https://cds.climate.copernicus.eu/datasets/reanalysis-era5-single-levels?tab=overview). Credit ECMWF/C3S, ERA5 DOI and mirror, identify modifications/no endorsement. |
| Planette ERA5 monthly **mean** | **Yes, CC BY 4.0** | [Producer repository](https://github.com/PlanetteAI/planette_era5_archive). Credit ERA5/C3S, NSF-NCAR and Planette, licence/source/changes. Its labelled max/min fields are not daily extrema; excluded for that purpose. |
| GHCN-Daily, verified US federal observations | **Yes, origin-filtered** | [Dataset](https://www.ncei.noaa.gov/access/metadata/landing-page/bin/iso?id=gov.noaa.ncdc:C00861), [source flags](https://www.ncei.noaa.gov/pub/data/ghcn/daily/readme.txt). Preserve contributor/source identity; US location alone does not establish federal authorship. |
| GHCN-Daily, foreign contributors | **Conflicting notices; private evaluation only here** | [Current AWS entry](https://registry.opendata.aws/noaa-ghcn/) says CC0, but the current product still links [documentation restricting foreign derived redistribution](https://www.ncei.noaa.gov/pub/data/cdo/documentation/GHCND_documentation.pdf). No assumption that NOAA hosting clears every contributor's rights. |
| Current GHCN-hourlyv 1 | **Yes, explicit worldwide CC0** | [NOAA C01688 metadata](https://www.ncei.noaa.gov/access/metadata/landing-page/bin/iso?id=gov.noaa.ncdc:C01688), [NOAA AWS entry](https://registry.opendata.aws/noaa-ghcnh/). Current product grant independently covers this product. Cite dataset/DOI/subset. |
| Legacy ISD/GSOD | **Foreign fitting not cleared; private validation** | [Legacy GSOD documentation](https://www.ncei.noaa.gov/data/global-summary-of-the-day/doc/readme.txt) retains foreign-derived restrictions. Do not apply GHCNh's different licence to these files. |
| GMTED2010 terrain | **Yes** | [USGS terms](https://topotools.cr.usgs.gov/gmted_viewer/gmted2010_fgdc_metadata.html). Credit USGS, describe changes/no endorsement. Anonymous global downloads. |
| CopernicusDEM | **Yes, specific licence notices** | [Producer collection](https://dataspace.copernicus.eu/explore-data/data-collections/copernicus-contributing-missions/collections-description/COP-DEM). Retain adapted-data copyright/licence/liability notices. Anonymous S3 route exists. |
| Natural Earth land + minor islands | **Yes, public domain** | [Producer terms](https://www.naturalearthdata.com/about/terms-of-use/). Both layers needed;10 m means 1:10 million map scale, not 10 metres. A small mask proposal, not complete survey-grade coastal truth. |

No accounts, API keys, purchases, emails or requests for permission were used.
TerraClimate's current primary release was not cleared as a workaround for
WorldClim 2.1. MERIT requires registration/email and is excluded. Full links,
licence distinctions and attribution requirements are in
[LICENCES.md](LICENCES.md) and the committed `research/fine/LICENCES.md`.

## Experiment

The earlier 15 stations are retained. Additional evaluation includes San Diego,
LAX, Santa Barbara, Quintero near Valparaíso, Seville, Souda near Chania, Las Vegas
and Don Muang near Bangkok. Agadir has no usable matching GHCNd daily station;
hourly observations cover Al Massira airport, about 21 km southeast of the city.
All evaluation station IDs and stations within 5 km are excluded from training
and training cross-validation.422 separate training stations were acquired.

The reference evaluation is 2016–2025. For daily instrument extrema, a month-year
needs at least 80% QA-passing days and a strict calendar-month normal needs eight
qualifying years. A separately labelled sensitivity needs five. Sparse records
remain diagnostic; none disappear silently. ERA5 and observations use matched
local dates. GHCNd reporting days can differ from midnight calendar days; this
is a remaining daily-comparison limitation.

For sparse daily sites, independent hourly files cover ten targets, including
Lima, Cape Town, Funchal, Gran Canaria and Agadir. Highs/lows require at least 18
observed hours per day and are compared with ERA5 at exactly those same hours.
They are **sampled extrema**, not instrument daily extrema. Actual 21:00 air
temperature is tested separately, using the nearest observation within 30 minutes.
Source coordinates must be within 5 km of the sampled grid location. Chania has
conflicting current primary coordinates and remains diagnostic. Quintero is a
coastal proxy 31 km north of Valparaíso; airport results are not city-centre truth.

### Candidate corrections

At each station, independently sample the source's containing 1 km high and low
normal. No temperature from the held-out station is used to modify that grid.
For each calendar month and element, compare:

1. **Source-period extrema anomaly:** matched recent raw ERA5 daily high/low plus
   `(fine historical high/low − native ERA5 high/low over that source period)`.
   Source periods: WorldClim 1.4 nominal 1960–1990, CHELSA1981–2010,
   PRISM/Daymet 1991–2020.
2. **Common mean-temperature period anomaly:** fine historical high/low plus
   the change in native ERA5 monthly mean temperature to 2016–2025, then add the
   paired recent day's high/low anomaly around its modernERA5 normal. This is
   practical globally with a small monthly-mean archive. A separate
   `available_mean_anomaly` variant uses the exact current five published chunks
   for the contemporary mean/extrema normals, rather than the full 2016–2025
   hourly history. Those native temperatures are already on the SSD. Only
   historical monthly mean temperature needs an additional global download.
3. **Uniform shift:** average the two extrema offsets and apply it to every
   hour. This tests whether the current single-offset format is adequate.
4. **Centred 5×5 smoothing:** repeat with approximately 5 km input averages. This
   is a local aggregation sensitivity, not a validated aligned 5 km output grid.
5. **Ordered available-period correction:** use method 2’s independent offsets,
   except where corrected daily high would be no greater than corrected low.
   On that day, apply the average offset uniformly at every hour. Same rule
   everywhere; no tuned threshold, clamp or additional fitting. Retain counts.
6. **Monthly affine correction:** map the available-period native mean low and
   high to the warmed fine low and high with one positive linear transform.
   This preserves hourly ordering, but scales day-to-day anomalies. Inspect
   monthly errors, daily errors and extreme transformed temperatures together.
7. **Private station residual experiment:** CHELSA plus interpolated monthly
   station residuals from up to eight neighbours within 500 km. Distance scales
   50/150/300 km are selected using training-only leave-station-and 5 km-out CV.
   Global foreign-daily results are private. A source-filtered US fit is shown
   separately. No extra tuning follows the evaluation results.

At 21:00, dual corrections interpolate the high/low shifts by the raw ERA5 day's
normalised temperature position between its minimum and maximum. They are
evaluated against actual 21:00 observations, rather than treating nightly minima
as the walk-home temperature. Corrected hourly extrema and any inverted daily
ranges are checked directly. The affine variant is evaluated separately,
without clamping or tuning to the held-out observations. These air-temperature tests do not prove dew-point,
sunshine, wind or final feels-like accuracy.

WorldClim 1.4's original fitting used 14,835 extrema stations and sometimes normals
outside its nominal 1960–1990 window. Existing regional station grids may also
contain evaluation stations. Therefore this is a held-out test of **our fitting**
and a later-period validation of published grids; it is not an independent
spatial holdout of their original construction.

## Station results

All errors are °C MAE of monthly mean temperatures. The main daily table uses
**the same 13 complete stations and 156 station-months per element**; daily
instrument extrema and hourly-sampled extrema remain separate.

| Daily-instrument comparison | High MAE | Low MAE |
|---|---:|---:|
| Raw native ERA5 | 2.31 | 1.01 |
| Current coastal/height algorithm on matched observation dates | 2.05 | 1.36 |
| Actual captured published high, different historical sample | 2.10 | not a stored low |
| CHELSA, practical available-period correction | 1.83 | 1.07 |
| WorldClim 1.4, practical independent offsets | 0.77 | 0.77 |
| WorldClim 1.4, centred 5×5 practical offsets | 0.78 | 0.80 |
| WorldClim 1.4, practical affine transform | 0.81 | 0.80 |
| **Recommended fixed source policy, ordered correction** | **0.64** | **0.72** |
| WorldClim 1.4, historical extrema reference calculation | 0.61 | 0.72 |

The historical-extrema reference is **not** the advertised global-build result:
it would need historical ERA5 daily extrema everywhere. The practical result
uses existing recent chunks plus a small historical monthly-mean archive.
On all accepted strict months, including partial-year stations, the recommended
policy gives 0.66°C high MAE (160 months,14 stations) and 0.73°C low MAE
(160 months,15 stations). Relaxing to five complete years gives 0.67/0.73°C.

Across the same five contiguous-US stations and 60 months, practical PRISM gives
0.56/0.76°C high/low MAE versus WorldClim 1.4's 0.89/0.88°C. This supports the
regional source policy; it is not a per-station switch to the winning source.
The US-source-filtered residual fit gave 0.64/1.63°C in its reference comparison,
so it is not an improvement worth building ahead of PRISM.

#### Complete daily stations

Each row has twelve accepted months for each element. “Current algorithm”
runs the current source selection/height rule on the exact observation dates;
it is not the rounded published card with its different year sample.

| Station | Raw high / low | Current algorithm high / low | Recommended high / low |
|---|---:|---:|---:|
| Málaga | 3.00 / 1.26 | 0.77 / 0.97 | 0.49 / 0.78 |
| Tenerife South | 5.31 / 0.60 | 3.77 / 2.10 | 1.14 / 0.33 |
| Sydney | 3.50 / 1.79 | 0.99 / 0.60 | 0.43 / 0.88 |
| Perth | 2.01 / 0.38 | 1.09 / 1.30 | 1.18 / 0.47 |
| Honolulu | 4.10 / 1.32 | 1.25 / 0.74 | 0.21 / 1.40 |
| Rotterdam | 0.82 / 0.71 | 0.82 / 0.71 | 0.85 / 0.65 |
| London Heathrow | 1.82 / 0.43 | 1.82 / 0.43 | 0.60 / 0.73 |
| San Francisco Downtown | 1.40 / 0.32 | 3.87 / 1.50 | 0.83 / 0.42 |
| San Diego | 2.14 / 0.98 | 1.05 / 0.54 | 0.72 / 0.43 |
| Los Angeles coastal airport | 1.07 / 0.21 | 3.65 / 0.61 | 0.25 / 0.34 |
| Santa Barbara | 1.64 / 2.14 | 4.39 / 5.12 | 0.75 / 0.98 |
| Seville | 0.97 / 0.25 | 0.97 / 0.25 | 0.68 / 0.38 |
| Las Vegas | 2.27 / 2.77 | 2.27 / 2.77 | 0.22 / 1.63 |

San Francisco illustrates the visible correction. Observed June/July/August
highs are 19.10/18.79/20.13°C; the frozen live card says 27.3/27.3/27.3°C. The
recommended PRISM route predicts 20.13/20.12/21.28°C on the observation dates.
The source error is substantially reduced, with roughly 1°C summer warmth bias
still visible. WorldClim happens to do better at this individual station;
PRISM wins the common US cohort, including LAX. Do not silently choose by
held-out station result.

#### Actual evening and sampled-extrema check

The same eight strict hourly sites contribute 96 station-months,26,045 paired
extrema-days and 26,394 paired 21:00 nights. Funchal uses the fixed WorldClim
window rule; the other seven use exact WorldClim. This cohort contains no US
site, so it does **not** independently validate PRISM at 21:00.

| Hourly cohort / fixed source policy | Monthly high | Monthly low | Monthly 21:00 | Daily high | Daily low | Daily 21:00 |
|---|---:|---:|---:|---:|---:|---:|
| Raw ERA5 | 1.97 | 1.22 | 1.22 | 2.17 | 1.43 | 1.50 |
| Current algorithm, matched hours/dates | 1.81 | 0.98 | 1.32 | 2.08 | 1.36 | 1.62 |
| **Recommended ordered correction** | **0.70** | **1.01** | **0.79** | **1.18** | **1.28** | **1.22** |
| Uniform shift | 1.18 | 0.79 | 0.56 | 1.51 | 1.08 | 0.96 |
| Affine transform | 0.76 | 1.04 | 0.81 | 1.44 | 1.39 | 1.31 |

A uniform shift is better at 21:00 in this cohort but much worse for highs,
including large errors at Honolulu, Tenerife and Sydney in the daily evidence.
Choose independent anchors to address the main daytime bias and true high/low
range. **Do not claim a universally best walk-home correction.** The air 21
validation also does not establish felt 21 accuracy.

Material exceptions:

- Lima's ordered 21:00 MAE is 1.63°C: better than the current 2.77°C but worse than
  native ERA5's 0.93°C. Uniform gives 0.87°C. Its sampled-high error improves from
  current 4.09°C to 0.71°C.
- Bangkok worsens from raw/current 0.74°C to 1.18°C at 21:00. The raw grid is already
  quite good there. This is a limitation of the global source rule.
- Funchal's WorldClim window gives 0.22/1.01/0.70°C high/low/21:00 error versus
  CHELSA2.38/0.40/0.87°C; current 21:00 error is only 0.40°C. The window helps the
  daytime anchor but is supported by only one missing-exact-pixel test site.
- Quintero/Valparaíso and Chania remain diagnostic. Quintero's sparse daily high
  error falls from current 5.46°C to 1.21°C; its hourly 21:00 error falls from 3.09°C
  to 1.12°C, versus raw 0.30°C. These are not strict city-centre results.
- Small regressions also remain in complete stations: e.g. Perth's monthly-high
  error 1.09→1.18°C and Rotterdam 0.82→0.85°C. No source wins everywhere.

#### Why the ordering exception is necessary

Before repair, WorldClim's additive offsets cross on 12 of 84,019 complete
2016–2025 days (Rotterdam 11, Seville 1), with none at SF or the other US sites.
PRISM has zero of 18,265 days. CHELSA crosses on 59 of 87,672 days. Switching
only those days to their average offset removes the crossed ranges without
altering ordinary days or multiplying anomalies. The rule changes method
at a physical zero-range boundary; record that switch in global build audits.

The affine alternative also preserves ordering, but multiplies weather-day
anomalies: a model high reaches 49.69°C at Cape Town, and uncertain Chania spans
−14.91…68.84°C. Those are transformed model values, not station records. Its
hourly daily errors are worse; do not choose it for its monthly averages.

#### Restricted benchmark and residual experiment

On the same eight complete stations where the earlier private WorldClim 2.1
benchmark is available, its historical-extrema reference gives 0.49/0.60°C
high/low MAE; WorldClim 1.4 gives 0.56/0.62°C, and its practical available-period
version 0.70/0.68°C. The private global station-residual experiment gives
1.08/0.73°C. Thus the licensed old grid is competitive, but the benchmark still
shows room to improve. No WorldClim 2.1 value or foreign-daily fitted residual
enters the recommended correction.

Every requested location, station ID, sparse/proxy result, per-source error and
coverage flag is retained in [station-results.md](station-results.md) and
[hourly-results.md](hourly-results.md). Full monthly outputs are
`monthly_comparison.csv` and `hourly_comparison.csv` in the report folder;
`comparison_metadata.json` records source hashes, date windows and the frozen
published baseline. Small error summaries are retained with the committed report.


## Resolution and delivery

Preserve 30 arc-second input detail (about 926 m north–south; narrower east–west
with latitude). Do not describe source pixel size as demonstrated 1 km accuracy.
WorldClim 1.4's actual global header is 43200×18000,90°N to 60°S. CHELSA is
43200×20880,84°N to 90°S, and has Antarctic values. CHELSA also contains ocean
pixels, so its nonmissing mask cannot define land.

WorldClim 1.4 is missing at the exact Funchal station pixel in every month.
Its centred 5×5 mean is a separately evaluated method, not an implicit nearest
land substitution. A global source policy must label missing exact anchors.

An independent land mask is needed. Natural Earth main land plus its separate
minor-islands layer is a small public-domain starting point (~3.4 MB), with known
cartographic limitations. Do not use this coarse mask to discard valid fine
coastal/island pixels: retain source tiles through uncertain coastlines, and
mask their display separately. Full global source rasters are small enough on
the SSD to avoid making a coarse shoreline a data-acquisition filter. Retain
explicit coverage metadata. Where a land pixel
has no eligible fine anchor, retain native ERA5 with zero fine residual; do not
reuse the current sea-level/inland correction or invent fine confidence.
Antarctic fine-source coverage would be CHELSA, without station accuracy proof
from this temperate-travel evaluation. Adding Antarctica to the site itself is
outside this Phase 1 task; the current product deliberately excludes it.

The present global 0.25° PNG cannot preserve 1 km detail. Retain the small coarse
global overview and 15-byte day records; add tiled monthly **two-channel air
temperature** corrections, independently for high and low, for visible close
views and exact place-card pixels. A256×256 tile with two signedInt16 values at
0.1°C is 262,144 bytes/month raw (3.15 MB/year). Use explicit grid origin, scale,
no-data, source and period metadata. A small source/support map, shared across months, distinguishes PRISM, exact
WorldClim, its coarser window, CHELSA and native-only coverage. This avoids
treating a missing fine correction as fine-source coverage.

Actual source-tile experiments measured 37–57 KB for a 256² opaqueRGB PNG packing
both monthly high/low values at 0.1°C, or roughly 0.45–0.68 MB for twelve months of
one tile. These are source-normal tiles, **not** a measured complete-world
residual bundle or production encoding choice. A binaryInt16 tile with HTTP
compression is simpler to type and range-read; measure its residual compression
before choosing the final wire encoding. Whole-world 24-bandInt16 storage at
CHELSA geometry is 43.30 GB raw including ocean, before tiling/compression.

Around SF the old-source high changes 18.7–26.2°C across the coast-to-inland
transect. Centred 5×5 smoothing changes individual samples by up to 1.37°C there
and 0.63°C near Valparaíso. This proves potentially visible information is lost;
station comparisons determine whether exact pixels actually improve accuracy.
See [resolution.json](results/resolution.json) and `research/fine/resolution.py`.

**Both consumers need a change.** The current GPU lookup scores only 11 uniform
shifts; two anchors require direct scoring of visible fine pixels or a 2D
lookup. The card currently reads precomputed 0.25° facts and cannot gain 1 km
accuracy by changing the PNG alone. It must use the same containing fine pixel,
hourly interpolation and corrected facts as the map. Carry validated diurnal
weights if retaining compact felt records; daily low alone is insufficient.
The ten felt fields use **eight unique air-temperature hours**, so an eight-byte
per-representative-day weight table is 14.3 MB/year for 18,667 cells and eight
days/month. It also needs the raw daily air range for the ordering decision,
coarse monthly anchors, and sufficient air/dew information for the humidity
cap. These estimates are not the complete final carrier size. A monthly
climatological-weight approximation would be smaller but needs its own error
check. Neither proves native-point recovery from spatially averaged 1° records. Before
locking the wire format, reconstruct representative 1 km coastal/mountain points
from the actual coarse carrier, compare with the tested corrected hourly values,
and include the dew-point cap in felt-temperature reconstruction. Additional
air/dew fields may be necessary; a second PNG alone is insufficient.

The forecast currently reuses monthly climate offsets. These tests validate
ERA5-based climatology, not ECMWF forecast skill; a fine climatology’s station
error must not be presented as a measured fourteen-day forecast error.

Browser timing for this extension is **not yet measured**. One 256² tile ×8 days
×11 hours is 5.77 million hourly-grade evaluations/month before coarse interpolation.
A121-level two-anchor lookup would cost about 11 times the present scoring pass.
Prefer an isolated visible-tile implementation and measure slider redraw at
phone, laptop and ultrawide sizes during Phase 2 before choosing the GPU path.
Keep fast coarse first paint while fine tiles load. No interface work is done
here while another worker owns that surface.

## Run time and reproducibility

Measured on this Mac, with several acquisitions running concurrently:

| Operation | Measured wall time | Scope |
|---|---:|---|
| GHCN-Daily acquisition and summaries | 834.6 s (13.9 min) | 445 initial station files; zero download failures; later cached metadata/summary update 26.6 s |
| CHELSA point sampling | 347.8 s (5.8 min) | 445 initial points, ~119.9 MB ranged bytes; final 446-point cached rerun<1 s |
| WorldClim 1.4 selected tiles and sampling | 1074.3 s (17.9 min) | 667.2 MB selected ZIPs; later updated cached evaluation 2.8 s |
| Native ERA5 historical point histories | 2777.2 s (46.3 min) | 23 initial locations,1960–2025;1340 shared chunk reads;6.72 GB **decoded**, not measured transferred bytes |
| Current algorithm's source histories | 683.6 s (11.4 min) | 23 initial points,2015-12-30 onward |
| Corrected Chania coordinate | 398.6 s (6.6 min) | Native history in separate scratch folder; preserved earlier location |
| Agadir airport addition | 336.9 s (5.6 min) | Native and product-source histories |
| Consistent live capture and 26 API checks | 39.8 s | ~90 MB public binary snapshot |
| Resolution/encoding probes | 17.3 s initial;~1 s cached | Bounded terrain/coastal tiles; not whole-world throughput |

Daily comparison reduction:188.48 s initial,188.05 s for the ordered candidate.
Hourly comparison:123.76 s initial,125.55 s for the ordered candidate, plus 0.43 s
for fixed-source composite reduction. Source/repair summaries are retained.
Selected hourly inputs total 305.7 MB and took 358 s initially; the final 18.0 s
cached rebuild is separate. Its summary records every file/byte.

For a later global build, the full WorldClim 1.4 high+low ZIPs total 3.127 GB by
verified HTTP lengths. All 24 CHELSA monthly high/low COGs total 3.841 GB if needed.
A historical monthly ERA5 **mean** baseline needs roughly 1.3 GB compressed.
The five complete recent native ERA5 chunks already on the SSD contain~125 GB;
stream those existing files to calculate contemporary normals. Do not download
decades of global hourly history. These core additional source downloads total
roughly 8.3 GB before any selected regional grid. Intermediate rasters/output need
more SSD space;1.6 TB was free at the Phase 1 check. Whole-world CPU/GPU build and
browser times have not been measured, so no full-build completion estimate is
claimed from this point-sampling benchmark.

Scripts are in `research/fine`; large samples are on the SSD under `fine-phase1`.
No old climate output was overwritten.

## Phase 2 boundary

Stop after this report and script commit. After Josh's go-ahead, build a new
dataset beside SSD `v6`, with a rerunnable `research/fine` command, source hashes,
coverage, licences and attribution. Never build into `artifacts/v6` and never
publish restricted benchmark or private foreign-daily fitted values. Regenerate
corrected air/felt values, facts, default scores and map detail consistently,
with native local dew point instead of the inland/height-shifted dew point.

Attribution template for a WorldClim 1.4-derived dataset:

> Temperature detail derived from WorldClim 1.4 (Hijmans et al.,2005), licensed
> under CC BY-SA 4.0; climate-period adjustments from ERA5/Copernicus Climate
> Change Service/ECMWF, via Earthmover and Planette. Nice Weather modified the
> data by period adjustment, resampling and temperature correction. Source and
> licence links accompany the dataset. This derived temperature dataset is
> available under CC BY-SA 4.0. No source organisation endorses Nice Weather.

Add PRISM Group/Oregon State URL and access date where PRISM is used; the
CHELSA/Daymet/SILO credits and modification notices follow their selected-source
conditions. Keep the exact attribution beside the published derivative and on
the public method/data page in Phase 2.
