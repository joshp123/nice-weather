# Nice Weather: what we agree before building

Written by AI on 2026.10.16 by model claude opus 5.5, for Josh. This is the one place where product decisions are asked and recorded. Josh's own words are in `.agent/josh-steering-ledger.md` and win over anything here.

**How to answer:** reply in chat with what you'd change, by section number. Anything you don't mention, I take as agreed. You judge outcomes (sections 1 to 3); section 4 is what I've decided myself, for you to veto.

## 1. The promise

What the product tells you. Correct any sentence that is wrong or missing something.

1. Open it, and the map shows where the next 15 days will be nice, by your taste. Each city shows how many of the 15 days are nice.
2. Pick a month, a season or the whole year, and the map shows where most days are nice, from 20 years of real weather. Winter is December to February.
3. Tap or search a place, and it tells you how many nice days to expect, what spoils the others, and the real numbers behind that: how warm it feels, rain, sun and wind.
4. "Nice" is your taste by default. Anyone can switch to Cycling (wind and rain count for more), or set their own: how warm they like it, and how much rain, cloud and wind bother them.
5. It is honest: it shows how fresh the forecast is, a stale forecast is obvious, and its numbers agree with weather stations.

## 2. Your taste

### 2a. What I've understood from you

Correct anything wrong. The scoring will be fitted to these and to your grades in 2b, not to my guesses.

| | Your taste | Your words |
|---|---|---|
| a | How warm it **feels** outside matters most: temperature together with sun, wind and humidity | "feels like temp is key" |
| b | Warmer is better: 18 °C is not that good, about 23 to 27 °C is perfect, past about 30 °C it gets too hot | "18 is not that good? 23 - 27 ish is kinda perfect?" |
| c | Humid heat is awful, and nothing like a Dutch summer | "singapore all year round is imo not nice" |
| d | Rain spoils a day | |
| e | Cloud matters, but much less than warmth: warm and grey beats cold and sunny | "i'd rather warm and overcast than cold and sunny? (mostly?)" |
| f | Wind matters a little for Nice, a lot for Cycling | |
| g | Awful is different from Poor; Britain and the Netherlands are awful at least half the time from late October to March | "britain/nl/norhtern europe anywhere from late october to march is usually awful ... at least50% of the time" |

### 2b. Grade twelve real days

Each is a real day from the 2006 to 2025 record, corrected for height and coast. Temperatures are in the shade, as any forecast gives them, at 10:00 and at the warmest daytime hour. Humidity words follow WeatherSpark's dew-point bands (dry up to 13 °C, comfortable to 16, humid to 18, muggy to 21, oppressive to 24, miserable above). Sun is the share of the 10:00 to 20:00 daylight with direct sunshine. Rain is 10:00 to 20:00. I have not shown what my scoring says, so the grades are yours.

Grade each Awful, Poor, Fair, Nice, Beautiful or Perfect.

| # | Place and date | 10:00 | High | Humidity (dew point) | Sun | Rain | Wind | Your grade |
|---|---|---|---|---|---|---|---|---|
| 1 | Málaga, 3 Nov 2025 | 20 °C | 23 °C | comfortable (14 °C) | 82% | dry | 12 km/h | |
| 2 | Málaga, 25 Nov 2019 | 15 °C | 20 °C | dry (12 °C) | 18% | dry | 9 km/h | |
| 3 | Málaga, 16 Jan 2020 | 11 °C | 17 °C | dry (7 °C) | 90% | dry | 5 km/h | |
| 4 | Seville, 11 Jul 2024 | 28 °C | 37 °C | muggy (19 °C) | 96% | dry | 8 km/h | |
| 5 | Santa Cruz de Tenerife, 14 Jan 2025 | 19 °C | 22 °C | comfortable (13 °C) | 54% | dry | 22 km/h | |
| 6 | Marrakech, 21 Jan 2021 | 10 °C | 20 °C | dry (10 °C) | 96% | dry | 7 km/h | |
| 7 | London, 21 Jul 2022 | 20 °C | 23 °C | comfortable (14 °C) | 20% | dry | 11 km/h | |
| 8 | Rotterdam, 8 Jan 2022 | 3 °C | 6 °C | dry (3 °C) | 0% | 5.9 mm | 28 km/h | |
| 9 | Singapore, 13 Jul 2024 | 29 °C | 31 °C | miserable (24 °C) | 53% | dry | 20 km/h | |
| 10 | Chania, 8 Apr 2022 | 17 °C | 19 °C | dry (11 °C) | 88% | dry | 28 km/h | |
| 11 | Lisbon, 5 Nov 2020 | 16 °C | 18 °C | comfortable (13 °C) | 31% | 2.4 mm | 19 km/h | |
| 12 | Cape Town, 20 Jan 2025 | 24 °C | 27 °C | comfortable (13 °C) | 95% | dry | 32 km/h | |

