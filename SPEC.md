# How Nice Weather works

Written by AI on 2026.10.16 by model claude opus 5.5, for Josh to agree or correct. Draft 2: it takes in a cold review of draft 1 and measured input fixes. Josh's own words in `.agent/josh-steering-ledger.md` win over anything here.

## What the product answers

Where should I be, and when, to get nice days?

- **Now:** the forecast. ECMWF gives 15 days, so the product shows 15. Josh has always said 14; this is the one deliberate change of number.
- **Planning:** a month, a season (Winter is December to February, then Spring, Summer, Autumn) or the whole year.

Two tastes: **Nice** (general) and **Cycling** (stricter on wind and rain).

## What is orthogonal

**Weather factors.** Four independent physical things decide a day. They are the lenses already on the site.

| Factor | Physics | Spoils a day when |
|---|---|---|
| Comfort | air temperature through the day, and moisture (dew point) | too cold, too hot, too humid |
| Dry | rain | wet |
| Clear sky | direct sun | grey |
| Calm | wind and gusts | windy |

**Product choices.** Every screen is one choice of place, time (a forecast day, the 15 days, a month, a season, the year), taste (Nice or Cycling) and factor (Overall or one of the four). The number shown follows from the time: a day has a grade; a period has nice days and an average day.

## The calculation, in five steps

Each step reads only the one before. A change of taste never touches data; a change of design never touches taste.

```
Weather facts  ->  Day measures  ->  Day judgement  ->  Period summary  ->  Published files
```

### 1. Weather facts

- **Forecast:** ECMWF open data, 0.25°, every 3 hours. Built daily on Sites. Unchanged.
- **Climate:** ERA5, every hour of every day, 2006 to 2025 (7,305 real days per place). Theo keeps one sampled day per date; we keep them all.
- **Coastal temperature (measured, recommended):** take temperature from the nearest grid cell that is at least 80% land, then correct it to the ground height (6.5 °C per km). Against NOAA stations this cuts the error in monthly highs from 2.2 °C to 1.0 °C overall, and from 3.1 °C to 0.8 °C on the coast (Tenerife July: 21 °C before, 30 °C after, 29 °C observed). Theo's WorldClim correction and the finer ERA5-Land grid both did worse. Known limits: Cape Town now reads 2.7 °C hot in summer against an airport 17 km inland; small islands without a mostly-land cell stay approximate.
- **Honest resolution:** the weather is about 31 km; temperature detail follows the ground at about 10 km. We never claim more.

### 2. Day measures (physical, no opinion)

For each place and local day, 10:00 to 20:00:

| Measure | Definition |
|---|---|
| Hourly temperature | the ten daytime hours, °C |
| Afternoon high | warmest daytime hour, °C |
| Dew point | daytime mean, °C |
| Rain | daytime total, mm |
| Sun | share of the time the sun is up between 10:00 and 20:00 that has direct sun, from ERA5 direct radiation against a clear sky |
| Wind | daytime mean, km/h; strongest gust (forecast only) |

The sun measure is cloud, measured better. It is a share of the hours when the sun is up, so a short winter day is not marked down (the "Bright" mistake Josh rejected). Against KNMI's Rotterdam records it is closer than cloud cover (error 10.8 against 14.7 points), and it beats cloud cover at all eight places with station sunshine.

For the card we also count **wet days** the way stations do: 1 mm or more over the whole day. ERA5 still over-counts them in dry climates (Málaga January 26% against 16.5% observed) and matches in wet ones (Rotterdam 42% against 44%). We show it as is; we do not subtract 1 mm from every day as Theo does, because that makes London and Rotterdam too dry.

Day measures are stored for every place and day. Changing the taste means recomputing from them, never downloading 20 years again.

### 3. Day judgement (the taste)

Each factor scores 0 to 100 on a curve. All curves sit in one table, as data:

