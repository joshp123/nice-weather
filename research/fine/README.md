Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Fine temperature research

Josh approved Phase 2 on 2026-10-17: PRISM in CONUS, WorldClim 1.4 elsewhere,
its centred 5×5 valid-pixel mean where necessary, CHELSA, then native ERA5.
Separate high/low anchors use an ERA5 period adjustment. No station validation
values enter the published correction. All generated data stays in isolated
SSD folders. These scripts do not overwrite `artifacts/v6`, publish, or modify
the running home job. The opt-in B1 application hooks are documented in
[B1_RESULT.md](B1_RESULT.md). Josh accepted B1's city-average score gate with
the uniform evening correction on 2026-10-18. The Step A candidates remain
rejected. [UPLOAD.md](UPLOAD.md) is the worldwide B1 handoff; it states whether
the complete bundle is ready. No deployment is authorized by these scripts.

The requested report is `/tmp/nwf-agents/round4/fine/PLAN.md`. Source rights and
their primary links are retained in [LICENCES.md](LICENCES.md).
The committed report is [PHASE1.md](PHASE1.md); detailed daily and hourly
evidence is in [station-results.md](station-results.md) and
[hourly-results.md](hourly-results.md).

## Phase 2: reproduce the data

Run these commands from the repository. All source downloads are anonymous.
Use new output folders whenever the source files, processor or chunk set
change. The five local chunks are 76, 78, 80, 82 and 84. The selected chunk
set comes from the assembled `climate/meta.json`, including its actual scored
dates; a nominal year range is insufficient. The example uses the native
rebuild's frozen five-chunk metadata so it remains reproducible after the
production metadata grows. A larger assembly must supply its new complete
chunk set and use fresh output roots.

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh anchors.py worldclim --cache "$SSD/fine-sources"
sh research/fine/run.sh anchors.py prism --cache "$SSD/fine-sources"
sh research/fine/run.sh anchors.py chelsa --cache "$SSD/fine-sources"
sh research/fine/run.sh anchors.py era5-monthly --cache "$SSD/fine-sources"

sh research/fine/run.sh rebuild_native.py --source-root "$SSD" \
  --root "$SSD/fine-build/native" --chunks 76 78 80 82 84 --workers 4
sh research/fine/run.sh native_normals.py --native-root "$SSD/fine-build/native" \
  --output "$SSD/fine-build/normals-76-78-80-82-84" \
  --meta "$SSD/fine-build/native/selected-meta.json" --workers 4
sh research/fine/run.sh build_step_a.py --native-root "$SSD/fine-build/native" \
  --normals "$SSD/fine-build/normals-76-78-80-82-84/normals.npz" \
  --source-cache "$SSD/fine-sources" --meta "$SSD/fine-build/native/selected-meta.json" \
  --build-root "$SSD/fine-build/step-a" --output "$SSD/v6-fine-step-a" --workers 4
sh research/fine/run.sh compare_step_a.py --current-root "$SSD/v6" \
  --step-a-root "$SSD/v6-fine-step-a"

sh research/fine/run.sh land_support.py --source-cache "$SSD/fine-sources" \
  --output "$SSD/fine-build/land-support-v2"
sh research/fine/run.sh audit_land.py --source-cache "$SSD/fine-sources" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --output "$SSD/fine-validation/step-b/land-audit.json"
sh research/fine/run.sh native_reference.py --source-root "$SSD/fine-build/reference" \
  --root "$SSD/fine-build/reference-global" --legacy-root "$SSD/fine-build/native" \
  --tile-list "$SSD/fine-build/land-support-v2/reference-tiles.json" \
  --meta "$SSD/v6-fine-step-a/climate/meta.json" --workers 8
sh research/fine/run.sh tiles.py --source-cache "$SSD/fine-sources" \
  --reference-root "$SSD/fine-build/reference-global" \
  --reference-normals "$SSD/fine-build/reference-global/normals/normals.npz" \
  --legacy-points "$SSD/v6-fine-step-a/points.bin" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --meta "$SSD/v6-fine-step-a/climate/meta.json" --output "$SSD/fine-30s"
sh research/fine/run.sh audit_tiles.py --tiles "$SSD/fine-30s" \
  --source-cache "$SSD/fine-sources" \
  --output "$SSD/fine-validation/step-b/tile-audit.json"
