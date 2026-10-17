Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Typical-year score comparison

The five-chunk baseline is preserved in `score-baseline-five-chunks.json`, including
the original published `meta.json`, the scores, the selected fine coordinates and
SHA-256 hashes of the inputs and scoring source. Chunks are 76, 78, 80, 82 and 84.
These are five 364-day spans. The metadata's calendar-year labels, 2015–2024,
include both years of each span; they do not mean ten full years of observations.

Underlying days per month, January through December:
`155, 143, 155, 150, 155, 150, 155, 155, 122, 155, 150, 155`.
Each published cell still contains eight representative real days per month.

Scores below are out of 100 for the default taste, before display rounding. The
comparison reads the published month records and offsets, uses the same scoring
functions as `src/engine/default_scores.py`, and selects the nearest fine land
point exactly as the place card does. It checks all 72 rounded results against
both `scores.bin` and the monthly score images. It does not apply the map's final
visual smoothing. Broader climate coverage can change a score in either direction;
one comparison alone does not establish statistical convergence.

| Place | Requested latitude, longitude | Selected land latitude, longitude |
| --- | --- | --- |
| Lisbon | 38.722, −9.139 | 38.75, −9.25 |
| Málaga | 36.721, −4.421 | 36.75, −4.50 |
| Rotterdam | 51.923, 4.479 | 52.00, 4.50 |
| Tenerife south coast | 28.12, −16.73 | 28.00, −16.75 |
| San Diego | 32.716, −117.162 | 32.75, −117.00 |
| Cape Town | −33.925, 18.424 | −33.75, 18.50 |

| Five-chunk baseline | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Lisbon | 25.1 | 32.4 | 25.2 | 39.1 | 62.4 | 77.4 | 89.2 | 88.1 | 87.5 | 71.6 | 46.2 | 29.7 |
| Málaga | 43.2 | 54.4 | 46.4 | 68.9 | 85.3 | 89.7 | 70.0 | 65.6 | 81.4 | 86.1 | 63.9 | 48.7 |
| Rotterdam | 0.1 | 0.1 | 1.9 | 15.3 | 40.0 | 49.3 | 63.7 | 65.9 | 49.8 | 12.4 | 1.4 | 0.5 |
| Tenerife south coast | 79.5 | 73.8 | 73.4 | 81.7 | 87.0 | 89.1 | 82.1 | 77.5 | 79.7 | 80.3 | 84.0 | 83.4 |
| San Diego | 72.9 | 73.2 | 68.1 | 77.9 | 84.7 | 93.8 | 90.0 | 88.4 | 87.0 | 93.0 | 86.3 | 68.6 |
| Cape Town | 81.5 | 84.2 | 87.9 | 83.7 | 77.0 | 58.2 | 51.1 | 47.9 | 61.8 | 76.6 | 82.4 | 87.3 |

After each later assembly, first regenerate its default scores and images, then
run this from the repository in the pipeline's Nix Python environment:

```sh
python3 research/climate-v6/compare_scores.py artifacts/v6 \
  --baseline research/climate-v6/score-baseline-five-chunks.json \
  --output research/climate-v6/score-latest.json
```

The command saves the latest evidence and prints a Markdown table of baseline,
latest and change for every month and place. Use an immutable assembly's `v6/`
directory instead of `artifacts/v6` if another assembly may run during capture.
The command does not modify the supplied data directory. Keep the original
baseline file unchanged.
