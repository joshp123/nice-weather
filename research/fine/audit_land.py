"""Check that source land polygons remain represented in the fine mask.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
"""
import argparse
import json
import pathlib
import time

import numpy as np
import shapefile
from shapely.geometry import shape,box

from land_support import WIDTH,HEIGHT
from native_normals import file_sha256

PLACES = {'Nauru':(-.5228,166.9315),'Funafuti':(-8.5211,179.1962),
    'Majuro':(7.0897,171.3803),'Bermuda':(32.2949,-64.7814),
    'Male':(4.1755,73.5093),'Tarawa Betio':(1.355,172.926),
    'Pitcairn':(-25.0667,-130.1000),'Wake':(19.28,166.65),
    'Funchal instrument':(32.63,-16.90),'South Pole':(-90.,0.),
    'Tarawa offshore diagnostic':(1.4518,172.9717)}


def pixel(latitude,longitude):
    return min(HEIGHT-1,max(0,int(np.floor((90-latitude)*120)))),int(np.floor((longitude+180)*120))%WIDTH


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--land-support',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    args = parser.parse_args()
    started = time.monotonic()
    masks = {name:np.memmap(args.land_support/f'{name}.u8',mode='r',dtype=np.uint8,shape=(HEIGHT,WIDTH))
             for name in ('natural-earth','osm','land')}
    counts,missing,representative_pixel_misses = {},[],[]
    def inspect(geometry,source,identifier):
        geometry = shape(geometry)
        polygons = geometry.geoms if geometry.geom_type=='MultiPolygon' else [geometry]
        for polygon in polygons:
            point = polygon.representative_point()
            row,column = pixel(point.y,point.x)
            counts[source] = counts.get(source,0)+1
            if not masks[source][row,column]:
                west,south,east,north = polygon.bounds
                first_row,first_column = pixel(north,west)
                last_row,last_column = pixel(south,east)
                retained = []
                for local_row,local_column in np.argwhere(masks[source][first_row:last_row+1,first_column:last_column+1]):
                    r,c = first_row+int(local_row),first_column+int(local_column)
                    cell = box(-180+c/120,90-(r+1)/120,-180+(c+1)/120,90-r/120)
                    if polygon.intersects(cell): retained.append([r,c])
                record = dict(source=source,id=identifier,point=[point.y,point.x],pixel=[row,column],retained_pixels=retained)
                representative_pixel_misses.append(record)
                if not retained: missing.append(record)
    for layer in ('ne_10m_land','ne_10m_minor_islands'):
        features = json.loads((args.source_cache/f'natural-earth/{layer}.geojson').read_text())['features']
        for number,feature in enumerate(features): inspect(feature['geometry'],'natural-earth',f'{layer}/{number}')
    path = next((args.source_cache/'osm-land/extracted').glob('*.shp'))
    with shapefile.Reader(str(path)) as source:
        for number,polygon in enumerate(source.iterShapes()):
            if polygon.points: inspect(polygon.__geo_interface__,'osm',number)
            if (number+1)%100000==0: print(f'polygon interior audit {number+1}; missing {len(missing)}',flush=True)
    places = {name:dict(pixel=pixel(*coordinates),support=int(masks['land'][pixel(*coordinates)]))
              for name,coordinates in PLACES.items()}
    result = dict(land_sha256=file_sha256(args.land_support/'land.u8'),
        method='Each polygon has a marked representative-point pixel or a marked pixel geometrically intersecting it.',
        checked_polygons=counts,missing=missing,representative_pixel_misses=representative_pixel_misses,
        places=places,seconds=time.monotonic()-started)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result),flush=True)
    if missing: raise ValueError('Source land polygons disappeared from the fine mask')
    absent = [name for name,value in places.items() if not value['support'] and name not in ('Funchal instrument','Tarawa offshore diagnostic')]
    if absent: raise ValueError(f'Named land probes are absent: {absent}')


if __name__ == '__main__': main()
