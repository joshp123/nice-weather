# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Snapshot the actual named-place release, retaining canonical city coordinates."""
import argparse
import datetime
import json
from pathlib import Path
import subprocess
import urllib.parse

QUERIES = [
    ('Lima', 'Lima', 'peru/'), ('Chiclayo', 'Chiclayo', 'peru/'),
    ('Chimbote', 'Chimbote', 'peru/'), ('Antofagasta', 'Antofagasta', 'chile/'),
    ('Iquique', 'Iquique', 'chile/'), ('Swakopmund', 'Swakopmund', 'namibia/'),
    ('Valparaíso', 'Valparaiso', 'chile/'), ('San Francisco', 'San Francisco', 'united-states/'),
    ('San Diego', 'San Diego', 'united-states/'), ('Tenerife south (Arona)', 'Arona', 'spain/'),
    ('Málaga', 'Malaga', 'spain/'), ('Seville', 'Seville', 'spain/'),
    ('Cape Town', 'Cape Town', 'south-africa/'), ('Rotterdam', 'Rotterdam', 'the-netherlands/'),
]

def read(base, route, **query):
    url = base.rstrip('/') + route + '?' + urllib.parse.urlencode(query)
    return json.loads(subprocess.check_output(['curl','--fail','--silent','--show-error','--max-time','90',url]))

def main(args):
    args.out.mkdir(parents=True, exist_ok=True)
    rows = []
    for name, query, country in QUERIES:
        found = read(args.base, '/api/search', q=query)
        matches = [place for place in found if place.get('slug', '').startswith(country)]
        if not matches:
            raise RuntimeError(f'No canonical result for {name}: {found}')
        place = matches[0]
        if name == 'San Francisco':
            place = min(matches, key=lambda value: abs(value['latitude']-37.775) + abs(value['longitude']+122.419))
        if name == 'Tenerife south (Arona)':
            place = min(matches, key=lambda value: abs(value['latitude']-28.1) + abs(value['longitude']+16.68))
        result = read(args.base, '/api/place', lat=place['latitude'], lon=place['longitude'], period='year')
        if not result.get('namedDefault'):
            raise RuntimeError(f'{name} did not use named default: {result}')
        row = {'validationName': name, **place, 'year': result}
        rows.append(row)
        (args.out/(place['slug'].replace('/', '-')+'.json')).write_text(json.dumps(row, indent=2)+'\n')
        print(f"{name}: {place['latitude']},{place['longitude']} {result['judgement']['niceDayCount']} nice, {result['judgement']['perfectDayCount']} perfect", flush=True)
    manifest = {'writtenBy':'AI, GPT-6.1 Sol, 2026-10-18', 'capturedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(), 'base':args.base, 'places':rows}
    (args.out/'snapshot.json').write_text(json.dumps(manifest, indent=2)+'\n')
    (args.out.parent/'places.json').write_text(json.dumps([{key:row[key] for key in ['validationName','name','slug','latitude','longitude']} for row in rows], indent=2)+'\n')

if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base',default='https://nice.jjpcodes.com')
    parser.add_argument('--out',type=Path,required=True)
    main(parser.parse_args())
