"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Anonymous source acquisition and exact-pixel monthly high/low anchors.
Selection: PRISM, WorldClim1.4 exact, its centered5x5 valid window, CHELSA,
then native-only coverage. This module never changes production climate files.
"""

import argparse
import asyncio
import concurrent.futures
from dataclasses import dataclass
import json
import calendar
import hashlib
import math
import pathlib
import shutil
import time
import xml.etree.ElementTree as ET
import zipfile

import numpy as np
import tifffile

try:
    from .source_cache import download, save_evidence
except ImportError:
    from source_cache import download, save_evidence


DEFAULT_CACHE = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-sources')
SOURCE_NAMES = {0:'native_only',1:'prism',2:'worldclim_exact',3:'worldclim_5x5',4:'chelsa'}
SOURCE_PERIODS = {1:(1991,2020),2:(1960,1990),3:(1960,1990),4:(1981,2010)}
PERIODS = [(1960,1990),(1981,2010),(1991,2020)]


@dataclass
class AnchorSamples:
    tmax_c: np.ndarray
    tmin_c: np.ndarray
    source: np.ndarray
    months: tuple


def worldclim_url(variable):
    return f'https://geodata.ucdavis.edu/climate/worldclim/1_4/grid/cur/{variable}_30s_bil.zip'


def chelsa_url(variable,month):
    return ('https://os.unil.cloud.switch.ch/chelsa02/chelsa/global/climatologies/'
            f'{variable}/1981-2010/CHELSA_{variable}_{month:02d}_1981-2010_V.2.1.tif')


def prepare_worldclim(root,workers=6):
    directory = root/'worldclim14'
    save_evidence('https://www.worldclim.org/data/v1.4/worldclim14.html',directory/'licence.html')
    metadata = dict(source='WorldClim1.4',licence='CC-BY-SA-4.0',period=[1960,1990],
                    period_caveat='Nominal1960-1990; original interpolation also used some1950-2000/undated normals.',
                    variables={})
    def fetch(variable):
        archive_path = directory/f'{variable}_30s_bil.zip'
        return variable,archive_path,download(worldclim_url(variable),archive_path,workers=workers)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        archives = list(pool.map(fetch,['tmax','tmin']))
    for variable,archive_path,source in archives:
        months = {}
        with zipfile.ZipFile(archive_path) as archive:
            for month in range(1,13):
                stem = f'{variable}_{month}'
                header = dict(line.split(None,1) for line in archive.read(stem+'.hdr').decode().splitlines()
                              if len(line.split(None,1)) == 2)
                raw_path = directory/(stem+'.bil')
                expected = int(header['NROWS'])*int(header['NCOLS'])*2
                if not raw_path.exists():
                    partial = raw_path.with_suffix('.bil.partial')
                    with archive.open(stem+'.bil') as stream, partial.open('wb') as output:
                        shutil.copyfileobj(stream,output,8*1024*1024)
                    if partial.stat().st_size != expected:
                        raise ValueError(f'Wrong extracted raster size: {partial}')
                    partial.replace(raw_path)
                if raw_path.stat().st_size != expected:
                    raise ValueError(f'Wrong cached raster size: {raw_path}')
                months[str(month)] = dict(header=header,path=raw_path.name,units='0.1C',dtype='<i2')
                print(f'WorldClim {variable} {month:02d}: prepared {expected:,} raster bytes',flush=True)
        metadata['variables'][variable] = dict(archive=source,months=months)
    (directory/'dataset.json').write_text(json.dumps(metadata,indent=2))


def prepare_chelsa(root,workers=2):
    directory = root/'chelsa'
    save_evidence('https://www.chelsa-climate.org/datasets/chelsa_climatologies',directory/'licence.html')
    jobs = [(variable,month) for variable in ['tasmax','tasmin'] for month in range(1,13)]
    def prepare(job):
        variable,month = job
        path = directory/f'{variable}_{month:02d}.tif'
        source = download(chelsa_url(variable,month),path,workers=2)
        with tifffile.TiffFile(path) as raster:
            page = raster.pages[0]
            attributes = {entry.attrib['name']:entry.text
                          for entry in ET.fromstring(page.tags['GDAL_METADATA'].value)}
            dx,dy,_ = page.tags['ModelPixelScaleTag'].value
            _,_,_,west,north,_ = page.tags['ModelTiepointTag'].value
            if attributes['variable_unit'] != 'K':
                raise ValueError(attributes)
            grid = dict(west=west,north=north,dx=dx,dy=dy,width=page.imagewidth,
                        height=page.imagelength,nodata=int(page.tags['GDAL_NODATA'].value),
                        scale=float(attributes['SCALE']),offset=float(attributes.get('OFFSET','0'))-273.15)
        return variable,month,dict(path=path.name,source=source,grid=grid,attributes=attributes)
    metadata = dict(source='CHELSAv2.1',licence='CC0-1.0',period=[1981,2010],variables={})
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for variable,month,result in pool.map(prepare,jobs):
            metadata['variables'].setdefault(variable,{})[str(month)] = result
    (directory/'dataset.json').write_text(json.dumps(metadata,indent=2))


def prepare_prism(root,workers=2):
    directory = root/'prism'
    directory.mkdir(parents=True,exist_ok=True)
    jobs = [(variable,month) for variable in ['tmax','tmin'] for month in range(1,13)]
    def prepare(job):
        variable,month = job
        stem = f'prism_{variable}_us_30s_2020{month:02d}_avg_30y'
        url = f'ftp://prism.oregonstate.edu/normals/us/800m/{variable}/monthly/{stem}.zip'
        path = directory/(stem+'.zip')
        source = download(url,path,workers=8,part_bytes=1024*1024)
        with zipfile.ZipFile(path) as archive:
            names = [name for name in archive.namelist() if name.endswith('.tif')]
            if len(names) != 1:
                raise ValueError(f'Expected one PRISM GeoTIFF in {path}')
            raster_path = directory/pathlib.Path(names[0]).name
            if not raster_path.exists():
                partial = raster_path.with_suffix('.tif.partial')
                with archive.open(names[0]) as stream, partial.open('wb') as output:
                    shutil.copyfileobj(stream,output,8*1024*1024)
                partial.replace(raster_path)
        with tifffile.TiffFile(raster_path) as raster:
            page = raster.pages[0]
            dx,dy,_ = page.tags['ModelPixelScaleTag'].value
            _,_,_,west,north,_ = page.tags['ModelTiepointTag'].value
            grid = dict(west=west,north=north,dx=dx,dy=dy,width=page.imagewidth,
                        height=page.imagelength,nodata=float(page.tags['GDAL_NODATA'].value),
                        scale=1.0,offset=0.0,coordinate_reference_system='EPSG:4269')
            if not (abs(dx-1/120)<1e-10 and abs(dy-1/120)<1e-10):
                raise ValueError(f'Unexpected PRISM grid: {grid}')
        return variable,month,dict(path=raster_path.name,source=source,grid=grid)
    metadata = dict(source='PRISM800m1991-2020normals',
                    licence='PRISM terms: unrestricted distribution with attribution',
                    licence_url='https://prism.oregonstate.edu/terms/',
                    licence_evidence='research/fine/LICENCES.md; primary grant verified2026-10-17 in Phase1',
                    period=[1991,2020],variables={})
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for variable,month,result in pool.map(prepare,jobs):
            metadata['variables'].setdefault(variable,{})[str(month)] = result
    (directory/'dataset.json').write_text(json.dumps(metadata,indent=2))
    save_evidence('ftp://prism.oregonstate.edu/readme.txt',directory/'producer_README.txt')


def prepare_era5_means(root,workers=4):
    """Aggregate the producer's verified monthly mean, never its interval extrema."""
    import icechunk
    import zarr
    import imagecodecs

    directory = root/'era5_monthly'
    directory.mkdir(parents=True,exist_ok=True)
    save_evidence('https://raw.githubusercontent.com/PlanetteAI/planette_era5_archive/main/README.md',
                  directory/'producer_README.md')
    reference_path = directory/'archive.json'
    if reference_path.exists():
        metadata = json.loads(reference_path.read_text())
    else:
        repository = icechunk.Repository.open(icechunk.s3_storage(
            bucket='planette-era5',prefix='ERA5_ic/month',region='us-east-2',anonymous=True))
        session = repository.readonly_session('main')
        group = zarr.open_group(store=session.store,path='single/0p25latx0p25lon',mode='r')
        dates = (np.datetime64('1940-01-01')+group['time'][:].astype('timedelta64[D]')).astype('datetime64[M]')
        if not (np.array_equal(group['lat'][:],np.arange(90,-90.001,-.25)) and
                np.array_equal(group['lon'][:],np.arange(-179.75,180.001,.25))):
            raise ValueError('Unexpected ERA5 monthly coordinate grid')
        temperature = group['t2m']
        if temperature.shape[1:] != (721,1440) or temperature.chunks != (30,721,1440):
            raise ValueError('Unexpected ERA5 monthly chunk shape')
        async def references():
            results = []
            async for batch in session.store.array_chunk_iterator('single/0p25latx0p25lon/t2m'):
                for coordinates,kind,identifier,offset,length in zip(*batch[:5]):
                    if int(kind) != 1 or int(offset) != 0:
                        raise ValueError('Expected physical, whole-object monthly chunks')
                    results.append(dict(index=int(coordinates[0]),id=identifier,bytes=int(length)))
            return sorted(results,key=lambda result:result['index'])
        metadata = dict(source='Planette ERA5 monthly mean t2m',licence='CC-BY-4.0',
                        snapshot=str(session.snapshot_id),array=temperature.metadata.to_dict(),
                        dates=dates.astype(str).tolist(),chunks=asyncio.run(references()))
        reference_path.write_text(json.dumps(metadata,indent=2))
    dates = np.asarray(metadata['dates'],dtype='datetime64[M]')
    years = dates.astype('datetime64[Y]').astype(int)+1970
    month_numbers = dates.astype(int)%12+1
    days = np.array([calendar.monthrange(int(year),int(month))[1]
                     for year,month in zip(years,month_numbers)])
    required = (years>=min(period[0] for period in PERIODS))&(years<=max(period[1] for period in PERIODS))
    chunks = [chunk for chunk in metadata['chunks']
              if required[chunk['index']*30:min((chunk['index']+1)*30,len(dates))].any()]

    def fetch(chunk):
        url = f"https://planette-era5.s3.us-east-2.amazonaws.com/ERA5_ic/month/chunks/{chunk['id']}"
        path = directory/'chunks'/chunk['id']
        source = download(url,path,workers=8,part_bytes=1024*1024)
        if source['bytes'] != chunk['bytes']:
            raise ValueError(f"Monthly source chunk length changed: {chunk['id']}")
        return chunk,path

    sums = np.zeros((len(PERIODS),12,721,1440),np.float64)
    counts = np.zeros((len(PERIODS),12),np.int32)
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        futures = [pool.submit(fetch,chunk) for chunk in chunks]
        for future in concurrent.futures.as_completed(futures):
            chunk,path = future.result()
            decoded = imagecodecs.zstd_decode(path.read_bytes())
            values = np.frombuffer(decoded,dtype='<f4').reshape(30,721,1440)
            begin = chunk['index']*30
            for position in range(30):
                index = begin+position
                if index>=len(dates):
                    break
                month = month_numbers[index]-1
                for period_index,(first,last) in enumerate(PERIODS):
                    if first<=years[index]<=last:
                        if not np.isfinite(values[position]).all():
                            raise ValueError(f'Non-finite monthly mean in {dates[index]}')
                        sums[period_index,month] += values[position]*float(days[index])
                        counts[period_index,month] += days[index]
            print(f"ERA5 historical mean chunk {chunk['index']}: aggregated",flush=True)
    for index,(first,last) in enumerate(PERIODS):
        expected = [sum(calendar.monthrange(year,month)[1] for year in range(first,last+1))
                    for month in range(1,13)]
        if not np.array_equal(counts[index],expected):
            raise ValueError(f'Historical mean has missing months: {first}-{last}')
        means = sums[index]/counts[index,:,None,None]-273.15
        means = np.roll(means,1,axis=-1).astype(np.float32)
        np.save(directory/f'mean_{first}_{last}.npy',means)
    metadata['aggregation'] = 'Calendar-day-weighted monthly means, Celsius; longitude rolled to -180..179.75'
    metadata['periods'] = PERIODS
    (directory/'dataset.json').write_text(json.dumps(metadata,indent=2))


