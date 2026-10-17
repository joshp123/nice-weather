# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
# Run with existing numpy/icechunk/zarr/numcodecs[pcodec] Python environment.
# python calibrate_dni.py --fetch; python calibrate_dni.py
# Cached radiation patches and results are on the external SSD; no credentials needed.
import sys, pathlib, time, json, concurrent.futures, gzip, argparse, os, subprocess
import numpy as np, icechunk, zarr
import datetime as dt
from zoneinfo import ZoneInfo
HERE=pathlib.Path(__file__).resolve().parent
REPO=HERE.parents[2]
parser=argparse.ArgumentParser(description='Calibrate SSRD to DNI against held-out ERA5 tiles')
parser.add_argument('--data-dir',type=pathlib.Path,default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/forecast-calibration'))
parser.add_argument('--output',type=pathlib.Path,default=HERE/'results.json')
parser.add_argument('--fetch',action='store_true')
parser.add_argument('--verify-go',action='store_true')
parser.add_argument('--go',default='go')
args=parser.parse_args()
ROOT=args.data_dir;ROOT.mkdir(parents=True,exist_ok=True)
HOURS=8736;CHUNK=82
PLACES=[('Malaga',36.75, -4.5,'train'),('Rotterdam',52,4.5,'train'),('Singapore',1.25,103.75,'train'),('Denver',39.75,-105,'train'),('CapeTown',-34,18.5,'train'),('Marrakech',31.75,-8,'train'),('London',51.5,0,'train'),('Sydney',-33.75,151.25,'train'),('SanDiego',32.75,-117.25,'holdout'),('Chania',35.5,24,'holdout'),('Bangkok',13.75,100.5,'holdout'),('Lima',-12,-77,'holdout'),('Tokyo',35.75,139.75,'holdout'),('Oslo',60,10.75,'holdout'),('Nairobi',-1.25,36.75,'holdout'),('Vancouver',49.25,-123,'holdout')]
def archive():
 s=icechunk.s3_storage(bucket='earthmover-icechunk-era5',prefix='icechunkV2',region='us-east-1',anonymous=True)
 return zarr.open_group(icechunk.Repository.open(s).readonly_session(snapshot_id='TZ2KX6A24CJ5DG17T7MG').store,mode='r',path='single/temporal')
def fetch(p):
 name,lat,lon,split=p;path=ROOT/(name+'.npz')
 if path.exists():return
 g=archive();r=round((90-lat)*4);c=round((lon%360)*4)%1440;rr=r//12*12;cc=c//12*12
 # Fetch archive-native complete tiles, then retain a 3x3 patch inside each tile.
 ix=np.array([max(rr,min(rr+11,r+i)) for i in [-1,0,1]]);jx=np.array([max(cc,min(cc+11,c+i)) for i in [-1,0,1]])
 data={}
 for field in ['ssrd','fdir']:
  a=g[field][CHUNK*HOURS:(CHUNK+1)*HOURS,rr:rr+12,cc:cc+12]/3600
  data[field]=a[:,ix-rr][:,:,jx-cc].astype(np.float32)
 data['lat']=90-ix/4;data['lon']=jx/4;data['height']=g['z_sfc'][rr:rr+12,cc:cc+12][ix-rr][:,jx-cc]/9.80665
 np.savez_compressed(path,**data);print('fetched',name,flush=True)
if args.fetch:
 with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:list(pool.map(fetch,PLACES))
 sys.exit()
linke=np.frombuffer(gzip.open(HERE.parent/'linke-quarter-degree.bin.gz','rb').read(),dtype=np.uint8).reshape(721,1440,12)/20
start=np.datetime64('1940-01-01T00')+np.timedelta64(CHUNK*HOURS,'h');times=start+np.arange(HOURS).astype('timedelta64[h]')
year=times.astype('datetime64[Y]'); doy=(times.astype('datetime64[D]')-year).astype(float)+1;hour=(times-times.astype('datetime64[D]')).astype('timedelta64[h]').astype(float)
month=(times.astype('datetime64[M]')-year).astype(int);mdays=np.array([31,28,31,30,31,30,31,31,30,31,30,31]);middle=np.cumsum(mdays)-mdays/2
# Match Go daily Linke interpolation, including the 2022 month cycle (this chunk has no leap day).
def radiation(lat,lon,height):
 lat=lat.reshape(1,-1);lon=lon.reshape(1,-1);height=height.reshape(1,-1);lt=linke[np.round((90-lat)*4).astype(int),np.round(lon*4).astype(int)%1440].reshape(-1,12)
 turb=np.stack([np.interp(doy,np.r_[middle[-1]-365,middle,middle[0]+365],np.r_[a[-1],a,a[0]]) for a in lt],axis=1)
 pressure=100*((44331.514-height)/11880.516)**(1/.1902632)/101325
 gh=np.zeros((HOURS,lat.size));dn=gh.copy();cosmean=gh.copy()
 for frac in [-.125,-.375,-.625,-.875]:
  hh=hour[:,None]+frac;dd=doy[:,None];gamma=2*np.pi/365*(dd-1+(hh-12)/24)
  eq=229.18*(.000075+.001868*np.cos(gamma)-.032077*np.sin(gamma)-.014615*np.cos(2*gamma)-.040849*np.sin(2*gamma))
  dec=.006918-.399912*np.cos(gamma)+.070257*np.sin(gamma)-.006758*np.cos(2*gamma)+.000907*np.sin(2*gamma)-.002697*np.cos(3*gamma)+.00148*np.sin(3*gamma)
  angle=np.deg2rad((hh*60+eq+4*lon)/4-180);lr=np.deg2rad(lat);mu=np.maximum(0,np.sin(lr)*np.sin(dec)+np.cos(lr)*np.cos(dec)*np.cos(angle));cosmean+=mu/4
  elev=np.rad2deg(np.arcsin(mu));am=pressure/(mu+.50572*(6.07995+elev)**-1.6364)
  fh=np.exp(-height/8000);sh=np.exp(-height/1250);ang=2*np.pi/365*(dd-1);extra=1366.1*(1.00011+.034221*np.cos(ang)+.00128*np.sin(ang)+.000719*np.cos(2*ang)+.000077*np.sin(2*ang))
  globalrad=(5.09e-5*height+.868)*extra*mu*np.exp(-(3.92e-5*height+.0387)*am*(fh+sh*(turb-1)))
  beam1=extra*(.664+.163/fh)*np.exp(-.09*am*(turb-1));beam2=np.divide(globalrad*(1-(.1-.2*np.exp(-turb))/(.1+.882/fh)),mu,out=np.zeros_like(mu),where=mu>0)
  gh+=globalrad/4;dn+=np.where(mu>0,np.minimum(beam1,beam2),0)/4
 return gh,dn,cosmean
if args.verify_go:
 inputs=[];expected=[]
 for name,_,_,_ in PLACES:
  d=np.load(ROOT/(name+'.npz'));lats,lons=np.meshgrid(d['lat'],d['lon'],indexing='ij');ghi,normal,_=radiation(lats,lons,np.zeros_like(d['height']))
  for day in [0,20,95,173,265,363]:
   for h in [0,1,6,10,12,14,18,21,23]:
    i=day*24+h
    for j in range(9):
     lon=float(lons.flat[j]);lon=lon if lon<=180 else lon-360
     inputs.append(dict(latitude=float(lats.flat[j]),longitude=lon,ending=str(times[i])+':00:00Z'))
     expected.append([float(ghi[i,j]),float(normal[i,j]),float(normal[i,j]*((.7-.05)/.95)**2.1)])
 process=subprocess.run([args.go,'run','./internal/clearsky/calibration'],input=json.dumps(inputs),text=True,capture_output=True,cwd=REPO,check=True)
 actual=np.array([[r['global'],r['normal'],r['dni']] for r in json.loads(process.stdout)])
 error=np.abs(actual-np.array(expected));midnight=np.array([r['ending'][11:13]=='00' for r in inputs])
 parity={'samples':len(inputs),'units':'W/m2','columns':['clearGHI','clearDNI','estimatedDNI at 0.7 clearness'],'maxAbsoluteError':error.max(axis=0).tolist(),'meanAbsoluteError':error.mean(axis=0).tolist(),'nonMidnightMaxAbsoluteError':error[~midnight].max(axis=0).tolist(),'midnightMaxAbsoluteError':error[midnight].max(axis=0).tolist(),'note':'Go correctly dates extraterrestrial irradiance to previous UTC day at midnight; original Python calibration uses hour-ending day for that factor. NOAA angle remains continuous. Differences are retained rather than hidden.'}
 args.output.write_text(json.dumps(parity,indent=2)+'\n');print(json.dumps(parity,indent=2));sys.exit()

def daily_counts(values,dayids):
 return np.stack([np.bincount(dayids,weights=values[:,i])[1:-1] for i in range(9)],axis=1)
ZONES=['Europe/Madrid','Europe/Amsterdam','Asia/Singapore','America/Denver','Africa/Johannesburg','Africa/Casablanca','Europe/London','Australia/Sydney','America/Los_Angeles','Europe/Athens','Asia/Bangkok','America/Lima','Asia/Tokyo','Europe/Oslo','Africa/Nairobi','America/Vancouver']
samples=[]
for name,lat,lon,split in PLACES:
 d=np.load(ROOT/(name+'.npz'));lats,lons=np.meshgrid(d['lat'],d['lon'],indexing='ij');ghi,normal,cos= radiation(lats,lons,np.zeros_like(d['height']));ssrd=d['ssrd'].reshape(HOURS,-1);fdir=d['fdir'].reshape(HOURS,-1)
 actual=np.divide(fdir,cos,out=np.zeros_like(fdir),where=cos>.001);local=[dt.datetime.fromisoformat(str(t)).replace(tzinfo=dt.timezone.utc).astimezone(ZoneInfo(ZONES[[p[0] for p in PLACES].index(name)]))-dt.timedelta(hours=1) for t in times];clock=np.array([t.hour for t in local])[:,None];dayids=np.array([t.date().toordinal() for t in local]);dayids-=dayids[0];mask=(clock>=10)&(clock<21)&(cos>.001)
 for step in [1,3,6]:
  # Hour-ending archive rows, UTC intervals (0,3], (3,6] ... or (0,6] ... .
  indices=((np.arange(HOURS)-1)//step)*step+1;indices=np.maximum(0,indices);ratio=np.empty_like(ghi)
  for j in np.unique(indices):
   sl=slice(j,min(j+step,HOURS));ratio[sl]=ssrd[sl].sum(axis=0)/np.maximum(.001,ghi[sl].sum(axis=0))
  samples.append(dict(name=name,split=split,step=step,ratio=ratio,normal=normal,actual=actual,mask=mask,dayids=dayids))
 print('computed',name,flush=True)
# Fit on train tiles, point/day counts; search floor and exponent, optimize count RMSE plus a modest DNI error term.
best=None
for floor in np.arange(.0,.61,.05):
 for power in np.arange(.5,2.51,.1):
  loss=[]
  for s in samples:
   if s['split']!='train' or s['step']!=3:continue
   pred=s['ratio']>=(floor+(1-floor)*np.power(300/np.maximum(.001,s['normal']),1/power))
   err=daily_counts(((pred)-(s['actual']>=300).astype(int))*s['mask'],s['dayids'])
   loss.append(np.mean(err**2))
  score=np.mean(loss)
  if best is None or score<best[0]:best=(float(score),float(floor),float(power))
print('BEST',best,flush=True);floor,power=best[1:];rows=[]
for s in samples:
 pred=s['normal']*np.clip((s['ratio']-floor)/(1-floor),0,1)**power;mask=s['mask'];error=daily_counts(((pred>=300)-(s['actual']>=300).astype(int))*mask,s['dayids'])
 rows.append(dict(name=s['name'],split=s['split'],intervalHours=s['step'],days=int(error.size),countBiasHours=float(error.mean()),countMAEHours=float(np.abs(error).mean()),countRMSEHours=float(np.sqrt(np.mean(error**2))),hourClassificationAccuracy=float(((pred>=300)==(s['actual']>=300))[mask].mean()),dniMAE=float(np.abs(pred-s['actual'])[mask].mean())))
report={'regions':PLACES,'clearSkyAltitudeM':0,'ssrdUnits':'hour-mean W/m2 (archive J/m2 divided by 3600)','fdirReference':'hourly direct horizontal divided by 4-midpoint integrated positive solar cosine','sourceSnapshot':'TZ2KX6A24CJ5DG17T7MG','chunk':CHUNK,'start':str(times[0]),'end':str(times[-1]),'fit':'Grid search minimizes training point-day sunny-count RMSE, 3-hour intervals; spatially disjoint held-out archive tiles. Nine fine points per tile. IANA local daytime 10–21, local calendar-day groups; incomplete boundary days excluded. Clear-sky model uses zero altitude as in deployed forecast.','mapping':{'floor':floor,'power':power},'results':rows}
(ROOT/'results.json').write_text(json.dumps(report,indent=2));args.output.write_text(json.dumps(report,indent=2));print(json.dumps(report),flush=True)
