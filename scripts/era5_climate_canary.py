# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "icechunk==2.1.1",
#   "numpy==2.5.1",
#   "numcodecs[pcodec]==0.16.5",
#   "zarr==3.2.1",
# ]
# ///

"""Read a bounded, pinned ERA5 sample for the Go climate canary scorer."""

from __future__ import annotations

import argparse
import calendar
import datetime as dt
import http.server
import json
import math
import os
import platform
import resource
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

import icechunk
import numpy as np
import zarr


SNAPSHOT_ID = "C2QSEC9FP3SJ6MT4D94G"
REPOSITORY = "s3://earthmover-icechunk-era5/icechunkV2"
UPSTREAM = "https://earthmover-icechunk-era5.s3.amazonaws.com/icechunkV2"
GROUP = "single/temporal"
FIELDS = ("t2m", "d2m", "tp", "ssrd", "tcc", "u10", "v10")
KELVIN_OFFSET = 273.15
WET_HOURLY_MM = 0.1
MAX_REQUESTS = 200
MAX_RESPONSE_BYTES = 32 * 1024 * 1024
MAX_TOTAL_BYTES = 160 * 1024 * 1024
MAX_DEW_POINT_EXCESS_C = 0.01


@dataclass(frozen=True)
class Cell:
    id: str
    scenario: str
    requested_latitude: float
    requested_longitude: float
    timezone: str
    months: tuple[int, ...]


CELLS = (
    *(Cell(f"chad-{longitude:.2f}", "chad-seam", 15.0, longitude, "Africa/Ndjamena", (4, 5))
      for longitude in (21.75, 22.0, 22.25, 22.5, 22.75, 23.0, 23.25)),
    Cell("malaga", "malaga-annual-cycle", 36.7213, -4.4214, "Europe/Madrid", tuple(range(1, 13))),
    Cell("amsterdam", "amsterdam-winter", 52.3676, 4.9041, "Europe/Amsterdam", (1,)),
    Cell("singapore", "singapore-humid-heat", 1.3521, 103.8198, "Asia/Singapore", (5,)),
    Cell("aswan", "aswan-hot-dry", 24.0889, 32.8998, "Africa/Cairo", (5,)),
)


class TransferCounter:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.requests = 0
        self.gets = 0
        self.heads = 0
        self.response_bytes = 0
        self.maximum_response_bytes = 0

    def start(self, method: str) -> None:
        with self.lock:
            self.requests += 1
            if self.requests > MAX_REQUESTS:
                raise RuntimeError(f"ERA5 request limit exceeded: {self.requests}")
            if method == "GET":
                self.gets += 1
            elif method == "HEAD":
                self.heads += 1

    def add_bytes(self, count: int) -> None:
        with self.lock:
            if self.response_bytes + count > MAX_TOTAL_BYTES:
                raise RuntimeError("ERA5 transfer limit exceeded")
            self.response_bytes += count

    def finish_response(self, count: int) -> None:
        with self.lock:
            self.maximum_response_bytes = max(self.maximum_response_bytes, count)


def proxy_handler(counter: TransferCounter):
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self) -> None:  # noqa: N802
            self._forward("GET")

        def do_HEAD(self) -> None:  # noqa: N802
            self._forward("HEAD")

        def log_message(self, _format: str, *_args: Any) -> None:
            return

        def _forward(self, method: str) -> None:
            counter.start(method)
            headers = {}
            for name in ("Range", "If-Match", "If-None-Match", "If-Modified-Since"):
                value = self.headers.get(name)
                if value:
                    headers[name] = value
            request = urllib.request.Request(UPSTREAM + self.path, headers=headers, method=method)
            try:
                response = urllib.request.urlopen(request, timeout=120)
            except urllib.error.HTTPError as error:
                response = error
            except Exception as error:  # pragma: no cover - exercised only by a live transport failure
                payload = str(error).encode("utf-8", errors="replace")
                self.send_response(502)
                self.send_header("Content-Type", "text/plain; charset=utf-8")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                if method != "HEAD":
                    counter.add_bytes(len(payload))
                    self.wfile.write(payload)
                    counter.finish_response(len(payload))
                return

            try:
                self.send_response(response.status)
                for name, value in response.headers.items():
                    if name.lower() not in {"connection", "transfer-encoding", "content-encoding"}:
                        self.send_header(name, value)
                self.send_header("Connection", "close")
                self.end_headers()
                if method == "HEAD":
                    return
                response_bytes = 0
                while True:
                    chunk = response.read(1024 * 1024)
                    if not chunk:
                        break
                    if response_bytes + len(chunk) > MAX_RESPONSE_BYTES:
                        raise RuntimeError("ERA5 response limit exceeded")
                    counter.add_bytes(len(chunk))
                    self.wfile.write(chunk)
                    response_bytes += len(chunk)
                counter.finish_response(response_bytes)
            finally:
                response.close()

    return Handler


