package update

import (
	"fmt"
	"time"

	"nice-weather-finder/internal/providers/aifs"
	"nice-weather-finder/internal/providers/dwd"
)

const productDays = 14

type dayPlan struct {
	Date     time.Time
	Detailed bool
}

func planDays(now, dwdRun, aifsRun time.Time) ([]dayPlan, error) {
	now, dwdRun, aifsRun = now.UTC(), dwdRun.UTC(), aifsRun.UTC()
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	// Across the product bounds, the local 10:00–20:00 window can start as
	// early as 06:00 UTC and end as late as 21:00 UTC.
	if first.Add(6*time.Hour).Before(dwdRun) || now.After(first.Add(20*time.Hour)) {
		first = first.AddDate(0, 0, 1)
	}
	last := first.AddDate(0, 0, productDays-1).Add(21 * time.Hour)
	if last.After(aifsRun.Add(360 * time.Hour)) {
		return nil, fmt.Errorf("latest AIFS run cannot cover 14 complete product days")
	}
	days := make([]dayPlan, productDays)
	for index := range days {
		date := first.AddDate(0, 0, index)
		days[index] = dayPlan{
			Date: date,
			Detailed: !date.Add(6*time.Hour).Before(dwdRun) &&
				!date.Add(21*time.Hour).After(dwdRun.Add(dwd.MaxForecastHour*time.Hour)),
		}
	}
	if !days[0].Detailed {
		return nil, fmt.Errorf("latest ICON-EU run cannot cover the first complete product day")
	}
	return days, nil
}

func stepsForDWD(run time.Time, days []dayPlan) []int {
	var first, last time.Time
	for _, day := range days {
		if !day.Detailed {
			continue
		}
		if first.IsZero() {
			first = day.Date.Add(3 * time.Hour)
		}
		last = day.Date.Add(21 * time.Hour)
	}
	return selectSteps(run, first, last, dwd.ForecastSteps())
}

func stepsForAIFS(run time.Time, days []dayPlan) []int {
	var first, last time.Time
	for _, day := range days {
		if day.Detailed {
			continue
		}
		if first.IsZero() {
			first = day.Date
		}
		last = day.Date.Add(24 * time.Hour)
	}
	return selectSteps(run, first, last, aifs.ForecastSteps())
}

func selectSteps(run, first, last time.Time, available []int) []int {
	if first.IsZero() || last.IsZero() {
		return nil
	}
	result := make([]int, 0, len(available))
	for _, step := range available {
		valid := run.Add(time.Duration(step) * time.Hour)
		if !valid.Before(first) && !valid.After(last) {
			result = append(result, step)
		}
	}
	// One preceding sample is required to turn point values and cumulative
	// accumulations into an interval at the first selected valid time.
	if len(result) > 0 {
		firstStep := result[0]
		for index := len(available) - 1; index >= 0; index-- {
			if available[index] < firstStep {
				result = append([]int{available[index]}, result...)
				break
			}
		}
	}
	return result
}