class TiffSamples:
    def __init__(self,path):
        self.raster = tifffile.TiffFile(path)
        self.page = self.raster.pages[0]

    def sample(self,rows,columns):
        page = self.page
        output = np.empty(rows.size,np.float32)
        if not rows.size:
            return output
        if not page.is_tiled:
            values = page.asarray()
            return values[rows,columns].astype(np.float32)
        tiles_across = math.ceil(page.imagewidth/page.tilewidth)
        indices = rows//page.tilelength*tiles_across+columns//page.tilewidth
        order = np.argsort(indices,kind='stable')
        sorted_indices = indices[order]
        boundaries = np.r_[0,np.flatnonzero(np.diff(sorted_indices))+1,rows.size]
        for begin,end in zip(boundaries[:-1],boundaries[1:]):
            positions = order[begin:end]
            tile_index = int(sorted_indices[begin])
            self.raster.filehandle.seek(page.dataoffsets[tile_index])
            compressed = self.raster.filehandle.read(page.databytecounts[tile_index])
            decoded = np.asarray(page.decode(compressed,tile_index)[0]).reshape(page.tilelength,page.tilewidth)
            output[positions] = decoded[rows[positions]%page.tilelength,columns[positions]%page.tilewidth]
        return output


class AnchorSources:
    """Local sampler requiring prepared PRISM, WorldClim and CHELSA.

    Each worker needs its own instance because TIFF seek/read handles are shared
    within one instance. Batching a month/strip avoids repeated tile decoding.
    """
    def __init__(self,cache_root=DEFAULT_CACHE,allow_worldclim_window=True):
        self.root = pathlib.Path(cache_root)
        self.allow_worldclim_window = allow_worldclim_window
        self.datasets = {name:json.loads((self.root/name/'dataset.json').read_text())
                         for name in ['prism','worldclim14','chelsa']}
        self.worldclim_arrays = {}
        self.tiff_arrays = {}

    def worldclim(self,variable,month):
        key = variable,month
        if key not in self.worldclim_arrays:
            info = self.datasets['worldclim14']['variables'][variable]['months'][str(month)]
            header = info['header']
            self.worldclim_arrays[key] = np.memmap(self.root/'worldclim14'/info['path'],
                dtype=info['dtype'],mode='r',shape=(int(header['NROWS']),int(header['NCOLS'])))
        return self.worldclim_arrays[key]

    def tiff(self,dataset,variable,month):
        key = dataset,variable,month
        if key not in self.tiff_arrays:
            info = self.datasets[dataset]['variables'][variable][str(month)]
            self.tiff_arrays[key] = TiffSamples(self.root/dataset/info['path'])
        return self.tiff_arrays[key]

    def sample_region(self,north,west,height,width,months=None,pixel_degrees=1/120):
        """Sample cell centers of a geographic raster window (north/west edges).

        Batch one month and one 512-row strip for a global build. A single call
        groups every requested pixel by source COG tile, decoding each once.
        """
        latitude = north-(np.arange(height)[:,None]+.5)*pixel_degrees
        longitude = west+(np.arange(width)[None,:]+.5)*pixel_degrees
        return self.sample(latitude,longitude,months)

    def sample(self,latitudes,longitudes,months=None):
        latitude,longitude = np.broadcast_arrays(np.asarray(latitudes,np.float64),np.asarray(longitudes,np.float64))
        coordinate_shape = latitude.shape
        latitude = latitude.ravel()
        longitude = ((longitude.ravel()+180)%360)-180
        if not (np.isfinite(latitude).all() and np.isfinite(longitude).all() and (np.abs(latitude)<=90).all()):
            raise ValueError('Coordinates must be finite latitude/longitude degrees')
        months = tuple(range(1,13) if months is None else months)
        if not months or any(month not in range(1,13) for month in months):
            raise ValueError('Months must be in1..12')
        shape = (len(months),latitude.size)
        high = np.full(shape,np.nan,np.float32)
        low = np.full(shape,np.nan,np.float32)
        source = np.zeros(shape,np.uint8)
        for index,month in enumerate(months):
            # Geographic domain selection is fixed; missing paired values retain the next source.
            for dataset,variables,code in [('prism',('tmax','tmin'),1),('worldclim14',('tmax','tmin'),2),
                                            ('chelsa',('tasmax','tasmin'),4)]:
                positions = np.flatnonzero(source[index] == 0)
                if not positions.size:
                    break
                if dataset == 'worldclim14':
                    header = self.datasets[dataset]['variables']['tmax']['months'][str(month)]['header']
                    grid = dict(west=float(header['ULXMAP'])-float(header['XDIM'])/2,
                                north=float(header['ULYMAP'])+float(header['YDIM'])/2,
                                dx=float(header['XDIM']),dy=float(header['YDIM']),
                                width=int(header['NCOLS']),height=int(header['NROWS']))
                else:
                    grid = self.datasets[dataset]['variables'][variables[0]][str(month)]['grid']
                rows = np.floor((grid['north']-latitude[positions])/grid['dy']).astype(np.int64)
                columns = np.floor((longitude[positions]-grid['west'])/grid['dx']).astype(np.int64)
                inside = (rows>=0)&(rows<grid['height'])&(columns>=0)&(columns<grid['width'])
                positions,rows,columns = positions[inside],rows[inside],columns[inside]
                if not positions.size:
                    continue
                values = []
                for variable in variables:
                    if dataset == 'worldclim14':
                        array = self.worldclim(variable,month)
                        raw = array[rows,columns]
                        values.append(np.where(raw == -9999,np.nan,raw.astype(np.float32)/10))
                    else:
                        variable_grid = self.datasets[dataset]['variables'][variable][str(month)]['grid']
                        raw = self.tiff(dataset,variable,month).sample(rows,columns)
                        values.append(np.where(raw == variable_grid['nodata'],np.nan,
                                               raw*variable_grid['scale']+variable_grid['offset']))
                valid = np.isfinite(values[0])&np.isfinite(values[1])
                selected = positions[valid]
                high[index,selected],low[index,selected] = values[0][valid],values[1][valid]
                source[index,selected] = code
                if dataset == 'worldclim14' and self.allow_worldclim_window:
                    missing = ~valid
                    if missing.any():
                        sums = [np.zeros(missing.sum(),np.float64),np.zeros(missing.sum(),np.float64)]
                        counts = [np.zeros(missing.sum(),np.uint8),np.zeros(missing.sum(),np.uint8)]
                        r,c = rows[missing],columns[missing]
                        for dr in range(-2,3):
                            valid_row = (r+dr>=0)&(r+dr<grid['height'])
                            for dc in range(-2,3):
                                for j,variable in enumerate(variables):
                                    raw = self.worldclim(variable,month)[np.clip(r+dr,0,grid['height']-1),
                                                                        (c+dc)%grid['width']]
                                    present = valid_row&(raw != -9999)
                                    sums[j][present] += raw[present]/10
                                    counts[j][present] += 1
                        valid_window = (counts[0]>0)&(counts[1]>0)
                        selected = positions[missing][valid_window]
                        high[index,selected] = sums[0][valid_window]/counts[0][valid_window]
                        low[index,selected] = sums[1][valid_window]/counts[1][valid_window]
                        source[index,selected] = 3
        return AnchorSamples(high.reshape((len(months),)+coordinate_shape),
                             low.reshape((len(months),)+coordinate_shape),
                             source.reshape((len(months),)+coordinate_shape),months)


