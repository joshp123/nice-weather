Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# B1 upload handoff

**The worldwide data are complete and audited. The app is not ready to upload.** Do not upload the private validation pointer. The release remains unsealed until the phone-card fix below passes the final rendered check.

The final active-fine San Francisco phone check caught a map-label query racing a GeoJSON update. Its weather requests succeeded, but the card displayed a missing-weather error. The reviewed fix is [FINE_LABEL_QUERY.patch](FINE_LABEL_QUERY.patch), with the evidence in [FINE_LABEL_QUERY.md](FINE_LABEL_QUERY.md). It remains unapplied pending approval because it touches `src/engine/weatherEngine.ts`, outside Josh's permitted module, Worker hook and outlook-ranking paths. The prepared code package must be rebuilt and pass the rendered check after that fix; its present size is provisional.

The accepted release keeps the daytime correction unchanged and uses one uniform evening shift everywhere. Rotterdam's May regression is accepted and remains in [the comparison](B1_EVENING_RESULT.md). The 1 km data affect named places; the surrounding overview retains its existing resolution. Forecast weather remains ECMWF, with the redesigned app's spatial sampling shared by the Worker and Go outlook.

## Prepared files

All folders below are directly under `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/`.

| Folder | Contents | Bytes |
|---|---|---:|
| `v6-fine-b1-20261018/temperature/` | 79,596 monthly correction tiles; source/support maps, reference normals and notices | 2,727,800,868 |
| `v6-fine-b1-20261018/labels/` | Two full-precision label catalogues, manifest and GeoNames attribution | 882,594 |
| `v6-fine-b1-20261018/replay/` | 397,686 own-native-point/clock packets, lookup, index and notices | 15,815,945,109 |
| `v6-fine-b1-20261018/ATTRIBUTION.txt` | Combined source and derived-data notices | 4,889 |
| `fine-code-b1-20261018/` | Matched Sites Worker and embedded app | 5,249,649 (final rendered check pending) |
| `fine-runner-b1-20261018/runner/` | Linux executable and four notices/instructions | 35,379,565 |
| `fine-previews-b1-20261018/v6/` | 133 climate and 15 forecast preview PNGs; three outlooks, each JSON/PNG/full PNG | 15,241,936 |
| `fine-activation-b1-20261018/v6/fine/current.json` | Activation pointer, uploaded last | Awaiting seal |
| `fine-rollback-b1-20261018/v6/fine/current.json` | Stored-null pointer; rollback only, never activation | 5 |

The preview bundle currently describes forecast run `20261017T0000Z` and outlook date `2026-10-18`. If the forecast run or tomorrow's UTC date changes before upload, regenerate that small bundle using the instructions below. Its `MANIFEST.json` lists every exact storage key, byte count and SHA-256. The runner manifest also binds its compile and embedded inputs.

The unsealed immutable data folder contains **484,004 regular files, 18,544,633,460 bytes (18.54 GB)**. It contains no symlinks or partial files. Sealing adds the root `RELEASE.json`; record that final uploader dry-run total before publishing. The component audits bind the delivered manifests and packet checksums.

Only the immutable release folder belongs under the storage prefix `v6-fine-b1-20261018/`. Private `fine-validation`, `fine-build`, `fine-sources`, source downloads and station observations are not upload inputs. No `cells.bin`, `points.bin`, weather records, offsets, facts, `scores.bin` or forecast pointer is replaced by this handoff. The matching interface preview images use new `scores-typical-day-*` keys.

## Finish before upload

After approval, apply the reviewed engine patch, rebuild the app and Sites package, rerun the packaged four-view browser check, and inspect its default screenshots. Keep the interface worker's latest commits. The final check must overwrite `fine-validation/b1-worldwide/app-complete/results.json` with a passing report bound to the actual packaged sources; the current report correctly records failure.

Then seal the already audited data and create the separate activation and rollback files:

```sh
NWF_DATA='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh prepare_b1_release.py \
  --data-root "$NWF_DATA" --release v6-fine-b1-20261018 \
  --validation "$NWF_DATA/fine-validation/b1-worldwide" \
  --activation "$NWF_DATA/fine-activation-b1-20261018" \
  --rollback "$NWF_DATA/fine-rollback-b1-20261018"
```

The sealer refuses the present failing app proof. Do not bypass it or publish the private local-building pointer. Update the provisional code size and activation size above, refresh the forecast-dependent bundle if needed, and record the actual dry-run total below.

## Upload order

These are operator instructions for a later authorized deployment. Nothing has been uploaded or deployed by this task. Use the existing owner publisher credential through `NICE_WEATHER_PUBLISH_TOKEN`; a forecast-only refresh grant cannot upload the fine release. Keep credentials out of commands, files and logs.

1. **Upload the complete immutable release.** Its root `RELEASE.json` must say `ready`. The uploader is resumable, records success only after HTTP 201 and cannot activate the separate pointer. Run its dry run first; the final measured total will be recorded below.

   ```sh
   NWF_DATA='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
   devenv shell -- node research/fine/upload_b1.mjs \
     --root "$NWF_DATA" --release v6-fine-b1-20261018 \
     --site https://nice.jjpcodes.com \
     --journal "$NWF_DATA/fine-upload-b1-20261018.jsonl" --workers 24 --dry-run
   ```

   For the actual upload, remove `--dry-run` after the existing secret workflow supplies the environment variable. Rerun with the same journal to resume.

