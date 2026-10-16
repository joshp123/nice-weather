# How Nice Weather works

Written by AI on 2026.10.16 by model claude opus 5.5, for Josh to agree or correct. Draft 1. Josh's own words in `.agent/josh-steering-ledger.md` win over anything here.

## What the product answers

Where should I be, and when, to get nice days? Two horizons, one meaning of "nice":

- **Now:** the next 15 days. Where do I go this fortnight?
- **Planning:** a month, a season (Winter is December to February) or the whole year. Where do I spend the winter?

Two tastes: **Nice** (general) and **Cycling** (stricter on wind and rain).

## The orthogonal model

Every screen is one choice on each of five independent axes. Nothing else exists.

| Axis | Choices |
|---|---|
| Where | any land point; the map shows all of them |
| When | one forecast day; the next 15 days; a month; a season; the year |
| Taste | Nice; Cycling (custom later, see decision 6) |
| Factor | Overall; or one of Comfort, Dry, Clear sky, Calm |
| Statistic | a day's grade; or, for a period, how good the average day is and how many days are nice |

The calculation runs in five layers. Each layer only reads the one before it, so a change of taste never touches data, and a change of design never touches taste.

```
Weather facts  ->  Day measures  ->  Day judgement  ->  Period summary  ->  Published layers and places
(sources,          (physical, no     (taste: curves,     (counts and          (what the browser draws;
 corrections)       opinion)          weakest factor)     averages)            no weather logic in it)
```

## 1. Weather facts

- **Forecast:** ECMWF open data, 0.25°, every 3 hours, 15 days. Built daily at 08:00 UTC on Sites. Unchanged.
- **Climate:** ERA5, every hour of every day 2006 to 2025 (7,305 real days per place). Theo samples one day per date; we keep them all.
- **Corrections (from Theo, applied once, before anything is judged):**
  - temperature moved to the real ground height on a 0.1° grid (6.5 °C per km);
  - a coastal correction, because coastal cells read too cold (Tenerife 21 °C in July against 29 °C observed). The method is being measured now.
- **Honest resolution:** the weather itself is about 31 km; temperature detail is about 10 km. We never claim more.

## 2. Day measures (physical, no opinion)

For each place and local day, between 10:00 and 20:00:

| Measure | Definition |
|---|---|
| Hourly temperature | the ten daytime hours, °C |
| Afternoon high | the warmest daytime hour, °C |
| Dew point | daytime mean, °C |
| Rain | daytime total, mm; a wet day is 1 mm or more |
| Sun | share of daytime hours with direct sun (proposed; today: cloud cover %) |
| Wind | daytime mean, km/h; strongest gust |

These are stored for every day. Changing the taste later means recomputing from them, not downloading 20 years again.

## 3. Day judgement (the taste)

Four factors, as today. Each is scored 0 to 100 on a curve. Josh's curves are data in one file, not code.

| Factor | Scored from | Notes |
|---|---|---|
| Comfort | every daytime hour on the temperature curve, averaged ("comfortable time"), capped by the hottest hour; and dew point | Austin in November (5.2 hours at 20 °C or more) beats Málaga (3.0) although their highs differ by 2 °C. The cap stops a pleasant morning hiding a 36 °C afternoon. |
| Dry | daytime rain and wet hours | |
| Clear sky | sun share (proposed) | Grey sky alone can make a day Poor, never Awful. |
| Calm | wind and gusts | Cycling uses stricter curves. |

Temperature curve (Josh, April): 16 °C = 45 (mediocre), 18 °C = 70, 20 °C = 88, 22 to 27 °C = 100, 30 °C = 85, 33 °C = 55, 36 °C = 20.

**The day is as good as its weakest factor.** Its grade is the band of that score: Awful under 25, Poor 25, Fair 40, Nice 55, Beautiful 70, Perfect 85. The reason shown is that factor in plain words ("too cold: 14 °C high", "wet: 6 mm"). Theo and Ryan Hall both decide this way; today's weighted sum lets sun hide rain.

Cycling: the same rule with stricter Dry and Calm curves, so it can never rate a day better than Nice.