def load_era5_mean_normals(cache_root=DEFAULT_CACHE):
    root = pathlib.Path(cache_root)/'era5_monthly'
    return {name:np.load(root/f'mean_{period[0]}_{period[1]}.npy',mmap_mode='r')
            for name,period in [('worldclim14',(1960,1990)),('chelsa',(1981,2010)),('prism',(1991,2020))]}


def load_era5_target_mean(months,cache_root=DEFAULT_CACHE,workers=3,month_day_weights=None):
    """Global month-of-year means for an exact set of complete YYYY-MM months.

    Default weights are calendar days. month_day_weights can supply the actual
    selected-day counts of non-calendar chunks. Individual retained days cannot
    be recovered from a monthly archive; those weights still use full-month T.
    Historical source preparation creates archive.json; shared chunks are reused.
    Missing month-of-year support remains NaN, with zero support days in metadata.
    """
    import imagecodecs

    requested = sorted(set(str(np.datetime64(month,'M')) for month in months))
    if not requested:
        raise ValueError('At least one complete month is required')
    directory = pathlib.Path(cache_root)/'era5_monthly'
    metadata = json.loads((directory/'archive.json').read_text())
    weights = {}
    for month in requested:
        year,number = map(int,month.split('-'))
        calendar_days = calendar.monthrange(year,number)[1]
        weight = calendar_days if month_day_weights is None else month_day_weights[month]
        if not (float(weight).is_integer() and 0<weight<=calendar_days):
            raise ValueError(f'Invalid selected-day weight for {month}: {weight}')
        weights[month] = int(weight)
    identity = hashlib.sha256(json.dumps([metadata['snapshot'],weights],sort_keys=True).encode()).hexdigest()[:20]
    output_path = directory/f'target_{identity}.npy'
    if output_path.exists():
        return np.load(output_path,mmap_mode='r')
    dates = metadata['dates']
    index_for_month = {month:index for index,month in enumerate(dates)}
    missing = set(requested)-index_for_month.keys()
    if missing:
        raise ValueError(f'ERA5 monthly archive has no requested months: {sorted(missing)}')
    indices = {index_for_month[month] for month in requested}
    chunks = [chunk for chunk in metadata['chunks'] if chunk['index'] in {index//30 for index in indices}]
    def fetch(chunk):
        url = f"https://planette-era5.s3.us-east-2.amazonaws.com/ERA5_ic/month/chunks/{chunk['id']}"
        path = directory/'chunks'/chunk['id']
        download(url,path,workers=8,part_bytes=1024*1024)
        return chunk,path
    sums = np.zeros((12,721,1440),np.float64)
    counts = np.zeros(12,np.int32)
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for chunk,path in pool.map(fetch,chunks):
            values = np.frombuffer(imagecodecs.zstd_decode(path.read_bytes()),dtype='<f4').reshape(30,721,1440)
            for offset in range(30):
                index = chunk['index']*30+offset
                if index in indices:
                    year,month = map(int,dates[index].split('-'))
                    days = weights[dates[index]]
                    if not np.isfinite(values[offset]).all():
                        raise ValueError(f'Non-finite monthly mean in {dates[index]}')
                    sums[month-1] += values[offset]*float(days)
                    counts[month-1] += days
    result = np.full(sums.shape,np.nan,np.float32)
    supported = counts>0
    result[supported] = np.roll(sums[supported]/counts[supported,None,None]-273.15,1,axis=-1)
    temporary = output_path.with_suffix('.partial.npy')
    np.save(temporary,result)
    temporary.replace(output_path)
    output_path.with_suffix('.json').write_text(json.dumps(dict(
        snapshot=metadata['snapshot'],months=requested,month_day_weights=weights,support_days=counts.tolist(),
        units='C',grid='721x1440; latitude90..-90; longitude-180..179.75',
        aggregation='Monthly mean t2m weighted by selected-day counts',
        limitation='Partly selected months use full-month mean T; exact retained-day normals require raw hourly data.'),indent=2))
    return np.load(output_path,mmap_mode='r')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command',choices=['worldclim','chelsa','prism','era5-monthly','era5-target'])
    parser.add_argument('--cache',type=pathlib.Path,default=DEFAULT_CACHE)
    parser.add_argument('--workers',type=int,default=6)
    parser.add_argument('--months',help='Comma-separated complete YYYY-MM months for era5-target')
    arguments = parser.parse_args()
    arguments.cache.mkdir(parents=True,exist_ok=True)
    started = time.monotonic()
    if arguments.command == 'worldclim':
        prepare_worldclim(arguments.cache,arguments.workers)
    elif arguments.command == 'chelsa':
        prepare_chelsa(arguments.cache)
    elif arguments.command == 'era5-monthly':
        prepare_era5_means(arguments.cache,min(arguments.workers,4))
    elif arguments.command == 'prism':
        prepare_prism(arguments.cache)
    elif arguments.command == 'era5-target':
        if not arguments.months:
            parser.error('era5-target requires --months')
        load_era5_target_mean(arguments.months.split(','),arguments.cache,min(arguments.workers,3))
    print(f'Prepared {arguments.command} in{time.monotonic()-started:.1f}s',flush=True)


if __name__ == '__main__':
    main()
