"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Acquire and sample GHSL R2023A's anonymous 2020 global population count grid.
Counts describe resident population, not tourists or climate observations.
"""

import argparse
import json
import pathlib
import shutil
import time
import zipfile

import numpy as np

try:
    from .anchors import containing_pixel_indices
    from .source_cache import download, save_evidence, sha256_file
except ImportError:
    from anchors import containing_pixel_indices
    from source_cache import download, save_evidence, sha256_file


DEFAULT_CACHE = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-sources/settlement')
SOURCE_STEM = 'GHS_POP_E2020_GLOBE_R2023A_4326_30ss_V1_0'
SOURCE_URL = ('https://jeodpp.jrc.ec.europa.eu/ftp/jrc-opendata/GHSL/GHS_POP_GLOBE_R2023A/'
              'GHS_POP_E2020_GLOBE_R2023A_4326_30ss/V1-0/'+SOURCE_STEM+'.zip')
EVIDENCE_URLS = {
    'use-conditions.html': 'https://human-settlement.emergency.copernicus.eu/GHSLhowToCite.php',
    'product.html': 'https://human-settlement.emergency.copernicus.eu/ghs_pop2023.php',
    'dataset.html': 'https://data.jrc.ec.europa.eu/dataset/2ff68a52-5b5b-4a22-8f40-c41da8332cfe',
    'CC-BY-4.0.txt': 'https://creativecommons.org/licenses/by/4.0/legalcode.txt',
}
DATASET_CITATION = ('Schiavina, M., Freire, S., Carioli, A., MacManus, K. (2026). '
    'GHS-POP R2023A - GHS population grid multitemporal (1975-2030). '
    'European Commission, Joint Research Centre (JRC). '
    'https://doi.org/10.2905/JRC.CXKEDRR; '
    'https://doi.org/10.2905/2FF68A52-5B5B-4A22-8F40-C41DA8332CFE')
METHOD_CITATION = ('Pesaresi et al. (2024). Advances on the Global Human Settlement Layer '
    'by joint assessment of Earth Observation and population survey data. '
    'International Journal of Digital Earth, 17(1). '
    'https://doi.org/10.1080/17538947.2024.2390454')


def prepare(cache_root=DEFAULT_CACHE, workers=8):
    """Cache one 482 MB archive; verify extraction CRC and retain its provenance."""
    root = pathlib.Path(cache_root)
    root.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    archive_path = root/(SOURCE_STEM+'.zip')
    source = download(SOURCE_URL, archive_path, workers=workers)
    evidence = {name:save_evidence(url, root/'evidence'/name)
                for name,url in EVIDENCE_URLS.items()}
    with zipfile.ZipFile(archive_path) as archive:
        rasters = [entry for entry in archive.infolist()
                   if entry.filename.lower().endswith('.tif')]
        if len(rasters) != 1:
            raise ValueError(f'Expected one global population TIFF, found {len(rasters)}')
        member = rasters[0]
        raster_path = root/pathlib.Path(member.filename).name
        prior_manifest = root/'dataset.json'
        previous = json.loads(prior_manifest.read_text()) if prior_manifest.exists() else None
        if not previous or not raster_path.exists():
            partial = raster_path.with_suffix('.tif.partial')
            with archive.open(member) as stream, partial.open('wb') as output:
                shutil.copyfileobj(stream, output, 8*1024*1024)
            if partial.stat().st_size != member.file_size:
                raise ValueError('Population TIFF does not match archive member size')
            partial.replace(raster_path)
        elif sha256_file(raster_path) != previous['raster']['sha256']:
            raise ValueError('Population TIFF changed since preparation')
        archive_member = dict(name=member.filename,bytes=member.file_size,crc32=f'{member.CRC:08x}',
                              validation='Full extraction completed with Python ZIP CRC verification')
        for entry in archive.infolist():
            if entry.filename.lower().endswith(('.pdf','.txt','.xml','.html')):
                target = root/'source-documents'/pathlib.Path(entry.filename).name
                if not target.exists():
                    target.parent.mkdir(exist_ok=True)
                    target.write_bytes(archive.read(entry))
    import rasterio
    with rasterio.open(raster_path) as raster:
        if raster.count != 1 or raster.crs.to_epsg() != 4326 or raster.transform.b or raster.transform.d:
            raise ValueError('Expected one north-up WGS84 population band')
        transform = raster.transform
        grid = dict(north=transform.f,west=transform.c,dx=transform.a,dy=-transform.e,
                    width=raster.width,height=raster.height)
        minimum,maximum,total = np.inf,-np.inf,0.
        nonfinite,zeros,pixels = 0,0,0
        for _,window in raster.block_windows(1):
            values = raster.read(1,window=window)
            minimum = min(minimum,float(values.min()))
            maximum = max(maximum,float(values.max()))
            total += float(values.sum(dtype=np.float64))
            nonfinite += int((~np.isfinite(values)).sum())
            zeros += int((values==0).sum())
            pixels += values.size
        if minimum < 0 or nonfinite:
            raise ValueError('Source population counts must be finite and nonnegative')
        raster_metadata = dict(path=raster_path.name,bytes=raster_path.stat().st_size,
            sha256=sha256_file(raster_path),dtype=raster.dtypes[0],nodata=raster.nodata,
            grid=grid,crs=raster.crs.to_string(),transform=list(transform),
            block_shape=list(raster.block_shapes[0]),bounds=list(raster.bounds),
            archive_member=archive_member,
            decoded_statistics=dict(minimum=minimum,maximum=maximum,total_population=total,
                nonfinite_pixels=nonfinite,zero_pixels=zeros,pixels=pixels),
            missing_data='No nodata tag or sentinel in the actual raster; zero is valid. Queries outside its geographic extent return NaN.')
    metadata = dict(name='GHSL GHS-POP R2023A',version='V1-0',population_epoch=2020,
        units='inhabitants per source pixel; absolute count, not density',licence='CC-BY-4.0',
        source_archive=source,raster=raster_metadata,evidence=evidence,
        dataset_citation=DATASET_CITATION,methods_citation=METHOD_CITATION,
        copyright='© European Union, 1995-2025',
        permitted_use='Copy, redistribute and adapt, including commercial use, with credit, changes and no endorsement.',
        limitation='Modelled resident population disaggregated from administrative census totals using built-up information; not visitor or station weighting.',
        script_sha256=sha256_file(__file__),
        seconds=time.monotonic()-started)
    (root/'dataset.json').write_text(json.dumps(metadata,indent=2)+'\n')
    (root/'ATTRIBUTION.txt').write_text(attribution_text())
    print(json.dumps(metadata,indent=2),flush=True)
    return metadata


def attribution_text():
    return ('GHSL population weighting\n\n'
        '© European Union, 1995-2025. European Commission, Joint Research Centre.\n'
        'GHSL GHS-POP R2023A, epoch 2020, WGS84 30 arcsecond grid, V1-0.\n'
        'Licensed under Creative Commons Attribution 4.0 International:\n'
        'https://creativecommons.org/licenses/by/4.0/\n'
        'Source: '+SOURCE_URL+'\n'
        'Use conditions: '+EVIDENCE_URLS['use-conditions.html']+'\n\n'
        +DATASET_CITATION+'\n'+METHOD_CITATION+'\n\n'
        'Changes: source population counts select the relative weights of independent\n'
        'fine-grid climate anchors inside each quarter-degree footprint. The resulting\n'
        'weather values are derived calculations, not an official GHSL product.\n'
        'No endorsement by the European Union or the source authors is implied.\n')


class SettlementPopulation:
    """One reader per worker; sample containing cells without filling missing data."""

    def __init__(self,cache_root=DEFAULT_CACHE):
        import rasterio
        self.root = pathlib.Path(cache_root)
        self.metadata = json.loads((self.root/'dataset.json').read_text())
        self.grid = self.metadata['raster']['grid']
        self.path = self.root/self.metadata['raster']['path']
        self.raster = rasterio.open(self.path)

    def sample(self,latitudes,longitudes):
        latitude,longitude = np.broadcast_arrays(np.asarray(latitudes,np.float64),
                                                 np.asarray(longitudes,np.float64))
        longitude = (longitude+180)%360-180
        rows,columns = containing_pixel_indices(latitude,longitude,self.grid)
        output = np.full(latitude.size,np.nan,np.float64)
        valid = np.isfinite(latitude.ravel()) & np.isfinite(longitude.ravel())
        rows,columns = rows.ravel(),columns.ravel()
        valid &= (rows>=0)&(rows<self.raster.height)&(columns>=0)&(columns<self.raster.width)
        positions = np.flatnonzero(valid)
        if positions.size:
            block_height,block_width = self.raster.block_shapes[0]
            blocks_across = (self.raster.width+block_width-1)//block_width
            block_ids = rows[positions]//block_height*blocks_across+columns[positions]//block_width
            order = np.argsort(block_ids,kind='stable')
            sorted_blocks = block_ids[order]
            boundaries = np.r_[0,np.flatnonzero(np.diff(sorted_blocks))+1,positions.size]
            for begin,end in zip(boundaries[:-1],boundaries[1:]):
                selected = positions[order[begin:end]]
                block_id = int(sorted_blocks[begin])
                block_row,block_column = divmod(block_id,blocks_across)
                window = self.raster.block_window(1,block_row,block_column)
                values = self.raster.read(1,window=window,masked=True)
                counts = values[rows[selected]-int(window.row_off),columns[selected]-int(window.col_off)]
                output[selected] = counts.astype(np.float64).filled(np.nan)
        return output.reshape(latitude.shape)

    def sample_region(self,north,west,height,width,pixel_degrees=1/120):
        """Sample geographic cell centers; north/west identify the window edges."""
        latitudes = north-(np.arange(height)+.5)*pixel_degrees
        longitudes = west+(np.arange(width)+.5)*pixel_degrees
        return self.sample(latitudes[:,None],longitudes[None,:])

    def close(self):
        self.raster.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache-root',type=pathlib.Path,default=DEFAULT_CACHE)
    parser.add_argument('--workers',type=int,default=8)
    args = parser.parse_args()
    prepare(args.cache_root,args.workers)


if __name__ == '__main__':
    main()
