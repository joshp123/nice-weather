package update

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/aifs"
)

// TestLiveAIFS2026071818SolarAudit freezes the first outlook day whose
// cumulative ssrd field failed the publication coverage gate. It downloads
// only six indexed solar messages into t.TempDir and publishes nothing.
func TestLiveAIFS2026071818SolarAudit(t *testing.T) {
	if os.Getenv("NWF_AIFS_SOLAR_LIVE") != "1" {
		t.Skip("set NWF_AIFS_SOLAR_LIVE=1 for the frozen AIFS solar regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	run := aifs.Run{Time: time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)}
	steps := []int{96, 102, 108, 114, 120, 126}
	client := aifs.NewClient(&http.Client{Timeout: 45 * time.Second})
	directory := t.TempDir()
	decoder := testDecoder(t)
	frames := make([]pipeline.Frame, 0, len(steps))
	paths := make([]string, 0, len(steps))
	for _, step := range steps {
		messages, err := client.FetchIndex(ctx, run, step)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, fmt.Sprintf("ssrd-%03d.grib2", step))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = client.DownloadIndexedMessage(ctx, run, step, messages["ssrd"], file)
		closeErr := file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		metadata, err := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-p", "shortName,stepType,startStep,endStep,units,packingError,binaryScaleFactor,decimalScaleFactor,bitsPerValue,numberOfDataPoints,numberOfMissing,min,max,average", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("step %d metadata: %s", step, metadata)
		frame, err := decoder.Decode(ctx, pipeline.DecodeRequest{Path: path, Field: pipeline.Solar, Run: run.Time, NativeResolutionKM: 28, CellDegrees: .25})
		if err != nil {
			t.Fatal(err)
		}
		missing := 0
		for cell, value := range frame.Grid.Values {
			if !math.IsNaN(float64(value)) {
				continue
			}
			missing++
			if missing == 1 {
				row, col := cell/int(frame.Grid.Spec.Cols), cell%int(frame.Grid.Spec.Cols)
				width := (frame.Grid.Spec.Bounds.East - frame.Grid.Spec.Bounds.West) / float64(frame.Grid.Spec.Cols)
				height := (frame.Grid.Spec.Bounds.North - frame.Grid.Spec.Bounds.South) / float64(frame.Grid.Spec.Rows)
				lon := frame.Grid.Spec.Bounds.West + (float64(col)+.5)*width
				lat := frame.Grid.Spec.Bounds.North - (float64(row)+.5)*height
				raw, rawErr := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-l", fmt.Sprintf("%.6f,%.6f,1", lat, lon), "-p", "latitude,longitude,value,units,packingError,binaryScaleFactor,decimalScaleFactor", path)
				if rawErr != nil {
					t.Fatal(rawErr)
				}
				t.Logf("step %d first transformed missing cell=%d lon=%.4f lat=%.4f raw=%s", step, cell, lon, lat, raw)
			}
		}
		t.Logf("step %d transformed frame missing: %d/%d", step, missing, len(frame.Grid.Values))
		frames = append(frames, frame)
		paths = append(paths, path)
	}

	normalized, err := pipeline.NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if frames[0].PackingError != .001024 || frames[0].PackingQuantum != .002048 || frames[1].PackingError != .002048 || frames[1].PackingQuantum != .004096 {
		t.Fatalf("frozen AIFS solar packing transition changed: error %.9g -> %.9g, quantum %.9g -> %.9g", frames[0].PackingError, frames[1].PackingError, frames[0].PackingQuantum, frames[1].PackingQuantum)
	}
	regressions, beyondDeclaredErrors, maxDeficit := 0, 0, 0.0
	for cell, current := range frames[1].Grid.Values {
		prior := frames[0].Grid.Values[cell]
		if current >= prior {
			continue
		}
		regressions++
		deficit := float64(prior - current)
		delta := current - prior
		declaredErrorBound := frames[0].PackingError + frames[1].PackingError + float64(math.Nextafter32(delta, float32(math.Inf(1)))-delta)
		if deficit > declaredErrorBound {
			beyondDeclaredErrors++
		}
		bound := frames[1].PackingQuantum + liveFloat32RepresentationError(current) + liveFloat32RepresentationError(prior)
		if deficit > bound {
			t.Fatalf("cell %d solar deficit %.12g exceeds one source quantum plus Float32 representation bound %.12g", cell, deficit, bound)
		}
		if deficit > maxDeficit {
			maxDeficit = deficit
		}
	}
	if regressions != 4288 || beyondDeclaredErrors != 352 || math.Abs(maxDeficit-.00409698486328125) > 1e-12 {
		t.Fatalf("frozen AIFS solar artifact changed: regressions=%d beyond-declared-errors=%d max-deficit=%.12g", regressions, beyondDeclaredErrors, maxDeficit)
	}
	if len(normalized) != len(frames) {
		t.Fatalf("normalized intervals = %d; want %d", len(normalized), len(frames))
	}
	for _, interval := range normalized {
		missing := 0
		for _, value := range interval.Grid.Values {
			if !math.IsNaN(float64(value)) {
				continue
			}
			missing++
		}
		if missing != 0 {
			t.Fatalf("interval %s..%s has %d/%d normalized missing cells", interval.Start.Format(time.RFC3339), interval.End.Format(time.RFC3339), missing, len(interval.Grid.Values))
		}
	}
	land := NewLandAuthority(directory, http.DefaultClient, pipeline.ExecRunner{})
	mask, err := land.Mask(ctx, frames[0].Grid.Spec)
	if err != nil {
		t.Fatal(err)
	}
	support, err := expectedForecastSupport(mask, testProductDomain().Bounds(), false)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := pipeline.MeasureFieldCoverage(pipeline.Series{pipeline.Solar: frames}, support)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 1 || coverage[0].WorstFrame.Finite != 25734 || coverage[0].WorstFrame.Support != 25734 {
		t.Fatalf("frozen AIFS solar coverage = %+v; want 25734/25734", coverage)
	}
	t.Logf("AIFS ssrd scale transition produced %d bounded raw regressions, %d beyond declared half-errors (max %.12g MJ/m2); normalized expected-land coverage is %d/%d", regressions, beyondDeclaredErrors, maxDeficit, coverage[0].WorstFrame.Finite, coverage[0].WorstFrame.Support)

	// Keep paths live until all ecCodes diagnostics above have completed.
	if len(paths) != len(steps) {
		t.Fatalf("downloaded paths = %d; want %d", len(paths), len(steps))
	}
}

func liveFloat32RepresentationError(value float32) float64 {
	up := math.Abs(float64(math.Nextafter32(value, float32(math.Inf(1))) - value))
	down := math.Abs(float64(value - math.Nextafter32(value, float32(math.Inf(-1)))))
	if math.IsInf(up, 0) {
		up = 0
	}
	if math.IsInf(down, 0) {
		down = 0
	}
	return .5 * math.Max(up, down)
}
