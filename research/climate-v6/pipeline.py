# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import argparse, datetime as dt, importlib.util, io, json, os, pathlib, shlex, subprocess, tempfile, time, zoneinfo
import numpy as np
from static import ROOT, POINT_DTYPE, build_static
spec=importlib.util.spec_from_file_location('taste',pathlib.Path(__file__).parents[1]/'taste/nice_v6.py')
taste=importlib.util.module_from_spec(spec); spec.loader.exec_module(taste)
HOURS=8736

def score_features(a):
    dl=np.rint(a[...,27]); mask=np.arange(11)<dl[...,None]
    thermal=(np.sum(np.minimum(taste.COLD_GRADE(a[...,:11]),taste.HEAT_GRADE(a[...,11:22]))*mask,axis=-1)+2*np.minimum(taste.EVENING_GRADE(a[...,22]),taste.HEAT_GRADE(a[...,22])))/(dl+2)
    return thermal*taste.HUMIDITY_FACTOR(a[...,23])*taste.RAIN_FACTOR(a[...,24])*taste.WIND_FACTOR(a[...,25])*taste.SUN_FACTOR(a[...,26]/dl)

def encode_records(a):
    out=np.zeros(a.shape[:-1]+(15,),dtype=np.uint8)
    out[...,:10]=np.clip(np.rint((a[...,[0,2,4,6,8,10,13,16,19,22]]+30)*3),0,255)
    out[...,10]=np.clip(np.rint((a[...,23]+30)*3),0,255)
    out[...,11]=np.clip(np.rint(np.sqrt(np.maximum(0,a[...,24]))*50),0,255)
    out[...,12]=np.clip(np.rint(a[...,25]*2),0,255)
    out[...,13:15]=np.clip(np.rint(a[...,26:28]),0,11)
    return out

def decode_records(records,offset=0):
    a=np.zeros(records.shape[:-1]+(29,),dtype=np.float32)
    temperatures=records[...,:10].astype(float)/3-30+offset
    for hour in range(11):
        left=hour//2; right=min(left+1,5); fraction=(hour%2)/2
        a[...,hour]=temperatures[...,left]*(1-fraction)+temperatures[...,right]*fraction
    for hour in range(12):
        x=np.clip((hour-2)/3,0,3); left=int(x); right=min(left+1,3)
        a[...,11+hour]=temperatures[...,6+left]*(1-(x-left))+temperatures[...,6+right]*(x-left)
    a[...,23]=records[...,10]/3-30; a[...,24]=(records[...,11]/50)**2
    a[...,25]=records[...,12]/2; a[...,26:28]=records[...,13:15]
    return a

def solar(day_numbers,utc_hour,latitude,longitude):
    gamma=2*np.pi/365*(day_numbers-1+(utc_hour-12)/24)
    equation=229.18*(.000075+.001868*np.cos(gamma)-.032077*np.sin(gamma)-.014615*np.cos(2*gamma)-.040849*np.sin(2*gamma))
    declination=.006918-.399912*np.cos(gamma)+.070257*np.sin(gamma)-.006758*np.cos(2*gamma)+.000907*np.sin(2*gamma)-.002697*np.cos(3*gamma)+.00148*np.sin(3*gamma)
    lat=np.deg2rad(latitude)
    angle=np.deg2rad((utc_hour*60+equation+4*longitude)/4-180)
    cosine=np.sin(lat)*np.sin(declination)+np.cos(lat)*np.cos(declination)*np.cos(angle)
    return cosine,equation,declination

def timezone_map(root,points):
    path=root/'climate-v6-work/timezones.npy'
    if path.exists(): return np.load(path)
    from timezonefinder import TimezoneFinder
    finder=TimezoneFinder(in_memory=True)
    names=[]
    for i,p in enumerate(points):
        r,c=divmod(int(p['fine']),1440)
        names.append(finder.timezone_at(lat=90-r*.25,lng=-180+c*.25))
        if i%25000==0: print('timezones',i,flush=True)
    result=np.array(names); np.save(path,result); return result

