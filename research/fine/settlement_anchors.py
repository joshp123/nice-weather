"""Population-weight fine anchors inside each native quarter-point footprint.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
The rule is fixed globally: resident counts, then land area, then own native.
No station observations enter these calculations.
"""
import argparse
import datetime as dt
import hashlib
import json
import pathlib
import time

import numpy as np

from anchors import AnchorSources, SOURCE_NAMES, load_era5_mean_normals
from native_normals import file_sha256
from settlement_sources import SettlementPopulation


SSD = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
PERIOD_NAMES = {1:'prism',2:'worldclim14',3:'worldclim14',4:'chelsa'}
REPRESENTATION_RULE = (
    'Within the nearest-quarter Voronoi footprint (+/-0.125 degrees), sample '
    'global 30arcsecond pixel centers. Weight paired temperature anchors by '
    'GHSL2020 resident population counts, without area multiplication. Positive '
    'population establishes eligibility even outside the land mask. '
    'If resident count is zero, use cosine-latitude-weighted land area from '
    'Natural Earth or WorldClim/PRISM validity (land.u8 bits0..2; excludes OSM-only); '
    'if no eligible land, retain the selected point native high/low. Each fine '
    'anchor receives its own bilinear common ERA5 period-mean anomaly before '
    'weighting. Missing climate anchors retain that pixel weight with the '
    'selected own native high/low. No station fitting or place exceptions.')


def dense_modern_mean(path, expected_chunks):
    with np.load(path) as normals:
        if not np.array_equal(normals['chunks'],expected_chunks):
            raise ValueError('Fine-pixel and selected-point normals use different chunks')
        dense = np.full((12,721*1440),np.nan,np.float32)
        dense[:,normals['fine']] = normals['mean_c'].T
    return dense.reshape(12,721,1440)


def bilinear(field, rows, columns):
    lower_row = np.floor(rows).astype(np.int64)
    upper_row = np.minimum(lower_row+1,720)
    lower_column = np.floor(columns).astype(np.int64)%1440
    upper_column = (lower_column+1)%1440
    row_fraction = rows-lower_row
    column_fraction = columns-np.floor(columns)
    result = np.zeros(rows.shape,np.float64)
    for r,rweight in ((lower_row,1-row_fraction),(upper_row,row_fraction)):
        for c,cweight in ((lower_column,1-column_fraction),(upper_column,column_fraction)):
            weight = rweight*cweight
            values = field[r,c]
            if not np.isfinite(values[weight>0]).all():
                missing = np.flatnonzero((weight>0)&~np.isfinite(values))
                raise ValueError(f'Missing nonzero-weight ERA5 corner: {len(missing)} pixels; '
                                 f'first native corner {int(r[missing[0]])},{int(c[missing[0]])}')
            result += np.where(weight>0,values,0)*weight
    return result


def adjusted_samples(sources,historical,modern,latitude,longitude,native_high,native_low,owners):
    sampled = sources.sample(latitude,longitude)
    high,low = sampled.tmax_c.copy(),sampled.tmin_c.copy()
    rows,columns = (90-latitude)*4,((longitude+180)*4)%1440
    for month in range(12):
        for code,period in PERIOD_NAMES.items():
            mask = sampled.source[month] == code
            if mask.any():
                anomaly = bilinear(modern[month],rows[mask],columns[mask])
                anomaly -= bilinear(historical[period][month],rows[mask],columns[mask])
                high[month,mask] += anomaly
                low[month,mask] += anomaly
        missing = sampled.source[month] == 0
        high[month,missing] = native_high[owners[missing],month]
        low[month,missing] = native_low[owners[missing],month]
    if not (np.isfinite(high).all() and np.isfinite(low).all()):
        raise ValueError('A weighted anchor or native fallback lacks finite paired high/low')
    return high,low,sampled.source


