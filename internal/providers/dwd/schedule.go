package dwd

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

const MaxForecastHour = 120

type Run struct {
	StartedAt time.Time
}

func (run Run) ID() string {
	return run.StartedAt.UTC().Format("2006010215")
}

func (run Run) Hour() string {
	return run.StartedAt.UTC().Format("15")
}

// ForecastSteps matches the files DWD publishes for ICON-EU: hourly through
// +78 hours, then every three hours through +120 hours.
func ForecastSteps() []int {
	steps := make([]int, 0, 93)
	for hour := 0; hour <= 78; hour++ {
		steps = append(steps, hour)
	}
	for hour := 81; hour <= MaxForecastHour; hour += 3 {
		steps = append(steps, hour)
	}
	return steps
}

func CandidateRuns(now time.Time, count int) ([]Run, error) {
	if count < 1 {
		return nil, fmt.Errorf("candidate run count must be positive")
	}
	now = now.UTC()
	latest := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()/6*6, 0, 0, 0, time.UTC)
	runs := make([]Run, count)
	for index := range runs {
		runs[index] = Run{StartedAt: latest.Add(-time.Duration(index*6) * time.Hour)}
	}
	return runs, nil
}

type File struct {
	Run   Run
	Field Field
	Step  int
	URL   string
}

func FileFor(baseURL string, run Run, field Field, step int) (File, error) {
	if step < 0 || step > MaxForecastHour {
		return File{}, fmt.Errorf("DWD ICON-EU forecast step %d is outside 0..%d", step, MaxForecastHour)
	}
	if !isForecastStep(step) {
		return File{}, fmt.Errorf("DWD ICON-EU does not publish forecast step %d", step)
	}
	if run.StartedAt.IsZero() || run.StartedAt.UTC().Minute() != 0 || run.StartedAt.UTC().Second() != 0 || run.StartedAt.UTC().Hour()%6 != 0 {
		return File{}, fmt.Errorf("DWD ICON-EU run must start on a 00, 06, 12, or 18 UTC cycle")
	}
	canonicalField, err := FieldByName(field.Name)
	if err != nil {
		return File{}, err
	}
	field = canonicalField
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return File{}, fmt.Errorf("invalid DWD base URL %q", baseURL)
	}
	filename := fmt.Sprintf(
		"icon-eu_europe_regular-lat-lon_single-level_%s_%03d_%s.grib2.bz2",
		run.ID(), step, field.FileToken,
	)
	parsed.Path = path.Join(parsed.Path, run.Hour(), field.Directory, filename)
	return File{Run: run, Field: field, Step: step, URL: parsed.String()}, nil
}

func isForecastStep(step int) bool {
	return step <= 78 || (step >= 81 && step%3 == 0)
}
