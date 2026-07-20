package update

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/providers/ifs"
)

func TestPlanDaysUsesOnlyCompleteWindowsAndSwitchesProviderHonestly(t *testing.T) {
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ifsRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, err := planDays(now, dwdRun, ifsRun)
	if err != nil {
		t.Fatal(err)
	}
	if got := days[0].Date.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("first date = %s", got)
	}
	detailed := 0
	for _, day := range days {
		if day.Detailed {
			detailed++
		}
	}
	if detailed != 4 {
		t.Fatalf("detailed days = %d, want 4", detailed)
	}
}

func TestPlanDaysRejectsShortIFSRun(t *testing.T) {
	_, err := planDays(
		time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC),
	)
	if err == nil || !strings.Contains(err.Error(), "00 or 12") {
		t.Fatalf("short IFS run error = %v", err)
	}
}

func TestWorldwidePlanRequiresTheCompleteDateLineEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	detail := forecastCoverage{provider: "ICON-EU", run: dwdRun, maxForecastHours: dwd.MaxForecastHour}

	// The 06 UTC baseline run ends two hours before UTC-12 reaches 20:00 on
	// the fourteenth shared local date. Publishing that date would make the
	// worldwide map silently partial.
	_, err := planDaysForCoverage(now, detail, forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC), maxForecastHours: 360}, worldwideForecastPolicy)
	if err == nil || !strings.Contains(err.Error(), "13/14 complete worldwide local days") || !strings.Contains(err.Error(), "2h0m0s short") {
		t.Fatalf("unexpected worldwide horizon result: %v", err)
	}

	// The 12 UTC run covers the exact same fourteen local dates completely.
	days, err := planDaysForCoverage(now, detail, forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: 360}, worldwideForecastPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if got := days[0].Date.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("first worldwide date = %s", got)
	}
	wantEnd := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	if got := globalProductWindow.end(days[13].Date); !got.Equal(wantEnd) {
		t.Fatalf("worldwide day 14 ends %s; want %s", got, wantEnd)
	}
}

func TestWorldwidePlanKeepsACompletelyCoveredDateAfterItStartsInUTCPlus14(t *testing.T) {
	now := time.Date(2026, 7, 18, 20, 1, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	baselineRun := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	days, err := planDaysForCoverage(
		now,
		forecastCoverage{provider: "ICON-EU", run: dwdRun, maxForecastHours: dwd.MaxForecastHour},
		forecastCoverage{provider: "IFS", run: baselineRun, maxForecastHours: 360},
		worldwideForecastPolicy,
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
	_, err := planDaysForCoverage(now, detailed, baseline, worldwideForecastPolicy)
	if err == nil || !strings.Contains(err.Error(), "latest IFS run provides 13/14 complete worldwide local days") || !strings.Contains(err.Error(), "1h0m0s short") {
		t.Fatalf("unexpected selected-baseline horizon result: %v", err)
	}
}

func TestWorldwidePlanPublishesWithoutRegionalDetail(t *testing.T) {
	days, err := planDaysForCoverage(
		time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC),
		forecastCoverage{},
		forecastCoverage{provider: "IFS", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: 360},
		worldwideForecastPolicy,
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
	days, err := planDaysForCoverage(
		time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC),
		forecastCoverage{provider: "ICON-EU", run: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), maxForecastHours: dwd.MaxForecastHour},
		forecastCoverage{provider: "IFS", run: run, maxForecastHours: 360},
		worldwideForecastPolicy,
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

func TestEuropePlanningRemainsEquivalentToThePreviousPolicy(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for nowHour := 0; nowHour < 24; nowHour++ {
		for _, dwdHour := range []int{0, 6, 12, 18} {
			for _, ifsHour := range []int{0, 12} {
				now := date.Add(time.Duration(nowHour) * time.Hour)
				dwdRun := date.Add(time.Duration(dwdHour) * time.Hour)
				ifsRun := date.Add(time.Duration(ifsHour) * time.Hour)
				got, gotErr := planDays(now, dwdRun, ifsRun)
				want, wantErr := legacyEuropePlan(now, dwdRun, ifsRun)
				if (gotErr != nil) != (wantErr != nil) || !reflect.DeepEqual(got, want) {
					t.Fatalf("now=%s ICON=%s IFS=%s: got %+v, %v; want %+v, %v", now, dwdRun, ifsRun, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

func legacyEuropePlan(now, dwdRun, ifsRun time.Time) ([]dayPlan, error) {
	now, dwdRun, ifsRun = now.UTC(), dwdRun.UTC(), ifsRun.UTC()
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if first.Add(6*time.Hour).Before(dwdRun) || now.After(first.Add(20*time.Hour)) {
		first = first.AddDate(0, 0, 1)
	}
	last := first.AddDate(0, 0, productDays-1).Add(21 * time.Hour)
	if last.After(ifsRun.Add(360 * time.Hour)) {
		return nil, fmt.Errorf("legacy IFS horizon")
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
		return nil, fmt.Errorf("legacy ICON horizon")
	}
	return days, nil
}

func TestSelectStepsIncludesOnePrecedingSample(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	got := selectSteps(run, run.Add(10*time.Hour), run.Add(14*time.Hour), []int{0, 6, 12, 18})
	if !reflect.DeepEqual(got, []int{6, 12}) {
		t.Fatalf("steps = %v", got)
	}
}
