package update

import (
	"fmt"
	"time"

	"nice-weather-finder/internal/providers/dwd"
)

const productDays = 14

// utcDayWindow is the complete UTC envelope needed to evaluate one local
// 10:00-20:00 product day across a geographic timezone footprint. Offsets are
// relative to midnight on the labelled local calendar date and may cross that
// date's UTC boundaries.
type utcDayWindow struct {
	startOffset time.Duration
	endOffset   time.Duration
}

var (
	// ICON-EU refinement spans UTC-1 through UTC+4 for supported land cells.
	europeProductWindow = utcDayWindow{startOffset: 6 * time.Hour, endOffset: 21 * time.Hour}
	// Worldwide civil time spans UTC-12 through UTC+14. Therefore a labelled
	// local day begins in UTC on the previous date and ends on the next date.
	globalProductWindow = utcDayWindow{startOffset: -4 * time.Hour, endOffset: 32 * time.Hour}

	// Existing decode windows intentionally retain provider-specific padding.
	// The staging layer also retains one preceding frame per field.
	europeDetailedDecodeWindow = utcDayWindow{startOffset: 3 * time.Hour, endOffset: 21 * time.Hour}
	// Baseline instant fields describe the interval ending at each sample.
	// Keep up to six hours after the latest worldwide local-day boundary so the
	// final partial interval is available to the aggregator.
	globalOutlookDecodeWindow = utcDayWindow{startOffset: -4 * time.Hour, endOffset: 38 * time.Hour}
)

func (window utcDayWindow) validate() error {
	if window.startOffset >= window.endOffset {
		return fmt.Errorf("UTC day window must end after it starts")
	}
	if window.startOffset < -24*time.Hour || window.endOffset > 48*time.Hour {
		return fmt.Errorf("UTC day window exceeds the adjacent calendar dates")
	}
	return nil
}

func (window utcDayWindow) start(date time.Time) time.Time {
	return productDate(date).Add(window.startOffset)
}

func (window utcDayWindow) end(date time.Time) time.Time {
	return productDate(date).Add(window.endOffset)
}

func productDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

type forecastPlanPolicy struct {
	name                 string
	productWindow        utcDayWindow
	detailedWindow       utcDayWindow
	currentDateCutoffUTC *time.Duration
	firstDaySource       firstDaySource
}

type firstDaySource uint8

const (
	firstDayUsesDetail firstDaySource = iota
	firstDayUsesBaseline
)

// A worldwide date has no single useful wall-clock expiry: it begins in
// UTC+14 before it begins in UTC-12. The selected baseline run therefore owns
// the first date. This keeps a completely covered date available after its
// easternmost window has begun instead of creating a daily publication gap.
var worldwideForecastPolicy = forecastPlanPolicy{
	name:           "worldwide",
	productWindow:  globalProductWindow,
	detailedWindow: europeProductWindow,
	firstDaySource: firstDayUsesBaseline,
}

type forecastCoverage struct {
	provider         string
	run              time.Time
	maxForecastHours int
}

func (coverage forecastCoverage) validate(role string) error {
	if coverage.provider == "" {
		return fmt.Errorf("%s forecast provider is required", role)
	}
	if coverage.run.IsZero() {
		return fmt.Errorf("%s %s run is required", coverage.provider, role)
	}
	if coverage.maxForecastHours <= 0 {
		return fmt.Errorf("%s %s forecast horizon must be positive", coverage.provider, role)
	}
	return nil
}

func (coverage forecastCoverage) empty() bool {
	return coverage.provider == "" && coverage.run.IsZero() && coverage.maxForecastHours == 0
}

func (coverage forecastCoverage) availableUntil() time.Time {
	return coverage.run.UTC().Add(time.Duration(coverage.maxForecastHours) * time.Hour)
}

type dayPlan struct {
	Date     time.Time
	Detailed bool
}

func planDaysForCoverage(now time.Time, detailed, baseline forecastCoverage, policy forecastPlanPolicy) ([]dayPlan, error) {
	return planDaysForCoverageCount(now, detailed, baseline, policy, productDays)
}

