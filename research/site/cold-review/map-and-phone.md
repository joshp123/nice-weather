# Map and phone follow-up

Written by AI on 2026.10.18 by GPT-6.1 Sol.

The scalar weather field, scoring curves and band thresholds are unchanged. The GPU keeps its last complete texture while daily records load for a new taste, then draws the replacement in the same render pass. The header/list retain the matching label layer until that replacement is ready. Contour antialiasing uses the same cubic B-spline at every zoom, with a 1.75 screen-pixel edge width set in the design tokens.

At world zoom the only base-map layers are the visible water mask and national borders, using zoom 1 vector tiles. Detailed coastlines and state borders begin at zoom 3. The app's own city labels remain independent of those tiles.

The phone card and time picker replace the dock and occupy at most half the screen. Controls have 44 px targets; month buttons use two rows on phones. Search focus follows the rounded search surface. The map credit button sits above the dock and opens a 44 px circle. The 3840 px header is capped at 1350 px, with its existing 1.5× type and spacing.

## Cold bytes

Headless Chromium, empty context, disabled HTTP cache, 1728 × 1117, light, Europe/Amsterdam home, 30 seconds from navigation. Both measurements use the same active named-place overlay, with no data changes. The baseline browser restores the old zoom 2 world-tile policy by intercepting the built JavaScript; the after run uses the shipped zoom 1 source. This isolates the tile-policy change while keeping the current card/count/preview integration. The two builds differ by 149 bytes of application code from concurrent headline edits.

`map-bytes.py` captures response bodies as well as headers, including requests made by MapLibre's dedicated workers. Reported bytes use Content-Length where supplied, body size otherwise. They exclude local blob URLs and HTTP overhead. Local server assets are uncompressed; production Sites compression will alter absolute totals.

| | Before | After |
|---|---:|---:|
| Network response bytes | 9,119,538 | 3,314,894 |
| Base-map tile bytes | 6,252,817 | 448,024 |
| Base-map tile requests | 12 at zoom 2 | 4 at zoom 1 |

Network bytes fall 63.6%; map tiles fall 92.8%. Captures: `/tmp/nwf-agents/round5/cold-fixes/map-before-active.png` and `map-after-active.png`. The original pre-edit source capture is retained separately as `map-before.json` in the same temporary evidence directory.

## Taste retention

`taste-retention.py` delays the climate records by three seconds, then moves the first real taste slider. It captures the preserved WebGL canvas before, during and after loading. At 390, 1512 and 3840 px the coloured-pixel count is exactly unchanged at every captured frame through 1.5 seconds, then changes when the completed layer replaces it. The three contexts report no page errors. This proves the map stays painted during a slow load, not merely that a fast redraw hid the blank interval. It does not measure physical iPhone performance.

| Viewport | Before | At 1.5 seconds | After replacement |
|---|---:|---:|---:|
| 390 × 844 | 82,273 | 82,273 | 83,139 |
| 1512 × 982 | 530,846 | 530,846 | 524,439 |
| 3840 × 1080 | 877,855 | 877,855 | 861,561 |

Full frame paths and timings are in `taste-retention.json`. Screenshots use `/tmp/nwf-agents/round5/cold-fixes/taste-{before,0.1,0.5,1.5,4.5}-{width}x{height}-light.png`.
