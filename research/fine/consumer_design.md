<!-- Written by AI on 2026.10.17 by model GPT-6.1 Sol. -->

# Fine temperature consumer

Use the 30 arc-second corrections for both the close map and place card. Retain
Step A for the global overview. A fine temperature pixel changes temperature;
it does not claim 1 km rain, wind, humidity observations or daily weather.
No application code was changed for this design or benchmark.

All reported scores and shader timings use the **arithmetic mean of graded daily
scores** from `research/taste/nice_v6.py` through `climate-v6/pipeline.py`: eight
equally weighted selected days per month, with the annual view weighting the
twelve months equally. They do not use a count of days above a score threshold.
Another worker separately developed application score aggregation; this benchmark
does not validate that consumer. Its integrating owner must align the
final aggregation in both map and card while preserving their shared pixel,
native weather, correction model and generation.

The recommended replay carrier is **60 bytes per native point and sampled day**.
It preserves native felt temperatures, air temperatures and uncapped dew point
in signed tenths of a degree. The existing 15-byte felt record cannot reconstruct
independent high/low corrections or the dew-point cap. A 51-byte extension keeps
that record's real −30 to 55 °C clipping. The extra nine bytes have an observed
purpose in Greenland and elsewhere, rather than a hypothetical safety benefit.

## Published correction contract

The builder in `tiles.py` owns this binary contract. A gzip payload contains:

| Bytes | Value |
|---|---|
| 32 | Little-endian `<4sHBBHHHH16s`: `NWFT`, version 1, month, flags 0, tile row, tile column, actual height, actual width, generation ID |
| 262,144 | 256×256 interleaved signed Int16 high/low offsets, 0.1 °C, −32768 missing |
| 65,536 | 256×256 UInt8 source/support flags |

The whole grid is 43,200 columns by 21,600 rows. Its north/west edges are 90°
and −180°. Pixel `(r,c)` is centred at `90−(r+.5)/120, −180+(c+.5)/120`.
The edge tile's height/width define valid pixels; its arrays remain 256×256.
The nearest native 0.25° reference is
`round((r+.5)/30), round((c+.5)/30) mod 1440`.
Use round-half-up for these nonnegative indices, including Python callers;
JavaScript `Math.round` already has that behavior. Do not bilinearly interpolate
paired high/low corrections or interpolate through missing pixels.
For valid query latitude −90..90, clamp the containing row to0..21599 so the
South Pole uses the final row; wrap longitude columns modulo43200.

Support byte bits:

| Bits | Meaning |
|---|---|
| 0–2 | Source: 0 native ERA5; 1 PRISM; 2 WorldClim exact pixel; 3 WorldClim centred 5×5 valid-value mean; 4 CHELSA |
| 3 | Native ERA5 temperature reference present |
| 4 | The native quarter-degree point exists in the legacy v6 land-point set |
| 5 | OpenStreetMap or public-domain Natural Earth polygon membership; this independent property is ODbL |
| 6 | Static WorldClim exact-pixel land support: paired high/low valid in any month |
| 7 | Static PRISM exact-pixel land support: paired high/low January normal valid |

Source 3 retains its approximately 5 km source support even though its storage
pixel is 30 arc-seconds. These flags describe support, not measured accuracy.
Bits 6 and 7 are static geographic properties inherited from the land-support
build, not the current month's selected source. Bits 0–2 alone name that source.
For example, a month may use WorldClim's 5×5 mean while bit 6 remains set because
another month has a valid exact pair at that pixel.
Attribution comes from the generation manifest and `ATTRIBUTION.txt`.
This is a collection with component licences: numerical weather/source
properties are CC BY-SA 4.0; polygon membership and the timezone lookup are
separately offered under ODbL. The `land-membership` folder contains packed
OSM-only and polygon-union masks, licence text, source hashes and notices.
WorldClim/PRISM validity remains a different property from OSM membership.
Do not imply the website's interface shares its data licences.

