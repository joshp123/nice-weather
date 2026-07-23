from __future__ import annotations

import datetime as dt
import hashlib
import http.client
import http.server
import io
import struct
import threading
import unittest
from contextlib import contextmanager
from unittest import mock

import numpy as np

import era5_hourly_frames as frames


UTC = dt.timezone.utc


class FakeArray:
    def __init__(self, field: str) -> None:
        self.shape = (756048, frames.ROWS, frames.COLUMNS)
        self.chunks = (1, frames.ROWS, frames.COLUMNS)
        self.dtype = np.dtype("<f4")
        self.attrs = {
            "units": {
                "t2m": "K", "d2m": "K", "tp": "m", "ssrd": "J m**-2",
                "tcc": "(0 - 1)", "u10": "m s**-1", "v10": "m s**-1",
            }[field],
            "GRIB_stepType": "accum" if field in {"tp", "ssrd"} else "instant",
            "accumulation_comment": "1 hour ending at `valid_time`",
        }


class FakeCoordinate:
    def __init__(self, values: np.ndarray) -> None:
        self.values = values
        self.shape = values.shape
        self.dtype = values.dtype

    def __getitem__(self, key: object) -> np.ndarray:
        return self.values[key]


class FakeSpatialArray:
    def __init__(self, values: np.ndarray) -> None:
        self.values = values
        self.reads: list[object] = []

    def __getitem__(self, key: object) -> np.ndarray:
        self.reads.append(key)
        return self.values[key]


def fake_group() -> dict[str, object]:
    return {
        **{field: FakeArray(field) for field in frames.FIELDS},
        "valid_time": FakeCoordinate(np.arange(frames.HOURS, dtype="<i8")),
        "latitude": FakeCoordinate(np.linspace(90.0, -90.0, frames.ROWS)),
        "longitude": FakeCoordinate(np.arange(frames.COLUMNS) * 0.25),
    }


def fake_stream_group(hours: int, rows: int, columns: int) -> dict[str, object]:
    group: dict[str, object] = {
        "valid_time": FakeCoordinate(np.arange(hours, dtype="<i8")),
    }
    for field_index, field in enumerate(frames.FIELDS):
        values = np.empty((hours, rows, columns), dtype="<f4")
        for hour in range(hours):
            values[hour] = field_index * 100 + hour
        group[field] = FakeSpatialArray(values)
    return group


@contextmanager
def running_server(handler: type[http.server.BaseHTTPRequestHandler]):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def proxy_request(
    server: http.server.ThreadingHTTPServer,
    method: str,
    path: str,
    headers: dict[str, str] | None = None,
) -> tuple[int, dict[str, str], bytes]:
    connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
    connection.request(method, path, headers=headers or {})
    response = connection.getresponse()
    result = response.status, dict(response.getheaders()), response.read()
    connection.close()
    return result


