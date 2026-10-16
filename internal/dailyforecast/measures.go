package dailyforecast

import (
	"fmt"
	"math"
	"time"

	"nice-weather-finder/internal/daymeasures"
)

// MeasurePoint is one ECMWF IFS open-data point in native field units.
// RainM is tp, accumulated since the model run began. State fields are 2t,
// 2d, 10u and 10v. These are the existing job's decoded fields.
type MeasurePoint struct {
	Time                                               time.Time
	TemperatureK, DewPointK, WindUMPS, WindVMPS, RainM float64
}

// MeasuresForDay is an opt-in adapter; the daily forecast job does not call it.
// IFS publishes state every three hours, then six hours. Linear interpolation
// yields the same ten civil-hour temperature samples as the climate interface;
// accumulated rain is apportioned uniformly within each source interval.
// Sun and gust remain explicitly unavailable: open data offers total (direct
// plus diffuse) radiation, and gust maxima cover only the last output hour.
func MeasuresForDay(date time.Time, location *time.Location, points []MeasurePoint) (daymeasures.DayMeasures, error) {
	var hours []daymeasures.HourlyFacts
	for index, point := range points {
		if point.Time.Minute() != 0 || point.Time.Second() != 0 || point.Time.Nanosecond() != 0 {
			return daymeasures.DayMeasures{}, fmt.Errorf("IFS states must be on UTC hours")
		}
		if index == 0 {
			hours = append(hours, daymeasures.HourlyFacts{Time: point.Time, TemperatureC: point.TemperatureK - 273.15, DewPointC: point.DewPointK - 273.15, WindKPH: math.Hypot(point.WindUMPS, point.WindVMPS) * 3.6, IntervalMissing: true})
			continue
		}
		before := points[index-1]
		duration := point.Time.Sub(before.Time)
		if duration <= 0 || duration%time.Hour != 0 {
			return daymeasures.DayMeasures{}, fmt.Errorf("IFS states must be ordered, at whole-hour intervals")
		}
		rainMM := (point.RainM - before.RainM) * 1000
		if rainMM < 0 {
			return daymeasures.DayMeasures{}, fmt.Errorf("IFS cumulative rain decreases at %s", point.Time.Format(time.RFC3339))
		}
		for instant := before.Time.Add(time.Hour); !instant.After(point.Time); instant = instant.Add(time.Hour) {
			weight := instant.Sub(before.Time).Seconds() / duration.Seconds()
			interpolate := func(left, right float64) float64 { return left + (right-left)*weight }
			hours = append(hours, daymeasures.HourlyFacts{Time: instant,
				TemperatureC: interpolate(before.TemperatureK, point.TemperatureK) - 273.15,
				DewPointC:    interpolate(before.DewPointK, point.DewPointK) - 273.15,
				WindKPH:      math.Hypot(interpolate(before.WindUMPS, point.WindUMPS), interpolate(before.WindVMPS, point.WindVMPS)) * 3.6,
				RainMM:       rainMM / duration.Hours(),
			})
		}
	}
	return daymeasures.Aggregate(date, location, hours)
}
