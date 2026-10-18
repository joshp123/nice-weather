Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Cards, map-label counts and sharing now count actual daily scores. They do not round an interpolated proportion of land into a number of days. Spatial interpolation happens to each day's score before its threshold is counted. Typical-day scores, curves and bands are unchanged.

The next forecast contains the same 14 explicitly dated days in its total and calendar. The first stored day was previously hidden after midnight, even though the aggregate still included it. With the current run, Chania has 13 nice, 8 perfect and 1 awful day. Santa Cruz de Tenerife has 14 nice, 5 perfect and no awful days.

Typical-year counts describe the daily data actually available to the app: 8 sampled days per month, 40 for November to March, 96 for the year. The interface names that denominator as sampled days. It no longer presents a rounded extrapolation as a count of 151 or 365 observed days. The calendar-length weighted typical-day score is retained. Collecting 20 to 25 complete years is outside this change, as Josh instructed.

`check.mjs` loads the prepared published-format climate and forecast tables through the production shared loader. It checks 30 places and four stretches (120 cases), including every forecast day separately, and proves all whole counts match their daily scores. It also compares every score and band to the preceding committed implementation and checks an explicit spatial case where averaging area counts would give the wrong answer. The complete daily values are in `results.json`.

Run from the repository:

```sh
devenv shell -- node research/site/cold-review-counts/check.mjs '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data' /tmp/nwf-counts-results.json
```

The preceding-version comparison uses the parent of the whole-day-count commit (`a4c8a54^:hosting/sites/named-place.mjs`), so it remains reproducible after later commits. No data files, scoring curves or bands were changed.
