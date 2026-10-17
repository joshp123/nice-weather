# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import datetime as dt,json,pathlib,sys,ast,zoneinfo,numpy as np
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent));import pipeline,validate
source=ast.parse((pathlib.Path(__file__).parent/'check_daylight.py').read_text());altitude_node=next(n for n in source.body if isinstance(n,ast.FunctionDef) and n.name=='altitude');exec(compile(ast.Module(body=[altitude_node],type_ignores=[]),'<independent-solar>','exec'))
root=validate.DEFAULT_DATA;points=np.fromfile(root/'v6/points.bin',dtype=pipeline.POINT_DTYPE,offset=4);selected=np.where(points['cell']==11096)[0];epoch,dates=pipeline.chunk_dates(82);dates=[d for d in dates if d.month==11];rows=[];zone=zoneinfo.ZoneInfo('Asia/Kolkata');cache={}
for pi in selected:
 p=points[pi];r,c=divmod(int(p['fine']),1440);nc=(c+720)%1440;tile=f'r{r//12:02}c{nc//12:03}.npz';lat=90-r/4;lon=c/4-180
 if tile not in cache:
  with np.load(root/'era5/chunk82'/tile) as a:cache[tile]=a['fdir'].astype(float)
 radiation=cache[tile][:,r%12,nc%12]
 for date in dates:
  for hour in [16,17,18]:
   start=dt.datetime.combine(date,dt.time(hour),zone).astimezone(dt.timezone.utc);u=(start-epoch).total_seconds()/3600;lo=int(np.floor(u));fraction=u-lo;direct=(1-fraction)*radiation[lo+1]+fraction*radiation[lo+2]
   moments=[start+dt.timedelta(seconds=30+60*i) for i in range(60)];cosine=np.maximum(0,np.sin(np.deg2rad(altitude(moments,lat,lon))));current=direct/cosine.mean() if cosine.mean()>.001 else 0
   pipeline_cosine=0
   for node,weight in [(.1127016654,5/18),(.5,8/18),(.8872983346,5/18)]:
    t=start+dt.timedelta(hours=node);cos,_,_=pipeline.solar(t.timetuple().tm_yday,t.hour+t.minute/60+t.second/3600,lat,lon);pipeline_cosine+=weight*max(0,float(cos))
   exact_pipeline=direct/pipeline_cosine if pipeline_cosine>.001 else 0
   native=[];corrected=0
   for base in [lo,lo+1]:
    begin=epoch+dt.timedelta(hours=base);times=[begin+dt.timedelta(seconds=30+60*i) for i in range(60)];cos=np.maximum(0,np.sin(np.deg2rad(altitude(times,lat,lon))));amplitude=radiation[base+1]/cos.mean() if cos.mean()>.001 else 0
    overlap=np.array([start<=t<start+dt.timedelta(hours=1) for t in times]);lit=int(((cos>0)&overlap).sum());corrected+=amplitude*lit/60
    native.append({'utc_start':begin.isoformat(),'fdir':float(radiation[base+1]),'mean_positive_cosine':float(cos.mean()),'lit_minutes':int((cos>0).sum()),'overlap_lit_minutes':lit,'inferred_sunlit_dni':float(amplitude)})
   rows.append({'fine':int(p['fine']),'latitude':lat,'longitude':lon,'date':str(date),'local_hour':hour,'utc_start':start.isoformat(),'interpolated_horizontal':float(direct),'local_mean_positive_cosine':float(cosine.mean()),'local_lit_minutes':int((cosine>0).sum()),'actual_pipeline_dni':float(exact_pipeline),'actual_pipeline_cosine':float(pipeline_cosine),'pipeline_style_dni':float(current),'native_conserving_hourmean_dni':float(corrected),'native':native})
summary={str(h):{'actual_pipeline_mean':float(np.mean([r['actual_pipeline_dni'] for r in rows if r['local_hour']==h])),'actual_pipeline_max':float(max(r['actual_pipeline_dni'] for r in rows if r['local_hour']==h)),'current_mean':float(np.mean([r['pipeline_style_dni'] for r in rows if r['local_hour']==h])),'conserving_mean':float(np.mean([r['native_conserving_hourmean_dni'] for r in rows if r['local_hour']==h])),'current_max':float(max(r['pipeline_style_dni'] for r in rows if r['local_hour']==h))} for h in [16,17,18]}
report={'written_by':'AI, GPT-6.1 Sol, 2026.10.17','method':'Independent Meeus solar geometry at minute midpoints; piecewise constant sunlit normal irradiance per native UTC bin conserving each fdir accumulation, then integrate its overlap with the local clock hour including zero after geometric sunset. This is a reconstruction assumption, not exact subhour cloud truth.','summary':summary,'examples':sorted([r for r in rows if r['local_hour']==17],key=lambda r:-r['pipeline_style_dni'])[:4],'rows':rows}
pathlib.Path('/tmp/nwf-agents/overnight/climate/RADIATION-AUDIT.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps({k:v for k,v in report.items() if k!='rows'},indent=2))
