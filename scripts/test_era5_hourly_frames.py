from __future__ import annotations

import datetime as dt
import hashlib
import io
import struct
import unittest

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


def fake_group() -> dict[str, object]:
    return {
        **{field: FakeArray(field) for field in frames.FIELDS},
        "valid_time": FakeCoordinate(np.arange(frames.HOURS, dtype="<i8")),
        "latitude": FakeCoordinate(np.linspace(90.0, -90.0, frames.ROWS)),
        "longitude": FakeCoordinate(np.arange(frames.COLUMNS) * 0.25),
    }


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
        original = frames.transport.UPSTREAM
        try:
            frames.transport.UPSTREAM = "https://example.invalid/era5"
            with self.assertRaises(ValueError):
                frames.validate_transport_identity()
        finally:
            frames.transport.UPSTREAM = original

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


if __name__ == "__main__":
    unittest.main()