## 4. Period summary

For a place and a period (15 days, month, season, year):

- days counted, and the share in each of the six grades;
- **nice days**: Nice or better, as a count ("19 nice days in a typical January");
- **average day score**, 0 to 100;
- typical measures for the card: high, warm hours, wet days, sun, wind;
- what spoils the other days, month by month ("too cold Dec to Mar").

Seasons and the year are built from days, not by averaging month percentages.

## 5. Published layers and places

- **Map colour for any period** = the average day score on the same six-band colours as a single day. A place of mostly Fair days looks different from a place of mostly Awful days. For a single forecast day the colour is that day's grade.
- **Numbers on the map** = nice days in the period, printed at towns (Theo's best idea).
- **Place card** = grade, nice days, what spoils the rest, the four factors with real figures, and the year at a glance.
- Layers: 0.1° images; places: compact binary records with one typed schema shared by Go and TypeScript.

## Code boundaries

One module per layer, each with a small interface and the complexity hidden inside:

- `facts`: sources (ECMWF GRIB, ERA5) to hourly facts per place and local day. Hides formats, time zones and corrections.
- `measures`: facts to `DayMeasures`. Pure.
- `judge`: `DayMeasures` and `Taste` to `DayJudgement` (score, grade, factor scores, limiting factor). Pure. The only place taste lives; the same code serves forecast and climate.
- `summarise`: judgements to `PeriodSummary`.
- `publish`: summaries to layers and place records.
- Browser: reads published files and draws. No scoring.
- `niceweather scenarios`: one command that prints the nice-day table for test places against weather stations. This is how taste is tuned in minutes, not by redeploying.

## Three tracks

1. **Just do**, no sign-off needed. Pipeline and speed:
   - the Safari fix (live today);
   - the climate rebuild on Sites storing day measures;
   - the height and coastal temperature corrections;
   - 0.1° layers;
   - load speed.
2. **Agree first:** the decisions below. Then build `judge` and `summarise` to them.
3. **Deliberate:** the UX redesign. A design lead is drafting a direction; you review mock-ups before any of it is built.

## Decisions for Josh

1. **Weakest factor decides the day.** Replaces today's weighted sum. Recommended.
2. **Comfort counts comfortable time, capped by the hottest hour.** Replaces the 10:00 to 20:00 average temperature. Recommended.
3. **Clear sky measured by direct sun.** ERA5 cloud counts thin high cloud, which fails a third of Málaga's winter days. Same meaning ("clear sky"), better measurement. Recommended if the measurement now running confirms it.
4. **A wet day is 1 mm or more of daytime rain.** No blanket subtraction: it would make London and Rotterdam too dry. Recommended.
5. **Period colour is the average day; the number is nice days.** Recommended.
6. **Configurable taste.** Presets now (Nice, Cycling). The design keeps custom curves possible later without a rewrite, because day measures are stored and taste is data. Recommended to defer the sliders.

## Evidence so far

Share of nice days, live site against the proposal (decisions 1, 2 and 4, with the height correction; cloud and coastal fixes not yet applied, so coastal places and grey-sky months still read low). Full table: `/tmp/nwf-agents/round3/opus/daily_candidate_both.csv`.

| Place | Jan | Apr | Jul | Nov |
|---|---|---|---|---|
| Málaga | 7 → 16 | 70 → 49 | 96 → 72 | 53 → 49 |
| Seville | 12 → 5 | 83 → 44 | 55 → 14 | 68 → 40 |
| Marrakech | 52 → 37 | 91 → 58 | 46 → 13 | 86 → 60 |
| Austin | – → 24 | – → 38 | – → 1 | – → 40 |
| Rotterdam | 0 → 0 | 19 → 9 | 77 → 30 | 0 → 0 |
| Singapore | 2 → 0 | 0 → 0 | 0 → 0 | 0 → 0 |

Station checks: after the height correction, Málaga and Seville daily highs match NOAA within about 1 °C; ERA5 counts Málaga January as wet on 26% of days against 16.5% observed.
