# Bay Area microclimates

Written by AI on 2026.10.17 by model GPT-6 (Codex).

**2016–2025.** Nice = supplied score ≥55, 10:00–21:00 local. “Station” means ERA5 temperatures rescaled to observed daily highs/lows; other weather remains ERA5. Counts rounded to days/year.

| Place | NOAA station; coverage | July high °C: station / ERA5 | Nice days/year: ERA5 / station / PRISM | PRISM error vs station | Largest monthly spread: ERA5 → station (PRISM) |
|---|---|---:|---:|---:|---|
| Half Moon Bay | USC00043714; 99% | 17.3 / 23.0 | 138 / 36 / 38 | +2 days | Jul 26.5 → 4.4 (5.9); Aug 30.1 → 14.3 (9.6) |
| San Francisco Downtown | USW00023272; 98% | 18.8 / 23.5 | 102 / 66 / 73 | +8 days | Jul 16.2 → 5.5 (7.1); Aug 23.3 → 12.7 (13.5) |
| SFO | USW00023234; 99% | 22.0 / 22.9 | 138 / 118 / 102 | -16 days | Jul 26.4 → 22.2 (15.7); Aug 30.1 → 26.6 (24.5) |
| Oakland Airport | USW00023230; 100% | 22.3 / 23.8 | 116 / 103 / 87 | -17 days | Jul 19.6 → 17.3 (9.9); Aug 25.8 → 24.7 (18.4) |
| Berkeley | USC00040693; 76% | 22.7 / 22.9 | 91 / 83 / 91 | +7 days | Aug 22.4 → 19.2 (21.6); Jul 13.0 → 10.2 (12.7) |
| Palo Alto area (Redwood City) | USC00047339; 100% | 27.0 / 23.0 | 138 / 130 / 137 | +7 days | Oct 19.1 → 12.1 (17.0); Sep 27.1 → 24.0 (26.5) |
| San Jose | USW00023293; 100% | 27.7 / 27.1 | 155 / 153 / 146 | -8 days | Oct 20.3 → 18.0 (16.3); Nov 4.8 → 4.4 (3.0) |
| Livermore Airport | USW00023285; 100% | 32.0 / 30.2 | 154 / 136 / 152 | +16 days | Jun 24.3 → 21.0 (24.0); May 14.0 → 11.0 (12.4) |

**All 12 months:** [monthly counts and monthly mean highs/lows](monthly.csv). PRISM column = a fixed monthly temperature offset from [800 m normals](https://prism.oregonstate.edu/normals/), using a matched 1991–2020 ERA5 baseline; [method, sources and reproduction](METHODS.md).

ERA5 misses the coastal gradient badly: Half Moon Bay falls from 138 to 36 nice days/year with observed temperature ranges, and downtown San Francisco from 102 to 66. PRISM cuts mean absolute annual error from 26 to 10 days, but makes Oakland worse and misses its July benchmark by 7.4 days, so use it for better temperature detail before claiming reliable microclimate nice-day totals. Separate monthly high/low offsets were also tested and leave errors of −12 to +16 days/year, so that refinement does not settle the question. Outside the US, I would test [WorldClim 2.1 at ~1 km](https://www.worldclim.org/data/worldclim21.html), using its 1970–2000 reference period consistently; it was not evaluated here.

**Could not measure:** Actual nice-day truth, local fog/sunshine, wind shelter and hourly feels-like temperatures: all methods retain ERA5 dew point, wind, sun and rain, and fog also removes sun. Berkeley has only 76% paired coverage; Palo Alto’s station only 18%, so Redwood City (~9 km northwest) is explicitly substituted; these are incomplete measurements of those two places.
