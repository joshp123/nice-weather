# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import datetime as dt,zoneinfo,json,pathlib,sys,numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent));import pipeline,validate
root=validate.DEFAULT_DATA;cells,points,_=validate.validate_static(root);zones=np.load(root/'climate-v6-work/timezones.npy');latitudes=90-points['fine']//1440/4;longitudes=points['fine']%1440/4-180
# Independent Julian-century NOAA/Meeus position, rather than pipeline's day-of-year approximation.
def altitude(utc,lat,lon):
 jd=np.array([x.timestamp()/86400+2440587.5 for x in utc]);t=(jd-2451545)/36525
 rad=np.deg2rad;l=(280.46646+t*(36000.76983+t*.0003032))%360;m=357.52911+t*(35999.05029-.0001537*t);e=.016708634-t*(.000042037+.0000001267*t)
 c=np.sin(rad(m))*(1.914602-t*(.004817+.000014*t))+np.sin(rad(2*m))*(.019993-.000101*t)+np.sin(rad(3*m))*.000289
 omega=125.04-1934.136*t;lam=l+c-.00569-.00478*np.sin(rad(omega));obl=23+(26+(21.448-t*(46.815+t*(.00059-.001813*t)))/60)/60+.00256*np.cos(rad(omega))
 dec=np.arcsin(np.sin(rad(obl))*np.sin(rad(lam)));y=np.tan(rad(obl)/2)**2
 eq=4*np.rad2deg(y*np.sin(2*rad(l))-2*e*np.sin(rad(m))+4*e*y*np.sin(rad(m))*np.cos(2*rad(l))-.5*y*y*np.sin(4*rad(l))-1.25*e*e*np.sin(2*rad(m)))
 utc_minutes=np.array([x.hour*60+x.minute+x.second/60 for x in utc]);ha=rad((utc_minutes+eq+4*lon)%1440/4-180)
 return np.rad2deg(np.arcsin(np.sin(rad(lat))*np.sin(dec)+np.cos(rad(lat))*np.cos(dec)*np.cos(ha)))
def reference(day,lat,lon,zone):
 base=dt.datetime.combine(day,dt.time(),zone);hours=np.arange(0,36*60+1)/60
 times=[(base+dt.timedelta(minutes=int(m))).astimezone(dt.timezone.utc) for m in range(len(hours))];elevation=altitude(times,lat,lon)
 noon=int(np.argmax(np.where((hours>=8)&(hours<=18),elevation,-1000)))
 if elevation[noon]<-.833:return 1,None,'sun below horizon at solar noon'
 falling=np.where((elevation[noon:-1]>=-.833)&(elevation[noon+1:]<-.833))[0]
 if not len(falling):return 11,None,'sun remains above horizon through sample'
 i=noon+int(falling[0]);fraction=(-.833-elevation[i])/(elevation[i+1]-elevation[i]);sunset=hours[i]+fraction/60
 return int(np.clip(np.ceil(sunset)-10,1,11)),float(sunset),'sunset'
def actual(day,lat,lon,zone):
 _,eq,dec=pipeline.solar(day.timetuple().tm_yday,12,lat,lon);arg=np.cos(np.deg2rad(90.833))/(np.cos(np.deg2rad(lat))*np.cos(dec))-np.tan(np.deg2rad(lat))*np.tan(dec)
 utc=(720-4*lon-eq+4*np.rad2deg(np.arccos(np.clip(arg,-1,1))))/60;offset=dt.datetime.combine(day,dt.time(12),zone).utcoffset().total_seconds()/3600
 sunset=utc+offset-24*np.rint((offset-lon/15)/24);dl=np.clip(np.ceil(sunset)-10,1,11);dl=np.where(arg>=1,1,np.where(arg<=-1,11,dl))
 return int(dl),float(sunset),float(arg),float(offset)
targets={'Apia':(-13.83,-171.75),'Attu':(52.9,173.2),'Tongatapu':(-21.2,-175.15),'Chatham':(-43.95,-176.55),'Adak':(51.9,-176.6),'Anadyr':(64.7,177.5),'Nome':(64.5,-165.4),'Hammerfest':(70.7,23.7),'Reykjavik':(64.15,-21.9),'Nuuk':(64.18,-51.7),'Fairbanks':(64.84,-147.7),'Utqiagvik':(71.29,-156.8),'Anchorage':(61.2,-149.9),'Eucla':(-31.7,128.9),'Kathmandu':(27.7,85.3),'Málaga':(36.72,-4.42)}
dates=[dt.date(2022,m,d) for m,d in [(3,20),(3,27),(6,21),(7,15),(9,23),(10,30),(12,21)]]
rows=[];saved_checks=[];chunk_dates=pipeline.chunk_dates(82)[1]
for name,(lat,lon) in targets.items():
 ix=int(np.argmin((latitudes-lat)**2+((longitudes-lon)*np.cos(np.deg2rad(lat)))**2));lat=float(latitudes[ix]);lon=float(longitudes[ix]);zone=zoneinfo.ZoneInfo(str(zones[ix]));r,c=divmod(int(points['fine'][ix]),1440);path=root/f'climate-v6-work/chunk82/r{r//12:02}c{((c+720)%1440)//12:03}.npz'
 for day in dates:
  dl,sunset,arg,off=actual(day,lat,lon,zone);ref,rs,reason=reference(day,lat,lon,zone)
  rows.append({'place':name,'latitude':lat,'longitude':lon,'zone':str(zone),'date':str(day),'offset':off,'pipeline_daylight':dl,'reference_daylight':ref,'pipeline_sunset_local_hour':round(sunset,4),'reference_sunset_local_hour':None if rs is None else round(rs,4),'reference_state':reason,'sunset_difference_minutes':None if rs is None or abs(arg)>=1 else round((sunset-rs)*60,2)})
 if path.exists():
  with np.load(path) as a:pi=list(a['point_ids']).index(ix);saved=a['daylight'][pi]
  expected=np.array([actual(d,lat,lon,zone)[0] for d in chunk_dates]);mismatch=np.where(saved!=expected)[0];saved_checks.append({'place':name,'tile':path.name,'days':len(saved),'mismatches':len(mismatch),'first_mismatch':None if not len(mismatch) else str(chunk_dates[mismatch[0]])})
report={'written_by':'AI, GPT-6.1 Sol, 2026.10.17','independent_reference':'Julian-century NOAA/Meeus altitude with minute-resolution descending -0.833deg crossing; retains next-day sunset hour','cases':len(rows),'count_differences':[r for r in rows if r['pipeline_daylight']!=r['reference_daylight']],'maximum_sunset_difference_minutes':max(abs(r['sunset_difference_minutes']) for r in rows if r['sunset_difference_minutes'] is not None),'saved_intermediate_checks':saved_checks,'rows':rows};out=pathlib.Path('/tmp/nwf-agents/overnight/climate/daylight-independent-validation.json');out.write_text(json.dumps(report,indent=2)+'\n');print(json.dumps({k:v for k,v in report.items() if k!='rows'},indent=2));print('dateline and summer cases');print(json.dumps([r for r in rows if (r['place'] in ['Apia','Attu','Tongatapu','Chatham','Adak'] and r['date']=='2022-06-21') or r['reference_sunset_local_hour'] is not None and r['reference_sunset_local_hour']>=24],indent=2))
