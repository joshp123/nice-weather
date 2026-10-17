"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Inspect existing WorldClim tiles and range-read CHELSA for coastal resolution,
land coverage and representative lossless tile sizes. No production data change.
Run with sh research/fine/run.sh resolution.py.
"""

import json
import math
import pathlib
import struct
import time
import zipfile
import zlib

import numpy as np
import tifffile

from chelsa import CachedRangeReader, source_url


SCRATCH = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1')
OUTPUT = pathlib.Path('/tmp/nwf-agents/round4/fine/resolution.json')


class ChelsaRaster:
    def __init__(self, month):
        self.reader = CachedRangeReader(source_url('tasmax', month),
                                       SCRATCH / 'chelsa_ranges' / f'tasmax-{month:02d}')
        self.raster = tifffile.TiffFile(self.reader)
        self.page = self.raster.pages[0]
        self.dx, self.dy, _ = self.page.tags['ModelPixelScaleTag'].value
        _, _, _, self.west, self.north, _ = self.page.tags['ModelTiepointTag'].value
        self.tiles = {}

    def tile(self, row, column):
        page = self.page
        across = math.ceil(page.imagewidth / page.tilewidth)
        index = row // page.tilelength * across + column // page.tilewidth
        if index not in self.tiles:
            self.reader.seek(page.dataoffsets[index])
            encoded = self.reader.read(page.databytecounts[index])
            self.tiles[index] = np.asarray(page.decode(encoded, index)[0]).reshape(
                page.tilelength, page.tilewidth)
        return self.tiles[index]

    def sample(self, lat, lon):
        row = math.floor((self.north-lat) / self.dy)
        column = math.floor((lon-self.west) / self.dx)
        if not (0 <= row < self.page.imagelength and 0 <= column < self.page.imagewidth):
            return dict(exact=None, mean_5x5=None, valid_pixels=0, outside_extent=True)
        neighbors = []
        exact = None
        for r in range(max(0, row-2), min(self.page.imagelength, row+3)):
            for c in range(max(0, column-2), min(self.page.imagewidth, column+3)):
                value = int(self.tile(r, c)[r % self.page.tilelength, c % self.page.tilewidth])
                if value != 65535:
                    neighbors.append(value / 10 - 273.15)
                    if (r, c) == (row, column):
                        exact = value / 10 - 273.15
        return dict(exact=exact, mean_5x5=float(np.mean(neighbors)) if neighbors else None,
                    valid_pixels=len(neighbors), outside_extent=False)

    def extent(self):
        return [self.west, self.north-self.page.imagelength*self.dy,
                self.west+self.page.imagewidth*self.dx, self.north]


def worldclim_tile(lat, lon, month, variable='tmax'):
    tile = f'{int((90-lat)//30)}{int((180+lon)//30)}'
    path = SCRATCH / f'worldclim14_{variable}_{tile}.zip'
    if not path.exists():
        return None, None
    with zipfile.ZipFile(path) as archive:
        stem = f'{variable}{month}_{tile}'
        header = dict(line.split(None, 1) for line in archive.read(stem+'.hdr').decode().splitlines()
                      if len(line.split(None, 1)) == 2)
        values = np.frombuffer(archive.read(stem+'.bil'), '<i2').reshape(
            int(header['NROWS']), int(header['NCOLS']))
    return header, values


def sample_worldclim(header, values, lat, lon):
    if header is None:
        return dict(exact=None, mean_5x5=None, reason='tile_not_in_existing_cache')
    row = math.floor((float(header['ULYMAP'])-lat)/float(header['YDIM'])+.5)
    column = math.floor((lon-float(header['ULXMAP']))/float(header['XDIM'])+.5)
    if not (0 <= row < values.shape[0] and 0 <= column < values.shape[1]):
        return dict(exact=None, mean_5x5=None, reason='outside_extent')
    window = values[max(0,row-2):row+3,max(0,column-2):column+3]
    valid = window[window != -9999]
    return dict(exact=float(values[row,column])/10 if values[row,column] != -9999 else None,
                mean_5x5=float(valid.mean())/10 if valid.size else None,
                valid_pixels=int(valid.size))


def png_bytes(values):
    """Actual PNG encoding with adaptive row filters, uint16 gray or uint8 RGB."""
    rgb = values.ndim == 3
    bytes_per_pixel = 3 if rgb else 2
    raw = values.astype(np.uint8 if rgb else '>u2').view(np.uint8).reshape(values.shape[0], -1)
    previous = np.zeros(raw.shape[1], np.uint8)
    rows = []
    for row in raw:
        left = np.concatenate([np.zeros(bytes_per_pixel,np.uint8),row[:-bytes_per_pixel]])
        upper_left = np.concatenate([np.zeros(bytes_per_pixel,np.uint8),previous[:-bytes_per_pixel]])
        average = ((left.astype(np.uint16)+previous)/2).astype(np.uint8)
        prediction = left.astype(np.int16)+previous.astype(np.int16)-upper_left.astype(np.int16)
        distances = [np.abs(prediction-left),np.abs(prediction-previous),np.abs(prediction-upper_left)]
        predictor = np.choose(np.argmin(distances,axis=0),[left,previous,upper_left])
        candidates = [row,row-left,row-previous,row-average,row-predictor]
        filter_index = min(range(5),key=lambda i:int(np.abs(candidates[i].view(np.int8).astype(np.int16)).sum()))
        rows.append(bytes([filter_index])+candidates[filter_index].tobytes())
        previous = row
    def chunk(kind, payload):
        return struct.pack('>I',len(payload))+kind+payload+struct.pack('>I',zlib.crc32(kind+payload))
    encoded = b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',values.shape[1],values.shape[0],8 if rgb else 16,2 if rgb else 0,0,0,0))
    return encoded+chunk(b'IDAT',zlib.compress(b''.join(rows),9))+chunk(b'IEND',b'')


def main():
    started = time.monotonic()
    rasters = {month: ChelsaRaster(month) for month in [1,7]}
    profiles = [
        ('San Francisco west to inland',37.75,-122.52,-122.10,7),
        ('Malaga coast east-west',36.72,-4.52,-4.34,7),
        ('Lima coast to inland',-12.05,-77.17,-76.95,1),
        ('Valparaiso coast to inland',-33.05,-71.67,-71.39,1),
    ]
    output_profiles = []
    headers = {}
    tile_measurements = []
    for name,lat,west,east,month in profiles:
        header,values = worldclim_tile(lat,(west+east)/2,month)
        minimum_header,minimum_values = worldclim_tile(lat,(west+east)/2,month,'tmin')
        headers[name] = header
        samples = []
        for lon in np.arange(west,east+1e-8,1/120):
            samples.append(dict(lat=lat,lon=float(lon),worldclim=sample_worldclim(header,values,lat,lon),
                                chelsa=rasters[month].sample(lat,float(lon))))
        output_profiles.append(dict(name=name,month=month,samples=samples))
        print(f'Transect {name}: {len(samples)} points',flush=True)
        if values is not None:
            for label,longitude in [('coast',west+.04),('inland',east-.02)]:
                row = math.floor((float(header['ULYMAP'])-lat)/float(header['YDIM'])+.5)
                column = math.floor((longitude-float(header['ULXMAP']))/float(header['XDIM'])+.5)
                r0 = min(max(0,row-128),values.shape[0]-256)
                c0 = min(max(0,column-128),values.shape[1]-256)
                tile = values[r0:r0+256,c0:c0+256]
                encoded = np.where(tile == -9999,65535,tile.astype(np.int32)+1000).astype(np.uint16)
                png = png_bytes(encoded)
                destination = SCRATCH/'resolution_tiles'/f'{name.split()[0].lower()}_{label}_256.png'
                destination.parent.mkdir(exist_ok=True)
                destination.write_bytes(png)
                measurement = dict(profile=name,label=label,source='WorldClim1.4',month=month,
                    land_fraction=float(np.mean(tile != -9999)),pixels=65536,raw_uint16_bytes=tile.nbytes,
                    png_uint16_bytes=len(png),zlib_uint16_bytes=len(zlib.compress(encoded.tobytes(),9)),
                    png_path=str(destination),encoding='0.1C offset+1000,65535NoData;oneanchor/one-month')
                if minimum_values is not None:
                    if any(header[k] != minimum_header[k] for k in ['ULXMAP','ULYMAP','XDIM','YDIM']):
                        raise ValueError('High/low grids differ')
                    minimum_tile = minimum_values[r0:r0+256,c0:c0+256]
                    encoded_minimum = np.where(minimum_tile == -9999,65535,
                                               minimum_tile.astype(np.int32)+1000).astype(np.uint16)
                    for anchor in [encoded,encoded_minimum]:
                        if np.any((anchor != 65535) & (anchor > 2046)):
                            raise ValueError('Anchor outside 11-bit temperature range')
                    packed = ((encoded.astype(np.uint32) & 2047) << 11) | (encoded_minimum & 2047)
                    rgb = np.stack([packed >> 16,(packed >> 8) & 255,packed & 255],axis=2).astype(np.uint8)
                    rgb_png = png_bytes(rgb)
                    rgb_path = destination.with_name(destination.stem+'_high_low_rgb.png')
                    rgb_path.write_bytes(rgb_png)
                    measurement.update(png_high_low_rgb_bytes=len(rgb_png),
                                       rgb_png_path=str(rgb_path),
                                       rgb_encoding='Opaque RGB8;22 packed bits:high11 then low11;0.1C+1000;2047NoData')
                tile_measurements.append(measurement)
    coverage_locations = [
        ('McMurdo',-77.85,166.67),('South Pole',-89.99,0),('North Greenland',83.6,-33),
        ('Svalbard Longyearbyen',78.22,15.65),('Tristan da Cunha',-37.105,-12.277),
        ('Gough',-40.32,-9.94),('Pitcairn',-25.066,-130.1),('Rapa Nui',-27.15,-109.42),
        ('Tuvalu Funafuti',-8.52,179.20),('Tarawa',1.45,172.98),('Male Maldives',4.175,73.51),
        ('Diego Garcia',-7.31,72.42),('St Helena',-15.965,-5.708),('Socotra',12.65,54.02),
        ('Funchal',32.65,-16.91),('Las Palmas',28.124,-15.43),('Cape Verde Praia',14.92,-23.51),
    ]
    coverage = []
    archive_samples = {}
    for name,lat,lon in coverage_locations:
        tile = f'{int((90-lat)//30)}{int((180+lon)//30)}'
        if tile not in archive_samples:
            archive_samples[tile] = worldclim_tile(lat,lon,7)
        coverage.append(dict(name=name,lat=lat,lon=lon,chelsa=rasters[7].sample(lat,lon),
                             worldclim=sample_worldclim(*archive_samples[tile],lat,lon)))
        print(f'Coverage {name}: CHELSA {coverage[-1]["chelsa"]["exact"]}',flush=True)
    ocean_checks = [dict(name=name,lat=lat,lon=lon,chelsa=rasters[7].sample(lat,lon))
                    for name,lat,lon in [('Pacific west of SF',37.75,-122.6),('Central Atlantic',0,-30)]]
    global_url = 'https://geodata.ucdavis.edu/climate/worldclim/1_4/grid/cur/tmax_30s_bil.zip'
    global_reader = CachedRangeReader(global_url,SCRATCH/'resolution_ranges/worldclim_30s_header')
    with zipfile.ZipFile(global_reader) as archive:
        header_name = next(name for name in archive.namelist() if name.endswith('.hdr'))
        global_header = dict(line.split(None,1) for line in archive.read(header_name).decode().splitlines()
                             if len(line.split(None,1)) == 2)
    output = dict(written_by='AI GPT-6.1 Sol 2026-10-17',seconds=time.monotonic()-started,
        units='degrees Celsius',chelsa_extent=rasters[7].extent(),worldclim_headers=headers,
        worldclim_global_header=global_header,worldclim_global_header_url=global_url,
        worldclim_coverage_primary='https://www.worldclim.org/data/v1.4/formats.html',
        coverage=coverage,ocean_checks=ocean_checks,transects=output_profiles,tile_compression=tile_measurements,
        coverage_proposal=dict(
            eligible_anchor_missing='Keep native ERA5 with zero fine residual and explicit coverage flag; '
                                    'do not reuse current sea-level terrain correction.',
            antarctica='WorldClim absent south of60S. CHELSA supplies tested Antarctic values but '
                       'has not been station-validated there.',
            independent_land_mask=dict(
                source='Natural Earth10m land v5.1.1 union minor-islands v4.1.0',
                licence='public domain; derivative publication allowed; attribution optional',
                licence_url='https://www.naturalearthdata.com/about/terms-of-use/',
                land_url='https://www.naturalearthdata.com/downloads/10m-physical-vectors/10m-land/',
                islands_url='https://www.naturalearthdata.com/downloads/10m-physical-vectors/10m-minor-islands/',
                advertised_download='3.12MB land plus307.55KB minor islands; no download made',
                rasterization='Union both polygon layers onto chosen temperature grid; retain '
                              'partial-land coverage instead of inheriting0.25degree land mask.',
                limitation='10m means1:10million map scale, not10metres. Minor islands are absent '
                           'from main land layer. Northern Russia/southern Chile coastline accuracy '
                           'and polar minor-island completeness are acknowledged source limitations.')),
        downloaded_bytes=sum(r.reader.downloaded_bytes for r in rasters.values())+global_reader.downloaded_bytes,
        caveats=['5x5 windows measure local smoothing, not globally aligned coarse cells.',
                 'Representative tile sizes are empirical examples, not a global storage total.',
                 'CHELSA point checks do not establish complete worldwide land-mask coverage.',
                 'WorldClim missing-cache tiles are untested, not absent data.',
                 'Grayscale16 PNGs encode one anchor; opaque RGB8 PNGs encode both high/low for one month.',
                 'Packed RGB PNGs need raw-channel decoding with color conversion disabled.',
                 'CHELSA has values at ocean coordinates; its valid-data mask is not a land mask.'])
    OUTPUT.write_text(json.dumps(output,indent=2))
    print(f'Saved {OUTPUT}; {output["downloaded_bytes"]:,} new range bytes; {output["seconds"]:.1f}s')


if __name__ == '__main__':
    main()
