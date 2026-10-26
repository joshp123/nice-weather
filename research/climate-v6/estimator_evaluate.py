# Written by AI on 2026.10.19 by model GPT-6.1 Sol.
"""Measure compact climate estimators against every historical day in a shared feature cache."""
import argparse
import gzip
import importlib.util
import json
import pathlib
import sys
import time
import numpy as np

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import pipeline

TASTES = {
    'default': {},
    'warmer': {'warm_shift': 3, 'heat_shift': -3, 'evening_shift': 2},
    'cooler': {'warm_shift': -3, 'heat_shift': 3, 'evening_shift': -2},
    'humidity_averse': {'humidity_shift': -4},
    'rain_averse': {'rain_scale': 4},
}
THRESHOLDS = np.array([15., 40., 55., 70., 85.])
MONTH_DAYS = np.array([31,28,31,30,31,30,31,31,30,31,30,31])


def score_features(features, taste, offset=0):
    daylight = np.maximum(1, np.rint(features[..., 27]))
    mask = np.arange(11) < daylight[..., None]
    hourly = np.minimum(pipeline.taste.COLD_GRADE(features[..., :11] + offset - taste.get('warm_shift', 0)),
                        pipeline.taste.HEAT_GRADE(features[..., 11:22] + offset + taste.get('heat_shift', 0)))
    evening = np.minimum(pipeline.taste.EVENING_GRADE(features[...,22] + offset - taste.get('evening_shift',0)),
                         pipeline.taste.HEAT_GRADE(features[...,22] + offset + taste.get('heat_shift',0)))
    thermal = ((hourly * mask).sum(-1) + 2 * evening) / (daylight + 2)
    return thermal * pipeline.taste.HUMIDITY_FACTOR(features[...,23] - taste.get('humidity_shift',0)) * pipeline.taste.RAIN_FACTOR(features[...,24] * taste.get('rain_scale',1)) * pipeline.taste.WIND_FACTOR(features[...,25] - taste.get('wind_shift',0)) * pipeline.taste.SUN_FACTOR(features[...,26] / daylight)


def shares(scores):
    return np.stack([(scores >= threshold - 0.00005).mean(-1) for threshold in THRESHOLDS], -1)


def interpolated_shares(scores):
    """Linear CDF between sorted mid-quantiles, with half-interval tail extrapolation."""
    ordered = np.sort(scores, axis=-1)
    count = scores.shape[-1]
    left_tail = np.maximum(0, ordered[..., :1] - (ordered[..., 1:2] - ordered[..., :1]) / 2)
    right_tail = np.minimum(100, ordered[..., -1:] + (ordered[..., -1:] - ordered[..., -2:-1]) / 2)
    lower = np.concatenate([left_tail, ordered], -1)
    upper = np.concatenate([ordered, right_tail], -1)
    probabilities = np.concatenate([[0], (np.arange(count)+.5)/count, [1]])
    result = []
    for threshold in THRESHOLDS:
        intervals = np.clip(np.divide(threshold-lower, upper-lower, out=np.zeros_like(lower), where=upper != lower),0,1)
        intervals = np.where(threshold > upper, 1, intervals)
        cdf = (intervals * np.diff(probabilities)).sum(-1)
        result.append(1-cdf)
    return np.stack(result,-1)


def kernel_shares(scores, width):
    return np.stack([np.clip(.5+(scores-threshold)/width,0,1).mean(-1) for threshold in THRESHOLDS],-1)


