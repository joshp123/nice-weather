Written by AI on 2026.10.16 by model GPT-6.1 Sol.

The Go climate day-measures builder works. A linked Sites cloud task built and
uploaded a real tile-year. The full climate run has **not** started. No app
deployment, UI change, scoring change, forecast-job change or live-data update
was made.

The builder reads six hourly ERA5 fields from a pinned, anonymous public
archive. It selects coastal temperature cells, corrects temperature to ground
height, resolves IANA local dates and writes physical measures. One shared Go
contract and 48-byte binary record serve climate and the opt-in forecast
adapter. Records are gzip-compressed, ordered by cell then date, and stored in
independent tile-year files with input identities and checksums. No grades or
preference curves are present. The executable needs no installed data tools;
Python's standard library only handles the cloud launch and hidden upload grant.

The observed cloud result is retained in [climate-probe-result.json](climate-probe-result.json).
It covers 131 land cells in southern Spain and its coast, for all 366 local
dates of 2020: **47,946 records**.

| Measurement | Real Sites cloud result |
|---|---:|
| Build, including ERA5 reads | 19.106 s |
| Build and batch upload | 27.168 s |
| Executable and static-file download | 12.269 s |
| Cloud chat, including setup, retry and verification | 2 min 25 s |
| Process peak RSS | 949,723,136 bytes; 905.7 MiB |
| Observed Go heap peak | 828,055,552 bytes |
| ERA5 snapshot, manifests and 12 source chunks read | 26,115,302 bytes |
| Compressed measures written | 1,469,483 bytes |
| Measures and batch report uploaded | 1,470,120 bytes |
| Executable and static file downloaded | 42,551,553 bytes |

These are cloud measurements, not local timings. HTTP headers and TLS overhead
are excluded. The result JSON itself adds 1,556 uploaded bytes. The launcher
initially received HTTP 403 with Python's default user agent; a curl user agent
worked and is now explicit in the saved launcher. An earlier cloud result for
the same batch recorded an 18.672 s build and the same output checksum; the
table uses the final cloud chat's verified result.

I downloaded the published measures and decoded every record with the shared
Go reader. Length and SHA-256 match the cloud report. Each cell has 366 dates.
Rain coverage is 23 hours on 131 records, 25 hours on 131 records and 24 hours
on the remaining 47,684. Every daytime window has ten hours and valid sunshine
coverage. The temporary linked automation was deleted after verification;
Sites now lists only the existing daily forecast automation for this project.
Scratch artifacts remain under `forecast/climate-measures-probe/`, away from
the live pointers. The normal forecast cleanup may remove them later.

After the timed run, remote resume was added to the CLI. A fresh cloud task
can now restore a completed R2 batch, verify its length, checksum and full
input identity, and skip both ERA5 reads and upload. The actual CLI was exercised
against this published batch from a fresh output directory with no credential;
an independent reviewer repeated that flow. Resumed reports preserve the
original build metrics. The probe executable remains the exact measured
artifact, identified by its recorded checksum; current source also includes
this subsequent resume step.

The twelve-place retained research comparison passes **87,659 daily records**.
Maximum stored temperature difference is 0.0050061 °C; sun-share difference is
below 0.000051; rain difference is zero at the reference's 0.1 mm precision.
Ten-sample dew-point and wind means match to floating-point precision. Details,
coverage gaps and exact tolerances are in
[the retained comparison](../../internal/climatemeasures/REFERENCE.md).
This runs retained hourly facts through the production conversion, solar model,
aggregator and binary codec. It does not redownload every historical chunk.
Actual archive reading is separately proved by the cloud tile-year and an
independent full-chunk decoded checksum. A fresh reviewer repeated both the
retained comparison and public-archive decoder check.

The research's original state samples include 20:00, making eleven samples.
The requested ten are 10:00 through 19:00; rain and sun integrate 10:00–20:00.
The comparison uses the first ten research temperatures and recomputes dew and
wind from those same ten hours. It does not claim eleven-sample means are equal.

The full grid has 364,523 land cells in 3,592 tiles. For 2006–2025 that means
71,840 tile-years and about 2.663 billion daily records; nonexistent local
dates are omitted. Scaling this single tile gives:

| Full-run estimate | Amount |
|---|---:|
| Records before gzip, excluding tiny headers/reports | 127.82 GB |
| Gzip measures at this tile's bytes per record | **81.61 GB** |
| Source reads at this tile's bytes per tile-year | **1.88 TB** |
| Serial build time | **15.9 days** |
| Serial build and upload time | **22.6 days** |
| With a fresh executable/static download for every batch | **32.8 days** |

GB and TB are decimal. These are projections, not measured global totals.
This tile has 131 cells; the global mean is 101.5. Weather entropy, coastal
temperature neighbours and source-chunk boundaries change compression and I/O.
Some neighbouring batches reread chunks. Automation setup adds time beyond
the program projection. Parallel throughput, long-run scheduling, storage
capacity and free-account quotas have **not** been established by this probe.
It proves that a single batch fits and runs on the free linked-cloud path;
it does not prove that Sites will admit an 82 GB dataset or 71,840 task runs.

The forecast adapter supplies the same temperatures, dew point, rain and wind
measures from ECMWF's sparse forecast steps. The inspected open-data run has
total downward solar radiation, cloud cover and partial-hour gust maxima, but
no direct-beam or clear-sky direct radiation. Sunshine and complete daytime
gust are explicitly unavailable. No radiation decomposition or cloud-cover
substitution is introduced. The current forecast job is unchanged.

Before a full run: agree the resource plan and confirm available storage and
execution limits. Historical raw ERA5 winds also retain documented analysis
spikes; this work does not repair or publish those inputs. The terrain has a
documented sea-level approximation at 389 coastline/island cells. Judgement,
period summaries, forecast-job integration and any presentation change remain
separate work.
