# Retained research comparison

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

Observed: **87,659 daily records pass** across the twelve research places,
2006–2025. This runs retained hourly inputs through the builder's actual
`hourlyFacts`, the physical solar model, shared day aggregator, binary writer
and binary reader. It also checks all twelve selected coastal cells and their
ERA5 terrain heights against the research.

Maximum stored differences from the corresponding physical inputs:

| Measure | Maximum difference | Allowed |
|---|---:|---:|
| Ten temperatures | 0.0050061°C | 0.0051°C |
| Ten-sample mean dew point | below 1e-14°C | 0.0051°C |
| Daytime and whole-day rain | 0 mm | 0.0501 mm |
| Sun share | 0.000050 | 0.000051 |
| Ten-sample mean wind | below 2e-14 km/h | 0.0051 km/h |

Temperature includes binary quantisation and the float32 Kelvin round trip.
Rain reference inputs have 0.1 mm precision; this result does not establish
that their underlying ERA5 values have that accuracy.

All twelve places have 7,305 retained daytime dates except Denver, whose final
local day extends beyond the retained UTC source range: 7,304. Whole-day rain
has 87,652 complete retained reference dates. The live builder reads a 48-hour
pad, so it does not inherit these reference boundary gaps.

The original arrays contain **eleven** state samples, 10:00 through 20:00.
The requested measure contains **ten**, 10:00 through 19:00. Temperatures are
checked against the first ten `b_pure` columns. Dew point and wind means are
checked against those same ten hours of the original hourly inputs. Their
mean absolute differences from the original eleven-sample baseline are
0.027–0.172°C and 0.234–0.361 km/h respectively; these are definition changes,
not numerical tolerance failures. The retained baseline also clips dew point
to its original lapse-adjusted temperature; physical dew point remains its
own fact in the new builder.

The comparison found a real midnight clock-change bug: Marrakech's 3 April
2011 whole-day rain was initially 22 mm instead of 20 mm. The shared local-day
bounds now find each civil boundary independently. Tests cover that 23-hour
day, Havana's repeated midnight, Santiago's missing midnight and Samoa's
nonexistent 30 December 2011. The builder omits nonexistent local dates.

Exact per-place results are in [reference-result.json](reference-result.json).
The preparation and reproduction commands are in [README.md](README.md).
This comparison does **not** claim a second download of every historical ERA5
chunk. Actual archive retrieval, Pcodec decoding and a complete tile-year are
separately exercised by the Sites probe.

The cloud batch was then restored through the actual CLI into a fresh local
output directory using only public R2 reads. No publisher token was supplied.
The command returned `resumed: true`, verified the 1,469,483-byte measures file
and its SHA-256, used the expected local path, emitted no ERA5 download logs
and skipped upload. The restored report retains the original cloud build's
metrics. A separate bounded test verifies that a corrupt download cannot
install a completion checkpoint.
