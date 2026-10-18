# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Integrate instantaneous satellite DNI at minute midpoints in civil hours."""
import calendar
import datetime as dt
import functools
from zoneinfo import ZoneInfo

import numpy as np

UTC = dt.timezone.utc


@functools.lru_cache(maxsize=4096)
def _month_offsets(zone_name, year, month):
    zone = ZoneInfo(zone_name)
    return tuple(dt.datetime(year, month, day, 12, tzinfo=zone).utcoffset().total_seconds()/3600
                 for day in range(1, calendar.monthrange(year, month)[1]+1))


@functools.lru_cache(maxsize=32)
def _time_axis(stamps, typical):
    observed = [dt.datetime.fromisoformat(stamp.replace('Z', '+00:00')).replace(tzinfo=UTC) for stamp in stamps]
    if typical:
        observed = [stamp.replace(year=2001) for stamp in observed]
    axis = np.array([stamp.timestamp() for stamp in observed])
    if np.any(np.diff(axis) <= 0):
        raise ValueError('Satellite timestamps must increase; typical years must omit February29')
    return axis


@functools.lru_cache(maxsize=128)
def _day_axis(stamps, zone_name, selected_years):
    typical = bool(selected_years)
    axis = _time_axis(stamps, typical)
    zone = ZoneInfo(zone_name)
    if typical:
        start, stop = dt.date(2001, 1, 1), dt.date(2001, 12, 31)
    else:
        start = dt.datetime.fromtimestamp(axis[0], UTC).astimezone(zone).date()
        stop = dt.datetime.fromtimestamp(axis[-1], UTC).astimezone(zone).date()
    dates, actual_years, offsets = [], [], []
    while start <= stop:
        actual_year = selected_years[start.month-1] if typical else start.year
        dates.append(start)
        actual_years.append(actual_year)
        offsets.append(_month_offsets(zone_name, actual_year, start.month)[start.day-1])
        start += dt.timedelta(days=1)
    return tuple(dates), tuple(actual_years), tuple(offsets)


@functools.lru_cache(maxsize=32)
def _minute_weights(stamps, typical, dates, offsets):
    axis = _time_axis(stamps, typical)
    positions = np.arange(len(axis))
    if typical:
        # Only wrap the artificial typical-year join, as the SARAH reducer does.
        year_seconds = 365*86400
        axis = np.concatenate((axis[-24:]-year_seconds, axis, axis[:24]+year_seconds))
        positions = np.concatenate((positions[-24:], positions, positions[:24]))
    midnight = np.array([dt.datetime.combine(date, dt.time(), UTC).timestamp() for date in dates])
    samples = midnight[:, None, None]+(np.arange(10, 22)[None, :, None]-np.array(offsets)[:, None, None])*3600+np.arange(60)[None, None, :]*60+30
    complete = ((samples >= axis[0]) & (samples <= axis[-1])).all(axis=2)
    left = np.clip(np.searchsorted(axis, samples, side='right')-1, 0, len(axis)-2)
    right = left+1
    fraction = np.clip((samples-axis[left])/(axis[right]-axis[left]), 0, 1)
    step = float(np.median(np.diff(axis)))
    complete &= ((axis[right]-axis[left]) <= step+1e-6).all(axis=2)
    # Reduce sixty minute samples to their few distinct source timestamps.
    # This retains np.interp's exact quadrature without a minute-sized array per place.
    first = left.min(axis=2)
    width = int((right.max(axis=2)-first).max())+1
    weights = np.zeros((len(dates)*12, width))
    rows = np.repeat(np.arange(len(dates)*12), 60)
    for indices, weight in [(left, 1-fraction), (right, fraction)]:
        np.add.at(weights, (rows, (indices-first[:, :, None]).ravel()), weight.ravel()/60)
    addresses = first.reshape(-1, 1)+np.arange(width)
    addresses = np.minimum(addresses, len(axis)-1)
    return positions[addresses], weights, complete


def integrate_civil_hours(values, stamps, zone_name, selected_years=None):
    """Return days with date, actual_year, utc_offset_hours, hourly_dni and complete.

    `stamps` are ISO UTC instants. Twelve selected source years identify a
    synthetic TMY/TDY: use a nominal non-leap calendar, with civil offsets from
    each source month's actual year. Observed series retain their real dates
    and leap days. Each returned twelve-hour array covers10:00–21:59; `complete`
    marks hours whose sixty minute centers have actual bracketing observations.
    Linear interpolation approximates hour means; it does not make hourly
    midpoint snapshots into measured hourly averages.
    """
    stamps = tuple(stamp.decode() if isinstance(stamp, bytes) else str(stamp) for stamp in stamps)
    selected_years = tuple(selected_years) if selected_years is not None else ()
    dates, actual_years, offsets = _day_axis(stamps, zone_name, selected_years)
    indices, weights, complete = _minute_weights(stamps, bool(selected_years), dates, offsets)
    hourly = (np.asarray(values)[indices]*weights).sum(axis=1).reshape(-1, 12)
    return [dict(date=date, actual_year=year, utc_offset_hours=offset, hourly_dni=hours, complete=valid)
            for date, year, offset, hours, valid in zip(dates, actual_years, offsets, hourly, complete)]
