Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# B1 upload handoff

Status: preparation in progress. Do not upload the partial directory or the
private validation pointer. This file will contain the measured final sizes
and completion evidence when the worldwide build passes its audits.

The accepted release uses unchanged daytime high/low correction tiles and one
uniform evening shift. Rotterdam's May score regression remains a known limit;
the accepted city-average comparison is [B1_EVENING_RESULT.md](B1_EVENING_RESULT.md).
The overview map remains at its existing resolution. Forecast weather retains
the existing ECMWF values and uses the shared judgement function.

The new immutable data namespace is `v6-fine-b1-20261018/`, on the SSD beside
`v6`. It contains `temperature/`, `replay/` and `ATTRIBUTION.txt`. The separately
prepared forecast runner is under `fine-runner-b1-20261018/runner/`.

Upload immutable data first. Package the matched Worker, shared modules and
app after the interface worker's changes land. Upload the new forecast runner
with its licence notices. Check the immutable data and matched code, then
upload `v6/fine/current.json` last. The existing `v6` overview files are not
part of this release. The publisher's token comes from the existing secret
workflow; it must never be copied into this document or a command argument.

The shared loader refreshes the activation pointer after 60 seconds, including
an absent pointer. A warm Worker therefore sees activation and rollback.
Reload an already open app tab after activation. Keep the previous pointer and
immutable generation available for rollback.

Named-place scores and counts come from exact replay. To avoid downloading
the 15,000-place catalogue's weather into each phone, the app first uses the
overview to shortlist up to 128 headline candidates within the current map
bounds, alongside the city labels actually drawn in that view. The
returned numbers and the final headline ranking use exact replay. This is a
bounded candidate search, not proof of the world's optimum: a place missing
from the overview shortlist can be missed by the headline. Searching or
clicking that place still reads its exact pixel. Selected cards and home
comparisons take priority over background label loading.

Nothing has been pushed, uploaded, activated or deployed by this task. B2 is
deferred; use Sites compute for that later work where it is faster.
