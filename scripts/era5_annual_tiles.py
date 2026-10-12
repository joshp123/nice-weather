# /// script
# requires-python = ">=3.12"
# dependencies = ["icechunk==2.1.1", "numpy==2.5.1", "numcodecs[pcodec]==0.16.5", "zarr==3.2.1"]
# ///
"""Read each pinned ERA5 temporal tile once for the complete climate window."""

import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import os
import struct
import sys
import time
import resource

import icechunk
import numpy as np
import zarr


ROWS, COLUMNS = 721, 1440
SOURCE_TOTAL_HOURS = 756048
FIELDS = ("t2m", "d2m", "tp", "ssrd", "tcc", "u10", "v10")
PINNED_SNAPSHOT_ID = "C2QSEC9FP3SJ6MT4D94G"
SOURCE_EPOCH = dt.datetime(1940, 1, 1, tzinfo=dt.timezone.utc)
SOURCE_DATASET_SHA256 = bytes.fromhex("060a698cd662cd89387573fa3aef3bd459a9da2815469575980ecd3cca3e10c8")


def validate_layout(group: zarr.Group, chunks: tuple[int, int, int] = (8736, 12, 12)) -> None:
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
        if array.shape != (SOURCE_TOTAL_HOURS, ROWS, COLUMNS) or array.chunks != chunks:
            raise ValueError(f"ERA5 temporal {field} shape or chunks changed")
        if array.dtype != np.dtype("<f4"):
            raise ValueError(f"ERA5 temporal {field} dtype changed")
        if array.attrs.get("units") != units or array.attrs.get("GRIB_stepType") != step_type:
            raise ValueError(f"ERA5 temporal {field} units or step type changed")
        if step_type == "accum" and "1 hour ending at `valid_time`" not in array.attrs.get("accumulation_comment", ""):
            raise ValueError(f"ERA5 temporal {field} accumulation semantics changed")


def validate_coordinates(group: zarr.Group) -> None:
    valid_time = group["valid_time"]
    latitude = group["latitude"]
    longitude = group["longitude"]
    if valid_time.shape != (SOURCE_TOTAL_HOURS,) or not np.issubdtype(valid_time.dtype, np.integer):
        raise ValueError("ERA5 valid_time axis changed")
    if latitude.shape != (ROWS,) or longitude.shape != (COLUMNS,):
        raise ValueError("ERA5 coordinate shape changed")
    latitudes = np.asarray(latitude[:])
    longitudes = np.asarray(longitude[:])
    if not np.array_equal(latitudes, np.linspace(90.0, -90.0, ROWS)):
        raise ValueError("ERA5 latitude axis changed")
    if not np.array_equal(longitudes, np.arange(COLUMNS) * 0.25):
        raise ValueError("ERA5 longitude axis changed")


