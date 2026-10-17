"""Build exact native replay packets for an explicitly scoped B1 candidate.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Packets contain all assembled days for physical means and selected day indices
for the current eight-representative-days-per-month judgement. No stations fit
or alter these values. This scoped diagnostic is not a worldwide release.
"""
import argparse
import gzip
import json
import pathlib
import time

import numpy as np

from compare_step_a import point_weather, climate_pipeline
from fine_city_probe import NativeReferences, fine_pixel
from native_normals import file_sha256


def point_features(weather):
    pipeline = climate_pipeline()
    sun, shade, capped = pipeline.taste.felt(weather['T'], weather['Td'], weather['W'], weather['dni'])
    mask = np.arange(11) < weather['daylight'][:, None]
    return dict(airC=weather['T'], dewC=weather['Td'], sunC=sun, shadeC=shade,
        dewMeanC=capped.mean(1), nativeDewMeanC=weather['Td'].mean(1), rainMm=weather['rain'][:, :11].sum(1),
        windKmh=weather['W'].mean(1), sunnyHours=((weather['dni'][:, :11] >= 300) & mask).sum(1),
        daylightHours=weather['daylight'])


def reference_scores(native_root, fine, chunks, own_features):
    """The original geometric cell's dates; absent cells use own native dates."""
    pipeline = climate_pipeline()
    cells = np.fromfile(native_root/'v6/cells.bin', dtype='<u2').reshape(-1, 2)
    row, column = divmod(fine, 1440)
    geometric = min(row//4, 179)*360+column//4
    matches = np.flatnonzero(cells[:, 0]*360+cells[:, 1] == geometric)
    values = []
    if matches.size:
        raw_column = (column+720)%1440
        name = f'r{row//12:02d}c{raw_column//12:03d}.npz'
        for chunk in chunks:
            path = native_root/f'climate-v6-work/chunk{chunk}'/name
            if not path.exists(): break
            with np.load(path) as data:
                index = np.flatnonzero(data['cells'] == matches[0])
                if not index.size: break
                values.append(data['sums'][index[0]]/data['counts'][index[0]])
    if len(values) == len(chunks):
        features = np.concatenate(values)
        source = 'native assembled geometric 1-degree cell'
    else:
        features = np.zeros((len(own_features['airC']), 29))
        features[:, :11] = own_features['sunC'][:, :11]
        features[:, 11:23] = own_features['shadeC']
        for index, key in enumerate(('dewMeanC', 'rainMm', 'windKmh', 'sunnyHours', 'daylightHours'), 23):
            features[:, index] = own_features[key]
        features[:, 28] = own_features['airC'][:, :11].max(1)
        source = 'own native point; geometric cell absent from published land cells'
    return pipeline.score_features(pipeline.decode_records(pipeline.encode_records(features))), source


def build_packet(native, native_root, fine, generation):
    reference = native.point(fine)
    zone = native.zone(fine)
    weather = [point_weather(str(native.raw_root), fine, chunk, zone) for chunk in native.chunks]
    features = [point_features(value) for value in weather]
    fields = {key: np.concatenate([value[key] for value in features]) for key in features[0]}
    dates = reference['dates'].tolist()
    months = np.array([int(date[5:7]) for date in dates])
    scores, selection_source = reference_scores(native_root, fine, native.chunks, fields)
    indices = []
    for month in range(1, 13):
        available = np.flatnonzero(months == month)
        order = np.argsort(scores[available], kind='stable')
        indices.extend(available[order[np.floor((np.arange(8)+.5)*len(order)/8).astype(int)]].tolist())
    row, column = divmod(fine, 1440)
    packet = dict(version=1, fineIndex=fine, latitude=90-row*.25, longitude=-180+column*.25,
        generation=generation, chunks=native.chunks, zone=zone, dates=dates,
        highC=reference['high'].tolist(), lowC=reference['low'].tolist(),
        selectedIndices=indices, selectionSource=selection_source,
        physicalFacts='Means over all assembled days, not representative-day means',
        humidity='Native dew input retained. Felt vapor and humidity nuisance use min(native dew, corrected air); native dew facts use the uncapped native input.')
    packet.update({key: value.reshape(-1).tolist() for key, value in fields.items()})
    return packet


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--raw-root', type=pathlib.Path, required=True)
    parser.add_argument('--reference-root', type=pathlib.Path, required=True)
    parser.add_argument('--native-root', type=pathlib.Path, required=True)
    parser.add_argument('--tiles', type=pathlib.Path, required=True)
    parser.add_argument('--queries', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    started = time.monotonic()
    config = json.loads((args.tiles/'configuration.json').read_text())
    for key, path in (('reference_points_sha256', args.reference_root/'v6/points.bin'),
                      ('reference_timezones_sha256', args.reference_root/'climate-v6-work/timezones.npy')):
        if file_sha256(path) != config[key]:
            raise ValueError('Native replay grid/timezones differ from correction tile generation')
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output/'native').mkdir(exist_ok=True)
    native = NativeReferences(args.raw_root, args.reference_root, config['chunks'], args.output/'native-reference-cache')
    queries = json.loads(args.queries.read_text())
    points = {}
    for query in queries:
        pixel = fine_pixel(query['lat'], query['lon'])
        row = int(np.floor((pixel['row']+.5)/30+.5))
        column = int(np.floor((pixel['column']+.5)/30+.5))%1440
        fine = row*1440+column
        if fine not in points:
            packet = build_packet(native, args.native_root, fine, config['generation'])
            path = args.output/f'native/{fine}.json.gz'
            path.write_bytes(gzip.compress(json.dumps(packet, separators=(',', ':'), allow_nan=False).encode(), mtime=0))
            points[fine] = dict(path=str(path.relative_to(args.output)), bytes=path.stat().st_size,
                sha256=file_sha256(path), days=len(packet['dates']), selectedDays=len(packet['selectedIndices']))
        print('B1 replay packet', query['slug'], fine, flush=True)
    manifest = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18', status='candidate-rejected',
        scope='Thirteen frozen city query points only; unavailable native points must not imply worldwide coverage',
        generation=config['generation'], chunks=config['chunks'], queries=queries, nativeFineIndices=sorted(points),
        points=points, seconds=time.monotonic()-started,
        reference_points_sha256=file_sha256(args.reference_root/'v6/points.bin'),
        reference_timezones_sha256=file_sha256(args.reference_root/'climate-v6-work/timezones.npy'),
        queries_sha256=file_sha256(args.queries), builder_sha256=file_sha256(__file__))
    (args.output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    (args.output/'ATTRIBUTION.txt').write_text(
        'Native replay weather: CC BY 4.0.\n'
        'ERA5 hourly, ECMWF / Copernicus Climate Change Service (C3S), DOI:10.24381/cds.adbb2d47, via the anonymous Earthmover archive.\n'
        'https://registry.opendata.aws/earthmover-era5/\n'
        'https://creativecommons.org/licenses/by/4.0/\n'
        'Contains modified Copernicus Climate Change Service information (2026).\n'
        'Changes: local-clock sampling, apparent temperature, daily extrema and weather summaries; representative-day selection.\n'
        'Neither the European Commission nor ECMWF is responsible for any use of this Copernicus information. No endorsement is implied.\n'
        'The independent timezone-name property uses timezone-boundary-builder/timezonefinder and © OpenStreetMap contributors, ODbL 1.0.\n'
        'https://github.com/evansiroky/timezone-boundary-builder\n'
        'https://www.openstreetmap.org/copyright\n'
        'Exact-pixel correction tiles are a separate database with their own attribution and CC BY-SA 4.0 weather values.\n')
    (args.output/'NOT_FOR_PUBLICATION.txt').write_text('Rejected diagnostic candidate: city station non-regression acceptance has not passed. Only thirteen city queries have replay packets.\n')
    print(json.dumps(dict(seconds=manifest['seconds'], points=len(points), bytes=sum(x['bytes'] for x in points.values()))), flush=True)


if __name__ == '__main__': main()
