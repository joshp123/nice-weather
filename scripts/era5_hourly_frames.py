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
import http.server
import io
import json
import struct
import sys
import threading
import urllib.error
import urllib.request
from typing import Any, BinaryIO

import icechunk
import numpy as np
import zarr


GROUP = "single/spatial"
FIELDS = ("t2m", "d2m", "tp", "ssrd", "tcc", "u10", "v10")
PINNED_REPOSITORY = "s3://earthmover-icechunk-era5/icechunkV2"
PINNED_UPSTREAM = "https://earthmover-icechunk-era5.s3.amazonaws.com/icechunkV2"
PINNED_SNAPSHOT_ID = "C2QSEC9FP3SJ6MT4D94G"
PINNED_TRANSPORT_IDENTITY = (
    PINNED_REPOSITORY,
    PINNED_UPSTREAM,
    PINNED_SNAPSHOT_ID,
)
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
READ_BATCH_HOURS = 4
PRODUCER_SUMMARY_PREFIX = "NICE_WEATHER_ERA5_SUMMARY_V1 "
FORWARDED_REQUEST_HEADERS = (
    "Range",
    "If-Range",
    "If-Match",
    "If-None-Match",
    "If-Modified-Since",
    "If-Unmodified-Since",
)
HOP_BY_HOP_RESPONSE_HEADERS = {
    "connection",
    "transfer-encoding",
    "content-encoding",
}
REDIRECT_STATUS = {301, 302, 303, 307, 308}
DOWNSTREAM_DISCONNECTS = (
    BrokenPipeError,
    ConnectionAbortedError,
    ConnectionResetError,
)


class NoRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args: Any, **_kwargs: Any) -> None:
        return None


UPSTREAM_OPENER = urllib.request.build_opener(NoRedirects)


def validate_transport_identity() -> None:
    if (
        PINNED_REPOSITORY,
        PINNED_UPSTREAM,
        PINNED_SNAPSHOT_ID,
    ) != PINNED_TRANSPORT_IDENTITY:
        raise ValueError("ERA5 transport origin or snapshot identity changed")


class TransferCounter:
    def __init__(
        self,
        *,
        max_requests: int = MAX_REQUESTS,
        max_response_bytes: int = MAX_RESPONSE_BYTES,
        max_total_bytes: int = MAX_TOTAL_BYTES,
    ) -> None:
        self.lock = threading.Lock()
        self.max_requests = max_requests
        self.max_response_bytes = max_response_bytes
        self.max_total_bytes = max_total_bytes
        self.requests = 0
        self.response_bytes = 0

    def start(self) -> None:
        with self.lock:
            self.requests += 1
            if self.requests > self.max_requests:
                raise RuntimeError(
                    f"ERA5 request limit exceeded: {self.requests}"
                )

    def check_declared_size(self, count: int) -> None:
        if count < 0 or count > self.max_response_bytes:
            raise RuntimeError("ERA5 response limit exceeded")
        with self.lock:
            if self.response_bytes + count > self.max_total_bytes:
                raise RuntimeError("ERA5 transfer limit exceeded")

    def reserve(self, response_bytes: int, wanted: int) -> int:
        response_remaining = self.max_response_bytes - response_bytes
        if response_remaining <= 0:
            raise RuntimeError("ERA5 response limit exceeded")
        with self.lock:
            total_remaining = self.max_total_bytes - self.response_bytes
            if total_remaining <= 0:
                raise RuntimeError("ERA5 transfer limit exceeded")
            reserved = min(wanted, response_remaining, total_remaining)
            self.response_bytes += reserved
        return reserved

    def refund(self, count: int) -> None:
        with self.lock:
            self.response_bytes -= count


