# Cold-review fixes

Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The headline and list now rank the labels actually drawn in the visible map, using their displayed number, then perfect days, typical-day score and population. Rows show nice and perfect counts. A region is named only when its extent is in view; other views say “on this map”. Flags and place buttons remain interactive.

Cards count actual daily scores after spatial interpolation. Forecast totals and calendars include the same 14 dated days. Typical-year counts explicitly name the sample: 8 days per month, 40 for November to March, 96 for the year. The separate calendar-weighted typical-day score is unchanged. The 30-place, four-stretch check passed all 120 comparisons, including unchanged scores and bands; see `../cold-review-counts/`.

Phone cards and lists replace the dock and occupy at most half the screen. The selected pin stays above the card. Paint containment prevents Chromium's scrolling sheets from covering the map. Forecast time and 44 px controls remain visible. The ultrawide header is a 1,350 px corner panel. Search focus, credits and the legend fit their surfaces.

Link previews, Copy image and Make a poster use the same clean 1200 × 630 template. Home and 14-day place previews work. Dated links use the calendar date throughout the UI, API and outlook links. Old relative `day-N` links are not supported. Direct fresh dated links were checked, as well as clicks from loaded cards. Unknown place URLs show a plain 404 message. The method page and card copy use visitor-facing wording.

The weather layer retains the last complete colours and labels until the replacement is ready. World edges use the same smoothing as closer views. World tiles are capped at zoom 1; extra coast and state detail begins at zoom 3. See `../cold-review/` for the delayed-record slider experiment and cold-byte measurements. No scoring curves, bands or weather data files changed.

## Verification

- `screenshots.py`: headless WebKit, 48 screenshots covering first view, November to March, list, dial, place card, forecast calendar, taste and poster at 390 × 844, 1512 × 982 and 3840 × 1080, light and dark. No page errors. All calendars contain 14 dates.
- `interactions.py`: headless Chromium at the same three sizes. Visible-label ranking, nice/perfect list counts, fresh dated Cairo links, search and Palestine naming, rounded device position before requests, method copy and unknown-place 404. Twelve check groups passed, no page errors.
- Independent cold recheck: fresh Chromium dated Chania and Cairo links, phone card pin position and phone list, plus earlier WebKit checks. No remaining blocker found in the requested scope.
- Sharing proof: nine metadata/image URLs return 200, and real clipboard PNG pixels equal the poster PNG at all three sizes. `../sharing/proof.py` and `/tmp/nwf-agents/round5/sharing/report.json`.
- Cold bytes at 1728 × 1117, 30 seconds, empty Chromium context: 9,122,697 before versus 3,317,904 after (63.6% less); base-map tiles 6,252,817 versus 448,024 bytes (92.8% less). Both runs use the same final frontend and active named-place data. The before run restores only the former zoom-2 world-tile policy in the browser response. The after run uses the production zoom-1 policy. Totals are local uncompressed response bytes, not production transfer timings. Full requests are in `map-before-final.json` and `map-after-final.json`.
- Production frontend build and local Sites package build pass. The packaged Worker imports and serves successfully. Nothing was deployed or pushed.

Screenshots: `/tmp/nwf-agents/round5/cold-fixes/shots/`.
Browser reports: `screenshots.json` and `interactions.json` beside this file.
Independent review: `cold-review.md` beside this file; inspected screenshots are in `/tmp/nwf-agents/round5/cold-fixes/review/`.

## Remaining limits

The scoring concerns and longer historical record were left to Josh, as requested. Custom-taste links still omit a preview image because the Worker has no matching precomputed map for that taste; their browser Copy image and poster work. Existing stored outlook PNGs retain their old artwork until the normal forecast refresh regenerates them. The outlook page and image generator now use the shared Nice! wordmark. Stored outlook images were not regenerated because the data pipeline was outside this change.

The redraw experiment uses headless software graphics and a deliberately delayed response. It proves that loading no longer blanks the map; it is not a physical iPhone or M-series GPU timing.