2. **Upload the matching preview bundle before the app code.** The new score-image keys do not replace the old mean-score PNGs. The bundle also refreshes the nine current outlook files. Check that `/data/v6/forecast/current.json` still names the prepared run. Upload only this prepared directory; it contains no forecast pointer:

   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-previews-b1-20261018" https://nice.jjpcodes.com v6
   ```

3. **Deploy the prepared Sites code package** from `fine-code-b1-20261018/` to the existing Site at `https://nice.jjpcodes.com`, preserving its current `WEATHER` binding and public audience. Include all shared modules and the embedded Vite build, not just `worker/index.js`. Use the existing Sites source/deployment flow described in [hosting/sites/README.md](../../hosting/sites/README.md). Leave `v6/fine/current.json` absent until the last step. The new code supports that inactive state.

4. **Upload the runner.** The next daily refresh will preserve the shared rule. Runner and outlook keys have a sixty-second cache lifetime.

   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-runner-b1-20261018" https://nice.jjpcodes.com runner
   ```

5. **Check the staged release before activation.** Read back `RELEASE.json`, both component manifests, label files and representative native/correction files. Verify their generation and checksums against the local inventories. Confirm the updated app, `/api/fine/replay`, `/api/fine/labels-1.json` and the three `/outlook/<region>` pages load while fine mode is inactive. Keep the previous activation pointer, if any, for rollback.

6. **Upload the one activation key last**, from its separate root:

   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-activation-b1-20261018" https://nice.jjpcodes.com \
     v6/fine/current.json
   ```

   After cache expiry, check Funchal, San Francisco and San Diego cards, labels, monthly figures and link previews. The Worker refreshes an absent or active pointer after sixty seconds; new API responses can add sixty seconds. Responses cached by the previously deployed API may retain its former one-hour lifetime. Reload open tabs; use an uncached request for operator verification. Retain the previous immutable release and pointer for rollback.

   To restore an earlier fine release, upload its saved pointer. To disable fine mode after this first activation, upload the prepared literal-null pointer through the same existing publisher route:

   ```sh
   devenv shell -- node hosting/sites/publish.mjs files \
     "$NWF_DATA/fine-rollback-b1-20261018" https://nice.jjpcodes.com \
     v6/fine/current.json
   ```

   That restores the existing overview-based place path after cache expiry and tab reload. It requires no deletion endpoint. Never upload this rollback folder during activation.

## Rebuild the small forecast-dependent bundle

Use the retained current forecast records and the final Go renderer to regenerate tomorrow's three outlooks; no new climate download is needed. Use the current UTC date for `--today`, and verify the current run still matches the retained forecast input. Re-run the poster-number comparison described in [outlook/README.md](outlook/README.md), then freeze the selected upload files:

```sh
python3 research/fine/prepare_preview_assets.py --artifacts artifacts \
  --outlooks "$NWF_DATA/fine-validation/b1-worldwide/outlook-refresh/ranking-render" \
  --output "$NWF_DATA/fine-previews-b1-20261018"
```

This copies existing files; it does not publish them. Never reuse the prepared date blindly after UTC midnight or a forecast-run change.

## Evidence and limits

The correction generation covers 320,420,484 supported land pixels at 30 arc seconds. The carrier uses each pixel's own ERA5 point and local clock. It contains the actually assembled five chunk years (`76,78,80,82,84`), 1,800 scored days between `2015-09-30` and `2024-09-13`; it is not a continuous ten-year or twenty-five-year record. New chunks require new period adjustment, corrections and replay as one new generation. Leave the home job and original raw data alone.

The shared JavaScript and independent Go binary decoder both pass all 397 climate queries (thirteen cities plus 384 seeded land pixels), with identical scores and day counts. Forecast app, Worker and Go agree on 1,148 available judgements across the same query cohort, two periods and two tastes; unavailable forecast cases agree too. Three outlook posters match their nine named places. Full-resolution labels use the same catalogue coordinates as search and previews. The existing live catalogue's coordinate/name/population columns were checked against those inputs.

The full replay audit passed all 397,686 packet checksums, decoded contents and fine-pixel coverage in 75.26 seconds. The temperature audit also passed. The shared-function and browser-transport comparisons were rerun against the completed carrier: all 397 places pass, with identical scores and day counts. The browser transport's numerical difference is zero.

The actual headless app check uses the Worker route, binary browser decoder and final rendered interface. It checks bounded traffic, slider input and card/API agreement, and saves default screenshots for Funchal, San Francisco and San Diego. These are Mac browser measurements at desktop and phone-sized viewports, not physical-phone or deployed Sites latency measurements. The final run failed at the San Francisco phone card; the complete-world rendered rerun remains blocked on the reviewed engine fix.

Headline search first shortlists up to 128 in-bounds overview candidates plus rendered labels, then ranks exact numbers. It can miss a place outside that shortlist; searching or clicking it still uses its exact pixel. Native weather other than corrected temperature and the surrounding overview remain coarse. The accepted comparison retains Funchal's evening-temperature limit and Rotterdam's May regression.

B2 remains deferred. Use Sites' free compute for its later map precomputation where faster. No push, upload, activation or deployment is authorized by this document itself.
