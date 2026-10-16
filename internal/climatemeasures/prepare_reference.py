"""Written by AI on 2026.10.16 by model GPT-6.1 Sol.

Prepare frozen research arrays for the optional production-path Go comparison.
This is offline evidence conversion, never part of the climate builder runtime.
"""

import argparse
import csv
import gzip
import json
from pathlib import Path
import struct

import numpy as np

parser = argparse.ArgumentParser()
parser.add_argument("--research", type=Path, required=True)
parser.add_argument("--out", type=Path, required=True)
parser.add_argument("--linke", type=Path, required=True)
args = parser.parse_args()
args.out.mkdir(parents=True, exist_ok=True)
analysis = args.research / "data/analysis"
inputs = args.research / "data2"
selection = json.loads((inputs / "temperature/selection.json").read_text())
linke = json.loads(args.linke.read_text())
sun = {(r["city"], r["date"]): float(r["ratio_sun_fraction_10_20"]) for r in csv.DictReader((inputs / "sun/daily_sun.csv").open())}
places = []
for name, latitude, longitude, timezone in json.loads((analysis / "places.json").read_text()):
    slug = name.lower().replace(" ", "_")
    native = json.loads((analysis / f"{slug}_hourly.json").read_text())
    ground = json.loads((analysis / f"{slug}_elevation.json").read_text())["elevation"][0]
    radiation = json.loads((inputs / "sun" / f"{slug}_sun.json").read_text())
    mirror = np.load(inputs / "temperature/cache" / f"{slug}_mirror16.npz")
    selected = selection[slug]["nearest_pure_land"]
    index = next(i for i, coordinate in enumerate(mirror["coords"]) if list(coordinate) == selected)
    temperature = mirror["temperature"][index].astype(float)
    h = native["hourly"]
    count = len(temperature)
    series = np.stack([
        temperature + 273.15,
        np.asarray(h["dew_point_2m"]) + 273.15,
        np.asarray(h["precipitation"]) / 1000,
        np.asarray(radiation["hourly"]["direct_radiation"]) * 3600,
        np.asarray(h["wind_speed_10m"]) / 3.6,
        np.zeros(count),
    ]).astype("<f4")
    with np.load(inputs / "temperature" / f"{slug}_temperature_methods.npz") as cached:
        expected_temperature = {key: cached[key] for key in ("dates", "b_pure")}
    with np.load(inputs / f"{slug}_base.npz") as cached:
        base = {key: cached[key] for key in ("indices", "rain", "fullrain", "dew", "wind")}
    dates = [str(date) for date in expected_temperature["dates"]]
    expected = []
    dew = np.asarray(h["dew_point_2m"])
    wind = np.asarray(h["wind_speed_10m"])
    for day, date in enumerate(dates):
        indices = base["indices"][day][:10]
        expected.append(list(expected_temperature["b_pure"][day][:10]) + [
            dew[indices].mean(),
            float(base["rain"][day]),
            float(base["fullrain"][day]),
            sun[(name, date)],
            wind[indices].mean(),
            float(base["dew"][day]),
            float(base["wind"][day]),
        ])
    meta = dict(name=name, latitude=latitude, longitude=longitude, elevationM=ground,
                timezone=timezone, monthlyTurbidity=linke[name], sourceElevationM=float(mirror["elevation"][index]),
                selectedLatitude=selected[0], selectedLongitude=selected[1],
                nativeLatitude=native["latitude"], nativeLongitude=native["longitude"],
                start=h["time"][0] + ":00Z", hours=count, dates=dates)
    header = json.dumps(meta).encode()
    with gzip.open(args.out / f"{slug}.bin.gz", "wb") as target:
        target.write(struct.pack("<I", len(header)))
        target.write(header)
        target.write(series.tobytes())
        target.write(np.asarray(expected, dtype="<f8").tobytes())
    places.append(dict(Latitude=latitude, Longitude=longitude, ElevationM=ground, Timezone=timezone,
                       MonthlyTurbidity=linke[name], SourceLatitude=native["latitude"], SourceLongitude=native["longitude"]))
    print(name, len(dates), "days")
(args.out / "places.json").write_text(json.dumps(places, indent=2))
