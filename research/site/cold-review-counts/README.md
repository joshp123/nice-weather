Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Typical-year counts now represent the real days of the chosen stretch, as Josh requested after the cold-review fixes. Each daily score represents its calendar month's length divided by the number of sampled days in that month. We add the qualifying days' weights and round once to whole days. November to March has 151 days, the whole year 365. Thresholds, typical-day scores and bands are unchanged.

Forecast days still count exactly once, after spatial interpolation. Chania has 13 nice, 8 perfect and 1 awful day out of the same 14 dates listed in its card. Santa Cruz de Tenerife has 14 nice, 5 perfect and no awful days.

The shared count function supplies browser labels, ranking, cards, home comparisons, posters and Worker previews. The interface no longer describes counts as sampled days. The method page explains how the calendar-day estimates are made.

`check.mjs` checks the actual daily tables for 30 places and four stretches (120 comparisons), using independent calendar weights. It also proves scores and bands unchanged against the earlier implementation and checks interpolation before forecast counting. Results are in `results.json`.

Run from the repository:

```sh
devenv shell -- node research/site/cold-review-counts/check.mjs '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data' /tmp/nwf-counts-results.json
```

November-to-March screenshots at 1512 × 982 and 390 × 844, plus list, card, poster and preview checks: `/tmp/nwf-agents/round5/calendar-days/`. Both browser checks report no page errors; Lisbon's card and preview agree on 34 nice and 4 perfect days out of 151. Agadir's home comparison reports 136 nice days out of 151. No scoring curves, bands or data files changed.
