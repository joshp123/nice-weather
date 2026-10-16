// Package daymeasures turns hourly physical facts into local-day measures.
// It contains no weather preferences, scores, or presentation rules.
package daymeasures

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// HourlyFacts holds instantaneous state at Time. Rain and sunshine are totals
// for the preceding hour [Time-1h, Time), following ERA5's accumulation labels.
type HourlyFacts struct {
	Time                                     time.Time
	TemperatureC, DewPointC, RainMM, WindKPH float64
	SunHours, SunUpHours                     float64
	SunAvailable                             bool
	GustKPH                                  float64
	GustAvailable                            bool
	// IntervalMissing distinguishes a boundary state with no preceding totals.
	IntervalMissing bool
}

type DayMeasures struct {
	// Ten samples at 10:00, 11:00, ... 19:00 local civil time.
	TemperatureC                                [10]float64
	DewPointC, DaytimeRainMM, WholeDayRainMM    float64
	SunShare, WindKPH, GustKPH                  float64
	DaytimeHours, WholeDayRainHours, SunUpHours float64
	SunAvailable, GustAvailable                 bool
}

func (day DayMeasures) AfternoonHighC() float64 {
	high := day.TemperatureC[0]
	for _, temperature := range day.TemperatureC[1:] {
		high = max(high, temperature)
	}
	return high
}

func LocalDayBounds(date time.Time, location *time.Location) (start, end time.Time) {
	year, month, day := date.Date()
	start = time.Date(year, month, day, 0, 0, 0, 0, location)
	return start, start.AddDate(0, 0, 1)
}

// Aggregate requires sorted, unique hourly facts and a complete daytime
// window. WholeDayRainHours exposes partial boundary days rather than inventing
// a dry night. Fractional-offset zones interpolate the instantaneous states;
// interval totals are apportioned by elapsed overlap.
func Aggregate(date time.Time, location *time.Location, hours []HourlyFacts) (DayMeasures, error) {
	var result DayMeasures
	if location == nil {
		return result, fmt.Errorf("local day requires an IANA time zone")
	}
	for index, hour := range hours {
		if index > 0 && !hour.Time.After(hours[index-1].Time) {
			return result, fmt.Errorf("hourly facts must be ordered and unique")
		}
	}
	dayStart, dayEnd := LocalDayBounds(date, location)
	daytimeStart := dayStart.Add(10 * time.Hour)
	// Construct civil hours independently: the midnight-to-10 interval may cross DST.
	year, month, day := date.Date()
	daytimeStart = time.Date(year, month, day, 10, 0, 0, 0, location)
	daytimeEnd := time.Date(year, month, day, 20, 0, 0, 0, location)
	for index := range result.TemperatureC {
		instant := time.Date(year, month, day, 10+index, 0, 0, 0, location)
		state, err := stateAt(instant, hours)
		if err != nil {
			return result, err
		}
		if !finite(state.TemperatureC) || !finite(state.DewPointC) || !finite(state.WindKPH) || state.WindKPH < 0 {
			return result, fmt.Errorf("invalid state at %s", instant.Format(time.RFC3339))
		}
		result.TemperatureC[index] = state.TemperatureC
		result.DewPointC += state.DewPointC
		result.WindKPH += state.WindKPH
	}
	result.DewPointC /= 10
	result.WindKPH /= 10
	var sunshine, sunCoverage, gustCoverage float64
	for _, hour := range hours {
		if hour.IntervalMissing {
			continue
		}
		intervalStart := hour.Time.Add(-time.Hour)
		wholeOverlap := overlapHours(intervalStart, hour.Time, dayStart, dayEnd)
		daytimeOverlap := overlapHours(intervalStart, hour.Time, daytimeStart, daytimeEnd)
		if wholeOverlap <= 0 {
			continue
		}
		if !finite(hour.RainMM) || hour.RainMM < 0 {
			return result, fmt.Errorf("invalid precipitation at %s", hour.Time.Format(time.RFC3339))
		}
		result.WholeDayRainMM += hour.RainMM * wholeOverlap
		result.WholeDayRainHours += wholeOverlap
		if daytimeOverlap <= 0 {
			continue
		}
		result.DaytimeRainMM += hour.RainMM * daytimeOverlap
		result.DaytimeHours += daytimeOverlap
		if hour.SunAvailable {
			if !finite(hour.SunHours) || !finite(hour.SunUpHours) || hour.SunHours < 0 || hour.SunUpHours < 0 || hour.SunHours > hour.SunUpHours+1e-8 || hour.SunUpHours > 1+1e-8 {
				return result, fmt.Errorf("invalid sunshine interval at %s", hour.Time.Format(time.RFC3339))
			}
			sunshine += hour.SunHours * daytimeOverlap
			result.SunUpHours += hour.SunUpHours * daytimeOverlap
			sunCoverage += daytimeOverlap
		}
		if hour.GustAvailable {
			if !finite(hour.GustKPH) || hour.GustKPH < 0 {
				return result, fmt.Errorf("invalid gust at %s", hour.Time.Format(time.RFC3339))
			}
			result.GustKPH = max(result.GustKPH, hour.GustKPH)
			gustCoverage += daytimeOverlap
		}
	}
	if math.Abs(result.DaytimeHours-10) > 1e-6 {
		return result, fmt.Errorf("incomplete daytime: %.4f of 10 hours", result.DaytimeHours)
	}
	result.SunAvailable = math.Abs(sunCoverage-10) < 1e-6 && result.SunUpHours > 0
	if result.SunAvailable {
		result.SunShare = sunshine / result.SunUpHours
	}
	result.GustAvailable = math.Abs(gustCoverage-10) < 1e-6
	return result, nil
}

func stateAt(instant time.Time, hours []HourlyFacts) (HourlyFacts, error) {
	index := sort.Search(len(hours), func(index int) bool { return !hours[index].Time.Before(instant) })
	if index < len(hours) && hours[index].Time.Equal(instant) {
		return hours[index], nil
	}
	if index == 0 || index == len(hours) || hours[index].Time.Sub(hours[index-1].Time) != time.Hour {
		return HourlyFacts{}, fmt.Errorf("missing hourly state at %s", instant.Format(time.RFC3339))
	}
	before, after := hours[index-1], hours[index]
	weight := instant.Sub(before.Time).Hours()
	return HourlyFacts{TemperatureC: before.TemperatureC + (after.TemperatureC-before.TemperatureC)*weight, DewPointC: before.DewPointC + (after.DewPointC-before.DewPointC)*weight, WindKPH: before.WindKPH + (after.WindKPH-before.WindKPH)*weight}, nil
}

func overlapHours(start, end, windowStart, windowEnd time.Time) float64 {
	if start.Before(windowStart) {
		start = windowStart
	}
	if end.After(windowEnd) {
		end = windowEnd
	}
	return max(0, end.Sub(start).Hours())
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
