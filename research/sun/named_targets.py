"""Combine complete satellite targets in one deterministic source order.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
SARAH long normals take priority, then the approved GOES typical direct year,
then both southern GOES years. Validation subsets are explicitly labelled.
"""
import argparse
from collections import Counter
import gzip
import hashlib
import json
from pathlib import Path


def records(path):
    if path.suffix=='.ndjson':
        with path.open() as source:
            for line in source:
                yield json.loads(line)
    else:
        yield from json.loads(path.read_text())['records']


def key(record):
    return round(record['latitude']*1e5),round(record['longitude']*1e5)


def digest(path):
    result=hashlib.sha256()
    with path.open('rb') as source:
        for block in iter(lambda:source.read(1024*1024),b''):
            result.update(block)
    return result.hexdigest()


def average_years(earlier,later):
    if key(earlier)!=key(later) or earlier['selectedCoordinates']!=later['selectedCoordinates']:
        raise ValueError('Southern source years use different native pixels')
    result=dict(earlier)
    result['source']='NSRDB GOES4 full-disc2022+2024'
    result['years']=[2022,2024]
    result['sourceFiles']=[dict(url=row['url'],rawSha256=row['rawSha256']) for row in (earlier,later)]
    if earlier.get('modelSupported') is False and later.get('modelSupported') is False:
        return result
    monthly=[]
    for a,b in zip(earlier['months'],later['months']):
        if a is None or b is None:
            raise ValueError('A southern source month is missing')
        count=a['sourceDays']+b['sourceDays']
        mean=lambda x,y:(x*a['sourceDays']+y*b['sourceDays'])/count
        monthly.append(dict(sunnyShare=mean(a['sunnyShare'],b['sunnyShare']),
                            hourlyDni=[mean(x,y) for x,y in zip(a['hourlyDni'],b['hourlyDni'])],
                            daylightHours=mean(a['daylightHours'],b['daylightHours']),sourceDays=count))
    result['months']=monthly
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sarah',type=Path,required=True)
    parser.add_argument('--north',type=Path,required=True)
    parser.add_argument('--south2022',type=Path,required=True)
    parser.add_argument('--south2024',type=Path,required=True)
    parser.add_argument('--coordinates',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--validation',action='store_true')
    parser.add_argument('--profiles-progress',type=Path)
    parser.add_argument('--north-plan',type=Path)
    parser.add_argument('--south-plan',type=Path)
    args=parser.parse_args()
    if not args.validation and not all((args.profiles_progress,args.north_plan,args.south_plan)):
        raise ValueError('Worldwide delivery requires final profile progress and both source plans')
    sarah_all={key(row):row for row in records(args.sarah)}
    for row in sarah_all.values():
        if not any(row['months']) and not(row.get('satelliteEligible') and row.get('timezone') is None):
            raise ValueError(f"Eligible SARAH point lacks satellite values: {key(row)}")
    sarah={point:row for point,row in sarah_all.items() if any(row['months'])}
    north={key(row):row for row in records(args.north)}
    south_a={key(row):row for row in records(args.south2022)}
    south_b={key(row):row for row in records(args.south2024)}
    if south_a.keys()!=south_b.keys():
        raise ValueError('Both southern years must cover every selected point')
    if not args.validation:
        progress=json.loads(args.profiles_progress.read_text())
        if progress['complete']!=progress['total'] or len(sarah_all)!=progress['eligible']:
            raise ValueError('SARAH source acquisition has unfinished eligible points')
        for path,count in [(args.north_plan,len(north)),(args.south_plan,len(south_a))]:
            if json.loads(path.read_text())['eligible']!=count:
                raise ValueError(f'Incomplete native point acquisition: {path}')
    combined={point:average_years(south_a[point],south_b[point]) for point in south_a}
    combined.update(north)
    combined.update(sarah_all)
    unsupported_model={point for point,row in combined.items() if not any(row['months'])}
    combined={point:row for point,row in combined.items() if any(row['months'])}
    coordinates=json.loads(args.coordinates.read_text())
    expected={(round((row['latitude'] if isinstance(row,dict) else row[0])*1e5),
               round((row['longitude'] if isinstance(row,dict) else row[1])*1e5)) for row in coordinates}
    extra=combined.keys()-expected
    if extra:
        raise ValueError(f'Unexpected named coordinates: {len(extra)}')
    ordered=[combined[point] for point in sorted(combined)]
    missing=sorted(expected-combined.keys())
    coverage=dict(namedPoints=len(expected),correctedPoints=len(ordered),unchangedPoints=len(missing),
                  sources=dict(Counter(row['source'] for row in ordered)),validationSubset=args.validation,
                  satelliteEligibleModelUnsupported=len(unsupported_model),
                  periods={row['source']:row['years'] for row in ordered},
                  inputFiles=[dict(file=path.name,bytes=path.stat().st_size,sha256=digest(path))
                              for path in (args.sarah,args.north,args.south2022,args.south2024)],
                  unchangedCoordinates=[[lat/1e5,lon/1e5] for lat,lon in missing],
                  unchangedReasons={'modelUnsupported':len(unsupported_model),
                                    'outsideAcquiredSources':len(missing)-len(unsupported_model)},
                  unchangedReason='Unsupported existing place model, or outside acquired satellite coverage; retain native model. Other unacquired independent satellite sources may exist.')
    args.output.parent.mkdir(parents=True,exist_ok=True)
    with args.output.with_suffix('.partial').open('w') as output:
        json.dump(dict(records=ordered,coverage=coverage,deliveryComplete=not args.validation),output,separators=(',',':'),allow_nan=False)
        output.write('\n')
    args.output.with_suffix('.partial').replace(args.output)
    args.output.with_suffix('.coverage.json').write_text(json.dumps(coverage,separators=(',',':'))+'\n')
    support=args.output.parent/'source-support.tsv.gz'
    with gzip.open(support,'wt') as table:
        table.write('latitude\tlongitude\tsource\n')
        for lat,lon in sorted(expected):
            row=combined.get((lat,lon))
            code='SARAH3' if row and row['source'].startswith('PVGIS-SARAH3') else 'GOES-TDY' if row and 'TDY' in row['source'] else 'GOES-TMY' if row and 'TMY' in row['source'] else 'GOES-2022-2024' if row else 'native'
            table.write(f'{lat/1e5:.5f}\t{lon/1e5:.5f}\t{code}\n')
    print(json.dumps({**{k:v for k,v in coverage.items() if k!='unchangedCoordinates'},
                      'bytes':args.output.stat().st_size,'sha256':digest(args.output),
                      'supportBytes':support.stat().st_size,'supportSha256':digest(support)}))


if __name__=='__main__':
    main()