def summary(errors, cell_ids, cells):
    index = int(np.argmax(errors))
    cell = cells[cell_ids[index]]
    return dict(median=float(np.median(errors)), p95=float(np.quantile(errors,.95)), worst=float(errors[index]),
                worstCell=int(cell_ids[index]), worstLatitude=float(89.5-cell[0]), worstLongitude=float(-179.5+cell[1]))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--cells',type=int,default=1024)
    parser.add_argument('--cell-file',type=pathlib.Path)
    parser.add_argument('--start',type=int,default=0,help='Contiguous global batch when nonzero or --contiguous.')
    parser.add_argument('--contiguous',action='store_true')
    parser.add_argument('--offset',type=float,default=0)
    parser.add_argument('--samples',type=int,nargs='+',default=[4,8,12,16,24,32])
    args=parser.parse_args()
    started=time.monotonic()
    args.output.mkdir(parents=True,exist_ok=True)
    cells=np.fromfile(args.cache/'cells.bin',dtype='<u2').reshape(-1,2)
    cell_ids=np.unique(np.linspace(0,len(cells)-1,min(args.cells,len(cells))).astype(int)) if not args.cell_file else np.load(args.cell_file)
    if args.contiguous or args.start:
        cell_ids=np.arange(args.start,min(len(cells),args.start+args.cells))
    features=[]; months=[]
    for chunk in range(61,86):
        path=args.cache/f'chunk{chunk}/features.npy'
        while not (path.parent/'complete.json').exists(): time.sleep(5)
        part=np.load(path,mmap_mode='r')[cell_ids].copy()
        features.append(part)
        months.extend([date.month for date in pipeline.chunk_dates(chunk)[1]])
        print(f'loaded {chunk}',flush=True)
    features=np.concatenate(features,axis=1); months=np.asarray(months)
    records=pipeline.encode_records(features)
    decoded=pipeline.decode_records(records)
    default_compact=score_features(decoded,{})
    features[...,:23]+=args.offset
    decoded[...,:23]+=args.offset
    exact={name: np.zeros((len(cell_ids),12,5)) for name in TASTES}
    estimates={}
    compressed={count:0 for count in args.samples}
    monthly_counts=[]
    for month in range(1,13):
        selection=months==month
        full_features=features[:,selection]
        compact_features=decoded[:,selection]
        default=default_compact[:,selection]
        order=np.argsort(default,axis=1,kind='stable')
        monthly_counts.append(int(selection.sum()))
        exact_scores={name:score_features(full_features,taste) for name,taste in TASTES.items()}
        for name,scores in exact_scores.items():
            exact[name][:,month-1]=shares(scores)
            key=f'all_records_{name}'
            if key not in estimates: estimates[key]=np.zeros((len(cell_ids),12,5))
            estimates[key][:,month-1]=shares(score_features(compact_features,TASTES[name]))
        for count in args.samples:
            ranks=np.floor((np.arange(count)+.5)*selection.sum()/count).astype(int)
            indices=order[:,ranks]
            sampled_records=np.take_along_axis(records[:,selection],indices[:,:,None],axis=1)
            # Scale the stratified evaluation subset to the world's actual cell count.
            compressed[count]+=len(gzip.compress(sampled_records.tobytes(),compresslevel=6,mtime=0))*len(cells)/len(cell_ids)
            sampled=pipeline.decode_records(sampled_records)
            sampled[...,:23]+=args.offset
            baseline_scores=score_features(sampled,{})
            baseline_step=shares(baseline_scores)
            baseline_smooth=interpolated_shares(baseline_scores)
            for name,taste in TASTES.items():
                selected_scores=score_features(sampled,taste)
                step=shares(selected_scores); smooth=interpolated_shares(selected_scores)
                candidates = [('step',step),('linear_cdf',smooth),('step_cv',np.clip(step+exact['default'][:,month-1]-baseline_step,0,1)),('linear_cdf_cv',np.clip(smooth+exact['default'][:,month-1]-baseline_smooth,0,1))]
                for width in (4,8,16):
                    kernel=kernel_shares(selected_scores,width)
                    baseline_kernel=kernel_shares(baseline_scores,width)
                    candidates.extend([(f'kernel{width}',kernel),(f'kernel{width}_cv',np.clip(kernel+exact['default'][:,month-1]-baseline_kernel,0,1))])
                for estimator,value in candidates:
                    key=f'{count}_{estimator}_{name}'
                    if key not in estimates: estimates[key]=np.zeros((len(cell_ids),12,5))
                    estimates[key][:,month-1]=value
        print(f'month {month} done {time.monotonic()-started:.1f}s',flush=True)
    result=dict(offset=args.offset,cells=len(cell_ids),allCells=len(cells),samples=9000,tastes=TASTES,thresholds=THRESHOLDS.tolist(),monthlyDays=monthly_counts,seconds=time.monotonic()-started,estimatedGzipYearBytes=compressed,errors={})
    for key,values in estimates.items():
        taste=next(name for name in TASTES if key.endswith('_'+name))
        error=values-exact[taste]
        result['errors'][key]={}
        for period,month_ids in [('year',np.arange(12)),('nov_mar',np.array([10,11,0,1,2]))]:
            weighted=(error[:,month_ids] * MONTH_DAYS[month_ids,None]).sum(1)
            result['errors'][key][period]={str(int(threshold)):summary(np.abs(weighted[:,i]),cell_ids,cells) for i,threshold in enumerate(THRESHOLDS)}
    np.savez_compressed(args.output/'monthly-estimates.npz',cell_ids=cell_ids,exact=np.stack(list(exact.values())),estimates=np.stack(list(estimates.values())),keys=np.array(list(estimates)))
    (args.output/'results.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({k:v for k,v in result.items() if k!='errors'}),flush=True)

if __name__=='__main__': main()
