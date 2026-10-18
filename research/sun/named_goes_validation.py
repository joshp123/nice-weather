# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Independent civil-hour integration of exact-coordinate GOES validation years."""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
from zoneinfo import ZoneInfo

import numpy as np
from era5_measurement import pipeline

ZONES={'peru/lima':'America/Lima','united-states/san-francisco':'America/Los_Angeles','united-states/san-diego':'America/Los_Angeles'}

def measure(place, root, years):
    results=[]; inputs=[]
    for year in years:
        raw=root/f'disc{year}'/f"{round(place['latitude']*1e5)},{round(place['longitude']*1e5)}.npz"
        data=np.load(raw)
        if not np.allclose(data['requested'],[place['latitude'],place['longitude']],rtol=0,atol=1e-8):
            raise ValueError(f'Wrong requested coordinates: {raw}')
        timestamps=np.array([dt.datetime.fromisoformat(str(stamp)).timestamp() for stamp in data['time']])
        values=data['dni'].astype(float); zone=ZoneInfo(ZONES[place['slug']])
        dates=[];date=dt.date(year,1,1)
        while date.year==year:
            dates.append(date);date+=dt.timedelta(days=1)
        hours=np.array([[dt.datetime.combine(date,dt.time(hour),zone).timestamp() for hour in range(10,22)] for date in dates])
        hourly=np.zeros(hours.shape)
        for minute in range(60):
            hourly+=np.interp(hours+60*minute+30,timestamps,values)/60
        valid=(hours[:,0]+30>=timestamps[0])&(hours[:,-1]+3570<=timestamps[-1])
        _,equation,declination=pipeline.solar(np.array([date.timetuple().tm_yday for date in dates]),12,place['latitude'],place['longitude'])
        latitude=np.deg2rad(place['latitude'])
        argument=np.cos(np.deg2rad(90.833))/(np.cos(latitude)*np.cos(declination))-np.tan(latitude)*np.tan(declination)
        sunset_utc=(720-4*place['longitude']-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
        offsets=np.array([dt.datetime.combine(date,dt.time(12),zone).utcoffset().total_seconds()/3600 for date in dates])
        sunset_local=sunset_utc+offsets-24*np.rint((offsets-place['longitude']/15)/24)
        daylight=np.clip(np.ceil(sunset_local)-10,1,11).astype(int)
        sunny=((hourly>=300)&(np.arange(12)[None,:]<daylight[:,None])).sum(1)/daylight
        months=np.array([date.month for date in dates])
        for month in range(1,13):
            selected=valid&(months==month)
            results.append({'year':year,'month':month,'days':int(selected.sum()),'sunnyShare':float(sunny[selected].mean()),'hourlyDni':hourly[selected].mean(0).tolist()})
        inputs.append({'year':year,'path':str(raw),'sha256':hashlib.sha256(raw.read_bytes()).hexdigest(),'requested':data['requested'].tolist(),'selected':data['selected'].tolist()})
    monthly=[]
    for month in range(1,13):
        rows=[row for row in results if row['month']==month];days=sum(row['days'] for row in rows)
        monthly.append({'month':month,'days':days,'sunnyShare':sum(row['days']*row['sunnyShare'] for row in rows)/days,'hourlyDni':(sum(row['days']*np.array(row['hourlyDni']) for row in rows)/days).tolist()})
    return {'place':place,'years':years,'radiationDatabase':'NSRDB GOES PSM v4 full disc','inputs':inputs,'monthly':monthly,'annualMonthly':results}

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--places',type=Path,required=True);parser.add_argument('--root',type=Path,required=True);parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--years',type=int,nargs='+',default=[2022,2024]);args=parser.parse_args()
    places=[place for place in json.loads(args.places.read_text()) if place['slug'] in ZONES]
    result={'writtenBy':'AI, GPT-6.1 Sol, 2026-10-18','method':'Instantaneous10-minuteDNI; independentnp.interp60minute-midpointcivil-hourmeans; localIANAoffsets; exactnamedcoordinates, nearestactualsatellitepixel; incompleteboundarydaysremoved.','caveat':'Twoobservedyearschecktypical-yearstability, notlong-termretrievaltruth. TheGOES4kmtypical-yearpixeland2kmfulldiscpixelcanresolvefoggradientsdifferently.','places':[measure(place,args.root,args.years) for place in places]}
    args.output.write_text(json.dumps(result,indent=2)+'\n')
