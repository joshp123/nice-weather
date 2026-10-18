"""Reuse the standard non-leap TMY calendar after checking every source stamp.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
PVGIS selected years differ by month; the normalization uses their common
month/day/hour calendar and handles the source year's civil offset separately.
"""
import datetime as dt

import numpy as np

_START = dt.datetime(2001, 1, 1, tzinfo=dt.timezone.utc)
_CALENDAR_TEXT = tuple((_START + dt.timedelta(hours=hour)).strftime('%m%d:%H%M')
                       for hour in range(8760))
_TIMESTAMPS = _START.timestamp() + np.arange(8760, dtype=float) * 3600
_TIMESTAMPS.flags.writeable = False


def nominal_tmy_timestamps(hourly):
    if tuple(row['time(UTC)'][4:] for row in hourly) != _CALENDAR_TEXT:
        raise ValueError('TMY does not contain the standard 8760-hour calendar')
    return _TIMESTAMPS
