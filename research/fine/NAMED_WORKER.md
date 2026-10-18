Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Compact named-place consumers

The Worker uses `named-place.mjs` for a named coordinate's default scores and day counts. Climate cards also use its monthly facts. Forecast cards retain the current forecast's physical facts and replace their scores and counts with the compact forecast verdict. Coordinates absent from the compact catalogue retain the existing 25 km card. Nearby place names do not confer membership.

`/api/named` returns availability and the active configuration. `/api/named/place?lat=…&lon=…&kind=climate` returns only the twelve compact fact rows, or JSON null. The browser downloads the compact score table once and requests these small facts responses for cards; it does not download facts shards. The existing label routes use the active catalogue's precise coordinates.

Named default link previews use a dynamic image generated from the same scores and counts. Custom-taste links have a neutral preview without a default score or image; they retain the slider query in their canonical URL. The app itself calculates custom tastes with the existing 25 km model. The Worker does not calculate custom-taste preview scores.

## Actual route checks

`named_worker_proof.mjs` exercises the real Worker with a read-only local bucket. Both reports pass on the final serialized-facts loader:

| Dataset | Validation coordinates | Exact named score/count verdicts | Fallback cases |
| --- | ---: | ---: | ---: |
| Private 397-coordinate model fixture | 397 | 1,215 | 1 |
| Worldwide named catalogue | 397 | 60 | 1,926 |

Each run checks July, winter, year, the forecast stretch and a forecast day. The compact fact endpoint and monthly card facts equal the shared function. Climate and forecast PNG metadata equal the API; both images were opened and inspected. A nearby unnamed San Francisco coordinate has a non-null legacy card. Removing the actual activation file and expiring the same warm Worker's cache restores legacy cards. A custom slider URL has neutral metadata and retains its query.

The worldwide test has twelve named validation cities. The Tenerife south validation coordinate is an explicit airport reference absent from the place list, so it correctly falls back. Named Arona and Adeje remain covered; no airport exception was added. Random Antarctica points below −60° retain today's lack of 25 km coverage.

Reports and preview PNGs are under the SSD's `fine-validation/named-b1-20261018/worker-proof/` (private model fixture) and `fine-validation/named-b1/worker-proof/` (worldwide catalogue). Each report binds its Worker, shared module, image renderer, fixture and proof script by SHA-256.

Run against the active worldwide overlay:

```sh
devenv shell -- node research/fine/named_worker_proof.mjs \
  "$SSD/fine-validation/named-b1/overlay-active" \
  "$SSD/fine-validation/b1-worldwide/queries.json" \
  "$SSD/fine-validation/named-b1/worker-proof/parity.json"
```

The separate rendered app check must use the packaged Worker and exercise both active and absent-pointer states. No data, code or pointer was uploaded by these checks.
