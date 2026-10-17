Written by AI on 2026.10.18 by model GPT-6 (Codex).

# B1 fanout follow-up

The original whole-catalogue fanout blocker is fixed in the currently built app. The tested bundle was `dist/assets/main-B2CRvq6L.js`, built successfully after the selector, foreground queue and overview fallback changes. No new concrete code blocker was found in these exercises. Complete-world performance still needs verification after the ongoing data build.

This was an independent headless app run using actual Worker, built HTML/JS, MapLibre, real data and real UI input events. No change to port 5198, another browser, source files or the UI worker. The read-only fixture's missing `bucket.head()` was supplied in a temporary fixture copy so the world URL could be served. This is a fixture repair, not an app fix.

## Actual demand

Both label catalogues loaded. The app no longer scheduled all 14,934 coordinate keys. After readiness and three seconds of network quiet:

| View | Native packets requested initially | Correction objects | Local first usable answer | Initial bytes |
| --- | ---: | ---: | ---: | ---: |
| World, July, 1440×1080 | 188 | 228 | 1.04 s | 23.75 MiB |
| Funchal, July, 1440×1080 | 80 | 132 | 2.46 s | 37.63 MiB |
| San Francisco, July, 390×844 | 58 | 60 | 2.11 s | 38.32 MiB |

Counts include the actual app's overview-ranked shortlist, rendered-label candidates and separate home/comparison/card queries. They are not restricted to an artificially isolated selector unit test. The same function can be called on a regional subset for the van recommendation, so total demand can exceed a single 128-place shortlist. That explains the world count exceeding 128; it does not constitute catalogue fanout.

One warm-enough slider change from 23 °C to 24 °C increased cumulative native request totals to 230, 88 and 60 respectively. The source keeps at most four active place loads, 512 completed place packets and 24 cached judgements per place. Shared raw replay/metadata caching is bounded to 256 completed entries. `overviewScores` falls back to the coarse sampler while the GPU revision is pending; it no longer re-enters fine scoring.

## Foreground card priority

The Funchal packet `native/330412/295.nwfp.gz` was the third native request in its cold navigation. The San Francisco packet `native/301190/134.nwfp.gz` was fifth, immediately after the first four active requests. Both arrived ahead of the remaining candidate backlog. The real queue therefore promotes a chosen card without needing to finish background catalogue work first.

The Funchal card displayed score 100 and 31 nice days. San Francisco displayed 75 and 27 nice days; after the slider input it displayed 70 and 19 nice days. No Runtime exceptions or recursive scoring failure occurred in any of the three runs.

## Limits of this proof

The private data fixture is incomplete: 194 of the world's cumulative 230 native addresses returned 404. Some candidate replay therefore stopped before requesting its twelve correction objects. These measured request counts prove that candidate demand is bounded and foreground priority works; they do not establish the network cost or card latency of the completed worldwide release. Repeat the world and phone runs with the finished data before claiming those performance results for production. Browser traffic is fulfilled locally and asset bytes are counted as fulfilled bytes, not a simulation of production compression or network latency.

The candidate search remains approximate: it takes the top 128 in-bounds overview candidates plus actual rendered labels, then uses the exact shared function for displayed values. This avoids silently excluding small weather destinations merely because their labels are population-filtered. It does not prove the resulting headline is the mathematical winner among all corrected towns. That limitation should remain in the release description.

Screenshots were opened and inspected. World labels, headline and ranking remained readable. The San Francisco phone card showed the adjusted score and physical explanation with a visible dock and ranked destinations below. This scoped check found no new visual failure caused by the engine changes.

## Preview follow-up

After the preview implementation landed, independently fetched actual HTML for San Diego in May, Funchal in July and the coordinate URL `/@32.66568,-16.92547,7z/july`. All three now expose a dynamic fine PNG URL; fetching it through the actual Worker returns HTTP 200 and `image/png`. Independently asserted their PNG dimensions are 1200×630 and their embedded score, nice/perfect/awful counts and total days exactly equal fresh `/api/place` judgements. The San Diego image was opened and inspected: it visibly says 85, 27 nice-or-better, 16 perfect, 0 awful, matching the corrected text. Its layout is readable without clipping. The two Funchal URLs returned identical PNG bytes, as expected for the same location and period. The original missing-preview-image finding is resolved.

## Uploader protocol review

Read `research/fine/upload_b1.mjs` against the actual Worker publisher route. No concrete protocol error found. Native/correction `.gz` bodies are sent raw and stored compressed, as the loaders expect. Text files are gzip-compressed for transport and the Worker unwraps them once before storage. Owner authentication permits the generation's top-level storage prefix; forecast-only credentials would not. Journal rows are appended only after HTTP 201. The release must have `status: ready`, and the uploader does not activate the separate fine pointer. It was not executed with a token or external network.

Evidence: `/tmp/nwf-agents/round4/fine/cold-bounded-recheck/results.json`, the three `*-reads.json` files and three screenshots in the same directory. The independent harness is `/tmp/nwf-agents/round4/fine/recheck-bounded.mjs`; temporary fixture is `recheck-fixture.mjs`. Its headless browser was closed at completion.
