# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Independently replay instantaneous GOES typical-year samples at named clocks."""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
from zoneinfo import ZoneInfo
import numpy as np
from era5_measurement import pipeline

ZONES={'peru/lima':'America/Lima','peru/chiclayo':'America/Lima','peru/chimbote':'America/Lima','united-states/san-francisco':'America/Los_Angeles','united-states/san-diego':'America/Los_Angeles'}

def verify(source,root,places,targets):
    locations=json.loads((root/f'{source}-selected.json').read_text());years=np.load(root/f'{source}-selected-years.npy',mmap_mode='r')
    targets={(round(row['latitude']*1e5),round(row['longitude']*1e5)):row for row in targets['records']};checks=[]
    for place in places:
        if place['slug'] not in ZONES:continue
        coordinate=(round(place['latitude']*1e5),round(place['longitude']*1e5));key=','.join(map(str,coordinate));raw=root/source/(key+'.npz');data=np.load(raw)
        selected=years[:,locations[key][0]].tolist();zone=ZoneInfo(ZONES[place['slug']])
        timestamps=np.array([dt.datetime.fromisoformat(str(stamp)).replace(year=2001,tzinfo=dt.timezone.utc).timestamp() for stamp in data['time']]);dni=data['dni'].astype(float)
        timestamps=np.r_[timestamps[-24:]-365*86400,timestamps,timestamps[:24]+365*86400];dni=np.r_[dni[-24:],dni,dni[:24]]
        dates=[];date=dt.date(2001,1,1)
        while date.year==2001:dates.append(date);date+=dt.timedelta(days=1)
        offsets=np.array([dt.datetime(selected[date.month-1],date.month,date.day,12,tzinfo=zone).utcoffset().total_seconds()/3600 for date in dates])
        midnight=np.array([dt.datetime.combine(date,dt.time(),dt.timezone.utc).timestamp() for date in dates])
        hour_starts=midnight[:,None]+(np.arange(10,22)[None,:]-offsets[:,None])*3600
        hourly=sum(np.interp(hour_starts+minute*60+30,timestamps,dni) for minute in range(60))/60
        observed_days=np.array([dt.date(selected[date.month-1],date.month,date.day).timetuple().tm_yday for date in dates])
        _,equation,declination=pipeline.solar(observed_days,12,place['latitude'],place['longitude']);latitude=np.deg2rad(place['latitude'])
        argument=np.cos(np.deg2rad(90.833))/(np.cos(latitude)*np.cos(declination))-np.tan(latitude)*np.tan(declination)
        sunset_utc=(720-4*place['longitude']-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
        sunset_local=sunset_utc+offsets-24*np.rint((offsets-place['longitude']/15)/24)
        daylight=np.clip(np.ceil(sunset_local)-10,1,11).astype(int);sunny=((hourly>=300)&(np.arange(12)[None,:]<daylight[:,None])).sum(1)/daylight
        months=np.array([date.month for date in dates]);sun_error=[];dni_error=[]
        for month in range(1,13):
            mask=months==month;target=targets[coordinate]['months'][month-1]
            sun_error.append(abs(float(sunny[mask].mean())-target['sunnyShare']));dni_error.append(float(np.max(np.abs(hourly[mask].mean(0)-target['hourlyDni']))))
        check={'place':place['slug'],'source':source,'selectedYears':selected,'selectedPixel':data['selected'].tolist(),'sha256':hashlib.sha256(raw.read_bytes()).hexdigest(),'maxSunnyShareDifference':max(sun_error),'maxHourlyDniDifference':max(dni_error)}
        check['passes']=check['maxSunnyShareDifference']<1e-12 and check['maxHourlyDniDifference']<1e-8;checks.append(check)
    return checks

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--places',type=Path,required=True);parser.add_argument('--root',type=Path,required=True);parser.add_argument('--output',type=Path,required=True);parser.add_argument('--tmy',type=Path,required=True);parser.add_argument('--tdy',type=Path,required=True);args=parser.parse_args()
    places=json.loads(args.places.read_text());checks=[]
    for source in ['tmy','tdy']:checks+=verify(source,args.root,places,json.loads(getattr(args,source).read_text()))
    args.output.write_text(json.dumps({'writtenBy':'AI, GPT-6.1 Sol, 2026-10-18','passes':all(row['passes'] for row in checks),'method':'Independentnp.interp60minute-midpointcivil-hourintegration; cyclicnonleaptypicalcalendar; eachactualselectedyearIANAoffset; independentlycomputedsolarsunsetmask.','checks':checks},indent=2)+'\n')
    if not all(row['passes'] for row in checks):raise ValueError('Production typical-year integration differs from independent replay')