def nearest_index(values: np.ndarray, wanted: float) -> int:
    return int(np.abs(values - wanted).argmin())


def output_cell(cell: Cell, latitudes: np.ndarray, longitudes: np.ndarray) -> dict[str, Any]:
    longitude = cell.requested_longitude % 360
    lat_index = nearest_index(latitudes, cell.requested_latitude)
    lon_index = nearest_index(longitudes, longitude)
    grid_longitude = float(longitudes[lon_index])
    if grid_longitude > 180:
        grid_longitude -= 360
    return {
        "id": cell.id,
        "scenario": cell.scenario,
        "timezone": cell.timezone,
        "requestedLatitude": cell.requested_latitude,
        "requestedLongitude": cell.requested_longitude,
        "gridLatitude": float(latitudes[lat_index]),
        "gridLongitude": grid_longitude,
        "latitudeIndex": lat_index,
        "longitudeIndex": lon_index,
        "months": list(cell.months),
    }


def expected_dates(year: int, months: tuple[int, ...]) -> list[dt.date]:
    result = []
    for month in months:
        result.extend(dt.date(year, month, day) for day in range(1, calendar.monthrange(year, month)[1] + 1))
    return result


def selected_interval_indices(date: dt.date, timezone_name: str, times: list[dt.datetime]) -> list[int]:
    timezone = ZoneInfo(timezone_name)
    return [
        index for index, valid_time in enumerate(times)
        if (local_end := valid_time.astimezone(timezone)).date() == date
        and 10 < local_end.hour <= 20 and local_end.minute == 0
    ]


def relative_humidity_percent(temperature_c: np.ndarray, dew_point_c: np.ndarray) -> tuple[np.ndarray, int, float]:
    if temperature_c.shape != dew_point_c.shape or not np.isfinite(temperature_c).all() or not np.isfinite(dew_point_c).all():
        raise ValueError("temperature and dew point must be complete synchronous arrays")
    excess = np.maximum(dew_point_c - temperature_c, 0)
    maximum_excess = float(excess.max())
    if maximum_excess > MAX_DEW_POINT_EXCESS_C + 1e-9:
        raise ValueError(f"dew point exceeds synchronous air temperature by {maximum_excess:.6f} C")
    adjusted_dew_point = np.minimum(dew_point_c, temperature_c)
    vapour = np.exp((17.625 * adjusted_dew_point) / (243.04 + adjusted_dew_point))
    saturation = np.exp((17.625 * temperature_c) / (243.04 + temperature_c))
    result = 100.0 * vapour / saturation
    return bounded_relative_humidity(result), int((excess > 0).sum()), maximum_excess


def bounded_relative_humidity(result: np.ndarray) -> np.ndarray:
    if not np.isfinite(result).all() or (result < 0).any() or (result > 100 + 1e-9).any():
        raise ValueError("derived relative humidity is outside 0-100")
    return np.minimum(result, 100)


def aggregate_day(cell: dict[str, Any], date: dt.date, times: list[dt.datetime], values: dict[str, np.ndarray]) -> dict[str, Any]:
    missing = set(FIELDS) - values.keys()
    if missing:
        raise ValueError(f"{cell['id']} is missing fields: {sorted(missing)}")
    selected = selected_interval_indices(date, cell["timezone"], times)
    if len(selected) != 10:
        raise ValueError(f"{cell['id']} {date.isoformat()} has {len(selected)} hourly intervals, want 10")

    def chosen(field: str) -> np.ndarray:
        result = values[field][selected]
        if result.shape != (10,) or not np.isfinite(result).all():
            raise ValueError(f"{cell['id']} {date.isoformat()} {field} is incomplete")
        return result.astype(np.float64)

    temperature = chosen("t2m") - KELVIN_OFFSET
    dew_point = chosen("d2m") - KELVIN_OFFSET
    humidity, saturation_clamped_hours, maximum_dew_point_excess = relative_humidity_percent(temperature, dew_point)
    rain_mm = chosen("tp") * 1000.0
    solar = chosen("ssrd")
    cloud = chosen("tcc")
    if (rain_mm < 0).any():
        raise ValueError(f"{cell['id']} {date.isoformat()} tp contains a negative accumulation")
    if (solar < 0).any():
        raise ValueError(f"{cell['id']} {date.isoformat()} ssrd contains a negative accumulation")
    if ((cloud < 0) | (cloud > 1)).any():
        raise ValueError(f"{cell['id']} {date.isoformat()} tcc is outside 0-1")
    wind = np.hypot(chosen("u10"), chosen("v10")) * 3.6
    metrics = {
        "date": date.isoformat(),
        "latitude": cell["gridLatitude"],
        "longitude": cell["gridLongitude"],
        "comfortableTempC": round(float(temperature.mean()), 6),
        "minTempC": round(float(temperature.min()), 6),
        "maxTempC": round(float(temperature.max()), 6),
        "rainMm": round(float(rain_mm.sum()), 6),
        "wetTimeFraction": round(float((rain_mm >= WET_HOURLY_MM).mean()), 6),
        "solarEnergyMJm2": round(float(solar.sum() / 1_000_000.0), 6),
        "cloudCoverPercent": round(float(cloud.mean() * 100.0), 6),
        "windSpeedKmh": round(float(wind.mean()), 6),
        "dewPointC": round(float(dew_point.mean()), 6),
        "relativeHumidityPercent": round(float(humidity.mean()), 6),
        "humiditySaturationClampedHours": saturation_clamped_hours,
        "maximumDewPointExcessC": round(maximum_dew_point_excess, 6),
    }
    return {"cellId": cell["id"], "metrics": metrics}


