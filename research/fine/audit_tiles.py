"""Read every delivered fine tile and compare coastal/island pixels to sources.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
"""
import argparse
import concurrent.futures
import hashlib
import json
import pathlib
import time

import numpy as np

from anchors import AnchorSources,load_era5_mean_normals
from tiles import decode_tile,load_reference,bilinear,PERIOD_NAMES

PLACES = {
    'San Francisco':(37.7749,-122.4194),'Los Angeles':(34.0522,-118.2437),
    'Santa Barbara':(34.4208,-119.6982),'San Diego':(32.7157,-117.1611),
    'Lima':(-12.0464,-77.0428),'Valparaiso':(-33.0472,-71.6127),
    'Malaga':(36.7213,-4.4214),'Tenerife south':(28.0445,-16.5725),
    'Funchal':(32.6669,-16.9241),'Cape Town':(-33.9249,18.4241),
    'Seville':(37.3891,-5.9845),'Rotterdam':(51.9244,4.4777),
    'Bangkok':(13.7563,100.5018),'Nauru':(-.5228,166.9315),
    'Funafuti':(-8.5211,179.1962),'Majuro':(7.0897,171.3803),
    'Bermuda':(32.2949,-64.7814),'Male':(4.1755,73.5093),
    'Tarawa Betio':(1.355,172.926),'Pitcairn':(-25.0667,-130.1000),
    'Palmer Station':(-64.7742,-64.0527),'South Pole':(-90.,0.),'Greenland':(72.,-40.)}


