package dwd

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadLocalDaytimeGridUsesEachCellTimezone(t *testing.T) {
	runStartedAt := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	horizons := []Horizon{{ID: "2026-05-05", LocalDate: "2026-05-05", Label: "Tue 5 May"}}
	timezoneAt := func(lon, lat float64) (string, *time.Location, error) {
		if lon < 10 {
			return "UTC", time.UTC, nil
		}
		return "UTC+2", time.FixedZone("UTC+2", 2*60*60), nil
	}
	reader := func(_ context.Context, path string) ([]GridValue, error) {
		field, step := testFieldAndStep(t, path)
		values := []GridValue{{Lat: 50, Lon: 0}, {Lat: 50, Lon: 20}}
		for i := range values {
			values[i].Value = testFieldValue(field, step)
		}
		return values, nil
	}

	grid, err := loadLocalDaytimeGrid(context.Background(), "", runStartedAt, []int{8, 10, 18, 20}, horizons, timezoneAt, reader)
	if err != nil {
		t.Fatal(err)
	}

	utcCell, ok := grid.InputAt("2026-05-05", 0, 50)
	if !ok {
		t.Fatal("expected UTC cell")
	}
	plusTwoCell, ok := grid.InputAt("2026-05-05", 20, 50)
	if !ok {
		t.Fatal("expected UTC+2 cell")
	}
	if utcCell.TempC != 16 {
		t.Fatalf("UTC cell temp = %.1f, want 16.0 from steps 10,18,20", utcCell.TempC)
	}
	if plusTwoCell.TempC != 12 {
		t.Fatalf("UTC+2 cell temp = %.1f, want 12.0 from steps 8,10,18", plusTwoCell.TempC)
	}
	if utcCell.RainMM != 10 || plusTwoCell.RainMM != 10 {
		t.Fatalf("rain delta = %.1f / %.1f, want 10 / 10", utcCell.RainMM, plusTwoCell.RainMM)
	}
}

func TestForecastStepsUseHourlyThenThreeHourlyCadence(t *testing.T) {
	steps := ForecastSteps()
	if steps[0] != 0 || steps[78] != 78 || steps[79] != 81 || steps[len(steps)-1] != 120 {
		t.Fatalf("unexpected steps around cadence switch: first=%d step78=%d step79=%d last=%d", steps[0], steps[78], steps[79], steps[len(steps)-1])
	}
}

func TestHorizonDatesUseConcreteDateLabels(t *testing.T) {
	horizons := HorizonDates(time.Date(2026, 5, 5, 14, 30, 0, 0, time.UTC), 2)
	if horizons[0].ID != "2026-05-05" || horizons[0].Label != "Tue 5 May" {
		t.Fatalf("first horizon = %#v, want concrete Tue 5 May label", horizons[0])
	}
	if horizons[1].ID != "2026-05-06" || horizons[1].Label != "Wed 6 May" {
		t.Fatalf("second horizon = %#v, want concrete Wed 6 May label", horizons[1])
	}
}

func TestFullLocalHorizonDatesSkipRunDateWhenRunCannotCoverDaytimeStart(t *testing.T) {
	horizons := FullLocalHorizonDates(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC), 2)
	if horizons[0].ID != "2026-05-06" || horizons[0].Label != "Wed 6 May" {
		t.Fatalf("first full horizon = %#v, want next day", horizons[0])
	}
}

func TestCandidateRunsRoundsDownToLatestSixHourRun(t *testing.T) {
	runs := CandidateRuns(time.Date(2026, 5, 5, 14, 30, 0, 0, time.UTC))
	if runs[0].ID != "2026050512" || runs[1].ID != "2026050506" {
		t.Fatalf("candidate runs = %s, %s; want 2026050512, 2026050506", runs[0].ID, runs[1].ID)
	}
}

func testFieldAndStep(t *testing.T, path string) (string, int) {
	t.Helper()
	name := filepath.Base(path)
	parts := strings.Split(strings.TrimSuffix(name, ".grib2"), "_")
	if len(parts) < 2 {
		t.Fatalf("bad test path %s", path)
	}
	step, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(parts[:len(parts)-1], "_"), step
}

func testFieldValue(field string, step int) float64 {
	switch field {
	case "t_2m":
		return 273.15 + float64(step)
	case "relhum_2m":
		return 50
	case "tot_prec":
		return float64(step)
	case "clct":
		return 20
	case "u_10m":
		return 0
	case "v_10m":
		return 0
	default:
		return 0
	}
}
