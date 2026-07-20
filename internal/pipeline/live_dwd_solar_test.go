package pipeline

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
)

// TestLiveDWD2026071800SolarAudit reproduces the bounded source audit that
// established solarCumulativeArtifactToleranceMJm2. It is opt-in because the
// 40 source GRIB messages are deliberately not retained in the repository.
func TestLiveDWD2026071800SolarAudit(t *testing.T) {
	directory := os.Getenv("NICE_WEATHER_LIVE_DWD_SOLAR_DIR")
	if directory == "" {
		t.Skip("set NICE_WEATHER_LIVE_DWD_SOLAR_DIR to the audited 2026071800 GRIB directory")
	}
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	decoder, err := NewDecoder(nil, delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		prefix             string
		field              Field
		expectedMaxDeficit float64
	}{{"aswdir_s", SolarDirect, 0.064436436}, {"aswdifd_s", SolarDiffuse, 0.005189063}} {
		paths, err := filepath.Glob(filepath.Join(directory, source.prefix+"-*.grib2"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(paths)
		if len(paths) != 20 {
			t.Fatalf("%s has %d messages; want steps 2..21", source.field, len(paths))
		}
		frames := make([]Frame, len(paths))
		for index, path := range paths {
			frames[index], err = decoder.Decode(context.Background(), DecodeRequest{Path: path, Field: source.field, Run: run, NativeResolutionKM: 7, CellDegrees: .0625})
			if err != nil {
				t.Fatalf("decode %s: %v", filepath.Base(path), err)
			}
		}
		normalized, err := NormalizeSeries(frames)
		if err != nil {
			t.Fatal(err)
		}
		if len(normalized) != len(frames) {
			t.Fatalf("%s normalized to %d intervals; want %d", source.field, len(normalized), len(frames))
		}
		maximumHighWaterDeficit := float64(0)
		maximumCell, maximumFrame := 0, 0
		for cell, first := range frames[0].Grid.Values {
			highWater := first
			for frameIndex := 1; frameIndex < len(frames); frameIndex++ {
				current := frames[frameIndex].Grid.Values[cell]
				if !finite32(highWater) || !finite32(current) {
					continue
				}
				if deficit := float64(highWater - current); deficit > maximumHighWaterDeficit {
					maximumHighWaterDeficit, maximumCell, maximumFrame = deficit, cell, frameIndex
				}
				if current > highWater {
					highWater = current
				}
			}
		}
		t.Logf("%s maximum high-water deficit %.9f MJ/m2 at cell %d before interval %s..%s", source.field, maximumHighWaterDeficit, maximumCell, normalized[maximumFrame].Start.Sub(run), normalized[maximumFrame].End.Sub(run))
		if math.Abs(maximumHighWaterDeficit-source.expectedMaxDeficit) > 1e-9 {
			t.Errorf("%s maximum high-water deficit %.9f MJ/m2; want frozen audit %.9f", source.field, maximumHighWaterDeficit, source.expectedMaxDeficit)
		}
		if maximumHighWaterDeficit > solarCumulativeArtifactToleranceMJm2 {
			t.Errorf("%s maximum high-water deficit %.9f exceeds audited ceiling %.9f", source.field, maximumHighWaterDeficit, solarCumulativeArtifactToleranceMJm2)
		}
		for interval := 1; interval < len(normalized); interval++ {
			createdMissing := 0
			for cell, value := range normalized[interval].Grid.Values {
				if finite32(frames[interval-1].Grid.Values[cell]) && finite32(frames[interval].Grid.Values[cell]) && math.IsNaN(float64(value)) {
					createdMissing++
				}
			}
			if createdMissing != 0 {
				t.Errorf("%s interval %s..%s created %d missing cells", source.field, normalized[interval].Start.Sub(run), normalized[interval].End.Sub(run), createdMissing)
			}
		}
	}
}
