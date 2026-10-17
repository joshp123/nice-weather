Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Interface follow-up

The app uses a full-bleed map. The exported poster crops that same map into its paper frame, preserving the geography and the current headline. The list starts collapsed. A short home and regional-city comparison sits under the headline, with the van suggestion in the opened list. App-picked places use the outlook's naming rule: the largest place within 50 km and five score points, using that named place's own coordinates and numbers. The viewport determines both the answer's candidates and its region; the headline, browser title and poster share the same sentence.

`src/design-tokens.css` remains the single place to change design values. Browser position is requested only by My location. The returned coordinates remain the selected point and its share link after reverse naming.

Run `screenshots.py` in the user-required headless Playwright environment with the built app served at port 5198. It writes the five viewport sizes in both palettes, including the open list and the actual exported PNG, to `/tmp/nwf-agents/round5/build-followup/shots/`.

`interactions.py` exercises headline links, the collapsed list, the dial and shortcuts, Play the year, the day calendar, all six sliders, an opted-in browser position, search, poster creation and a real PNG clipboard write/read. Screenshot and interaction reports are saved beside the shots. The final redraw timings and numeric map-card checks are in `../map-card/`.

The final headless WebKit matrix captured 80 interface screenshots and 10 exported poster PNGs at 390 × 844, 1512 × 982, 1728 × 1117, 1920 × 1080 and 3840 × 1080, in light and dark. No browser errors, failed responses or measured panel overflow occurred. Contact sheets and the actual poster exports were visually inspected. The Chromium interaction pass completed all paths without errors and read back a 427,665-byte PNG from the clipboard. Geolocation used a browser-supplied off-centre Sydney position; this verifies the opt-in API path, not a physical GPS device. An independent final check confirmed descending counts after nearby-city naming and matching first-three headline names.

The map-card numerical results and repeatable checks are in `../map-card/`. The 148 new typical-day preview PNGs were regenerated locally in the existing `artifacts/v6` data location. Their producers, readers and forecast publisher now use explicit `scores-typical-day-*` filenames. Publishing those new assets is required with a future authorised deployment. This change has not uploaded assets, pushed Git or deployed the site.