The offsets already contain the source anchor's period adjustment. Modern and
historical monthly means use four native quarter-degree corners bilinearly;
native high/low normals use the nearest native quarter-degree point. A missing
nonzero-weight mean corner stays explicit; it cannot silently become a nearest
inland temperature. Bind the generation ID to the chunk set, reference normals,
source versions, model and sampled-day identity. Reject a correction/carrier
pair from different generations.

The source/support grid retains eligible Antarctic pixels. Legacy v6 excludes
land below −60°, and the existence of a source anchor does not mean there is a
replay carrier there. Bit 3 and the carrier's own presence determine whether a
fine score can be shown. A source-only pixel can expose its anchor when the
temperature reference normals are available, together with its support; it
cannot claim a replayed weather score. Offsets without reference normals cannot
reconstruct an absolute anchor. A missing native reference never
triggers the previous inland borrowing rule.

## One pixel and one model

A shared pure decoder selects the pixel by flooring its geographic position,
reads that exact tile entry and native reference ID, and evaluates it. The map
and place card call that decoder with the same generation and taste. The card
must not use the current Worker's separate nearest-land search. The map must
not smooth temperatures across coast pixels while the card reads one value.

For each sampled local day, retain native hourly air `T`, uncapped dew `Td`,
daily high `H`, daily low `L`, wind, rain and sunshine. Given that month's high
and low offsets `dh,dl`:

```
if H == L or H + dh <= L + dl:
    delta(hour) = (dh + dl) / 2
else:
    delta(hour) = dl + (T(hour) − L) / (H − L) * (dh − dl)
corrected_air(hour) = T(hour) + delta(hour)
corrected_dew(hour) = min(native_Td(hour), corrected_air(hour))
```

This is the same globally applied ordered exception evaluated in Phase 1.
A zero native daily range uses the averaged offset; no day is allowed to invert.
Both native and corrected felt temperature use the existing taste model:
`felt_delta = delta + .33*(vap(corrected_dew)−vap(min(native_Td,T)))`.
Update the humidity factor from the corrected capped dew values too. Native Td
is the input; do not replace it with old capped Td, which loses information when
a formerly cold hour warms. Rain, wind, sunshine and daily timing stay native.

The score still uses its eight selected days. Place-card climate facts must use
**every retained local day in the assembled chunk set**, evaluated offline with
the same pixel's published high/low offsets and daily ordered exception. The
current card's high is the daily maximum from 10:00 through 20:00. The source
anchors are daily 24-hour extrema: anchor H is not the card's daytime-high fact.
An eight-day mean is not the full-period 21:00 fact. The Step A uniform felt-offset
format cannot independently correct the cell's humidity factor; Step B can and
should.

## Fine-pixel civil time

Clock hours belong to the fine pixel's civil time zone. The nearest quarter-degree
weather point can lie across a time-zone boundary; its zone is not automatically
the visitor's zone. Assign each fine pixel a stable IANA zone ID from the pinned
boundary dataset, with its required attribution. Include the zone map, IANA rules
version and retained-date UTC sampling schedule in the consumer generation.
Both the map and card resolve the same pixel's zone ID. Do not infer it from the
visitor's computer or from longitude alone.

The future carrier key is `(native quarter-degree ID, target fine-zone ID)`.
Generate variants only where required; zones whose complete retained-date UTC
sampling schedules match may share one variant. Sample native weather at the
fine zone's 10:00–21:00 hours and its complete 24-hour local day, with date-specific
DST and fractional UTC offsets. Selected day IDs remain civil-date labels:
interpret each label in the target zone, rather than shifting a midnight instant
from the selection centre's zone. The existing/absent-cell quantile selection
rule below still determines those labels; it never supplies the weather.

Published correction offsets were calculated against the reference point's
original local-day normals. A target-zone variant must preserve the same absolute
period-adjusted anchors when its daily H/L and monthly normals change. Carry the
target-zone monthly native high/low normals and expose the original reference
normals in the generation's small reference metadata. For each month:

```
dh_effective = dh_tile + H_reference_normal − H_target_zone_normal
dl_effective = dl_tile + L_reference_normal − L_target_zone_normal
```

