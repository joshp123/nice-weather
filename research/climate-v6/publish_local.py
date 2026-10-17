# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import ctypes
import os
import pathlib
import tempfile


def publish_climate_snapshot(root, snapshot):
    published = root / 'v6/climate'
    stage = pathlib.Path(tempfile.mkdtemp(prefix='exchange-', dir=root / 'climate-v6-work'))
    for source in (snapshot / 'v6/climate').iterdir():
        os.link(source, stage / source.name)
    if published.exists():
        system = ctypes.CDLL(None, use_errno=True)
        exchange = system.renameatx_np
        exchange.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        exchange.restype = ctypes.c_int
        # Darwin's atomic directory exchange: AT_FDCWD=-2, RENAME_SWAP=2.
        if exchange(-2, os.fsencode(stage), -2, os.fsencode(published), 2):
            raise OSError(ctypes.get_errno(), 'Atomic climate directory exchange failed')
        stage.rename(snapshot / 'previous-climate')
    else:
        stage.rename(published)
    marker = root / 'v6/READY-climate'
    temporary_marker = root / 'v6/.READY-climate-next'
    temporary_marker.symlink_to('climate/meta.json')
    temporary_marker.replace(marker)
