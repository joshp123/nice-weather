# Decisions

Written by AI on 2026.10.16 by model claude opus 5.5. One list of every product decision, so none is lost in chat. Josh answers the open ones once; the answers then go into SPEC.md and are not asked again. Josh's words in `.agent/josh-steering-ledger.md` are the authority.

## Already decided by Josh

| Area | Decision | Josh's words (record) |
|---|---|---|
| Hosting | Public on nice.jjpcodes.com, on ChatGPT Sites; heavy work server-side so the browser is fast | "server side stuff is free on chatGPT sites ... use sites as much as possible so browser is FAST. public DNS on jjpcodes is fine" (082) |
| Hosting | Runs in the cloud only, nothing on Josh's machines | "make sure it runs just in the cloud where possible" (084) |
| Product | Two questions: where is it nice in the next weeks, and how many days per season or year are nice (where to spend the winter) | (086) |
| Time | Seasons are Winter (Dec to Feb), Spring, Summer, Autumn | "use the dec 1 - feb 28 model" (088) |
| Scope | Best-places list waits | "later but not yet" (088) |
| Look | Light and dark appearance both | "test it in light mode and dark mode" (088) |
| Taste | Configurable, like Theo's | "his is configurable remember" (089), "again, needs to be configurable" (090) |
| Taste | Comfort counts the high and the comfortable time | "count a bit of both plus like the comfortable time" (089) |
| Taste | Temperature matters far more than cloud | "i'd rather warm and overcast than cold and sunny? (mostly?)" (090) |
| Taste | 16 °C is mediocre; ideal 22 to 27 °C; Singapore humidity is awful; Poor and Awful differ; Spain in November is great | (012, P1, P2, 052) |
| Data | Climate from recent real days, with Theo's corrections; computed on Sites | "lets do best of both worlds?" (089), (088) |
| Engineering | Orthogonal components; taste separate from presentation; a spec agreed before building | (089) |
| Process | Pipeline and speed: just do. UX redesign: deliberate. Agents iterate on the UI once the direction is locked; less is more | (089, 091) |

## Open decisions

Effects use the twelve test places on corrected inputs (`/tmp/nwf-agents/round3/opus/`). "Nice" means Nice or better.

### A. What a nice day is

**A1. How the four factors combine into a day's grade.**
- Weighted, gated: a weighted mix, but at most one grade better than the worst factor (Ryan Hall's method). **Recommended.**
- Weighted only (today): generous; London July 92%.
- Worst factor only (Theo's all-must-pass): strict; Tenerife January 65%.

**A2. How good can a warm, dry, calm but fully overcast day be?** Your words: rather warm and overcast than cold and sunny.
- Fair at best.
- Nice at best. **Recommended.**
- Beautiful at best.

**A3. How much each factor counts.**
- Today: Comfort 45%, Dry 25%, Clear sky 20%, Calm 10%.
- Temperature-heavy: Comfort 55%, Dry 25%, Clear sky 10%, Calm 10%. **Recommended.** With A2 at "Nice at best": Málaga November 68 → 82% nice, Tenerife January 84 → 93%, Rotterdam January 0%, Singapore 0%, but London July 59 → 90%.

**A4. Comfort.**
- Average of the afternoon high's score and the ten daytime hours' score, capped by the hottest hour. **Recommended.** Seville July 16% (too hot); Málaga November keeps its warm afternoons.
- Afternoon high only.
- Comfortable hours only.

**A5. The temperature curve.** 8 °C = 0, 12 = 20, 16 = 45, 18 = 70, 20 = 88, 22 to 27 = 100, 30 = 85, 33 = 55, 36 = 20, 39 = 0.
- Keep. **Recommended.**
- Change (say how).

**A6. Humidity.** Dew point up to 16 °C fine, 18 slightly sticky, 21 the edge of Nice, 24 awful.
- Keep. **Recommended.**
- Stricter or looser (say how).

**A7. Rain.** A day is judged on its 10:00 to 20:00 rain. Cards also show wet days (1 mm or more over the whole day), where ERA5 over-counts in dry climates (Málaga January 26% against 16.5% observed).
- Measure Theo's 1 mm correction against stations, then choose. **Recommended.**
- Show ERA5 as it is.
- Subtract 1 mm like Theo, untested.

**A8. Cycling.** Stricter wind and gusts, and Fair at best on a day with 3 mm of rain or more; never better than Nice.
- Keep. **Recommended.**
- Change (say how).

### B. Configurable

**B1. What can a visitor change?** (any)
- Presets such as Nice, Cycling, Beach, Hiking.
- Their ideal temperature range.
- How much rain, sun and wind matter.
- Theo's builder: add any condition with a range.
- **Recommended:** presets, ideal temperature range, and how much rain, sun and wind matter.

**B2. How quickly must a custom map appear?**
- Instantly while dragging, on coarser data (Theo's way: a sample sent to the browser).
- Within a few seconds, at full detail, computed on Sites. **Recommended**, subject to the Sites probe now running.

### C. Data inputs

**C1. Clear sky.** ECMWF's free forecast has no direct-sun field, but both the forecast and ERA5 have total solar radiation.
- One sun measure for forecast and climate, from total solar radiation, checked against KNMI sunshine before switching. **Recommended.**
- Cloud cover everywhere (today).

**C2. Coastal temperature.**
- Nearest mostly-land grid cell plus height correction. **Recommended.** Coastal error 3.1 → 0.8 °C. Cost: Cape Town summer reads 2.7 °C hot; small islands stay approximate.
- Keep the nearest cell.

**C3. Gusts.** Both ECMWF and ERA5 publish them.
- Add gusts to Calm. **Recommended.**
- Leave gusts out.

### D. Periods and the map

**D1. What colours the map for a month, season or year?** Your words: "its obvious what the good places are", and bad places shouldn't look as good.
- Share of nice days (Theo): good places stand out.
- Average day: Awful, Poor and Fair look different.
- Render both and choose by eye. **Recommended.**

**D2. Numbers printed at cities on the map (nice days, or the day's grade).**
- Yes. **Recommended.**
- No.

**D3. Forecast length.** ECMWF gives 15 days.
- Show 15. **Recommended.**
- Show 14.

### E. UX direction (locked here, then agents iterate)

**E1. Layout.** Full-screen map; one bottom bar (time row, then taste and factor row); one place card; search; seasons and All year in the time row; on the phone one bottom sheet.
- Lock it and iterate. **Recommended.**
- Change (say how).

**E2. What the place card must show.** (any)
- The verdict and the count ("Perfect" or "5 nice days of 90").
- What spoils the day or period, in words.
- The days or months as a coloured strip.
- The four factors with real figures.
- Typical high and low temperatures.
- **Recommended:** the first four. You said the mock-ups may not show the right things; tick what must stay.

**E3. Map labels.**
- Cities only. **Recommended.**
- Cities and country names.

**E4. Light appearance.**
- Follows the device setting; Awful a faded mauve. **Recommended.**
- Follows the device setting; Awful the same dark wine as dark mode.
- Always dark.

**E5. Where each day's verdict appears in the 15-day view.**
- In the place card's strip. **Recommended.**
- On the day buttons in the bar, as today.

**E6. How you review each UI round.**
- Screenshots at phone, MacBook and 57-inch. **Recommended.**
- A live preview link.
- A single design-system page.
