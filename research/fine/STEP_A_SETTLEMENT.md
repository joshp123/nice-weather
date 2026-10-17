Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Step A: settlement representation

This experiment tests Josh's request to represent the populated part of each
quarter-degree point without place-specific exceptions. It changes no site
consumer, production data, running home job or download script. Step B is paused.

The original Step A used the single 1 km anchor at the quarter-point coordinate,
not the mean of its footprint. That coordinate can fall inland above a coastal
town. For Funchal's selected point (32.75, -17.00), the July period-adjusted
centre high/low anchors were 21.56/14.86 °C. An area average gives 22.27/15.75;
resident weighting gives 24.06/18.00. The corresponding uniform air shifts are
-2.53, -1.73 and +0.29 °C. Population weighting fixes much of this geographic
mismatch, but does not establish station accuracy by itself.

## One global rule

Use the 30 × 30 canonical 30 arcsecond pixels in the nearest quarter-point's
Voronoi footprint, ±0.125° in latitude and longitude. Weight eligible paired
high/low anchors by GHSL 2020 resident counts. Counts already describe people
per source pixel; do not multiply them by area. Use the source raster's actual
georeferencing rather than assuming its edges coincide with the climate grid.
Positive population retains a pixel even if the coastline geometry misses it.

Where the footprint has no residents, use land-area weights (cosine latitude).
Independent land support is Natural Earth public-domain geometry or paired
WorldClim/PRISM validity: bits 0–2 of the retained support raster. OSM-only
bit 3 is excluded from temperature averaging. With no eligible land, retain
that point's own native ERA5 high/low. Missing climate anchors retain their
population/area weight with the own native high/low; they are not discarded.

Each fine anchor uses the approved PRISM → WorldClim 1.4 → centred 5×5
WorldClim fallback → CHELSA → native hierarchy. Apply its common monthly-mean
ERA5 period anomaly before averaging. The modern reference uses exactly the
assembled chunk dates; missing nonzero-weight ERA5 corners are errors, never
an invitation to borrow inland weather. Exact selected-point native means can
fill the same coordinates missing from the wider reference cache.

The existing format still has one monthly uniform shift, the arithmetic mean
of the resulting high and low offsets. Recompute felt temperature with the
corrected-air vapor cap and native dew point, wind and radiation. Native
cell-day records and the rebuilt native points remain unchanged. No validation
station measurement, fitted station shift or place-specific parameter enters
this rule.

GHSL source terms, citations and the anonymous downloader are recorded in
[settlement-LICENCE.md](settlement-LICENCE.md). The complete bundle notice is
[ATTRIBUTION-STEP-A-SETTLEMENT.txt](ATTRIBUTION-STEP-A-SETTLEMENT.txt).

## Reproduce the experiment

Run from the repository in its existing devenv. Use fresh generation roots if
inputs, selected chunks or scripts change. Native five-chunk intermediates
were already rebuilt from raw in Step A and can be reused unchanged here.

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh settlement_sources.py
sh research/fine/run.sh settlement_anchors.py \
  --output "$SSD/fine-build/settlement-v2/adjustment.npz" \
  --summary "$SSD/fine-build/settlement-v2/summary.json"
sh research/fine/run.sh build_step_a.py \
  --native-root "$SSD/fine-build/native" \
  --normals "$SSD/fine-build/normals-76-78-80-82-84/normals.npz" \
  --source-cache "$SSD/fine-sources" \
  --meta "$SSD/fine-build/native/selected-meta.json" \
  --adjustments "$SSD/fine-build/settlement-v2/adjustment.npz" \
  --adjustment-summary "$SSD/fine-build/settlement-v2/summary.json" \
  --attribution research/fine/ATTRIBUTION-STEP-A-SETTLEMENT.txt \
  --build-root "$SSD/fine-build/step-a-settlement" \
  --output "$SSD/v6-fine-step-a-settlement" --workers 4
```

Before a full build, the same CLI can preflight reference coverage without
sampling climate anchors:

```sh
sh research/fine/run.sh settlement_anchors.py \
  --output "$SSD/fine-build/settlement-v2/adjustment.npz" \
  --reference-preflight "$SSD/fine-build/settlement-v2/reference-preflight.json"
