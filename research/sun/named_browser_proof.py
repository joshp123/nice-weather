# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Rendered headless WebKit year/winter and fog-coast card checks."""
import argparse
import asyncio
import importlib.util
from pathlib import Path

spec=importlib.util.spec_from_file_location('named_view_webkit',Path(__file__).resolve().parents[1]/'fine/named_view_webkit.py')
proof=importlib.util.module_from_spec(spec)
spec.loader.exec_module(proof)
proof.VIEWS=[
    ('world-year-laptop','/year',1512,982),
    ('world-year-phone','/year',390,844),
    ('nov-mar-laptop','/nov-mar',1512,982),
    ('nov-mar-phone','/nov-mar',390,844),
    ('lima-year-phone','/peru/lima/year',390,844),
    ('chiclayo-year-laptop','/peru/chiclayo/year',1512,982),
    ('antofagasta-winter-phone','/chile/antofagasta/nov-mar',390,844),
]

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base',default='https://nice.jjpcodes.com')
    parser.add_argument('--out',type=Path,required=True)
    parser.add_argument('--view',action='append')
    parser.add_argument('--settle-ms',type=int,default=3000)
    parser.add_argument('--fixture',type=Path)
    parser.add_argument('--reference',type=Path)
    parser.set_defaults(correctness=False)
    asyncio.run(proof.main(parser.parse_args()))