Then use these effective offsets in the same daily interpolation and ordered
exception. This is simply `absolute_target_anchor − target_zone_native_normal`;
it does not add a second warming adjustment or move the source pixel. Keep the
normals as Float32 values in future carrier/reference metadata, avoiding repeated
tenths rounding during rebasing. The target H/L pair adds eight bytes per month
per variant, plus the shared original reference pair; no change to the existing
NWFT correction-tile format is needed. Full-period facts use the same target-zone
sampling and rebased offsets. Daily repair can still affect the final normal,
as in the already selected ordered model; rebasing does not waive that rule.

A targeted probe found matching zones for the thirteen benchmark cities, but
four real mismatches among twenty additional locations: Valença, Ayamonte,
Badajoz and Coolangatta. This is a targeted counterexample search, not a global
frequency estimate. Replaying all 1,800 retained days at the same native point
in each zone gave Valença a 0.743 °C mean absolute 21:00 air difference (maximum
3.031 °C); Coolangatta gave 0.104 °C (maximum 2.063 °C). Monthly native high/low
normal differences were much smaller, at most 0.069 °C in these two probes,
but the rebasing formula preserves the declared absolute target exactly. These
are time-sampling errors within ERA5, not station validation results. Evidence
is in `timezone-probe.json` and `timezone-weather-probe.json` under the Phase 2
scratch report directory; the GPU benchmark cities did not exercise this path.

This is scheduled consumer/carrier work, not a change to the present temperature
reference or correction tiles. A temperature-only reference cannot yield a fine
score or 21:00 felt fact merely because its time zone is known. Missing variant
weather stays explicit; the browser must not substitute the original zone's
21:00 record.

## Full-period place-card facts

The scheduled consumer needs a separate offline fact-tile build alongside its
day carrier. For each eligible fine pixel and calendar month, process every
actual retained day using its own nearest native quarter-degree reference,
fine-zone local clock sampling, native Td and wind. Rebase the published,
tenths-rounded `dh,dl` as above, then apply those effective offsets to every local hour using that day's native 24-hour H/L and the same
ordered exception above. Compute three facts before rounding:

| Channel | Full-period monthly value |
|---|---|
| Daytime air high | Mean over all retained days of `max(corrected_air[10:00..20:00])` |
| Walk-home felt | Mean over all retained days of shade felt at 21:00, using corrected air, native wind and `min(native_Td,corrected_air)` |
| Native dew point | Mean of uncapped native Td at the twelve local samples 10:00..21:00, over all retained days; no temperature offset is applied to this input |

The dew fact describes the native moisture input. The score's humidity factor
continues to use dew capped against corrected air, as the taste model requires.
Do not replace the native-dew fact with the old cap against uncorrected air.
If 24-hour high/low anchor facts are shown, give them their own labels; they do
not replace the daytime-high channel. Facts do not change when taste sliders
move; the score does.

Use a companion gzip tile with the same 32-byte header layout, a distinct magic
`NWFF`, version 1, the correction generation ID, month and tile coordinates.
Its payload is 256×256×3 interleaved little-endian signed Int16 values in tenths
of a degree, channel order as above, with −32768 missing. Its manifest fixes
these definitions, complete date set, reference time-zone rules and algorithm
version. The header's valid width/height handles edge tiles. A reader uses the
same geographic pixel as the correction and score, verifies the generation, and
fetches only the selected place's fact tile. An annual card may fetch its twelve
monthly tiles; neither map display nor taste-slider redraw needs viewport-wide
fact downloads.

Require the full expected day count for a channel; an incomplete raw series
leaves that channel missing instead of silently computing a different period.
Temperature-only references can support the daytime-high channel if their raw
hourly T covers that period, but cannot produce native dew or 21:00 felt without
Td and wind. They are not complete day carriers and cannot produce a fine score.
Missing channels remain explicit; a source anchor alone never fills them. The
full-period fact builder may use unquantized hourly weather, so its exact means
avoid the carrier's measured sample/interpolation errors while preserving the
same physical correction model.

