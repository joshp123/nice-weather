Written by AI on 2026.10.18 by model claude opus 5.5.

# ERA5 hourly weather

`fetch_era5.py <chunk> <processes>` downloads one ERA5 "chunk year" (8,736 hours) of the six fields Nice Weather needs (2 m temperature and dew point, 10 m wind, total precipitation, direct radiation) for every 3° land tile, from Earthmover's free, anonymous public ERA5 archive (CC BY 4.0). It is resumable: finished tiles are skipped. `research/climate-v6` turns the downloaded chunks into the published typical-year files.

## The 25-year climate

The published climate uses chunks **61–85**: **2000-10-16 00:00 UTC to 2025-09-15 00:00 UTC, exclusive**. The processor scores 360 local dates per chunk, **9,000 days** in total, from **2000-10-18** to **2025-09-12**. The calendar labels in `meta.json.years` are 2000–2025; `chunkYears: 25` gives the number of complete chunk years. Each month still publishes **eight representative days per cell**, chosen across all selected years, so the published tables are the same size as with five years.

The overview uses `research/climate-v6/pipeline.py`, then both default-score commands (`scores.bin` and the score images for every run of consecutive months). Named places use the native-normal, correction-tile, replay and compact named-place builders in `research/fine`. Five-year corrections or replay packets must not be reused for a 25-year named table. Every catalogue place, map label and comparison anchor uses its exact production packet address: 50,436 of the 397,686 worldwide point/clock pairs, in 1,867 spatial tiles.

A release is a set of immutable files with an upload inventory. After upload, every file's checksum is read back before the Worker that selects the release is deployed. A release is live only after the live `meta.json`, the named-place comparison and the phone and laptop views have been checked. Earlier releases are kept for rollback.

## Checking a release

`research/climate-v6/compare_named_days.mjs` reads the production decoder and ranking rule and compares nice days for the year and for November–March, including the exact Tenerife south-coast coordinate separately from named Arona. The five-year baseline is kept in [named-five-year-baseline.md](../climate-v6/named-five-year-baseline.md); the 25-year comparison is in [named-days-25.md](../climate-v6/named-days-25.md). With `--verify-live`, the script also requires the live named configuration to match the release's root, build generation and table checksum.

The bottom band has one editable value: `hosting/sites/awful-maximum.json` (**15**). JavaScript, TypeScript and the GPU shaders, Python and Go all derive it. A score of 23 is Poor; a score of 0 is Awful. Rebuild the images, named counts, forecast counts and posters when it changes.
