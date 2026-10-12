package update

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/providers/ifs"
)

func TestWorldwidePlanRequiresTheCompleteDateLineEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ifsRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	detail := forecastCoverage{provider: "ICON-EU", run: dwdRun, maxForecastHours: dwd.MaxForecastHour}

	// The 06 UTC baseline run ends two hours before UTC-12 reaches 20:00 on
	// the fourteenth shared local date. Publishing that date would make the
	// worldwide map silently partial.
	_, err := planDaysForCoverageCount(now, detail, forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC), maxForecastHours: 360}, worldwideForecastPolicy, productDays)
	if err == nil || !strings.Contains(err.Error(), "13/14 complete worldwide local days") || !strings.Contains(err.Error(), "2h0m0s short") {
		t.Fatalf("unexpected worldwide horizon result: %v", err)
	}

	// The 12 UTC run covers the exact same fourteen local dates completely.
	days, err := planDaysForCoverageCount(now, detail, forecastCoverage{provider: "IFS", run: ifsRun, maxForecastHours: 360}, worldwideForecastPolicy, productDays)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 14 {
		t.Fatalf("worldwide dates = %d, want 14", len(days))
	}
	if got := days[0].Date.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("first worldwide date = %s", got)
	}
	for index, day := range days {
		want := time.Date(2026, 7, 19+index, 0, 0, 0, 0, time.UTC)
		if !day.Date.Equal(want) {
			t.Fatalf("worldwide date %d = %s, want %s", index+1, day.Date, want)
		}
	}
	wantEnd := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	if got := globalProductWindow.end(days[13].Date); !got.Equal(wantEnd) {
		t.Fatalf("worldwide day 14 ends %s; want %s", got, wantEnd)
	}
	if got := globalProductWindow.end(days[13].Date).Sub(ifsRun); got != 356*time.Hour {
		t.Fatalf("worldwide day 14 requires T+%s, want T+356h with four hours of IFS margin", got)
	}
}

func TestWorldwidePlanKeepsACompletelyCoveredDateAfterItStartsInUTCPlus14(t *testing.T) {
	now := time.Date(2026, 7, 18, 20, 1, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	baselineRun := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	days, err := planDaysForCoverageCount(
		now,
		forecastCoverage{provider: "ICON-EU", run: dwdRun, maxForecastHours: dwd.MaxForecastHour},
		forecastCoverage{provider: "IFS", run: baselineRun, maxForecastHours: 360},
		worldwideForecastPolicy,
		productDays,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := days[0].Date.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("first worldwide date = %s; a completely covered date was discarded", got)
	}
}

func TestForecastPlanningUsesTheSelectedBaselinesHorizon(t *testing.T) {
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	detailed := forecastCoverage{provider: "ICON-EU", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: 120}
	baseline := forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: 355}
	_, err := planDaysForCoverageCount(now, detailed, baseline, worldwideForecastPolicy, productDays)
	if err == nil || !strings.Contains(err.Error(), "latest IFS run provides 13/14 complete worldwide local days") || !strings.Contains(err.Error(), "1h0m0s short") {
		t.Fatalf("unexpected selected-baseline horizon result: %v", err)
	}
}

func TestWorldwidePlanPublishesWithoutRegionalDetail(t *testing.T) {
	days, err := planDaysForCoverageCount(
		time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC),
		forecastCoverage{},
		forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: 360},
		worldwideForecastPolicy,
		productDays,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != productDays || days[0].Date.Format("2006-01-02") != "2026-07-19" {
		t.Fatalf("unexpected worldwide baseline plan: %+v", days)
	}
	for _, day := range days {
		if day.Detailed {
			t.Fatalf("worldwide baseline invented regional detail: %+v", day)
		}
	}
}

func TestWorldwideIFSStepsCoverBothDateLineExtremes(t *testing.T) {
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, err := planDaysForCoverageCount(
		time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC),
		forecastCoverage{provider: "ICON-EU", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: dwd.MaxForecastHour},
		forecastCoverage{provider: "IFS", run: run, maxForecastHours: 360},
		worldwideForecastPolicy,
		productDays,
	)
	if err != nil {
		t.Fatal(err)
	}
	available, err := ifs.ForecastSteps(ifs.Run{Time: run})
	if err != nil {
		t.Fatal(err)
	}
	steps := stepsForDays(run, days, globalOutlookDecodeWindow, available)
	if steps[0] != 6 || steps[len(steps)-1] != 360 {
		t.Fatalf("worldwide IFS steps = %d..%d; want predecessor 6 through endpoint 360", steps[0], steps[len(steps)-1])
	}
}

func TestSelectStepsIncludesOnePrecedingSample(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	got := selectSteps(run, run.Add(10*time.Hour), run.Add(14*time.Hour), []int{0, 6, 12, 18})
	if !reflect.DeepEqual(got, []int{6, 12}) {
		t.Fatalf("steps = %v", got)
	}
}