| Factor | Curve |
|---|---|
| Temperature | 8 °C = 0, 12 = 20, 16 = 45, 18 = 70, 20 = 88, 22 to 27 = 100, 30 = 85, 33 = 55, 36 = 20, 39 = 0 |
| Dew point | up to 16 °C = 100, 18 = 85, 21 = 55, 24 = 0 |
| Rain (daytime) | 0 mm = 100, 0.2 = 96, 1 = 80, 3 = 48, 6 = 18, 10 = 0 |
| Sun share | 0 to 25% = 25, 50% = 60, 75% = 90, 100% = 100 |
| Wind, Nice | up to 12 km/h = 100, 20 = 82, 30 = 48, 45 = 0 |
| Wind, Cycling | up to 14 km/h = 100, 20 = 78, 28 = 38, 35 = 10, 42 = 0 |

The temperature curve comes from the April plan Josh approved. His own words: "16, is not very good", "drop off quite hard on the colder side", Spain in November "like 22c - 27c". The sun curve is today's cloud curve applied to sun share, with a floor, so grey sky alone makes a day Poor, never Awful. The other curves are today's code.

**Comfort** is "a bit of both plus the comfortable time": the average of the afternoon high's score and the ten hours' average score, never better than the hottest hour allows above 27 °C, and never better than the dew point allows.

**The day's grade** is the weighted mix of the four factors (Comfort 45%, Dry 25%, Clear sky 20%, Calm 10%, as today), but it can be at most one grade better than its worst factor, and never better than Comfort. Bands: Awful under 25, Poor 25, Fair 40, Nice 55, Beautiful 70, Perfect 85. The card names the worst factor in plain words ("held back by wind: 32 km/h").

**Cycling** uses the Cycling wind curve, and a day with 3 mm of rain or more is Fair at best. A day is never better for Cycling than for Nice.

**What goes**, so nothing is hidden: the cold-damp blend, the rain and wind caps, the wet-time measure and the 10:00 to 20:00 average temperature. The one-grade gate and the Comfort ceiling replace them.

### 4. Period summary

For a place and a period:

- days counted, and the share in each of the six grades;
- **nice days**: Nice or better, as a count per typical period;
- **average day score**, 0 to 100;
- typical measures: high, warm hours, wet days, sun share, wind;
- the worst factor on the days that are not nice, month by month.

Seasons and the year are counted from days, not by averaging months.

### 5. Published files

- **Map colour** for a period is the average day score, on the same six colours as a single day. Mostly-Fair places look different from mostly-Awful ones. A single forecast day shows its grade.
- Layers are 0.1° images; places are compact binary records with one typed schema shared by Go and TypeScript.
- How numbers, labels and the card are laid out is the UX track, decided separately.

## Code boundaries

One module per step, each with a small interface and its complexity inside:

- `facts`: sources to hourly facts per place and local day. Hides GRIB, Zarr, time zones and the coastal and height corrections.
- `measures`: facts to `DayMeasures`. Pure.
- `judge`: `DayMeasures` and `Taste` to `DayJudgement` (score, grade, factor scores, worst factor). Pure. The only place taste lives, shared by forecast and climate. `Taste` is the curve table plus weights.
- `summarise`: judgements to `PeriodSummary`.
- `publish`: summaries to layers and place records.
- Browser: reads published files and draws. No weather logic.
- `niceweather scenarios`: one command that prints the table below, against stations, from stored measures. Taste is tuned here in minutes.

## Three tracks

1. **Just do.**
   - Done: the Safari fix (live).
   - The climate rebuild on Sites with the measured coastal and sun inputs, storing day measures. First a measured probe, because Sites has no published quota for this. The ERA5 sources are free; storage for every day at 0.25° is an estimated 15 to 25 GB. Report before the full run.
   - 0.1° layers.
   - Load speed.
2. **Agree first:** the decisions below.
3. **Deliberate:** the UX redesign. Mock-ups reviewed before anything is built. That includes Theo's numbers on the map and Ryan's headline sentence.

## Decisions for Josh

Effects are nice days, from the table below, live to proposed.

