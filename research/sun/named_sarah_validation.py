# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Independent 19-year SARAH-3 validation at the exact named-place coordinates."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
from urllib.parse import urlencode
from zoneinfo import ZoneInfo
import numpy as np
from pvgis import pipeline, LICENCE

ZONES={'namibia/swakopmund':'Africa/Windhoek','spain/arona':'Atlantic/Canary','spain/malaga':'Europe/Madrid','spain/seville':'Europe/Madrid','south-africa/cape-town':'Africa/Johannesburg','the-netherlands/rotterdam':'Europe/Amsterdam'}

def measure(place, output):
    slug=place['slug']; latitude=place['latitude']; longitude=place['longitude']
    params=dict(lat=latitude,lon=longitude,startyear=2005,endyear=2023,raddatabase='PVGIS-SARAH3',pvcalculation=0,components=1,outputformat='json',angle=0,usehorizon=0)
    url='https://re.jrc.ec.europa.eu/api/v5_3/seriescalc?'+urlencode(params)
    raw=output/(slug.replace('/','-')+'-2005-2023.json')
    if not raw.exists():
        subprocess.run(['curl','--fail','--location','--silent','--show-error','--retry','2','--connect-timeout','20','--max-time','300',url,'-o',str(raw)],check=True)
    payload=json.loads(raw.read_text())
    if payload['inputs']['meteo_data']['radiation_db']!='PVGIS-SARAH3':
        raise RuntimeError('PVGIS returned a different radiation database')
    records=payload['outputs']['hourly']
    timestamp=np.array([dt.datetime.strptime(row['time'],'%Y%m%d:%H%M').replace(tzinfo=dt.timezone.utc).timestamp() for row in records])
    elevation=np.array([row['H_sun'] for row in records]); direct=np.array([row['Gb(i)'] for row in records])
    dni=np.divide(direct,np.sin(np.deg2rad(elevation)),out=np.zeros(len(records)),where=elevation>0)
    dates=[]; date=dt.date(2005,1,1)
    while date.year<=2023:
        dates.append(date); date+=dt.timedelta(days=1)
    zone=ZoneInfo(ZONES[slug])
    hours=np.array([[dt.datetime.combine(date,dt.time(hour),zone).timestamp() for hour in range(10,22)] for date in dates])
    hourly=np.zeros(hours.shape)
    for minute in range(60):
        hourly+=np.interp(hours+60*minute+30,timestamp,dni)/60
    _,equation,declination=pipeline.solar(np.array([date.timetuple().tm_yday for date in dates]),12,latitude,longitude)
    latitude_radians=np.deg2rad(latitude)
    argument=np.cos(np.deg2rad(90.833))/(np.cos(latitude_radians)*np.cos(declination))-np.tan(latitude_radians)*np.tan(declination)
    sunset_utc=(720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
    offsets=np.array([dt.datetime.combine(date,dt.time(12),zone).utcoffset().total_seconds()/3600 for date in dates])
    sunset_local=sunset_utc+offsets-24*np.rint((offsets-longitude/15)/24)
    daylight=np.clip(np.ceil(sunset_local)-10,1,11).astype(int)
    mask=np.arange(12)[None,:]<daylight[:,None]
    valid=(hours[:,0]>=timestamp[0])&(hours[:,-1]+3600<=timestamp[-1])
    sunny=((hourly>=300)&mask).sum(1)/daylight
    months=np.array([date.month for date in dates]); years=np.array([date.year for date in dates])
    result={'place':place,'years':[2005,2023],'radiationDatabase':'PVGIS-SARAH3','url':url,'sha256':hashlib.sha256(raw.read_bytes()).hexdigest(),'licenceSource':LICENCE,'method':'Gb(i)/sin(H_sun); linear DNI interpolation; minute-midpoint hour means; civil local time10:00 through sunset; DNI>=300W/m2; ERA5-compatible integer daylight denominator. Excludes incomplete boundary days.','monthly':[],'annualMonthly':[]}
    for month in range(1,13):
        select=valid&(months==month)
        result['monthly'].append({'month':month,'days':int(select.sum()),'sunnyShare':float(sunny[select].mean()),'pooledSunnyShare':float((sunny[select]*daylight[select]).sum()/daylight[select].sum()),'hourlyDni':hourly[select].mean(0).tolist()})
        for year in range(2005,2024):
            select=valid&(months==month)&(years==year)
            result['annualMonthly'].append({'year':year,'month':month,'days':int(select.sum()),'sunnyShare':float(sunny[select].mean()),'hourlyDni':hourly[select].mean(0).tolist()})
    (output/(slug.replace('/','-')+'-validation.json')).write_text(json.dumps(result,indent=2)+'\n')
    print(slug+' '+', '.join(str(round(row['sunnyShare']*100,1)) for row in result['monthly']),flush=True)
    return result

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--places',type=Path,required=True)
    parser.add_argument('--out',type=Path,required=True)
    args=parser.parse_args(); args.out.mkdir(parents=True,exist_ok=True)
    places=[place for place in json.loads(args.places.read_text()) if place['slug'] in ZONES]
    with ThreadPoolExecutor(max_workers=3) as pool:
        rows=list(pool.map(lambda place:measure(place,args.out),places))
    (args.out/'summary.json').write_text(json.dumps({'writtenBy':'AI,GPT-6.1Sol,2026-10-18','places':rows},indent=2)+'\n')
