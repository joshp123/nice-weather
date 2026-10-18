Written by AI (GPT-6), 2026-10-18.

# Named-place release: live

Nice Weather is public at https://nice.jjpcodes.com, Sites version **19**. Default-taste named places now use the compact catalogue. The surrounding map, unnamed areas, unsupported places and changed tastes retain the 25 km calculation.

## Uploads

| Phase | Objects | Stored bytes |
| --- | ---: | ---: |
| Immutable named climate, facts, labels, attribution, manifest and current-run forecast table | 19 | 117,728,981 |
| Rebuilt Linux runner, refresh instructions and three licences | 5 | 35,416,693 |
| Matching staged outlooks | 9 | 1,502,098 |
| Named activation pointer, uploaded last among activation dependencies | 1 | 282 |
| Poster-export previews: world, San Francisco and Lisbon, all 18 periods plus year aliases | 57 | 15,326,499 |
| Site-route daily refresh: complete forecast, named table, outlooks and pointer | 27 | 22,585,235 |
| **Storage writes, including repeated forecast/outlook keys** | **118** | **192,559,788** |

The app package contains 18 files / 5,307,019 bytes; its uploaded gzip archive is 1,169,631 bytes. Data upload took 107.575 seconds; runner 24.826 seconds; staged outlooks 4.516 seconds; previews 30.021 seconds. Every uploaded storage object was downloaded and checked against its expected size and SHA-256.

Publication followed `research/fine/UPLOAD.md`: data, matching app, runner and matching dated outlooks, readback and inactive-app checks, then the separate named pointer. The pointer is `v6/named/current.json`, generation `e68c86ba3e24654a499ae4ef37ad35f9`, root `v6-named-b1-20261018`. The held `v6/fine/current.json` returns 404 and `/api/fine/replay` reports disabled. No full replay or private validation/source objects were uploaded. No paid service was used.

## Daily refresh

The Site's owner-only `begin_forecast_refresh` route issued the one-hour grant. The native Darwin build from the same 29 compiled inputs as the uploaded Linux binary ran the documented forecast command. Credentials passed through hidden stdin and remained in memory.

The refresh completed in **216.950 seconds**, downloading **433,938,015 ECMWF bytes**. Publication took 38.854 seconds. All 26 data objects were published before the forecast pointer.

`/data/v6/forecast/current.json` points to `20261017T0000Z`, the newest complete run available during the refresh, with first date 2026-10-17, 14 days and an empty previous run. The 3,920,070-byte `days.bin` matches `(74,668 / 4) × 14 × 15`. The fresh named table is 15,783,945 bytes, SHA-256 `fef9cd80cf84111d08fe531947b25d1a7aaaac74a456cdae707c09488f927862`.

Outlooks are dated **19 October 2026**:

- World: Chengdu, Lahore and Delhi.
- Europe: Rome, Milan and Rostov-on-Don.
- USA: Los Angeles, Houston and Phoenix.

All three outlook manifests and six PNGs passed live readback. Europe serves its matching poster preview.

## Live checks

Headless Chromium only; no visible window or personal browser.

- San Francisco search and an actual canvas click both settle to July **75 / 27 nice / 8 perfect**. The click's requested point resolves to the canonical city coordinate and share URL. Untouched production JavaScript returned no runtime or console errors on the click.
- Funchal and San Diego: July **100 / 31 nice / 31 perfect**. City-label score/count data agree with the cards.
- World July: Los Angeles, Belo Horizonte and Brasília. World November–March: Bahir Dar, Adama and Antofagasta, with flags.
- Changed taste gives a working area estimate; resetting restores the named score. Remote unnamed land also uses the area estimate.
- An untouched browser's real wheel zoom followed by a San Francisco canvas click also returns 75 / 27 / 8, with zero page or console errors.
- San Francisco link-preview text and PNG agree on 75 / 27 / 8. Lisbon November–March tags use its current named judgement. World previews use the newly rendered poster exports.
- Fresh-run San Francisco forecast cards match the named API/table: fortnight score 43, 6 nice / 3 perfect / 6 awful; the checked individual day scores 80. The browser requested the current run's `named.bin.gz`; both views passed without runtime errors. Forecast scoring retains the existing shared forecast model described in the handoff.
- Phone 390 × 844 and laptop 1512 × 982 screenshots were viewed. Cards, headline text and controls fit.

## Rollback

The separate rollback payload is the literal five-byte `null\n` at `fine-named-upload-b1-20261018-rollback/v6/named/current.json` on the data volume. Its checksum inventory is saved with the deployment evidence. Publish only that key with the owner publisher, wait at least 60 seconds, then reload. `/api/named` must report disabled and cards return to area estimates.

Warm rollback was tested against the exact packaged Worker with a local read-only storage overlay and expired configuration cache: active SF 75 became disabled and matched a fresh inactive Worker. The live app was also checked before activation with the pointer absent: SF 98 / 31 nice / 30 perfect, explicitly Area estimate. Production was not toggled off after activation.

## Limits and provenance

The renderer fix is committed and uses the actual Make a poster preview blob. The complete world, SF and Lisbon sets were rendered and uploaded. The remaining **157 city sets were skipped under Josh's short-time allowance**: all 18 periods projected to 70–80 minutes and roughly 600 MB. Existing cards were preserved. Named-place preview PNGs are also generated directly by the Worker from current named values.

Cold pages can initially show the 25 km Area estimate while the named tables load, then replace it with the named result. Independent untouched-browser checks confirmed the settled values above.

Product build: clean checkout `d1c3747` (renderer change only after `548d7f6`). Sites source commit: `760aeebdd9e46ec485e858321afdc0058f5046d1`. The freshly rebuilt runner's 29 compiled inputs match the deployed product checkout on both platforms. The Site remains public and its existing daily 08:00 UTC automation remains enabled.

Durable evidence, exact inventories, live readbacks and screenshots: `/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/deployments/named-20261018/`.

Key screenshots within its `evidence/browser/` folder:

- `san-francisco-search-laptop-dark.png`, `uninstrumented-after-click.png`, `san-francisco-phone-light.png`.
- `funchal-laptop-dark.png`, `san-diego-laptop-dark.png`.
- `world-july-laptop-dark.png`, `world-nov-mar-laptop-dark.png`.
- `forecast-sf-next14-laptop-dark.png`, `forecast-sf-next14-phone-dark.png`, `forecast-sf-single-day-laptop-dark.png`.
- `taste-fallback-laptop-dark.png`, `wheel-zoom-after-click.png`.

Independent untouched-browser review and its taste/unnamed/preview screenshots are in `evidence/cold-review/REVIEW.md` and the same folder.

The final acceptance records are `evidence/browser/uninstrumented-click.json`, `final-uninstrumented.json`, `forecast-report.json` and `local-null-rollback.json`, plus the independent review. Earlier incomplete probe attempts are retained for diagnosis; the final untouched click, wheel, taste and forecast sessions supersede them. All owned headless browsers and the local preview server were stopped.
