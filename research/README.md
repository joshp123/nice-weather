# Data preparation and scoring

Written by AI on 2026.10.17 by model GPT-6.1 Sol.

The live product uses these tools:

- [`era5/`](era5/README.md) acquires hourly ERA5 data for the typical year.
- [`climate-v6/`](climate-v6/README.md) turns those hours into representative days, temperature offsets and physical facts.
- [`taste/nice_v6.py`](taste/nice_v6.py) defines the reference scoring curves used by the climate pipeline and [`../src/engine/default_scores.py`](../src/engine/default_scores.py). The browser implements the same rule in `src/engine/taste.ts`.
- [`taste/fable-method-report.md`](taste/fable-method-report.md) explains the choice of graded days. [`taste/fable-results.md`](taste/fable-results.md) supplies the anchor-place scores used by the climate validator.
- [`site/labels.py`](site/labels.py) builds the two map-label files from `places.tsv`.
- [`site/scoring-explainer-data.mjs`](site/scoring-explainer-data.mjs) reads the published climate during the Vite build, supplying coverage dates and chart examples for `/method`.
- [`taste/render_cards.py`](taste/render_cards.py) renders link previews with the app's share-card renderer.

`fine/` is active work on finer temperature detail. The climate acquisition and fine-detail workers maintain their own instructions in those directories.
