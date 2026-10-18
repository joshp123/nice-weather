Written by AI (GPT-6.1 Sol), 2026-10-18.

# Compact named-place upload handoff

**The compact B1 data are live.** The original upload is recorded in [the release report](../site/named-live-deploy-20261018.md). The 18.54 GB / 484,004-file worldwide replay remains on hold. Do not run the full-release sealer or uploader.

The compact delivery covers **170,513 distinct catalogue coordinates**, including every label and every place-list entry. At the default taste, supported named places use their accepted 1 km temperature model: unchanged daytime correction and the uniform evening shift. There are 31 unsupported climate coordinates; those, unnamed areas outside the place-naming rule and changed tastes use the current 25 km calculation. The map remains the 25 km overview. A click named after a town or city uses that place’s canonical coordinate and numbers, under the same existing naming rule. Other cards identify the fallback as “Area estimate”.

## Data to upload

All folders are under `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/`.

| Local folder or file | Storage destination | Files | Bytes |
| --- | --- | ---: | ---: |
| `fine-named-upload-b1-20261018/v6-named-b1-20261018/` | Same immutable prefix | 18 | 101,945,036 |
| `fine-named-upload-b1-20261018/v6/forecast/20261017T0000Z/named.bin.gz` | Same forecast-run key | 1 | 15,783,945 |
| `fine-named-upload-b1-20261018-activation/v6/named/current.json` | `v6/named/current.json`, **last** | 1 | 282 |
| `fine-named-upload-b1-20261018-rollback/v6/named/current.json` | Rollback only; literal `null` | 1 | 5 |

The **19 payload objects total 117,728,981 bytes (117.7 MB)**. Twelve score/facts objects are 101,053,569 bytes: 11,207,848 bytes of climate scores and 89,845,721 bytes of monthly card facts. The rest is two label catalogues and their notices, source attribution, the public manifest, and the forecast table. The sibling `fine-named-upload-b1-20261018.MANIFEST.json` lists exact keys, bytes and SHA-256 values. It is an operator inventory, outside the upload tree. Private `coordinates.json`, station observations, validation queries and the source replay are excluded.

This exceeds the original “tens of MB plus a few MB” aim. Full-precision facts account for most of it. The climate sample scores are one byte each; the forecast retains canonical scores, fractional nice/perfect/awful shares and the existing full-fortnight judgement. Those extra forecast fields preserve the app's spatially blended counts and typical-day calculation. No model or fact precision was changed to obtain the size reduction. The data upload is more than 150 times smaller than the held worldwide release.

The view-scoped transport replaces whole-table browser downloads. The browser posts exact coordinates to `/api/named/points` for the visible labels and existing headline-ranking candidates, receiving only their original packed day records. Climate views request climate rows; forecast views request forecast rows. The Worker reads the existing full storage tables, and the browser caches requested rows for instant stretch changes. Selected cards fetch their facts separately. The world map and its day-record path are unchanged; changed tastes use the overview without new named requests.

The rebuilt daily runner is `fine-runner-named-b1-20261018/runner/`: **5 files, 35,416,693 bytes**. Its sibling manifest binds the executable, notices, refresh instructions and compiled source inputs.

The matched Sites package is `fine-named-code-b1-20261018/`: **18 files, 5,307,019 bytes**. The separately staged outlooks are `fine-named-outlooks-b1-20261018/`: **9 files, 1,502,098 bytes**, for forecast run `20261017T0000Z` and **19 October 2026**. Their nine winners pass 36 comparisons with the shared compact values. Each folder has a sibling checksum inventory.

## View-scoped transport update

Published on 2026-10-18 as Sites version 21, preserving public access. This was a code-only update: no new weather files, pointer changes or runner replacement. The source/archive receipt is `deployments/named-viewport-20261018/deployment.json` on the data volume. Measurements and rerun instructions are in [NAMED_VIEW_LOADING.md](NAMED_VIEW_LOADING.md). Keep the existing named activation and forecast pointers.

