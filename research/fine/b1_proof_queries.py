"""Freeze the thirteen city queries and 384 randomly sampled supported land pixels.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
These coordinates test published bytes; they do not fit the weather model.
"""
import argparse
import json
import pathlib

import numpy as np

from native_normals import file_sha256


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=pathlib.Path, required=True)
    args = parser.parse_args()
    root = args.data_root
    land_path = root/'fine-build/land-support-v2/land.u8'
    city_path = root/'fine-validation/step-a-settlement/inputs/city_queries.json'
    land = np.memmap(land_path, mode='r', dtype=np.uint8, shape=(21600,43200))
    queries = json.loads(city_path.read_text())
    random = np.random.default_rng(20261018)
    pixels = set()
    while len(pixels) < 384:
        row, column = int(random.integers(21600)), int(random.integers(43200))
        if land[row,column]: pixels.add((row,column))
    for index, (row,column) in enumerate(sorted(pixels)):
        queries.append(dict(slug=f'random-{index:03d}', name=f'Land pixel {row},{column}',
                            lat=90-(row+.5)/120, lon=-180+(column+.5)/120,
                            pixel_row=row, pixel_column=column))
    output = root/'fine-validation/b1-worldwide'
    output.mkdir(parents=True, exist_ok=True)
    path = output/'queries.json'
    path.write_text(json.dumps(queries, indent=2)+'\n')
    (output/'queries-provenance.json').write_text(json.dumps(dict(
        written_by='AI, GPT-6.1 Sol, 2026.10.18', seed=20261018,
        city_queries=13, random_supported_pixels=384,
        sampling='Uniform random global grid pixels, rejecting unsupported pixels; containing pixel centres.',
        city_sha256=file_sha256(city_path), land_sha256=file_sha256(land_path),
        queries_sha256=file_sha256(path)), indent=2)+'\n')
    print(path)


if __name__ == '__main__': main()
