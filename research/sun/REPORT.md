<!-- Written by AI on 2026.10.17 by model GPT-6.1 Sol. -->

# Why the fog coasts read sunny

**An ERA5-only 0.25° sunshine offset will not fix this.** Lima has three measured errors: 1° aggregation, excessive ERA5 sunshine at its own point, and a steep coastal gradient still unresolved by 0.25°. On matched July 2022 dates these contribute 8.2, 40.5 and 31.8 percentage points respectively. The place card already uses native ERA5 points, so its Lima figure is not a 1° averaging error.

The smallest adequate correction is a monthly sunshine offset measured from independent satellite direct radiation, retaining its 2–5 km detail on fog coasts. Apply it to each sampled day's sunny share **before** the sunshine curve, and update the card's sunshine facts from the same source. A single extra byte at the existing 25 km points improves their values but cannot resolve Lima's city-to-inland gradient. Keep the agreed scoring curves. Temperature stays with the separate temperature work.

The sunshine-only prototype takes Lima's July card from 92% sunny toward a two-year satellite estimate of 26%, and its default score from 81.6 to 68.4. Antofagasta's July satellite estimate is 54%; Swakopmund's is 81%, downtown San Francisco's 85%. These coasts need different corrections. Sunshine alone cannot remove every 365-day winner under the agreed scoring rule.

This is a research prototype, not a published correction. No application, hosting, temperature or source-pipeline files were edited. The baseline is the currently published five-chunk climate, built on 17 October 2026 at 13:42 UTC. Its files were copied before this experiment so another worker's temperature changes cannot enter these comparisons.

## What the raw data says

I measured 1,800 actual local dates at each of 14 places: five retained ERA5 chunk-years, numbered 76, 78, 80, 82 and 84, spanning 2015–2024 with alternate chunk-years absent. Each contributes 360 complete local dates. These are not ten continuous observed years. The calculation uses production's IANA civil time, hour-ending direct radiation, integrated solar geometry, DNI threshold of 300 W/m², and daylight hours starting at 10:00.

“Cell” below is the complete daily 1° record, including sunny-hour rounding. “Coastal point” is the fine land pixel selected by the published place API. These are means of daily sunny-hour fractions; the card pools sunny/daylight hours and rounds to a whole percent.

| Place | July cell | July coastal point | January cell | January coastal point |
| --- | ---: | ---: | ---: | ---: |
| Lima | 98.0% | 91.8% | 58.2% | 69.6% |
| Antofagasta | 84.7% | 83.7% | 90.4% | 91.1% |
| Iquique | 86.0% | 85.7% | 88.3% | 88.0% |
| Swakopmund | 88.1% | 87.3% | 95.6% | 94.6% |
| Walvis Bay | 88.0% | 88.3% | 97.2% | 95.9% |
| Valparaíso | 71.1% | 72.7% | 97.0% | 96.9% |
| San Francisco | 89.3% | 89.4% | 52.8% | 50.8% |

For Lima in July, aggregation adds 6.2 percentage points, but removing it still leaves 91.8% sunny. Antofagasta, Swakopmund and San Francisco change by about one percentage point or less. For the annual mean, replacing cell sunshine with native sunshine actually raises Lima by 4.3 percentage points. There is no general “desert inland contaminates the coast” explanation for these results.

Moving offshore is insufficient too. At ERA5 points with less than 5% land within 0.5° of the cities, July sunny shares are 70.6–89.8% near Lima, 84.9–86.8% near Antofagasta, 84.8–86.0% near Swakopmund, and 81.1–84.0% near San Francisco. These are model values, not observations.

I checked the radiation plumbing separately. A fresh anonymous Earthmover read at Lima reproduces the cached `fdir` values after the existing float16 encoding; the largest quantisation difference is 0.249 W/m². The source explicitly describes one-hour accumulations ending at valid time. Native horizontal direct radiation already averages 585 W/m² at Lima's July noon, before the DNI conversion. A separate minute-integrated solar calculation gives 91.3% sunny rather than production's 91.8%. Its July changes at the other four required target coasts are below 0.3 percentage point. Solar integration has larger effects in a few other place-months, up to 4.4 percentage points at Iquique in September, but cannot explain a grey Lima winter reading near 92% sunny.

The bad coastal temperature substitution is independent: temperature and dew point use the substituted source indices; radiation uses the target's native indices. It does not leak the inland temperature source into sunshine. Eight-day selection is another small, separate difference: Lima's July cell is 98.0% sunny over all retained dates and 96.9% in its eight representative days. The card is 92%. All three remain implausibly bright.

