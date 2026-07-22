package era5canary

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEvaluateUsesCanonicalRangeSummaryAndRequiresAnnualCycle(t *testing.T) {
	report, err := Evaluate(fixtureInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if report.ScorePolicy != "canonical-weather-score-current-unmodified" || len(report.Days) != 425 || len(report.Scenarios) != 14 || len(report.Seam) != 1 {
		t.Fatalf("unexpected report: policy=%q days=%d scenarios=%d seam=%d", report.ScorePolicy, len(report.Days), len(report.Scenarios), len(report.Seam))
	}
	malagaMonths := 0
	for _, summary := range report.Scenarios {
		if summary.CellID == "malaga" {
			malagaMonths++
			if summary.Physical.RelativeHumidityPercent.Mean != 60 {
				t.Fatalf("Malaga humidity range = %+v", summary.Physical.RelativeHumidityPercent)
			}
		}
	}
	if malagaMonths != 12 || report.Seam[0].MaximumBoundaryTemperatureDeltaC != 1 || report.Seam[0].MaximumBoundaryComfortScoreDelta == 0 {
		t.Fatalf("annual or seam evidence missing: months=%d seam=%+v", malagaMonths, report.Seam[0])
	}
	if report.HumidityRepair.SaturationClampedHours != 1 || report.HumidityRepair.MaximumDewPointExcessC != MaximumDewPointExcessC {
		t.Fatalf("humidity repair evidence = %+v", report.HumidityRepair)
	}
}

func TestDecodeAndEvaluationRejectUnboundedOrIncompleteEvidence(t *testing.T) {
	input := fixtureInput(t)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(append(encoded, []byte(`{"extra":true}`)...))); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaximumInputBytes+1))); err == nil {
		t.Fatal("oversized JSON was accepted")
	}
	input.Days = input.Days[1:]
	if _, err := Evaluate(input); err == nil {
		t.Fatal("incomplete daily coverage was accepted")
	}
}

func TestEvaluateRejectsMissingBoundaryAndWrongSourceSemantics(t *testing.T) {
	input := fixtureInput(t)
	input.Cells[1].GridLongitude = 22.75
	for index := range input.Days {
		if input.Days[index].CellID == input.Cells[1].ID {
			input.Days[index].Metrics.Longitude = 22.75
		}
	}
	if _, err := Evaluate(input); err == nil {
		t.Fatal("seam scenario without the declared boundary was accepted")
	}
	input = fixtureInput(t)
	input.Source.Variables[2].Semantics = "instant"
	if _, err := Evaluate(input); err == nil {
		t.Fatal("incorrect source semantics were accepted")
	}
	input = fixtureInput(t)
	input.Days[0].Metrics.MaximumDewPointExcessC = MaximumDewPointExcessC + .000001
	if _, err := Evaluate(input); err == nil {
		t.Fatal("humidity correction above the declared tolerance was accepted")
	}
}

func fixtureInput(t *testing.T) Input {
	t.Helper()
	cells := []Cell{
		{ID: "chad-22.25", Scenario: "chad-seam", Timezone: "Africa/Ndjamena", GridLatitude: 15, GridLongitude: 22.25, Months: []int{4}},
		{ID: "chad-22.50", Scenario: "chad-seam", Timezone: "Africa/Ndjamena", GridLatitude: 15, GridLongitude: 22.5, Months: []int{4}},
		{ID: "malaga", Scenario: "malaga-annual-cycle", Timezone: "Europe/Madrid", GridLatitude: 36.75, GridLongitude: -4.5, Months: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
	}
	var days []Day
	for _, cell := range cells {
		for _, month := range cell.Months {
			last := time.Date(2025, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for day := 1; day <= last; day++ {
				temperature := 20.0
				if cell.ID == "chad-22.50" {
					temperature = 21
				}
				evidence := DailyEvidence{
					Date: time.Date(2025, time.Month(month), day, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Latitude: cell.GridLatitude, Longitude: cell.GridLongitude,
					ComfortableTempC: temperature, MinTempC: temperature - 2, MaxTempC: temperature + 2,
					RainMM: 0, WetTimeFraction: 0, SolarEnergyMJm2: 18, CloudCoverPercent: 20, WindSpeedKPH: 8, DewPointC: 10, RelativeHumidityPercent: 60,
				}
				if cell.ID == "malaga" && month == 1 && day == 1 {
					evidence.HumiditySaturationClampedHours = 1
					evidence.MaximumDewPointExcessC = MaximumDewPointExcessC
				}
				days = append(days, Day{CellID: cell.ID, Metrics: evidence})
			}
		}
	}
	return Input{
		SchemaVersion: 1,
		Source: Source{
			Authority: "Copernicus Climate Change Service", Dataset: "ERA5 hourly data on single levels", Repository: "s3://earthmover-icechunk-era5/icechunkV2",
			SnapshotID: PinnedSnapshotID, Transport: "Earthmover Icechunk public mirror", GridDegrees: .25, NativeResolutionKM: 30,
			Variables: []Variable{
				{Name: "t2m", Units: "K", Semantics: "instant"}, {Name: "d2m", Units: "K", Semantics: "instant"},
				{Name: "tp", Units: "m", Semantics: "one-hour accumulation ending at valid_time"}, {Name: "ssrd", Units: "J m**-2", Semantics: "one-hour accumulation ending at valid_time"},
				{Name: "tcc", Units: "0-1", Semantics: "instant"}, {Name: "u10", Units: "m s**-1", Semantics: "instant"}, {Name: "v10", Units: "m s**-1", Semantics: "instant"},
			},
		},
		Window: Window{ID: "civil-local-10-20", StartLocalHour: 10, EndLocalHour: 20, HourlyIntervalsPerDay: 10, WetHourlyThresholdMM: .1},
		Year:   2025, Retrieval: Retrieval{RequestCount: 1, GetCount: 1, ResponseBodyBytes: 1, MaximumResponseBodyBytes: 1, LogicalArrayReads: 1, DecodedArrayBytes: 1, PeakProcessRSSBytes: 1, WallTimeMilliseconds: 1},
		Cells: cells, Days: days,
	}
}
