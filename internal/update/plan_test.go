package update

import (
	"reflect"
	"testing"
	"time"
)

func TestPlanDaysUsesOnlyCompleteWindowsAndSwitchesProviderHonestly(t *testing.T) {
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	dwdRun := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	aifsRun := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)
	days, err := planDays(now, dwdRun, aifsRun)
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

func TestSelectStepsIncludesOnePrecedingSample(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	got := selectSteps(run, run.Add(10*time.Hour), run.Add(14*time.Hour), []int{0, 6, 12, 18})
	if !reflect.DeepEqual(got, []int{6, 12}) {
		t.Fatalf("steps = %v", got)
	}
}
