#!/usr/bin/env python3
"""Rebuild the bundled poster geometry and fonts using only Python's standard library."""

import argparse
import hashlib
import json
from pathlib import Path
import struct
import urllib.request


NATURAL_EARTH_COMMIT = "f1890d9f152c896d250a77557a5751a93d494776"
GOOGLE_FONTS_COMMIT = "47d54ca1ebb14fd864695f1d39dc734f39017c33"
NATURAL_EARTH_URL = f"https://raw.githubusercontent.com/nvkelso/natural-earth-vector/{NATURAL_EARTH_COMMIT}/geojson/"
BARLOW_URL = f"https://raw.githubusercontent.com/google/fonts/{GOOGLE_FONTS_COMMIT}/ofl/barlow/"
SOURCE_CHECKSUMS = {
    "ne_50m_land.geojson": "e874b27a51d146452be360cafb3cc50c86001074a67d534113e6534682f9826b",
    "ne_50m_admin_0_boundary_lines_land.geojson": "2faac4f6b34386f3d21b6e018cf151f241f00e5c936d44dd17d7d9bfb147fa48",
    "ne_50m_admin_1_states_provinces_lines.geojson": "72cca93c850d412628a5da4bc5ebfe21ba4d376eb34611bde6b623ee73f0fdcf",
    "Barlow-Regular.ttf": "95aa02c7c43096e0dd44d787ba6216864a67157e402adab59b35572e0c1577ea",
    "Barlow-Bold.ttf": "84e6a4d61e7c3e21f3c50ea6a4f7e5303a3467864c038be6ea3759bab8d547f9",
    "OFL.txt": "186d750eb496a4c17a76385f82be6aea2ac1cf2de074a811d63786cf374ea73f",
}
SIMPLIFICATION_DEGREES = 0.025


def read_source(filename, cache_directory):
    cached_file = cache_directory / filename if cache_directory else None
    if cached_file and cached_file.exists():
        content = cached_file.read_bytes()
    else:
        base_url = NATURAL_EARTH_URL if filename.endswith(".geojson") else BARLOW_URL
        content = urllib.request.urlopen(base_url + filename, timeout=120).read()
        if cached_file:
            cached_file.parent.mkdir(parents=True, exist_ok=True)
            cached_file.write_bytes(content)
    if hashlib.sha256(content).hexdigest() != SOURCE_CHECKSUMS[filename]:
        raise ValueError(f"Source checksum does not match: {filename}")
    return content


def simplify_path(points):
    """Douglas-Peucker, preserving the source path's endpoints and order."""
    if len(points) < 3:
        return points
    longitude_start, latitude_start = points[0]
    longitude_end, latitude_end = points[-1]
    longitude_span = longitude_end - longitude_start
    latitude_span = latitude_end - latitude_start
    length_squared = longitude_span ** 2 + latitude_span ** 2
    greatest_distance_squared = -1
    split_index = 0
    for index in range(1, len(points) - 1):
        longitude, latitude = points[index]
        fraction = ((longitude - longitude_start) * longitude_span + (latitude - latitude_start) * latitude_span) / length_squared if length_squared else 0
        fraction = max(0, min(1, fraction))
        distance_squared = (longitude - longitude_start - fraction * longitude_span) ** 2 + (latitude - latitude_start - fraction * latitude_span) ** 2
        if distance_squared > greatest_distance_squared:
            greatest_distance_squared = distance_squared
            split_index = index
    if greatest_distance_squared <= SIMPLIFICATION_DEGREES ** 2:
        return [points[0], points[-1]]
    return simplify_path(points[:split_index + 1])[:-1] + simplify_path(points[split_index:])


def geometry_paths(geometry):
    coordinates = geometry["coordinates"]
    match geometry["type"]:
        case "Polygon" | "MultiLineString":
            return coordinates
        case "MultiPolygon":
            return [ring for polygon in coordinates for ring in polygon]
        case "LineString":
            return [coordinates]
        case _:
            raise ValueError(f"Unsupported geometry: {geometry['type']}")


def write_geometry(source, destination, only_usa=False, closed=False):
    paths = []
    for feature in json.loads(source)["features"]:
        if only_usa and feature["properties"]["ADM0_A3"] != "USA":
            continue
        for points in geometry_paths(feature["geometry"]):
            simplified_points = simplify_path(points)
            quantised_points = []
            for longitude, latitude in simplified_points:
                point = (round(longitude * 100), round(latitude * 100))
                if not quantised_points or point != quantised_points[-1]:
                    quantised_points.append(point)
            if closed and quantised_points and quantised_points[-1] != quantised_points[0]:
                quantised_points.append(quantised_points[0])
            if len(quantised_points) >= (4 if closed else 2):
                paths.append(quantised_points)
    with destination.open("wb") as output:
        output.write(struct.pack("<I", len(paths)))
        for points in paths:
            output.write(struct.pack("<I", len(points)))
            for longitude, latitude in points:
                output.write(struct.pack("<hh", longitude, latitude))
    print(f"{destination.name}: {len(paths):,} paths, {sum(map(len, paths)):,} points, {destination.stat().st_size:,} bytes")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache", type=Path, help="Optional directory for downloaded source files")
    arguments = parser.parse_args()
    assets_directory = Path(__file__).resolve().parent
    write_geometry(read_source("ne_50m_land.geojson", arguments.cache), assets_directory / "land.bin", closed=True)
    write_geometry(read_source("ne_50m_admin_0_boundary_lines_land.geojson", arguments.cache), assets_directory / "borders.bin")
    write_geometry(read_source("ne_50m_admin_1_states_provinces_lines.geojson", arguments.cache), assets_directory / "states.bin", only_usa=True)
    for filename in ["Barlow-Regular.ttf", "Barlow-Bold.ttf", "OFL.txt"]:
        (assets_directory / filename).write_bytes(read_source(filename, arguments.cache))


if __name__ == "__main__":
    main()