def reduce_anchors(high,low,source,owners,weights,native_high,native_low):
    count = len(native_high)
    total = np.bincount(owners,weights=weights,minlength=count)
    valid = total>0
    result_high,result_low = native_high.copy(),native_low.copy()
    fractions = np.zeros((count,12,5),np.float32)
    fractions[~valid,:,0] = 1
    for month in range(12):
        result_high[valid,month] = np.bincount(owners,weights=weights*high[month],
                                             minlength=count)[valid]/total[valid]
        result_low[valid,month] = np.bincount(owners,weights=weights*low[month],
                                            minlength=count)[valid]/total[valid]
        for code in range(5):
            fractions[valid,month,code] = np.bincount(owners,
                weights=weights*(source[month]==code),minlength=count)[valid]/total[valid]
    return result_high,result_low,fractions


def process_band(fine,native_high,native_low,population,land,sources,historical,modern):
    count = len(fine)
    quarter_rows,quarter_columns = fine//1440,fine%1440
    offsets = np.arange(-15,15)
    fine_rows = np.broadcast_to(quarter_rows[:,None,None]*30+offsets[None,:,None],(count,30,30))
    fine_columns = np.broadcast_to((quarter_columns[:,None,None]*30+offsets[None,None,:])%43200,
                                   (count,30,30))
    valid_rows = (fine_rows>=0)&(fine_rows<21600)
    owners = np.broadcast_to(np.arange(count)[:,None,None],(count,30,30))[valid_rows]
    fine_rows,fine_columns = fine_rows[valid_rows],fine_columns[valid_rows]
    latitude,longitude = 90-(fine_rows+.5)/120,-180+(fine_columns+.5)/120
    resident_counts = population.sample(latitude,longitude)
    positive_population = np.isfinite(resident_counts)&(resident_counts>0)
    resident_weights = np.where(positive_population,resident_counts,0)
    polygon_land = (land[fine_rows,fine_columns]&7)>0
    area_weights = np.where(polygon_land,np.cos(np.deg2rad(latitude)),0)
    population_sum = np.bincount(owners,weights=resident_weights,minlength=count)
    area_sum = np.bincount(owners,weights=area_weights,minlength=count)
    population_pixels = np.bincount(owners,weights=positive_population,minlength=count)
    polygon_pixels = np.bincount(owners,weights=polygon_land,minlength=count)
    primary_weights = np.where(population_sum[owners]>0,resident_weights,area_weights)
    needed = (primary_weights>0)|(area_weights>0)
    high,low,source = adjusted_samples(sources,historical,modern,latitude[needed],longitude[needed],
                                      native_high,native_low,owners[needed])
    primary_high,primary_low,fractions = reduce_anchors(high,low,source,owners[needed],
        primary_weights[needed],native_high,native_low)
    area_high,area_low,_ = reduce_anchors(high,low,source,owners[needed],area_weights[needed],
                                        native_high,native_low)
    center_high,center_low,center_source = adjusted_samples(sources,historical,modern,
        90-quarter_rows*.25,-180+quarter_columns*.25,native_high,native_low,np.arange(count))
    fallback = np.where(population_sum>0,0,np.where(area_sum>0,1,2)).astype(np.uint8)
    return dict(adjusted_high_c=primary_high,adjusted_low_c=primary_low,
        area_adjusted_high_c=area_high,area_adjusted_low_c=area_low,
        centre_adjusted_high_c=center_high.T,centre_adjusted_low_c=center_low.T,
        centre_source=center_source.T,source_fractions=fractions,
        source=fractions.argmax(2).astype(np.uint8),
        population_sum=population_sum,population_positive_pixels=population_pixels.astype(np.uint16),
        polygon_land_pixels=polygon_pixels.astype(np.uint16),fallback_mode=fallback,
        population_outside_polygon=np.bincount(owners,
            weights=resident_weights*~polygon_land,minlength=count),
        population_missing_land_pixels=np.bincount(owners,
            weights=~np.isfinite(resident_counts)&polygon_land,minlength=count).astype(np.uint16))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fine-ids',type=pathlib.Path,
                        help='JSON array of native quarter IDs; omit for every point in native order')
    parser.add_argument('--output',required=True,type=pathlib.Path)
    parser.add_argument('--summary',type=pathlib.Path)
    parser.add_argument('--source-cache',type=pathlib.Path,default=SSD/'fine-sources')
    parser.add_argument('--population-cache',type=pathlib.Path,default=SSD/'fine-sources/settlement')
    parser.add_argument('--native-normals',type=pathlib.Path,
        default=SSD/'fine-build/normals-76-78-80-82-84/normals.npz')
    parser.add_argument('--reference-normals',type=pathlib.Path,
        default=SSD/'fine-build/reference-global/normals/normals.npz')
    parser.add_argument('--land-support',type=pathlib.Path,default=SSD/'fine-build/land-support-v2')
    args = parser.parse_args()
    started = time.monotonic()
    args.output.parent.mkdir(parents=True,exist_ok=True)
    summary_path = args.summary or args.output.with_suffix('.summary.json')
    with np.load(args.native_normals) as normals:
        all_fine = normals['fine'].copy()
        lookup = {int(fine):index for index,fine in enumerate(all_fine)}
        fine = (np.asarray(json.loads(args.fine_ids.read_text()),np.uint32)
                if args.fine_ids else all_fine)
        if len(set(fine.tolist())) != len(fine):
            raise ValueError('Requested fine IDs must be unique')
        positions = np.array([lookup[int(point)] for point in fine])
        result = {key:normals[key][positions].copy() for key in ['high_c','low_c','mean_c']}
        chunks,days = normals['chunks'].copy(),normals['days'].copy()
    if not all(np.isfinite(values).all() for values in result.values()):
        raise ValueError('Selected native normals must be finite')
    native_high,native_low,native_mean = result['high_c'],result['low_c'],result['mean_c']
    population = SettlementPopulation(args.population_cache)
    configuration = dict(representation_rule=REPRESENTATION_RULE,
        fine_sha256=hashlib.sha256(fine.tobytes()).hexdigest(),
        native_normals_sha256=file_sha256(args.native_normals),
        reference_normals_sha256=file_sha256(args.reference_normals),
        land_support_complete=json.loads((args.land_support/'complete.json').read_text()),
        land_sha256=file_sha256(args.land_support/'land.u8'),
        settlement_dataset=population.metadata,
        population_sampler_sha256=file_sha256(pathlib.Path(__file__).with_name('settlement_sources.py')),
        source_manifests={name:file_sha256(args.source_cache/name/'dataset.json')
                          for name in ['prism','worldclim14','chelsa']},
        historical_mean_sha256={period:file_sha256(args.source_cache/'era5_monthly'/name)
            for period,name in [('worldclim14','mean_1960_1990.npy'),
                                ('chelsa','mean_1981_2010.npy'),('prism','mean_1991_2020.npy')]},
        script_sha256=file_sha256(__file__),
        anchor_sampler_sha256=file_sha256(pathlib.Path(__file__).with_name('anchors.py')),
        chunks=chunks.tolist(),monthly_days=days.tolist())
    # Per-latitude-band checkpoints avoid rereading source rasters after interruption.
    checkpoint_root = args.output.parent/(args.output.stem+'-bands')
    checkpoint_root.mkdir(exist_ok=True)
    configuration_path = checkpoint_root/'configuration.json'
    if configuration_path.exists() and json.loads(configuration_path.read_text()) != configuration:
        raise ValueError('Existing band checkpoints have different inputs; choose a fresh output folder')
    configuration_path.write_text(json.dumps(configuration,indent=2)+'\n')
    modern = dense_modern_mean(args.reference_normals,chunks)
    historical = load_era5_mean_normals(args.source_cache)
    sources = AnchorSources(args.source_cache)
    land = np.memmap(args.land_support/'land.u8',mode='r',dtype=np.uint8,shape=(21600,43200))
    combined = {}
    quarter_rows = fine//1440
    bands = np.unique(quarter_rows)
    for number,row in enumerate(bands,1):
        ids = np.flatnonzero(quarter_rows==row)
        checkpoint = checkpoint_root/f'row-{int(row):03d}.npz'
        if checkpoint.exists():
            with np.load(checkpoint) as saved:
                if not np.array_equal(saved['fine'],fine[ids]):
                    raise ValueError('Checkpoint point selection changed')
                band_result = {key:saved[key] for key in saved.files if key!='fine'}
        else:
            band_result = process_band(fine[ids],native_high[ids],native_low[ids],population,land,
                                      sources,historical,modern)
            temporary = checkpoint.with_suffix('.partial.npz')
            np.savez_compressed(temporary,fine=fine[ids],**band_result)
            temporary.replace(checkpoint)
        for key,values in band_result.items():
            if key not in combined:
                combined[key] = np.empty((len(fine),)+values.shape[1:],values.dtype)
            combined[key][ids] = values
        print(f'band {number}/{len(bands)} row{row}: {len(ids)} points; '
              f'{time.monotonic()-started:.1f}s',flush=True)
    population.close()
    combined.update(fine=fine,native_high_c=native_high,native_low_c=native_low,
                    native_mean_c=native_mean,chunks=chunks,days=days)
    for prefix in ('','area_','centre_'):
        high = combined[prefix+'adjusted_high_c']
        low = combined[prefix+'adjusted_low_c']
        combined[prefix+'uniform_offset_c'] = ((high-native_high+low-native_low)*.5).astype(np.float32)
    combined['high_offset_c'] = combined['adjusted_high_c']-native_high
    combined['low_offset_c'] = combined['adjusted_low_c']-native_low
    temporary = args.output.with_suffix('.partial.npz')
    np.savez_compressed(temporary,**combined)
    temporary.replace(args.output)
    summary = dict(configuration,created_utc=dt.datetime.now(dt.timezone.utc).isoformat(),
        points=len(fine),output=str(args.output),output_sha256=file_sha256(args.output),
        source_fraction_mean={SOURCE_NAMES[code]:float(combined['source_fractions'][:,:,code].mean())
                              for code in range(5)},
        fallback_point_counts={name:int((combined['fallback_mode']==code).sum())
                               for code,name in enumerate(['population','land_area','native_no_land'])},
        total_population=float(combined['population_sum'].sum()),
        population_outside_polygon=float(combined['population_outside_polygon'].sum()),
        missing_population_land_pixels=int(combined['population_missing_land_pixels'].sum()),
        native_fallback_weight_pointmonths=float(combined['source_fractions'][:,:,0].sum()),
        uniform_shift_range_c=[float(combined['uniform_offset_c'].min()),
                               float(combined['uniform_offset_c'].max())],
        source_field='Dominant weighted source for diagnostics; mixed source_fractions are authoritative.',
        quarter_row_range=[int(quarter_rows.min()),int(quarter_rows.max())],
        footprint='30x30 canonical30arcsecond pixel centers, quarter coordinate +/-0.125deg; clipped only at poles, longitude wraps.',
        area_mask='(land.u8 & 7)>0: independent Natural Earth, WorldClim paired-valid-any-month, PRISM paired-January validity; OSM-only bit8 excluded.',
        population_sampling='Containing source cell from GHSL producer affine at each canonical anchor pixel center; missing values tracked, never interpreted as residents.',
        fallback_mode='0 resident-count weighting; 1 area-weighted independent land bits0..2; 2 own native, no eligible land.',
        missing_corner_policy='Fail on any missing nonzero-weight ERA5 corner; no borrowing or renormalization.',
        seconds=time.monotonic()-started)
    summary_path.write_text(json.dumps(summary,indent=2)+'\n')
    print(json.dumps({key:summary[key] for key in ['points','fallback_point_counts','uniform_shift_range_c','seconds']},indent=2),flush=True)


if __name__ == '__main__':
    main()
