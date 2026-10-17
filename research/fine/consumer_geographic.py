"""Prepare bounded real SF viewport weather and published fine tiles for replay.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Private benchmark inputs only; no application or worldwide carrier generation.
"""
import argparse
import gzip
import json
import math
import pathlib
import sys
import time

import numpy as np

from consumer_benchmark import compact,correct
from native_normals import file_sha256,local_hour_indices,sample_local
sys.path.insert(0,str(pathlib.Path(__file__).resolve().parents[1]/'climate-v6'))
import pipeline
from static import POINT_DTYPE

CHUNKS=[76,78,80,82,84]
ZONE='America/Los_Angeles'


def viewport(width,height,zoom):
    longitude,latitude=-122.4194,37.7749
    world=512*2**zoom
    x=(longitude+180)/360
    y=(1-math.asinh(math.tan(math.radians(latitude)))/math.pi)/2
    west,east=(x-width/2/world)*360-180,(x+width/2/world)*360-180
    north=math.degrees(math.atan(math.sinh(math.pi*(1-2*(y-height/2/world)))))
    south=math.degrees(math.atan(math.sinh(math.pi*(1-2*(y+height/2/world)))))
    r0,r1=math.floor((90-north)*120),math.floor((90-south)*120)
    c0,c1=math.floor((west+180)*120),math.floor((east+180)*120)
    return dict(view_width=width,view_height=height,zoom=zoom,world_css_pixels=world,
        center=[longitude,latitude],west=west,east=east,north=north,south=south,
        tile_row_first=r0//256,tile_row_last=r1//256,tile_column_first=c0//256,tile_column_last=c1//256)


def prepare(root,output,zoom):
    """Use process_tile with one private accumulator per quarter point."""
    views=[viewport(w,h,zoom) for w,h in [(390,844),(3840,1080)]]
    r0=min(v['tile_row_first'] for v in views)*256;r1=(max(v['tile_row_last'] for v in views)+1)*256
    c0=min(v['tile_column_first'] for v in views)*256;c1=(max(v['tile_column_last'] for v in views)+1)*256
    qr0,qr1=math.floor((r0+.5)/30+.5),math.floor((r1-.5)/30+.5)
    qc0,qc1=math.floor((c0+.5)/30+.5),math.floor((c1-.5)/30+.5)
    fine=[];missing=[]
    for row in range(qr0,qr1+1):
        for column in range(qc0,qc1+1):
            name=f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz'
            (fine if all((root/f'era5/chunk{k}'/name).exists() for k in CHUNKS) else missing).append(row*1440+column)
    points=np.zeros(len(fine),dtype=POINT_DTYPE)
    points['fine']=fine;points['source']=fine;points['cell']=np.arange(len(fine))
    private=output/'private-weather';(private/'v6').mkdir(parents=True,exist_ok=True)
    (private/'climate-v6-work').mkdir(exist_ok=True)
    with (private/'v6/points.bin').open('wb') as stream:
        np.array([len(points)],dtype='<u4').tofile(stream);points.tofile(stream)
    np.save(private/'climate-v6-work/timezones.npy',np.full(len(points),ZONE))
    configuration=dict(chunks=CHUNKS,zone=ZONE,views=views,quarter_rows=[qr0,qr1],quarter_columns=[qc0,qc1],
        fine=points['fine'].tolist(),missing_full_raw_quarter_ids=missing,
        private_accumulators='one per quarter point; not public climate cells',
        source_root=str(root),model='60-byte carrier; own native weather; native1degree cell quantile dates')
    config_path=output/'geographic-configuration.json'
    if config_path.exists() and json.loads(config_path.read_text())!=configuration:raise ValueError('Geographic cache uses different inputs; use another output folder')
    config_path.write_text(json.dumps(configuration,indent=2)+'\n')
    weather_path=output/'geographic-weather.npz'
    if weather_path.exists():return configuration
    all_features=[];all_air=[];all_dew=[];all_high=[];all_low=[];all_dates=[];cell_days={}
    cells=np.fromfile(root/'v6/cells.bin',dtype='<u2').reshape(-1,2)
    ordinal={int(r)*360+int(c):i for i,(r,c) in enumerate(cells)}
    names=sorted({f'r{int(p)//1440//12:02d}c{((int(p)%1440+720)%1440)//12:03d}.npz' for p in points['fine']})
    for chunk in CHUNKS:
        (private/f'climate-v6-work/chunk{chunk}').mkdir(exist_ok=True)
        _,dates=pipeline.chunk_dates(chunk)
        features=np.zeros((len(points),len(dates),29),np.float32)
        air=np.zeros((len(points),len(dates),12),np.float32);dew=np.zeros_like(air)
        high=np.zeros((len(points),len(dates)),np.float32);low=np.zeros_like(high)
        _,indices,fraction=local_hour_indices(chunk,ZONE,range(24))
        for name in names:
            pipeline.process_tile((str(private),chunk,root/f'era5/chunk{chunk}'/name))
            with np.load(private/f'climate-v6-work/chunk{chunk}'/name) as saved:
                ids=saved['point_ids'];features[ids]=saved['sums']/saved['counts'][:,None,None]
            rows=(points['fine'][ids]//1440)%12;columns=((points['fine'][ids]%1440+720)%1440)%12
            with np.load(root/f'era5/chunk{chunk}'/name) as raw:
                t=raw['t2m'][:,rows,columns].astype(np.float32);td=raw['d2m'][:,rows,columns].astype(np.float32)
            native24=sample_local(t,indices,fraction,np.arange(len(ids)))
            dew24=sample_local(td,indices,fraction,np.arange(len(ids)))
            air[ids]=native24[:,10:22].transpose(2,0,1);dew[ids]=dew24[:,10:22].transpose(2,0,1)
            high[ids]=native24.max(1).T;low[ids]=native24.min(1).T
            native_path=root/f'climate-v6-work/chunk{chunk}'/name
            if native_path.exists():
                with np.load(native_path) as saved:
                    for cell,value,count in zip(saved['cells'],saved['sums'],saved['counts']):
                        cell_days.setdefault(int(cell),[]).append(value/count)
        all_features.append(features);all_air.append(air);all_dew.append(dew);all_high.append(high);all_low.append(low);all_dates.extend(dates)
        print('geographic native weather',chunk,len(points),flush=True)
    data=dict(features=np.concatenate(all_features,axis=1),air=np.concatenate(all_air,axis=1),dew=np.concatenate(all_dew,axis=1),
        high=np.concatenate(all_high,axis=1),low=np.concatenate(all_low,axis=1),dates=np.array(all_dates,dtype='datetime64[D]'))
    months=np.array([date.month for date in all_dates]);chosen=np.full((len(points),12,8),-1,np.int32);date_cells={}
    point_positions={int(f):i for i,f in enumerate(points['fine'])}
    for index,fine_id in enumerate(points['fine']):
        row,column=divmod(int(fine_id),1440);cell_key=min(row//4,179)*360+column//4
        if cell_key not in date_cells:
            cell=ordinal.get(cell_key)
            if cell in cell_days:
                reference=np.concatenate(cell_days[cell])
                if len(reference)!=len(months):raise ValueError('Native cell selection did not cover every retained date')
                selection_source='native assembled cell'
            else:
                center=(row//4*4+2)*1440+(column//4*4+2)
                position=point_positions.get(center)
                if position is None:continue
                reference=data['features'][position];selection_source='own geometric cell centre'
            scores=pipeline.score_features(pipeline.decode_records(pipeline.encode_records(reference)))
            selection=[]
            for month in range(1,13):
                available=np.flatnonzero(months==month);order=np.argsort(scores[available],kind='stable')
                selection.append(available[order[np.floor((np.arange(8)+.5)*len(order)/8).astype(int)]])
            date_cells[cell_key]=dict(indices=np.stack(selection),source=selection_source)
        chosen[index]=date_cells[cell_key]['indices']
    data['chosen']=chosen
    np.savez_compressed(weather_path,**data)
    (output/'geographic-date-cells.json').write_text(json.dumps({str(k):dict(source=v['source'],date_ids=v['indices'].tolist()) for k,v in date_cells.items()},indent=2)+'\n')
    return configuration


def assemble_inputs(configuration,output,tiles_root,reference_root=None):
    from tiles import decode_tile
    generation=json.loads((tiles_root/'configuration.json').read_text())
    if generation['chunks']!=CHUNKS:raise ValueError('B generation chunk set differs')
    with np.load(output/'geographic-weather.npz') as saved:data={k:saved[k] for k in saved.files}
    fine=np.array(configuration['fine']);position={int(f):i for i,f in enumerate(fine)}
    zone_lookup={};reference_identity={}
    if reference_root:
        for key,path in [('reference_points_sha256',reference_root/'v6/points.bin'),('reference_timezones_sha256',reference_root/'climate-v6-work/timezones.npy')]:
            reference_identity[key]=file_sha256(path)
            if reference_identity[key]!=generation[key]:raise ValueError('Zone audit reference identity differs from the correction generation')
        reference_points=np.fromfile(reference_root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
        reference_zones=np.load(reference_root/'climate-v6-work/timezones.npy',mmap_mode='r')
        selected=np.flatnonzero(np.isin(reference_points['fine'],fine))
        zone_lookup={int(reference_points['fine'][i]):str(reference_zones[i]) for i in selected}
    point_records=[]
    for i in range(len(fine)):
        records=compact(data['features'][i],data['air'][i],data['dew'][i],data['high'][i],data['low'][i],60)[0]
        point_records.append(records[data['chosen'][i]])
    point_records=np.stack(point_records)
    qr0,qr1=configuration['quarter_rows'];qc0,qc1=configuration['quarter_columns'];nx=qc1-qc0+1;ny=qr1-qr0+1
    outcomes=[]
    for viewport in configuration['views']:
        prefix=f"sf-{viewport['view_width']}-{viewport['view_height']}"
        r0,r1=viewport['tile_row_first']*256,(viewport['tile_row_last']+1)*256
        c0,c1=viewport['tile_column_first']*256,(viewport['tile_column_last']+1)*256
        rows,columns=np.indices((r1-r0,c1-c0));refs=(np.floor((rows+r0+.5)/30+.5).astype(int)*1440+np.floor((columns+c0+.5)/30+.5).astype(int))
        offsets=np.full((12,r1-r0,c1-c0,2),-32768,dtype='<i2');support=np.zeros((12,r1-r0,c1-c0),np.uint8)
        tile_payloads=[];active_tiles=set()
        for month in range(1,13):
            for tr in range(viewport['tile_row_first'],viewport['tile_row_last']+1):
                for tc in range(viewport['tile_column_first'],viewport['tile_column_last']+1):
                    path=tiles_root/f'tiles/{month:02d}/{tr:03d}/{tc:03d}.nwf.gz'
                    if not path.exists():continue
                    tile=decode_tile(path)
                    if tile['generation']!=generation['generation']:raise ValueError('Mixed B tile generations')
                    rr,cc=tr*256-r0,tc*256-c0
                    offsets[month-1,rr:rr+256,cc:cc+256]=np.rint(tile['offsets_c']*10).astype('<i2')
                    support[month-1,rr:rr+256,cc:cc+256]=tile['support']
                    tile_payloads.append(dict(month=month,tile_row=tr,tile_column=tc,path=str(path),bytes=path.stat().st_size));active_tiles.add((tr,tc))
        qr0,qr1=math.floor((r0+.5)/30+.5),math.floor((r1-.5)/30+.5)
        qc0,qc1=math.floor((c0+.5)/30+.5),math.floor((c1-.5)/30+.5)
        nx,ny=qc1-qc0+1,qr1-qr0+1
        required=np.unique(refs[np.any(np.all(offsets!=-32768,axis=-1),axis=0)])
        available=np.array([int(f) in position and np.all(data['chosen'][position[int(f)]]>=0) for f in required]);missing=required[~available]
        zone_audit=dict(target_zone=ZONE,reference_root=str(reference_root) if reference_root else None,
            checked=len(required) if reference_root else 0,
            mismatched_reference_zones=[int(f) for f in required if reference_root and zone_lookup.get(int(f))!=ZONE])
        carrier=np.zeros((12,ny*8,nx*60),np.uint8);availability=np.zeros((ny,nx),np.uint8)
        for f in required[available]:
            index=position[int(f)];r,c=divmod(int(f),1440);r-=qr0;c-=qc0
            availability[r,c]=1
            for month in range(12):carrier[month,r*8:r*8+8,c*60:c*60+60]=point_records[index,month]
        def write(name,values):
            path=output/f'{prefix}-{name}.bin.gz';payload=gzip.compress(values.tobytes(),mtime=0);path.write_bytes(payload)
            return dict(path=str(path),compressed_bytes=len(payload),decoded_bytes=values.nbytes)
        fixtures=[];fixture_points=set()
        for r,c in np.argwhere(np.all(offsets!=-32768,axis=(0,3))):
            latitude=90-(r+r0+.5)/120;longitude=-180+(c+c0+.5)/120
            if not(viewport['south']<=latitude<=viewport['north'] and viewport['west']<=longitude<=viewport['east']):continue
            f=int(refs[r,c]);index=position.get(f)
            if f in fixture_points:continue
            fixture_points.add(f)
            if index is None or np.any(data['chosen'][index]<0):continue
            chosen=data['chosen'][index].ravel()
            _,features,air,dew,high,low,_=compact(data['features'][index],data['air'][index],data['dew'][index],data['high'][index],data['low'][index],60)
            h=np.repeat(offsets[:,r,c,0]/10,8);l=np.repeat(offsets[:,r,c,1]/10,8)
            corrected=correct(features[chosen],air[chosen],dew[chosen],high[chosen],low[chosen],h,l)[0];corrected[:,:11]-=.75
            fixtures.append(dict(pixel=[int(c),int(r)],native_fine=f,score=float(pipeline.score_features(corrected).mean())))
            if len(fixtures)==13:break
        outcomes.append(dict(**viewport,prefix=prefix,width=c1-c0,height=r1-r0,fine_origin=[r0,c0],quarter_origin=[qr0,qc0],
            nx=nx,ny=ny,tile_slots=(r1-r0)*(c1-c0)//65536,active_tiles=[list(v) for v in sorted(active_tiles)],
            distinct_required_native_points=len(required),available_native_points=int(available.sum()),missing_native_points=missing.tolist(),
            reference_zone_audit=zone_audit,
            supported_pixels_by_month=np.all(offsets!=-32768,axis=-1).sum((1,2)).tolist(),
            files={name:write(name,values) for name,values in [('carrier',carrier),('availability',availability),('fine',offsets),('support',support)]},
            actual_tile_transfers=tile_payloads,cpu_card_pixels=fixtures))
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',generation=generation['generation'],correction_tile_root=str(tiles_root),cases=outcomes,
        score_aggregation='Arithmetic mean of graded daily scores: eight equally weighted days per month; year is the equally weighted mean of twelve months. Reference research/taste/nice_v6.py through climate-v6/pipeline.py; no threshold-count aggregation.',
        scope='Actual SF geographic views and distinct native-quarter histories; full-period card facts excluded; this Mac, not physical phone hardware.')
    (output/'geographic-inputs.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report),flush=True)
    if reference_root:
        (output/'geographic-zone-audit.json').write_text(json.dumps(dict(generation=generation['generation'],**reference_identity,cases=[dict(view_width=case['view_width'],**case['reference_zone_audit']) for case in outcomes]),indent=2)+'\n')


def audit_land_coverage(configuration,output,land_root):
    complete=json.loads((land_root/'complete.json').read_text())
    land=np.memmap(land_root/'land.u8',dtype=np.uint8,mode='r',shape=(21600,43200))
    with np.load(output/'geographic-weather.npz') as weather:
        ready={int(f) for f,chosen in zip(configuration['fine'],weather['chosen']) if np.all(chosen>=0)}
    cases=[]
    for view in configuration['views']:
        active=[];required=set();land_pixels=0
        for row in range(view['tile_row_first'],view['tile_row_last']+1):
            for column in range(view['tile_column_first'],view['tile_column_last']+1):
                mask=land[row*256:(row+1)*256,column*256:(column+1)*256]>0
                if not mask.any():continue
                active.append([row,column]);land_pixels+=int(mask.sum())
                rr,cc=np.nonzero(mask)
                refs=np.floor((rr+row*256+.5)/30+.5).astype(int)*1440+np.floor((cc+column*256+.5)/30+.5).astype(int)
                required.update(int(v) for v in np.unique(refs))
        cases.append(dict(view_width=view['view_width'],view_height=view['view_height'],zoom=view['zoom'],active_land_tiles=active,
            full_tile_land_pixels=land_pixels,distinct_native_quarters=len(required),available_native_quarters=len(required&ready),
            missing_native_quarters=sorted(required-ready)))
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',land_root=str(land_root),land_completion=complete,cases=cases,
        scope='Full visible-tile land support; correction eligibility may differ. Carrier availability requires all five chunks and a valid native-cell date selection.')
    (output/'geographic-land-coverage.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(dict(land_coverage=cases)),flush=True)


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--native-root',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True);parser.add_argument('--zoom',type=int,default=9)
    parser.add_argument('--tiles',type=pathlib.Path)
    parser.add_argument('--land-support',type=pathlib.Path)
    parser.add_argument('--reference-root',type=pathlib.Path,help='Audit original reference zones against this bounded SF target-zone carrier')
    args=parser.parse_args();args.output.mkdir(parents=True,exist_ok=True);started=time.monotonic()
    configuration=prepare(args.native_root,args.output,args.zoom)
    if args.land_support:audit_land_coverage(configuration,args.output,args.land_support)
    if args.tiles:assemble_inputs(configuration,args.output,args.tiles,args.reference_root)
    print(json.dumps(dict(seconds=time.monotonic()-started,quarter_points=len(configuration['fine']),views=configuration['views'])),flush=True)


if __name__=='__main__':main()
