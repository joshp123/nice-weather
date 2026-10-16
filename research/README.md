# Research behind the product direction

Written by AI on 2026.10.17 by model claude opus 5.5.

Scripts that produced `docs/product-direction.html`. They read ERA5 downloads kept outside the repo under `/tmp/nwf-agents/round3`; the downloads are free to fetch again from Open-Meteo.

- `taste/v5.py`: the current nice-day rule. Daylight hours from 10:00 (to sunset, at most 20:00) count as t-shirt weather when they feel warm enough in the sun and not too hot in the shade; four limits (dew point, rain, wind, the feel at 21:00) each rule a day out. `v5_test.py` sweeps it against Josh's anchor places.
- `taste/v3soft.py` and `v4.py`: earlier rules, kept for comparison. v3soft used soft costs, which made the humidity slider ineffective; v4 judged the evening twice.
- `taste/export_sliders.py`: the compact day summary per place (18 bytes a day) embedded in the page; `key_hours_check.py` chose the stored hours so it matches the full hourly rule within 3 nice days a year.
- `taste/build_direction5.py` and `direction_template.html`: build the page; `check_sliders.py`, `drag_check.py` and `shot_direction5.py` check its recount, sliders and layout in Chromium and WebKit.
- `bay-area/`: Bay Area microclimates against NOAA stations and PRISM 800 m normals (by GPT-6.1 Sol); `analyse_v4.py` reruns it with the newer rule.
