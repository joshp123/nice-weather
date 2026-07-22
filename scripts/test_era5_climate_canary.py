import datetime as dt
import unittest

import numpy as np

import era5_climate_canary as canary


UTC = dt.timezone.utc


def hourly(start: dt.datetime, count: int = 72) -> list[dt.datetime]:
    return [start + dt.timedelta(hours=index) for index in range(count)]


class AggregationTests(unittest.TestCase):
    def test_selects_ten_interval_ends_from_1100_through_2000_local(self) -> None:
        times = hourly(dt.datetime(2025, 1, 14, tzinfo=UTC))
        selected = canary.selected_interval_indices(dt.date(2025, 1, 15), "Europe/Amsterdam", times)
        local_hours = [times[index].astimezone(canary.ZoneInfo("Europe/Amsterdam")).hour for index in selected]
        self.assertEqual(local_hours, list(range(11, 21)))

    def test_selection_can_cross_a_utc_date(self) -> None:
        times = hourly(dt.datetime(2025, 1, 14, tzinfo=UTC))
        selected = canary.selected_interval_indices(dt.date(2025, 1, 15), "Pacific/Auckland", times)
        self.assertEqual(times[selected[0]], dt.datetime(2025, 1, 14, 22, tzinfo=UTC))
        self.assertEqual(times[selected[-1]], dt.datetime(2025, 1, 15, 7, tzinfo=UTC))

    def test_dst_days_still_have_ten_daytime_intervals(self) -> None:
        for date in (dt.date(2025, 3, 30), dt.date(2025, 10, 26)):
            times = hourly(dt.datetime.combine(date - dt.timedelta(days=1), dt.time(), UTC))
            self.assertEqual(len(canary.selected_interval_indices(date, "Europe/Amsterdam", times)), 10)

    def test_aggregates_synchronous_physics(self) -> None:
        date = dt.date(2025, 5, 1)
        times = hourly(dt.datetime(2025, 4, 30, tzinfo=UTC))
        selected = canary.selected_interval_indices(date, "Asia/Singapore", times)
        values = {field: np.zeros(len(times), dtype=np.float64) for field in canary.FIELDS}
        values["t2m"][:] = 303.15
        values["d2m"][:] = 297.15
        values["tp"][selected] = np.arange(10) * 0.00005
        values["ssrd"][selected] = 1_000_000
        values["tcc"][selected] = 0.25
        values["u10"][selected] = 3
        values["v10"][selected] = 4
        result = canary.aggregate_day(self.cell(), date, times, values)["metrics"]
        self.assertEqual(result["comfortableTempC"], 30)
        self.assertEqual(result["rainMm"], 2.25)
        self.assertEqual(result["wetTimeFraction"], 0.8)
        self.assertEqual(result["solarEnergyMJm2"], 10)
        self.assertEqual(result["cloudCoverPercent"], 25)
        self.assertEqual(result["windSpeedKmh"], 18)
        self.assertAlmostEqual(result["relativeHumidityPercent"], 70.293061, places=6)

    def test_rejects_missing_nonfinite_and_invalid_fields(self) -> None:
        date = dt.date(2025, 5, 1)
        times = hourly(dt.datetime(2025, 4, 30, tzinfo=UTC))
        values = {field: np.ones(len(times), dtype=np.float64) for field in canary.FIELDS}
        values["t2m"][:] = 300
        values["d2m"][:] = 290
        missing = dict(values)
        del missing["tcc"]
        with self.assertRaisesRegex(ValueError, "missing fields"):
            canary.aggregate_day(self.cell(), date, times, missing)
        selected = canary.selected_interval_indices(date, "Asia/Singapore", times)
        values["u10"][selected[0]] = np.nan
        with self.assertRaisesRegex(ValueError, "incomplete"):
            canary.aggregate_day(self.cell(), date, times, values)
        values["u10"][selected[0]] = 1
        values["tcc"][selected[0]] = 1.1
        with self.assertRaisesRegex(ValueError, "outside 0-1"):
            canary.aggregate_day(self.cell(), date, times, values)

    def test_rejects_non_synchronous_or_supersaturated_humidity(self) -> None:
        with self.assertRaises(ValueError):
            canary.relative_humidity_percent(np.array([20.0]), np.array([np.nan]))
        cases = ((20.0, 0, 0.0), (20.001, 1, 0.001), (20.01, 1, 0.01))
        for dew_point, wanted_count, wanted_excess in cases:
            humidity, corrected, excess = canary.relative_humidity_percent(np.array([20.0]), np.array([dew_point]))
            self.assertEqual(corrected, wanted_count)
            self.assertAlmostEqual(excess, wanted_excess)
            self.assertLessEqual(humidity[0], 100)
        with self.assertRaises(ValueError):
            canary.relative_humidity_percent(np.array([20.0]), np.array([20.01001]))
        np.testing.assert_array_equal(canary.bounded_relative_humidity(np.array([100.0, 100.0 + 5e-10])), np.array([100.0, 100.0]))
        with self.assertRaises(ValueError):
            canary.bounded_relative_humidity(np.array([100.0 + 2e-9]))

    @staticmethod
    def cell() -> dict[str, object]:
        return {"id": "test", "timezone": "Asia/Singapore", "gridLatitude": 1.25, "gridLongitude": 103.75}


if __name__ == "__main__":
    unittest.main()
