Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Hourly station evidence: high, low and the 21:00 walk home

WorldClim 1.4 provides a much better daytime temperature anchor than the current coastal-source rule in this cohort. How we apply that anchor matters: separate high/low offsets give the best highs, while a uniform offset gives the best lows and 21:00 air temperature. The monthly affine mapping preserves ordering but amplifies day-to-day anomalies; it is not an accuracy winner. These results support a global, explicitly chosen tradeoff, not a different winning method at each station.

This validates **air temperature**, including the actual 21:00 observation. It does not validate felt temperature, wind, dew point, fog, sunshine or the resulting comfort score. NOAA hourly sampled extrema are also different from instrument daily extrema; the separate daily-station experiment supplies that evidence.

## Comparison and cohort

Ten requested locations have hourly records. Predictions and observations use the same local calendar date and the same observed whole UTC hours. A report must be within 30 minutes of the hour; sampled extrema require at least 18 temperature hours/day, and the 10:00–20:00 high additionally requires 9 of those 11 hours. Station coordinates must be within 5 km of the evaluated point. No station observations fit or tune any candidate.

For the strict comparison, a month-year needs observations on at least 80% of calendar days, and each calendar month needs at least five qualifying years. Means exclude the sparse month-years. The main common cohort contains seven sites, 84 station-months, 22,607 paired extrema-days, 22,970 paired 21:00 nights and 22,276 daytime-high days in 2016–2025: Agadir airport, Bangkok, Cape Town airport, Gran Canaria airport, Lima airport, Lisbon and Nairobi. Airports are evaluated at their own coordinates; this does not establish city-centre accuracy. Agadir airport is approximately 21 km southeast of the requested city.

Funchal is an eighth otherwise eligible site, but its exact WorldClim 1.4 coastal pixel is missing. CHELSA-only comparisons have 96 station-months. Chania has uncertain instrument geolocation and Quintero has insufficient dense years; both remain diagnostic. Quintero is a station approximately 31 km north of Valparaíso, not a validation of Valparaíso itself. The largest accepted hourly-site/evaluation-point separation is 1.23 km (Nairobi). No site was rejected for exceeding 5 km.

The current-algorithm column runs the published source-point selection and fixed elevation shift on the **same dates and hours** as the observations. It isolates the correction, rather than conflating it with different years. Actual published monthly values are recorded separately: the consistent live capture uses five chunks spanning 2015–2024. The 2019–2022 sensitivity output is not today's published period.

## Main common-cohort result

Values are °C. Monthly MAE is the mean absolute error of the 84 station-month means, equally weighted. The high/low columns refer to extrema sampled at the station's observed hours.

| Candidate | Monthly high MAE | Monthly low MAE | Monthly 21:00 MAE |
|---|---:|---:|---:|
| Raw native ERA5 | 1.77 | 1.08 | 1.03 |
| Current published correction algorithm | 1.88 | 1.11 | 1.45 |
| WorldClim, available-period separate offsets | 0.77 | 1.01 | 0.81 |
| WorldClim, available-period uniform offset | 1.17 | 0.85 | 0.58 |
| WorldClim, available-period affine mapping | 0.83 | 1.03 | 0.81 |
| CHELSA, available-period separate offsets | 1.76 | 0.90 | 1.18 |
| CHELSA, available-period uniform offset | 1.75 | 1.19 | 1.29 |
| CHELSA, available-period affine mapping | 1.79 | 0.92 | 1.21 |

The actual 10:00–20:00 high gives essentially the same conclusion: raw 1.77, current 1.89, WorldClim separate 0.77, uniform 1.17 and affine 0.83 °C monthly MAE. WorldClim separate offsets have a +0.31 °C high bias and −0.84 °C low bias; the uniform offset reverses that balance, with −1.13 °C high bias and +0.60 °C low bias. Neither eliminates all systematic error.

Daily MAE below is first calculated within each station-month, then averaged equally over the same 84 station-months. It measures individual weather days, rather than error after averaging away daily variation.

| Candidate | Daily high MAE | Daily low MAE | Daily 21:00 MAE |
|---|---:|---:|---:|
| Raw native ERA5 | 1.99 | 1.33 | 1.35 |
| Current published correction algorithm | 2.14 | 1.46 | 1.74 |
| WorldClim separate | 1.20 | 1.30 | 1.24 |
| WorldClim uniform | 1.50 | 1.13 | 0.98 |
| WorldClim affine | 1.41 | 1.38 | 1.32 |
| CHELSA separate | 1.87 | 1.20 | 1.40 |
| CHELSA uniform | 1.88 | 1.41 | 1.48 |
| CHELSA affine | 1.91 | 1.23 | 1.42 |

Pooling days instead of weighting station-months equally changes the WorldClim separate values to 1.20/1.31/1.24 and uniform to 1.49/1.15/0.99 °C. The conclusion is unchanged. No hourly site here samples the US, so this experiment supplies no direct PRISM 21:00 validation.

## What “available period” means