def proxy_handler(counter: TransferCounter):
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self) -> None:  # noqa: N802
            self._forward("GET")

        def do_HEAD(self) -> None:  # noqa: N802
            self._forward("HEAD")

        def log_message(self, _format: str, *_args: Any) -> None:
            return

        def _end_headers(self) -> bool:
            try:
                self.end_headers()
            except DOWNSTREAM_DISCONNECTS:
                self.close_connection = True
                return False
            return True

        def _plain_error(self, status: int, message: str, method: str) -> None:
            payload = message.encode("utf-8", errors="replace")
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Connection", "close")
            self.close_connection = True
            if not self._end_headers() or method == "HEAD":
                return
            try:
                self.wfile.write(payload)
            except DOWNSTREAM_DISCONNECTS:
                self.close_connection = True

        def _forward(self, method: str) -> None:
            try:
                counter.start()
            except RuntimeError as error:
                self._plain_error(503, str(error), method)
                return

            headers = {
                name: value
                for name in FORWARDED_REQUEST_HEADERS
                if (value := self.headers.get(name))
            }
            request = urllib.request.Request(
                PINNED_UPSTREAM + self.path,
                headers=headers,
                method=method,
            )
            try:
                response = UPSTREAM_OPENER.open(request, timeout=120)
            except urllib.error.HTTPError as error:
                if error.code in REDIRECT_STATUS:
                    error.close()
                    self._plain_error(502, "ERA5 upstream redirected", method)
                    return
                response = error
            except Exception as error:  # pragma: no cover - live transport only
                self._plain_error(502, str(error), method)
                return

            response_bytes = 0
            try:
                content_length = response.headers.get("Content-Length")
                if method != "HEAD" and content_length is not None:
                    try:
                        counter.check_declared_size(int(content_length))
                    except (RuntimeError, ValueError) as error:
                        self._plain_error(502, str(error), method)
                        return

                self.send_response(response.status)
                for name, value in response.headers.items():
                    if name.lower() not in HOP_BY_HOP_RESPONSE_HEADERS:
                        self.send_header(name, value)
                self.send_header("Connection", "close")
                self.close_connection = True
                if not self._end_headers():
                    return
                if method == "HEAD":
                    return

                while True:
                    try:
                        reserved = counter.reserve(
                            response_bytes,
                            1024 * 1024,
                        )
                    except RuntimeError:
                        self.close_connection = True
                        return
                    try:
                        chunk = response.read(reserved)
                    except Exception:
                        counter.refund(reserved)
                        raise
                    counter.refund(reserved - len(chunk))
                    if not chunk:
                        break
                    response_bytes += len(chunk)
                    try:
                        self.wfile.write(chunk)
                    except DOWNSTREAM_DISCONNECTS:
                        self.close_connection = True
                        return
            finally:
                response.close()

    return Handler


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


def read_plane_batch(
    group: zarr.Group,
    start_index: int,
    stop_index: int,
) -> dict[str, np.ndarray]:
    batch_hours = stop_index - start_index
    if (
        start_index < 0
        or stop_index > HOURS
        or batch_hours < 1
        or batch_hours > READ_BATCH_HOURS
    ):
        raise ValueError("ERA5 plane batch must contain one to four consecutive hours")
    result: dict[str, np.ndarray] = {}
    for field in FIELDS:
        planes = np.asarray(group[field][start_index:stop_index], dtype="<f4")
        if planes.shape != (batch_hours, ROWS, COLUMNS) or not np.isfinite(planes).all():
            raise ValueError(
                f"ERA5 {field} hours {start_index}:{stop_index} are incomplete"
            )
        result[field] = planes
    return result


def stream_index_range(
    group: zarr.Group,
    first_index: int,
    last_index: int,
    output: BinaryIO,
) -> tuple[int, int]:
    if first_index < 0 or last_index < first_index or last_index >= HOURS:
        raise ValueError("ERA5 frame index range is invalid")
    frames = output_bytes = 0
    for batch_first in range(first_index, last_index + 1, READ_BATCH_HOURS):
        batch_last = min(batch_first + READ_BATCH_HOURS, last_index + 1)
        valid_times: list[dt.datetime] = []
        for index in range(batch_first, batch_last):
            offset = int(np.asarray(group["valid_time"][index]).item())
            if offset != index:
                raise ValueError(f"ERA5 valid_time index {index} contains {offset}")
            valid_times.append(SOURCE_EPOCH + dt.timedelta(hours=index))
        plane_batch = read_plane_batch(group, batch_first, batch_last)
        for batch_index, valid_time in enumerate(valid_times):
            planes = {field: plane_batch[field][batch_index] for field in FIELDS}
            output_bytes += write_frame(output, valid_time, planes)
            frames += 1
    return frames, output_bytes


def stream(first: dt.datetime, last: dt.datetime, output: BinaryIO) -> dict[str, Any]:
    validate_transport_identity()
    if first > last:
        raise ValueError("first ERA5 frame must not follow the last")
    first_index, last_index = source_index(first), source_index(last)
    counter = TransferCounter()
    server = http.server.ThreadingHTTPServer(
        ("127.0.0.1", 0),
        proxy_handler(counter),
    )
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
        frames, output_bytes = stream_index_range(
            group, first_index, last_index, output
        )
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


def write_summary(summary: dict[str, Any], output: io.TextIOBase) -> None:
    print(file=output)
    print(
        PRODUCER_SUMMARY_PREFIX + json.dumps(summary, sort_keys=True),
        file=output,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--first-valid-time", type=parse_utc_hour, required=True)
    parser.add_argument("--last-valid-time", type=parse_utc_hour, required=True)
    args = parser.parse_args()
    summary = stream(args.first_valid_time, args.last_valid_time, sys.stdout.buffer)
    write_summary(summary, sys.stderr)


if __name__ == "__main__":
    main()
