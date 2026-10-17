<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->

# Exact place replay in the browser

The app requests `/api/fine/replay?lat=<latitude>&lon=<longitude>` for any named or clicked point. The Worker uses `createFinePlaceLoader`, reads that exact 30 arc-second pixel's twelve correction pairs and its own native weather point in the target local clock, then returns only the representative weather needed by `placeNumbers`. The browser decodes this packet and calls the same `placeNumbers` function. Monthly physical temperatures continue to use the full 1,800-day response inside the Worker. The accepted daytime correction and uniform evening correction are unchanged.

The route without coordinates returns `{enabled: boolean}`. Absent activation or an unsupported pixel gives HTTP 404 for a coordinate request. Both status and packet responses expire after 60 seconds, matching the activation pointer's refresh interval. Generation and evening-model headers must match the packet; its pixel must match the requested coordinates.

The `NWPL` version 1 packet contains:

- A 16-byte little-endian header: magic `NWPL`, uint16 version, uint16 representative-day count, uint32 metadata byte count, uint32 reserved zero.
- UTF-8 JSON metadata: pixel row/column, status, evening model; native point index, generation, target timezone, representative dates and their original date-axis indices.
- Twelve high/low correction pairs as 24 float64 values, including the existing target-clock adjustment.
- Float32 arrays in this order: air, dew point, felt sun and felt shade (12 hourly values per day each); native daily high, low, rain, wind, sunny hours and daylight hours (one per day each).

The response uses gzip. The browser never requests the twelve full correction tiles or target-clock tiles for a place. Worker caches retain their existing limits.

The real Worker route and production browser decoder matched the direct loader on all 397 frozen validation coordinates: thirteen city points and 384 sampled land pixels. Across seventeen periods and two tastes, all 2,522,935 compared numeric values were identical, including scores, day counts, weather arrays and float64 corrections. The inactive-pointer and bad-coordinate paths also passed.

Measured compressed packets were 12,846–16,629 bytes, median 14,907 bytes; all 397 totalled 5,872,620 bytes. These are actual route response sizes on this Mac. The final rendered-app traffic check is separate; these packet measurements do not claim remote Worker latency.

Rerun:

```sh
devenv shell -- node research/fine/b1_transport_proof.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/overlay' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/queries.json' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/transport.json'
```
