# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Read exact-date SARAH DNI for the native replay's retained representative days."""
import datetime as dt
import json
from pathlib import Path
import sys
from zoneinfo import ZoneInfo
import numpy as np

queries=json.load(sys.stdin); result=[]
for query in queries:
    if query.get('goesRoot'):
        zone=ZoneInfo(query['timezone']);sources={};days=[]
        for year in [2022,2024]:
            raw=Path(query['goesRoot'])/f'disc{year}'/f"{round(query['latitude']*1e5)},{round(query['longitude']*1e5)}.npz"
            data=np.load(raw);sources[year]=(np.array([dt.datetime.fromisoformat(str(stamp)).timestamp() for stamp in data['time']]),data['dni'])
        for date in query['dates']:
            date=dt.date.fromisoformat(date)
            if date.year not in sources:days.append(None);continue
            timestamps,dni=sources[date.year];hours=np.array([dt.datetime.combine(date,dt.time(hour),zone).timestamp() for hour in range(10,22)])
            if hours[0]+30<timestamps[0]or hours[-1]+3570>timestamps[-1]:days.append(None);continue
            samples=hours[:,None]+np.arange(60)*60+30;days.append(np.interp(samples,timestamps,dni).mean(axis=1).tolist())
        result.append({'slug':query['slug'],'days':days});continue
    payload=json.loads(Path(query['raw']).read_text());records=payload['outputs']['hourly']
    if payload['inputs']['meteo_data']['radiation_db']!='PVGIS-SARAH3':raise RuntimeError('Not independent satellite radiation')
    timestamps=np.array([dt.datetime.strptime(row['time'],'%Y%m%d:%H%M').replace(tzinfo=dt.timezone.utc).timestamp() for row in records])
    elevation=np.array([row['H_sun'] for row in records]);beam=np.array([row['Gb(i)'] for row in records]);dni=np.divide(beam,np.sin(np.deg2rad(elevation)),out=np.zeros(len(records)),where=elevation>0)
    zone=ZoneInfo(query['timezone']);days=[]
    for date in query['dates']:
        date=dt.date.fromisoformat(date)
        hours=np.array([dt.datetime.combine(date,dt.time(hour),zone).timestamp() for hour in range(10,22)])
        if hours[0]<timestamps[0]or hours[-1]+3600>timestamps[-1]:days.append(None);continue
        samples=hours[:,None]+np.arange(60)*60+30
        days.append(np.interp(samples,timestamps,dni).mean(axis=1).tolist())
    result.append({'slug':query['slug'],'days':days})
json.dump(result,sys.stdout)
