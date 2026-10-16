"""Written by AI on 2026.10.17 by model gpt-6-astra.
Fetch the public PRISM explorer's 800 m, 1991–2020 point monthly normals.
Run: uv run --with numpy python -I prism_fetch.py
"""
import json
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request, urlopen

POINTS = [
    ('half_moon_bay','USC00043714',37.4725,-122.4433),
    ('san_francisco','USW00023272',37.7706,-122.4269),
    ('sfo','USW00023234',37.6197,-122.3656),
    ('oakland','USC00046336',37.7983,-122.2642),
    ('berkeley','USC00040693',37.8744,-122.2606),
    ('palo_alto','USC00046646',37.4436,-122.1403),
    ('san_jose','USW00023293',37.3594,-121.9244),
    ('livermore','USW00023285',37.6931,-121.8150),
    ('sf_oceanside','USC00047767',37.7281,-122.5053),
]
ENDPOINT='https://prism.oregonstate.edu/explorer/dataexplorer/rpc.php'
HERE=Path(__file__).resolve().parent
results={}
for place,station,lat,lon in POINTS:
    params=dict(proc='gridserv',call='pp/monthly_normals_timeseries',lon=lon,lat=lat,
                spares='800m',stats='tmax tmin',units='si',interp=0,range='monthly_normals')
    path=HERE/f'prism_{place}.json'
    if not path.exists():
        request=Request(ENDPOINT,data=urlencode(params).encode(),headers={'User-Agent':'BayAreaClimateResearch/1.0'})
        with urlopen(request,timeout=120) as response:
            data=json.load(response)
        if data.get('errors') or 'result' not in data:
            raise RuntimeError(data)
        path.write_text(json.dumps(data,indent=2)+'\n')
    else:
        data=json.loads(path.read_text())
    values=data['result']['data']
    assert len(values['tmin']) == len(values['tmax']) == 12
    results[place]=dict(station=station,latitude=lat,longitude=lon,baseline='1991-2020',resolution='800m',
                        interpolation=False,request=params,**values)
    print(place,'July high',values['tmax'][6],flush=True)
(HERE/'prism_normals.json').write_text(json.dumps(results,indent=2)+'\n')
