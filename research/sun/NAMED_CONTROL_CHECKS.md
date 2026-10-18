<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->
# Named sunshine: control limits

The monthly satellite means improve all six European and African controls against their complete 2005–2023 SARAH-3 series. That does **not** prove every sampled day's score or nice-day count improves. A monthly correction cannot recover the observed relationship between cloud and the other weather on a particular day.

## Americas: one typical-year choice

Both GOES typical-year products were checked against independently integrated full-disc observations at the exact named coordinates of Lima, San Francisco and San Diego, for 2022 and 2024. The ordinary typical meteorological year (TMY) improves both sunny share and direct radiation at all three controls. The direct-radiation typical year (TDY) worsens San Diego's direct-radiation error. These controls support using TMY throughout the eligible northern GOES domain; they do not support choosing a different source for each city.

| Monthly sunshine error (percentage points) | Native samples | TMY | TDY |
| --- | --- | --- | --- |
| Lima | 29.83 | 3.71 | 3.56 |
| San Francisco | 10.14 | 5.98 | 8.79 |
| San Diego | 12.91 | 5.23 | 6.98 |

| Hourly direct-normal radiation error (W/m²) | Native samples | TMY | TDY |
| --- | --- | --- | --- |
| Lima | 133.70 | 31.97 | 33.12 |
| San Francisco | 64.70 | 54.34 | 63.53 |
| San Diego | 56.26 | 45.79 | 62.49 |

The two observed years are a stability check, not a long-term normal or an independent satellite instrument. They use approximately 2 km full-disc pixels, whereas TMY and TDY use approximately 4 km pixels; fog gradients can differ within that distance. Complete series were integrated from instantaneous samples into sixty minute-midpoint samples per civil hour. An independent replay of both typical-year products at all five northern validation cities agrees with production to less than 1e−12 sunny share and 1e−8 W/m². It uses the actual selected source year for each month's daylight-saving clock.

Evidence: SSD `sun/named-validation/goes-two-year-normal.json`, `goes-{tmy,tdy}-final-two-year-accuracy.json`, and `goes-typical-integration-proof.json`. Rerun with `named_goes_validation.py`, `named_source_validation.mjs`, and `named_goes_typical_proof.py`. This comparison concerns the satellite inputs; it does not replace the final fourteen-place score/count comparison.

Iquique also has long-period TMY coverage at the northern domain's southern edge. Against its cached 2022/2024 full-disc observations, the final TMY improves monthly sunshine error from 32.77 to 15.64 percentage points and direct-radiation error from 235.2 to 113.7 W/m². That large remaining difference limits confidence in its exact monthly correction. Antofagasta and Valparaíso fall outside that TMY domain and use the two full-disc years themselves. Independent integration reproduces their fitted monthly means; this is a transport/calculation check, not withheld validation of a two-year climatology. Evidence: SSD `sun/named-validation/goes-six-final-accuracy.json` and `goes-six-two-year-normal.json`.

## Americas: exact-date score and count diagnostic

The cached complete GOES 2022/2024 arrays also allow an exact-date radiation substitution on the ERA5 representative days. This exposes additional count regressions despite improved monthly satellite means. All scenarios use identical available dates and the shared place function. There are only 25–33 samples per city, some months have one or two, and **all cities lack October–December samples**: the currently assembled native chunks selected those months in other years. Aggregates therefore cover January–September's 273 days, not a whole year. They cannot be presented as annual observed-weather validation.

| Monthly mean absolute error | Score before → after | Nice days before → after | Perfect days before → after |
| --- | --- | --- | --- |
| Lima | 21.39 → 10.42 | 8.56 → 2.78 | 9.89 → 6.22 |
| Antofagasta | 6.78 → 4.28 | 3.78 → 2.56 | 0.67 → 2.11 |
| Iquique | 10.42 → 7.06 | 5.78 → 3.67 | 1.33 → 1.89 |
| Valparaíso | 1.57 → 1.48 | 0.89 → 0.89 | 1.11 → 0.00 |
| San Francisco | 4.08 → 7.14 | 1.11 → 3.44 | 1.89 → 3.67 |
| San Diego | 2.75 → 5.00 | 1.67 → 3.22 | 2.67 → 4.44 |

San Francisco's January–September nice-day count moves 190 → 169 against an exact-date reference of 200; its perfect count moves 86 → 71 against 82. San Diego's nice count moves 245 → 231 against 245; its perfect count moves 148 → 121 against 125. The aggregate score moves closer at both cities, but monthly score errors and threshold counts do not. Antofagasta and Iquique also have worse aggregate score errors despite closer monthly scores. These results do **not** support the requested universal no-regression gate for places that are right today.