## Separating model error from the remaining coastal gradient

The satellite comparison was repeated at the selected ERA5 coordinate, as well as at town. The nearest NSRDB pixel to Lima's ERA5 point is about 1 km from that point; its requested coordinate is 12.000° S, 77.000° W and selected pixel 12.000° S, 77.010° W. Town is approximately 7 km southwest. All four values below use the same 31 July 2022 dates:

| Location/data | Sunny share |
| --- | ---: |
| 1° ERA5 record | 98.0% |
| Native 0.25° ERA5 point | 89.8% |
| Satellite at that ERA5 point | 49.3% |
| Satellite at Lima town centre | 17.5% |

The total 80.5 percentage point discrepancy splits into **8.2 points from 1° aggregation, 40.5 points from ERA5's radiation error at the same point, and 31.8 points from the unresolved town-to-point gradient**. On this measured month, roughly half the excess is ERA5 itself, two-fifths the remaining coastal representation, and one-tenth the 1° aggregation. These fractions describe this location and month, not a universal coastal decomposition.

The steep satellite gradient was checked directly against the original HDF5 data and pixel metadata. At 16:50 UTC on 5 July 2022, DNI is 816 W/m² at the near-ERA5 pixel and 12 W/m² at town. At 19:20 UTC on 22 July it is 817 versus 5 W/m². All four source reads match the saved arrays exactly. The near-ERA5 pixel's metadata gives 230 m elevation; this is a distinct inland location, not a relabelled city pixel.

Swakopmund differs. Across matched July 2020/2022 dates the cell is 88.2%, native ERA5 87.3%, and satellite 80.8% both at town and at the ERA5 point. Here the 1° penalty is 0.9 point, the same-point radiation error 6.5 points, and there is no measured town-to-point difference. In January the native-point radiation error is 20.2 points. Co-located independent radiation was not extracted at the other cities; their native-to-town differences retain a location-representation component.

## Independent observations, and what they mean

The station work downloaded 119 of 144 requested GHCN-hourly station-years for 2016–2024 and retained 209,954 valid daytime cloud reports, choosing one report per local hour. It distinguishes total cloud, cumulative layers and discrete layers, keeps quality flags, and records missing coverage. Maximum discrete-layer coverage lower-bounds total cloud; it must not be mistaken for a measured total.

| Station | July unobscured sky | July broken/overcast reports | Coverage limit |
| --- | ---: | ---: | --- |
| Antofagasta airport | 43.2% | 45.5% | Monthly reporting coverage 65–85% across the year |
| Iquique airport | 29.0% | 64.5% | Monthly coverage 29–81% |
| Airport near Valparaíso | 28.9% | 67.5% | About 17 km from town; monthly coverage 43–57% |
| San Francisco airport | 63.6% | — | Nearly complete hours; bay side, about 17 km from town |
| San Diego airport | 47.8% | — | Nearly complete hours |
| Singapore Changi | 16.9% | 87.4% | Nearly complete hours |

**Cloud opening is not sunny-hour share.** Cloud amount does not say where the sun is, whether a thin cloud transmits direct light, or what the hour-mean DNI was. These observations independently reject the description “almost cloudless” on the Chilean coast and protect Singapore from an unjustified sunny correction. They are not substituted for the app's sunshine quantity.

Lima has only one usable daytime cloud report across the requested nine years. Files existing for 2016–2019 did not provide seasonal coverage. Swakopmund has none; nearby Walvis Bay is an inland airport with only 18–25% monthly coverage. The selected Málaga, Tenerife south and Cape Town GHCNh records have temperatures but no usable daytime clouds. Missing clouds do not mean clear sky.

AEMET's independent 1981–2010 station normals give January/July sunshine of 180/347 hours per month at Málaga, 183/354 at Seville and 193/295 at Tenerife south. These are valuable seasonal checks but cover the entire daylight day in an older period. They cannot be equated to sunshine after 10:00 at the 300 W/m² threshold. [AEMET Málaga](https://www.aemet.es/en/serviciosclimaticos/datosclimatologicos/valoresclimatologicos?k=and&l=6155A), [Seville](https://www.aemet.es/en/serviciosclimaticos/datosclimatologicos/valoresclimatologicos?k=and&l=5783), [Tenerife south](https://www.aemet.es/en/serviciosclimaticos/datosclimatologicos/valoresclimatologicos?k=coo&l=C429I).

## Independent direct radiation

