"""Written by AI on 2026.10.17 by model gpt-6-astra.
Public ERA5 1991–2020 normal baseline for PRISM spatial corrections.
uv run --with numpy python -I prism_era5_baseline.py
"""
import json,time,datetime
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import urlopen
import numpy as np
HERE=Path(__file__).resolve().parent
stations=json.loads((HERE/'stations.json').read_text())[:8]
raw_path=HERE/'prism_era5_1991_2020_raw.json'
query=dict(latitude=','.join(str(s['lat']) for s in stations),longitude=','.join(str(s['lon']) for s in stations),start_date='1991-01-01',end_date='2020-12-31',daily='temperature_2m_max,temperature_2m_min',models='era5',timezone='America/Los_Angeles')
url='https://archive-api.open-meteo.com/v1/archive?'+urlencode(query)
(HERE/'prism_era5_1991_2020_request.txt').write_text(url+'\n')
if not raw_path.exists():
    while not all((HERE/(s['slug']+'_era5.json')).exists() for s in stations):
        print('Waiting for parent modern ERA5 downloads',datetime.datetime.now().isoformat(),flush=True)
        time.sleep(30)
    # Parent pauses 42 s after its final request; leave 50 s before ours.
    print('Modern files ready; allowing 50 s rate-limit spacing',flush=True)
    time.sleep(50)
    for attempt in range(6):
        try:
            with urlopen(url,timeout=240) as response:raw=response.read()
            data=json.loads(raw)
            if isinstance(data,dict) and data.get('error'):raise RuntimeError(data)
            raw_path.write_bytes(raw)
            break
        except Exception as error:
            print('Retry',error,flush=True)
            time.sleep(50+10*attempt)
    else:raise RuntimeError('ERA5 baseline fetch failed')
data=json.loads(raw_path.read_text())
assert len(data)==len(stations)
result={}
for station,point in zip(stations,data):
    daily=point['daily']; months=np.array([int(d[5:7]) for d in daily['time']])
    row=dict(station=station['station'],latitude=station['lat'],longitude=station['lon'],resolved_latitude=point['latitude'],resolved_longitude=point['longitude'],elevation=point['elevation'],timezone=point['timezone'],baseline='1991-2020')
    for variable,key in [('temperature_2m_max','tmax'),('temperature_2m_min','tmin')]:
        values=np.asarray(daily[variable],dtype=float)
        row[key]=[float(np.nanmean(values[months==month])) for month in range(1,13)]
    result[station['slug']]=row
(HERE/'prism_era5_1991_2020_normals.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v['tmax'][6] for k,v in result.items()}),flush=True)