```

The reference command above reuses this delivery's `fine-build/reference`
cache, which includes symlinks to the retained full raw chunks. For a fresh
reference build, use `--source-root "$SSD"` and a new reference root; the
script anonymously fetches the missing t2m tiles itself. Reference configurations
bind the input root, so do not change it inside an existing generation.

`build_step_a.py` reuses native cell-day sums, changes fine aggregates using
the average of the high/low air offsets, and replays the exact vapor-pressure
cap change for felt temperatures. It calls the current assembler and default
score/image generator unchanged. Native dew point is never shifted. Its
`fine-build.json` binds inputs, dates, source counts, runtime and output hashes.

`tiles.py` retains separate high/low offsets at 30 arc seconds, with a source
and support byte for every land pixel. Apply the high/low correction across
the day's native temperature range. If the corrected high would be at or
below the corrected low, apply their average offset uniformly for that day.
The same canonical pixel and model must serve the map and place card.
[consumer_design.md](consumer_design.md) describes the binary contract,
weather carrier, measured rendering cost and consumer work still required.

`land_support.py` retains independent Natural Earth, OpenStreetMap coastline,
WorldClim validity and PRISM validity properties. Any-touched polygon rasterization
preserves narrow islands. CHELSA-only coastline support is explicitly marked.
The publication offers OSM-only and polygon-union membership masks separately
under ODbL; source validity is not merged into that coastline property.
`native_reference.py`
retains each coastal pixel's own native ERA5 reference, downloading only t2m
for missing tiles; its reference-only points file is never a publishable v6
points file. Existing raw files remain untouched.

Read [HOME_JOB_HANDOFF.md](HOME_JOB_HANDOFF.md) before incorporating the home
job's additional chunks. Every new chunk set requires new period normals and
corrections across the complete selected set, including a new fine-tile
generation. Keep station validation output private, outside publication roots.
Publication requires the supplied [ATTRIBUTION.txt](ATTRIBUTION.txt), the
CC BY-SA 4.0 weather-data licence, ODbL notices for the separately offered
coastline/timezone properties, and prominent PRISM credit.

The five-chunk Step A validation is in `$SSD/fine-validation/step-a`.
Its current-format bundle is **not recommended for publication alone**:
the unchanged card selects an inland mountain point for Funchal and a point
26 km from San Francisco. The format also clips 0.12% of felt offsets and
cold-region facts. Fine correction values require the scheduled shared-pixel
map/card consumer to remove the geographic mismatch; no place-specific
temperature repair is applied.

[PHASE2.md](PHASE2.md) records the earlier generations and their limits;
[B1_EVENING_RESULT.md](B1_EVENING_RESULT.md) records the accepted evening
comparison. [UPLOAD.md](UPLOAD.md) supersedes earlier release-status notes.
Step A receives `ATTRIBUTION-STEP-A.txt` as its output `ATTRIBUTION.txt`;
the fine tiles receive `ATTRIBUTION.txt`, including the separate OSM coastline
property and its offered ODbL masks. Both retain the separately offered timezone
lookup and its ODbL notice.

## Phase 1: reproduce the experiments

Run from this repository. `run.sh` enters its existing devenv and uses uv for
the research Python dependencies. Raw downloads and derived samples stay in
`/tmp/nwf-agents/round4/fine` or the SSD's `fine-phase1` directory. No credentials
or accounts are needed.

```sh
python3 research/fine/stations.py
sh research/fine/run.sh published.py --output /tmp/nwf-agents/round4/fine/live_points.json
sh research/fine/run.sh chelsa.py --evaluation-stations /tmp/nwf-agents/round4/fine/evaluation_queries.json
sh research/fine/run.sh grids.py worldclim --stations /tmp/nwf-agents/round4/fine/evaluation_queries.json
sh research/fine/run.sh grids.py prism --stations /tmp/nwf-agents/round4/fine/evaluation_queries.json
sh research/fine/run.sh hourly_stations.py /tmp/nwf-agents/round4/fine/hourly
sh research/fine/run.sh era5_points.py --stations /tmp/nwf-agents/round4/fine/evaluation_queries.json
python3 research/fine/residuals.py
sh research/fine/run.sh compare.py
sh research/fine/run.sh hourly_compare.py
sh research/fine/run.sh resolution.py
```

`stations.py` retains the earlier fifteen evaluation stations, then chooses
nearby stations for the additional requested places. It downloads its own
metadata when earlier research is unavailable. It excludes every evaluation
station ID and every station within 5 km of an evaluation site from training.
Monthly summaries reject NOAA quality flags and retain completeness, source
flags and file hashes. The conservative default requires eight complete
month-years in 2016–2025; a separate five-year sensitivity remains labelled.

`chelsa.py` samples exact containing 30 arc-second pixels and a separate
centred 5×5 mean. HTTP ranges are cached. Missing coastal pixels stay missing.
`grids.py` samples WorldClim **1.4**, whose specific historical-data licence is
CC BY-SA 4.0, and PRISM's anonymous monthly-normal service. WorldClim 2.1 is
only read as a private comparison from previous research; it is not an input
to a publishable correction.

`era5_points.py` fetches native and current-product source point histories
from a pinned anonymous Earthmover Icechunk snapshot. It retains small point
histories, rather than global hourly tiles. Native histories cover 1960–2025
for period comparisons; product-source histories cover 2016–2025. A changed
station coordinate requires a separate scratch folder before replacing its
old point history. This matters for Chania's conflicting station metadata.

`compare.py` pairs daily observations and ERA5 by local calendar date. It
reads the authoritative `stations/station_monthly.csv` generated by the first
command; no manually prepared evaluation-only CSV is needed. It
compares independent high/low offsets, a uniform shift, and a common monthly
mean-temperature period anomaly. The ordered candidate uses the averaged
uniform offset only on days whose independently corrected high would cross
the corrected low. The source policy selects by availability: PRISM, exact
WorldClim1.4, its centred5×5 mean, CHELSA, then explicitly coarse native ERA5.
Instrument daily extrema and the product's
10:00–20:00 high have different observation windows and are labelled. Live
published facts and the matched-period reproduction of the current algorithm
are reported separately.

`hourly_stations.py` acquires NOAA GHCN-hourly and, where its aliases omit
useful reports, legacy ISD for **private validation**. It retains the nearest
report within 30 minutes of an hour and its quality flags. `hourly_compare.py`
compares ERA5 at those same sampled hours, and tests actual 21:00 air
temperature separately. Daily minimum temperature is not an evening measure.
Both comparisons retain the affine alternative and its amplification audit;
it is not the recommended hourly correction.
Station locations more than 5 km from the sampled site are rejected; uncertain
Chania remains diagnostic. Agadir uses the explicitly labelled Al Massira
airport proxy, about 21 km from the city.

`residuals.py` is a **private experiment**, not the published model. It fits
monthly station-minus-CHELSA residuals, chooses a distance scale through
training-only spatial cross-validation, then reports the held-out results.
Foreign GHCN-Daily derived-data rights remain conflicted. A separate
source-filtered US experiment does not clear the global fit. Current
GHCN-hourly has an explicit CC0 grant if subsequent global station fitting is
needed.

## Interpretation

All requested evaluation stations are excluded from our fitting and parameter
selection. Published station-interpolated grids may already contain these
stations. This is later-period validation of those grids, not proof of an
independent spatial holdout in their original construction.

The monthly-temperature corrections preserve individual ERA5 weather days.
They do not by themselves validate sunshine, fog, wind, dew point or the final
comfort score. In particular, the present inland-source/sea-level correction
must not also be applied to dew point in the new temperature experiment.

Sparse and proxy sites remain visible in the output. They must not be pooled
with complete instrument-extrema records or presented as city-centre truth.
The report records measured acquisition/reduction times and separates them
from estimates for a future global build.

## Capture a published baseline

`published.py` downloads the live `points.bin`, monthly facts, default scores
and climate metadata. It reproduces the Worker's rounded-centre, clipped 5×5
nonzero-facts selection, including tie order, then checks every decoded month
against `/api/place` at the evaluation station's coordinates. It includes the
Agadir airport proxy and preserves Chania's conflicting original query. Raw
responses, input hashes, query URLs and capture time stay beside the result.
The public binary snapshot is about 90 MB. Existing SSD static elevation/land
fractions are optional diagnostic metadata, with their directory recorded.

```sh
sh research/fine/run.sh published.py \
  --output /tmp/nwf-agents/round4/fine/published-capture/live_points.json
sh research/fine/run.sh era5_points.py \
  --stations /tmp/nwf-agents/round4/fine/published-capture/evaluation_queries.json \
  --live-points /tmp/nwf-agents/round4/fine/published-capture/live_points.json
```

Use a new capture folder when the live site changes; keep the original baseline
for reproducible errors. On a fresh Phase 1 run, `--output
/tmp/nwf-agents/round4/fine/live_points.json` puts the capture at the comparison
scripts' default input path. The emitted `evaluation_queries.json` also contains
the additional Agadir airport site for grid sampling and ERA5 acquisition.