func planDaysForCoverageCount(now time.Time, detailed, baseline forecastCoverage, policy forecastPlanPolicy, dayCount int) ([]dayPlan, error) {
	now = now.UTC()
	detailed.run = detailed.run.UTC()
	baseline.run = baseline.run.UTC()
	if policy.name == "" {
		return nil, fmt.Errorf("forecast planning policy name is required")
	}
	if dayCount <= 0 {
		return nil, fmt.Errorf("forecast day count must be positive")
	}
	if err := policy.productWindow.validate(); err != nil {
		return nil, fmt.Errorf("%s product window: %w", policy.name, err)
	}
	if err := policy.detailedWindow.validate(); err != nil {
		return nil, fmt.Errorf("%s detailed window: %w", policy.name, err)
	}
	if policy.firstDaySource != firstDayUsesDetail && policy.firstDaySource != firstDayUsesBaseline {
		return nil, fmt.Errorf("%s first-day source is invalid", policy.name)
	}
	detailRequired := policy.firstDaySource == firstDayUsesDetail
	if detailRequired || !detailed.empty() {
		if err := detailed.validate("detail"); err != nil {
			return nil, err
		}
	}
	if err := baseline.validate("baseline"); err != nil {
		return nil, err
	}
	first := productDate(now)
	if policy.currentDateCutoffUTC != nil {
		cutoff := *policy.currentDateCutoffUTC
		if cutoff < 0 || cutoff >= 24*time.Hour {
			return nil, fmt.Errorf("%s current-date cutoff must be within its UTC date", policy.name)
		}
		if now.After(first.Add(cutoff)) {
			first = first.AddDate(0, 0, 1)
		}
	}
	for (policy.firstDaySource == firstDayUsesBaseline && policy.productWindow.start(first).Before(baseline.run)) ||
		(detailRequired && policy.detailedWindow.start(first).Before(detailed.run)) {
		first = first.AddDate(0, 0, 1)
	}
	requiredUntil := policy.productWindow.end(first.AddDate(0, 0, dayCount-1))
	availableUntil := baseline.availableUntil()
	if requiredUntil.After(availableUntil) {
		completeDays := 0
		for index := 0; index < dayCount; index++ {
			if policy.productWindow.end(first.AddDate(0, 0, index)).After(availableUntil) {
				break
			}
			completeDays++
		}
		return nil, fmt.Errorf(
			"latest %s run provides %d/%d complete %s local days; UTC coverage ends %s, but day %d requires %s (%s short)",
			baseline.provider, completeDays, dayCount, policy.name, availableUntil.Format(time.RFC3339), dayCount, requiredUntil.Format(time.RFC3339), requiredUntil.Sub(availableUntil),
		)
	}
	days := make([]dayPlan, dayCount)
	for index := range days {
		date := first.AddDate(0, 0, index)
		days[index].Date = date
		if !detailed.empty() {
			days[index].Detailed = !policy.detailedWindow.start(date).Before(detailed.run) &&
				!policy.detailedWindow.end(date).After(detailed.availableUntil())
		}
	}
	if detailRequired && !days[0].Detailed {
		return nil, fmt.Errorf("latest %s run cannot cover the first complete product day", detailed.provider)
	}
	return days, nil
}

func stepsForDWD(run time.Time, days []dayPlan) []int {
	return stepsForProviderWindow(run, days, true, europeDetailedDecodeWindow, dwd.ForecastSteps())
}

func stepsForProviderWindow(run time.Time, days []dayPlan, detailed bool, window utcDayWindow, available []int) []int {
	selected := make([]dayPlan, 0, len(days))
	for _, day := range days {
		if day.Detailed == detailed {
			selected = append(selected, day)
		}
	}
	return stepsForDays(run, selected, window, available)
}

// stepsForDays selects source frames for every supplied date. Worldwide
// baselines use this path because regional detail refines a date without
// replacing the baseline required everywhere else on that date.
func stepsForDays(run time.Time, days []dayPlan, window utcDayWindow, available []int) []int {
	var first, last time.Time
	for _, day := range days {
		if first.IsZero() {
			first = window.start(day.Date)
		}
		last = window.end(day.Date)
	}
	return selectSteps(run, first, last, available)
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
