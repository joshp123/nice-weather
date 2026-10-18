"""Stop explicitly identified acquisition workers at an agreed wire budget.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Only monitors the named progress files and process IDs supplied by the operator.
The threshold leaves room for responses already in flight.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import time

def atomic_json(path,value):
    path.parent.mkdir(parents=True,exist_ok=True)
    temporary=path.with_suffix('.partial')
    temporary.write_text(json.dumps(value,separators=(',',':'))+'\n')
    temporary.replace(path)


def transferred(path):
    return json.loads(path.read_text())['transferredBytes'] if path.exists() else 0


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--progress',type=Path,action='append',default=[])
    parser.add_argument('--pid',type=int,action='append',required=True)
    parser.add_argument('--maximum-additional-bytes',type=int,required=True)
    parser.add_argument('--maximum-seconds',type=float,required=True)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    baseline=[transferred(path) for path in args.progress]
    started=time.monotonic()
    while True:
        elapsed=time.monotonic()-started
        current=[transferred(path) for path in args.progress]
        additional=sum(value-old for value,old in zip(current,baseline))
        reason='wire-budget' if additional>=args.maximum_additional_bytes else 'time-budget' if elapsed>=args.maximum_seconds else None
        status=dict(processIds=args.pid,progressFiles=[str(path) for path in args.progress],baselineTransferredBytes=baseline,
                    currentTransferredBytes=current,additionalTransferredBytes=additional,seconds=elapsed,
                    maximumAdditionalBytes=args.maximum_additional_bytes,maximumSeconds=args.maximum_seconds,
                    stopped=reason is not None,reason=reason)
        if reason:
            for pid in args.pid:
                try:
                    os.kill(pid,signal.SIGTERM)
                except ProcessLookupError:
                    pass
        atomic_json(args.output,status)
        if reason:
            print(json.dumps(status),flush=True)
            return
        time.sleep(2)


if __name__=='__main__':
    main()
