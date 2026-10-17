"""Written by AI on 2026.10.17 by GPT-6.1 Sol. Private phase-one diagnostic.

Fit monthly station-minus-CHELSA residuals. Geographic validation stations and
their five-kilometre neighborhoods never enter training or parameter selection.
The global GHCN-derived result is NOT cleared for public redistribution.
"""

import argparse
import collections
import csv
import json
import math
from pathlib import Path
import statistics
import time

from stations import distance_km


DISTANCE_SCALES_KM = (50, 150, 300)
US_GOVERNMENT_SOURCE_FLAGS = set("0167ABDdFKRTWXZ")


def indexed_observations(path, period, relaxed):
    observations = {}
    accepted_field = "accepted_5_years" if relaxed else "accepted"
    with path.open() as stream:
        for row in csv.DictReader(stream):
            if row["period"] == period and row[accepted_field] == "True":
                observations[(row["station"], int(row["month"]), row["element"])] = float(row["mean_c"])
    return observations


def base_temperature(points, station_id, month, element):
    point = points.get(station_id)
    if point is None:
        return None
    return point["tmax" if element == "TMAX" else "tmin"][month - 1]


def nearest_training_stations(target, training):
    neighbors = []
    for station in training:
        if station["station"] == target["station"]:
            continue
        distance = distance_km(target, station)
        if 5 < distance <= 500:
            neighbors.append((distance, station["station"]))
    return sorted(neighbors)


def interpolated_residual(neighbors, residuals, month, element, distance_scale):
    selected = [(distance, residuals[(station_id, month, element)])
                for distance, station_id in neighbors
                if (station_id, month, element) in residuals][:8]
    if not selected:
        return 0, 0, None
    weighted = [(math.exp(-distance / distance_scale) / max(distance, 5) ** 2, residual)
                for distance, residual in selected]
    correction = sum(weight * residual for weight, residual in weighted) / sum(weight for weight, residual in weighted)
    return correction, len(selected), selected[0][0]


def cross_validate(training, observations, points, residuals):
    neighbors = {station["station"]: nearest_training_stations(station, training)
                 for station in training}
    scores = {}
    selected_scales = {}
    for element in ("TMAX", "TMIN"):
        element_scores = []
        for scale in DISTANCE_SCALES_KM:
            errors_by_station = collections.defaultdict(list)
            baseline_by_station = collections.defaultdict(list)
            with_neighbors = 0
            for station in training:
                station_id = station["station"]
                for month in range(1, 13):
                    key = (station_id, month, element)
                    observed = observations.get(key)
                    base = base_temperature(points, station_id, month, element)
                    if observed is None or base is None:
                        continue
                    correction, count, distance = interpolated_residual(
                        neighbors[station_id], residuals, month, element, scale)
                    errors_by_station[station_id].append(abs(base + correction - observed))
                    baseline_by_station[station_id].append(abs(base - observed))
                    with_neighbors += count > 0
            station_mean_errors = [statistics.mean(errors) for errors in errors_by_station.values()]
            metrics = dict(distance_scale_km=scale,
                           station_balanced_mae_c=statistics.mean(station_mean_errors) if station_mean_errors else None,
                           baseline_station_balanced_mae_c=statistics.mean(statistics.mean(errors) for errors in baseline_by_station.values()) if baseline_by_station else None,
                           stations=len(errors_by_station), extrema_months=sum(map(len, errors_by_station.values())),
                           extrema_months_with_neighbors=with_neighbors)
            element_scores.append(metrics)
        eligible_scores = [entry for entry in element_scores if entry["station_balanced_mae_c"] is not None]
        selected_scales[element] = min(eligible_scores, key=lambda entry: entry["station_balanced_mae_c"])["distance_scale_km"] if eligible_scores else None
        scores[element] = element_scores
    return selected_scales, scores