def source_index(valid_time: dt.datetime) -> int:
    seconds = (valid_time - SOURCE_EPOCH).total_seconds()
    if seconds < 0 or seconds % 3600:
        raise ValueError("ERA5 valid time is outside the hourly source axis")
    index = int(seconds // 3600)
    if index >= SOURCE_TOTAL_HOURS:
        raise ValueError("ERA5 valid time is outside the hourly source axis")
    return index


FIRST = dt.datetime(2005, 12, 31, tzinfo=dt.timezone.utc)
LAST = dt.datetime(2026, 1, 2, tzinfo=dt.timezone.utc)
FIRST_INDEX = source_index(FIRST)
STOP_INDEX = source_index(LAST)
HOURS = STOP_INDEX - FIRST_INDEX
HEADER_BYTES = 256
ACQUISITION_SHA256 = hashlib.sha256(b"earthmover-era5-annual-tile-v1\nlayout=single/temporal\n" + SOURCE_DATASET_SHA256).digest()


class CompressedSourceCounter(zarr.storage.WrapperStore):
    compressed_bytes = 0
    chunks = 0

    async def get(self, key, prototype, byte_range=None):
        value = await self._store.get(key, prototype, byte_range)
        if value is not None and "/c/" in key:
            self.compressed_bytes += len(value)
            self.chunks += 1
        return value


def tile_header(row, column, rows, columns):
    header = bytearray(HEADER_BYTES)
    header[:8] = b"NWE5AT01"
    struct.pack_into("<HHIIIIIIq", header, 8, 1, HEADER_BYTES, row, column,
                     rows, columns, HOURS, len(FIELDS), int(FIRST.timestamp()))
    header[44:64] = PINNED_SNAPSHOT_ID.encode()
    header[64:96] = ACQUISITION_SHA256
    return header


def download_tile(tile, output_directory, arrays, store, executor):
    row, column = tile["row"], tile["column"]
    rows, columns = min(12, ROWS-row), min(12, COLUMNS-column)
    path = os.path.join(output_directory, f"r{row:03d}-c{column:04d}.raw")
    tile_started = time.monotonic()
    tile_cpu = time.process_time()
    first_bytes, first_chunks = store.compressed_bytes, store.chunks
    shape = (len(FIELDS), HOURS, rows, columns)
    partial = path + ".partial"
    with open(partial, "wb") as output:
        output.write(tile_header(row, column, rows, columns))
        output.truncate(HEADER_BYTES + int(np.prod(shape)) * 4 + 32)
    mapped = np.memmap(partial, dtype="<f4", mode="r+", offset=HEADER_BYTES, shape=shape)

    def read_field(field_index):
        field = FIELDS[field_index]
        values = np.asarray(arrays[field][FIRST_INDEX:STOP_INDEX, row:row+rows, column:column+columns], dtype="<f4")
        if values.shape != shape[1:] or not np.isfinite(values).all():
            raise ValueError(f"ERA5 temporal {field} tile is incomplete")
        mapped[field_index] = values
        mapped.flush()
        print(f"ERA5 tile r{row}-c{column}: {field} complete in {time.monotonic()-tile_started:.1f}s", file=sys.stderr, flush=True)

    list(executor.map(read_field, range(len(FIELDS))))
    mapped.flush()
    del mapped
    digest = hashlib.sha256()
    with open(partial, "r+b") as output:
        remaining = HEADER_BYTES + int(np.prod(shape)) * 4
        while remaining:
            chunk = output.read(min(1024**2, remaining))
            digest.update(chunk)
            remaining -= len(chunk)
        output.write(digest.digest())
        output.flush()
        os.fsync(output.fileno())
    os.replace(partial, path)
    return {"row": row, "column": column, "path": path,
            "elapsedSeconds": time.monotonic()-tile_started,
            "decodedBytes": int(np.prod(shape))*4,
            "compressedBytes": store.compressed_bytes-first_bytes,
            "sourceChunks": store.chunks-first_chunks,
            "cpuSeconds": time.process_time()-tile_cpu,
            "peakRSSBytes": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if sys.platform == "darwin" else 1024)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tiles", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    with open(args.tiles) as input_file:
        tiles = json.load(input_file)
    os.makedirs(args.out, exist_ok=True)
    started = time.monotonic()
    zarr.config.set({"async.concurrency": 8})
    repo = icechunk.Repository.open(icechunk.s3_storage(
        bucket="earthmover-icechunk-era5", prefix="icechunkV2", region="us-east-1",
        anonymous=True, network_stream_timeout_seconds=60))
    store = CompressedSourceCounter(repo.readonly_session(snapshot_id=PINNED_SNAPSHOT_ID).store)
    group = zarr.open_group(store, mode="r")["single/temporal"]
    print(f"ERA5 pinned temporal repository ready in {time.monotonic()-started:.1f}s", file=sys.stderr, flush=True)
    validate_coordinates(group)
    validate_layout(group, chunks=(8736, 12, 12))
    arrays = {field: group[field] for field in FIELDS}
    # Metadata and array handles remain alive across the entire world run.
    with concurrent.futures.ThreadPoolExecutor(max_workers=7) as executor, concurrent.futures.ThreadPoolExecutor(max_workers=1) as prefetch:
        future = prefetch.submit(download_tile, tiles[0], args.out, arrays, store, executor)
        for index in range(len(tiles)):
            completed = future.result()
            print(json.dumps(completed), flush=True)
            # One following tile downloads while Go reduces this completed tile.
            if index + 1 < len(tiles):
                future = prefetch.submit(download_tile, tiles[index+1], args.out, arrays, store, executor)
            if sys.stdin.readline().strip() != "ok":
                raise RuntimeError("ERA5 tile consumer stopped before checkpoint")


if __name__ == "__main__":
    main()
