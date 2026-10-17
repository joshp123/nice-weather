Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Map and card use one field

The discrepancy had three causes. The default map permanently preferred older mean-score PNGs over its correctly recounted typical-day GPU grid. Its final 4 × 4 B-spline also mixed neighbouring land values, while the card selected one nearest land point. The new exact-place path would add a third source unless its native pixel also appeared in the map.

The live grid now replaces the preview as soon as records load. Preview filenames explicitly include `typical-day`, so older stored mean-score images cannot be selected. The map, named scores, counts and overview card apply the same normalized spatial footprint. Every native grid point first uses the unchanged temporal typical-day rule. The card combines the full native verdicts, including the physical figures and counts, with those spatial weights. It does not replace only a score while leaving unrelated nearest-point facts.

When exact fine-place data are active, the shared `placeNumbers` result is authoritative for both the card and its actual containing 30 arc-second geographic cell. Loaded cells form one batched weather draw below the basemap water. The surrounding overview is still overview data. No marker, radius or invented smoothing pretends to provide exact values for unloaded cells.

## Numeric check

`check-map-card.py` instruments the running production engine only in a headless browser route. It takes the actual linked map-fragment source and exact readback of the live resolved GPU texture, compiles that fragment in an isolated GPU context, and reads its final spatially filtered score at each requested coordinate. This checks the final shader value rather than the unfiltered grid that already agreed before the fix. The other value is `judgePoint(...).meanScore`, the same verdict supplied to the card.

Thirty places, including all eight named by Josh, were checked for November to March, the whole year, July and the next fourteen days. Both browsers passed all 120 comparisons with the same band after rounding:

| Browser | Comparisons | Largest score difference, out of 100 |
|---|---:|---:|
| Chromium | 120 | 0.0004514161 |
| WebKit | 120 | 0.0004437867 |

The saved rows are `chromium.csv` and `webkit.csv`. For example, Chania in November to March now has a card score of 38.2317 and a map-fragment score of 38.2316666, both Poor. The previous nearest-point card was 31.338; the final filtered shader was 37.77314 when its constituent default grid scores were floored. Fixing this changes the card to describe the actual displayed field. It preserves the native temporal rule and does not claim the old nearest-point value was the same spatial sample.

A separate check read the actual composed map pixel at Chania's canonical named point, 35.51124 N, 24.02921 E. Its card score was 38.4989, Poor, and the map pixel was Poor red, RGBA 157/112/115/255, with live records active. The selection ring hides the narrow coastal strip at zoom 5. About 30 km south, the score reaches 56.22, Nice, explaining the neighbouring blue area. The unlabelled screenshot and pixel samples are in `/tmp/nwf-agents/round5/build-followup/chania-pixel/`.

## Exact pixel check

`make-fine-fixture.mjs` reads the actual sealed data from the SSD and writes the production binary transport format for those same thirty places. `check-fine-field.py` serves those bytes through a headless route, runs the production browser client, requests the real exact card verdicts, then reads the actual uploaded batched vertex buffer and runs its production fine-cell vertex/fragment shaders. It verifies that each cell contains the requested place and reads the cell's value at its centre. Sampling the interior avoids GPU edge rasterisation of an exactly-on-boundary coordinate; it does not change the geographic pixel or the card's value.

Chromium and WebKit each passed all 90 exact-pixel climate comparisons, with maximum difference 0.0000036622 out of 100. The next fourteen days use the existing forecast field and are covered by the first 120 comparisons. See `fine-chromium.csv` and `fine-webkit.csv`. No browser JavaScript errors occurred. This is headless browser verification on the Mac, not physical-phone performance proof.

## Redraw performance

After the final interface changes, actual pointer drags and 24 measured input changes across all six sliders gave these full input-to-painted-interface times in headless WebKit on this Mac. The phone row is viewport emulation, not physical-phone hardware.

| Viewport | Input to paint | Median | 95th percentile | GPU engine | First weather frame |
|---|---:|---:|---:|---:|---:|
| 1512 × 982 | 70–116 ms | 81 ms | 98 ms | 17–31 ms | 354 ms |
| 390 × 844 | 52–81 ms | 64 ms | 73 ms | 17–33 ms | 155 ms |

No JavaScript errors occurred. Raw samples are in `performance.csv`; the full JSON is in the temporary proof folder. The sixteen-neighbour sampling is not causing a redraw regression in this local check.

## Repeat

Build and serve the app on port 5198. Run the checks with the user-required headless Playwright environment:

```sh
cd /tmp
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/research/site/map-card/check-map-card.py --out /tmp/nwf-agents/round5/build-followup --browser chromium
```

Use `--browser webkit` for WebKit. Generate fine fixtures from the unchanged sealed data root with `node research/site/map-card/make-fine-fixture.mjs <data-root> <output-folder>`, then run `check-fine-field.py --fixture <output-folder> --out <proof-folder>` through the same headless Playwright command. Fixture packets and JSON summaries remain in `/tmp/nwf-agents/round5/build-followup/`; the durable numeric rows and repeatable scripts are kept here in Git.