For a fine-grid high or low normal, add the difference between the available modern ERA5 **mean** and that source's historical ERA5 mean. Compare the resulting high/low anchors with the available modern ERA5 high/low normals to obtain the offsets. The available modern normal uses the exact published chunk-day subset, rather than an assumed complete 2016–2025 period. This matches the practical global route using the already downloaded chunks and historical monthly mean temperature.

Separate offsets interpolate the high and low offsets by the day's normalized native temperature. Uniform uses their average for every hour. Affine instead maps the available monthly native high/low normals directly to the warmed fine high/low anchors, multiplying each hourly anomaly by a monthly range ratio. All are untuned global rules.

Source-period sensitivity for WorldClim on the same 84 station-months:

| Reference calculation | High MAE | Low MAE | 21:00 MAE |
|---|---:|---:|---:|
| Historical source-period ERA5 high/low normals | 0.63 | 0.94 | 0.77 |
| Mean warming, full 2016–2025 native normals | 0.74 | 0.97 | 0.79 |
| Mean warming, exact available-day native normals | 0.77 | 1.01 | 0.81 |

The historical-extrema result is useful evidence about the approximation. It requires historical ERA5 high/low normals everywhere; the current practical global proposal instead needs only historical monthly **means**. Do not present its better high error as the expected production result. CHELSA's corresponding eight-site high/low/21:00 values are 1.82/0.71/1.13 (historical), 1.84/0.79/1.13 (full modern) and 1.84/0.84/1.15 (available modern).

## Resolution sensitivity

The alternative sample is a centred 5×5 average of 30-arcsecond pixels: 2.5 arcminutes across, approximately 4.63 km north–south and narrower east–west with latitude. This is not a constant 5 km equal-area grid.

| WorldClim, same 84 months | Exact pixel high/low/21:00 MAE | 5×5 mean high/low/21:00 MAE |
|---|---|---|
| Separate offsets | 0.77 / 1.01 / 0.81 | 0.74 / 1.02 / 0.82 |
| Uniform offset | 1.17 / 0.85 / 0.58 | 1.18 / 0.84 / 0.59 |
| Affine mapping | 0.83 / 1.03 / 0.81 | 0.81 / 1.04 / 0.83 |

CHELSA's eight-site changes are also small: separate 1.84/0.84/1.15 becomes 1.85/0.83/1.15. This small hourly cohort shows no consistent accuracy advantage for either exact 1 km sampling or the 5×5 average. It does not test the present 0.25° spacing, establish that 25 km is adequate, or rule out useful 1 km detail in mountains and urban coastal gradients. The broader daily-extrema cohort should determine the resolution proposal.

## Station exceptions at 21:00

Each row is monthly air-temperature MAE over 12 calendar months. Values marked diagnostic use available paired days and do not qualify for the strict result.

| Site | Raw | Current | WC separate | WC uniform | WC affine |
|---|---:|---:|---:|---:|---:|
| Agadir airport | 0.66 | 3.31 | 0.66 | 0.73 | 0.71 |
| Bangkok | 0.74 | 0.74 | 1.18 | 1.00 | 1.19 |
| Cape Town airport | 0.51 | 1.53 | 0.35 | 0.57 | 0.38 |
| Gran Canaria airport | 3.01 | 0.63 | 0.70 | 0.16 | 0.67 |
| Lima airport | 0.93 | 2.77 | 1.63 | 0.87 | 1.56 |
| Lisbon | 1.17 | 0.96 | 0.93 | 0.50 | 0.97 |
| Nairobi | 0.23 | 0.23 | 0.21 | 0.23 | 0.22 |
| Funchal | 2.55 | 0.40 | missing | missing | missing |
| Chania, diagnostic | 1.20 | 1.81 | 1.80 | 1.42 | 1.30 |
| Quintero, diagnostic | 0.30 | 3.09 | 1.12 | 0.76 | 1.18 |

Lima is a material counterexample to a general “better nighttime temperature” claim for separate offsets: they improve on the present overheating correction but are worse than native ERA5 at 21:00. Bangkok worsens under all three WorldClim corrections. The present coastal correction happens to improve Funchal and Gran Canaria substantially. A method must be judged globally, without selecting a different winner at each evaluation station.

## Ordering and anomaly audit

Among all observed dates available for the hourly audit (including diagnostic sites), exact-pixel WorldClim separate available-period offsets produce zero non-positive corrected daily ranges in 27,684 audited days. CHELSA produces 17 in 31,185 days: six at Funchal (minimum −0.67 °C) and eleven at Gran Canaria (minimum −0.34 °C). The averaged uniform offset preserves the native range. These are sampled-site audits, not a proof about all land; the broader instrument-daily experiment finds additional inversions elsewhere.

Affine mapping has no inverted ranges here, but larger anomalies. Its largest exact WorldClim monthly range ratio at a site with certain geolocation is 2.53 at Cape Town in March. A transformed Cape Town April daily hourly maximum reaches 49.69 °C; the smallest transformed value there is −1.85 °C in July. At uncertain Chania the ratio reaches 5.87 and transformed values span −14.91 to 68.84 °C. These are model audit bounds, not observed station records. Exact CHELSA reaches a ratio of 1.51 at Agadir and a transformed hourly maximum of 57.99 °C. This makes the affine mapping unattractive despite its mathematically preserved ordering.

