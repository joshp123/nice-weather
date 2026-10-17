Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Status: the 15-byte format and measured approximation error were accepted by Opus on 2026-10-17. Accuracy statistics remain in meta.json and the report; they do not block readiness.

For the ongoing expansion from five to twenty-five chunk years, see
[`../era5/README.md`](../era5/README.md). Its home runner streams raw tiles to the
NAS; its laptop receiver assembles only complete chunks at 10, 15 and 25 years.
It uses `assemble(root, publish=False)` to generate scores and images in the
immutable snapshot before exchanging the complete climate directory atomically.
Assembly retains daily reference scores after each chunk, so the number of years
does not multiply the memory used by hourly features.

Run from the repository:

```sh
research/climate-v6/run.sh static
research/climate-v6/run.sh watch --workers 4
```

`watch` consumes each completed ERA5 tile once and assembles when every land cell has a complete sample. Only complete chunks are assembled. Later chunks may be processed incrementally but enter the publication together once every tile is ready. Stop the watcher before manually running another processing command.

For a single rerunnable command that processes all currently available input and assembles:

```sh
research/climate-v6/run.sh run --workers 4
```

To assemble from existing intermediates without processing more input:

```sh
research/climate-v6/run.sh assemble
```

All large files live on the SSD. Published files are under `v6/`; reusable intermediates are under `climate-v6-work/chunkK/`. The latter contain daily sums of the full hourly felt temperatures and day quantities by cell, each fine point's monthly sums, and its daily daylight count. They retain enough information to assemble cell records, monthly offsets and facts without reopening hourly source tiles. A separate `timezones.npy` caches each selected point's IANA zone.

ERA5 fdir is direct radiation through a horizontal plane. The processor divides each local hour's mean direct radiation by the integrated positive cosine of the solar zenith angle over that hour (three-point Gaussian quadrature) to supply the rule's DNI. Instantaneous fields are interpolated to the exact local clock hour; hour-ending accumulations are weighted by their overlap with that local hour. This also handles half-hour and quarter-hour zones. UTC offsets come from ZoneInfo on each date. The first two and last two local dates of each 364-day chunk are omitted globally to ensure complete local days.

Sunset uses NOAA's solar equations in each point's local calendar. The solar date is aligned across the international date line; late summer sunsets after midnight remain on the next local day. The independent check in `check_daylight.py` compares real locations against a separate Julian-century solar calculation.

`static.py` follows the contract's within-tile 3×3 coastal rule. If that neighbourhood has no point with land-sea mask ≥0.8, the point remains its own temperature source.

`validate.py` independently reads the binary files, checks static selection and encoding, compares seven places with Fable's ten-year table, and creates an offsets preview. `climate/meta.json` includes the monthly full-hourly versus compact score errors over every published cell and the eight-day sample errors.

Assembly writes an immutable snapshot in `climate-v6-work/assemblies/`. Its root contains `v6/cells.bin`, `v6/points.bin`, `v6/climate/` and readiness markers. The production climate directory receives hardlinks to those immutable files through Darwin's atomic directory exchange; old generations are retained. `v6/READY-climate` points at that directory's metadata, so its chunk list changes with the data. For an upload spanning another assembly, use the immutable snapshot directory named by `buildId` in `climate/meta.json` as the artifacts root (the full private path is in REPORT.md). Static files and forecast directories are not replaced. Independent validation and REPORT.md refresh after each assembly.