This layout costs **393,248 bytes per month tile** before compression
(32 + 256×256×6), or **4,718,976 bytes for twelve monthly tiles** for one place's
tile. A single three-channel pixel is six payload bytes per month, but the
proposed static fetch unit is the whole tile. Keeping every global pixel would
cost 67.18 GB/year uncompressed before ocean pruning and tile padding; publish
only supported tiles, as with the correction set. These are arithmetic payload
estimates, not measured compressed transfer size, generation time or GPU cost.
Fact tiles are a concrete requirement of the future consumer; generating them
globally is outside this Step B correction-tile build. The measured GPU results
below exclude fact-tile generation, transfer and decoding.

## Native day carrier

The native quarter-degree reference includes its own ocean points where a fine
coast or island needs them. It is a different coverage set from the 250,011
legacy land points. Source temperature, dew and weather come from that point,
never from a nearby inland point. Temperature-only reference normals are not a
complete day carrier: carrier availability must also cover Td/wind/rain/radiation.

For a cell present in `cells.bin`, keep the eight dates per month selected by
that native 1° cell's existing score quantiles. Every reference point in the
geometric cell uses those exact dates, maintaining coherent comparisons with
the overview. Some tiny-island cells are absent entirely from the legacy grid.
For each absent cell, score its geometric centre's native ERA5 quarter-degree
point over every actual assembled day, then select the same eight default-score
quantile ranks with the existing stable date-order tie breaking. Reuse those
date IDs throughout that cell. This selects dates only: each fine pixel still
replays its own native quarter-degree weather. No nearest-land weather enters
either operation. The future carrier builder must acquire all six weather
fields for both these selection centres and each required reference point;
the present temperature-only normals do not supply them. Record the date IDs
and generation explicitly. Changing the chunk set requires recomputing normals,
dates and corrections together.

The tested carrier alternatives are:

| Carrier | Stored values per point/day |
|---|---|
| 33 B | Existing 15 bytes + eight air and eight uncapped Td samples + H/L, all temperature bytes at ⅓ °C, −30 to 55 °C |
| 51 B | Existing 15 bytes + those 18 auxiliary temperatures as signed Int16 tenths °C |
| 60 B | Ten native felt samples as signed Int16 tenths °C (20 B), rain/wind/sunshine/daylight (4 B), eighteen auxiliary temperatures as signed Int16 tenths °C (36 B) |

Air/Td clock samples are 10,12,14,15,16,18,20,21; interpolate the missing clock
hours exactly as in the benchmark. Felt uses the existing six sun and four shade
sample times and interpolation. The 60-byte version omits the redundant old
mean-capped-dew byte. It preserves current rain/wind/sunshine/daylight packing.
A quarter-degree point needs 480 bytes per month (eight days). One fine tile
normally needs roughly a 10×10 native reference patch, about 48 KB/month,
plus its 327,712-byte fine tile before compression. Only fetch visible tiles and
their references; no browser should fetch the global fine grid or global carrier.

A full 12-month fine grid is 55.99 GB uncompressed before ocean pruning. Current
legacy land-point coverage at 60 B/day is 120.0 MB/month or 1.44 GB/year before
compression; the broader reference set will be larger. Neither belongs in the
initial page transfer.

## Measured replay fidelity

`consumer_benchmark.py` reads all five actual native chunks at San Francisco,
Los Angeles, Santa Barbara, San Diego, Lima, Valparaíso, Málaga, Tenerife south,
Funchal, Cape Town, Seville, Rotterdam and Bangkok. It chooses the actual
cell-quantile dates, and compares compact replay with unquantized hourly replay.
There is no station fitting. The final run read the exact production NWFT offsets
from generation `e68c86ba3e24654a499ae4ef37ad35f9`, including its period adjustment,
for all thirteen places. This is carrier representation fidelity, not station
accuracy. The four US cities use PRISM; Tenerife south uses WorldClim's 5×5
support; the other eight use exact WorldClim pixels. CHELSA and native fallback
were not exercised in this city cohort.

Across 13 places × 96 selected days, using those actual source offsets:

