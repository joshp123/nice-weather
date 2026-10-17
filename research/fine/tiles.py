"""Build 30 arc-second monthly high/low corrections and their support map.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
No application changes or publication. Source selection is geographic and
availability-based, identical everywhere. No station observations are inputs.
"""
import argparse
import concurrent.futures
import datetime as dt
import gzip
import hashlib
import json
import pathlib
import shutil
import struct
import time

import numpy as np
from PIL import Image

from anchors import AnchorSources,load_era5_mean_normals
from land_support import WIDTH,HEIGHT
from native_normals import file_sha256
from attach_timezones import attach as attach_timezones

TILE_SIZE = 256
NODATA = -32768
HEADER = struct.Struct('<4sHBBHHHH16s')
PERIOD_NAMES = {1:'prism',2:'worldclim14',3:'worldclim14',4:'chelsa'}
POINT_DTYPE = np.dtype([('fine','<u4'),('source','<u4'),('shift','<i2'),('cell','<u2')])


def load_reference(normals_path,chunks):
    with np.load(normals_path) as saved:
        if sorted(saved['chunks'].tolist()) != sorted(chunks):
            raise ValueError('Reference normals do not match the assembled chunk set')
        fine = saved['fine']
        fields = {}
        for name in ('high_c','low_c','mean_c'):
            values = np.full((12,721*1440),np.nan,np.float32)
            values[:,fine] = saved[name].T
            fields[name] = values.reshape(12,721,1440)
    return fields


def bilinear(field,rows,columns):
    lower_row = np.floor(rows).astype(int)
    upper_row = np.minimum(720,lower_row+1)
    lower_column = np.floor(columns).astype(int)%1440
    upper_column = (lower_column+1)%1440
    row_fraction = rows-lower_row
    column_fraction = columns-np.floor(columns)
    result = np.zeros(rows.shape,np.float64)
    for r,row_weight in ((lower_row,1-row_fraction),(upper_row,row_fraction)):
        for c,column_weight in ((lower_column,1-column_fraction),(upper_column,column_fraction)):
            weight = row_weight*column_weight
            values = field[r,c]
            if not np.all(np.isfinite(values[weight>0])):
                raise ValueError('A fine land pixel lacks a nonzero-weight ERA5 reference corner')
            result += np.where(weight>0,values,0)*weight
    return result


def write_tile(job):
    path,header,offsets,support = job
    if not path.exists():
        payload = header+offsets.astype('<i2',copy=False).tobytes()+support.tobytes()
        encoded = gzip.compress(payload,compresslevel=6,mtime=0)
        path.parent.mkdir(parents=True,exist_ok=True)
        temporary = path.with_suffix('.partial')
        temporary.write_bytes(encoded)
        temporary.replace(path)
    encoded = path.read_bytes()
    return path,len(encoded),hashlib.sha256(encoded).hexdigest()


def decode_tile(path):
    """Reference decoder for the binary contract, also used by audit tools."""
    decoded = gzip.decompress(pathlib.Path(path).read_bytes())
    magic,version,month,flags,row,column,height,width,generation = HEADER.unpack_from(decoded)
    if magic != b'NWFT' or version != 1 or len(decoded) != 32+TILE_SIZE*TILE_SIZE*5:
        raise ValueError('Invalid fine correction tile')
    offsets = np.frombuffer(decoded,dtype='<i2',count=TILE_SIZE*TILE_SIZE*2,offset=32).reshape(TILE_SIZE,TILE_SIZE,2)
    support = np.frombuffer(decoded,dtype=np.uint8,offset=32+TILE_SIZE*TILE_SIZE*4).reshape(TILE_SIZE,TILE_SIZE)
    return dict(month=month,row=row,column=column,height=height,width=width,
                generation=generation.hex(),offsets_c=offsets.astype(np.float32)/10,support=support)


def write_source_overviews(output,counts):
    directory = output/'source-overviews'
    directory.mkdir(exist_ok=True)
    colors = [(100,100,100),(45,105,220),(35,160,90),(235,165,30),(155,75,190)]
    palette = sum((list(color) for color in colors),[])+[0]*((256-len(colors))*3)
    for month in range(12):
        totals = counts[month].sum(0)
        dominant = counts[month].argmax(0).astype(np.uint8)
        weakest = np.full(totals.shape,255,np.uint8)
        for code in (1,2,3,4,0): weakest[counts[month,code]>0] = code
        dominant[totals==0] = 255
        for name,values in [('dominant',dominant),('weakest',weakest)]:
            image = Image.fromarray(values).convert('P')
            image.putpalette(palette)
            image.save(directory/f'{name}-{month+1:02d}.png',transparency=255)
    np.savez_compressed(directory/'counts.npz',counts=counts)
    (directory/'legend.txt').write_text(
        'Each overview pixel aggregates a30x30 block of30arc-second pixels.\n'
        'Grid1440x720, north90/west-180 edges,0.25degree cells; not the721-row v6 point grid.\n'
        'Transparent: no supported land. Gray0: native ERA5; blue1: PRISM; green2: WorldClim exact; '
        'orange3: WorldClim5x5; purple4: CHELSA.\n'
        'Dominant: most numerous source, ties select lowest code. '
        'Weakest: any native fallback, then CHELSA, WorldClim5x5, WorldClim exact, PRISM.\n'
        'counts.npz has[12months,5sources,720rows,1440columns] uint16 pixel counts. '
        'These maps describe source/support, not measured accuracy.\n')


