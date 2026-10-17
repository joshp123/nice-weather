"""Read every worldwide replay packet and prove the target-clock coverage index.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""
import argparse
import concurrent.futures
import gzip
import hashlib
import json
import pathlib
import struct
import time

import numpy as np

from native_normals import file_sha256


def inspect_packet(job):
    root, relative, digest, generation, days, months = job
    encoded = (root/relative).read_bytes()
    if hashlib.sha256(encoded).hexdigest() != digest:
        raise ValueError(f'Replay checksum differs: {relative}')
    payload = gzip.decompress(encoded)
    magic, version, model, selected, fine, zone, count, identity = struct.unpack_from('<4sBBHIHH16s', payload)
    expected = f'native/{fine}/{zone}.nwfp.gz'
    if (relative != expected or magic != b'NWFP' or version != 1 or model != 1
            or selected != 96 or count != days or identity.hex() != generation
            or len(payload) != 32+selected*2+(selected*54+count*6+72)*4):
        raise ValueError(f'Replay identity or length differs: {relative}')
    indices = np.frombuffer(payload, dtype='<u2', count=selected, offset=32)
    if np.any(indices >= days) or not np.all(np.bincount(months[indices], minlength=13)[1:] == 8):
        raise ValueError(f'Representative days differ: {relative}')
    values = np.frombuffer(payload, dtype='<f4', offset=32+selected*2)
    if not np.isfinite(values).all():
        raise ValueError(f'Replay contains nonfinite weather: {relative}')
    return (int(fine) << 16) | int(zone), len(encoded)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--replay', type=pathlib.Path, required=True)
    parser.add_argument('--land-support', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--workers', type=int, default=8)
    args = parser.parse_args()
    started = time.monotonic()
    manifest = json.loads((args.replay/'manifest.json').read_text())
    if manifest['format'] != 'nwfp-v1' or manifest['eveningModel'] != 'uniform':
        raise ValueError('This release requires compact replay and uniform evenings')
    months = np.array([int(date[5:7]) for date in manifest['dates']], dtype=np.uint8)
    checksums = {}
    for line in (args.replay/'files.sha256').read_text().splitlines():
        digest, relative = line.split('  ',1)
        checksums[relative] = digest
    pairs = np.fromfile(args.replay/'packet-index.bin', dtype='<u8')
    if len(pairs) != manifest['packetCount'] or np.any(pairs[1:] <= pairs[:-1]):
        raise ValueError('Replay packet index is incomplete or unsorted')
    jobs = [(args.replay, relative, digest, manifest['generation'], len(months), months)
            for relative, digest in checksums.items() if relative.startswith('native/')]
    if len(jobs) != len(pairs): raise ValueError('Packet inventory and index differ')
    decoded, packet_bytes = [], 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for number, (pair, size) in enumerate(pool.map(inspect_packet, jobs), 1):
            decoded.append(pair); packet_bytes += size
            if number%25000 == 0: print('Replay packets audited', number, '/', len(jobs), flush=True)
    if not np.array_equal(np.sort(np.asarray(decoded, dtype='<u8')), pairs):
        raise ValueError('Decoded replay packets differ from the index')
    land = np.memmap(args.land_support/'land.u8', mode='r', dtype=np.uint8, shape=(21600,43200))
    covered, zone_bytes, zones = 0, 0, 0
    for relative, digest in checksums.items():
        if not relative.startswith('zones/'): continue
        encoded = (args.replay/relative).read_bytes()
        if hashlib.sha256(encoded).hexdigest() != digest: raise ValueError(f'Clock checksum differs: {relative}')
        payload = gzip.decompress(encoded)
        magic, version, row, column, width, identity, size = struct.unpack_from('<4sHHHH16sI', payload)
        if (relative != f'zones/{row:03d}/{column:03d}.nwz.gz' or magic != b'NWZT'
                or version != 1 or width != 256 or size != 65536
                or len(payload) != 131104 or identity.hex() != manifest['generation']):
            raise ValueError(f'Clock tile differs: {relative}')
        zone = np.frombuffer(payload, dtype='<u2', offset=32).reshape(256,256)
        height, width = min(256,21600-row*256), min(256,43200-column*256)
        supported = land[row*256:row*256+height,column*256:column*256+width] != 0
        if not np.array_equal(zone[:height,:width] != 65535, supported):
            raise ValueError(f'Clock support differs from land: {relative}')
        local_rows, local_columns = np.nonzero(supported)
        selected_zones = zone[local_rows,local_columns]
        if np.any(selected_zones >= len(manifest['timezoneNames'])): raise ValueError('Invalid timezone index')
        native = (np.floor((row*256+local_rows+.5)/30+.5).astype(np.uint32)*1440
                  + np.floor((column*256+local_columns+.5)/30+.5).astype(np.uint32)%1440)
        keys = (native.astype(np.uint64) << 16) | selected_zones
        positions = np.searchsorted(pairs, keys)
        if np.any(positions == len(pairs)) or not np.array_equal(pairs[positions], keys):
            raise ValueError(f'Supported fine pixels lack replay packets: {relative}')
        covered += len(keys); zone_bytes += len(encoded); zones += 1
    expected_land = json.loads((args.land_support/'complete.json').read_text())['pixels']
    if (covered != expected_land or covered != manifest['supportedFinePixels']
            or len(np.unique(pairs >> 16)) != manifest['ownNativePoints']
            or packet_bytes != manifest['compressedPacketBytes']
            or zones != manifest['zoneCount'] or zone_bytes != manifest['compressedZoneBytes']):
        raise ValueError('Worldwide replay coverage or byte totals differ')
    report = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18', passes=True,
        generation=manifest['generation'], packets=len(pairs), ownNativePoints=manifest['ownNativePoints'],
        supportedFinePixels=covered, zones=zones, packetBytes=packet_bytes, zoneBytes=zone_bytes,
        manifest_sha256=file_sha256(args.replay/'manifest.json'), seconds=time.monotonic()-started)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps(report), flush=True)


if __name__ == '__main__': main()
