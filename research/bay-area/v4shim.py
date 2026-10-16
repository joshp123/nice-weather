# Written by AI on 2026.10.17 by model claude opus 5.5. Lets the Bay Area analysis score days with the v4 rule at Josh's defaults.
import importlib.util
spec = importlib.util.spec_from_file_location('v4', '/tmp/nwf-agents/round3/opus/v4.py'); v4 = importlib.util.module_from_spec(spec); spec.loader.exec_module(v4)
v4.COLD_RAMP, v4.HOT_RAMP = 4.0, 6.0
DEFAULT = dict(low=21.0, high=30.0, evening=12.0, humidity=20.0, rain=1.0, wind=35.0)
local_index = None
def score(T, dew, ws, dni, rain): return (v4.score(T, dew, ws, dni, rain, DEFAULT),)
