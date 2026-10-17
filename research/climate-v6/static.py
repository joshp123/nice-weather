# Written by AI on 2026.10.17 by model GPT-6.1 Sol; sea-level correction for coastal points by claude opus 5.5.
import pathlib, time
import numpy as np
ROOT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
POINT_DTYPE = np.dtype([('fine','<u4'),('source','<u4'),('shift','<i2'),('cell','<u2')])

def build_static(root=ROOT):
    started = time.monotonic()
    source = root/'era5'; output = root/'v6'; output.mkdir(exist_ok=True)
    lsm = np.load(source/'static_lsm.npy'); heights = np.load(source/'static_z.npy') / 9.80665
    latitude = np.load(source/'latitude.npy'); longitude = np.load(source/'longitude.npy')
    rows, columns = np.indices(lsm.shape)
    cell_rows = np.clip(np.floor(90-latitude).astype(int),0,179)[:,None] + np.zeros(lsm.shape,dtype=int)
    cell_columns = np.floor((longitude+180)%360).astype(int)[None,:] + np.zeros(lsm.shape,dtype=int)
    cell_ids = cell_rows*360+cell_columns
    true_land = (lsm>=.5) & (latitude[:,None]>=-60)
    land_cells = np.bincount(cell_ids[true_land],minlength=64800)>0
    selected = (latitude[:,None]>=-60) & ((lsm>=.5) | ((lsm>=.05)&~land_cells[cell_ids]))
    ids = np.unique(cell_ids[selected]); ordinal = np.full(64800,-1); ordinal[ids]=np.arange(len(ids))
    np.stack([ids//360,ids%360],axis=1).astype('<u2').tofile(output/'cells.bin')
    native_rows,native_columns = np.where(selected)
    points=np.zeros(len(native_rows),dtype=POINT_DTYPE)
    points['fine']=native_rows*1440+(native_columns+720)%1440
    points['source']=points['fine']; points['cell']=ordinal[cell_ids[selected]]
    # Coastal towns sit near sea level, but a coastal grid square's mean height includes the hills behind them
    # (Málaga's square reads about 3 °C too cold). Coastal points therefore take their temperature corrected to
    # sea level, at 6.5 °C per km, for at most 600 m. Coastal: some sea in the square, or sea in a neighbouring one.
    sea=lsm<.5
    sea_next_door=np.zeros_like(sea)
    for dr in (-1,0,1):
        for dc in (-1,0,1):
            sea_next_door|=np.roll(np.roll(sea,dr,axis=0),dc,axis=1)
    coastal=(lsm<.95)|sea_next_door
    SEA_LEVEL_CORRECTION_LIMIT_M=600
    for i,(r,c) in enumerate(zip(native_rows,native_columns)):
        if not coastal[r,c]: continue
        points['shift'][i]=np.rint(.0065*min(max(heights[r,c],0),SEA_LEVEL_CORRECTION_LIMIT_M)*100)
        if lsm[r,c]>=.8: continue
        candidates=[]
        for dr in (-1,0,1):
            for dc in (-1,0,1):
                rr,cc=r+dr,c+dc
                if 0<=rr<721 and 0<=cc<1440 and rr//12==r//12 and cc//12==c//12 and lsm[rr,cc]>=.8:
                    distance=dr*dr+(dc*np.cos(np.deg2rad(latitude[r])))**2
                    candidates.append((distance,rr,cc))
        if candidates:
            _,rr,cc=min(candidates)
            points['source'][i]=rr*1440+(cc+720)%1440
            points['shift'][i]=np.rint(.0065*min(max(heights[rr,cc],0),SEA_LEVEL_CORRECTION_LIMIT_M)*100)
    points=points[np.argsort(points['fine'])]
    with open(output/'points.bin','wb') as f:
        np.array([len(points)],dtype='<u4').tofile(f); points.tofile(f)
    elapsed=time.monotonic()-started
    (output/'READY-static').write_text(f'N={len(ids)} P={len(points)} cells.bin={len(ids)*4} points.bin={4+len(points)*12} seconds={elapsed:.3f}\n')
    print((output/'READY-static').read_text(),flush=True)
    return points

if __name__=='__main__': build_static()
