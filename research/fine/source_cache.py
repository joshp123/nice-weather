"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Anonymous, resumable source downloads. Completed files retain their URL,
response headers, byte count and SHA256 next to the cached source.
"""

import concurrent.futures
import fcntl
import hashlib
import json
import pathlib
import shutil
import subprocess
import time


def sha256_file(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(8*1024*1024), b''):
            digest.update(block)
    return digest.hexdigest()


def download(url,destination,workers=4,part_bytes=8*1024*1024):
    destination = pathlib.Path(destination)
    destination.parent.mkdir(parents=True,exist_ok=True)
    with destination.with_name(destination.name+'.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        try:
            return _download(url,destination,workers,part_bytes)
        except Exception as error:
            print(f'Source download failed: {url}: {error}',flush=True)
            raise


def _download(url, destination, workers, part_bytes):
    destination = pathlib.Path(destination)
    metadata_path = destination.with_name(destination.name+'.source.json')
    if destination.exists() and metadata_path.exists():
        metadata = json.loads(metadata_path.read_text())
        if metadata['url'] != url or destination.stat().st_size != metadata['bytes']:
            raise ValueError(f'Cached source does not match its manifest: {destination}')
        return metadata
    destination.parent.mkdir(parents=True, exist_ok=True)
    headers = subprocess.check_output([
        'curl','--fail','--silent','--show-error','--location','--head',
        '--retry','4','--retry-all-errors','--connect-timeout','20','--max-time','120',url,
    ]).decode()
    lengths = [int(line.split(':',1)[1]) for line in headers.splitlines()
               if line.lower().startswith('content-length:')]
    if not lengths:
        raise ValueError(f'Source has no Content-Length: {url}')
    length = lengths[-1]
    if destination.exists() and destination.stat().st_size != length:
        raise ValueError(f'Unexpected existing source size: {destination}')
    started = time.monotonic()
    if not destination.exists():
        parts = destination.with_name(destination.name+'.parts')
        parts.mkdir(exist_ok=True)
        ranges = [(start,min(start+part_bytes,length)) for start in range(0,length,part_bytes)]

        def fetch(bounds):
            start,end = bounds
            path = parts/str(start)
            if path.exists() and path.stat().st_size == end-start:
                return path
            partial = path.with_suffix('.partial')
            subprocess.run([
                'curl','--fail','--silent','--show-error','--location','--retry','5',
                '--retry-all-errors','--connect-timeout','20','--max-time','300',
                '--range',f'{start}-{end-1}',url,'--output',str(partial),
            ],check=True)
            if partial.stat().st_size != end-start:
                raise ValueError(f'Incorrect HTTP range length: {url} {start}:{end}')
            partial.replace(path)
            print(f'{destination.name}: {end:,}/{length:,} source bytes cached',flush=True)
            return path

        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
            paths = list(pool.map(fetch,ranges))
        partial = destination.with_name(destination.name+'.partial')
        with partial.open('wb') as output:
            for path in paths:
                with path.open('rb') as stream:
                    shutil.copyfileobj(stream,output,8*1024*1024)
        partial.replace(destination)
    metadata = dict(url=url,bytes=length,sha256=sha256_file(destination),
                    headers=headers,accessed_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),
                    seconds=time.monotonic()-started)
    metadata_path.write_text(json.dumps(metadata,indent=2))
    return metadata


def save_evidence(url, destination):
    destination = pathlib.Path(destination)
    if not destination.exists():
        destination.parent.mkdir(parents=True,exist_ok=True)
        partial = destination.with_name(destination.name+'.partial')
        subprocess.run(['curl','--fail','--silent','--show-error','--location',
                        '--retry','4','--retry-all-errors','--connect-timeout','20',
                        '--max-time','120',url,'--output',str(partial)],check=True)
        partial.replace(destination)
    metadata_path = destination.with_name(destination.name+'.source.json')
    if metadata_path.exists():
        metadata = json.loads(metadata_path.read_text())
        if metadata['url'] != url or metadata['sha256'] != sha256_file(destination):
            raise ValueError(f'Licence snapshot does not match its manifest: {destination}')
    else:
        metadata = dict(url=url,path=destination.name,bytes=destination.stat().st_size,
                        sha256=sha256_file(destination),
                        accessed_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()))
        metadata_path.write_text(json.dumps(metadata,indent=2))
    return metadata


