package dwd

import (
	"strings"
	"testing"
	"time"
)

func TestForecastStepsUseHourlyThenThreeHourlyCadence(t *testing.T) {
	steps := ForecastSteps()
	if len(steps) != 93 {
		t.Fatalf("len(ForecastSteps()) = %d, want 93", len(steps))
	}
	for hour := 0; hour <= 78; hour++ {
		if steps[hour] != hour {
			t.Fatalf("steps[%d] = %d, want %d", hour, steps[hour], hour)
		}
	}
	wantTail := []int{81, 84, 87, 90, 93, 96, 99, 102, 105, 108, 111, 114, 117, 120}
	for index, want := range wantTail {
		if got := steps[79+index]; got != want {
			t.Fatalf("tail step %d = %d, want %d", index, got, want)
		}
	}
}

func TestCandidateRunsAreNewestFirstAtSixHourCycles(t *testing.T) {
	runs, err := CandidateRuns(time.Date(2026, 7, 18, 17, 42, 0, 0, time.FixedZone("test", 2*60*60)), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026071812", "2026071806", "2026071800"}
	for index := range want {
		if got := runs[index].ID(); got != want[index] {
			t.Fatalf("run %d = %s, want %s", index, got, want[index])
		}
	}
}

func TestFileForMapsRegularLatLonProductURL(t *testing.T) {
	run := Run{StartedAt: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	field, err := FieldByName("dewpoint_2m")
	if err != nil {
		t.Fatal(err)
	}
	file, err := FileFor(DefaultBaseURL, run, field, 81)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://opendata.dwd.de/weather/nwp/icon-eu/grib/06/td_2m/icon-eu_europe_regular-lat-lon_single-level_2026071806_081_TD_2M.grib2.bz2"
	if file.URL != want {
		t.Fatalf("FileFor URL = %s, want %s", file.URL, want)
	}
}

func TestFileForRejectsUnpublishedStep(t *testing.T) {
	run := Run{StartedAt: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	field, _ := FieldByName("temperature_2m")
	if _, err := FileFor(DefaultBaseURL, run, field, 80); err == nil {
		t.Fatal("expected unpublished +80 hour step to fail")
	}
}

func TestFileForUsesCanonicalCatalogueMapping(t *testing.T) {
	run := Run{StartedAt: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	file, err := FileFor(DefaultBaseURL, run, Field{
		Name:      "temperature_2m",
		Directory: "attacker-controlled",
		FileToken: "WRONG",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.URL, "/t_2m/") || !strings.HasSuffix(file.URL, "_T_2M.grib2.bz2") {
		t.Fatalf("FileFor used caller-supplied field mapping: %s", file.URL)
	}
}
