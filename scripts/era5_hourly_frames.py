# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "icechunk==2.1.1",
#   "numpy==2.5.1",
#   "numcodecs[pcodec]==0.16.5",
#   "zarr==3.2.1",
# ]
# ///

"""Stream pinned hourly ERA5 fields into the fixed Go ingestion frame."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import io
import json
import struct
import sys
import threading
from typing import Any, BinaryIO

import icechunk
import numpy as np
import zarr

import era5_climate_canary as transport


GROUP = "single/spatial"
FIELDS = ("t2m", "d2m", "tp", "ssrd", "tcc", "u10", "v10")
PINNED_REPOSITORY = "s3://earthmover-icechunk-era5/icechunkV2"
PINNED_UPSTREAM = "https://earthmover-icechunk-era5.s3.amazonaws.com/icechunkV2"
PINNED_SNAPSHOT_ID = "C2QSEC9FP3SJ6MT4D94G"
ROWS = 721
COLUMNS = 1440
HOURS = 756048
HEADER_BYTES = 128
TRAILER_BYTES = 32
MAGIC = b"NWE5HR01"
SCHEMA_VERSION = 1
SOURCE_EPOCH = dt.datetime(1940, 1, 1, tzinfo=dt.timezone.utc)
SOURCE_CONTRACT = (
    "earthmover-era5-hourly-frame-v1\n"
    f"repository={PINNED_REPOSITORY}\n"
    f"upstream={PINNED_UPSTREAM}\n"
    f"snapshot={PINNED_SNAPSHOT_ID}\n"
    f"group={GROUP}\n"
    "grid=0.25deg,721x1440,lat90..-90,lon0..359.75\n"
    "fields=t2m[K,instant];d2m[K,instant];tp[m,1h-ending];"
    "ssrd[J m**-2,1h-ending];tcc[0-1,instant];"
    "u10[m s**-1,instant];v10[m s**-1,instant]\n"
)
SOURCE_CONTRACT_SHA256 = hashlib.sha256(SOURCE_CONTRACT.encode()).digest()
MAX_REQUESTS = 6_000
MAX_RESPONSE_BYTES = 4 * 1024 * 1024
MAX_TOTAL_BYTES = 14 * 1024 * 1024 * 1024


def validate_transport_identity() -> None:
    if (
        transport.REPOSITORY != PINNED_REPOSITORY
        or transport.UPSTREAM != PINNED_UPSTREAM
        or transport.SNAPSHOT_ID != PINNED_SNAPSHOT_ID
    ):
        raise ValueError("ERA5 transport origin or snapshot identity changed")


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
        if array.shape != (HOURS, ROWS, COLUMNS) or array.chunks != (1, ROWS, COLUMNS):
            raise ValueError(f"ERA5 spatial {field} shape or chunks changed")
        if array.dtype != np.dtype("<f4"):
            raise ValueError(f"ERA5 spatial {field} dtype changed")
        if array.attrs.get("units") != units or array.attrs.get("GRIB_stepType") != step_type:
            raise ValueError(f"ERA5 spatial {field} units or step type changed")
        if step_type == "accum" and "1 hour ending at `valid_time`" not in array.attrs.get("accumulation_comment", ""):
            raise ValueError(f"ERA5 spatial {field} accumulation semantics changed")


def validate_coordinates(group: zarr.Group) -> None:
    valid_time = group["valid_time"]
    latitude = group["latitude"]
    longitude = group["longitude"]
    if valid_time.shape != (HOURS,) or not np.issubdtype(valid_time.dtype, np.integer):
        raise ValueError("ERA5 valid_time axis changed")
    if latitude.shape != (ROWS,) or longitude.shape != (COLUMNS,):
        raise ValueError("ERA5 coordinate shape changed")
    latitudes = np.asarray(latitude[:])
    longitudes = np.asarray(longitude[:])
    if not np.array_equal(latitudes, np.linspace(90.0, -90.0, ROWS)):
        raise ValueError("ERA5 latitude axis changed")
    if not np.array_equal(longitudes, np.arange(COLUMNS) * 0.25):
        raise ValueError("ERA5 longitude axis changed")


def parse_utc_hour(value: str) -> dt.datetime:
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise argparse.ArgumentTypeError("time must be an ISO-8601 UTC hour") from error
    if parsed.tzinfo is None or parsed.utcoffset() != dt.timedelta(0) or any((parsed.minute, parsed.second, parsed.microsecond)):
        raise argparse.ArgumentTypeError("time must be an ISO-8601 UTC hour")
    return parsed.astimezone(dt.timezone.utc)


def source_index(valid_time: dt.datetime) -> int:
    seconds = (valid_time - SOURCE_EPOCH).total_seconds()
    if seconds < 0 or seconds % 3600:
        raise ValueError("ERA5 valid time is outside the hourly source axis")
    index = int(seconds // 3600)
    if index >= HOURS:
        raise ValueError("ERA5 valid time is outside the hourly source axis")
    return index


def frame_header(valid_time: dt.datetime, rows: int, columns: int) -> bytes:
    if valid_time.tzinfo is None or valid_time.utcoffset() != dt.timedelta(0) or any((valid_time.minute, valid_time.second, valid_time.microsecond)):
        raise ValueError("ERA5 frame time must be a whole UTC hour")
    if rows <= 0 or columns <= 0 or rows > ROWS or columns > COLUMNS:
        raise ValueError("ERA5 frame shape is invalid")
    payload_bytes = len(FIELDS) * rows * columns * 4
    header = bytearray(HEADER_BYTES)
    header[:8] = MAGIC
    struct.pack_into("<HHHHHHqQ", header, 8, SCHEMA_VERSION, HEADER_BYTES, rows, columns, len(FIELDS), 4, int(valid_time.timestamp()), payload_bytes)
    header[36:56] = PINNED_SNAPSHOT_ID.encode("ascii")
    header[56:88] = SOURCE_CONTRACT_SHA256
    return bytes(header)


def write_frame(output: BinaryIO, valid_time: dt.datetime, planes: dict[str, np.ndarray]) -> int:
    if tuple(planes) != FIELDS:
        raise ValueError("ERA5 frame fields are missing or out of order")
    first = np.asarray(planes[FIELDS[0]])
    if first.ndim != 2:
        raise ValueError("ERA5 frame planes must be two-dimensional")
    rows, columns = first.shape
    output.write(frame_header(valid_time, rows, columns))
    digest = hashlib.sha256()
    payload_bytes = 0
    for field in FIELDS:
        plane = np.asarray(planes[field], dtype="<f4", order="C")
        if plane.shape != (rows, columns) or not np.isfinite(plane).all():
            raise ValueError(f"ERA5 frame {field} is incomplete")
        encoded = memoryview(plane).cast("B")
        output.write(encoded)
        digest.update(encoded)
        payload_bytes += encoded.nbytes
    output.write(digest.digest())
    return HEADER_BYTES + payload_bytes + TRAILER_BYTES


def read_planes(group: zarr.Group, index: int) -> dict[str, np.ndarray]:
    result: dict[str, np.ndarray] = {}
    for field in FIELDS:
        plane = np.asarray(group[field][index], dtype="<f4")
        if plane.shape != (ROWS, COLUMNS) or not np.isfinite(plane).all():
            raise ValueError(f"ERA5 {field} hour {index} is incomplete")
        result[field] = plane
    return result


def stream(first: dt.datetime, last: dt.datetime, output: BinaryIO) -> dict[str, Any]:
    validate_transport_identity()
    if first > last:
        raise ValueError("first ERA5 frame must not follow the last")
    first_index, last_index = source_index(first), source_index(last)
    transport.MAX_REQUESTS = MAX_REQUESTS
    transport.MAX_RESPONSE_BYTES = MAX_RESPONSE_BYTES
    transport.MAX_TOTAL_BYTES = MAX_TOTAL_BYTES
    counter = transport.TransferCounter()
    server = transport.http.server.ThreadingHTTPServer(("127.0.0.1", 0), transport.proxy_handler(counter))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    frames = output_bytes = 0
    try:
        storage = icechunk.http_storage(f"http://127.0.0.1:{server.server_port}")
        repository = icechunk.Repository.open(storage)
        session = repository.readonly_session(snapshot_id=PINNED_SNAPSHOT_ID)
        group = zarr.open_group(store=session.store, mode="r")[GROUP]
        validate_layout(group)
        validate_coordinates(group)
        for index in range(first_index, last_index + 1):
            offset = int(np.asarray(group["valid_time"][index]).item())
            if offset != index:
                raise ValueError(f"ERA5 valid_time index {index} contains {offset}")
            valid_time = SOURCE_EPOCH + dt.timedelta(hours=index)
            output_bytes += write_frame(output, valid_time, read_planes(group, index))
            frames += 1
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    return {
        "frames": frames,
        "frameBytes": output_bytes,
        "requests": counter.requests,
        "responseBytes": counter.response_bytes,
        "repository": PINNED_REPOSITORY,
        "upstream": PINNED_UPSTREAM,
        "snapshotId": PINNED_SNAPSHOT_ID,
        "contractSha256": SOURCE_CONTRACT_SHA256.hex(),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--first-valid-time", type=parse_utc_hour, required=True)
    parser.add_argument("--last-valid-time", type=parse_utc_hour, required=True)
    args = parser.parse_args()
    summary = stream(args.first_valid_time, args.last_valid_time, sys.stdout.buffer)
    print(json.dumps(summary, sort_keys=True), file=sys.stderr)


if __name__ == "__main__":
    main()
