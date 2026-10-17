<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->

# The same forecast at a coordinate

The app's map and forecast cards use a normalized four-by-four B-spline footprint around a coordinate. The previous Worker preview used the nearest valid point, which could give a different score or band for the same place. The shared module now exports `mapPointWeights` and `blendPlaceNumbers`, and the Worker applies that same footprint to its existing native forecast judgements.

Each valid point is judged with `placeNumbers` before spatial blending. The final score is rounded to four decimal places; shares, counts, score parts and physical weather are weighted with the same footprint. Period length and representative-day count remain the common period values. Forecast temperature, rain, wind and other weather inputs remain the existing ECMWF records and current monthly offsets. Exact 1 km climate replay remains unchanged.

Forecast `placeNumbers` now returns the complete score parts and physical averages from its existing lower/upper temperature-shift judgements. Its score and day-count calculation is unchanged. Within each Worker request, weather reads are reused by cell and file so the sixteen-point footprint does not repeat identical storage requests for each forecast day.

The actual unchanged browser `PointSampler` matched the new Worker/default-taste and shared/custom-taste path on 397 validation coordinates, two forecast periods and two tastes. All 1,148 available judgements had identical scores and day counts. The maximum difference across score parts and physical weather was 7.11e-14. The 440 unavailable cases matched on both paths. Evidence: `fine-validation/b1-worldwide/outlook-refresh/spatial-parity/app-forecast-parity.json` on the SSD.

The small unapplied browser hook is `research/fine/outlook/sampling-shared.patch`: import and re-export the shared footprint, and replace inline spatial averaging with `blendPlaceNumbers`. It changes neither the footprint nor the native grid-point calculation. The interface owner controls its application. The Go outlook port uses the same ordered footprint and blending rule; its separate parity evidence is recorded by the outlook work.
