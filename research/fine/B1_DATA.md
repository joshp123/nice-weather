Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The exact 1 km candidate fails the city non-regression test. Do not publish it. It improves every city's high-temperature error, but makes Funchal, San Diego and Seville's 21:00 error worse by more than 0.3°C. Rotterdam's evening error increases by 0.09°C. The retained native humidity, wind and daily temperature shape still matter at an exact source pixel. No station fitting or per-place adjustment was introduced.

The diagnostic bundle is on the SSD at `fine-b1-diagnostic`, with thirteen native-point replay packets, not worldwide replay coverage. Each gzip JSON packet contains all 1,800 assembled days for physical means, native 24-hour high/low, twelve hourly air/dew/sun/shade temperatures, native rain/wind/sunshine/daylight, and 96 selected indices for the existing eight representative days per month. Daily selection uses the native geometric 1° cell's existing encoded-score quantiles; an absent land cell uses its own native point. One module under `hosting/sites` corrects these records with the exact containing 30 arc-second correction tile and applies the current typical-day rule. No existing overview map data is changed.

Packets take about six seconds to build and total 6.64 MB compressed. This full-day JSON carrier is a diagnostic representation. A worldwide carrier should retain compact selected-day score records and six full-period response fields per day (24-hour high/low, daytime maximum, air21, dew21 and wind21). That preserves physical means without sending full hourly histories for every native point. It has not been globally built, because the acceptance gate failed.

The actual delivered-byte comparison is at `fine-validation/b1/decoded-bytes/COMPARISON.md` and `monthly.csv`; `acceptance.json` records every consumed packet/tile hash. All 312 frozen current high/felt21 values were checked against a fresh live API capture and are identical. Independent Python replay also matches all 312 shared-module monthly high/felt values and 1,092 monthly judgement scores, shares and counts within 0.000001. San Francisco retains its separate air21 waterfront proxy; all other evening comparisons use felt21. City cards are compared to the same instruments as before, retaining their city/instrument displacement and observation coverage. Sparse missing observations stay missing. The product high covers10–20h while station TMAX covers24h.

To rebuild the scoped candidate, from the repository root:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh b1_data.py \
  --raw-root "$SSD/fine-build/reference-global/era5" \
  --reference-root "$SSD/fine-build/reference-global" \
  --native-root "$SSD/fine-build/native" \
  --tiles "$SSD/fine-30s" \
  --queries "$SSD/fine-validation/step-a-settlement/inputs/city_queries.json" \
  --output "$SSD/fine-b1-diagnostic"
sh research/fine/run.sh b1_validate.py \
  --bundle "$SSD/fine-b1-diagnostic" \
  --tiles "$SSD/fine-30s" \
  --baseline "$SSD/fine-validation/step-a/monthly_step_a.csv" \
  --today-capture "$SSD/fine-validation/b1/today/capture.json" \
  --module-results "$SSD/fine-validation/b1/module-all-months.jsonl" \
  --output "$SSD/fine-validation/b1/decoded-bytes"
```

The opt-in local overlay pointer is `v6/fine/current.json`, containing:

```json
{"nativeRoot":"fine-b1-diagnostic","tileRoot":"fine-30s","status":"candidate-rejected"}
```

It must not be added to production. The pointer is absent by default, so the published site continues using today's data. The bundle manifest lists its thirteen `nativeFineIndices`; other points are unavailable to this diagnostic. Correction and replay generations/chunk identities must agree. No source licence or station observations are embedded in the replay values; native records are derived from ERA5, while correction tiles carry their separate source/licence provenance.