| Carrier | Mean monthly score MAE, points | Selected-day score MAE, points | Largest selected-day score error | 21:00 air MAE, °C | 21:00 felt MAE, °C |
|---|---:|---:|---:|---:|---:|
| 33 B | 0.504 | 0.776 | 5.97 | 0.145 | 0.124 |
| 51 B | 0.502 | 0.723 | 5.80 | 0.043 | 0.088 |
| 60 B | 0.509 | 0.709 | 5.61 | 0.043 | 0.037 |

The 60-byte layout improves individual-day and 21:00 felt precision. Its monthly
score error is similar to 51 bytes because retained-hour interpolation still
dominates. Its largest monthly score error was 2.87 points; monthly MAE by city
ranged from 0.14 in Rotterdam to 1.18 in Bangkok. Tenerife south had the largest
21:00 air/felt MAE, 0.132/0.112 °C: the dual mapping can amplify tenths rounding
when the native daily temperature range is narrow. This remains a measured
representation limit, even with the recommended carrier.

There were five ordered exceptions across all 23,400 actual days: four in
Rotterdam and one in Seville. Quantization changed none of those decisions for
any of the three layouts in this actual-source run. The eight selected dates'
absolute monthly score bias against all 1,800 corrected native-point days was
**1.372 points on average, maximum 6.882**. The carrier cannot turn eight-day
sampling into exact full-period scoring or facts. Python replay took 1.08 s with
the thirteen raw-weather caches already prepared. Results and per-city values
are in `consumer-benchmark-actual/carrier-results.json` on the SSD.

An earlier stress run used explicit **synthetic offsets
−2 °C high / +2 °C low** to exercise the dual model while global source files
were downloading. These are representation errors, not station accuracy claims.

Across 13 places × 96 selected days:

| Carrier | Mean monthly score MAE, points | Selected-day score MAE, points | Largest selected-day score error | 21:00 air MAE, °C | 21:00 felt MAE, °C |
|---|---:|---:|---:|---:|---:|
| 33 B | 0.410 | 0.696 | 12.50 | 0.077 | 0.107 |
| 51 B | 0.401 | 0.612 | 4.94 | 0.020 | 0.084 |
| 60 B | 0.408 | 0.592 | 4.87 | 0.020 | 0.030 |

The 60-byte layout improves temperature precision; monthly score error remains
similar because interpolation between retained felt hours dominates these
coastal examples. Quantization changed the daily ordered-exception decision on
494/23,400 days for 33 B and 130 for 51/60 B in this stress model. All use the
same rule, with no source- or city-specific repair.

Selecting eight cell dates also has a separate cost: the absolute monthly score
bias against **all days at the fine native point with the same correction** was
1.52 points on average, with a maximum of 7.71. This is measured under the same
synthetic offsets. It cannot be described as exact full-period scoring. The
actual-source result above quantifies that same sampling limit with the
finished offsets. Neither substitutes for the full-period fact build.

The global native audit read all 12,545 fresh intermediates in 10.1 s. Of
336,006,000 retained felt values across all dates, 19,379,773 were below −30 °C
and 1,225 above 55 °C. These are tile-local cell means before final assembly,
not a count of affected eight-day records. Two raw quarter-degree checks confirm
the loss is real:

| Point/date/hour | Native felt | 33/51 B | 60 B |
|---|---:|---:|---:|
| Greenland 75.5,−35.75; 2023-12-27 10:00 | −68.948 °C | −30.000 °C | −68.900 °C |
| Australia −12.25,137.0; 2022-04-27 18:00 sun | 96.134 °C | 55.000 °C | 96.100 °C |

The Australian value is the existing pipeline's sunlight-adjusted felt model
(air was 28.625 °C), not a station air temperature or an assertion of human
exposure to 96 °C. The Greenland record alone establishes that baseline clipping
must be resolved for faithful global replay. Cold extremes can matter after a
fine correction, even where the native point itself scores zero.

## Actual geographic SF render

The actual production NWFT files were decoded and rendered around San Francisco
at zoom 9, using the geographic cache architecture above. Generation
`e68c86ba3e24654a499ae4ef37ad35f9` identifies these measurements. The phone view
has two visible land tiles and 104 distinct native-quarter histories. The wide
view has three land tiles and 176 histories; its fourth geometric tile slot is
ocean. Every required reference has all five chunks of the six weather fields.
Actual correction support agrees with the v2 polygon mask in these views.
All required references use America/Los_Angeles, so this experiment does not
exercise target-zone rebasing.

