package update

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"nice-weather-finder/internal/pipeline"
)

// TestLiveDWD2026071800DayTwoSolar proves the sparse optional-evidence policy
// against the frozen source messages that exposed the three Iceland outliers.
// Source GRIBs and the pinned Natural Earth cache are intentionally external.
func TestLiveDWD2026071800DayTwoSolar(t *testing.T) {
	directory := os.Getenv("NICE_WEATHER_LIVE_DWD_SOLAR_DIR")
	landCache := os.Getenv("NICE_WEATHER_LIVE_LAND_CACHE")
	if directory == "" || landCache == "" {
		t.Skip("set NICE_WEATHER_LIVE_DWD_SOLAR_DIR and NICE_WEATHER_LIVE_LAND_CACHE for the frozen day-two audit")
	}
	ctx := context.Background()
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	series := make(pipeline.Series)
	decoder := pipeline.NewDecoder(nil)
	for _, source := range []struct {
		prefix string
		field  pipeline.Field
	}{{"aswdir_s", pipeline.SolarDirect}, {"aswdifd_s", pipeline.SolarDiffuse}} {
		paths, err := filepath.Glob(filepath.Join(directory, source.prefix+"-*.grib2"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(paths)
		if len(paths) != 20 {
			t.Fatalf("%s has %d messages; want steps 26..45", source.field, len(paths))
		}
		for _, path := range paths {
			frame, decodeErr := decoder.Decode(ctx, pipeline.DecodeRequest{Path: path, Field: source.field, Run: run, NativeResolutionKM: 7, CellDegrees: .0625})
			if decodeErr != nil {
				t.Fatalf("decode %s: %v", filepath.Base(path), decodeErr)
			}
			series[source.field] = append(series[source.field], frame)
		}
	}
	spec := series[pipeline.SolarDirect][0].Grid.Spec
	land := NewLandAuthority(landCache, nil, pipeline.ExecRunner{})
	mask, err := land.Mask(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	support, err := expectedForecastSupport(mask, true)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := pipeline.MeasureFieldCoverage(series, support)
	if err != nil {
		t.Fatal(err)
	}
	byField := make(map[pipeline.Field]pipeline.FieldCoverage, len(coverage))
	for _, measured := range coverage {
		byField[measured.Field] = measured
	}
	direct := byField[pipeline.SolarDirect]
	diffuse := byField[pipeline.SolarDiffuse]
	if direct.WorstFrame != (pipeline.CoverageCount{Finite: 379215, Support: 379218}) {
		t.Fatalf("direct frozen coverage = %+v; want 379215/379218", direct.WorstFrame)
	}
	if diffuse.WorstFrame.Finite != diffuse.WorstFrame.Support || diffuse.WorstFrame.Support != 379218 {
		t.Fatalf("diffuse frozen coverage = %+v; want 379218/379218", diffuse.WorstFrame)
	}
	if omitted, policyErr := enforceFieldCoveragePolicy(time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), "DWD ICON-EU", series, coverage); policyErr != nil || omitted {
		t.Fatalf("sparse frozen solar was omitted: omitted=%t err=%v", omitted, policyErr)
	}

	directFrames, err := pipeline.NormalizeSeries(series[pipeline.SolarDirect])
	if err != nil {
		t.Fatal(err)
	}
	diffuseFrames, err := pipeline.NormalizeSeries(series[pipeline.SolarDiffuse])
	if err != nil {
		t.Fatal(err)
	}
	incomplete := make(map[int]bool)
	for frameIndex := range directFrames {
		for cell := range support.Values {
			if support.Values[cell] == 1 && (!finiteLiveSolar(directFrames[frameIndex].Grid.Values[cell]) || !finiteLiveSolar(diffuseFrames[frameIndex].Grid.Values[cell])) {
				incomplete[cell] = true
			}
		}
	}
	for _, cell := range []int{121536, 136964, 136965} {
		if !incomplete[cell] {
			t.Errorf("audited Iceland cell %d was not marked incomplete", cell)
		}
		delete(incomplete, cell)
	}
	if len(incomplete) != 0 {
		t.Fatalf("unexpected incomplete paired-solar cells: %v", incomplete)
	}
	t.Logf("kept paired solar at %.5f%% worst-frame coverage; exactly three supported cells contain incomplete paired source intervals", 100*direct.Fraction())
}

func finiteLiveSolar(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