The ordered-offset candidate uses the same available-period separate offsets everywhere, except that a day whose corrected high would be at or below its corrected low receives the averaged uniform offset at every hour. It preserves the native ordering on that day without multiplying anomalies by the monthly affine ratio. It does not selectively improve ordinary days or select a station-specific method. The rerun repaired exactly the 17 CHELSA exact-pixel days above (16 with the 5×5 CHELSA sample); WorldClim needed zero repairs, exact or 5×5. No non-positive ranges remain in these ordered hourly audits. On the seven-site common cohort, ordered WorldClim results are exactly unchanged from separate offsets. CHELSA ordered high/low/21:00 monthly MAE is 1.761/0.900/1.184 °C; all changes from its un-repaired result are below 0.001 °C. Repair counts describe all audited observation dates, not just strict monthly means.

## Availability-based composite, including Funchal

The fixed source policy is PRISM within the contiguous US, otherwise WorldClim exact pixel, otherwise the valid centred WorldClim 5×5 mean, otherwise CHELSA exact pixel, otherwise native ERA5 flagged coarse. Selection uses availability only, never observed error. In this hourly cohort that selects WorldClim exact at seven strict sites and the WorldClim 5×5 mean at Funchal. None tests the PRISM or CHELSA fallback branch. Chania and Quintero still remain diagnostic.

This gives eight strict sites and 96 station-months, with 26,045 paired extrema-days and 26,394 paired 21:00 nights. All table entries use exactly this common cohort. The “daily” columns are the equal station-month average of daily absolute errors, as above.

| Fixed source composite | Monthly high MAE | Monthly low MAE | Monthly 21:00 MAE | Daily high MAE | Daily low MAE | Daily 21:00 MAE |
|---|---:|---:|---:|---:|---:|---:|
| Raw native ERA5 | 1.97 | 1.22 | 1.22 | 2.17 | 1.43 | 1.50 |
| Current correction algorithm | 1.81 | 0.98 | 1.32 | 2.08 | 1.36 | 1.62 |
| Ordered separate offsets | 0.70 | 1.01 | 0.79 | 1.18 | 1.28 | 1.22 |
| Uniform offset | 1.18 | 0.79 | 0.56 | 1.51 | 1.08 | 0.96 |
| Affine mapping | 0.76 | 1.04 | 0.81 | 1.44 | 1.39 | 1.31 |

Ordered and original separate offsets are identical for this composite's observed days: neither WorldClim exact nor the Funchal window needed a repair. Composite ordered 10:00–20:00 high MAE is 0.70 °C monthly and 1.18 °C daily on 25,692 paired daytime-high days, versus current 1.81/2.09 °C.

Funchal makes the source tradeoff concrete. The WorldClim window's ordered high/low/21:00 monthly MAE is 0.22/1.01/0.70 °C, versus CHELSA exact ordered 2.38/0.40/0.87 °C. The WorldClim window's uniform result is 1.25/0.39/0.41 °C. Availability-based selection strongly improves Funchal's high but sacrifices some low-temperature accuracy; it is not a per-element winner. The current algorithm's Funchal 21:00 error is also only 0.40 °C.

The evidence favours the WorldClim temperature anchors over CHELSA at these coastal targets. Ordered separate offsets are the stronger daytime choice and avoid the observed range inversion path without affine amplification. A uniform offset remains the stronger choice for lows and the walk home. The final recommendation must state that tradeoff and use the broader daily-extrema and product-score evidence; this hourly test alone does not establish a universal best transformation.

## Reproducibility and limits

Run `sh research/fine/run.sh hourly_compare.py` after the documented station, published-baseline and grid/ERA5 acquisitions. The script accepts `--output` and `--scratch`; the Agadir airport definition is imported from `published.py` if the optional manual additions file is absent. It writes `hourly_comparison.csv` (individual monthly/cohort/method results) and `hourly_comparison_summary.json` (metadata, method definitions, timing, affine parameters and range/repair audits). `hourly_metrics.json` is a temporary analysis of common cohorts, not an input to the pipeline.

The first complete comparison took 123.76 seconds on this Mac, producing 52,266 rows with no unavailable site. The ordered-offset rerun took 125.55 seconds and produced 59,222 rows. Composite rows were then selected from those existing results in 0.43 seconds, avoiding another historical-data pass; final CSV size is 66,370 rows. The durable script includes the same composite source selection for fresh runs. Chosen hourly validation inputs total 305.7 MB and initially took 358 seconds to acquire; a cached reprocess took approximately 18 seconds. The durable published-baseline capture took 39.84 seconds and downloaded approximately 90 MB of live binary facts/scores/points. Cached ERA5/grid acquisition is additional and reported in the main plan.

Primary hourly input is NOAA GHCNh, with original NOAA legacy hourly files where the intended station has substantially denser usable records there. These station observations are validation inputs, never part of the published correction. GHCNh and legacy source identifiers, input URLs, QC selection, physical coordinates and year coverage are retained in `hourly/summary.json`; the licence report distinguishes their terms. Station humidity and wind are acquired where available, but this experiment makes no felt-temperature accuracy claim.
