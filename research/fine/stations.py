"""Written by AI on 2026.10.17 by GPT-6.1 Sol. Private phase-one station benchmark.

GHCN observations are not cleared for global redistribution: this script does not
produce a public dataset. Source flags and country codes are retained for review.
"""

import argparse
import calendar
import collections
import concurrent.futures
import csv
import datetime
import hashlib
import json
import math
from pathlib import Path
import shutil
import statistics
import time
import urllib.request


NOAA_BASE = "https://www.ncei.noaa.gov/pub/data/ghcn/daily/"
PERIODS = {"1981-2010": (1981, 2010, 20), "1991-2020": (1991, 2020, 20),
           "2016-2025": (2016, 2025, 8)}
PREVIOUS_STATIONS = [
    ("malaga", "Málaga", "SP000006155"),
    ("tenerife_south", "Tenerife South", "SPE00120476"),
    ("las_palmas", "Las Palmas", "SPE00120215"),
    ("funchal", "Funchal", "PO000008522"),
    ("lisbon", "Lisbon", "PO000008535"),
    ("cape_town", "Cape Town", "SFM00068816"),
    ("sydney", "Sydney", "ASN00066037"),
    ("perth", "Perth", "ASN00009021"),
    ("honolulu", "Honolulu", "USW00022521"),
    ("nairobi", "Nairobi", "KE000063740"),
    ("lima", "Lima", "PEM00084628"),
    ("rotterdam", "Rotterdam", "NLE00152490"),
    ("london", "London Heathrow", "UKE00107650"),
    ("marrakech", "Marrakech", "MOM00060230"),
    ("sf", "San Francisco Downtown", "USW00023272"),
]
NEW_TARGETS = [
    ("san_diego", "San Diego", 32.73, -117.18),
    ("los_angeles", "Los Angeles coastal airport", 33.94, -118.41),
    ("santa_barbara", "Santa Barbara", 34.43, -119.84),
    ("valparaiso", "Valparaíso", -33.05, -71.62),
    ("agadir", "Agadir", 30.42, -9.60),
    ("seville", "Seville", 37.42, -5.90),
    ("chania", "Chania", 35.53, 24.15),
    ("las_vegas", "Las Vegas", 36.08, -115.16),
    ("bangkok", "Bangkok", 13.91, 100.61),
]


def distance_km(first, second):
    latitude_delta = math.radians(first["lat"] - second["lat"])
    longitude_delta = math.radians(first["lon"] - second["lon"])
    half_angle = (math.sin(latitude_delta / 2) ** 2
                  + math.cos(math.radians(first["lat"]))
                  * math.cos(math.radians(second["lat"]))
                  * math.sin(longitude_delta / 2) ** 2)
    return 6371 * 2 * math.asin(min(1, math.sqrt(half_angle)))


def download(url, destination):
    if destination.exists():
        return
    for attempt in range(4):
        try:
            with urllib.request.urlopen(url, timeout=90) as response:
                payload = response.read()
            temporary = destination.with_suffix(destination.suffix + ".partial")
            temporary.write_bytes(payload)
            temporary.replace(destination)
            return
        except Exception:
            if attempt == 3:
                raise
            time.sleep(2 ** attempt)


def load_station_inventory(metadata_path, inventory_path):
    stations = {}
    for line in metadata_path.read_text().splitlines():
        stations[line[:11]] = dict(station=line[:11], lat=float(line[12:20]),
                                  lon=float(line[21:30]), elevation=float(line[31:37]),
                                  name=line[41:71].strip(), country_code=line[:2])
    inventory = collections.defaultdict(dict)
    for line in inventory_path.read_text().splitlines():
        station, latitude, longitude, element, first, last = line.split()
        if element in ("TMAX", "TMIN"):
            inventory[station][element] = (int(first), int(last))
    return stations, inventory


