Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Exact-pixel temperatures with one uniform evening rule

Josh accepted the city-average score gate and the uniform evening rule on 2026-10-18, with Rotterdam's May regression retained as a known limit. Every city improves in mean score error against today's published site. Individual monthly regressions still reach 15 points; this is not a claim that every month improves. The worldwide B1 bundle and upload instructions are tracked in [UPLOAD.md](UPLOAD.md). Nothing has been pushed or deployed. No station correction, per-place exception or new source was introduced.

The 10:00–20:00 correction and all correction tile bytes remain unchanged. At 21:00 every place uses the average of its monthly high and low corrections. Native dew point remains native, capped at corrected air when calculating felt temperature. Across the thirteen places, mean 21:00 error is 1.114 °C with uniform shift, versus 1.186 °C with ordered correction and 1.218 °C with native ERA5 evening. This selects the evening rule by temperature accuracy, without optimizing it per place.

## Visitor-visible comparison

Errors below are mean absolute errors across observed months. Scores use the exact displayed integers; nice days mean Nice-or-better and counts use display rounding. The station-T diagnostic substitutes observed hourly air temperatures into native weather, then applies the same typical-day calculation. It is not a full station weather score.

There are two baselines: today’s published site still serves older mean-day scores; the checked-out app and Worker implement the new typical-day rule. A fresh capture of all 156 live monthly previews matches the published scores used here exactly. Applying the new rule to the original data isolates the temperature change. Today’s older previews do not publish day counts, so count comparisons use the new rule on original data.

| Place | Published score error → 1 km | Same-rule score error → 1 km | Nice-day error → 1 km, days | High error → 1 km, °C | 21:00 error → 1 km, °C |
|---|---:|---:|---:|---:|---:|
| San Francisco | 20.75 → 11.50 | 25.92 → 11.50 | 8.00 → 4.92 | 3.89 → 1.01 | 2.66 → 0.84 |
| Los Angeles | 10.75 → 7.08 | 7.92 → 7.08 | 1.42 → 1.25 | 3.63 → 2.78 | 3.09 → 2.18 |
| Santa Barbara | 10.83 → 7.83 | 15.17 → 7.83 | 2.58 → 1.83 | 4.42 → 0.75 | 4.26 → 0.56 |
| San Diego | 8.50 → 1.17 | 5.67 → 1.17 | 0.75 → 0.75 | 1.07 → 0.57 | 0.52 → 0.62 |
| Lima | 16.83 → 2.58 | 17.08 → 2.58 | 4.75 → 1.08 | 3.84 → 0.28 | 4.89 → 0.80 |
| Valparaíso | 51.00 → 5.00 | 43.00 → 5.00 | 16.25 → 0.00 | 5.36 → 0.85 | 6.49 → 1.79 |
| Málaga | 8.42 → 3.58 | 5.33 → 3.58 | 2.50 → 1.50 | 0.76 → 0.61 | 1.67 → 0.84 |
| Tenerife south (airport reference) | 4.58 → 3.33 | 3.83 → 3.33 | 1.17 → 1.17 | 3.84 → 1.27 | 2.07 → 1.55 |
| Funchal | 11.92 → 5.42 | 3.67 → 5.42 | 1.33 → 2.67 | 2.12 → 1.94 | 1.07 → 2.81 |
| Cape Town | 10.25 → 8.25 | 16.50 → 8.25 | 5.17 → 3.17 | 1.74 → 1.16 | 2.19 → 0.75 |
| Seville | 6.58 → 4.83 | 4.25 → 4.83 | 1.50 → 1.83 | 0.82 → 0.69 | 0.72 → 1.08 |
| Rotterdam | 8.58 → 4.25 | 2.08 → 4.25 | 0.67 → 2.00 | 0.95 → 0.93 | 0.29 → 0.37 |
| Bangkok | 4.83 → 2.17 | 2.25 → 2.17 | 0.83 → 0.67 | 0.41 → 0.38 | 0.57 → 0.29 |

Every city improves in mean score error against today’s published scores. With the same new typical-day rule on both datasets, the largest mean score-error increases are Rotterdam +2.17, Funchal +1.75 and Seville +0.58 points; all others improve. Fog-coast score and day-count errors fall. These averages do not erase individual monthly regressions. Funchal’s 21:00 error still worsens substantially, and its nice-day count error increases.

Valparaíso has only 60 complete observed days in June–September, so its score/count averages cover four months. Its other eight monthly score targets remain untested. All other places have twelve target months. High compares the model’s 10:00–20:00 maximum with observed 24-hour TMAX. San Francisco retains the same air-only waterfront proxy at 21:00; other places use felt21. The original instrument displacements and observation coverage remain.

## Individual-month counterexamples

These displayed scores record the limits of the accepted city-average decision. Errors use the identically displayed station-T target.

| Place / month | Published | Original data, new rule | 1 km | Station-T target | Error increase versus published / same-rule |
|---|---:|---:|---:|---:|---:|
| San Francisco / November | 55 | 70 | 70 | 50 | +15 / +0 |
| Funchal / February | 56 | 70 | 55 | 74 | +1 / +15 |
| Rotterdam / May | 40 | 40 | 55 | 35 | +15 / +15 |
| Málaga / January | 43 | 47 | 35 | 45 | +8 / +8 |

The same-month station target is shared by both versions. All three fixed evening rules retain individual-month failures; changing the evening rule alone does not establish that stronger claim.

## Proof and reproducibility

The actual shared loader and place function were run on the retained gzip replay packets and exact NWFT pixels for all thirteen cities, twelve months and three evening rules: 468 monthly results and 3,276 comparisons. Scores, all three day counts and sample counts match the independent Python calculation exactly; maximum physical-value difference is below 0.000001 °C. All 156 daytime highs are bit-identical between evening variants. All 144 consumed correction tile files remain unchanged. The original-data Python decoder also matches 624 actual Worker scores/counts exactly.

Private evidence lives on the SSD under `fine-validation/b1-evening/`: `monthly.csv` contains every month, score and all three day-count estimates; `DISPLAY_COMPARISON.md` contains all displayed count errors; `COMPARISON.md` contains all three evening rules and coverage; `WORST_MONTHS.md` contains monthly counterexamples; `acceptance.json`, `current-parity.json`, `module-parity.json`, `current-live.json` and `current-worker.json` retain numerical evidence and consumed-file hashes. Station observations remain private and never enter production values.

From the repository root:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
devenv shell -- node research/fine/b1_current_numbers.mjs artifacts \
  "$SSD/fine-validation/step-a-settlement/inputs/city_queries.json" \
  "$SSD/fine-validation/b1-evening/current-worker.json" \
  "$SSD/fine-validation/b1-evening/current-live.json"
sh research/fine/run.sh b1_evening_gate.py --data-root "$SSD" \
  --output "$SSD/fine-validation/b1-evening"
devenv shell -- node research/fine/evening_module_parity.mjs \
  --bundle "$SSD/fine-b1-candidate" \
  --expected "$SSD/fine-validation/b1-evening/module-expected.json" \
  --output "$SSD/fine-validation/b1-evening/module-parity.json"
```

## Upload status

Josh authorized preparation of the worldwide B1 bundle after accepting the city-average result above. [UPLOAD.md](UPLOAD.md) is the release handoff; [B1_WORLDWIDE.md](B1_WORLDWIDE.md) preserves the earlier input inventory. The thirteen-place diagnostic and its private pointer are not production data. B2 remains deferred.