1. **The day rule.** Josh (July): "a weighted (vector?) sum"; (today) "computable the way the theo guy's stuff does it" and "some kinda curve scoring". Proposed: the weighted sum, gated at one grade above the worst factor (Ryan's method; Theo's transparency). Málaga November 53 → 68%, Tenerife January 82 → 84%, Rotterdam January 0 → 0%, Singapore 0%. The alternatives are in the table: plain weighted is generous (London July 92%); worst factor only is strict (Tenerife January 65%).
2. **Comfort counts the high and the comfortable time.** Josh: "count a bit of both plus like the comfortable time". Seville July 55 → 16%: the 36 °C afternoon now counts. Austin November has 5.2 warm hours against Málaga's 3.0, so Austin wins on Comfort; Austin is not yet in the corrected data set.
3. **Clear sky from direct sun, not cloud cover.** Josh: "clear sky good"; and against the winter-dark "Bright": "the metric is just dumb". Better measured, same meaning.
4. **Coastal temperature from the nearest mostly-land cell.** Josh: "we can fix by 1) better data granularity". Measured above.
5. **Period colour is the average day.** Josh: "some kinda curve scoring seems sensible". Recommended.
6. **Configurable taste.** Josh: "his is configurable remember". Taste is already data, so any taste can be computed. The open question is where: (a) presets only, recomputed on Sites when we change them (no extra cost); (b) custom sliders that recompute the map on Sites on request (how fast is unknown until the probe); (c) Theo's way, a one-day-per-date sample sent to the browser for instant sliders (about 10 to 30 MB, coarser). Recommended: (a) now, then (b) if the probe shows it is quick.

## Evidence

Nice days per month (%), on the corrected inputs, with three day rules and the proposed Comfort. Full tables: `/tmp/nwf-agents/round3/opus/draft2_rules.csv`, `draft2_variants.csv`; input measurements: `/tmp/nwf-agents/round3/data2/REPORT.md`.

| Place | Rule | Jan | Apr | Jul | Nov | Winter days | Year days |
|---|---|---|---|---|---|---|---|
| Málaga | live today | 7 | 70 | 96 | 53 | 11 | |
| | weighted | 37 | 89 | 74 | 82 | 37 | 273 |
| | **gated (proposed)** | **31** | **79** | **74** | **68** | **32** | **249** |
| | worst factor | 29 | 71 | 74 | 63 | 30 | 235 |
| Tenerife | live today | 82 | 89 | 100 | 91 | 71 | |
| | weighted | 97 | 97 | 77 | 97 | 87 | 336 |
| | **gated** | **84** | **92** | **77** | **85** | **77** | **314** |
| | worst factor | 65 | 81 | 76 | 73 | 63 | 283 |
| Seville | live today | 12 | 83 | 55 | 68 | 23 | |
| | **gated** | **22** | **74** | **16** | **60** | **29** | **175** |
| Marrakech | live today | 52 | 91 | 46 | 86 | 55 | |
| | **gated** | **60** | **81** | **14** | **83** | **61** | **224** |
| Chania | live today | 4 | 36 | 88 | 57 | 7 | |
| | **gated** | **20** | **78** | **86** | **60** | **23** | **234** |
| Lisbon | live today | 2 | 48 | 100 | 40 | 5 | |
| | **gated** | **4** | **61** | **86** | **34** | **10** | **204** |
| Cape Town | live today | 97 | 87 | 16 | 88 | 88 | |
| | **gated** | **55** | **76** | **48** | **73** | **55** | **232** |
| London | live today | 0 | 13 | 81 | 0 | 0 | |
| | weighted | 0 | 16 | 92 | 0 | 0 | 124 |
| | **gated** | **0** | **14** | **59** | **0** | **0** | **80** |
| Rotterdam | live today | 0 | 19 | 77 | 0 | 0 | |
| | **gated** | **0** | **19** | **64** | **0** | **0** | **92** |
| Denver | live today | 0 | 41 | 89 | 13 | 1 | |
| | **gated** | **1** | **48** | **53** | **25** | **4** | **154** |
| Bangkok | live today | 57 | 2 | 0 | 25 | 43 | |
| | **gated** | **47** | **0** | **0** | **19** | **35** | **43** |
| Singapore | live today | 2 | 0 | 0 | 0 | 1 | |
| | **gated** | **2** | **0** | **0** | **0** | **1** | **1** |

Winter days and year days are nice days in a typical December to February and year. Cape Town's winter is its summer; its corrected temperature runs hot (see Weather facts). No independent count of nice days exists to check these against; the inputs (highs, sun, wet days) are checked against stations, the taste is Josh's call.
