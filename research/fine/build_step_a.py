"""Build current-format fine climate corrections beside the published dataset.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Native cell-day sums are immutable inputs. Only fine monthly facts and felt
aggregates change. Native dew point, wind, sunshine and rain are retained.
"""
import argparse
import datetime as dt
import functools
import json
import multiprocessing
import pathlib
import shutil
import subprocess
import sys
import time

import numpy as np

from native_normals import file_sha256, local_hour_indices, sample_local
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parents[1]/'climate-v6'))
import pipeline
from static import POINT_DTYPE

SOURCE_PERIODS = {1:'prism',2:'worldclim14',3:'worldclim14',4:'chelsa'}
_WORKER_INPUTS = {}


def make_adjustment(native_root, normals_path, cache_root, output):
    from anchors import AnchorSources, load_era5_mean_normals
    points = np.fromfile(native_root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
    latitudes = 90-(points['fine']//1440)*.25
    longitudes = -180+(points['fine']%1440)*.25
    anchors = AnchorSources(cache_root).sample(latitudes,longitudes)
    historical = load_era5_mean_normals(cache_root)
    rows,columns = points['fine']//1440,points['fine']%1440
    with np.load(normals_path) as normals:
        if not np.array_equal(normals['fine'],points['fine']):
            raise ValueError('Native normals and point selection differ')
        high,low,mean = (normals[key].copy() for key in ('high_c','low_c','mean_c'))
        chunks,days = normals['chunks'].copy(),normals['days'].copy()
    source = anchors.source.T.copy()
    anchored_high,anchored_low = anchors.tmax_c.T.copy(),anchors.tmin_c.T.copy()
    for code,period in SOURCE_PERIODS.items():
        mask = source == code
        reference = historical[period][:,rows,columns].T
        anomaly = mean-reference
        anchored_high[mask] += anomaly[mask]
        anchored_low[mask] += anomaly[mask]
    eligible = (source != 0) & np.isfinite(anchored_high) & np.isfinite(anchored_low)
    eligible &= np.isfinite(high) & np.isfinite(low)
    source[~eligible] = 0
    high_offset = np.where(eligible,anchored_high-high,0).astype(np.float32)
    low_offset = np.where(eligible,anchored_low-low,0).astype(np.float32)
    uniform = (high_offset+low_offset)*.5
    np.savez_compressed(output,fine=points['fine'],source=source,
        high_offset_c=high_offset,low_offset_c=low_offset,uniform_offset_c=uniform,
        adjusted_high_c=np.where(eligible,anchored_high,high),
        adjusted_low_c=np.where(eligible,anchored_low,low),
        native_high_c=high,native_low_c=low,native_mean_c=mean,chunks=chunks,days=days)
    return dict(source_point_months={str(code):int((source==code).sum()) for code in range(5)},
        uniform_shift_range_c=[float(uniform.min()),float(uniform.max())],
        source_periods=SOURCE_PERIODS,monthly_days=days.tolist())


def vapor_pressure(dew):
    return 6.105*np.exp(17.27*dew/(237.7+dew))


def corrected_tile(job):
    native_root,build_root,adjustment_path,chunk,name = job
    native_root,build_root = pathlib.Path(native_root),pathlib.Path(build_root)
    destination = build_root/f'climate-v6-work/chunk{chunk}'/name
    if destination.exists():
        with np.load(destination) as existing:
            return name,float(existing['maximum_dew_cap_change_c'])
    with np.load(native_root/f'climate-v6-work/chunk{chunk}'/name) as native:
        saved = {key:native[key] for key in native.files}
    selected = saved['point_ids']
    key = (str(native_root),str(adjustment_path))
    if key not in _WORKER_INPUTS:
        with np.load(adjustment_path) as adjustments:
            uniform = adjustments['uniform_offset_c']
        _WORKER_INPUTS[key] = (np.fromfile(native_root/'v6/points.bin',dtype=POINT_DTYPE,offset=4),
            np.load(native_root/'climate-v6-work/timezones.npy',mmap_mode='r'),uniform)
    all_points,all_zones,all_shifts = _WORKER_INPUTS[key]
    points = all_points[selected]
    rows,columns = (points['fine']//1440)%12,((points['fine']%1440+720)%1440)%12
    zones = all_zones[selected]
    shift = all_shifts[selected]
    with np.load(native_root/f'era5/chunk{chunk}'/name) as raw:
        temperature = raw['t2m'][:,rows,columns].astype(np.float32)
        dew = raw['d2m'][:,rows,columns].astype(np.float32)
    _,dates = pipeline.chunk_dates(chunk)
    months = np.array([date.month for date in dates])
    monthly = saved['monthly'].astype(np.float64)
    maximum_dew_cap_change = 0.
    for zone in np.unique(zones):
        ids = np.flatnonzero(zones == zone)
        _,indices,fraction = local_hour_indices(chunk,zone,range(10,22))
        air = sample_local(temperature,indices,fraction,ids)
        native_dew = sample_local(dew,indices,fraction,ids)
        delta = shift[ids][:,months-1].T[:,None,:]
        old_cap = np.minimum(native_dew,air)
        new_cap = np.minimum(native_dew,air+delta)
        felt_delta = delta+.33*(vapor_pressure(new_cap)-vapor_pressure(old_cap))
        maximum_dew_cap_change = max(maximum_dew_cap_change,float(np.abs(new_cap-old_cap).max()))
        for month in range(1,13):
            day_mask = months == month
            changes = felt_delta[day_mask]
            daylight = saved['daylight'][ids][:,day_mask].T
            daylight_mask = np.arange(11)[None,:,None]<daylight[:,None,:]
            monthly[ids,month-1,0] += shift[ids,month-1]*day_mask.sum()
            monthly[ids,month-1,1] += changes[:,11,:].sum(0)
            monthly[ids,month-1,6] += (changes[:,:11,:]*daylight_mask).sum((0,1))
    saved['monthly'] = monthly.astype(np.float32)
    saved['fine_uniform_correction'] = np.array(True)
    saved['maximum_dew_cap_change_c'] = np.array(maximum_dew_cap_change)
    temporary = destination.with_suffix('.partial.npz')
    np.savez_compressed(temporary,**saved)
    temporary.replace(destination)
    return name,maximum_dew_cap_change


def prepare_build(native_root,build_root,chunks,adjustment_path,normals_path):
    build_root.mkdir(parents=True,exist_ok=True)
    configuration = dict(chunks=sorted(chunks),points_sha256=file_sha256(native_root/'v6/points.bin'),
        normals_sha256=file_sha256(normals_path),adjustment_sha256=file_sha256(adjustment_path),
        correction_sha256=file_sha256(__file__),
        assembler_sha256=file_sha256(pipeline.__file__),
        native_chunk_manifest_sha256={str(chunk):file_sha256(
            native_root/f'climate-v6-work/chunk{chunk}/complete.json') for chunk in chunks},
        model='uniform air shift at every hour; native Td; exact felt vapor-cap difference',
        cells='native ERA5; monthly fine aggregates corrected independently')
    manifest = build_root/'configuration.json'
    if manifest.exists() and json.loads(manifest.read_text()) != configuration:
        raise ValueError('Correction build has different chunks or inputs; choose a fresh build root')
    manifest.write_text(json.dumps(configuration,indent=2)+'\n')
    (build_root/'v6').mkdir(exist_ok=True)
    for name in ('cells.bin','points.bin','READY-static'):
        target = build_root/'v6'/name
        if not target.exists():
            shutil.copy2(native_root/'v6'/name,target)
    (build_root/'era5').mkdir(exist_ok=True)
    for name in ('static_lsm.npy','static_z.npy'):
        target = build_root/'era5'/name
        if not target.exists(): target.symlink_to(native_root/'era5'/name)
    (build_root/'climate-v6-work').mkdir(exist_ok=True)
    for chunk in chunks:
        completion = native_root/f'climate-v6-work/chunk{chunk}/complete.json'
        if not completion.exists():
            raise ValueError(f'Native chunk {chunk} is not yet complete')
        native_manifest = json.loads(completion.read_text())
        if native_manifest['points_sha256'] != configuration['points_sha256']:
            raise ValueError(f'Native chunk {chunk} uses different static points')
        (build_root/f'climate-v6-work/chunk{chunk}').mkdir(exist_ok=True)
    return configuration


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native-root',type=pathlib.Path,required=True)
    parser.add_argument('--build-root',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--normals',type=pathlib.Path,required=True)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--meta',type=pathlib.Path,required=True)
    parser.add_argument('--workers',type=int,default=4)
    parser.add_argument('--adjustment-only',action='store_true')
    args = parser.parse_args()
    started = time.monotonic()
    chunks = sorted(json.loads(args.meta.read_text())['chunks'])
    with np.load(args.normals) as normals:
        if sorted(normals['chunks'].tolist()) != chunks:
            raise ValueError('Normals must cover exactly the assembled metadata chunk set')
    args.build_root.mkdir(parents=True,exist_ok=True)
    adjustment_path = args.build_root/'adjustment.npz'
    source_summary = make_adjustment(args.native_root,args.normals,args.source_cache,adjustment_path)
    if args.adjustment_only:
        print(json.dumps(source_summary),flush=True)
        return
    configuration = prepare_build(args.native_root,args.build_root,chunks,adjustment_path,args.normals)
    names = sorted(json.loads((args.native_root/f'climate-v6-work/chunk{chunks[0]}/complete.json')
                             .read_text())['intermediate_sha256'])
    jobs = [(str(args.native_root),str(args.build_root),str(adjustment_path),chunk,name)
            for chunk in chunks for name in names]
    cap_change = 0.
    with multiprocessing.Pool(args.workers) as pool:
        for number,(name,change) in enumerate(pool.imap_unordered(corrected_tile,jobs),1):
            cap_change = max(cap_change,change)
            if number%100 == 0 or number == len(jobs):
                print(f'corrected fine aggregates {number}/{len(jobs)} {time.monotonic()-started:.1f}s',flush=True)
    # Reuse the current assembler unchanged; its validation output belongs here.
    import report
    original_validate = report.validate_snapshot
    report.validate_snapshot = functools.partial(original_validate,
        report_directory=args.build_root/'validation')
    try:
        snapshot = pipeline.assemble(args.build_root,publish=False)
    finally:
        report.validate_snapshot = original_validate
    climate = snapshot/'v6/climate'
    metadata = json.loads((climate/'meta.json').read_text())
    if sorted(metadata['chunks']) != chunks:
        raise ValueError('Assembler chunk set differs from the period adjustment')
    metadata['fineTemperature'] = dict(model=configuration['model'],sources=source_summary,
        nativeCells=True,nativeDewPoint=True,maximumDewCapChangeC=cap_change,
        adjustment_sha256=configuration['adjustment_sha256'],
        source='PRISM 1991-2020; WorldClim1.4; CHELSA2.1; common ERA5 monthly-mean period anomaly')
    (climate/'meta.json').write_text(json.dumps(metadata,indent=2)+'\n')
    shutil.copy2(pathlib.Path(__file__).with_name('ATTRIBUTION.txt'),snapshot/'v6/ATTRIBUTION.txt')
    provenance = {name:json.loads((args.source_cache/name/'dataset.json').read_text())
                  for name in ('worldclim14','prism','chelsa','era5_monthly')}
    (snapshot/'v6/SOURCE-PROVENANCE.json').write_text(json.dumps(provenance,indent=2)+'\n')
    script = pathlib.Path(__file__).resolve().parents[2]/'src/engine/default_scores.py'
    subprocess.run([sys.executable,str(script),str(snapshot/'v6')],check=True)
    subprocess.run([sys.executable,str(script),str(snapshot/'v6'),'--images',str(snapshot/'v6/images')],check=True)
    if args.output.exists():
        raise FileExistsError(f'Output already exists; use a new generation folder: {args.output}')
    shutil.copytree(snapshot/'v6',args.output)
    evidence = dict(configuration=configuration,sources=source_summary,snapshot=str(snapshot),
        output=str(args.output),maximum_dew_cap_change_c=cap_change,
        completed_utc=dt.datetime.now(dt.timezone.utc).isoformat(),seconds=time.monotonic()-started,
        output_sha256={str(path.relative_to(args.output)):file_sha256(path)
            for path in sorted(args.output.rglob('*')) if path.is_file()})
    (args.output/'fine-build.json').write_text(json.dumps(evidence,indent=2)+'\n')
    print(json.dumps(evidence),flush=True)


if __name__ == '__main__':
    main()
