Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Fine temperature: Phase 2 delivery

Historical build log from before the Step A rejection and B1 implementation.
The current decision is in [B1_RESULT.md](B1_RESULT.md): do not publish these
candidates. Statements below about unchanged application code describe that
earlier build. Global fine tiles remain incomplete; no global carrier exists.

All data is isolated under `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data`.
Nothing was pushed, deployed or written into `artifacts/v6`. The running home
download job and the application were left alone.

## Step A

`v6-fine-step-a` contains the current-format generation for chunks 76, 78, 80,
82 and 84. These score 1,800 actual days, discontinuously from 2015-09-30 to
2024-09-13. The chunk list, rather than a nominal year range, determines every
period adjustment. Adding chunks requires rebuilding the corrections across
the entire new set. [HOME_JOB_HANDOFF.md](HOME_JOB_HANDOFF.md) gives the owner
the static-file change, NAS replay call and assembly instructions.

All 250,011 fine points now use their own ERA5 temperature and dew point,
with zero sea-level shift. All 12,545 local tile intermediates were rebuilt
from retained raw files. Native replay took 405 seconds; native temperature
normals took 110 seconds; correction, assembly and score/image generation
took 439 seconds. Downloads and validation are additional.

The unchanged formats have measurable limits. Of 3,000,132 point-months,
3,594 felt offsets exceed the available signed-tenths encoding after rounding
(0.12%); the largest lost correction is 15.11 degrees. Existing cold-region
fact encoding also clips. The unchanged place lookup can select a point far
from the requested place: 26 km from San Francisco, and an inland mountain
point for Funchal. **Do not publish this generation alone as the completed
coastal accuracy fix.** The shared fine-pixel consumer is needed to remove
that geographic error. There are no city-specific adjustments.

The complete private station comparison is `fine-validation/step-a`:
`STEP_A_COMPARISON.md`, `MONTHLY_STEP_A.md`, `monthly_step_a.csv` and
`station_summary.json`. It covers all thirteen requested places and twelve
months, with instrument distances, observation coverage and exact selected
grid points. Actual current and Step A facts/scores were decoded, checked
against the live current API and independently replayed from raw weather.
The largest fact replay difference was 0.197 degrees, within the existing
one-third-degree encoding. All 69 publication-file hashes were verified. The recorded default-score
generator and taste implementation reproduce all eighteen score files and
images byte-for-byte. These comparisons use the mean of graded daily scores
from that generation; a separate interface change to score aggregation is not
part of this data validation.

Station daily TMAX and the product's 10:00–20:00 high have different windows;
the report also includes date-paired hourly daytime highs. A complete observed
comfort score is unavailable without validated station radiation and rain.
The separately labelled temperature-substitution score diagnostic retains
native gridded other weather and must not be presented as a station score.
Station-derived validation remains outside publication roots and git.

## Step B

The fine correction build and delivered-file audit are being completed.
The binary and consumer design is in [consumer_design.md](consumer_design.md);
[README.md](README.md) contains the reproduction commands. Final artifact
counts, sizes and build timing will be recorded after the complete-file audit.

The completed native reference has 528,624 own-source points covering all
3,781 required native tiles, including islands and polar land. Independent
raw-weather recomputation for San Francisco, Funchal, Funafuti and the South
Pole agrees with its monthly high/low/mean normals within 0.000002 degrees.
No inland borrowing or sea-level adjustment is used.

The actual San Francisco geographic benchmark downloads the generated NWFT
files, parses their headers, replays distinct native weather histories and
renders the resulting score textures through headless Chrome/Metal on this
Mac. The 390×844 view needs two fine tiles and 104 native histories; the
3840×1080 view needs three land tiles and 176 histories. All twelve months
are included in the measured transfers:

| View | Compressed input | Decoded input | Warm month | Warm year | Cached draw |
|---|---:|---:|---:|---:|---:|
| 390×844 | 1,667,521 bytes | 8,798,370 bytes | 9.1 ms | 22.4 ms | 4.6 ms |
| 3840×1080 | 3,490,379 bytes | 13,612,347 bytes | 4.3 ms | 31.1 ms | 4.75 ms |

Initial data readiness, including local fetch, gzip decoding, compilation and
completed upload, was 365.0/203.6 ms. The first case paid a 74.4 ms shader
compile; the warm second case paid 4.9 ms. These are desktop measurements at
phone-sized/wide viewports, not phone hardware, internet latency or full
MapLibre timings. CPU/card and GPU scores differ by at most 0.000034 points.
Both grayscale diagnostic renders were inspected; native quarter-degree
weather seams remain visible. No source application files were changed.

The design requires a shared containing-pixel decoder and generation for map
and card, a 60-byte sampled-day weather carrier with uncapped dew point, and
separate full-period card facts in signed tenths. Target local-clock zones
must also agree. The global carrier and full-period card-fact tiles are
scheduled consumer work, not deliverables claimed by this correction build.
