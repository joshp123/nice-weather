Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Named weather now travels as the requested places' existing day records, through `POST /api/named/points`. The browser asks for the visible labels, the existing headline candidates and the selected card. It fetches climate or forecast records only for the selected period. The Worker still reads the published whole table; the browser no longer downloads either whole table.

The live winter page's named score/facts download fell from **27.03 MB to 164 KB**, with the same temperature model, day scores and counts. Including the unchanged place-name catalogues (385 KB), all named data is **0.55 MB per view**. The remaining roughly 14 MB includes the unchanged day records, map, application and fonts. No data objects, runner or correction tiles changed for this release.

The headline waits for the complete label catalogue before ranking. An intermediate live check exposed a brief ranking of the first catalogue alone; that was a partial fine ranking, not evidence of an area estimate. The final release shows one final ranked winter headline on each cold view. A named card waits for its named facts instead of briefly displaying the area estimate. The existing map image can still paint immediately.

## Live headless WebKit, before and after

Same Mac, same live `/nov-mar` URL, fresh browser context for each view, light theme, laptop 1512 × 982 and phone viewport 390 × 844. These are one cold sample per size before and after, without throttling; timings include changing public-network/CDN latency. This is not a physical-phone benchmark.

| View | State | Completed requests | Total response bodies | Named response bodies | First ranked headline | Settled headline | First scored labels | Settled label source |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| Laptop | Before | 40 | 41,045,784 B | 27,027,180 B | 7.237 s | 11.896 s | 3.678 s | 11.895 s |
| Laptop | Live after | 42 | 14,183,609 B | 164,044 B | 3.016 s | 3.016 s | 2.284 s | 4.042 s |
| Phone | Before | 40 | 41,045,562 B | 27,027,126 B | 3.597 s | 6.818 s | 1.400 s | 6.817 s |
| Phone | Live after | 42 | 14,183,305 B | 163,616 B | 2.233 s | 2.233 s | 0.991 s | 3.091 s |

First scored labels arrived **30 ms after the overview score image response on laptop, 28 ms on phone**. The overview image response finished at 2.254 / 0.963 s after, compared with 3.653 / 1.382 s before. This measures response availability, not the exact GPU paint. The final whole-world headline needs the complete ranking candidates and arrives later; no provisional ranking is shown.

The previous page fetched both `climate.bin.gz` (11.22 MB as WebKit reports it) and `forecast/.../named.bin.gz` (15.80 MB), even on a winter climate URL. The new request list contains neither whole table. The two extra completed requests buy a **99.4% reduction in named response bytes** and **65.4% reduction in total page response bytes** for these views.

The named column above counts score/facts responses. The two unchanged name-catalogue responses add 385,287 B on laptop and 385,280 B on phone: **549,331 / 548,896 B combined named data**, both below 1 MB.

## How this is measured

`named_view_webkit.py` uses installed headless Playwright WebKit. `request.sizes().responseBodySize` supplies encoded HTTP response-body bytes: gzip stays compressed. Totals include cross-origin fonts and map objects, but exclude request/response headers, TLS and packet overhead. WebKit's accounting can differ slightly from the raw gzip file size. Resource Timing is retained as a cross-check; cross-origin resources without timing permission can report zero there, so it is not the total-byte authority.

An observer records actual headline DOM text changes and the `city-scores` GeoJSON sent to MapLibre's worker. First scored labels excludes the earlier names-only layer. Settled label time is the last source update, a conservative upper bound that can include offscreen features; it does not claim an exact screen-paint timestamp. The harness waits for three quiet seconds and saves full request lists, statuses, timings, text transitions and screenshots. Ordinary canceled OpenFreeMap tile requests during the camera move are recorded separately. Named/API failures and runtime errors still fail the check.

The local WebKit fixture uses the actual Worker and files without opening a server. Its route bridge decompresses gzip for browser fulfillment, so local fixture transfer figures are not used in the live table above. The raw encoded Worker response sizes are retained separately.

## Correctness and fallbacks

The final built app passed the original eight correctness views plus the remote-area view, both with the named data active and with its pointer absent: **18 views, zero runtime exceptions and zero requests caused by the taste change**. These checks compare the real card with the Worker or unchanged overview model, compare actual rendered map labels with the same values, independently rank the world headline, and then change the taste to exercise the area fallback.

The views are world July; Funchal July; San Francisco July on phone and laptop; San Diego July; a nearby San Francisco click on phone; a remote Australian interior point on phone; and San Francisco forecast on phone and laptop. Local WebKit also checked the winter view at both sizes and the San Francisco card at both sizes. The final cold winter check has one ranked headline at both sizes; San Francisco's cold card shows a loading state followed by 75, without the old 98 area estimate.

The final live WebKit run passed **all 11 cold views**: the two winter performance views and all nine correctness views, with 71 label comparisons and every selected card matching the exact local reference. It recorded zero runtime/API/network errors. Five ordinary canceled OpenFreeMap tile requests during camera movement are retained separately. Across all 11 views, named score/facts responses peaked at 242,979 B; including name catalogues, named data peaked at **628,221 B**. No view fetched either whole named table.

The cold San Francisco phone, laptop and nearby-click card timelines each contain loading placeholders followed by 75, with no 98 or area-estimate card in between. Expected cards are:

| Place / period | Score | Nice days | Perfect | Awful |
|---|---:|---:|---:|---:|
| San Francisco July, search and nearby click | 75 | 27 | 8 | 0 |
| Funchal July | 100 | 31 | 31 | 0 |
| San Diego July | 100 | 31 | 31 | 0 |
| Remote point −25, 135, July | 40 | 12 | 4 | 8 |
| San Francisco next 14 days, current forecast | 43 | 6 | 3 | 6 |

The remote point keeps its coordinates and area estimate. A changed taste keeps the existing 25 km model and instant redraw path. No score, high-temperature or evening-temperature correction changed.

The final production bundle is `main-B9tBEs38.js`, from the clean product checkout at `a298e8f`. Sites deployment 21 succeeded at 2026-10-18 01:07:39 UTC. The final live request lists confirm that bundle at both sizes. The deployment receipt is `deployments/named-viewport-20261018/deployment.json` on the SSD. No data objects were uploaded for this change.

## Evidence and rerun

Evidence root: `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-view-transfer/`.

- `before/`: original live WebKit cold phone/laptop winter requests, timelines and screenshots.
- `final-local-active/`, `final-local-inactive/`: exact nine-view assertions for the final bundle in both data states.
- `final-local-webkit/`: final local winter single-headline checks.
- `local-webkit/`: local San Francisco phone/laptop cards, screenshots and full timelines before the headline-only guard.
- `after-live-final/`: final live cold winter and nine correctness views.
- `after-live/`: retained intermediate live run, including the partial-catalogue headline that prompted the final guard. Its Funchal numerical assertions passed; its old harness classified two normal tile cancellations as failures.

Run from the repository's devenv (Playwright is already cached):

```sh
devenv shell -- sh -c 'cd /tmp && UV_CACHE_DIR=/tmp/nwf-uv-cache uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/research/fine/named_view_webkit.py --out /tmp/named-view-live --correctness --reference "/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/named-view-transfer/final-local-active/results.json"'
```

The exact local proof is `named_app_proof.mjs OUTPUT OVERLAY "" active|inactive`, run from the clean built checkout. Its screenshots and full source hashes stay with the results. The browser bridge is observational; it does not change the published bundle.