| SF viewport | Twelve-month gzip transfer | Decoded payload | Gzip decode | Completed texture upload | Warm month recompute | Warm year recompute | Cached geographic draw |
|---|---:|---:|---:|---:|---:|---:|---:|
| 390×844 | 1,667,521 B | 8,798,370 B | 113.2 ms | 25.7 ms | 9.1 ms | 22.4 ms | 4.6 ms |
| 3840×1080 | 3,490,379 B | 13,612,347 B | 80.6 ms | 12.8 ms | 4.3 ms | 31.1 ms | 4.75 ms |

Transfers include real 32-byte NWFT headers and source/support bytes, the
60-byte native carrier and an explicit carrier-availability texture. Fine tiles
alone account for 1,267,851 and 2,790,147 compressed bytes respectively; distinct
native weather accounts for 399,623 and 700,183 bytes. All twelve months are
loaded for both redraw modes in this experiment. Monthly rendering therefore
does not imply a measured one-month-only download. A full-tile atlas is scored
once per fine pixel; the display uses its actual Web Mercator pixel lookup.
Python also writes reassembled fine/support gzip files for inspection; the
browser does not fetch those fixtures. Its transfer/decode measurements come
from the real NWFT files plus the separately packed native carrier/availability.

Data ready for scoring took 365.0 ms in the first, phone-sized case and 203.6 ms
in the subsequent wide case. This includes local fetch, decompression, tile
assembly, shader compilation and completed upload, but no internet delay.
First-use shader compilation was 74.4 ms versus 4.9 ms in the subsequent case;
tile assembly was 2.0/4.4 ms. The differing cold costs are not a comparison of
phone and desktop hardware. Global tile generation was active concurrently.
Short GPU operations include the JavaScript fence polling floor of about
4.5 ms; report these as completed wall times, not exact device execution times.

CPU place-card replay and GPU map replay at thirteen distinct-reference pixels
per view differed by at most 0.000034 score points; both GL error checks were
zero. The actual grayscale score-diagnostic PNGs were inspected: coastal and terrain temperature variation
appears in both views without missing tiles or failed shaders. Quarter-degree
weather and selected-day boundaries remain visible: fine temperature anchors
do not create fine rain, wind or day histories. Evidence is in
`consumer-geographic-actual/geographic-results.json`, its two `sf-*.png` files
and `geographic-zone-audit.json` under the SSD build folder. These are real
geographic payload and rendering measurements on this Mac, not a basemap,
full-period-fact, integrated application or physical-phone acceptance test.

The source data supports a bounded close-zoom cache in this real view: annual
taste recompute is 22–32 ms after data arrival, followed by a roughly 5 ms draw.
The larger synthetic experiment below supplies throughput limits for views
containing more land tiles; it does not replace this geographic measurement.

## Synthetic GPU throughput timings

The actual experiment ran in an isolated headless Chrome profile on this Mac's
Apple M4 Pro, ANGLE Metal, DPR 1. It decoded gzip, uploaded integer textures,
executed the graded taste shader and waited for a GPU fence after each operation.
A `drawArrays` submission time or `gl.finish` proxy is not the reported redraw
time. Each warm median below is three actual taste changes after the first draw.

| Viewport / carrier | Gzip bytes transferred | Decoded bytes | Gzip decode | Texture upload, completed | Month redraw | Year redraw |
|---|---:|---:|---:|---:|---:|---:|
| 390×844 / 33 B | 478,580 | 16,994,016 | 21.3 ms | 67.2 ms | 9.1 ms | 52.3 ms |
| 390×844 / 51 B | 497,493 | 17,645,472 | 9.7 ms | 9.1 ms | 9.1 ms | 67.2 ms |
| 390×844 / 60 B | 502,856 | 17,971,200 | 14.6 ms | 11.3 ms | 9.1 ms | 58.7 ms |
| 3840×1080 / 33 B | 3,125,276 | 213,663,744 | 145.7 ms | 98.3 ms | 63.6 ms | 587.9 ms |
| 3840×1080 / 51 B | 4,888,345 | 221,626,368 | 88.7 ms | 82.4 ms | 67.2 ms | 670.0 ms |
| 3840×1080 / 60 B | 5,027,002 | 225,607,680 | 119.8 ms | 144.1 ms | 76.2 ms | 695.9 ms |

