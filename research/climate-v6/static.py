# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import pathlib, time
import numpy as np
ROOT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
POINT_DTYPE = np.dtype([('fine','<u4'),('source','<u4'),('shift','<i2'),('cell','<u2')])

def build_static(root=ROOT):
    started = time.monotonic()
    source = root/'era5'; output = root/'v6'; output.mkdir(exist_ok=True)
    lsm = np.load(source/'static_lsm.npy')
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
    points=points[np.argsort(points['fine'])]
    with open(output/'points.bin','wb') as f:
        np.array([len(points)],dtype='<u4').tofile(f); points.tofile(f)
    elapsed=time.monotonic()-started
    (output/'READY-static').write_text(f'N={len(ids)} P={len(points)} temperature=native source=fine shift=0 cells.bin={len(ids)*4} points.bin={4+len(points)*12} seconds={elapsed:.3f}\n')
    print((output/'READY-static').read_text(),flush=True)
    return points

if __name__=='__main__': build_static()