def peak_rss_bytes() -> int:
    value = int(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss)
    return value if platform.system() == "Darwin" else value * 1024


def directory_bytes(path: str | None) -> int:
    if not path:
        return 0
    total = 0
    pending = [Path(path)]
    while pending:
        current = pending.pop()
        for entry in current.iterdir():
            if entry.is_dir() and not entry.is_symlink():
                pending.append(entry)
            elif entry.is_file():
                total += entry.stat().st_size
    return total


def validate_layout(group: zarr.Group) -> None:
    expected_units = {
        "t2m": "K",
        "d2m": "K",
        "tp": "m",
        "ssrd": "J m**-2",
        "tcc": "(0 - 1)",
        "u10": "m s**-1",
        "v10": "m s**-1",
    }
    for field, units in expected_units.items():
        array = group[field]
        step_type = "accum" if field in {"tp", "ssrd"} else "instant"
        if array.shape != (756048, 721, 1440) or array.chunks != (8736, 12, 12):
            raise ValueError(f"ERA5 {field} has an unexpected shape or chunk layout")
        if array.attrs.get("units") != units or array.attrs.get("GRIB_stepType") != step_type:
            raise ValueError(f"ERA5 {field} has unexpected units or time semantics")
        if step_type == "accum" and "1 hour ending at `valid_time`" not in array.attrs.get("accumulation_comment", ""):
            raise ValueError(f"ERA5 {field} does not declare one-hour ending accumulations")