1. Read the live Site and source revision, preserving its public audience, `WEATHER` binding and latest interface.
2. Build the committed app with `devenv shell -- npm run build`. Package that build with `hosting/sites/build.mjs` into the Sites source checkout opened by the Sites skill.
3. Commit and synchronize that exact generated source through the Sites workflow helper, package its `dist`, save that source revision and deploy the saved version. Supply its short-lived source credential through hidden stdin only at use time. This update does not need the weather publisher token.
4. Repeat the headless WebKit phone/laptop cold-view measurements and the named-place correctness checks. Confirm the browser makes no requests for whole `climate.bin.gz` or `named.bin.gz` tables.
5. On a product regression, redeploy the preceding saved Site version; the weather data and activation remain compatible with that version.

The original data-upload order below is retained for rebuilding B1, rather than repeated for this code-only update.

## Original compact-data upload order

Keep `v6/fine/current.json` absent. This release activates only through `v6/named/current.json`; the old full-replay activation key is not used.

1. **Check the forecast run.** The prepared forecast table belongs to `20261017T0000Z`. Read `/data/v6/forecast/current.json` immediately before uploading. If it names another run, regenerate `named.bin.gz` for that run with the rebuilt runner; do not change the live forecast pointer to match this old table. Climate data do not expire with the forecast.
2. **Upload immutable named data and the matching current-run table.** Existing code ignores them until activation. Supply the existing owner publisher credential privately through `NICE_WEATHER_PUBLISH_TOKEN`; do not put it in a command or file.

   ```sh
   NWF_DATA='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-named-upload-b1-20261018" https://nice.jjpcodes.com \
     v6-named-b1-20261018 v6/forecast/20261017T0000Z/named.bin.gz
   ```

3. **Wait for tonight’s parallel deployment to finish, then deploy the matching code package** from `fine-named-code-b1-20261018/` using the existing Sites source/deployment workflow in [hosting/sites/README.md](../../hosting/sites/README.md). Include the embedded app and `named-place.mjs` beside both Worker entry points, preserving the existing `WEATHER` binding and public audience. Build on the latest interface commits; do not restore an older interface. While the named pointer is absent, cards, labels and headlines continue to use today's numbers.
4. **Upload the rebuilt daily runner and its notices.** The unchanged daily command now builds all named forecast judgements before the outlooks. It uploads `named.bin.gz` with the complete run before switching `v6/forecast/current.json`. Tomorrow's app, previews and outlooks therefore read the same table. See [hosting/sites/refresh.md](../../hosting/sites/refresh.md). The nine staged outlooks can be uploaded only while that forecast run and tomorrow’s UTC date still match. Otherwise regenerate them with this runner; do not reuse a dated poster bundle blindly.
   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-runner-named-b1-20261018" https://nice.jjpcodes.com runner
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-named-outlooks-b1-20261018" https://nice.jjpcodes.com \
     v6/forecast/20261017T0000Z/outlook
   ```

5. **Read back staged objects** and compare sizes/checksums with the inventory. Check `/api/named` reports disabled, inactive cards still work, and the new Worker serves the named data files.
6. **Activate last** by uploading the separate pointer folder:

   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-named-upload-b1-20261018-activation" https://nice.jjpcodes.com \
     v6/named/current.json
   ```

   Reload after the sixty-second configuration/API cache lifetime. Check San Francisco, Funchal and San Diego cards, visible map labels, headlines and link previews. Check a nearby San Francisco click matches search (75 / 27 nice / 8 perfect days in July). Check a genuinely unnamed area and a changed taste both give working area estimates. Existing tabs need a reload to discover activation.

Rollback requires no deletion: upload the separate rollback folder's `v6/named/current.json` (literal `null`) through the same publisher. After cache expiry and reload, the entire named-place overlay is inactive and the current 25 km path is restored. Never upload the rollback folder during activation.