def inspect_file(job):
    root,relative,digest,generation = job
    path = root/relative
    encoded = path.read_bytes()
    if hashlib.sha256(encoded).hexdigest() != digest: raise ValueError(f'Hash mismatch: {relative}')
    tile = decode_tile(path)
    expected = pathlib.PurePosixPath(relative).parts
    if (tile['month'],tile['row'],tile['column']) != (int(expected[1]),int(expected[2]),int(path.name.split('.')[0])):
        raise ValueError(f'Tile position mismatch: {relative}')
    if tile['generation'] != generation: raise ValueError(f'Generation mismatch: {relative}')
    if (tile['height'],tile['width']) != (min(256,21600-tile['row']*256),min(256,43200-tile['column']*256)):
        raise ValueError(f'Tile edge dimensions differ from the global grid: {relative}')
    flags,offsets = tile['support'],tile['offsets_c']
    land = flags != 0
    source = flags&7
    if np.any(source[land]>4) or np.any((flags[land]&8)==0): raise ValueError(f'Invalid support: {relative}')
    missing = offsets == np.float32(-3276.8)
    if not np.array_equal(missing,np.repeat((~land)[...,None],2,axis=-1)):
        raise ValueError(f'Missing-value mismatch: {relative}')
    if np.any(offsets[land&(source==0)] != 0): raise ValueError(f'Non-native fallback: {relative}')
    if np.any(flags[tile['height']:] != 0) or np.any(flags[:,tile['width']:] != 0):
        raise ValueError(f'Edge padding contains land: {relative}')
    counts = np.bincount(source[land],minlength=5)
    return tile['month']-1,counts,len(encoded)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tiles',type=pathlib.Path,required=True)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--workers',type=int,default=8)
    args = parser.parse_args()
    started = time.monotonic()
    manifest = json.loads((args.tiles/'manifest.json').read_text())
    generation = manifest['configuration']['generation']
    jobs = []
    for line in (args.tiles/'tiles.sha256').read_text().splitlines():
        digest,relative = line.split('  ',1)
        jobs.append((args.tiles,relative,digest,generation))
    index_count = int(np.fromfile(args.tiles/'tiles.bin',dtype='<u4',count=1)[0])
    index = np.fromfile(args.tiles/'tiles.bin',dtype='<u2',offset=4).reshape(-1,2)
    expected_files = {f'tiles/{month:02d}/{row:03d}/{column:03d}.nwf.gz'
                      for month in range(1,13) for row,column in index}
    if (index_count != len(index) or index_count != manifest['tilesPerMonth']
            or len(jobs) != len(expected_files) or len(jobs) != manifest['files']
            or {relative for _,relative,_,_ in jobs} != expected_files):
        raise ValueError('Tile index, monthly inventory and manifest differ')
    counts = np.zeros((12,5),np.int64)
    byte_count = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for number,(month,values,size) in enumerate(pool.map(inspect_file,jobs),1):
            counts[month] += values
            byte_count += size
            if number%5000 == 0: print(f'fine tile audit {number}/{len(jobs)}',flush=True)
    if not np.array_equal(counts,manifest['sourcesPerMonth']): raise ValueError('Manifest source counts differ')
    land_metadata = json.loads((args.tiles/'LAND-SUPPORT.json').read_text())
    if not np.all(counts.sum(axis=1) == land_metadata['pixels']):
        raise ValueError('Delivered months do not cover the complete fine land mask')
    with np.load(args.tiles/'source-overviews/counts.npz') as overview:
        if not np.array_equal(overview['counts'].sum(axis=(2,3),dtype=np.int64),counts):
            raise ValueError('Overview source counts differ')
    if byte_count != manifest['compressedTileBytes']: raise ValueError('Transfer size differs')
    sources = AnchorSources(args.source_cache)
    fields = load_reference(args.tiles/'native-reference-normals.npz',manifest['configuration']['chunks'])
    historical = load_era5_mean_normals(args.source_cache)
    places = {}
    largest_error = 0.
    for name,(latitude,longitude) in PLACES.items():
        row = min(21599,max(0,int(np.floor((90-latitude)*120))))
        column = int(np.floor((longitude+180)*120))%43200
        latitude,longitude = 90-(row+.5)/120,-180+(column+.5)/120
        sample = sources.sample(np.array([latitude]),np.array([longitude]))
        nr,nc = (row+.5)/30,(column+.5)/30
        rr,cc = int(np.floor(nr+.5)),int(np.floor(nc+.5))%1440
        months = []
        for month in range(1,13):
            path = args.tiles/f'tiles/{month:02d}/{row//256:03d}/{column//256:03d}.nwf.gz'
            if not path.exists():
                raise ValueError(f'Missing land tile at {name}/{month}')
            tile = decode_tile(path)
            flags = int(tile['support'][row%256,column%256])
            if not flags:
                raise ValueError(f'Missing land pixel at {name}/{month}')
            source = int(sample.source[month-1,0])
            if (flags&7) != source: raise ValueError(f'Source mismatch at {name}/{month}')
            expected = np.zeros(2)
            if source:
                period = PERIOD_NAMES[source]
                anomaly = bilinear(fields['mean_c'][month-1]-historical[period][month-1],np.array([nr]),np.array([nc]))[0]
                expected = np.array([sample.tmax_c[month-1,0],sample.tmin_c[month-1,0]])+anomaly
                expected -= [fields['high_c'][month-1,rr,cc],fields['low_c'][month-1,rr,cc]]
            actual = tile['offsets_c'][row%256,column%256]
            error = float(np.max(np.abs(actual-expected)))
            if error>.05001: raise ValueError(f'Offset differs from source at {name}/{month}: {error}')
            largest_error = max(largest_error,error)
            months.append(dict(month=month,land=True,source=source,support=flags,
                high_offset_c=float(actual[0]),low_offset_c=float(actual[1]),encoding_error_c=error,
                adjusted_normal_high_c=float(fields['high_c'][month-1,rr,cc]+actual[0]),
                adjusted_normal_low_c=float(fields['low_c'][month-1,rr,cc]+actual[1])))
        places[name] = dict(pixel=[row,column],centre=[latitude,longitude],months=months)
    result = dict(files=len(jobs),compressed_bytes=byte_count,source_counts=counts.tolist(),
        maximum_sample_encoding_error_c=largest_error,places=places,seconds=time.monotonic()-started)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({key:value for key,value in result.items() if key!='places'}),flush=True)


if __name__ == '__main__': main()