This diagnostic retains the accepted temperature anchors and native rain, humidity and wind. Exact-date cloud and weather relationships can differ from the native ERA5 sequence; monthly mean matching cannot restore them. Two observed years and different 2 km/4 km source pixels add uncertainty. No source-specific or city-specific exception was fitted in response, and no new network data were acquired for this diagnostic. Evidence: SSD `sun/named-validation/goes-six-exact-day-diagnostic.json`. Rerun `named_day_diagnostic.mjs` with `--normal` pointing to `goes-six-two-year-normal.json` and `--goes-root` pointing to the cached GOES directory.

## Rotterdam: the remaining count regression

The exact-date radiation diagnostic substitutes satellite radiation on the retained ERA5 sample dates, while retaining the accepted temperature anchors and ERA5 rain, humidity and wind. All scenarios exclude the same seventeen samples from 2024, beyond SARAH-3's available period. Seventy-nine samples remain; they are reweighted within each month. This is a radiation diagnostic, not a complete observed-weather reference.

| Annual diagnostic | Before | Monthly correction | Exact-date satellite reference |
| --- | --- | --- | --- |
| Mean score | 19.7 | 19.2 | 18.5 |
| Nice days | 79 | 66 | 76 |
| Perfect days | 34 | 34 | 29 |

Monthly score error improves from 6.17 to 2.07 points, but annual nice-day error increases from 3 to 10 days. Applying only the sunny-share correction gives 84 nice days; only the radiation warmth correction gives 71; both give 66. The largest differences are May (12 → 6, reference 12) and September (20 → 15, reference 20). A retained day's warmth can cross the nice threshold even when its actual satellite radiation was brighter than the monthly correction implies. This is a real limitation of applying a monthly correction to eight representative days, not evidence that the monthly radiation mean is wrong.

Seville's annual mean score is unchanged, while monthly score error increases slightly, 1.04 → 1.15 points. Its perfect-day count moves closer to the exact-date reference. These limitations must accompany any claim that controls improve; an unconditional no-regression claim is unsupported.

An offline universal alternative fitted a clipped additive DNI shift, preserving native zero radiation. It did not broadly improve the six controls: Rotterdam was unchanged, while Swakopmund's monthly score error and perfect-day error increased. The production multiplicative rule was retained. Evidence: SSD `sun/named-validation/additive-day-diagnostic.json`; rerun with `named_day_diagnostic.mjs`.

## Arona is not the Tenerife South airport

Three coordinates have appeared under “Tenerife south”:

| Location | Latitude, longitude | Elevation | Role |
| --- | --- | --- | --- |
| Named Arona town | 28.09962, −16.68102 | PVGIS 624 m | The published named place |
| Original sunshine-study coastal point | 28.05500, −16.73100 | PVGIS 6 m | The earlier satellite study |
| AEMET Tenerife Sur Airport, C429I | 28.046944, −16.561111 | AEMET 64 m | Independent station normals |

Arona is 13.14 km from the airport and 6.98 km from the earlier coastal point. The airport is in Granadilla de Abona. Its sunshine duration is a useful broad seasonal check, **not** an exact-coordinate observation of Arona. [AEMET station coordinates and 1981–2010 normals](https://www.aemet.es/en/serviciosclimaticos/datosclimatologicos/valoresclimatologicos?k=&l=C429I).

AEMET reports 193 sunshine hours in January and 295 in July; summing the twelve monthly normals gives 2,725 hours per year. These count the whole daylight day in 1981–2010. The app counts hours after 10:00 meeting its 300 W/m² DNI threshold, and its satellite period is 2005–2023. Dividing station duration by daylight does not create an interchangeable validation target. AEMET is the author of these station data.

Nor does the available satellite evidence demonstrate that Arona's lower sunshine is caused by its elevation. At matched 2020 and 2022 dates, the original coastal point and Arona have almost the same SARAH-3 monthly sunshine shares:

| % sunny | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Coastal point | 56.6 | 70.7 | 41.0 | 44.7 | 58.9 | 57.0 | 84.2 | 76.0 | 49.8 | 54.5 | 58.5 | 47.5 |
| Arona | 56.8 | 70.6 | 39.9 | 42.6 | 56.6 | 55.8 | 84.3 | 76.5 | 50.3 | 54.4 | 58.1 | 45.2 |

The earlier coastal point's monthly mean is 58.28%; Arona's full 2005–2023 monthly mean is 58.30%. The matched two-year comparison supports a regional satellite cloud signal, rather than a demonstrated town-only mountain effect. SARAH-3's approximately 5 km radiation pixels can still miss local cloud gradients; the 1 km temperature detail does not make sunshine a 1 km observation. No location-specific correction is justified by this check.

Evidence: SSD `sun/pvgis/tenerife-south-{2020,2022}.json`, `sun/pvgis/monthly.json`, `sun/named-validation/sarah-longnormal/spain-arona-validation.json`, and `sun/aemet-monthly.csv`. The current named target must always be compared at Arona's actual coordinates.
