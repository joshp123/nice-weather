Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The fourteen-place satellite targets are complete. Worldwide acquisition is
paused pending the owner's transfer-budget decision. No worldwide correction
set exists and nothing in this source task was deployed.

The complete validation input is
`$SSD/sun/named/validation-final.json` (76,258 bytes, SHA-256
`f0ada0a995daf443ac2d3f9da76c136e1f3e568c5fca36e5d4820f06061f7c2e`).
It has six SARAH targets, six northern GOES TMY targets, and two southern
GOES targets averaged over 2022 and 2024. Every target uses the shared place
model's exact 1 km civil timezone and minute-integrated instantaneous DNI.
Iquique is inside the northern native grid and uses the same TMY as all other
northern points. TDY was tested and rejected as a universal replacement.

`$SSD` below is `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data`.

| Component | Retained state | Full acquisition payload |
| --- | --- | ---: |
| SARAH TMY | 17,154 valid compressed CSVs and normalized targets | About 59 GB CSV for all covered named coordinates |
| SARAH 2005–2023 normal profiles | 43,991 valid responses; 43,990 complete NDJSON records | About 2.1 GB |
| Northern GOES TMY | Full native coordinates and selected-year table; controls and four-point throughput probe | Current plan: 2,915 groups, 29.150 GB DNI |
| Southern GOES 2022 and 2024 | Complete 2022 native coordinates; fourteen-place controls | 664 groups, 35.856 GB/year, 71.712 GB total DNI |

The current total is about **162 GB before Asia**, rather than a few tens of
GB. The separate Himawari plan adds about 37.39 GB, bringing the combined
estimate to **199.35 GB before small overlap reductions and metadata**. Northern source priority may remove a few more groups when all SARAH
profiles finish. Southern deduplication is complete: 1,055 named coordinates,
with every SARAH-supported and northern-GOES-supported point excluded.
No worldwide northern or southern radiation transfer has started.

The 16-worker northern probe transferred 40,000,000 radiation bytes in
74.79 seconds: **0.535 MB/s**, excluding metadata lookup. At that observed
rate, the northern 29.15 GB alone takes about **15 hours**. Applying that rate
to the two southern years gives another roughly 37 hours; southern throughput
has not been measured. Do not promise a worldwide release tonight from this
Mac's measured connection. The PVGIS route is considerably faster.

The CSV cache contains 10,078,419,349 accepted decoded bytes. These responses
were uncompressed on the wire. The profile cache contains about 0.91 GB of
accepted decoded data. Retained HDF metadata includes 1.419 GB of full-disc
metadata, 0.262 GB of northern metadata and 0.097 GB of selected years.
Retries, discarded metadata windows and earlier probes are not fully metered;
accepted cache bytes are a lower bound on network traffic, not an exact wire
counter. Whole-world transfer estimates above count the source payloads, not
only retained compressed scratch.

`Accept-Encoding: gzip` did not reduce PVGIS CSV transfers: the same 594,754
bytes arrived with no Content-Encoding through both tested routes. A bounded
cache audit compared the 200 closest pairs inside assumed 0.05-degree bins:
zero had identical hourly DNI, although 197 pairs selected the same years.
There is no verified rule permitting those requests to be shared.

PVGIS stopped automatically after 904,326,739 additional accepted response
bytes, with room left for responses already in flight. Its two workers are
stopped. Both NDJSON streams end with a complete, valid record; no repair was
needed. A complete integrity read checked all 61,145 compressed CSV/profile
files (10,989,437,468 decoded bytes): zero corrupt files and zero partial
files. Evidence:

- `sun/named/acquisition-stop.json`
- `sun/named/pause-checkpoint.json`
- `sun/named/cache-integrity.json`
- `sun/named/wire-audit.json`
- `sun/named/duplicate-audit.json`
- `sun/named/goes/tmy-global.plan.json` and `.locations.json`
- `sun/named/goes/disc2022-global.plan.json` and `.locations.json`
- `sun/named/goes/throughput-probe.progress.json`

The profile progress file conservatively says 43,900 completed; there are
43,990 complete stream records and 43,991 cached responses. This is an
interrupted checkpoint, not a completion claim. Restart the producer and its
follower together: the producer rewrites the stream from completed cached
responses, and both progress files are recomputed from actual cached work.
A cached response or target does not incur another download. The exact-clock
cache check reruns a reduction if its target timezone changed.

After approval, start these two commands concurrently in separate owned jobs:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/sun/run.sh named_profiles.py \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --output "$SSD/sun/named/sarah-profiles.ndjson" \
  --workers 64 --requests-per-second 20
```

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/sun/run.sh named_satellite.py \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --profiles "$SSD/sun/named/sarah-profiles.ndjson" \
  --output "$SSD/sun/named/sarah-normalized-global.json" \
  --normalize --verified-csv --watch-profiles --workers 96 \
  --requests-per-second 8 --finished-requests-per-second 25
```

The two API limits total 28 requests/s, below PVGIS's documented 30/s ceiling.
The follower accelerates only after the producer finishes. In the last run,
new DR responses averaged about 14/s and new TMY responses about 7/s; cached
warmup rates must not be used as network completion estimates. The remaining
SARAH payload is approximately 49 GB TMY plus 1.2 GB profiles. Measured rates
suggest hours, not minutes; the route may change after the owner's decision.
See `named_inputs.md` for the exact native-plan/index/extract commands.
Finish profiles before taking the final north/south plans; extract each with
an explicit `--max-groups` matching its newly approved plan. Southern extract
must use both `--exclude-profiles` and `--exclude-locations` from the completed
northern plan. Run both source years, then `named_targets.py` in worldwide
mode. Its completion gates reject missing eligible coordinates. The output
must be `sun/named/world-targets.json`; its support table replaces the current
fourteen-place validation support table before any public staging.

Thirty-one catalogue points have no existing supported fine-place clock/model.
They remain unchanged, with a separate model-unsupported reason. Locations
outside the acquired sources also remain unchanged. Other independent public
satellite sources, including Himawari, exist outside this acquisition; do not
claim that such locations have no satellite coverage. The separate Asia probe
and its budget are documented in `NAMED_ASIA_SCOPE.md`.

At 2026-10-18 10:58 UTC, all acquisition workers and budget watchers owned by
this source task are stopped. There is no continuing worldwide transfer.
The complete northern chunk directory is retained as
`sun/named/goes/tmy-dni-chunks.json`: 20,185 exact HDF5 chunk addresses,
741,560 bytes, SHA-256
`1d4ec23042d50b7bd5408d61e1ed632543ac99d1aec4275bf72c2c8ac1d2f1e7`.
It took about 22 minutes; metadata traversal accelerated after PVGIS stopped.
The current north plan selects 14,575 of those chunks. They form 3,263
contiguous physical runs, at most 10 MB each; coalescing adjacent reads may
reduce request overhead, but no improved throughput has been measured.
The producer currently fetches individual 2 MB chunks. This opportunity does
not change the 199.35 GB source-payload budget.

A cached replay of all four newly downloaded throughput-probe points matched
the extracted values exactly across 48 monthly targets and 576 hourly DNI
means, with zero network requests. Evidence:
`sun/named/goes/cached-replay-proof.json`.
