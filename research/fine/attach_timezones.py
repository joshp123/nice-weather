"""Publish the actual local-clock lookup separately under ODbL.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Does not regenerate lookup values or alter the weather build manifest.
"""

import argparse
import datetime as dt
import importlib.metadata
import json
import pathlib
import shutil

import numpy as np

from source_cache import save_evidence, sha256_file


POINT_DTYPE = np.dtype([('fine','<u4'),('source','<u4'),('shift','<i2'),('cell','<u2')])
DEFAULT_CACHE = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-sources/timezones')
PRIMARY_EVIDENCE = {
    'timezonefinder-README.rst': 'https://raw.githubusercontent.com/jannikmi/timezonefinder/master/README.rst',
    'timezone-boundary-builder-README.md': 'https://raw.githubusercontent.com/evansiroky/timezone-boundary-builder/master/README.md',
    'ODbL-1.0.html': 'https://opendatacommons.org/licenses/odbl/1-0/',
    'OSM-copyright.html': 'https://www.openstreetmap.org/copyright',
    'OSMF-produced-work.html': 'https://osmfoundation.org/wiki/Licence/Community_Guidelines/Produced_Work_-_Guideline',
    'OSMF-geocoding.html': 'https://osmfoundation.org/wiki/Licence/Community_Guidelines/Geocoding_-_Guideline',
}
NOTICE = '''Local-clock timezone lookup data: Open Database License 1.0 (ODbL).
Contains information from timezone-boundary-builder, processed by timezonefinder,
and © OpenStreetMap contributors, available under ODbL 1.0.
https://github.com/evansiroky/timezone-boundary-builder
https://www.openstreetmap.org/copyright
https://opendatacommons.org/licenses/odbl/1-0/

lookup.npz is the machine-readable point-to-IANA-timezone lookup used for the
local-clock weather calculations. It is offered separately under ODbL 1.0.
Modifications: polygon membership sampled at native quarter-degree coordinates;
lookup arrays extracted from the exact cached build inputs and deduplicated.
The separately supplied weather values retain their stated CC BY-SA 4.0 licence.
timezonefinder application code is MIT licensed; see timezonefinder-MIT.txt.
'''


def attach(native_root, output, source_cache=DEFAULT_CACHE):
    points_path = native_root/'v6/points.bin'
    names_path = native_root/'climate-v6-work/timezones.npy'
    point_count = np.fromfile(points_path,dtype='<u4',count=1)
    points = np.fromfile(points_path,dtype=POINT_DTYPE,offset=4)
    names = np.load(names_path,allow_pickle=False)
    if len(point_count) != 1 or int(point_count[0]) != len(points) or names.shape != (len(points),):
        raise ValueError('Point header, records and timezone lookup lengths differ')
    if names.dtype.kind != 'U' or np.any(names == ''):
        raise ValueError('Timezone lookup must contain nonempty Unicode IANA identifiers')
    target = output/'timezones'
    evidence = target/'evidence'
    evidence.mkdir(parents=True,exist_ok=True)
    timezone_names,timezone_index = np.unique(names,return_inverse=True)
    lookup_path = target/'lookup.npz'
    np.savez_compressed(lookup_path,fine=points['fine'],
                        timezone_index=timezone_index.astype('<u2'),timezone_names=timezone_names)
    (target/'ATTRIBUTION.txt').write_text(NOTICE)
    packages = {}
    for package,licence_name in [('timezonefinder','LICENSE'),('timezonefinder-data','DATA_LICENSE')]:
        distribution = importlib.metadata.distribution(package)
        licence = next(path for path in distribution.files
                       if pathlib.Path(str(path)).name == licence_name)
        destination = target/('timezonefinder-MIT.txt' if package == 'timezonefinder' else 'ODbL-1.0.txt')
        shutil.copyfile(distribution.locate_file(licence),destination)
        metadata_path = evidence/(package+'-METADATA.txt')
        metadata_path.write_text(distribution.read_text('METADATA'))
        packages[package] = dict(version=distribution.version,
            metadata_sha256=sha256_file(metadata_path),licence_sha256=sha256_file(destination))
        if package == 'timezonefinder-data':
            dataset_files = [path for path in distribution.files
                             if str(path).startswith('timezonefinder_data/data/')]
            packages[package]['installed_data_sha256'] = {
                str(path):sha256_file(distribution.locate_file(path)) for path in dataset_files}
            version_path = next(path for path in dataset_files if str(path).endswith('/data_version.txt'))
            packages[package]['data_version'] = distribution.locate_file(version_path).read_text().strip()
    primary = {}
    for name,url in PRIMARY_EVIDENCE.items():
        cached_path = source_cache/name
        primary[name] = save_evidence(url,cached_path)
        shutil.copyfile(cached_path,evidence/name)
        shutil.copyfile(cached_path.with_name(name+'.source.json'),evidence/(name+'.source.json'))
    manifest = dict(licence='ODbL-1.0',licence_url='https://opendatacommons.org/licenses/odbl/1-0/',
        attached_utc=dt.datetime.now(dt.timezone.utc).isoformat(),points=len(points),
        unique_timezones=len(timezone_names),lookup_sha256=sha256_file(lookup_path),
        input_points_sha256=sha256_file(points_path),input_timezones_sha256=sha256_file(names_path),
        coordinates='fine = row * 1440 + column; latitude = 90 - row / 4; longitude = -180 + column / 4',
        schema={'fine':'uint32 point grid index','timezone_index':'uint16 index into timezone_names',
                'timezone_names':'Unicode IANA timezone identifiers; no pickle required'},
        lookup_generation_versions='Not recorded in the original cached input. Attachment does not regenerate or relabel its values.',
        attachment_environment=packages,primary_evidence=primary,
        scope='Only this point-to-timezone lookup is ODbL. Weather values keep their separate declared licence.',
        files_sha256={str(path.relative_to(target)):sha256_file(path)
                      for path in sorted(target.rglob('*')) if path.is_file() and path.name != 'manifest.json'})
    reference_configuration = native_root/'reference-configuration.json'
    if reference_configuration.exists():
        manifest['native_reference_configuration_sha256'] = sha256_file(reference_configuration)
    (target/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(f'Attached {len(points)} timezone lookups ({lookup_path.stat().st_size:,} bytes) to {target}',flush=True)
    print('Weather build metadata was not changed; rehash its publication files after attachment.',flush=True)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native-root',required=True,type=pathlib.Path)
    parser.add_argument('--output',required=True,type=pathlib.Path)
    parser.add_argument('--source-cache',default=DEFAULT_CACHE,type=pathlib.Path)
    args = parser.parse_args()
    attach(args.native_root,args.output,args.source_cache)


if __name__ == '__main__':
    main()
