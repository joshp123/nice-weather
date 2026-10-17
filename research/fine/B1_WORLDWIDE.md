Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Worldwide B1 is not an uploadable bundle yet. The station score gate must pass
first. The thirteen-place diagnostic is not worldwide coverage, and the global
temperature reference does not contain global humidity, wind, precipitation or
radiation. This note records the measured remaining work; it does not approve
publication or change the map.

Measured input coverage

The inventory uses the actual 30 arc-second land support, the same containing
pixel and nearest own native point calculation as the shared place loader, and
the central directories of the retained raw NPZ files. It does not decompress
or verify the raw values. The private machine-readable inventory is
`fine-validation/b1/worldwide-inventory.json` on the SSD.

- 320,420,484 supported 1 km land pixels require 376,591 distinct own native
  quarter-degree points, in 3,662 raw spatial tiles per chunk.
- All required temperature points exist in the 528,624-point global reference.
  The larger reference also supplies bilinear anomaly corners and ocean points
  within required raw tiles. It is not a full-weather replay carrier.
- For each of chunks 76, 78, 80, 82 and 84, 2,499 required raw tiles contain all
  six weather fields. Another 1,163 tiles contain temperature alone. None of
  the required temperature tiles is missing.
- 266,739 required native points have full weather. The other 109,852 points
  serve 96,031,771 fine pixels and lack dew point, both wind components, rain
  and direct radiation. Those five fields must be fetched for 5,815 tile-chunks
  from the same anonymous pinned Earthmover source. No new account is needed.
- The retained required raw inputs total about 132.2 GB compressed. About
  125.5 GB of that is the existing full-weather tiles. The exact file counts
  and bytes by chunk are in the inventory.
- The partial correction set contains 74,188 monthly files, 2,623,824,977 bytes.
  There is no completion manifest. Its original builder and configuration still
  match, so the existing resumable builder can finish and then audit it without
  changing the daytime high offsets. Existing files must retain their original
  generation; a changed evening consumer does not alter high/low anchor values.
- Thirteen diagnostic JSON packets total 6,639,392 compressed bytes. Their
  manifest records 5.83 seconds to build. This is not a valid global runtime
  measurement. Straight point-by-point scaling would take about 47 hours and
  produce about 192 GB compressed, before target-clock variants.
- The SSD had 1.58 TB free at inspection. No other job was stopped, changed or
  contacted by this inventory.

The clock issue

The cached native reference has 436 distinct IANA timezone names. Its lookup
is evaluated at each quarter-degree native point. A place near a timezone
border can have a different local clock from that point. The diagnostic replays
its native point's clock; a worldwide claim must state and resolve that limit.
Use the target place's timezone and own native weather, never an adjacent
point's weather. Reuse packets for equal `(native point, target clock)` pairs.
Keep the target timezone lookup independently attributed under ODbL, as with
the existing lookup. Do not label native-point timezone coverage as exact
1 km target timezone coverage. The inventory does not count the unbuilt target
clock pairs.

Smallest exact worldwide carrier

Do not replicate 1,800 days of twelve full hourly arrays in JSON for every
native point. Decode each raw spatial tile once, handle its required native
points and clock variants in batches, and write one compact response packet
per required point/clock pair. Keep these independent data groups:

1. Ninety-six representative days: each day's native 12-hour air/dew/sun/shade
   temperatures, 24-hour high and low, rain, mean wind, sunny hours and daylight
   hours. This is 54 float32 values per day, 20,736 bytes before compression.
   Preserve existing geometric-cell quantile date selection, with the same
   own-point fallback for absent geometric cells. Dates/indices can share a
   generation-level axis.
2. Every assembled day: native 24-hour high, low, daytime air maximum, air at
   21:00, dew at 21:00 and wind at 21:00. These six response values let the
   shared function recover physical high and evening facts for arbitrary
   anchor offsets without sending all hours. At 1,800 days this is 43,200
   float32 bytes. The high transform is monotone under the ordered rule, with
   the existing uniform fallback, so the native daytime maximum is sufficient.
3. Twelve monthly native dew, wet-day, sunshine and mean-wind facts: 192 bytes
   as float32. They do not change with the high/low correction. The dew facts
   retain native dew, as in the delivered-byte diagnostic.

That is 64,128 uncompressed bytes per native point before dates, identity,
headers and clock variants: about 24.2 GB for the measured 376,591 points.
Compression, elapsed build time, additional timezone variants and the served
packet latency must be measured on the actual builder before promising a
release size or completion time. The existing shared loader supports JSON
full-history packets only; this compact format needs a decoder and matching
full-period facts path before it can be uploaded.

For capacity planning only, missing five-field downloads are on the order of
50 GB compressed if the existing full-weather tiles' sizes are representative.
The home job's measured approximately nine-second six-field archive reads at
24 reader threads would imply a roughly 38-minute read-only lower estimate for
5,815 tile-chunks, excluding retries, compression and SSD writes. Different
polar fields and this Mac/network can differ substantially. This is not an
observed global acquisition or build time. Do not alter the home job or use its
NAS as scratch to achieve it.

Release order after a passing visitor-score gate

Finish and audit the complete correction set first. Download only the five
missing fields into a new private cache; preserve retained temperature files
and all other workers' paths. Build the compact replay carrier into a new SSD
folder. A finished replay manifest must cover every supported fine pixel's
point/clock pair, state the approved single evening rule, identify the same
five-chunk temperature generation, and hash every served packet. Decode the
actual bytes through the shared Worker/app function and repeat the city score,
nice-day-count and physical-temperature comparison. Verify representative
boundary clocks and dateline/polar queries as well as the city cohort.

Prepare these storage keys only after those checks pass:

- A versioned correction directory containing all `tiles/`, `tiles.bin`,
  `tiles.sha256`, `manifest.json`, attribution, source provenance, independent
  land-membership/timezone databases and their licences.
- A versioned replay directory containing all packets, its index/manifest,
  date axes, target-clock addressing, attribution and licence notices.
- `v6/fine/current.json`, uploaded **last**, pointing at those two complete
  directories and naming the one evening rule. The overview files under
  `v6/climate` remain the existing 25 km map.

The storage namespace is relative to the data root served by the existing
Worker. `hosting/sites/publish.mjs files <SSD root> <site URL> <key>...` is the
existing upload mechanism. Do not invoke it under this task: Josh said to
prepare data and exact upload instructions, not deploy. No pointer should
reference the thirteen-place diagnostic or the incomplete tile directory.

To repeat the read-only inventory:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh worldwide_inventory.py \
  --data-root "$SSD" \
  --output "$SSD/fine-validation/b1/worldwide-inventory.json"
```