def publish_polygon_masks(output,land_root,source_cache):
    directory = output/'land-membership'
    directory.mkdir(exist_ok=True)
    ne = np.memmap(land_root/'natural-earth.u8',mode='r',dtype=np.uint8,shape=(HEIGHT,WIDTH))
    osm = np.memmap(land_root/'osm.u8',mode='r',dtype=np.uint8,shape=(HEIGHT,WIDTH))
    for name in ('osm','polygon-union'):
        with (directory/f'{name}.bits.gz').open('wb') as raw:
            with gzip.GzipFile(filename='',mode='wb',fileobj=raw,mtime=0) as destination:
                for first in range(0,HEIGHT,256):
                    values = osm[first:first+256].copy()
                    if name == 'polygon-union': values |= ne[first:first+256]
                    destination.write(np.packbits(values,axis=1,bitorder='little').tobytes())
    for name in ('ODbL-1.0.html','licence.html','description.html','dataset.json'):
        shutil.copy2(source_cache/'osm-land'/name,directory/name)
    (directory/'ATTRIBUTION.txt').write_text(
        '© OpenStreetMap contributors; Open Database License1.0.\n'
        'https://www.openstreetmap.org/copyright\n'
        'https://osmdata.openstreetmap.de/data/land-polygons.html\n'
        'Modifications: regular30arc-second any-touched polygon rasterization; '
        'polygon-union additionally unions public-domain Natural Earth land/minor islands.\n'
        'This independent land-membership database is offered underODbL1.0. '
        'No WorldClim/PRISM validity property is merged into these polygon masks.\n')
    metadata = dict(licence='ODbL-1.0',width=WIDTH,height=HEIGHT,
        encoding='gzip; row-major packed bits, little bit order within each byte; one=true',
        coordinates='north90/west-180 edges;43200x21600;30arc-second; any-touched polygons',
        properties={'osm':'OSM-only membership','polygon-union':'OSM or public-domain Natural Earth membership'},
        separation='Weather values and source-data validity are independent properties with their own licences.',
        files_sha256={path.name:file_sha256(path) for path in sorted(directory.iterdir()) if path.is_file()})
    (directory/'manifest.json').write_text(json.dumps(metadata,indent=2)+'\n')
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--reference-normals',type=pathlib.Path,required=True)
    parser.add_argument('--reference-root',type=pathlib.Path,required=True,help='Exact native points and timezone lookup used for the normals')
    parser.add_argument('--legacy-points',type=pathlib.Path,required=True)
    parser.add_argument('--land-support',type=pathlib.Path,required=True)
    parser.add_argument('--meta',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--workers',type=int,default=8)
    args = parser.parse_args()
    started = time.monotonic()
    chunks = sorted(json.loads(args.meta.read_text())['chunks'])
    args.output.mkdir(parents=True,exist_ok=True)
    source_provenance = {name:json.loads((args.source_cache/name/'dataset.json').read_text())
                         for name in ('worldclim14','prism','chelsa','era5_monthly')}
    configuration = dict(chunks=chunks,reference_normals_sha256=file_sha256(args.reference_normals),
        legacy_points_sha256=file_sha256(args.legacy_points),
        reference_points_sha256=file_sha256(args.reference_root/'v6/points.bin'),
        reference_timezones_sha256=file_sha256(args.reference_root/'climate-v6-work/timezones.npy'),
        land_support_sha256=file_sha256(args.land_support/'land.u8'),
        source_metadata_sha256={name:file_sha256(args.source_cache/name/'dataset.json')
                               for name in source_provenance},
        sampler_sha256=file_sha256(pathlib.Path(__file__).with_name('anchors.py')),
        builder_sha256=file_sha256(__file__),
        daily_model='independent high/low additive offsets; if corrected H<=corrected L or native H==L use their average all day',
        carrier_selection='future carrier: geometric cell quantile dates, own-centre dates for absent cells; own native quarter-grid weather',
        anomaly='bilinear common ERA5 monthly-mean anomaly; actual assembled scored dates',
        baseline='nearest own native 0.25deg point monthly mean daily 24-clock-hour high/low')
    generation = hashlib.sha256(json.dumps(configuration,sort_keys=True).encode()).digest()[:16]
    configuration['generation'] = generation.hex()
    configuration_path = args.output/'configuration.json'
    if configuration_path.exists() and json.loads(configuration_path.read_text()) != configuration:
        raise ValueError('Existing tile generation uses different dates, sources or model; choose a new output')
    configuration_path.write_text(json.dumps(configuration,indent=2)+'\n')
    fields = load_reference(args.reference_normals,chunks)
    historical = load_era5_mean_normals(args.source_cache)
    legacy = np.zeros(721*1440,np.uint8)
    legacy[np.fromfile(args.legacy_points,dtype=POINT_DTYPE,offset=4)['fine']] = 1
    legacy = legacy.reshape(721,1440)
    land = np.memmap(args.land_support/'land.u8',mode='r',dtype=np.uint8,shape=(HEIGHT,WIDTH))
    sources = AnchorSources(args.source_cache)
    source_counts = np.zeros((12,5),np.int64)
    source_ranges = np.full((12,2,2),np.nan,np.float64)
    overview_counts = np.zeros((12,5,720,1440),np.uint16)
    tile_index = []
    outputs = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for first_row in range(0,HEIGHT,TILE_SIZE):
            last_row = min(HEIGHT,first_row+TILE_SIZE)
            support_mask = np.asarray(land[first_row:last_row])
            local_rows,columns = np.nonzero(support_mask)
            if not columns.size: continue
            rows = local_rows+first_row
            latitudes = 90-(rows+.5)/120
            longitudes = -180+(columns+.5)/120
            native_rows = (rows+.5)/30
            native_columns = (columns+.5)/30
            nearest_rows = np.floor(native_rows+.5).astype(int)
            nearest_columns = np.floor(native_columns+.5).astype(int)%1440
            active_columns = sorted(set((columns//TILE_SIZE).tolist()))
            tile_row = first_row//TILE_SIZE
            tile_index.extend((tile_row,column) for column in active_columns)
            geography = support_mask[local_rows,columns]
            base_support = (8+legacy[nearest_rows,nearest_columns]*16+
                ((geography&9)>0)*32+((geography&2)>0)*64+((geography&4)>0)*128).astype(np.uint8)
            for month in range(1,13):
                samples = sources.sample(latitudes,longitudes,months=[month])
                source = samples.source[0]
                high,low = samples.tmax_c[0].astype(np.float64),samples.tmin_c[0].astype(np.float64)
                baseline_high = fields['high_c'][month-1,nearest_rows,nearest_columns]
                baseline_low = fields['low_c'][month-1,nearest_rows,nearest_columns]
                if not (np.isfinite(baseline_high).all() and np.isfinite(baseline_low).all()):
                    raise ValueError('Fine land has no native ERA5 high/low reference')
                high_offset,low_offset = np.zeros(columns.size),np.zeros(columns.size)
                for code,period in PERIOD_NAMES.items():
                    selected = source == code
                    if selected.any():
                        anomaly = fields['mean_c'][month-1]-historical[period][month-1]
                        adjustment = bilinear(anomaly,native_rows[selected],native_columns[selected])
                        high_offset[selected] = high[selected]+adjustment-baseline_high[selected]
                        low_offset[selected] = low[selected]+adjustment-baseline_low[selected]
                offsets = np.stack((high_offset,low_offset),axis=-1)
                if not np.isfinite(offsets).all() or np.any(np.abs(offsets)>3276.7):
                    raise ValueError('Fine correction cannot be represented as signed tenths')
                encoded = np.rint(offsets*10).astype('<i2')
                source_counts[month-1] += np.bincount(source,minlength=5)
                coarse_first,coarse_last = first_row//30,(last_row-1)//30+1
                coarse_rows = coarse_last-coarse_first
                bins = (source.astype(np.int64)*coarse_rows+rows//30-coarse_first)*1440+columns//30
                counts = np.bincount(bins,minlength=5*coarse_rows*1440).reshape(5,coarse_rows,1440)
                overview_counts[month-1,:,coarse_first:coarse_last] += counts.astype(np.uint16)
                limits = np.stack((offsets.min(0),offsets.max(0)),axis=1)
                source_ranges[month-1,:,0] = np.fmin(source_ranges[month-1,:,0],limits[:,0])
                source_ranges[month-1,:,1] = np.fmax(source_ranges[month-1,:,1],limits[:,1])
                raster = np.full((TILE_SIZE,WIDTH,2),NODATA,dtype='<i2')
                support = np.zeros((TILE_SIZE,WIDTH),np.uint8)
                raster[local_rows,columns] = encoded
                support[local_rows,columns] = base_support|source
                jobs = []
                for column in active_columns:
                    first_column = column*TILE_SIZE
                    last_column = min(WIDTH,first_column+TILE_SIZE)
                    values = np.full((TILE_SIZE,TILE_SIZE,2),NODATA,dtype='<i2')
                    flags = np.zeros((TILE_SIZE,TILE_SIZE),np.uint8)
                    values[:last_row-first_row,:last_column-first_column] = raster[:last_row-first_row,first_column:last_column]
                    flags[:last_row-first_row,:last_column-first_column] = support[:last_row-first_row,first_column:last_column]
                    header = HEADER.pack(b'NWFT',1,month,0,tile_row,column,
                        last_row-first_row,last_column-first_column,generation)
                    destination = args.output/f'tiles/{month:02d}/{tile_row:03d}/{column:03d}.nwf.gz'
                    jobs.append((destination,header,values,flags))
                outputs.extend(pool.map(write_tile,jobs))
            print(f'fine tiles rows {last_row}/{HEIGHT}; files {len(outputs)}; '
                  f'{time.monotonic()-started:.1f}s',flush=True)
    with (args.output/'tiles.bin').open('wb') as stream:
        np.array([len(tile_index)],dtype='<u4').tofile(stream)
        np.asarray(tile_index,dtype='<u2').tofile(stream)
    with (args.output/'tiles.sha256').open('w') as stream:
        for path,size,digest in outputs:
            stream.write(f'{digest}  {path.relative_to(args.output)}\n')
    shutil.copy2(pathlib.Path(__file__).with_name('ATTRIBUTION.txt'),args.output/'ATTRIBUTION.txt')
    shutil.copy2(args.reference_normals,args.output/'native-reference-normals.npz')
    shutil.copy2(args.land_support/'complete.json',args.output/'LAND-SUPPORT.json')
    write_source_overviews(args.output,overview_counts)
    polygon_metadata = publish_polygon_masks(args.output,args.land_support,args.source_cache)
    attach_timezones(args.reference_root,args.output,args.source_cache/'timezones')
    (args.output/'SOURCE-PROVENANCE.json').write_text(json.dumps(source_provenance,indent=2)+'\n')
    sizes = np.array([size for _,size,_ in outputs])
    metadata = dict(configuration=configuration,grid=dict(width=WIDTH,height=HEIGHT,tileSize=TILE_SIZE,
        west=-180,north=90,degreesPerPixel=1/120,pixel='containing pixel; centre west+(column+.5)/120, north-(row+.5)/120'),
        encoding=dict(headerStruct='<4sHBBHHHH16s',magic='NWFT',version=1,
            payload='256x256x2 interleaved little-endian int16 tenths C, then256x256 uint8 support',
            nodata=NODATA,compression='gzip',sourceBits={'0':'native ERA5','1':'PRISM','2':'WorldClim exact',
                '3':'WorldClim centered5x5 valid mean','4':'CHELSA'},
            supportBits={'8':'native ERA5 temperature reference available','16':'legacy v6 land-point membership, not a replay carrier',
                '32':'OSM or Natural Earth polygon membership, ODbL','64':'WorldClim paired exact land','128':'PRISM paired exact land'}),
        sourcesPerMonth=source_counts.tolist(),offsetRangesC=source_ranges.tolist(),
        tilesPerMonth=len(tile_index),files=len(outputs),compressedTileBytes=int(sizes.sum()),
        tileBytes=dict(minimum=int(sizes.min()),median=float(np.median(sizes)),p95=float(np.quantile(sizes,.95)),maximum=int(sizes.max())),
        licence='CC-BY-SA-4.0',licenceUrl='https://creativecommons.org/licenses/by-sa/4.0/',
        collection=True,componentLicences={'weatherOffsetsAndSourceProperties':'CC-BY-SA-4.0',
            'supportBit5PolygonMembership':'ODbL-1.0','land-membership/*':'ODbL-1.0','timezones/*':'ODbL-1.0'},
        polygonMembership=polygon_metadata,
        attribution='ATTRIBUTION.txt',completedUtc=dt.datetime.now(dt.timezone.utc).isoformat(),
        sourceOverviews='source-overviews: dominant/weakest categorical PNGs and exact quarter-cell source counts',
        seconds=time.monotonic()-started)
    (args.output/'manifest.json').write_text(json.dumps(metadata,indent=2)+'\n')
    print(json.dumps(metadata),flush=True)


if __name__ == '__main__': main()
