<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->
# Named sunshine: control limits

The monthly satellite means improve all six European and African controls against their complete 2005–2023 SARAH-3 series. That does **not** prove every sampled day's score or nice-day count improves. A monthly correction cannot recover the observed relationship between cloud and the other weather on a particular day.

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
