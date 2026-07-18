package update

import (
	"math"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

func TestRealForecastCoverageGatesRejectPatchwork(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	fields := []pipeline.FieldCoverage{{Field: pipeline.Rain, Frames: 1, WorstFrame: pipeline.CoverageCount{Finite: 999, Support: 1000}, WorstBlock: pipeline.CoverageCount{Finite: 255, Support: 256}}}
	if _, err := enforceFieldCoveragePolicy(date, "DWD ICON-EU", pipeline.Series{}, fields); err == nil || !strings.Contains(err.Error(), "worst-frame coverage 99.90%") {
		t.Fatalf("single-cell field hole was not rejected clearly: %v", err)
	}
	fields[0].WorstFrame.Finite = 1000
	if _, err := enforceFieldCoveragePolicy(date, "DWD ICON-EU", pipeline.Series{}, fields); err != nil {
		t.Fatalf("complete field was rejected: %v", err)
	}
	if err := requireForecastCoverage(date, "DWD ICON-EU", pipeline.ForecastCoverage{Global: pipeline.CoverageCount{Finite: 999, Support: 1000}, WorstBlock: pipeline.CoverageCount{Finite: 255, Support: 256}}); err == nil || !strings.Contains(err.Error(), "publishable coverage 99.90%") {
		t.Fatalf("single-cell forecast hole was not rejected clearly: %v", err)
	}
	if err := requireForecastCoverage(date, "DWD ICON-EU", pipeline.ForecastCoverage{Global: pipeline.CoverageCount{Finite: 1000, Support: 1000}, WorstBlock: pipeline.CoverageCount{Finite: 256, Support: 256}}); err != nil {
		t.Fatalf("complete forecast was rejected: %v", err)
	}
}

func TestOptionalSolarCoverageKeepsSparseEvidenceOrOmitsWholePair(t *testing.T) {
	coverage := func(finite int) []pipeline.FieldCoverage {
		return []pipeline.FieldCoverage{
			{Field: pipeline.SolarDirect, WorstFrame: pipeline.CoverageCount{Finite: finite, Support: 1000}},
			{Field: pipeline.SolarDiffuse, WorstFrame: pipeline.CoverageCount{Finite: 1000, Support: 1000}},
		}
	}
	series := func() pipeline.Series {
		return pipeline.Series{
			pipeline.SolarDirect:  {{Field: pipeline.SolarDirect}},
			pipeline.SolarDiffuse: {{Field: pipeline.SolarDiffuse}},
		}
	}

	sparse := series()
	omitted, err := applyOptionalSolarCoveragePolicy(sparse, coverage(999))
	if err != nil || omitted || len(sparse[pipeline.SolarDirect]) == 0 || len(sparse[pipeline.SolarDiffuse]) == 0 {
		t.Fatalf("99.9%% optional solar was not retained as sparse evidence: omitted=%t err=%v", omitted, err)
	}
	if _, err := enforceFieldCoveragePolicy(time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), "DWD ICON-EU", sparse, coverage(999)); err != nil {
		t.Fatalf("sparse optional solar blocked exact required-field coverage: %v", err)
	}

	widespread := series()
	omitted, err = applyOptionalSolarCoveragePolicy(widespread, coverage(998))
	if err != nil || !omitted || len(widespread[pipeline.SolarDirect]) != 0 || len(widespread[pipeline.SolarDiffuse]) != 0 {
		t.Fatalf("sub-99.9%% solar pair was not omitted atomically: omitted=%t err=%v", omitted, err)
	}

	partial := series()
	delete(partial, pipeline.SolarDiffuse)
	if _, err := applyOptionalSolarCoveragePolicy(partial, coverage(1000)); err == nil {
		t.Fatal("lone direct solar component was accepted by coverage policy")
	}
}

func TestMeasuredFinalCoverageRejectsOneMissingSupportedCell(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	support := pipeline.FloatGrid{Spec: spec, Values: []float32{1, 1}}
	metrics := []weather.DailyMetrics{{Valid: true}, {Valid: false}}
	coverage, err := pipeline.MeasureForecastCoverage(spec, metrics, support)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(coverage.Fraction()-.5) > 1e-9 {
		t.Fatalf("measured final coverage = %+v", coverage)
	}
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	if err := requireForecastCoverage(date, "DWD ICON-EU", coverage); err == nil || !strings.Contains(err.Error(), "every expected provider-support cell is required") {
		t.Fatalf("measured one-cell final hole was not rejected: %v", err)
	}
}

func TestICONSupportUsesDocumentedDomainCellCentres(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: -22, South: 29, North: 72}, Rows: 43, Cols: 4}
	land := pipeline.FloatGrid{Spec: spec, Values: make([]float32, 43*4)}
	for index := range land.Values {
		land.Values[index] = 1
	}
	support, err := expectedForecastSupport(land, true)
	if err != nil {
		t.Fatal(err)
	}
	// Cell centres west of 23.5 W and north of 70.5 N are outside ICON-EU;
	// the adjacent centres are supported. This guards accidental narrowing.
	if support.Values[1] != 0 || support.Values[4] != 0 || support.Values[5] != 1 {
		t.Fatalf("documented ICON-EU boundary support changed: first rows %v", support.Values[:8])
	}
}