```

For these five chunks, 250,001 overlapping selected-point modern means match
the wider reference bit for bit. Ten exact own-point means fill missing
coordinates. The complete footprint preflight requires 271,533 corners and
finds none missing; no extra download or spatial substitution is needed.

The preparation binds population, climate sources, land support, reference
normals, native normals and scripts to the adjustment. The builder checks
adjustment identity, native point order and chunk set, regenerates current-format
facts/offsets/cells, then calls the existing default score/image generator.
Its manifest binds generated files and the exact scoring implementation.
Changing the assembled chunk set requires new native normals, matching global
reference normals and a complete new settlement adjustment and assembly.

## Validation

The acceptance comparison uses the same frozen thirteen places, instruments,
station dates and current-product values as the first Step A report. Monthly
high and 21:00 mean absolute errors must each stay within current +0.3 °C.
High compares the product's 10–20h maximum with station 24h TMAX, matching the
original report. San Francisco has only the labelled air21 waterfront proxy;
observed felt21 remains unavailable. Sparse/proxy instruments remain labelled.
The full report retains all twelve months, both city and instrument queries,
coverage and coordinates. It does not infer a full observed comfort score.

`settlement_feasibility.py` independently replays raw hourly native weather
before the compact-file comparison. Its private station-oracle diagnostic
shows that arbitrary monthly shifts could meet annual budgets; failure of the
fixed geographic rule does not prove every uniform correction impossible.
`settlement_report.py` checks that station observations and the current
baseline have not changed before applying the acceptance limits. Validation
outputs stay on the SSD outside publication folders.

The exact inputs for this comparison have been frozen outside the repository:
`$SSD/fine-validation/step-a-settlement/inputs` contains the thirteen daily
instruments, station metadata, monthly definitions, original hourly cache and
city queries. `rebuild-work/hourly*` retains the additional hourly aliases and
waterfront proxy. `inputs/frozen-input-sha256.json` records all 374 source-file
hashes. This avoids depending on temporary directories or a later NOAA update.

```sh
VALIDATION="$SSD/fine-validation/step-a-settlement"
sh research/fine/run.sh compare_step_a.py \
  --current-root "$SSD/v6" --step-a-root "$SSD/v6-fine-step-a-settlement" \
  --observations "$VALIDATION/inputs/stations" \
  --base-hourly "$VALIDATION/inputs/base-hourly" \
  --queries "$VALIDATION/inputs/city_queries.json" \
  --output "$VALIDATION/rebuild-work" \
  --adjustments "$SSD/fine-build/step-a-settlement/adjustment.npz" \
  --skip-live-verification --private-report-output "$VALIDATION/rebuilt"
sh research/fine/run.sh settlement_report.py \
  --original "$SSD/fine-validation/step-a/monthly_step_a.csv" \
  --candidate "$VALIDATION/rebuilt/monthly_step_a.csv" \
  --output "$VALIDATION/rebuilt"
```

`--skip-live-verification` intentionally retains the first Step A report's
frozen current-product baseline; its binary hashes were checked unchanged
before this replay. Comparison of the rebuilt facts took 33.6 seconds.
The acceptance script also requires identical observation,
current-product and query columns for all 156 monthly instrument comparisons.

## Delivered generation and decision

The isolated bundle is `$SSD/v6-fine-step-a-settlement`. The production
`v6`, original Step A bundle, home job and interface were not changed.

The global adjustment covers all 250,011 existing points: 162,654 use resident
counts, 85,470 use land area, and 1,887 retain native values because neither
support exists. Anchor preparation took 752.9 seconds. Correction, assembly,
scores and the current generator's 133 score images took 344.2 seconds.
All 186 generated file hashes were verified; the added rejection marker brings
the final manifest to 187 verified files. Points, cell geometry and all
twelve cell-month files are byte-identical to the original native Step A;
dew-point, wet-day, sunshine and wind fact bytes are unchanged. All sixteen
city/instrument quarter points match the separate population-rule probe
bit for bit.

**Do not publish either Step A candidate.** Population weighting repairs much
of the inland-anchor mismatch, but the rebuilt facts fail Josh's per-place
non-regression limits. This is a failure of the tested rule, not proof that
every possible uniform correction is impossible. The fixed inputs were not
tuned against the observed errors. The detailed station comparison, coverage,
all twelve months and exact acceptance results remain private at:

- `$SSD/fine-validation/step-a-settlement/rebuilt/SETTLEMENT_COMPARISON.md`
- `$SSD/fine-validation/step-a-settlement/rebuilt/monthly_step_a.csv`
- `$SSD/fine-validation/step-a-settlement/rebuilt/settlement-acceptance.json`
- `$SSD/fine-validation/step-a-settlement/rebuilt/bundle-audit.json`

Native humidity and wind also differ from the declared coastal instrument;
population-weighted air-temperature anchors cannot correct those inputs.
The retained raw replay decomposes that evening discrepancy separately from
the geographic anchor effect. No humidity or wind correction was introduced.

The new bundle carries `NOT_FOR_PUBLICATION.txt` and a failed validation
reference in `fine-build.json`. No publication or deployment was attempted.
Step B remains paused.
