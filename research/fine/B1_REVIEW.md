Written by AI on 2026.10.18 by model GPT-6.

# B1 independent review

**Do not publish this candidate. B1 is incomplete.** The temperature gate fails
at three protected cities even under the earlier 0.3 °C allowance. The candidate
also lacks worldwide replay coverage and the required outlook producer hookup.
Keeping its pointer local is correct; this review does not authorize deployment.

Reviewed against Josh's verbatim request in
`/tmp/nwf-agents/round4/fine/B1_REQUEST.md`, the original Phase 2 and revised
Step A requests, the current source, and actual local Worker responses.

## Accuracy gate

The isolated SSD bundle is `fine-b1-diagnostic`. Its manifest explicitly says
`candidate-rejected`, lists thirteen query points, and records five chunks.
`fine-validation/b1/decoded-bytes/COMPARISON.md` compares all twelve months with
the same instruments and frozen current-site facts. The protected-city errors
are:

| City | High MAE, current → candidate °C | 21:00 MAE, current → candidate °C |
|---|---:|---:|
| Funchal | 2.12 → 1.94 | **1.07 → 3.14** |
| Tenerife south | 3.84 → 1.27 | 2.07 → 1.08 |
| San Diego | 1.07 → 0.57 | **0.52 → 0.94** |
| Málaga | 0.76 → 0.61 | 1.67 → 0.82 |
| Seville | 0.82 → 0.69 | **0.72 → 1.21** |
| Rotterdam | 0.95 → 0.93 | 0.29 → 0.38 |

The latest wording is “no worse than today”; the validation script's default
still permits 0.3 °C. Rotterdam's small evening regression passes that older
allowance but fails a literal zero-regression requirement. This distinction
does not change rejection: Funchal, San Diego and Seville fail both.

The fog-coast high errors improve: San Francisco 3.89 → 1.01 °C,
Los Angeles 3.63 → 2.78, Santa Barbara 4.42 → 0.75, Lima 3.84 → 0.28,
Valparaíso 5.36 → 0.85. Improvements do not waive the protected-city gate.

Independent check: requested `/api/place?lat=…&lon=…&period=july` from the
running `http://127.0.0.1:5198` Worker at all thirteen manifest coordinates.
Compared every returned month's `highC` and `feelsAt21C` with the validator's
decoded-byte `monthly.csv`: 312 values, maximum difference
0.000000565 °C. The failing temperatures therefore reach the actual API.
The tiny arithmetic differences do not explain the regressions.

The comparison retains the stated instrument limitations: model high is
10:00–20:00, observed TMAX is a whole-day instrument, and San Francisco uses
the original 21:00 air-temperature waterfront proxy rather than felt
temperature. These are matched-comparison limitations, not new accuracy proof.

## Consumer completeness

`hosting/sites/fine-place.mjs` supplies one exact containing-pixel decoder and
climate calculation to the app's `FinePlaces` engine and Worker. Its stretch
aggregation implements the current weighted median band and nice, perfect and
awful counts. `scorePoints` and `countPoints` route climate named-place numbers
through `judgePoint`; the overview map retains its previous data source.

The Worker place API and climate preview text call the shared module. Actual
`/portugal/funchal/july` HTML says “Perfect most days” and counts 31 nice,
31 perfect, zero awful days, consistent with the current exact-pixel API.
It suppresses old climate preview images rather than publishing stale numbers.
An accepted release still needs regenerated preview images.

There is no worldwide replay carrier. An actual Lisbon request at
38.7223, −9.1393 returns `months:null`, `judgement:null`, `fineReplay:true`.
This is an explicit unavailable result, not evidence of worldwide delivery.
Activating this pointer publicly would remove numbers at ordinary places
outside its diagnostic coverage.

The Go forecast/outlook producer does not call the shared calculation and has
no native forecast carrier. Its named rankings and posters retain the previous
forecast model. An example import in `hosting/sites/FINE_PLACE.md` is useful
documentation but is not the requested producer integration. Do not describe
all named numbers or outlook posters as converted to 1 km.

## Required proof still outstanding

At review time the requested three actual card screenshots and final upload
bundle handoff were still being produced by the parent. They can document this
rejected candidate; they cannot turn it into a passing release. I have not
visually accepted those screenshots or exercised all app headline/label flows.

The source and decoded comparisons support preserving this work as an isolated
diagnostic. They do not support calling B1 proven, deploying its pointer, or
proceeding to the map-detail milestone as though the acceptance gate passed.