class FrameTests(unittest.TestCase):
    def test_fixed_frame_binds_source_time_shape_order_and_payload(self) -> None:
        valid_time = dt.datetime(2025, 11, 1, 4, tzinfo=UTC)
        planes = {
            field: np.full((2, 3), index + 0.25, dtype="<f4")
            for index, field in enumerate(frames.FIELDS)
        }
        output = io.BytesIO()
        written = frames.write_frame(output, valid_time, planes)
        encoded = output.getvalue()
        payload = encoded[frames.HEADER_BYTES:-frames.TRAILER_BYTES]
        self.assertEqual(written, len(encoded))
        self.assertEqual(encoded[:8], frames.MAGIC)
        self.assertEqual(struct.unpack_from("<HHHHHH", encoded, 8), (1, 128, 2, 3, 7, 4))
        self.assertEqual(struct.unpack_from("<q", encoded, 20)[0], int(valid_time.timestamp()))
        self.assertEqual(struct.unpack_from("<Q", encoded, 28)[0], len(payload))
        self.assertEqual(encoded[36:56].decode(), frames.PINNED_SNAPSHOT_ID)
        self.assertEqual(encoded[56:88], frames.SOURCE_CONTRACT_SHA256)
        self.assertEqual(encoded[88:128], bytes(40))
        self.assertEqual(encoded[-32:], hashlib.sha256(payload).digest())

    def test_layout_rejects_units_shape_dtype_and_accumulation_drift(self) -> None:
        group = fake_group()
        frames.validate_layout(group)
        for mutate in (
            lambda: setattr(group["t2m"], "shape", (1, 2, 3)),
            lambda: group["d2m"].attrs.__setitem__("units", "C"),
            lambda: setattr(group["tcc"], "dtype", np.dtype(">f4")),
            lambda: group["tp"].attrs.__setitem__("accumulation_comment", "daily"),
        ):
            group = fake_group()
            mutate()
            with self.assertRaises(ValueError):
                frames.validate_layout(group)

    def test_transport_identity_rejects_origin_or_snapshot_drift(self) -> None:
        frames.validate_transport_identity()
        with mock.patch.object(
            frames,
            "PINNED_UPSTREAM",
            "https://example.invalid/era5",
        ):
            with self.assertRaises(ValueError):
                frames.validate_transport_identity()

    def test_proxy_forwards_get_head_range_validators_and_response(self) -> None:
        received: list[dict[str, str]] = []
        payload = b"2345"

        class Upstream(http.server.BaseHTTPRequestHandler):
            def _respond(self, body: bool) -> None:
                request = {"method": self.command, "path": self.path}
                request.update(
                    {
                        name: self.headers.get(name, "")
                        for name in frames.FORWARDED_REQUEST_HEADERS
                    }
                )
                received.append(request)
                if self.path == "/unchanged":
                    self.send_response(304)
                    self.send_header("ETag", '"fixed-etag"')
                    self.end_headers()
                    return
                self.send_response(206)
                self.send_header("Content-Length", str(len(payload)))
                self.send_header("Content-Range", "bytes 2-5/10")
                self.send_header("ETag", '"fixed-etag"')
                self.end_headers()
                if body:
                    self.wfile.write(payload)

            def do_GET(self) -> None:  # noqa: N802
                self._respond(True)

            def do_HEAD(self) -> None:  # noqa: N802
                self._respond(False)

            def log_message(self, _format: str, *_args: object) -> None:
                return

        counter = frames.TransferCounter()
        headers = {
            "Range": "bytes=2-5",
            "If-Range": '"fixed-etag"',
            "If-Match": '"fixed-etag"',
            "If-None-Match": '"other-etag"',
            "If-Modified-Since": "Wed, 22 Jul 2026 11:00:00 GMT",
            "If-Unmodified-Since": "Wed, 22 Jul 2026 13:00:00 GMT",
        }
        with running_server(Upstream) as upstream:
            with running_server(frames.proxy_handler(counter)) as proxy:
                upstream_url = f"http://127.0.0.1:{upstream.server_port}"
                with mock.patch.object(
                    frames,
                    "PINNED_UPSTREAM",
                    upstream_url,
                ):
                    status, response_headers, body = proxy_request(
                        proxy,
                        "GET",
                        "/chunk?part=1",
                        headers,
                    )
                    self.assertEqual((status, body), (206, payload))
                    self.assertEqual(
                        response_headers["Content-Range"],
                        "bytes 2-5/10",
                    )
                    self.assertEqual(
                        response_headers["ETag"],
                        '"fixed-etag"',
                    )
                    status, _, body = proxy_request(
                        proxy,
                        "HEAD",
                        "/metadata",
                    )
                    self.assertEqual((status, body), (206, b""))
                    status, response_headers, body = proxy_request(
                        proxy,
                        "GET",
                        "/unchanged",
                        {"If-None-Match": '"fixed-etag"'},
                    )
                    self.assertEqual((status, body), (304, b""))
                    self.assertEqual(
                        response_headers["ETag"],
                        '"fixed-etag"',
                    )

            self.assertEqual(
                received[0],
                {"method": "GET", "path": "/chunk?part=1", **headers},
            )
            self.assertEqual(received[1]["method"], "HEAD")
            self.assertEqual(received[1]["path"], "/metadata")
            self.assertEqual(received[2]["If-None-Match"], '"fixed-etag"')
            self.assertEqual(counter.requests, 3)
            self.assertEqual(counter.response_bytes, len(payload))

    def test_transfer_counter_enforces_request_response_and_total_caps(self) -> None:
        request_counter = frames.TransferCounter(max_requests=1)
        request_counter.start()
        with self.assertRaisesRegex(RuntimeError, "request limit"):
            request_counter.start()

        response_counter = frames.TransferCounter(
            max_response_bytes=4,
            max_total_bytes=6,
        )
        response_counter.check_declared_size(4)
        with self.assertRaisesRegex(RuntimeError, "response limit"):
            response_counter.check_declared_size(5)
        self.assertEqual(response_counter.reserve(0, 4), 4)
        with self.assertRaisesRegex(RuntimeError, "response limit"):
            response_counter.reserve(4, 1)
        self.assertEqual(response_counter.reserve(0, 2), 2)
        with self.assertRaisesRegex(RuntimeError, "transfer limit"):
            response_counter.reserve(0, 1)
        response_counter.refund(2)
        self.assertEqual(response_counter.response_bytes, 4)

    def test_transfer_reservations_bound_concurrent_reads(self) -> None:
        counter = frames.TransferCounter(
            max_response_bytes=4,
            max_total_bytes=5,
        )
        barrier = threading.Barrier(3)
        reserved: list[int] = []

        def reserve() -> None:
            barrier.wait()
            reserved.append(counter.reserve(0, 4))

        workers = [threading.Thread(target=reserve) for _ in range(2)]
        for worker in workers:
            worker.start()
        barrier.wait()
        for worker in workers:
            worker.join(timeout=5)
        self.assertEqual(sorted(reserved), [1, 4])
        self.assertEqual(counter.response_bytes, 5)

    def test_proxy_closes_oversized_upstream_response_without_fetching_body(
        self,
    ) -> None:
        body_read = False
        upstream_closed = False

        class FakeResponse:
            status = 200
            headers = {"Content-Length": "5"}

            def read(self, _count: int) -> bytes:
                nonlocal body_read
                body_read = True
                return b"12345"

            def close(self) -> None:
                nonlocal upstream_closed
                upstream_closed = True

        counter = frames.TransferCounter(max_response_bytes=4)
        with running_server(frames.proxy_handler(counter)) as proxy:
            with mock.patch.object(
                frames.UPSTREAM_OPENER,
                "open",
                return_value=FakeResponse(),
            ):
                status, _, body = proxy_request(proxy, "GET", "/oversized")
                self.assertEqual(status, 502)
                self.assertIn(b"response limit exceeded", body)
            self.assertFalse(body_read)
            self.assertTrue(upstream_closed)
            self.assertEqual(counter.response_bytes, 0)

    def test_missing_or_false_length_cannot_read_past_the_cap(self) -> None:
        for declared_length, expected_body in ((None, b"xxxx"), ("2", b"xx")):
            with self.subTest(content_length=declared_length):
                read_sizes: list[int] = []
                upstream_closed = False

                class FakeResponse:
                    status = 200
                    headers = (
                        {}
                        if declared_length is None
                        else {"Content-Length": declared_length}
                    )

                    def read(self, count: int) -> bytes:
                        read_sizes.append(count)
                        return b"x" * count

                    def close(self) -> None:
                        nonlocal upstream_closed
                        upstream_closed = True

                counter = frames.TransferCounter(max_response_bytes=4)
                with running_server(frames.proxy_handler(counter)) as proxy:
                    with mock.patch.object(
                        frames.UPSTREAM_OPENER,
                        "open",
                        return_value=FakeResponse(),
                    ):
                        status, _, body = proxy_request(
                            proxy,
                            "GET",
                            "/unknown-size",
                        )
                self.assertEqual((status, body), (200, expected_body))
                self.assertEqual(read_sizes, [4])
                self.assertEqual(counter.response_bytes, 4)
                self.assertTrue(upstream_closed)

    def test_redirect_target_is_never_contacted(self) -> None:
        target_hits = 0

        class Target(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                nonlocal target_hits
                target_hits += 1
                self.send_response(200)
                self.end_headers()

            def log_message(self, _format: str, *_args: object) -> None:
                return

        with running_server(Target) as target:
            target_url = f"http://127.0.0.1:{target.server_port}/moved"

            class Redirect(http.server.BaseHTTPRequestHandler):
                def do_GET(self) -> None:  # noqa: N802
                    self.send_response(302)
                    self.send_header("Location", target_url)
                    self.end_headers()

                def log_message(
                    self,
                    _format: str,
                    *_args: object,
                ) -> None:
                    return

            with running_server(Redirect) as redirect:
                counter = frames.TransferCounter()
                with running_server(
                    frames.proxy_handler(counter)
                ) as proxy:
                    redirect_url = (
                        f"http://127.0.0.1:{redirect.server_port}"
                    )
                    with mock.patch.object(
                        frames,
                        "PINNED_UPSTREAM",
                        redirect_url,
                    ):
                        status, _, body = proxy_request(
                            proxy,
                            "GET",
                            "/source",
                        )
        self.assertEqual(status, 502)
        self.assertIn(b"upstream redirected", body)
        self.assertEqual(target_hits, 0)

    def test_expected_downstream_header_disconnect_is_quiet(self) -> None:
        counter = frames.TransferCounter()
        handler = frames.proxy_handler(counter)
        instance = object.__new__(handler)
        instance.close_connection = False
        with mock.patch.object(
            instance,
            "end_headers",
            side_effect=BrokenPipeError,
        ):
            self.assertFalse(instance._end_headers())
        self.assertTrue(instance.close_connection)

    def test_coordinates_bind_grid_order_and_hourly_axis(self) -> None:
        group = fake_group()
        frames.validate_coordinates(group)
        for mutate in (
            lambda: setattr(group["valid_time"], "shape", (1,)),
            lambda: group["latitude"].values.__setitem__(0, 89.75),
            lambda: group["longitude"].values.__setitem__(1, 0.5),
        ):
            group = fake_group()
            mutate()
            with self.assertRaises(ValueError):
                frames.validate_coordinates(group)

    def test_writer_rejects_missing_reordered_nonfinite_or_mismatched_planes(self) -> None:
        valid_time = dt.datetime(2025, 11, 1, 4, tzinfo=UTC)
        good = {field: np.zeros((2, 3), dtype="<f4") for field in frames.FIELDS}
        cases = [
            {field: value for field, value in list(good.items())[1:]},
            dict(reversed(list(good.items()))),
            {**good, "t2m": np.full((2, 3), np.nan, dtype="<f4")},
            {**good, "v10": np.zeros((3, 2), dtype="<f4")},
        ]
        for planes in cases:
            with self.assertRaises(ValueError):
                frames.write_frame(io.BytesIO(), valid_time, planes)

    def test_time_parser_and_source_index_require_a_whole_utc_hour(self) -> None:
        self.assertEqual(
            frames.source_index(dt.datetime(1940, 1, 1, 1, tzinfo=UTC)),
            1,
        )
        with self.assertRaises(ValueError):
            frames.source_index(frames.SOURCE_EPOCH + dt.timedelta(hours=frames.HOURS))
        for value in ("2025-11-01T04:30:00Z", "2025-11-01T04:00:00+01:00", "bad"):
            with self.assertRaises(Exception):
                frames.parse_utc_hour(value)

    def test_summary_writer_emits_one_prefixed_final_line(self) -> None:
        output = io.StringIO()
        frames.write_summary({"requests": 2, "frames": 3}, output)
        self.assertEqual(
            output.getvalue(),
            f'\n{frames.PRODUCER_SUMMARY_PREFIX}{{"frames": 3, "requests": 2}}\n',
        )
        self.assertEqual(output.getvalue().count(frames.PRODUCER_SUMMARY_PREFIX), 1)

    def test_summary_writer_establishes_line_after_partial_diagnostic(self) -> None:
        output = io.StringIO("Warning without a newline")
        output.seek(0, io.SEEK_END)
        frames.write_summary({"frames": 3}, output)
        self.assertEqual(
            output.getvalue(),
            f"Warning without a newline\n"
            f'{frames.PRODUCER_SUMMARY_PREFIX}{{"frames": 3}}\n',
        )

    def test_stream_reads_fixed_four_hour_batches_with_every_tail_size(self) -> None:
        rows, columns = 2, 3
        with mock.patch.multiple(frames, ROWS=rows, COLUMNS=columns):
            for tail_hours in range(1, frames.READ_BATCH_HOURS + 1):
                with self.subTest(tail_hours=tail_hours):
                    hours = frames.READ_BATCH_HOURS + tail_hours
                    group = fake_stream_group(hours, rows, columns)
                    output = io.BytesIO()
                    count, written = frames.stream_index_range(
                        group, 0, hours - 1, output
                    )
                    self.assertEqual(count, hours)
                    self.assertEqual(written, len(output.getvalue()))
                    expected_reads = [
                        slice(0, frames.READ_BATCH_HOURS),
                        slice(frames.READ_BATCH_HOURS, hours),
                    ]
                    for field in frames.FIELDS:
                        self.assertEqual(group[field].reads, expected_reads)

    def test_batched_stream_is_byte_exact_and_keeps_hour_and_field_order(self) -> None:
        rows, columns, hours = 2, 3, 7
        with mock.patch.multiple(frames, ROWS=rows, COLUMNS=columns):
            batched_group = fake_stream_group(hours, rows, columns)
            batched = io.BytesIO()
            count, written = frames.stream_index_range(
                batched_group, 0, hours - 1, batched
            )

            reference_group = fake_stream_group(hours, rows, columns)
            reference = io.BytesIO()
            for index in range(hours):
                planes = {
                    field: np.asarray(reference_group[field][index], dtype="<f4")
                    for field in frames.FIELDS
                }
                frames.write_frame(
                    reference,
                    frames.SOURCE_EPOCH + dt.timedelta(hours=index),
                    planes,
                )

            self.assertEqual(count, hours)
            self.assertEqual(written, len(batched.getvalue()))
            self.assertEqual(batched.getvalue(), reference.getvalue())
            frame_bytes = (
                frames.HEADER_BYTES
                + len(frames.FIELDS) * rows * columns * 4
                + frames.TRAILER_BYTES
            )
            encoded = batched.getvalue()
            for index in range(hours):
                frame = encoded[
                    index * frame_bytes:(index + 1) * frame_bytes
                ]
                self.assertEqual(
                    struct.unpack_from("<q", frame, 20)[0],
                    int(
                        (
                            frames.SOURCE_EPOCH + dt.timedelta(hours=index)
                        ).timestamp()
                    ),
                )
                for field_index in range(len(frames.FIELDS)):
                    value_at = (
                        frames.HEADER_BYTES + field_index * rows * columns * 4
                    )
                    self.assertEqual(
                        struct.unpack_from("<f", frame, value_at)[0],
                        field_index * 100 + index,
                    )

    def test_plane_batch_rejects_invalid_bounds_shape_and_nonfinite_values(self) -> None:
        rows, columns = 2, 3
        with mock.patch.multiple(frames, ROWS=rows, COLUMNS=columns):
            group = fake_stream_group(5, rows, columns)
            for first, last in ((-1, 1), (0, 0), (0, 5)):
                with self.subTest(first=first, last=last):
                    with self.assertRaises(ValueError):
                        frames.read_plane_batch(group, first, last)

            malformed = fake_stream_group(4, rows, columns)
            malformed["tcc"].values = malformed["tcc"].values[:, :, :columns - 1]
            with self.assertRaises(ValueError):
                frames.read_plane_batch(malformed, 0, 4)

            nonfinite = fake_stream_group(4, rows, columns)
            nonfinite["d2m"].values[2, 1, 1] = np.nan
            with self.assertRaises(ValueError):
                frames.read_plane_batch(nonfinite, 0, 4)


if __name__ == "__main__":
    unittest.main()