def chunk_dates(chunk):
    epoch=dt.datetime(1940,1,1,tzinfo=dt.timezone.utc)+dt.timedelta(hours=chunk*HOURS)
    dates=np.array([epoch.date()+dt.timedelta(days=i) for i in range(2,362)])
    return epoch,dates

def process_tile(args):
    root,chunk,path,*remote=args; started=time.monotonic(); root=pathlib.Path(root)
    points=np.fromfile(root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
    zones=np.load(root/'climate-v6-work/timezones.npy')
    tile_row=int(path.stem[1:3]); tile_column=int(path.stem[4:7])
    native_row=points['fine']//1440; native_column=(points['fine']%1440+720)%1440
    selected=np.where((native_row//12==tile_row)&(native_column//12==tile_column))[0]
    if not len(selected): return str(path),0
    p=points[selected]; latitude=90-(p['fine']//1440)*.25; longitude=-180+(p['fine']%1440)*.25
    rr=native_row[selected]%12; cc=native_column[selected]%12
    sr=(p['source']//1440)%12; sc=((p['source']%1440+720)%1440)%12
    source = io.BytesIO(subprocess.check_output(['/usr/bin/ssh', '-o', 'BatchMode=yes', remote[0],
        f'cat {shlex.quote(str(path))}'])) if remote else path
    with np.load(source) as data:
        t=data['t2m'][:,sr,sc].astype(np.float32)+p['shift']/100
        dew=data['d2m'][:,sr,sc].astype(np.float32)+p['shift']/100
        wind=np.hypot(data['u10'][:,rr,cc].astype(np.float32),data['v10'][:,rr,cc].astype(np.float32))*3.6
        rain=data['tp'][:,rr,cc].astype(np.float32); radiation=data['fdir'][:,rr,cc].astype(np.float32)
    epoch,dates=chunk_dates(chunk); D=len(dates); P=len(p)
    values=np.zeros((P,D,29),np.float32)
    day_numbers=np.array([d.timetuple().tm_yday for d in dates])[:,None,None]
    for zone_name in np.unique(zones[selected]):
        ids=np.where(zones[selected]==zone_name)[0]; zone=zoneinfo.ZoneInfo(str(zone_name))
        utc=np.array([[(dt.datetime.combine(d,dt.time(h),zone).astimezone(dt.timezone.utc)-epoch).total_seconds()/3600 for h in range(10,22)] for d in dates])
        low=np.floor(utc).astype(int); frac=utc-low
        def sample(field,accumulation=False):
            base=low+(1 if accumulation else 0)
            return field[base[:,:,None],ids]*(1-frac[:,:,None])+field[base[:,:,None]+1,ids]*frac[:,:,None]
        T=sample(t); Td=sample(dew); W=sample(wind); R=sample(rain,True); direct=sample(radiation,True)
        utc_clock=utc[:,:,None]%24
        cosine=np.zeros_like(direct)
        for fraction in (.1127016654,.5,.8872983346):
            weight=5/18 if fraction!=.5 else 8/18
            utc_dates=np.datetime64(epoch.date())+np.floor((utc[:,:,None]+fraction)/24).astype("timedelta64[D]")
            utc_day_numbers=(utc_dates-utc_dates.astype("datetime64[Y]")).astype(int)+1
            cos,_,_=solar(utc_day_numbers,(utc_clock+fraction)%24,latitude[ids],longitude[ids]); cosine+=weight*np.maximum(0,cos)
        dni=np.divide(direct,cosine,out=np.zeros_like(direct),where=cosine>.001)
        sun,shade,Td=taste.felt(T,Td,W,dni)
        _,equation,declination=solar(day_numbers[:,0,:],12,latitude[ids],longitude[ids])
        lat=np.deg2rad(latitude[ids]); argument=(np.cos(np.deg2rad(90.833))/(np.cos(lat)*np.cos(declination))-np.tan(lat)*np.tan(declination))
        sunset_utc=(720-4*longitude[ids]-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
        offsets=np.array([dt.datetime.combine(d,dt.time(12),zone).utcoffset().total_seconds()/3600 for d in dates])[:,None]
        local_date_wrap=24*np.rint((offsets-longitude[ids]/15)/24)
        sunset_local=sunset_utc+offsets-local_date_wrap
        daylight=np.clip(np.ceil(sunset_local)-10,1,11)
        daylight=np.where(argument>=1,1,np.where(argument<=-1,11,daylight))
        mask=np.arange(11)[None,:,None]<daylight[:,None,:]
        v=values[ids]
        v[:,:,:11]=sun[:,:11,:].transpose(2,0,1); v[:,:,11:23]=shade.transpose(2,0,1)
        v[:,:,23]=Td.mean(1).T; v[:,:,24]=R[:,:11].sum(1).T; v[:,:,25]=W.mean(1).T
        v[:,:,26]=((dni[:,:11]>=300)&mask).sum(1).T; v[:,:,27]=daylight.T; v[:,:,28]=T[:,:11].max(1).T
        values[ids]=v
    cells,inverse=np.unique(p['cell'],return_inverse=True)
    sums=np.zeros((len(cells),D,29),np.float32); np.add.at(sums,inverse,values)
    counts=np.bincount(inverse)
    months=np.array([d.month for d in dates])
    monthly=np.zeros((P,12,8),np.float32)
    for month in range(1,13):
        v=values[:,months==month]; dl=v[:,:,27]; mask=np.arange(11)<dl[:,:,None]
        monthly[:,month-1,0]=v[:,:,28].sum(1); monthly[:,month-1,1]=v[:,:,22].sum(1)
        monthly[:,month-1,2]=v[:,:,23].sum(1); monthly[:,month-1,3]=(v[:,:,24]>=1).sum(1)
        monthly[:,month-1,4]=np.sum(v[:,:,26],axis=1); monthly[:,month-1,5]=v[:,:,25].sum(1)
        monthly[:,month-1,6]=np.sum((v[:,:,:11]+v[:,:,11:22])*.5*mask,axis=(1,2))
        monthly[:,month-1,7]=dl.sum(1)
    output=root/f'climate-v6-work/chunk{chunk}'; output.mkdir(exist_ok=True)
    temporary=output/(path.stem+'.partial.npz')
    np.savez_compressed(temporary,point_ids=selected,cells=cells,sums=sums,counts=counts,monthly=monthly,daylight=values[:,:,27].astype(np.uint8),seconds=time.monotonic()-started)
    temporary.replace(output/path.name)
    return str(path),time.monotonic()-started

def assemble(root, publish=True):
    from PIL import Image
    started=time.monotonic(); output=root/'v6'
    snapshots=root/'climate-v6-work/assemblies'; snapshots.mkdir(parents=True,exist_ok=True)
    snapshot=pathlib.Path(tempfile.mkdtemp(prefix=dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%S-'),dir=snapshots))
    climate=snapshot/'v6/climate'; climate.mkdir(parents=True)
    for name in ('cells.bin','points.bin','READY-static'): os.link(output/name,snapshot/'v6'/name)
    points=np.fromfile(output/'points.bin',dtype=POINT_DTYPE,offset=4)
    N=(output/'cells.bin').stat().st_size//4; P=len(points)
    work=root/'climate-v6-work'; selected_chunks=[]; all_records=[]; all_features=[]
    point_monthly=np.zeros((P,12,8),np.float64); point_cell_sum=np.zeros((P,12),np.float64)
    month_days=np.zeros((P,12),dtype=int); coverage=[]; cell_covered=np.zeros(N,dtype=bool)
    expected_counts=np.bincount(points["cell"],minlength=N)
    expected=set()
    native_rows=points['fine']//1440; native_columns=(points['fine']%1440+720)%1440
    for r,c in zip(native_rows//12,native_columns//12): expected.add(f'r{r:02d}c{c:03d}.npz')
    present=set().union(*[{p.name for p in folder.glob('r*.npz') if '.partial' not in p.name} for folder in work.glob('chunk*')])
    if not expected<=present: raise ValueError(f'Waiting for {len(expected-present)} remaining land tiles')
    for folder in sorted(work.glob('chunk*')):
        available={p.name for p in folder.glob('r*.npz') if '.partial' not in p.name}
        coverage.append({'chunk':int(folder.name[5:]),'tiles':len(available & expected),'required':len(expected)})
        if not expected<=available: continue
        chunk=int(folder.name[5:]); _,dates=chunk_dates(chunk); D=len(dates)
        sums=np.zeros((N,D,29),np.float32); counts=np.zeros(N,np.int32)
        for name in sorted(expected & available):
            with np.load(folder/name) as a:
                sums[a['cells']]+=a['sums']; counts[a['cells']]+=a['counts']
        valid=counts==expected_counts; cell_covered|=valid
        features=sums/np.maximum(counts,1)[:,None,None].astype(np.float32); del sums
        features[~valid,:,27]=1
        records=encode_records(features); months=np.array([d.month for d in dates])
        selected_chunks.append(chunk); all_records.append(records); all_features.append((features,months,valid))
        for month in range(1,13): month_days[:,month-1]+=valid[points["cell"]]*np.sum(months==month)
        for name in sorted(expected & available):
            with np.load(folder/name) as a:
                keep=valid[points['cell'][a['point_ids']]]
                ids=a['point_ids'][keep]; cells=points['cell'][ids]; dl=a['daylight'][keep]
                point_monthly[ids]+=a['monthly'][keep]
                for month in range(1,13):
                    # Prefix sums make each point's daylight mask cheap, even across zone boundaries.
                    v=features[cells][:,months==month]
                    prefix=np.cumsum((v[:,:,:11]+v[:,:,11:22])*.5,axis=2)
                    cutoff=dl[:,months==month].astype(int)-1
                    point_cell_sum[ids,month-1]+=np.take_along_axis(prefix,cutoff[:,:,None],axis=2)[:,:,0].sum(1)
        print('assembled chunk',chunk,flush=True)
    if not np.all(cell_covered): raise ValueError(f'{int((~cell_covered).sum())} cells still lack a complete tile sample; coverage={coverage}')
    proof=[]
    for month in range(1,13):
        records=np.concatenate([r[:,m==month] for r,(_,m,_) in zip(all_records,all_features)],axis=1)
        full=np.concatenate([score_features(f[:,m==month]) for f,m,_ in all_features],axis=1)
        compact=score_features(decode_records(records))
        valid_days=np.concatenate([np.repeat(valid[:,None],np.sum(m==month),axis=1) for _,m,valid in all_features],axis=1)
        count=valid_days.sum(1)
        order=np.argsort(np.where(valid_days,compact,np.inf),axis=1,kind='stable')
        ranks=np.floor((np.arange(8)[None,:]+.5)*count[:,None]/8).astype(int)
        indices=np.take_along_axis(order,ranks,axis=1)
        selected=np.take_along_axis(records,indices[:,:,None],axis=1)
        selected.tofile(climate/f'month-{month:02d}.bin')
        full_mean=(full*valid_days).sum(1)/count
        compact_mean=(compact*valid_days).sum(1)/count
        error=np.abs(full_mean-compact_mean)
        sample_error=np.abs(full_mean-score_features(decode_records(selected)).mean(1))
        proof.append({'month':month,'mean_error':float(error.mean()),'p95_error':float(np.quantile(error,.95)),'p99_error':float(np.quantile(error,.99)),'max_error':float(error.max()),'above_3':int((error>3).sum()),'sample_max_error':float(sample_error.max())})
        offsets=(point_monthly[:,month-1,6]-point_cell_sum[:,month-1])/point_monthly[:,month-1,7]
        image=np.full(721*1440,255,np.uint8)
        image[points['fine']]=np.clip(np.rint(offsets*10)+128,0,254)
        Image.fromarray(image.reshape(721,1440)).save(climate/f'offsets-{month:02d}.png')
    facts=np.zeros((721*1440,12,6),np.uint8)
    values=point_monthly[:,:,:6]/month_days[:,:,None]
    values[:,:,4]=point_monthly[:,:,4]/point_monthly[:,:,7]
    encoded=np.zeros((P,12,6),np.uint8)
    encoded[:,:,:3]=np.clip(np.rint((values[:,:,:3]+30)*3),0,255)
    encoded[:,:,3:5]=np.clip(np.rint(values[:,:,3:5]*100),0,100)
    encoded[:,:,5]=np.clip(np.rint(values[:,:,5]*2),0,255)
    facts[points['fine']]=encoded; facts.tofile(climate/'facts.bin')
    years=sorted({d.year for chunk in selected_chunks for d in chunk_dates(chunk)[1]})
    metadata={'years':years,'chunks':selected_chunks,'buildTime':dt.datetime.now(dt.timezone.utc).isoformat(),'source':'ERA5 hourly Earthmover icechunk; fdir normalized to DNI using integrated solar cosine','N':N,'P':P,'daysPerMonthMinimum':month_days.min(0).tolist(),'daysPerMonthMaximum':month_days.max(0).tolist(),'assemblySeconds':time.monotonic()-started,'buildId':snapshot.name,'compactProof':proof,'incompleteChunks':coverage}
    metadata.update(chunkYears=len(selected_chunks), sampleDaysPerMonth=8,
                    firstScoredDay=str(min(chunk_dates(chunk)[1][0] for chunk in selected_chunks)),
                    lastScoredDay=str(max(chunk_dates(chunk)[1][-1] for chunk in selected_chunks)))
    (climate/'meta.json').write_text(json.dumps(metadata,indent=2)+'\n')
    (snapshot/'v6/READY-climate').write_text(json.dumps({'chunks':selected_chunks,'buildTime':metadata['buildTime'],'N':N})+'\n')
    from report import validate_snapshot, write_report
    validation=validate_snapshot(root,snapshot)
    if publish:
        from publish_local import publish_climate_snapshot
        publish_climate_snapshot(root,snapshot)
        write_report(root,validation=validation)
    print(json.dumps(metadata),flush=True)
    return snapshot

def process_available(root,workers):
    started=time.monotonic()
    import multiprocessing as mp
    work=root/'climate-v6-work'; work.mkdir(exist_ok=True)
    points=np.fromfile(root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
    timezone_map(root,points)
    jobs=[]
    native_rows=points['fine']//1440; native_columns=(points['fine']%1440+720)%1440
    expected={f'r{r:02d}c{c:03d}.npz' for r,c in zip(native_rows//12,native_columns//12)}
    for folder in sorted((root/'era5').glob('chunk*')):
        chunk=int(folder.name[5:]); target=work/folder.name; target.mkdir(exist_ok=True)
        for path in sorted(folder.glob('r*.npz')):
            if path.name in expected and not (target/path.name).exists(): jobs.append((str(root),chunk,path))
    print('pending tiles',len(jobs),flush=True)
    with mp.Pool(workers) as pool:
        for i,(path,seconds) in enumerate(pool.imap_unordered(process_tile,jobs)):
            if i%25==0: print('processed',i+1,'/',len(jobs),path,round(seconds,2),flush=True)
    with open(work/"timings.jsonl","a") as timing_file:
        timing_file.write(json.dumps({"tiles":len(jobs),"seconds":time.monotonic()-started,"completed":dt.datetime.now(dt.timezone.utc).isoformat()})+"\n")
    return len(jobs)

def main():
    parser=argparse.ArgumentParser(); parser.add_argument('command',choices=['static','process','assemble','run','watch'])
    parser.add_argument('--root',type=pathlib.Path,default=ROOT); parser.add_argument('--workers',type=int,default=4)
    args=parser.parse_args(); root=args.root
    if args.command=='static': build_static(root); return
    if args.command=='assemble': assemble(root); return
    if args.command in ('process','run'):
        process_available(root,args.workers)
        if args.command=='run': assemble(root)
        return
    while True:
        process_available(root,args.workers)
        try:
            points=np.fromfile(root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
            expected={f'r{r:02d}c{c:03d}.npz' for r,c in zip(points['fine']//1440//12,((points['fine']%1440+720)%1440)//12)}
            folders=list((root/'climate-v6-work').glob('chunk*'))
            signature=sorted(int(f.name[5:]) for f in folders if expected<={p.name for p in f.glob('r*.npz')})
            if signature and signature!=getattr(main,'signature',None):
                assemble(root); main.signature=signature
        except ValueError as error: print(error,flush=True)
        time.sleep(45)

if __name__=='__main__': main()
