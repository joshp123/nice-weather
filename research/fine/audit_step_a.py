"""Audit current-format range loss against unencoded corrected aggregates.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
This reads the actual assembled intermediates, not station observations.
It does not alter a snapshot or the climate assembler.
"""
import argparse
import json
import pathlib

import numpy as np
from PIL import Image

from native_normals import file_sha256
import pipeline
from static import POINT_DTYPE


def audit(build_root,publication_root):
    points = np.fromfile(publication_root/'points.bin',dtype=POINT_DTYPE,offset=4)
    cells = (publication_root/'cells.bin').stat().st_size//4
    chunks = sorted(json.loads((publication_root/'climate/meta.json').read_text())['chunks'])
    totals = np.zeros((len(points),12,8),np.float64)
    baseline = np.zeros((len(points),12),np.float64)
    days = np.zeros(12,int)
    for chunk in chunks:
        names = sorted((build_root/f'climate-v6-work/chunk{chunk}').glob('r*.npz'))
        _,dates = pipeline.chunk_dates(chunk)
        months = np.array([date.month for date in dates])
        sums = np.zeros((cells,len(dates),29),np.float32)
        counts = np.zeros(cells,np.int32)
        for path in names:
            with np.load(path) as saved:
                sums[saved['cells']] += saved['sums']
                counts[saved['cells']] += saved['counts']
        expected = np.bincount(points['cell'],minlength=cells)
        if not np.array_equal(counts,expected): raise ValueError('Incomplete native cell coverage')
        features = sums/counts[:,None,None].astype(np.float32)
        del sums
        days += np.bincount(months-1,minlength=12)
        for path in names:
            with np.load(path) as saved:
                ids = saved['point_ids']
                totals[ids] += saved['monthly']
                for month in range(1,13):
                    values = features[points['cell'][ids]][:,months==month]
                    prefix = np.cumsum((values[:,:,:11]+values[:,:,11:22])*.5,axis=2)
                    cutoff = saved['daylight'][:,months==month].astype(int)-1
                    baseline[ids,month-1] += np.take_along_axis(prefix,cutoff[:,:,None],axis=2)[:,:,0].sum(1)
        print(f'audited assembled chunk {chunk}',flush=True)
    offsets = (totals[:,:,6]-baseline)/totals[:,:,7]
    encoded = np.clip(np.rint(offsets*10)+128,0,254).astype(np.uint8)
    for month in range(1,13):
        actual = np.asarray(Image.open(publication_root/f'climate/offsets-{month:02d}.png')).ravel()[points['fine']]
        if not np.array_equal(actual,encoded[:,month-1]): raise ValueError('Offset encoding disagrees with unencoded aggregates')
    values = totals[:,:,:3]/days[None,:,None]
    fact_encoded = np.clip(np.rint((values+30)*3),0,255).astype(np.uint8)
    actual = np.memmap(publication_root/'climate/facts.bin',mode='r',dtype=np.uint8,shape=(721*1440,12,6))
    if not np.array_equal(actual[points['fine'],:,:3],fact_encoded): raise ValueError('Fact encoding disagrees with aggregates')
    decoded = (encoded.astype(float)-128)/10
    return dict(chunks=chunks,points=len(points),point_months=int(offsets.size),
        offset_range_c=[float(offsets.min()),float(offsets.max())],
        offset_outside_range=int(((offsets < -12.8)|(offsets > 12.6)).sum()),
        offset_rounds_outside_range=int(((np.rint(offsets*10)+128<0)|(np.rint(offsets*10)+128>254)).sum()),
        offset_maximum_encoding_error_c=float(np.abs(offsets-decoded).max()),
        fact_fields=['daytime air high','21:00 felt','native capped dew point'],
        fact_ranges_c=[[float(values[:,:,i].min()),float(values[:,:,i].max())] for i in range(3)],
        fact_outside_range_by_field=[int(((values[:,:,i]<-30)|(values[:,:,i]>55)).sum()) for i in range(3)],
        facts_sha256=file_sha256(publication_root/'climate/facts.bin'),encoding_matches=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--build-root',type=pathlib.Path,required=True)
    parser.add_argument('--publication-root',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    args = parser.parse_args()
    result = audit(args.build_root,args.publication_root)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result),flush=True)


if __name__ == '__main__': main()