def select_stations(previous_targets, metadata, inventory, nearest_count):
    evaluation = []
    for target in previous_targets:
        target = dict(target)
        target["country_code"] = target["station"][:2]
        target["target_lat"] = target["lat"]
        target["target_lon"] = target["lon"]
        target["target_distance_km"] = 0
        target["role"] = "evaluation"
        evaluation.append(target)
    for slug, place, latitude, longitude in NEW_TARGETS:
        target = dict(slug=slug, place=place, lat=latitude, lon=longitude)
        choices = []
        for station_id, elements in inventory.items():
            if (set(elements) != {"TMAX", "TMIN"}
                    or any(first > 2016 or last < 2020 for first, last in elements.values())):
                continue
            station = metadata[station_id]
            distance = distance_km(target, station)
            if distance <= 40:
                choices.append((distance, station_id))
        if not choices:
            evaluation.append(dict(target, station=None, role="evaluation", target_lat=latitude,
                                   target_lon=longitude, missing_reason="No GHCN station with both extrema and recent inventory within 40 km"))
            continue
        distance, station_id = min(choices)
        evaluation.append(dict(metadata[station_id], slug=slug, place=place, role="evaluation",
                               target_lat=latitude, target_lon=longitude, target_distance_km=distance))
    for station in evaluation:
        if station.get("station") == "GR000167460":
            station["original_noaa_location"] = {key: station[key] for key in ("lat", "lon", "elevation")}
            station.update(lat=35 + 31 / 60 + 43 / 3600, lon=24 + 8 / 60 + 43 / 3600, elevation=149)
            station["coordinate_note"] = ("GHCNd identifies WMO16746. GCOS/DWD 2024 GSN list locates WMO16746 SOUDA(AIRPORT) "
                                           "at35°31′43″N24°08′43″E,149m. Modern benchmark uses that airport site; "
                                           "this is a modern-location assumption, not a proven instrument location. "
                                           "HNMS March2025 reports35.55,24.12. Historical moves remain unresolved.")
            station["geolocation_uncertain"] = True
            station["alternative_primary_location"] = dict(
                lat=35.55, lon=24.12, source="HNMS March2025 Souda station bulletin",
                source_url="https://api.emy.gr/api/cms/assets/329203e1-787c-40dc-9c7e-7b1d6b6d5f2a")
            station["coordinate_source"] = "https://gcos.dwd.de/DWD-GCOS/EN/nationalcontributions/servicesforgcos/centresforqualityassurance/gsmnc/gsnmc_messnetze/gsn_stations_list_pdf.pdf?__blob=publicationFile&v=9"
            station["target_distance_km"] = distance_km(station, dict(lat=station["target_lat"], lon=station["target_lon"]))
    held_out = [station for station in evaluation if station["station"]]
    held_out_identifiers = {station["station"] for station in held_out}
    training = {}
    for target in evaluation:
        choices = []
        for station_id, elements in inventory.items():
            if (set(elements) != {"TMAX", "TMIN"}
                    or any(first > 1991 or last < 2020 for first, last in elements.values())):
                continue
            station = metadata[station_id]
            if station_id in held_out_identifiers or any(distance_km(station, held) <= 5 for held in held_out):
                continue
            distance = distance_km(target, station)
            if distance <= 500:
                choices.append((distance, station_id))
        for distance, station_id in sorted(choices)[:nearest_count]:
            if station_id not in training:
                training[station_id] = dict(metadata[station_id], role="training", near_targets=[])
            training[station_id]["near_targets"].append(dict(slug=target["slug"], distance_km=distance))
    return evaluation, list(training.values())


