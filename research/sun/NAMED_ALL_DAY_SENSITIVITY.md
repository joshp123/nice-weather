Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# All-day radiation-reference sensitivity

The original exact-date diagnostics use only the retained representative days that overlap the satellite archive. This separate check expands the reference to every cached native ERA5 date with satellite radiation. It changes some conclusions materially, but still does **not establish the universal no-regression gate**. No correction, source choice or coefficient was changed.

The compact replay carries only 96 hourly weather records; its 1,800-date section contains physical rollups. The full hourly weather was therefore re-extracted from the already cached raw ERA5 fields and weather supplements. Every retained day's air, dew, sun/shade felt temperature, rain, wind, sunny hours and daylight must match the published carrier within 0.0001; the observed maximum difference is zero.

The same accepted 1 km temperature anchors apply throughout. The reference replaces only direct radiation and sunny share with exact-date satellite values, keeping native temperature, rain, dew point and wind. It is a satellite-radiation substitution diagnostic, **not a complete observed-weather comfort score**. Scores are the app's typical-day score, not arithmetic mean scores.

## Coverage and the comparison

- SARAH: 1,543 native dates at each of six places, spanning the cached 2015–2023 overlap. All twelve months are represented. The native chunks through 2024 extend beyond the satellite archive, so unavailable dates are excluded from the reference. The original exact-date check retained only 76–85 days.
- GOES: 516 native dates per place, January–September of 2022 and 2024. The native carrier has no October–December dates in those two years, so this is a **273-day seasonal aggregate**, never an annual result. The original exact-date check retained only 25–33 days.
- Chiclayo and Chimbote lack cached complete observed-year series and are not part of this sensitivity check. Their monthly targets and published-number checks remain in the fourteen-place report.

“Representative” below uses the original eight days per month, including years outside the satellite overlap, and the unmodified production coefficients. The larger reference uses all available native days. Their period and selection differ: this tests representative-day estimates against a larger radiation reference, rather than claiming a fully paired weather validation. The second table applies before/after/reference to the **same all-day dates**, which removes the selection difference but is not what visitors currently see.

## Visitor representative-day predictions against the larger reference

Monthly absolute errors are averaged across twelve months for SARAH and nine for GOES. Count errors are days per month.

| Place | Monthly typical-score MAE before → after | Monthly nice-count MAE before → after | Monthly perfect-count MAE before → after | Aggregate nice before / after / reference | Aggregate perfect before / after / reference |
|---|---:|---:|---:|---:|---:|
| Swakopmund | 6.82 → 4.91 | 2.17 → 2.17 | 4.50 → 3.25 | 285 / 266 / 263 | 147 / 103 / 107 |
| Tenerife south (Arona) | 12.94 → 5.39 | 5.50 → 2.17 | 5.17 → 4.17 | 323 / 267 / 255 | 191 / 96 / 133 |
| Málaga | 4.38 → 5.63 | 1.83 → 2.00 | 2.08 → 2.08 | 270 / 266 / 262 | 160 / 156 / 150 |
| Seville | 3.33 → 2.73 | 0.83 → 0.83 | 2.50 → 1.83 | 220 / 220 / 215 | 129 / 118 / 107 |
| Cape Town | 6.22 → 4.35 | 2.08 → 2.00 | 3.58 → 3.25 | 258 / 235 / 245 | 128 / 90 / 105 |
| Rotterdam | 5.69 → 2.20 | 1.83 → 0.50 | 1.00 → 1.00 | 88 / 80 / 81 | 46 / 46 / 42 |
| Lima | 18.22 → 6.04 | 9.44 → 3.22 | 4.78 → 3.11 | 258 / 178 / 182 | 110 / 65 / 77 |
| Antofagasta | 11.87 → 3.11 | 5.56 → 2.78 | 3.67 → 1.89 | 254 / 219 / 202 | 143 / 113 / 110 |
| Iquique | 19.64 → 12.14 | 9.56 → 7.11 | 6.44 → 2.56 | 258 / 235 / 170 | 162 / 124 / 102 |
| Valparaíso | 8.42 → 5.04 | 1.56 → 1.11 | 3.89 → 2.33 | 75 / 71 / 65 | 48 / 34 / 14 |
| San Francisco | 6.00 → 4.64 | 2.00 → 2.44 | 3.33 → 3.44 | 156 / 145 / 148 | 61 / 50 / 64 |
| San Diego | 6.31 → 4.62 | 1.89 → 1.56 | 3.89 → 3.33 | 235 / 232 / 228 | 163 / 152 / 132 |

