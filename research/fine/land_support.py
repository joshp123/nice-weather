"""Retain fine source land and independently licensed coastline support.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Natural Earth 1:10m is a cartographic scale, not ten-metre resolution. It never
discards a valid WorldClim/PRISM coastal or island pixel. Polygon-only coasts
remain distinguishable in the source/support map.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import time
import zipfile
import shutil
import struct

import numpy as np

from anchors import AnchorSources, containing_pixel_indices
from source_cache import save_evidence,download
from native_normals import file_sha256

WIDTH,HEIGHT = 43200,21600


def natural_earth(cache,layer='ne_10m_land'):
    directory = pathlib.Path(cache)/'natural-earth'
    directory.mkdir(parents=True,exist_ok=True)
    path = directory/f'{layer}.geojson'
    evidence = directory/f'{layer}.source.json'
    if path.exists() and evidence.exists():
        metadata = json.loads(evidence.read_text())
        if file_sha256(path) != metadata['sha256']: raise ValueError('Land polygons changed')
        return path,metadata
    revision = subprocess.check_output(['git','ls-remote',
        'https://github.com/nvkelso/natural-earth-vector.git','HEAD'],text=True).split()[0]
    url = f'https://raw.githubusercontent.com/nvkelso/natural-earth-vector/{revision}/geojson/{layer}.geojson'
    temporary = path.with_suffix('.partial')
    subprocess.run(['curl','--fail','--location','--silent','--show-error','--retry','4',
                    '--connect-timeout','20','--max-time','300',url,'--output',str(temporary)],check=True)
    document = json.loads(temporary.read_text())
    if document['type'] != 'FeatureCollection': raise ValueError('Expected land GeoJSON')
    temporary.replace(path)
    terms = directory/'terms-of-use.html'
    save_evidence('https://www.naturalearthdata.com/about/terms-of-use/',terms)
    metadata = dict(url=url,revision=revision,sha256=file_sha256(path),bytes=path.stat().st_size,
        licence='Public domain',licence_url='https://www.naturalearthdata.com/about/terms-of-use/',
        licence_evidence_sha256=file_sha256(terms),
        accessed_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()))
    evidence.write_text(json.dumps(metadata,indent=2)+'\n')
    return path,metadata


def coastline(cache):
    directory = pathlib.Path(cache)/'osm-land'
    directory.mkdir(parents=True,exist_ok=True)
    archive_path = directory/'land-polygons-split-4326.zip'
    source = download('https://osmdata.openstreetmap.de/download/land-polygons-split-4326.zip',archive_path,
                      workers=24,part_bytes=1024*1024)
    terms = save_evidence('https://osmdata.openstreetmap.de/info/license.html',directory/'licence.html')
    description = save_evidence('https://osmdata.openstreetmap.de/data/land-polygons.html',directory/'description.html')
    odbl = save_evidence('https://opendatacommons.org/licenses/odbl/1-0/',directory/'ODbL-1.0.html')
    extracted = directory/'extracted'
    extracted.mkdir(exist_ok=True)
    with zipfile.ZipFile(archive_path) as archive:
        for member in archive.infolist():
            if member.is_dir(): continue
            name = pathlib.PurePosixPath(member.filename).name
            path = extracted/name
            if path.exists() and path.stat().st_size == member.file_size: continue
            temporary = path.with_suffix(path.suffix+'.partial')
            with archive.open(member) as source_stream,temporary.open('wb') as destination:
                shutil.copyfileobj(source_stream,destination,8*1024*1024)
            # ZipExtFile checks every extracted member's CRC before EOF.
            if temporary.stat().st_size != member.file_size: raise ValueError('Incomplete coastline member')
            temporary.replace(path)
    shapefiles = list(extracted.glob('*.shp'))
    if len(shapefiles) != 1: raise ValueError('Expected one coastline shapefile')
    metadata = dict(source=source,licence='ODbL-1.0',licence_evidence=terms,
        description_evidence=description,licence_text=odbl,
        files_sha256={path.name:file_sha256(path) for path in sorted(extracted.iterdir()) if path.is_file()})
    (directory/'dataset.json').write_text(json.dumps(metadata,indent=2)+'\n')
    return shapefiles[0],metadata


def polygon_mask(geometries,path,batch_size=512):
    from rasterio.features import rasterize,bounds
    from rasterio.transform import from_origin
    result = np.memmap(path,mode='w+',dtype=np.uint8,shape=(HEIGHT,WIDTH))
    result[:] = 0
    batch = []
    def paint(shapes):
        boxes = np.array([bounds(geometry) for geometry in shapes])
        west,south = boxes[:,:2].min(0)
        east,north = boxes[:,2:].max(0)
        first_row,last_row = max(0,int(np.floor((90-north)*120))),min(HEIGHT,int(np.floor((90-south)*120))+1)
        first_column,last_column = max(0,int(np.floor((west+180)*120))),min(WIDTH,int(np.floor((east+180)*120))+1)
        if first_row>=last_row or first_column>=last_column: return
        block = rasterize(((shape,1) for shape in shapes),out_shape=(last_row-first_row,last_column-first_column),
            transform=from_origin(-180+first_column/120,90-first_row/120,1/120,1/120),
            all_touched=True,dtype=np.uint8)
        result[first_row:last_row,first_column:last_column] |= block
    for geometry in geometries:
        batch.append(geometry)
        if len(batch) == batch_size:
            paint(batch); batch.clear()
    if batch: paint(batch)
    result.flush()
    return result


def natural_geometries(paths):
    for path in paths:
        for feature in json.loads(path.read_text())['features']:
            yield feature['geometry']


def osm_geometries(path):
    import shapefile
    # Producer record order scatters islands worldwide. Spatial batches avoid
    # rasterizing a nearly global empty rectangle for every512 small polygons.
    index = np.fromfile(path.with_suffix('.shx'),dtype='>i4',offset=100).reshape(-1,2)
    boxes = np.empty((len(index),4))
    with path.open('rb') as stream:
        for number,(offset,_) in enumerate(index):
            stream.seek(int(offset)*2+12)
            boxes[number] = struct.unpack('<4d',stream.read(32))
    latitude_bin = np.floor(((boxes[:,1]+boxes[:,3])/2+90)/2)
    longitude_bin = np.floor(((boxes[:,0]+boxes[:,2])/2+180)/2)
    order = np.argsort(latitude_bin*180+longitude_bin,kind='stable')
    with shapefile.Reader(str(path)) as source:
        for number in order:
            shape = source.shape(int(number))
            if shape.points: yield shape.__geo_interface__


def prepare_support(cache,output,strip_rows=256):
    started = time.monotonic()
    output = pathlib.Path(output)
    output.mkdir(parents=True,exist_ok=True)
    source_hashes = {name:file_sha256(pathlib.Path(cache)/name/'dataset.json')
                     for name in ('prism','worldclim14','chelsa')}
    if (output/'complete.json').exists():
        saved = json.loads((output/'complete.json').read_text())
        if saved['builder_sha256'] != file_sha256(__file__) or saved['source_metadata_sha256'] != source_hashes:
            raise ValueError('Land support inputs changed; choose a new output folder')
        return output/'land.u8'
    layers = [natural_earth(cache,layer) for layer in ('ne_10m_land','ne_10m_minor_islands')]
    provenance = [metadata for path,metadata in layers]
    coast_path,coast_metadata = coastline(cache)
    polygon = polygon_mask(natural_geometries([path for path,metadata in layers]),output/'natural-earth.u8')
    osm = polygon_mask(osm_geometries(coast_path),output/'osm.u8')
    sources = AnchorSources(cache)
    mask = np.memmap(output/'land.u8',mode='w+',dtype=np.uint8,shape=(HEIGHT,WIDTH))
    # Independent properties: NE, WC validity, PRISM validity, OSM membership.
    for begin in range(0,HEIGHT,strip_rows):
        end = min(HEIGHT,begin+strip_rows)
        block = np.asarray(polygon[begin:end]).copy() | (osm[begin:end]*8)
        wc_rows = min(end,18000)-begin
        if wc_rows>0:
            valid = np.zeros((wc_rows,WIDTH),bool)
            for month in range(1,13):
                high = sources.worldclim('tmax',month)[begin:begin+wc_rows]
                low = sources.worldclim('tmin',month)[begin:begin+wc_rows]
                valid |= (high != -9999)&(low != -9999)
            block[:wc_rows] |= valid.astype(np.uint8)*2
        grid = sources.datasets['prism']['variables']['tmax']['1']['grid']
        latitudes = 90-(np.arange(begin,end)+.5)/120
        rows,_ = containing_pixel_indices(latitudes,np.zeros_like(latitudes),grid)
        valid_rows = (rows>=0)&(rows<grid['height'])
        longitudes = -180+(np.arange(WIDTH)+.5)/120
        _,columns = containing_pixel_indices(np.zeros_like(longitudes),longitudes,grid)
        valid_columns = (columns>=0)&(columns<grid['width'])
        if valid_rows.any() and valid_columns.any():
            r,c = np.meshgrid(rows[valid_rows],columns[valid_columns],indexing='ij')
            high = sources.tiff('prism','tmax',1).sample(r.ravel(),c.ravel()).reshape(r.shape)
            low = sources.tiff('prism','tmin',1).sample(r.ravel(),c.ravel()).reshape(r.shape)
            valid = (high != grid['nodata'])&(low != grid['nodata'])&np.isfinite(high)&np.isfinite(low)
            block[np.ix_(valid_rows,valid_columns)] |= valid.astype(np.uint8)*4
        mask[begin:end] = block
        if begin%(strip_rows*10)==0:
            print(f'fine land support {end}/{HEIGHT} {time.monotonic()-started:.1f}s',flush=True)
    mask.flush()
    needed = []
    for row in range(61):
        first,last = max(0,row*360-30),min(HEIGHT,(row+1)*360+30)
        if first>=last: continue
        for column in range(120):
            columns = np.arange(column*360-30,(column+1)*360+30)%WIDTH
            if np.any(mask[first:last][:,columns]):
                needed.append(f'r{row:02d}c{(column+60)%120:03d}.npz')
    (output/'reference-tiles.json').write_text(json.dumps(sorted(needed),indent=2)+'\n')
    metadata = dict(grid=dict(width=WIDTH,height=HEIGHT,west=-180,north=90,degrees_per_pixel=1/120),
        support_bits={'1':'Natural Earth land polygon','2':'WorldClim paired exact land pixel',
                      '4':'PRISM paired exact land pixel','8':'OpenStreetMap land polygon'},
        polygon_rasterization='any touched pixel; independent union preserves islands inside other polygon holes',
        polygon_licence='ODbL-1.0 for OSM and NE union; source validity retains independent source licences',
        natural_earth=provenance,openstreetmap=coast_metadata,
        builder_sha256=file_sha256(__file__),source_metadata_sha256=source_hashes,
        required_native_tiles=len(needed),
        pixels=int(np.count_nonzero(mask)),sha256=file_sha256(output/'land.u8'),
        seconds=time.monotonic()-started)
    (output/'complete.json').write_text(json.dumps(metadata,indent=2)+'\n')
    return output/'land.u8'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    args = parser.parse_args()
    print(prepare_support(args.source_cache,args.output),flush=True)


if __name__ == '__main__': main()