def aggregate_station(station, path):
    values = collections.defaultdict(list)
    source_flags = collections.Counter()
    measurement_flags = collections.Counter()
    rejected = collections.Counter()
    for line in path.read_text().splitlines():
        year, month, element = int(line[11:15]), int(line[15:17]), line[17:21]
        if element not in ("TMAX", "TMIN") or not 1981 <= year <= 2025:
            continue
        for day in range(1, calendar.monthrange(year, month)[1] + 1):
            record = line[21 + (day - 1) * 8:29 + (day - 1) * 8]
            value = int(record[:5])
            if value == -9999:
                continue
            if record[6] != " ":
                rejected[element] += 1
                continue
            values[(year, month, element)].append(value / 10)
            source_flags[(element, record[7])] += 1
            measurement_flags[(element, record[5])] += 1
    rows = []
    for period, (first_year, last_year, required_years) in PERIODS.items():
        for month in range(1, 13):
            for element in ("TMAX", "TMIN"):
                complete_years = []
                all_values = []
                for year in range(first_year, last_year + 1):
                    month_values = values[(year, month, element)]
                    all_values.extend(month_values)
                    if len(month_values) / calendar.monthrange(year, month)[1] >= .8:
                        complete_years.append((year, statistics.mean(month_values), len(month_values)))
                expected_days = sum(calendar.monthrange(year, month)[1] for year in range(first_year, last_year + 1))
                accepted = len(complete_years) >= required_years
                monthly_mean = statistics.mean(mean for year, mean, days in complete_years) if complete_years else None
                rows.append(dict(station=station["station"], role=station["role"],
                                 slug=station.get("slug", ""), name=station["name"],
                                 country_code=station["country_code"], lat=station["lat"],
                                 lon=station["lon"], elevation=station["elevation"],
                                 period=period, month=month, element=element,
                                 mean_c=monthly_mean, accepted=accepted,
                                 accepted_5_years=len(complete_years) >= (5 if period == "2016-2025" else required_years),
                                 complete_years=len(complete_years), required_years=required_years,
                                 years=";".join(str(year) for year, mean, days in complete_years),
                                 days=len(all_values), expected_days=expected_days,
                                 coverage=len(all_values) / expected_days,
                                 raw_mean_c=statistics.mean(all_values) if all_values else None))
    provenance = dict(station=station["station"], sha256=hashlib.sha256(path.read_bytes()).hexdigest(),
                      bytes=path.stat().st_size, qflag_rejected=dict(rejected),
                      accepted_source_flags={element: {flag: count for (observed_element, flag), count in source_flags.items() if observed_element == element} for element in ("TMAX", "TMIN")},
                      accepted_measurement_flags={element: {flag: count for (observed_element, flag), count in measurement_flags.items() if observed_element == element} for element in ("TMAX", "TMIN")},
                      origin_review="US station" if station["country_code"] == "US" else "Non-US station: derived products require origin licence review")
    return rows, provenance


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("/tmp/nwf-agents/round4/fine/stations"))
    parser.add_argument("--previous", type=Path, default=Path("/tmp/nwf-agents/round3/finedata"))
    parser.add_argument("--nearest-training", type=int, default=30)
    args = parser.parse_args()
    started = time.monotonic()
    args.output.mkdir(parents=True, exist_ok=True)
    daily_directory = args.output / "daily"
    daily_directory.mkdir(exist_ok=True)
    metadata_path = args.output / "ghcnd-stations.txt"
    inventory_path = args.output / "ghcnd-inventory.txt"
    for source, destination in [(Path("/tmp/nwf-agents/round3/bayarea/ghcnd-stations.txt"), metadata_path),
                                (args.previous / "ghcnd-inventory.txt", inventory_path)]:
        if not destination.exists() and source.exists():
            shutil.copyfile(source, destination)
        download(NOAA_BASE + destination.name, destination)
    metadata, inventory = load_station_inventory(metadata_path, inventory_path)
    previous_selection_path = args.previous / "stations.json"
    if previous_selection_path.exists():
        previous = json.loads(previous_selection_path.read_text())
    else:
        previous = [dict(metadata[station_id], slug=slug, place=place)
                    for slug, place, station_id in PREVIOUS_STATIONS]
        for station in previous:
            if station["slug"] == "london":
                station.update(lat=51.47895, lon=-0.45158)
    evaluation, training = select_stations(previous, metadata, inventory, args.nearest_training)
    for filename, payload in [("evaluation_stations.json", evaluation), ("training_stations.json", training)]:
        (args.output / filename).write_text(json.dumps(payload, indent=2) + "\n")
    for station in previous:
        old_path = args.previous / "station_raw" / (station["slug"] + ".dly")
        destination = daily_directory / (station["station"] + ".dly")
        if old_path.exists() and not destination.exists():
            shutil.copyfile(old_path, destination)
    all_stations = [station for station in evaluation + training if station["station"]]
    print(f"Selected {len(evaluation)} evaluation targets and {len(training)} training stations", flush=True)
    errors = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        jobs = {executor.submit(download, NOAA_BASE + "all/" + station["station"] + ".dly",
                                daily_directory / (station["station"] + ".dly")): station for station in all_stations}
        for index, future in enumerate(concurrent.futures.as_completed(jobs), 1):
            station = jobs[future]
            try:
                future.result()
            except Exception as error:
                errors.append(dict(station=station["station"], error=str(error)))
            if index % 25 == 0:
                print(f"Fetched {index}/{len(jobs)}", flush=True)
    rows, provenance = [], []
    for station in all_stations:
        path = daily_directory / (station["station"] + ".dly")
        if path.exists():
            station_rows, station_provenance = aggregate_station(station, path)
            rows.extend(station_rows)
            provenance.append(station_provenance)
    with (args.output / "station_monthly.csv").open("w") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    (args.output / "provenance.json").write_text(json.dumps(dict(
        retrieved=datetime.datetime.now(datetime.timezone.utc).isoformat(),
        source=NOAA_BASE, elapsed_seconds=time.monotonic() - started,
        errors=errors, stations=provenance), indent=2) + "\n")
    for station in evaluation:
        accepted = [row for row in rows if row["station"] == station["station"] and row["period"] == "2016-2025" and row["accepted"]]
        print(station["slug"], station["station"], f"{len(accepted)}/24 accepted evaluation extrema-months", flush=True)
    print(f"Completed in {time.monotonic() - started:.1f}s; download errors: {len(errors)}", flush=True)


if __name__ == "__main__":
    main()