## Rebuild without a global replay upload

The source replay is read locally only. Recompute compact climate scores whenever the assembled chunk set changes; the accepted temperature anchors and their period adjustment must change together. Current data represent the five assembled chunk years `76,78,80,82,84`, 1,800 scored days; they are not a continuous 25-year record.

```sh
NWF_DATA='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
devenv shell -- node research/fine/build_named_places.mjs \
  --data "$NWF_DATA" \
  --pointer "$NWF_DATA/fine-validation/b1-worldwide/overlay/v6/fine/current.json" \
  --places artifacts/places.tsv \
  --output "$NWF_DATA/v6-named-b1-20261018" --workers 4
```

The four-worker climate build took 112 seconds on this Mac. `prepare_named_delivery.mjs` verifies the builder's checksums, copies only the public objects and prepares separate activation/rollback pointers. It does not publish anything:

```sh
devenv shell -- node research/fine/prepare_named_delivery.mjs \
  --data "$NWF_DATA" --output "$NWF_DATA/fine-named-upload-b1-20261018" \
  --forecast "$NWF_DATA/fine-named-forecast-b1-20261018/v6/forecast/20261017T0000Z/named.bin.gz" \
  --run 20261017T0000Z
```

## Proof and limits

The shared JavaScript decoder/aggregate is used by the app and Worker. The Go daily writer computes the identical forecast model once; outlook ranking then reads the same compact table. The 397-place proof includes the thirteen cities and 384 seeded random land coordinates. Random points are included only in a private validation table; they do not become publicly eligible named places.

- **Climate:** 26,996 score/count comparisons across all months, seasons and year are exactly equal to the full 1 km replay.
- **Forecast:** all fourteen days and the total agree with the Go port and current Worker. Canonical scores are exact; maximum raw count difference from float32 storage is 0.000000413 days and every visible day count is identical. Unavailable cases agree.
- **Facts:** maximum numerical difference is 0.000003815. All thirteen cities' displayed facts agree. Forty-six fields at random validation points round differently at numerical ties, such as 12.499999999999998% versus 12.5%; these are enumerated in the validation report. Scores and day counts are unaffected.
- The actual Worker API, climate/forecast preview PNGs, facts endpoint, unnamed fallback and warm removal of the activation pointer are exercised independently of the browser.
- Rendered phone/laptop checks cover cards, actual map-label updates, headline ranking, changed taste, forecast, and genuinely absent activation data. Paired warm slider redraw medians are 31.25 ms active versus 31.40 ms inactive, with zero requests/errors. The first transition is slower (32.3 versus 21.6 ms); the earlier eight-view cold-start gap remains recorded. Full timings and screenshots are recorded in [NAMED_DELIVERY.md](NAMED_DELIVERY.md).

The airport reference coordinates in the numerical validation are not added as special catalogue entries. Public clicks follow the same place-naming and coordinate rule everywhere; named Arona and Adeje retain their own fine rows. Existing places outside the overview land coverage retain its existing unavailable state.

An independent review checked the final phone/laptop images, current Worker cards and label routes, the parity evidence and the paired slider measurements. The click-to-place change is covered by a fresh rerun of the original eight views plus a remote unnamed land point, in both data states. The earlier paired slider measurements remain recorded as a reference; this fix changes no engine or scoring code.

The accepted station comparison and Rotterdam's May regression remain in [B1_EVENING_RESULT.md](B1_EVENING_RESULT.md). Named scores do not improve the surrounding overview or non-temperature weather variables. Custom-taste link previews use neutral place/period text rather than advertising the default score.

Source licences and attribution accompany the payload. [SITES_STORAGE.md](SITES_STORAGE.md) records the official current terms: hosting is included for Plus/Pro, with shared storage/usage limits and no published numerical allowance. This smaller preparation does not establish unlimited storage or approve spending. No capacity test or upload was performed. B2 remains deferred.