If you only have a minute: grade 1, 2, 3, 5, 7 and 10. They decide how warmth, cloud and wind trade against each other.

### 2c. Where would you spend December to February?

A typical December to February at each test place. "Days 20 °C+" is the share of days whose high reaches 20 °C. Wet days count 1 mm or more over the whole day; ERA5 over-counts these in dry places by up to a third, a fix I'm testing (section 4). Cape Town's December to February is its summer.

| Place | 10:00 | High | Days 20 °C+ | Humidity (dew point) | Sun | Wet days a month | Wind |
|---|---|---|---|---|---|---|---|
| Bangkok | 28 °C | 32 °C | 100% | muggy (21 °C) | 73% | 3 | 9 km/h |
| Cape Town | 25 °C | 30 °C | 100% | comfortable (15 °C) | 83% | 3 | 24 km/h |
| Chania | 15 °C | 16 °C | 5% | dry (8 °C) | 44% | 14 | 19 km/h |
| Denver | 2 °C | 6 °C | 2% | dry (−8 °C) | 71% | 6 | 8 km/h |
| Lisbon | 12 °C | 15 °C | 2% | dry (9 °C) | 53% | 9 | 17 km/h |
| London | 6 °C | 8 °C | 0% | dry (4 °C) | 33% | 11 | 17 km/h |
| Málaga | 12 °C | 17 °C | 11% | dry (7 °C) | 60% | 7 | 10 km/h |
| Marrakech | 12 °C | 20 °C | 47% | dry (5 °C) | 77% | 5 | 8 km/h |
| Rotterdam | 4 °C | 7 °C | 0% | dry (3 °C) | 28% | 13 | 19 km/h |
| Santa Cruz de Tenerife | 19 °C | 21 °C | 69% | comfortable (13 °C) | 64% | 6 | 20 km/h |
| Seville | 10 °C | 17 °C | 13% | dry (7 °C) | 58% | 6 | 10 km/h |
| Singapore | 27 °C | 30 °C | 100% | oppressive (23 °C) | 52% | 19 | 16 km/h |

Put them in your order, or mark each yes, maybe or no. Also name places you'd actually consider that are missing (south Tenerife, the Algarve, Cyprus, somewhere in Mexico?). I'll add them to the test set.

## 3. The design direction

The mock-ups in `/tmp/nwf-agents/round3/designer/renders/` (sent in chat earlier) propose one structure:
- a full-screen map;
- one bottom bar: time (Next 15 days or Typical year, with All year, the four seasons and the months), then taste and factor;
- one place card, with search above it;
- on the phone, one bottom sheet.

Your verdict on them: too noisy, too many font weights and types, unbalanced, maybe showing the wrong things. That goes to the design agents as their brief. Each round comes back to you as screenshots at phone, MacBook and 57-inch.

**Decide:** lock this structure and iterate on what goes in it and how it is weighted, or start the structure again? I recommend locking it. The card then shows exactly promise sentence 3, and nothing else.

## 4. What I've decided myself (veto any)

| Decision | Why |
|---|---|
| Comfort is the felt temperature, in sun and in shade, fitted to your grades in 2b | Your "feels like temp is key"; also explains Spain in November feeling 22 to 27 °C on sunny days |
| Coastal temperature from the nearest mostly-land grid cell, corrected for height | Coastal error 3.1 °C to 0.8 °C against stations. Known limits: Cape Town summer reads 2.7 °C hot against an inland airport; small islands stay approximate |
| One sun measure for forecast and climate, from solar radiation | Both sources have it; ECMWF's free forecast has no direct-sun field. Checked against KNMI sunshine before use |
| Gusts added to Calm | Both sources publish them |
| Rain: test Theo's 1 mm correction against stations, keep whichever matches | ERA5 says Málaga January is wet on 26% of days; stations say 16.5% |
| 15 forecast days | ECMWF gives 15 |
| Map colour for a month, season or year: render both options (share of nice days; how good the average day is) in the first design round | You'll choose by eye |
| The climate pipeline runs on Sites | It works, measured: one area in 27 seconds. The full 20-year world run would take weeks one at a time, so I'll bring a faster plan before running it |
| Code: five steps (facts, day measures, judgement, period summary, published files); taste is data; the browser only draws | Changing taste never touches data or design |
| Switch the forecast to ECMWF's 9 km data when it becomes public | Finer detail at no cost |
