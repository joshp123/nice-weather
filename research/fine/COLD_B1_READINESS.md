Written by AI on 2026.10.18 by model GPT-6 (Codex).

# B1 readiness review

Scope: Josh's verbatim latest instruction supplied by the parent, the original
B1 request, repository steering, actual consumer code and retained artifacts.
Read-only consumer review; no deployment, upload, source fix or data change.

## Blocking result

The final rendered San Francisco phone check failed. Its screenshot visibly
shows “Weather data for july is missing: feature index out of bounds” instead
of a weather card. Its captured requests show HTTP 200 from both the actual
place API and the San Francisco fine replay endpoint. This is a consumer
integration failure, not absent San Francisco weather. Do not seal the release
until the corrected rendered flow passes. Another agent owns the candidate fix.

Evidence: SSD `fine-validation/b1-worldwide/app-complete/`
`san-francisco-phone-failure.json` and `.png`, and
`/tmp/nwf-agents/round4/fine/b1-app-final.log`. The screenshot was opened and
inspected. The existing release sealer requires the completed real-app report;
that requirement must remain intact.

## Candidate fix review

The unapplied `FINE_LABEL_QUERY.patch` is narrow and appropriate. It caches
the rendered-label slugs and queries them on MapLibre's `idle` event rather
than synchronously during weather redraw. The installed MapLibre source emits
that event only when the map is loaded, no camera movement is active and no
source/style/placement work remains dirty. The patch keeps the existing
overview shortlist and requests another redraw when the rendered slug set
changes, so visible labels still enter exact replay. It changes no weather,
temperature corrections or score calculation. No blocking patch defect found.

The instrumented idle run produced a working San Francisco phone card and
reported loaded label source and tiles. This supports the candidate, but does
not clear the original failure: the final rebuilt source and rendered phone
flow must pass before upload readiness is claimed.

## Independently checked

Fine climate cards, monthly facts, link-preview text, browser judgements,
labels and headline values reach the shared JavaScript calculation. Outlook
ranking invokes its Go port on forecast records; its spatial footprint matches
the Worker and current browser implementation. Forecasts remain ECMWF weather.
The accepted uniform evening rule is used in both daily judgements and physical
monthly facts. This review does not reopen the accepted Rotterdam May limit.

I exercised the actual Worker against read-only SSD files, with an in-memory
review pointer, for San Francisco, Funchal, San Diego, random-113 and random-275.
For July, the direct shared judgement, decoded browser transport, Worker
judgement and returned monthly score matched exactly in every case. All five
packets declared the uniform evening model. This is a five-place independent
consumer check, not a replacement for the full worldwide audit or Go proof.

No concrete upload-protocol or ordering defect found. The immutable-data
uploader requires a ready release, preserves compressed binary objects,
unwraps text transport through the actual publisher route and records success
only after HTTP 201. The handoff stages data, matching previews, compatible
code and the runner before uploading the separate activation pointer last.

At review, the replay manifest still said `building`; the release root had no
`RELEASE.json`. `UPLOAD.md` correctly states that the partial directory must
not be uploaded and leaves final replay size, code package and activation
pointer pending. Those completion items and the failed rendered flow prevent
an upload-ready claim. No additional product feature is requested.