def fit_scope(scope, training, evaluation, observations, points):
    residuals = {}
    for station in training:
        for month in range(1, 13):
            for element in ("TMAX", "TMIN"):
                key = (station["station"], month, element)
                base = base_temperature(points, station["station"], month, element)
                if key in observations and base is not None:
                    residuals[key] = observations[key] - base
    selected_scales, cv = cross_validate(training, observations, points, residuals)
    rows = []
    for station in evaluation:
        if not station.get("station"):
            continue
        neighbors = nearest_training_stations(station, training)
        for month in range(1, 13):
            for element in ("TMAX", "TMIN"):
                base = base_temperature(points, station["station"], month, element)
                scale = selected_scales[element]
                correction, count, distance = interpolated_residual(
                    neighbors, residuals, month, element, scale) if scale is not None else (0, 0, None)
                rows.append(dict(station=station["station"], slug=station.get("slug", ""),
                                 month=month, element=element, scope=scope,
                                 prediction_c=base + correction if base is not None else None,
                                 base_c=base, correction_c=correction if base is not None else None,
                                 trainingN=count if base is not None else 0,
                                 nearestkm=distance if base is not None else None,
                                 distance_scale_km=scale,
                                 private_diagnostic=scope == "global-private"))
    return rows, dict(training_stations=len(training), residual_extrema_months=len(residuals),
                     selected_scales_km=selected_scales, training_only_cross_validation=cv)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stations", type=Path, default=Path("/tmp/nwf-agents/round4/fine/stations"))
    parser.add_argument("--scratch", type=Path, default=Path("/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1"))
    parser.add_argument("--observations", type=Path)
    parser.add_argument("--period", default="2016-2025")
    parser.add_argument("--relaxed", action="store_true", help="Use five complete evaluation years rather than eight")
    args = parser.parse_args()
    started = time.monotonic()
    evaluation = json.loads((args.stations / "evaluation_stations.json").read_text())
    training = json.loads((args.stations / "training_stations.json").read_text())
    observations = indexed_observations(args.observations or args.stations / "station_monthly.csv", args.period, args.relaxed)
    chelsa = json.loads((args.scratch / "chelsa_points.json").read_text())
    points = chelsa["points"]
    held_out = [station for station in evaluation if station.get("station")]
    for station in training:
        if any(station["station"] == held["station"] or distance_km(station, held) <= 5 for held in held_out):
            raise ValueError(f"Held-out station or co-located duplicate in training: {station['station']}")
    provenance = json.loads((args.stations / "provenance.json").read_text())
    provenance_by_station = {station["station"]: station for station in provenance["stations"]}
    us_training = []
    us_origin_audit = []
    for station in training:
        if station["country_code"] != "US":
            continue
        flags = provenance_by_station[station["station"]]["accepted_source_flags"]
        source_flags = set(flags["TMAX"]) | set(flags["TMIN"])
        eligible = source_flags <= US_GOVERNMENT_SOURCE_FLAGS
        us_origin_audit.append(dict(station=station["station"], flags=sorted(source_flags), eligible=eligible))
        if eligible:
            us_training.append(station)
    all_rows, diagnostics = fit_scope("global-private", training, evaluation, observations, points)
    us_rows, us_diagnostics = fit_scope("us-government-origin", us_training,
                                      [station for station in evaluation if station.get("country_code") == "US"],
                                      observations, points)
    all_rows.extend(us_rows)
    with (args.scratch / "private_residuals.csv").open("w") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(all_rows[0]))
        writer.writeheader()
        writer.writerows(all_rows)
    held_out_errors = collections.defaultdict(list)
    for row in all_rows:
        key = (row["station"], row["month"], row["element"])
        if key in observations and row["prediction_c"] is not None:
            held_out_errors[(row["scope"], row["station"], row["slug"], row["element"])].append(
                (row["base_c"] - observations[key], row["prediction_c"] - observations[key]))
    error_rows = []
    for (scope, station_id, slug, element), errors in held_out_errors.items():
        error_rows.append(dict(scope=scope, station=station_id, slug=slug, element=element,
                               accepted_months=len(errors), complete_year_of_months=len(errors) == 12,
                               base_mae_c=statistics.mean(abs(base) for base, prediction in errors),
                               prediction_mae_c=statistics.mean(abs(prediction) for base, prediction in errors),
                               prediction_bias_c=statistics.mean(prediction for base, prediction in errors)))
    if error_rows:
        with (args.scratch / "private_residuals_errors.csv").open("w") as stream:
            writer = csv.DictWriter(stream, fieldnames=list(error_rows[0]))
            writer.writeheader()
            writer.writerows(error_rows)
    output = dict(written_by="GPT-6.1 Sol, 2026-10-17", phase="Private diagnostic only; no production output",
                  method="Station-minus-exact-CHELSA monthly residual, eight nearest within500 km, inverse-distance-squared times exponential distance weight; five-kilometre geographic exclusion in all CV folds",
                  period=args.period, accepted_field="accepted_5_years" if args.relaxed else "accepted",
                  parameter_selection="Training-only leave-station-and-five-kilometre-neighborhood-out CV; equal weight per station after averaging its monthly absolute errors",
                  missing_neighbors="Zero correction, with trainingN=0; CHELSA alone is retained",
                  missing_chelsa="Missing prediction; no coastal nearest-land substitution",
                  licensing="Global GHCN-origin residuals remain private. US-government-origin is a source-filtered candidate for licence review, not legal clearance.",
                  held_out_error_reporting="Computed only after training-CV selected parameters; accepted_months preserves sparse targets; these errors did not select parameters",
                  chelsa_period=chelsa.get("period"), global_private=diagnostics,
                  us_government_origin=us_diagnostics, us_origin_audit=us_origin_audit,
                  elapsed_seconds=time.monotonic() - started)
    (args.scratch / "private_residuals_cv.json").write_text(json.dumps(output, indent=2) + "\n")
    print(json.dumps({"global_private": diagnostics, "us_government_origin": us_diagnostics,
                      "seconds": output["elapsed_seconds"]}, indent=2))


if __name__ == "__main__":
    main()
