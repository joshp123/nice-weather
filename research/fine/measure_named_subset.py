"""Measure existing B1 file unions without creating a release or changing inputs.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Uses Python's standard library. Output inventories are private measurement evidence.
"""
import argparse
import collections
import datetime
import gzip
import hashlib
import json
import math
import pathlib
import struct


def address(latitude, longitude):
    # Match finePixel/nativeFineIndex: native address uses containing 1 km centre.
    row = min(21599, math.floor((90-latitude)*120))
    # Preserve wrap's actual floating-point operations at catalogue grid edges.
    column = math.floor((((longitude+180) % 360+360) % 360)*120)
    fine = min(720, max(0, math.floor((row+.5)/30+.5)))*1440 + math.floor((column+.5)/30+.5) % 1440
    return row, column, fine


def encoded(value):
    return (json.dumps(value, indent=2)+'\n').encode()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', type=pathlib.Path, required=True)
    parser.add_argument('--places', type=pathlib.Path, default=pathlib.Path('artifacts/places.tsv'))
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    release = args.release
    replay_inventory = json.loads(gzip.decompress((release/'replay/BUILD-INVENTORY.json.gz').read_bytes()))
    packet_inventory = {entry['path']: entry for entry in replay_inventory if entry['path'].startswith('native/')}
    packets_by_native = collections.defaultdict(list)
    for name in packet_inventory:
        packets_by_native[int(name.split('/')[1])].append('replay/'+name)
    manifest = json.loads((release/'replay/manifest.json').read_text())
    labels = [entry for level in [1, 2] for entry in json.loads((release/f'labels/labels-{level}.json').read_text())]
    places = []
    for line in args.places.read_text().splitlines():
        fields = line.split('\t')
        places.append([fields[0], float(fields[3]), float(fields[4]), int(fields[5])])
    label_points = [(entry[0], entry[2], entry[3], entry[4]) for entry in labels]
    sets = {'all-labels-and-places': label_points+places, 'all-labels-only': label_points}
    for cutoff in [10000, 50000, 100000, 500000]:
        sets[f'all-labels-plus-places-pop-{cutoff}'] = label_points+[entry for entry in places if entry[3] >= cutoff]
        sets[f'both-catalogues-pop-{cutoff}'] = [entry for entry in label_points+places if entry[3] >= cutoff]
    # Every non-payload file retained is an exact, conservative unchanged-ancillary cost.
    ancillary = {str(path.relative_to(release)) for path in release.rglob('*') if path.is_file()
                 and not str(path.relative_to(release)).startswith(('replay/native/', 'replay/zones/', 'temperature/tiles/'))}
    runtime_and_notices = {'replay/manifest.json', 'replay/ATTRIBUTION.txt', 'ATTRIBUTION.txt'}
    runtime_and_notices.update(str(path.relative_to(release)) for path in (release/'labels').rglob('*') if path.is_file())
    # Keep complete published licence/provenance packages (including underlying ODbL data).
    runtime_and_notices.update(str(path.relative_to(release)) for folder in ['temperature/timezones', 'temperature/land-membership']
                              for path in (release/folder).rglob('*') if path.is_file())
    runtime_and_notices.update({'temperature/ATTRIBUTION.txt', 'temperature/SOURCE-PROVENANCE.json', 'temperature/configuration.json'})
    size_cache = {'replay/'+entry['path']: entry['bytes'] for entry in replay_inventory}
    def file_bytes(name):
        if name not in size_cache:
            size_cache[name] = (release/name).stat().st_size
        return size_cache[name]
    def size(names):
        return {'files': len(names), 'bytes': sum(file_bytes(name) for name in names)}
    tile_points = collections.defaultdict(list)
    for entries in sets.values():
        for _, latitude, longitude, _ in entries:
            row, column, fine = address(latitude, longitude)
            tile_points[(row//256, column//256)].append((row, column, fine))
    # Decode each candidate geographic tile once. Verify actual pixel support and clock.
    supported = {}
    for (tile_row, tile_column), entries in tile_points.items():
        stem = f'{tile_row:03d}/{tile_column:03d}'
        temperature = release/f'temperature/tiles/01/{stem}.nwf.gz'
        timezone = release/f'replay/zones/{stem}.nwz.gz'
        if not temperature.exists() or not timezone.exists():
            continue
        correction = gzip.decompress(temperature.read_bytes())
        zones = gzip.decompress(timezone.read_bytes())
        for row, column, fine in entries:
            index = (row % 256)*256 + column % 256
            zone = struct.unpack_from('<H', zones, 32+index*2)[0]
            high, low = struct.unpack_from('<hh', correction, 32+index*4)
            if correction[32+256*256*4+index] & 8 and high != -32768 and low != -32768 and zone != 65535:
                packet = f'native/{fine}/{zone}.nwfp.gz'
                if packet in packet_inventory:
                    supported[(row, column)] = (fine, zone)
    args.output.mkdir(parents=True, exist_ok=True)
    results = {}
    for label, entries in sets.items():
        coordinates = {(latitude, longitude) for _, latitude, longitude, _ in entries}
        pixels = {address(latitude, longitude)[:2] for latitude, longitude in coordinates}
        good_pixels = pixels & supported.keys()
        native_points = {supported[pixel][0] for pixel in good_pixels}
        pairs = {supported[pixel] for pixel in good_pixels}
        tiles = {(row//256, column//256) for row, column in good_pixels}
        exact_packets = {f'replay/native/{fine}/{zone}.nwfp.gz' for fine, zone in pairs}
        native_packets = {name for fine in native_points for name in packets_by_native[fine]}
        zone_files = {f'replay/zones/{row:03d}/{column:03d}.nwz.gz' for row, column in tiles}
        correction_files = {f'temperature/tiles/{month:02d}/{row:03d}/{column:03d}.nwf.gz' for month in range(1, 13) for row, column in tiles}
        minimal = exact_packets | zone_files | correction_files | runtime_and_notices
        native_complete = native_packets | zone_files | correction_files | runtime_and_notices
        unchanged = native_packets | zone_files | correction_files | ancillary
        inventory = [{'path': name, 'bytes': file_bytes(name)} for name in sorted(native_complete)]
        inventory_bytes = encoded(inventory)
        (args.output/f'{label}.files.json.gz').write_bytes(gzip.compress(inventory_bytes, mtime=0))
        results[label] = {'inputRows': len(entries), 'distinctCoordinates': len(coordinates), 'distinctPixels': len(pixels),
            'supportedPixels': len(good_pixels), 'unsupportedPixels': len(pixels)-len(good_pixels),
            'supportedCoordinates': sum(address(*coordinate)[:2] in supported for coordinate in coordinates),
            'nativePoints': len(native_points), 'exactClockPackets': size(exact_packets), 'allClockPacketsForNativePoints': size(native_packets),
            'correctionTiles': size(correction_files), 'zoneMaps': size(zone_files),
            'minimumExactPointPacketsWithNoticesAndLabels': size(minimal),
            'allNativeClockPacketsWithNoticesAndLabels': size(native_complete),
            'allNativeClockPacketsWithUnchangedAncillary': size(unchanged),
            'inventorySha256': hashlib.sha256(inventory_bytes).hexdigest(),
            'hypotheticalUncompressedPointAnchorPayloadBytes': len(good_pixels)*24*2,
            'unsupportedExamples': [{'latitude': latitude, 'longitude': longitude} for latitude, longitude in sorted(coordinates) if address(latitude, longitude)[:2] not in supported][:10]}
    output = {'written_by': 'AI, GPT-6.1 Sol, 2026.10.18', 'measuredUTC': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'release': str(release), 'generation': manifest['generation'], 'sourceCatalogues': {'labels': len(labels), 'places': len(places)},
        'catalogueSha256': {'places': hashlib.sha256(args.places.read_bytes()).hexdigest(), **{f'labels-{level}': hashlib.sha256((release/f'labels/labels-{level}.json').read_bytes()).hexdigest() for level in [1, 2]}},
        'catalogueAddressSha256': hashlib.sha256(''.join(','.join(map(str, address(latitude, longitude)))+'\n' for _, latitude, longitude, _ in label_points+places).encode()).hexdigest(),
        'runtimeAndNotices': size(runtime_and_notices), 'runtimeAndNoticesFiles': sorted(runtime_and_notices), 'unchangedAncillary': size(ancillary),
        'notes': ['No release built, sealed, uploaded or activated. Exact union of current compressed payload files; every candidate pixel checked against actual source/support and clock maps.',
                  'Runtime reads replay manifest, target zone map, own-native clock packet and twelve correction tiles. Normals and packet/tile indexes are not read.',
                  'Notices tier retains label catalogues, replay manifest, attribution, provenance and complete underlying ODbL land/timezone packages. New subset manifests/checksum inventories would need generating before publication; these counts are measurement, not a ready release.',
                  'Unchanged ancillary tier includes full-world indexes, normals, source overviews and audit inventories; those full-world inventories must not claim subset completeness.',
                  'Hypothetical point anchors are 24 int16 temperature offsets per supported distinct fine pixel only; excludes point addresses, metadata, replay, clocks and indexing; not implemented.'],
        'options': results}
    (args.output/'measurement.json').write_bytes(encoded(output))
    print(json.dumps({'output': str(args.output/'measurement.json'), 'runtimeAndNotices': output['runtimeAndNotices'], 'options': {label: {'supportedPixels': value['supportedPixels'], 'unsupportedPixels': value['unsupportedPixels'], 'nativePoints': value['nativePoints'], **value['allNativeClockPacketsWithNoticesAndLabels']} for label, value in results.items()}}, indent=2))


if __name__ == '__main__':
    main()