def run(year: int, staging: Path) -> dict[str, Any]:
    started = time.monotonic()
    if not staging.is_absolute():
        raise ValueError("staging must be an absolute path")
    staging.mkdir(parents=True, exist_ok=True)
    scratch = Path(tempfile.mkdtemp(prefix="era5-source-scratch-", dir=staging))
    os.environ["TMPDIR"] = str(scratch)
    os.environ["TMP"] = str(scratch)
    os.environ["TEMP"] = str(scratch)
    counter = TransferCounter()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), proxy_handler(counter))
    server_thread = threading.Thread(target=server.serve_forever, daemon=True)
    server_thread.start()
    logical_reads = 0
    decoded_bytes = 0
    scratch_peak_bytes = 0
    try:
        storage = icechunk.http_storage(f"http://127.0.0.1:{server.server_port}")
        repository = icechunk.Repository.open(storage)
        session = repository.readonly_session(snapshot_id=SNAPSHOT_ID)
        root = zarr.open_group(store=session.store, mode="r")
        group = root[GROUP]

        validate_layout(group)

        latitudes = np.asarray(group["latitude"][:])
        longitudes = np.asarray(group["longitude"][:])
        logical_reads += 2
        decoded_bytes += latitudes.nbytes + longitudes.nbytes
        if not np.array_equal(latitudes, np.linspace(90.0, -90.0, 721)) or not np.array_equal(longitudes, np.arange(1440) * 0.25):
            raise ValueError("ERA5 latitude or longitude coordinates do not match the declared 0.25 degree grid")
        cells = [output_cell(cell, latitudes, longitudes) for cell in CELLS]

        epoch = dt.datetime(1940, 1, 1, tzinfo=dt.timezone.utc)
        first = dt.datetime(year, 1, 1, tzinfo=dt.timezone.utc)
        last = dt.datetime(year + 1, 1, 1, tzinfo=dt.timezone.utc)
        start_index = int((first - epoch).total_seconds() // 3600)
        end_index = int((last - epoch).total_seconds() // 3600)
        time_offsets = np.asarray(group["valid_time"][start_index:end_index])
        logical_reads += 1
        decoded_bytes += time_offsets.nbytes
        expected_offsets = np.arange(start_index, end_index, dtype=time_offsets.dtype)
        if not np.array_equal(time_offsets, expected_offsets):
            raise ValueError("ERA5 valid_time is not the expected complete hourly sequence")
        times = [epoch + dt.timedelta(hours=int(offset)) for offset in time_offsets]

        values: dict[str, dict[str, np.ndarray]] = {cell["id"]: {} for cell in cells}
        chunk_groups: dict[tuple[int, int], list[dict[str, Any]]] = {}
        for cell in cells:
            chunk_groups.setdefault((cell["latitudeIndex"] // 12, cell["longitudeIndex"] // 12), []).append(cell)

        for (lat_chunk, lon_chunk), chunk_cells in sorted(chunk_groups.items()):
            row_start, col_start = lat_chunk * 12, lon_chunk * 12
            row_end = min(row_start + 12, len(latitudes))
            col_end = min(col_start + 12, len(longitudes))
            for field in FIELDS:
                block = np.asarray(group[field][start_index:end_index, row_start:row_end, col_start:col_end])
                scratch_peak_bytes = max(scratch_peak_bytes, directory_bytes(str(scratch)))
                logical_reads += 1
                decoded_bytes += block.nbytes
                if not np.isfinite(block).all():
                    raise ValueError(f"{field} chunk {lat_chunk},{lon_chunk} contains missing values")
                for cell in chunk_cells:
                    values[cell["id"]][field] = block[:, cell["latitudeIndex"] - row_start, cell["longitudeIndex"] - col_start].copy()

        days = []
        definitions = {cell.id: cell for cell in CELLS}
        for cell in cells:
            definition = definitions[cell["id"]]
            for date in expected_dates(year, definition.months):
                days.append(aggregate_day(cell, date, times, values[cell["id"]]))
        days.sort(key=lambda item: (item["cellId"], item["metrics"]["date"]))
        scratch_end_bytes = directory_bytes(str(scratch))
    finally:
        server.shutdown()
        server.server_close()
        server_thread.join(timeout=5)
        scratch_peak_bytes = max(scratch_peak_bytes, directory_bytes(str(scratch)))
        scratch_end_bytes = directory_bytes(str(scratch))
        subprocess.run(["trash", str(scratch)], check=True)

    return {
        "schemaVersion": 1,
        "source": {
            "authority": "Copernicus Climate Change Service",
            "dataset": "ERA5 hourly data on single levels",
            "repository": REPOSITORY,
            "snapshotId": SNAPSHOT_ID,
            "transport": "Earthmover Icechunk public mirror",
            "gridDegrees": 0.25,
            "nativeResolutionKm": 30.0,
            "variables": [
                {"name": "t2m", "units": "K", "semantics": "instant"},
                {"name": "d2m", "units": "K", "semantics": "instant"},
                {"name": "tp", "units": "m", "semantics": "one-hour accumulation ending at valid_time"},
                {"name": "ssrd", "units": "J m**-2", "semantics": "one-hour accumulation ending at valid_time"},
                {"name": "tcc", "units": "0-1", "semantics": "instant"},
                {"name": "u10", "units": "m s**-1", "semantics": "instant"},
                {"name": "v10", "units": "m s**-1", "semantics": "instant"},
            ],
        },
        "window": {
            "id": "civil-local-10-20",
            "startLocalHour": 10,
            "endLocalHour": 20,
            "hourlyIntervalsPerDay": 10,
            "wetHourlyThresholdMm": WET_HOURLY_MM,
        },
        "year": year,
        "retrieval": {
            "requestCount": counter.requests,
            "getCount": counter.gets,
            "headCount": counter.heads,
            "responseBodyBytes": counter.response_bytes,
            "maximumResponseBodyBytes": counter.maximum_response_bytes,
            "logicalArrayReads": logical_reads,
            "decodedArrayBytes": decoded_bytes,
            "sourceScratchPeakBytes": scratch_peak_bytes,
            "sourceScratchEndBytes": scratch_end_bytes,
            "peakProcessRssBytes": peak_rss_bytes(),
            "wallTimeMilliseconds": round((time.monotonic() - started) * 1000),
        },
        "cells": cells,
        "days": days,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--year", type=int, default=2025)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--staging", type=Path, required=True)
    args = parser.parse_args()
    if args.year < 1940 or args.year > 2025:
        parser.error("--year must be between 1940 and 2025")
    if not args.out.is_absolute() or not args.staging.is_absolute():
        parser.error("--out and --staging must be absolute")
    report = run(args.year, args.staging)
    encoded = (json.dumps(report, indent=2, sort_keys=True, allow_nan=False) + "\n").encode("utf-8")
    args.out.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.out.with_suffix(args.out.suffix + ".tmp")
    temporary.write_bytes(encoded)
    os.replace(temporary, args.out)
    print(json.dumps({"out": str(args.out), "bytes": len(encoded), "days": len(report["days"])}, sort_keys=True))


if __name__ == "__main__":
    main()
