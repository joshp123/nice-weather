# Himawari acquisition scope

Written by AI on 2026.10.18 by model GPT-6.1 Sol.

This is a bounded source probe, not an acquired worldwide correction. It does
not alter the production targets, the scoring method or the deployed site.

The [producer's Himawari TMY documentation](https://developer.nlr.gov/docs/solar/nsrdb/himawari-tmy-download/)
identifies PSM v3, a 2011–2020 source period, 4 km pixels and hourly typical
years. Its named options include TDY. The [producer-managed AWS registry](https://registry.opendata.aws/nrel-pds-nsrdb/)
permits anonymous access with no AWS account and grants CC BY 3.0 US. The
source therefore does not require another paid service or an API account.

The anonymously inspected `himawari/himawari_tmy-2020.h5` archive is
939,864,525,852 bytes. That is not the acquisition size: DNI is uncompressed
`uint16`, shape 8,760 × 2,170,782 and physical chunks 1,344 × 745. One complete
745-site group requires seven chunks, 14,017,920 bytes. Selecting the named
points and sharing their physical chunks can avoid the rest of the archive.
The complete metadata plan finds **29,586 of 170,513 named coordinates** within
8 km of a native pixel. They span **2,667 physical groups**, requiring
**37,385,792,640 bytes of radiation transfer**. The whole native domain contains
2,914 groups, an upper bound of 40,848,218,880 radiation bytes.

These are network transfer sizes. The 29,586 selected DNI columns alone would
occupy 518,346,720 bytes uncompressed. Shared timestamps, metadata, provenance
and derived targets need additional disk space. The probe retains requested
columns only; it does not retain downloaded physical radiation chunks.

Metadata is also chunked: 16,806 compound records per chunk, 119 bytes per
record, 258,323,058 bytes total. The existing GOES contiguous-metadata reader
cannot be reused unchanged. `named_himawari_probe.py` asks HDF5 for physical
chunk offsets and downloads metadata with an explicit additional byte budget.
It selects the spherical nearest actual pixel for every named coordinate.
It reads the twelve selected source years as individual values from their
physical chunks.
The default 100,000,000-byte budget includes both metadata and radiation.
Only Bangkok, Singapore, Perth, Sydney and Tokyo from `artifacts/places.tsv`
can be acquired by this probe.

The [producer's source at the archive's declared version 3.2.0](https://github.com/NatLabRockies/nsrdb/blob/v3.2.0/nsrdb/tmy/tmy.py)
constructs the typical-year timestamps at half past each hour
(`Tmy.time_index`), and
subsamples source observations rather than averaging them
(`Tmy._make_tmy_timeseries`). The inspected archive's first and last timestamps
are `2020-01-01 00:30:00` and `2020-12-31 23:30:00`; it omits leap day.
These are midpoint samples, not measured hourly means. The probe uses the
existing minute-midpoint linear integration in `instant_hours.py` and each
place's canonical IANA timezone, with civil offsets from that month's actual
selected source year. It applies the existing sunset denominator and
300 W/m² threshold unchanged. A TMY remains a selected typical year, not a
ten-year hourly distribution or an observed long-term sunshine normal.

The five radiation probes use TMY, matching the common source choice selected
by the independent Americas controls. TDY metadata was reused after checking
the actual TMY metadata shape and 25 native coordinate records. Both actual
archives have the same declared version, dimensions and physical DNI chunk
shape. Radiation chunk offsets always come from the actual source file. The
five selected pixels are 0.28–1.79 km from the exact named coordinates.

All five TMY probes completed. Each retained raw array is `uint16`,
8,760 × 1, with 8,760 strictly hourly UTC timestamps at minute 30 and all
twelve selected source years inside 2011–2020. The five physical groups
transferred 70,089,600 radiation bytes. The final TMY run used 74,155,927
range-payload bytes including HDF5 structural metadata, 25 coordinate
checks and 60 selected-year values. This equals the successful payload
sizes; no full-sized retry reservation was needed in that run. Separately,
the complete TDY metadata consists of 259,988,820 physical chunk bytes,
including the final padded chunk. HTTP headers and earlier diagnostic
requests are not included in those figures.

An independent direct `numpy.interp` calculation evaluated every minute
midpoint of all twelve civil hours on all 365 days for each point. It used
the selected source month's actual IANA clock and the common sunset rule.
Every monthly sunshine share matched exactly; the largest monthly hourly
DNI difference was 5.69 × 10⁻¹⁴ W/m². Sydney's October sample correctly used
both UTC+10 and UTC+11. All monthly day counts matched the non-leap typical
calendar. This verifies extraction and time integration, not accuracy
against independent sunshine observations.

The resulting share of visitor daylight hours with DNI at least 300 W/m²:

| Place | January | July |
| --- | ---: | ---: |
| Bangkok | 58.4% | 19.4% |
| Singapore | 19.7% | 43.9% |
| Perth | 80.0% | 45.2% |
| Sydney | 54.3% | 57.1% |
| Tokyo | 70.0% | 24.7% |

Raw arrays, results and dated primary-source snapshots are retained under
the external SSD's `sun/named/himawari/`. No source inputs are stored in git.
The exact-point timezone export is shared with the production model.
`tmy-independent-validation.json` records the independent calculation’s
array shapes, ranges, clocks and numerical comparisons.

The approved metadata-only plan and the five-point TMY probe run separately:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
devenv shell -- uv run --python 3.12 \
  --with numpy --with scipy --with h5py --with fsspec --with aiohttp \
  python research/sun/named_himawari_probe.py --source tdy --metadata-only \
  --full-metadata-plan "$SSD/v6-named-b1-20261018/coordinates.json" \
  --max-bytes 400000000

devenv shell -- uv run --python 3.12 \
  --with numpy --with scipy --with h5py --with fsspec --with aiohttp \
  python research/sun/named_himawari_probe.py --source tmy \
  --reference-metadata "$SSD/sun/named/himawari/tdy-metadata.npy" \
  --full-metadata-plan "$SSD/v6-named-b1-20261018/coordinates.json"
```

Results and any proposed wider acquisition budget must be reviewed before a
global download is started. Unsupported or unacquired places remain unchanged.