The Americas comparison uses **NSRDB GOES full-disc v4**, about 2 km pixels and instantaneous DNI every ten minutes: 2022 and 2024 at Lima and Antofagasta, 2024 at Valparaíso and San Francisco, and 2022 at San Diego. Six samples within each local clock hour approximate its mean DNI. The calculation then applies the same 300 W/m² threshold and integer sunset-hour denominator as production. Complete required daylight hours are mandatory; missing daylight is never filled with zero. [Producer dataset specification](https://developer.nlr.gov/docs/solar/nsrdb/nsrdb-GOES-full-disc-v4-0-0-download/).

Africa and Europe use **CM SAF SARAH-3 through JRC PVGIS**, 2020 and 2022, at Swakopmund, Cape Town, Málaga, Seville, Rotterdam and Tenerife south. These are approximately 5 km satellite radiation pixels. PVGIS supplies hourly satellite snapshots; direct horizontal radiation divided by the sine of solar elevation gives DNI. Linear interpolation between snapshots and minute integration approximate clock-hour means. This is a lower temporal resolution check than NSRDB. The API's radiation database is explicitly verified as SARAH-3. Its separate ERA5 temperature/wind columns are not used. [JRC radiation documentation](https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis/getting-started-pvgis/pvgis-data-sources-calculation-methods_en).

Both sources retrieve clouds from satellite imagery and model radiation through them. They are independent of ERA5's cloud simulation, but neither is a ground radiometer. Their instantaneous sampling also approximates the exact hour-mean metric. Source coordinates, requested coordinates, timestamps, extraction URLs and original values are retained so those differences can be examined.

The eight saved NSRDB point-years contain their expected 52,560 or 52,704 ten-minute samples, with no missing time steps or numerical fill sentinels. Two local 31 December dates cannot be completed from a UTC-year file and are excluded. NSRDB's own `fill_flag` was not extracted, so this audit does not establish whether every upstream cloud retrieval was observed or model-filled.

The matched July dates are decisive: Lima's 1° records give 98.8%, the native ERA5 point 92.3%, and the satellite at town 26.2%. Antofagasta gives 86.7%, 85.8% and 54.5%. Removing aggregation makes a small improvement while leaving a large radiation discrepancy. Valparaíso's single sampled July is 67.5%; its larger seasonal correction is in the nominally near-cloudless summer. The complete monthly comparisons below avoid imposing a Lima-shaped correction on every coast.

Lima also shows why one year is not a normal: July is 17.5% sunny in 2022 and 34.8% in 2024. Both reject 92%, but their spread matters. The prototype uses the two-year average; the held-out check learns from 2022 and tests 2024 separately.

The controls do not support the assumption that all their original sunshine values were correct. A bias learned in 2020 reduces mean monthly error against withheld 2022 SARAH-3 radiation at all five European/African controls. Tenerife south changes most in score: its twelve-month mean falls from 80.9 to 76.7, with a maximum monthly fall of 8.0 points. That substantial change warrants a longer-period check before publication. Rotterdam has one worse held-out month, and Swakopmund three: average improvement does not mean every month improves. San Diego has only one independent radiation year here; Singapore has cloud observations but no independent DNI sample and receives no sunshine correction. This establishes a promising correction, not a worldwide regression-free release.

## The prototype

For each fine pixel and month, fit one offset in percentage points to the observed satellite sunshine fraction. Add it to every retained day's sunny-hours/daylight-hours ratio, clamp to 0–100%, then apply the existing SUN curve and the remaining daily calculation. Use one offset before the current spatial blend. A one-byte encoding gives 1 percentage point steps and reserves 255 for sea.

All temperature bytes, thermal offsets, rain, wind, humidity, representative days and interpolation weights remain fixed. This isolates the sunshine multiplier. It deliberately does not replace the direct-radiation contribution to felt temperature: that would need a separate fine correction to the sun-warmth profile and must be combined with the temperature worker's result.

That remaining radiation warmth is material: lowering the SUN multiplier while retaining ERA5's excessive direct-radiation heating does not produce a fully corrected physical day. The other worker's coastal air-temperature fix addresses a different error. These scores show the isolated sunshine-factor effect; a physically complete score needs the satellite-derived radiation's effect on felt temperature too.

The unchanged calculation reproduces all 168 API-selected published monthly scores within 0.000002 score point before integer rounding. A second implementation checked six sunshine offsets across every place-month and found the same agreement. The encoded offset can leave at most 0.5 percentage point of target mismatch.

Matching the fitted target is not an accuracy test. Independent source comparisons and held-out years below assess the input correction. A monthly offset retains ERA5's daily ordering, with ties introduced by clipping, and relies on ERA5's association between clouds and other weather; it cannot recover which individual days were really cloudy. The resulting nice-day figures remain estimates from the eight representative days.

The probe attaches each city's satellite target to that city's API-selected fine pixel. Several selected pixels are displaced from town by 20–33 km. This is an explicit place-level sensitivity experiment, not a correctly georeferenced global correction field. A build must query the independent source at every actual output pixel; the co-located measurements above test how much that distinction matters.

There is an existing location-selection mismatch worth keeping separate. The place API selects land relative to a rounded grid coordinate; a browser click selects relative to the actual fractional coordinate. Six requested cities can therefore select different fine pixels. San Diego's monthly score can differ by 12.9 points, San Francisco's by 4.8 points. These tables explicitly use the API-selected published pixel, rather than claiming an exact city-click screenshot baseline. The audit records every coordinate and score difference; no selection code was changed.

A 0.25° correction is still roughly 25 km. It will not distinguish San Francisco's western fog belt from downtown several kilometres away, or every narrow coast from the hills behind it. Lima's co-located check demonstrates that this is a material error here, rather than a hypothetical resolution concern.

## What shipping this would require

1. Build independent satellite sunshine normals with matched source years and actual after-10:00 hour means. Retain 2–5 km source detail along the fog coasts, including at the requested place coordinates. Derive monthly offsets against the existing four-cell weather blend at each output position. This corrects source bias and spatial averaging together; an ERA5-point-minus-cell offset alone cannot. The source period needs extending before calling these normals.
2. Store monthly 8-bit sunshine offsets. A 0.25° global field costs 1.04 MB decoded per month, 12.46 MB for the year; packed for 250,011 land points, 3.00 MB before compression. That is the smallest point-bias improvement, but is insufficient for Lima's measured gradient. A global 0.05° field (about 5 km) would cost 25.93 MB per month, 311.13 MB per year before compression. Retaining that detail only in coastal tiles avoids allocating it worldwide; the actual coastal coverage and compressed size are unmeasured. The research probe's twelve PNGs total 196 kB and change only measured pixels, so they are not a production size estimate.
3. Apply the correction before each day's SUN multiplier in both the GPU and the point sampler. The current GPU first pass has already averaged away the days, so simply multiplying its final monthly score is wrong for nice-day counts and loses covariance. A direct implementation keeps the eight per-day weather scores without SUN until the fine-grid pass, then reads each day's original sunshine fraction, applies the offset and counts days scoring at least 55. The original sunny/daylight bytes must remain accessible there; a compact two-channel array for all land-cell days costs about 3.6 MB for twelve months. R32F per-day scores use twice today's RGBA32F monthly score-intermediate memory. R16F would use about the same memory, but needs a precision check at the 55-point threshold. Both require roughly eight times as many resolve score lookups, plus sunshine reads. Mobile and full-year redraw latency is unmeasured here and must be checked before choosing that implementation.
4. Sample the same coastal sunshine field for the place card and score, at the actual requested place. Replace the sunshine fact from the same source; rebuild default scores and score images. A 25 km map can display corrected 25 km points, but resolving the measured Lima gradient on the map requires finer score output along that coast too. The present pixel-selection mismatch must not assign a town's measured value to a displaced grid point. The card, API, map, explanation, link preview and share card must describe the same sunshine quantity. No new runtime weather provider or account is needed.

A global 5 km score resolve would process 25 times today's fine-grid pixels and use 25 times its final score-buffer memory. Combined with eight daily score reads, a naive global implementation would perform roughly 200 times today's resolve score lookups. This is why the recommendation retains high detail only on the affected coasts, rather than increasing the entire world's scoring grid. The coastal coverage and resulting latency still need measurement; the point prototype has not established browser performance for that change.

Keep this correction on **typical-year climate**. A monthly climatological correction is not a prediction of tomorrow's marine layer; copying it into the 14-day forecast would need its own forecast validation.

The offline source cost is larger than the browser payload. The anonymous NSRDB file is about 3.6 TB per year, but a point extraction reads roughly 54 MB of DNI ranges because storage groups 500 nearby sites together. A complete cached-index point took about 3–6 minutes in this run. A production acquisition should share chunks between neighbouring points and retain several source years. This experiment has not measured the cost of a worldwide build or established a worldwide normal from its one- and two-year samples.

Sunshine alone cannot promise to remove all 365-day winners. The agreed SUN curve bottoms out at 0.75. A day with an otherwise perfect score still scores 75 under zero sunshine, well above the 55-point “nice” threshold. Fixing sunshine must not become an unrequested change to Josh's taste. The separate temperature correction is essential to assessing the all-year ranking.

## Monthly sunshine and default scores

All sunshine columns are percent. Scores are out of 100. “Card” is the published native-point fact; “engine” is the existing eight-day, four-cell blend. “After” is the encoded sunshine-offset probe. Temperature and all other weather stay fixed. Thus the card-to-after difference is not entirely a change to engine sunshine. Singapore has no independent DNI sample here: its engine and score are unchanged, and its card should remain unchanged too.

| Place | Independent radiation period | Mean monthly score before → after |
| --- | --- | ---: |
| Lima | 2022, 2024 | 69.6 → 63.4 |
| Antofagasta | 2022, 2024 | 95.7 → 92.9 |
| Swakopmund | 2020, 2022 | 92.6 → 91.6 |
| Valparaíso | 2024 | 82.0 → 80.4 |
| San Francisco | 2024 | 65.2 → 64.6 |
| Málaga | 2020, 2022 | 67.0 → 66.2 |
| Seville | 2020, 2022 | 63.9 → 63.7 |
| Cape Town | 2020, 2022 | 73.3 → 72.3 |
| Tenerife south | 2020, 2022 | 80.9 → 76.7 |
| San Diego | 2022 | 77.3 → 76.8 |
| Rotterdam | 2020, 2022 | 25.0 → 24.4 |
| Singapore | No DNI correction | 9.4 → 9.4 |

### Lima

Source: NSRDB; years: 2022, 2024. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 70% | 60.8% | 53.0% | 58.5 → 57.4 |
| Feb | 63% | 55.9% | 50.9% | 46.7 → 46.0 |
| Mar | 61% | 50.7% | 55.7% | 47.9 → 48.6 |
| Apr | 79% | 72.0% | 66.0% | 67.7 → 67.0 |
| May | 93% | 92.6% | 55.6% | 82.5 → 77.6 |
| Jun | 91% | 93.0% | 33.3% | 84.1 → 72.5 |
| Jul | 92% | 94.6% | 26.6% | 81.6 → 68.4 |
| Aug | 87% | 87.5% | 21.5% | 78.0 → 64.3 |
| Sep | 85% | 82.3% | 21.6% | 76.1 → 63.1 |
| Oct | 80% | 72.2% | 37.4% | 74.2 → 67.3 |
| Nov | 73% | 67.0% | 36.0% | 71.8 → 65.6 |
| Dec | 72% | 64.2% | 45.2% | 66.4 → 63.0 |

### Antofagasta

Source: NSRDB; years: 2022, 2024. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 91% | 94.3% | 78.3% | 97.1 → 96.4 |
| Feb | 86% | 90.9% | 79.9% | 94.8 → 94.8 |
| Mar | 95% | 98.9% | 79.9% | 98.4 → 98.1 |
| Apr | 88% | 90.7% | 72.7% | 98.5 → 96.6 |
| May | 85% | 86.2% | 60.2% | 95.7 → 90.9 |
| Jun | 84% | 88.9% | 59.9% | 91.7 → 87.1 |
| Jul | 84% | 84.7% | 54.7% | 87.7 → 82.5 |
| Aug | 88% | 89.0% | 59.0% | 92.8 → 87.9 |
| Sep | 97% | 98.8% | 61.7% | 96.5 → 92.0 |
| Oct | 99% | 100.0% | 64.0% | 97.8 → 93.9 |
| Nov | 93% | 93.2% | 68.2% | 98.4 → 95.5 |
| Dec | 87% | 90.9% | 79.9% | 99.4 → 99.4 |

### Swakopmund

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 95% | 96.2% | 75.2% | 93.7 → 92.6 |
| Feb | 92% | 92.5% | 73.5% | 93.2 → 91.7 |
| Mar | 88% | 90.0% | 77.0% | 92.9 → 92.2 |
| Apr | 95% | 93.3% | 76.3% | 92.0 → 90.9 |
| May | 90% | 88.5% | 86.5% | 91.2 → 91.2 |
| Jun | 88% | 88.9% | 86.9% | 92.3 → 92.3 |
| Jul | 87% | 88.7% | 80.7% | 90.1 → 90.1 |
| Aug | 92% | 90.1% | 78.1% | 86.1 → 85.5 |
| Sep | 97% | 100.0% | 77.0% | 90.6 → 89.9 |
| Oct | 88% | 90.0% | 64.0% | 95.9 → 92.0 |
| Nov | 87% | 88.8% | 72.8% | 95.5 → 93.8 |
| Dec | 90% | 95.0% | 76.0% | 97.8 → 96.7 |

### Valparaíso

Source: NSRDB; years: 2024. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 97% | 98.9% | 75.9% | 97.7 → 96.7 |
| Feb | 95% | 95.5% | 70.5% | 97.0 → 94.7 |
| Mar | 91% | 92.3% | 71.3% | 98.2 → 96.2 |
| Apr | 78% | 82.1% | 62.1% | 89.1 → 86.3 |
| May | 72% | 75.1% | 58.2% | 76.3 → 74.4 |
| Jun | 65% | 60.2% | 37.9% | 53.5 → 51.0 |
| Jul | 73% | 76.6% | 67.8% | 55.1 → 54.7 |
| Aug | 76% | 68.1% | 55.2% | 63.4 → 61.9 |
| Sep | 88% | 86.2% | 67.2% | 82.1 → 80.7 |
| Oct | 85% | 86.3% | 57.2% | 82.0 → 78.2 |
| Nov | 88% | 88.6% | 80.6% | 91.7 → 91.5 |
| Dec | 98% | 100.0% | 79.0% | 98.2 → 98.0 |

### San Francisco

Source: NSRDB; years: 2024. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 51% | 49.2% | 41.9% | 23.6 → 23.2 |
| Feb | 83% | 85.9% | 45.1% | 50.6 → 46.8 |
| Mar | 66% | 67.4% | 59.4% | 40.3 → 40.0 |
| Apr | 82% | 83.2% | 69.2% | 63.3 → 62.1 |
| May | 81% | 81.2% | 81.2% | 77.2 → 77.2 |
| Jun | 88% | 80.7% | 80.7% | 91.1 → 91.1 |
| Jul | 89% | 90.3% | 85.3% | 93.3 → 93.2 |
| Aug | 89% | 92.5% | 85.5% | 95.1 → 95.1 |
| Sep | 88% | 86.2% | 81.2% | 90.7 → 90.5 |
| Oct | 82% | 85.4% | 81.4% | 83.0 → 82.8 |
| Nov | 74% | 81.0% | 60.5% | 55.0 → 53.4 |
| Dec | 56% | 63.4% | 56.9% | 19.7 → 19.5 |

### Málaga

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 70% | 71.9% | 65.1% | 43.2 → 42.9 |
| Feb | 79% | 76.1% | 68.2% | 54.4 → 54.1 |
| Mar | 66% | 62.3% | 46.3% | 46.4 → 45.1 |
| Apr | 77% | 70.7% | 54.5% | 68.9 → 67.2 |
| May | 91% | 90.3% | 80.3% | 85.3 → 85.0 |
| Jun | 97% | 98.6% | 91.6% | 89.7 → 89.7 |
| Jul | 99% | 100.0% | 93.0% | 70.0 → 70.0 |
| Aug | 96% | 97.7% | 86.7% | 65.6 → 65.5 |
| Sep | 82% | 85.5% | 73.5% | 81.4 → 80.4 |
| Oct | 78% | 73.6% | 65.6% | 86.1 → 85.2 |
| Nov | 75% | 81.2% | 61.3% | 63.9 → 61.7 |
| Dec | 69% | 71.5% | 56.7% | 48.7 → 47.3 |

### Seville

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 68% | 70.1% | 70.1% | 36.6 → 36.6 |
| Feb | 73% | 71.5% | 73.2% | 51.0 → 51.1 |
| Mar | 63% | 71.4% | 53.0% | 44.7 → 43.1 |
| Apr | 71% | 69.9% | 60.9% | 64.2 → 63.5 |
| May | 84% | 85.8% | 79.8% | 82.4 → 82.2 |
| Jun | 90% | 87.5% | 91.9% | 85.8 → 86.6 |
| Jul | 99% | 100.0% | 96.0% | 69.6 → 69.6 |
| Aug | 98% | 100.0% | 89.0% | 69.6 → 69.6 |
| Sep | 83% | 83.0% | 78.0% | 83.8 → 83.4 |
| Oct | 73% | 68.3% | 69.9% | 80.4 → 80.6 |
| Nov | 68% | 57.6% | 57.6% | 58.8 → 58.8 |
| Dec | 65% | 59.7% | 51.6% | 39.4 → 38.8 |

### Cape Town

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 91% | 94.7% | 78.7% | 81.5 → 80.9 |
| Feb | 90% | 84.7% | 81.7% | 84.2 → 84.1 |
| Mar | 86% | 84.4% | 74.4% | 87.9 → 87.4 |
| Apr | 79% | 83.7% | 69.7% | 83.7 → 81.9 |
| May | 80% | 87.3% | 59.8% | 77.0 → 73.9 |
| Jun | 73% | 75.4% | 61.8% | 58.2 → 57.4 |
| Jul | 73% | 64.5% | 62.8% | 51.1 → 51.1 |
| Aug | 71% | 72.2% | 55.3% | 47.9 → 46.5 |
| Sep | 81% | 73.6% | 64.6% | 61.8 → 61.4 |
| Oct | 85% | 88.8% | 74.7% | 76.6 → 75.8 |
| Nov | 85% | 82.5% | 72.5% | 82.4 → 81.7 |
| Dec | 94% | 94.7% | 72.7% | 87.3 → 85.8 |

### Tenerife south

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 86% | 80.9% | 57.1% | 79.5 → 75.9 |
| Feb | 90% | 90.8% | 70.8% | 73.8 → 72.3 |
| Mar | 88% | 86.3% | 41.3% | 73.4 → 65.4 |
| Apr | 89% | 87.6% | 44.6% | 81.7 → 73.8 |
| May | 89% | 92.5% | 58.5% | 87.0 → 82.1 |
| Jun | 94% | 94.0% | 57.0% | 89.1 → 83.7 |
| Jul | 93% | 93.9% | 83.9% | 82.1 → 82.1 |
| Aug | 89% | 90.5% | 75.5% | 77.5 → 76.7 |
| Sep | 87% | 85.6% | 49.4% | 79.7 → 73.9 |
| Oct | 84% | 80.1% | 54.2% | 80.3 → 76.6 |
| Nov | 82% | 80.9% | 59.0% | 84.0 → 80.7 |
| Dec | 82% | 77.6% | 47.1% | 83.4 → 77.7 |

### San Diego

Source: NSRDB; years: 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 75% | 73.3% | 68.5% | 61.1 → 60.9 |
| Feb | 84% | 92.8% | 76.0% | 63.5 → 63.2 |
| Mar | 80% | 80.1% | 79.2% | 59.1 → 59.1 |
| Apr | 79% | 83.9% | 78.9% | 74.2 → 74.0 |
| May | 86% | 89.5% | 69.5% | 86.5 → 84.5 |
| Jun | 93% | 95.4% | 83.4% | 95.2 → 95.1 |
| Jul | 93% | 89.1% | 82.1% | 88.2 → 88.0 |
| Aug | 89% | 89.2% | 83.2% | 85.7 → 85.6 |
| Sep | 85% | 87.9% | 76.9% | 86.4 → 85.9 |
| Oct | 82% | 83.4% | 68.6% | 91.2 → 89.6 |
| Nov | 87% | 87.9% | 77.9% | 81.1 → 80.7 |
| Dec | 75% | 69.9% | 62.1% | 55.7 → 55.1 |

### Rotterdam

Source: SARAH-3 via PVGIS; years: 2020, 2022. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 23% | 24.3% | 11.2% | 0.1 → 0.1 |
| Feb | 39% | 36.7% | 24.6% | 0.1 → 0.1 |
| Mar | 50% | 45.7% | 52.7% | 1.9 → 1.9 |
| Apr | 61% | 63.6% | 56.1% | 15.3 → 15.1 |
| May | 65% | 64.8% | 59.5% | 40.0 → 39.8 |
| Jun | 59% | 52.3% | 50.4% | 49.3 → 49.1 |
| Jul | 70% | 74.4% | 46.2% | 63.7 → 59.7 |
| Aug | 67% | 53.4% | 48.2% | 65.9 → 65.2 |
| Sep | 51% | 53.1% | 41.1% | 49.8 → 48.0 |
| Oct | 34% | 29.9% | 21.6% | 12.4 → 11.9 |
| Nov | 26% | 18.0% | 25.0% | 1.4 → 1.5 |
| Dec | 19% | 18.7% | 20.8% | 0.5 → 0.5 |

### Singapore

Source: unchanged; no satellite sample; years: none. These are observed-period sensitivity targets, not established long-term climate normals.

| Month | Card sun | Engine sun | After sun | Default score before → after |
| --- | ---: | ---: | ---: | ---: |
| Jan | 58% | 53.4% | 53.4% | 15.1 → 15.1 |
| Feb | 68% | 68.8% | 68.8% | 19.7 → 19.7 |
| Mar | 69% | 68.1% | 68.1% | 12.4 → 12.4 |
| Apr | 59% | 61.9% | 61.9% | 6.1 → 6.1 |
| May | 49% | 51.6% | 51.6% | 4.2 → 4.2 |
| Jun | 45% | 25.6% | 25.6% | 6.7 → 6.7 |
| Jul | 57% | 54.1% | 54.1% | 8.9 → 8.9 |
| Aug | 59% | 48.8% | 48.8% | 8.8 → 8.8 |
| Sep | 50% | 48.4% | 48.4% | 8.0 → 8.0 |
| Oct | 62% | 54.9% | 54.9% | 7.2 → 7.2 |
| Nov | 42% | 45.5% | 45.5% | 5.3 → 5.3 |
| Dec | 46% | 43.4% | 43.4% | 10.6 → 10.6 |

## Matched July dates

Both ERA5 columns and the independent source use exactly the same retained local dates. Satellite values here are at town centres; the co-located check in the main report addresses the remaining location difference.

| Place | Paired days | 1° ERA5 record | Native ERA5 point | Independent satellite |
| --- | ---: | ---: | ---: | ---: |
| Lima | 62 | 98.8% | 92.3% | 26.2% |
| Antofagasta | 62 | 86.7% | 85.8% | 54.5% |
| Swakopmund | 62 | 88.2% | 87.3% | 80.8% |
| Valparaíso | 31 | 82.1% | 83.2% | 67.5% |
| San Francisco | 31 | 90.0% | 90.3% | 85.0% |
| Málaga | 62 | 99.7% | 99.4% | 92.5% |
| Seville | 62 | 99.4% | 99.1% | 96.2% |
| Cape Town | 62 | 80.5% | 78.0% | 62.6% |
| Tenerife south | 62 | 93.0% | 93.1% | 84.2% |
| San Diego | 31 | 93.0% | 93.0% | 82.6% |
| Rotterdam | 62 | 63.6% | 66.9% | 46.2% |

## Held-out year check

For each month, fit native ERA5-to-satellite bias in the earlier source year, add it to each retained ERA5 day in the later year and clamp to 0–100%. Compare monthly means against that later satellite year, without fitting to it. Only paired months with at least 15 days in both years enter. The retained ERA5 chunks provide January–September for the PVGIS comparison and January–August for the NSRDB comparison, not a full held-out year. This tests transfer across years within the independent source; it does not prove ground-radiometer accuracy or validate every month individually.

| Place | Train → test | Months | Mean absolute sunny-share error before → after | Months with larger error |
| --- | --- | ---: | ---: | ---: |
| Lima | 2022 → 2024 | 8 | 35.6 → 7.3 percentage points | 0 |
| Antofagasta | 2022 → 2024 | 8 | 22.7 → 6.9 percentage points | 0 |
| Swakopmund | 2020 → 2022 | 9 | 11.9 → 4.6 percentage points | 3 |
| Málaga | 2020 → 2022 | 9 | 10.3 → 4.3 percentage points | 0 |
| Seville | 2020 → 2022 | 9 | 5.5 → 2.9 percentage points | 0 |
| Cape Town | 2020 → 2022 | 9 | 12.8 → 4.9 percentage points | 0 |
| Tenerife south | 2020 → 2022 | 9 | 31.8 → 11.4 percentage points | 0 |
| Rotterdam | 2020 → 2022 | 9 | 11.8 → 7.6 percentage points | 1 |

## Rights and reproducibility

The inputs used for calculated values have explicit publication routes: GHCN-hourly CC0, ERA5 CC BY 4.0, NSRDB CC BY 3.0 US, PVGIS's unrestricted product-specific reuse grant with source acknowledgement, and AEMET's commercial/noncommercial reuse grant with attribution. Full primary links, conditions and exclusions are in [LICENCES.md](LICENCES.md). EarthEnv's 1 km cloud product is CC BY-NC and was excluded. No account, key, paid request, email or push was used.

All large inputs, copied baseline files and derived outputs are under `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/`. In particular: `era5/monthly.csv`, `era5/neighbours-monthly.csv`, `era5/radiation-solar-audit.csv`, `era5/archive-cache-audit.json`, `era5/browser-selection-audit.csv`, `observations-2016-2024-monthly.csv`, `observations-2016-2024-hourly.csv.gz`, `aemet-monthly.csv`, `satellite/audit.json`, `satellite/colocation-direct-check.json`, `comparison/matched-monthly.csv`, `comparison/colocated-monthly.csv`, `comparison/year-holdout.csv`, `prototype/monthly.csv`, and the satellite/PVGIS raw files with provenance. The scripts live beside this report in `research/sun/`; [README.md](README.md) gives the commands.
