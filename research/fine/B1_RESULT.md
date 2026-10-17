Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Exact-pixel place numbers: rejected candidate

Historical ordered-evening result. Josh subsequently accepted the uniform evening rule and city-average score gate in [B1_EVENING_RESULT.md](B1_EVENING_RESULT.md). [UPLOAD.md](UPLOAD.md) is the current worldwide release handoff. This document preserves the earlier rejected candidate.

**B1 is not proven. Do not upload or deploy this candidate. Keep today's data.**
The exact 30 arcsecond pixels improve daytime highs at all thirteen city
queries, but Funchal, San Diego and Seville fail even the previous +0.3 °C
evening-error allowance. Rotterdam also has a small regression under the
latest literal “no worse” requirement. Resolution alone does not correct the
retained native wind, humidity or local daily temperature shape.

## City acceptance

Mean absolute error across the twelve monthly physical means, °C. These are
the **city query points**, unlike the instrument-coordinate summary in the
earlier Step A response. The instruments and observation coverage are frozen.
The high is the model's 10–20h maximum versus observed 24h TMAX. San Francisco
uses the same separately labelled air21 waterfront proxy; the other evening
columns use station-derived felt21. A complete observed comfort score is
unavailable because validated station rain and radiation are missing.

| Place | High: today → exact pixel | 21:00: today → exact pixel |
|---|---:|---:|
| San Francisco | 3.89 → 1.01 | 2.66 → 0.68 |
| Los Angeles | 3.63 → 2.78 | 3.09 → 2.25 |
| Santa Barbara | 4.42 → 0.75 | 4.26 → 0.54 |
| San Diego | 1.07 → 0.57 | **0.52 → 0.94** |
| Lima | 3.84 → 0.28 | 4.89 → 1.22 |
| Valparaíso | 5.36 → 0.85 | 6.49 → 2.41 |
| Málaga | 0.76 → 0.61 | 1.67 → 0.82 |
| Tenerife south, airport reference | 3.84 → 1.27 | 2.07 → 1.08 |
| Funchal | 2.12 → 1.94 | **1.07 → 3.14** |
| Cape Town | 1.74 → 1.16 | 2.19 → 0.33 |
| Seville | 0.82 → 0.69 | **0.72 → 1.21** |
| Rotterdam | 0.95 → 0.93 | **0.29 → 0.38** |
| Bangkok | 0.41 → 0.38 | 0.57 → 0.42 |

Today’s public API was captured again at all thirteen city coordinates. All
312 current high/felt21 monthly values equal the frozen comparison baseline.
The new values were independently reconstructed from the actual gzip replay
packets and NWFT tile bytes, then checked against the shared JavaScript model
and actual Worker API. The maximum physical-value difference was below
0.000001 °C. Independent scoring checked 1,092 monthly score/share/count
values as well. This establishes the failing result rather than an accuracy
claim from a source file or a convenient sampler.

## Implemented local slice

`hosting/sites/fine-place.mjs` provides the containing-pixel lookup, daily
ordered high/low correction, corrected-air dew cap, current typical-day
judgement, day counts and full-period monthly physical facts. Worker API,
link-preview text and the browser adapter call it. Monthly words and scores
use the current weighted median-band calculation; counts use the same eight
representative days per month and real month lengths. Physical monthly facts
use all 1,800 assembled days. No station fitting or place exceptions enter
these values.

The minimum functional adapter in `src/engine` is needed because cards,
headlines and map labels currently obtain their judgements directly from the
engine. It changes no UI styling or overview-map rendering. A missing
`v6/fine/current.json` preserves the current product. When a candidate pointer
is present, unavailable fine data stays unavailable rather than silently
reusing a quarter-degree result. Source and replay generation identities are
checked. The native point's zone is used in this scoped cohort; a worldwide
target-zone carrier remains unbuilt.

**The daily forecast outlook producer is not converted.** It currently runs
in Go and has no native forecast point carrier. A shared-module import example
does not count as that integration. Old prebuilt climate preview images are
suppressed when fine mode is active, so an old image cannot contradict the
new text. Neither global named-place coverage nor updated outlook posters is
claimed. These are additional missing parts of B1, not waived requirements.

## Artifacts and reproduction

All paths below are under
`/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data`:

- `fine-b1-diagnostic`: thirteen native-point packets, 6.64 MB gzip, full
  histories and selected indices. Built in about six seconds. Explicitly
  marked `NOT_FOR_PUBLICATION.txt`; not a worldwide carrier.
- `fine-validation/b1/decoded-bytes`: `COMPARISON.md`, all 156 monthly rows,
  `acceptance.json` and consumed-file hashes. Decoded validation took under
  one second.
- `fine-validation/b1/today`: fresh public API baseline and capture time.
- `fine-validation/b1/screenshots`: three native headless captures of the
  actual July cards, full-page captures, API responses and preview HTML.
  These illustrate the rejected candidate, not a live release. The concurrent
  interface worker's current local styling appears in these captures.
- `fine-b1-candidate`: local serving overlay, linking the scoped packets and
  correction tiles. Its pointer is diagnostic only. Do not upload it.
- `fine-30s`: retained partial global correction tiles, with no completion
  manifest. The global continuation was stopped after the city gate failed;
  neither the original ERA5 background job nor the home job was touched.

[B1_DATA.md](B1_DATA.md) gives exact packet and validation commands.
[B1_REVIEW.md](B1_REVIEW.md) records the independent release review.
[../../hosting/sites/FINE_PLACE.md](../../hosting/sites/FINE_PLACE.md)
documents the shared module and activation path.

To view the diagnostic locally after building the app:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
NICE_WEATHER_GENERATED_DATA="$SSD/fine-b1-candidate" devenv shell -- npm run serve
```

In a separate agent-owned shell, capture the integrated cards and verify their
displayed score and walk-home value against the Worker response:

```sh
devenv shell -- node research/fine/b1_screenshots.mjs \
  "$SSD/fine-validation/b1/screenshots"
```

Source changes are committed with the requested co-author trailer. Nothing
was pushed or deployed. There is **no data bundle recommended for upload**.
B2 has not started.