These are measured benchmark payload bytes, not an estimate of production
geographic gzip size. Thirteen real weather sequences are cycled across a
synthetic reference grid; fine offsets have a synthetic spatial variation. The
benchmark uploads twelve months of 4 B/pixel paired corrections, excluding the
production source/support byte and 32-byte tile headers. Repetition makes this
fixture highly compressible. Local fetch time is not a mobile-network claim.
Phone-sized means viewport size on this Mac, **not phone hardware performance**.
This synthetic experiment excludes basemap work and the geographic fine-tile
decoder; the actual San Francisco experiment above exercises that decoder.

The 60-byte run's largest CPU place-card/GPU map disagreement at thirteen shared
pixels was 0.000023 score points. This checks the same-pixel dual rule,
interpolation, native-dew cap and graded scoring in the experiment; it does not
prove the future application uses the decoder correctly. Saved grayscale PNGs
were inspected: the intended synthetic reference bands and fine spatial
variation render across both sizes, without blank or failed shaders.

The 60-byte annual ultrawide experiment uses ~225.6 MB decoded correction/carrier
textures plus ~66.4 MB for its Float32 readback target. Production should use a
single-channel score texture and avoid this test-only readback target. The
source/support texture adds ~49.8 MB if all twelve full-screen months remain
resident. Loading all twelve months for every physical screen pixel is too
expensive for the product's immediate taste redraw.

The recommended architecture was also executed, rather than inferred from the
expensive full-screen case: compute one score per **visible 30 arc-second pixel**
into a 256×256 tile texture, then present the cached result with one texture
lookup per display pixel. The experiment actually scored 1, 4, 8 and 16 distinct
tiles (65,536 to 1,048,576 fine pixels), using the 60-byte carrier and the same
dual correction. It then drew the cache across each full viewport. The atlas is
synthetic and stretched for lookup timing, not a geographically rendered map.

| Distinct fine tiles | Phone month / year recompute | Wide month / year recompute | Cached phone / wide draw | Fixture gzip bytes | Decoded bytes |
|---|---:|---:|---:|---:|---:|
| 1 | 4.5 / 17.7 ms | 4.5 / 18.3 ms | 4.5 / 4.6 ms | 151,463 | 3,612,288 |
| 4 | 9.1 / 53.9 ms | 9.1 / 49.6 ms | 4.6 / 4.5 ms | 451,913 | 14,397,312 |
| 8 | 13.1 / 97.6 ms | 13.2 / 94.3 ms | 4.3 / 4.6 ms | 851,386 | 28,742,784 |
| 16 | 22.6 / 183.1 ms | 18.2 / 187.2 ms | 4.5 / 4.6 ms | 1,631,738 | 57,433,728 |

Actual wide-viewport decode/upload costs for these four cache sizes were
1.9/4.7, 7.5/10.2, 12.9/11.7 and 27.9/26.3 ms respectively. Timings include the
JavaScript fence polling interval, about 4.5 ms on short operations; they are
completed wall times rather than precise hardware elapsed-query times.
CPU/GPU parity at thirteen shared pixels remained within 0.000023 points for
every cache size. Phone and wide cache screenshots were inspected.

The same fourteen synthetic throughput cases were rerun using the actual
production city offsets, retaining their synthetic spatial perturbation and
cycled reference grid. All GL checks were zero; maximum CPU/GPU disagreement
was 0.000026 points. At eight tiles the wide-view warm month/year recompute was
13.7/101.6 ms; at sixteen tiles it was 17.8/193.6 ms. Representative actual-source
phone and cache PNGs were inspected. These repeated-fixture timings confirm the
budget trend; the actual geographic transfer and projection measurement remains
the separate SF experiment above. The complete repeat is recorded in
`consumer-benchmark-actual/render-results.json`.