Rotterdam's earlier count regression does not persist against the larger reference: nice days move 88 → 80 against 81. San Diego's monthly score and both count errors improve against the larger reference. San Francisco's monthly typical-score error improves, but its monthly count errors worsen slightly and its aggregate perfect-day error increases from 3 to 14 days. Málaga's monthly typical-score error increases by 1.25 points, while its aggregate count errors improve. These remaining limits prevent an unconditional no-regression claim.

## Applying the same correction to every available native day

| Place | Typical-score MAE before → after | Nice-count MAE before → after | Perfect-count MAE before → after |
|---|---:|---:|---:|
| Swakopmund | 3.97 → 1.59 | 1.75 → 1.08 | 2.75 → 0.92 |
| Tenerife south (Arona) | 13.29 → 5.19 | 5.00 → 2.25 | 5.00 → 3.58 |
| Málaga | 2.98 → 2.70 | 0.92 → 0.83 | 0.83 → 0.58 |
| Seville | 1.23 → 0.76 | 0.42 → 0.42 | 0.42 → 0.33 |
| Cape Town | 3.54 → 2.05 | 0.83 → 0.67 | 1.00 → 1.08 |
| Rotterdam | 2.72 → 1.97 | 0.67 → 0.67 | 0.50 → 0.67 |
| Lima | 15.29 → 2.44 | 8.33 → 2.67 | 3.11 → 2.22 |
| Antofagasta | 9.92 → 1.62 | 5.44 → 1.56 | 3.44 → 1.56 |
| Iquique | 15.98 → 9.60 | 8.78 → 5.67 | 3.89 → 1.67 |
| Valparaíso | 4.18 → 1.23 | 1.44 → 0.56 | 1.78 → 0.44 |
| San Francisco | 1.36 → 3.47 | 0.78 → 1.44 | 0.89 → 1.89 |
| San Diego | 2.69 → 4.06 | 1.33 → 2.22 | 1.89 → 1.22 |

Most controls improve when both correction and reference use all available dates. San Francisco still worsens on all three monthly measures; San Diego's score and nice-count errors worsen while its perfect-count error improves. Rotterdam's perfect-count error also rises slightly. Monthly radiation matching cannot reconstruct the observed relationship between sunshine and the native day's weather. No fitting or per-place exception was attempted in response.

## Evidence and rerun

- New artifact: SSD `sun/named-validation/all-native-day-sensitivity.json`; exact native dates, month coverage, all scenarios and separate errors are retained.
- Cached extractor: [named_native_days.py](named_native_days.py). It reads `fine-build/reference-global/era5` and `fine-sources/b1-worldwide-weather`; it makes no network requests.
- Harness: [named_all_day_diagnostic.mjs](named_all_day_diagnostic.mjs), with the same source/control/native/temperature arguments as the prior diagnostic plus `--ssd` and an environment Python with the existing fine-pipeline dependencies.
- Original diagnostics remain unchanged: SSD `sun/named-validation/sarah-diagnostic-with-coverage.json` and `goes-six-exact-day-diagnostic.json`. They answer the smaller, exact-selected-date question.
- Inputs: SSD `sun/named/validation-final.json`, `sun/named-validation/sarah-longnormal`, `sun/named/goes/disc2022`, `disc2024`, `v6-fine-b1-20261018/replay` and `temperature`.
- Full-period monthly sunshine errors in the fourteen-place report use published card facts from `final-fourteen-comparison/comparison.json`. Its DNI errors use eight-sample native means from `production-sarah-normalized-accuracy.json`; those distinct baselines are now labelled explicitly.
