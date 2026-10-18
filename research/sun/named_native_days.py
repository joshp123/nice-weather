"""Cached full native replay extraction for the bounded sunshine sensitivity check.
Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""
import json,pathlib,sys
import numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parents[1]/'fine'))
import compare_step_a
from b1_data import point_features
base=pathlib.Path(sys.argv[1]);queries=json.load(sys.stdin);output=[]
def cached_field(raw_root,fine,chunk,name):
    row,column=divmod(int(fine),1440);column=(column+720)%1440
    filename=f'r{row//12:02d}c{column//12:03d}.npz'
    original=base/'fine-build/reference-global/era5'/f'chunk{chunk}'/filename
    with np.load(original) as data:
        if name in data.files:return data[name][:,row%12,column%12].astype(np.float32),original
    extra=base/'fine-sources/b1-worldwide-weather'/f'chunk{chunk}'/filename
    with np.load(extra) as data:return data[name][:,row%12,column%12].astype(np.float32),extra
compare_step_a.raw_field=cached_field
for query in queries:
    weather=[compare_step_a.point_weather(str(base),query['fineIndex'],chunk,query['timezone']) for chunk in query['chunks']]
    features=[point_features(row) for row in weather]
    output.append(dict(slug=query['slug'],dates=[date for row in weather for date in row['dates']],**{key:np.concatenate([row[key] for row in features]).astype(np.float32).reshape(-1).tolist() for key in features[0]}))
    print('Extracted '+query['slug'],file=sys.stderr)
json.dump(output,sys.stdout)