This supports close-zoom activation with roughly 1–2 visible tiles on the phone
and up to eight on a wide screen: monthly taste recompute is 4.5–13.2 ms on this
Mac; annual recompute is roughly 18–98 ms, after which panning/drawing takes
about 4.5 ms. It does **not** support claiming a 16-tile annual slider redraw is
instant: the measured cost is about 187 ms. A candidate zoom 9 activation must
be checked against the actual map projection, latitude, viewport and land tile
count; use the measured fine-pixel budget, not an unverified zoom label.

A bounded visible-tile cache keeps Step A's fast overview intact. Do not replay
the same 1 km weather at every high-DPR display pixel. Reuse cached scores until
taste, period or generation changes, and score newly visible tiles as needed.
Physical-phone performance and the real application integration still need
measurement. The place card's single-pixel model is inexpensive and shares the
same decoder, source pixel and generation. This is a measured consumer design,
not a shipped consumer or a phone acceptance test.

## Reproduction

Run in the project environment, with scratch output on the SSD:

```sh
sh research/fine/run.sh consumer_benchmark.py \
  --native-root '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/native' \
  --source-cache '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-sources' \
  --output '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-benchmark' \
  --test-offsets -2 2

devenv shell -- node research/fine/consumer_benchmark.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-benchmark'
```

The Python command writes carrier binaries, CPU card fixtures and
`carrier-results.json`; Node writes viewport gzip fixtures, `render-results.json`
and inspected PNGs. Use `--weather-only` to prepare real weather without source
files. Use `--audit-native-clipping` then `--probe-native-clipping` with the same
paths for the global and two raw-point audits. Those two modes do not require
finished source downloads. The raw-point probe selects the most extreme actual
point in each audited cell rather than inventing an extreme input.

After the builder is complete, replace `--test-offsets -2 2` with
`--tiles <completed fine tile output>` to replay the exact published offsets,
including bilinear period adjustment and recorded generation. This path reads
the builder's real gzip/header/paired-offset/support tiles for all thirteen cities.
The throughput grid remains synthetic.

Keep the final actual-source run separate from the synthetic stress run above:

```sh
sh research/fine/run.sh consumer_benchmark.py \
  --native-root '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/native' \
  --source-cache '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-sources' \
  --tiles '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-30s' \
  --output '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-benchmark-actual'

devenv shell -- node research/fine/consumer_benchmark.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-benchmark-actual'

sh research/fine/run.sh consumer_geographic.py \
  --native-root '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/native' \
  --reference-root '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/reference-global' \
  --land-support '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/land-support-v2' \
  --tiles '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-30s' \
  --output '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-geographic-actual'

devenv shell -- node research/fine/consumer_geographic.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-build/consumer-geographic-actual'
```

The geographic experiment uses the actual San Francisco centre and Web Mercator
viewport at zoom 9, with 512-pixel map tiles: 390×844 and 3840×1080 CSS pixels at
DPR 1. It reads each distinct native quarter-degree point's own complete weather,
selects its native 1° cell's actual eight dates, and directly downloads and decodes
the visible production NWFT gzip files, including support flags and headers.
It computes the fine-pixel cache once and presents it through geographic pixel
lookup. `geographic-land-coverage.json` checks every required land reference;
`geographic-results.json` records actual byte counts, local gzip decode, tile
assembly, completed upload/recompute/draw times and CPU/card parity at shared
pixels. It excludes basemap work, full-period facts and physical-phone testing.
The separate `consumer-geographic` folder holds zero-offset decoder/debug
evidence; its highly compressible tiles are not a production transfer result.

Alternatively, after the source cache is complete, omit `--test-offsets` for source-derived
city corrections. That path currently samples mean warming at the native point;
the finished production tile path additionally uses the documented bilinear
period adjustment. The final actual-tile runs above complete this data-replay
check. Physical-phone timing, real coast/island missing-reference behavior and
map/card parity inside the scheduled interface integration remain acceptance
checks for that future consumer.
