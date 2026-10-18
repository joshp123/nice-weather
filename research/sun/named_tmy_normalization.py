# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Experiment: normalize a selected satellite year to its full-period DNI profile."""
import argparse
import csv
import datetime as dt
import gzip
import hashlib
import json
from pathlib import Path
from zoneinfo import ZoneInfo
import numpy as np
from named_satellite import solar_sunset
from named_sarah_validation import ZONES

def normalize(control, root):
    place=control['place']; lat=place['latitude']; lon=place['longitude']
    key=f'{round(lat*1e5)},{round(lon*1e5)}'; prefix=hashlib.sha256(key.encode()).hexdigest()[:2]
    directory=root/'pvgis'/prefix
    lines=gzip.decompress((directory/(key+'.csv.gz')).read_bytes()).decode().splitlines()
    metadata=json.loads((directory/(key+'.metadata.json')).read_text())
    header=next(index for index,line in enumerate(lines) if line.startswith('time(UTC),'))
    records=list(csv.DictReader(lines[header:header+8761]))
    years={row['month']:row['year'] for row in metadata['outputs']['months_selected']}
    source_offset=metadata['inputs']['location']['irradiance_time_offset']*3600
    timestamps=np.array([dt.datetime.strptime('2001'+row['time(UTC)'][4:],'%Y%m%d:%H%M').replace(tzinfo=dt.timezone.utc).timestamp() for row in records])+source_offset
    dni=np.array([float(row['Gb(n)']) for row in records])
    timestamps=np.concatenate((timestamps[-24:]-365*86400,timestamps,timestamps[:24]+365*86400))
    dni=np.concatenate((dni[-24:],dni,dni[:24])); zone=ZoneInfo(ZONES[place['slug']])
    days=[[] for _ in range(12)]; daylight=[[] for _ in range(12)]
    date=dt.date(2001,1,1)
    while date.year==2001:
        actual=date.replace(year=years[date.month]); offset=dt.datetime.combine(actual,dt.time(12),zone).utcoffset().total_seconds()/3600
        midnight=dt.datetime.combine(date,dt.time(),dt.timezone.utc).timestamp()
        samples=midnight+(np.arange(10,22)[:,None]-offset)*3600+np.arange(60)*60+30
        days[date.month-1].append(np.interp(samples,timestamps,dni).mean(axis=1))
        daylight[date.month-1].append(solar_sunset(actual,lat,lon,offset));date+=dt.timedelta(days=1)
    monthly=[]
    for month in range(12):
        hourly=np.array(days[month]); target=np.array(control['monthly'][month]['hourlyDni']); mean=hourly.mean(axis=0)
        scale=np.divide(target,mean,out=np.ones(12),where=mean>0)
        corrected=hourly*scale
        denominator=np.array(daylight[month]); mask=np.arange(12)[None,:]<denominator[:,None]
        sunny=((corrected>=300)&mask).sum(axis=1)/denominator
        monthly.append({'sunnyShare':float(sunny.mean()),'hourlyDni':corrected.mean(axis=0).tolist(),'daylightHours':float(denominator.mean()),'sourceDays':len(hourly),'selectedYear':years[month+1],'normalizationScale':scale.tolist()})
    return {'latitude':lat,'longitude':lon,'months':monthly,'source':'PVGIS-SARAH3 TMY normalized to2005–2023monthlyhourlyDNI','method':'One month/hour multiplicative DNI scale from full-period profile divided by TMY profile; same factor to each TMY day; recompute DNI>=300hourlysunfraction. This experiment uses full19yearprofiles, not compact DRcalc profiles.'}

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--normal',type=Path,required=True);parser.add_argument('--root',type=Path,required=True);parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args(); normal=json.loads(args.normal.read_text())
    rows=[normalize(control,args.root) for control in normal['places']]
    args.output.write_text(json.dumps({'records':rows},indent=2)+'\n')
    print(args.output)
