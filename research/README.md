# Research behind the product direction

Written by AI on 2026.10.17 by model claude opus 5.5.

Scripts that produced `docs/product-direction.html`. They read ERA5 downloads kept outside the repo under `/tmp/nwf-agents/round3`; the downloads are free to fetch again from Open-Meteo.

- `taste/v3soft.py`: the candidate nice-day rule (t-shirt hours from 10:00 to 21:00, rain, wind and humidity costs, the evening ceiling).
- `taste/export_sliders.py`: the compact day summary per place, embedded in the page; `compact_check.py` compares it with the full hourly rule.
- `taste/build_direction2.py` and `direction_template.html`: build the page; `check_sliders.py` and `drag_check.py` check its live recount and sliders in Chromium and WebKit.
- `bay-area/`: Bay Area microclimates against NOAA stations and PRISM 800 m normals (by GPT-6.1 Sol).
