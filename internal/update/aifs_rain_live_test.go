package update

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/aifs"
)

// This opt-in regression freezes the late-horizon AIFS run/day that exposed
// false negative cumulative-rain deltas. It downloads only six indexed total-
// precipitation messages into t.TempDir and publishes nothing.
func TestLiveAIFSLateHorizonRain(t *testing.T) {
	if os.Getenv("NWF_AIFS_RAIN_LIVE") != "1" {
		t.Skip("set NWF_AIFS_RAIN_LIVE=1 for the frozen AIFS rain regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	run := aifs.Run{Time: time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)}
	steps := []int{264, 270, 276, 282, 288, 294}
	fullAudit := os.Getenv("NWF_AIFS_RAIN_AUDIT") == "1"
	if fullAudit {
		steps = steps[:0]
		for step := 120; step <= 342; step += 6 {
			steps = append(steps, step)
		}
		if len(steps) != 38 || steps[0] != 120 || steps[len(steps)-1] != 342 {
			t.Fatalf("frozen AIFS audit horizon changed: %v", steps)
		}
	}
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
		path := filepath.Join(directory, fmt.Sprintf("tp-%03d.grib2", step))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = client.DownloadIndexedMessage(ctx, run, step, messages["tp"], file)
		closeErr := file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		frame, err := decoder.Decode(ctx, pipeline.DecodeRequest{Path: path, Field: pipeline.Rain, Run: run.Time, NativeResolutionKM: 28, CellDegrees: .25})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("step %d: %s..%s, semantics=%d, packingError=%.12g mm", step, frame.Start.Format(time.RFC3339), frame.End.Format(time.RFC3339), frame.Semantics, frame.PackingError)
		frames = append(frames, frame)
		paths = append(paths, path)
	}
	normalized, err := pipeline.NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	var firstFailure string
	for interval, frame := range normalized {
		missing := make([]int, 0)
		for cell, value := range frame.Grid.Values {
			if math.IsNaN(float64(value)) {
				missing = append(missing, cell)
			}
		}
		t.Logf("interval %s..%s: %d/%d missing", frame.Start.Format(time.RFC3339), frame.End.Format(time.RFC3339), len(missing), len(frame.Grid.Values))
		if len(missing) == 0 {
			continue
		}
		limit := len(missing)
		if limit > 8 {
			limit = 8
		}
		var evidence []string
		for _, cell := range missing[:limit] {
			row, col := cell/int(frame.Grid.Spec.Cols), cell%int(frame.Grid.Spec.Cols)
			width := (frame.Grid.Spec.Bounds.East - frame.Grid.Spec.Bounds.West) / float64(frame.Grid.Spec.Cols)
			height := (frame.Grid.Spec.Bounds.North - frame.Grid.Spec.Bounds.South) / float64(frame.Grid.Spec.Rows)
			lon := frame.Grid.Spec.Bounds.West + (float64(col)+.5)*width
			lat := frame.Grid.Spec.Bounds.North - (float64(row)+.5)*height
			values := make([]string, len(frames))
			highWater := float32(math.Inf(-1))
			for index, source := range frames {
				value := source.Grid.Values[cell]
				if index <= interval && value > highWater {
					highWater = value
				}
				values[index] = fmt.Sprintf("%.9g", value)
			}
			current := frames[interval].Grid.Values[cell]
			raw, rawErr := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-l", fmt.Sprintf("%.6f,%.6f,1", lat, lon), "-p", "latitude,longitude,value,units,packingError,startStep,endStep,stepType,binaryScaleFactor,decimalScaleFactor,bitsPerValue", paths[interval])
			if rawErr != nil {
				raw = []byte(rawErr.Error())
			}
			evidence = append(evidence, fmt.Sprintf("cell=%d lon=%.4f lat=%.4f transformed=[%s] highWater=%.9g current=%.9g deficit=%.9g raw=%q", cell, lon, lat, strings.Join(values, ","), highWater, current, highWater-current, strings.TrimSpace(string(raw))))
		}
		if firstFailure == "" {
			firstFailure = fmt.Sprintf("late AIFS rain interval has %d missing cells; %s", len(missing), strings.Join(evidence, "; "))
		}
	}

	// Independently audit every raw cumulative frame against the previous
	// per-cell high-water mark, even after the production normalizer fails a
	// cell closed. This exposes the complete provider artifact envelope.
	highWater := append([]float32(nil), frames[0].Grid.Values...)
	highWaterError := make([]float64, len(highWater))
	highWaterQuantum := make([]float64, len(highWater))
	maxAuditedDeficit := 0.0
	for cell := range highWaterError {
		highWaterError[cell] = frames[0].PackingError
		highWaterQuantum[cell] = frames[0].PackingQuantum
	}
	for index := 1; index < len(frames); index++ {
		count, beyond := 0, 0
		maxDeficit, maxExcess := 0.0, 0.0
		for cell, current := range frames[index].Grid.Values {
			if !math.IsNaN(float64(current)) && current < highWater[cell] {
				deficit := float64(highWater[cell] - current)
				tolerance := highWaterError[cell] + frames[index].PackingError
				quantum := math.Max(highWaterQuantum[cell], frames[index].PackingQuantum)
				if deficit > quantum+math.SmallestNonzeroFloat64 {
					t.Fatalf("step %d cell %d deficit %.12g exceeds exact coarser quantum %.12g", steps[index], cell, deficit, quantum)
				}
				count++
				if deficit > maxDeficit {
					maxDeficit = deficit
				}
				if deficit > maxAuditedDeficit {
					maxAuditedDeficit = deficit
				}
				if deficit > tolerance {
					beyond++
					if deficit-tolerance > maxExcess {
						maxExcess = deficit - tolerance
					}
				}
			}
			if current >= highWater[cell] {
				highWater[cell] = current
				highWaterError[cell] = frames[index].PackingError
				highWaterQuantum[cell] = frames[index].PackingQuantum
			}
		}
		t.Logf("step %d high-water audit: declines=%d beyond-declared=%d max-deficit=%.9g mm max-excess=%.9g mm", steps[index], count, beyond, maxDeficit, maxExcess)
	}
	step264, step276 := stepIndex(steps, 264), stepIndex(steps, 276)
	if step264 < 0 || step276 < 0 {
		t.Fatalf("frozen AIFS audit omits packing transition: steps=%v", steps)
	}
	if frames[step264].PackingError != .00390625 || frames[step276].PackingError != .0078125 || frames[step264].PackingQuantum != .0078125 || frames[step276].PackingQuantum != .015625 || maxAuditedDeficit != .015625 {
		t.Fatalf("frozen AIFS packing transition/artifact envelope changed: errors %.9g -> %.9g, quanta %.9g -> %.9g, max deficit %.9g", frames[step264].PackingError, frames[step276].PackingError, frames[step264].PackingQuantum, frames[step276].PackingQuantum, maxAuditedDeficit)
	}
	if firstFailure != "" {
		t.Fatal(firstFailure)
	}
	if fullAudit {
		root := filepath.Join(directory, "land")
		land := NewLandAuthority(root, http.DefaultClient, pipeline.ExecRunner{})
		if _, err := land.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		mask, err := land.Mask(ctx, frames[0].Grid.Spec)
		if err != nil {
			t.Fatal(err)
		}
		support, err := expectedForecastSupport(mask, testProductDomain().Bounds(), false)
		if err != nil {
			t.Fatal(err)
		}
		staged := make([]stagedFrame, len(steps))
		frameByStep := make(map[int]pipeline.Frame, len(steps))
		for index, step := range steps {
			staged[index] = stagedFrame{path: paths[index], field: pipeline.Rain, step: step, run: run.Time, resolution: 28, spacing: .25}
			frameByStep[step] = frames[index]
		}
		for dayIndex := 0; dayIndex < 9; dayIndex++ {
			day := dayPlan{Date: time.Date(2026, 7, 23+dayIndex, 0, 0, 0, 0, time.UTC)}
			selected := stagedForDay(staged, day.Date, false)
			dayFrames := make([]pipeline.Frame, 0, len(selected))
			for _, item := range selected {
				dayFrames = append(dayFrames, frameByStep[item.step])
			}
			daySeries := pipeline.Series{pipeline.Rain: dayFrames}
			coverage, err := pipeline.MeasureFieldCoverage(daySeries, support)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("day %s rain: %s", day.Date.Format("2006-01-02"), formatFieldCoverage(coverage))
			if _, err := enforceFieldCoveragePolicy(day.Date, "ECMWF AIFS", daySeries, coverage); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func stepIndex(steps []int, wanted int) int {
	for index, step := range steps {
		if step == wanted {
			return index
		}
	}
	return -1
}

// This is the bounded end-to-end proof for the same frozen day. It downloads
// the six required AIFS times for all seven fields, then exercises exact source
// and final-day publication coverage without writing a release.
func TestLiveAIFSLateHorizonDayCoverage(t *testing.T) {
	if os.Getenv("NWF_AIFS_RAIN_LIVE") != "1" {
		t.Skip("set NWF_AIFS_RAIN_LIVE=1 for the frozen AIFS day regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	run := aifs.Run{Time: time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)}
	day := dayPlan{Date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)}
	client := aifs.NewClient(&http.Client{Timeout: 45 * time.Second})
	root := t.TempDir()
	land := NewLandAuthority(root, http.DefaultClient, pipeline.ExecRunner{})
	if _, err := land.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	staged, err := stageAIFS(ctx, client, run, stepsForAIFS(run.Time, []dayPlan{day}), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertLiveDayCoverage(ctx, t, land, day, staged, 28, "ECMWF AIFS"); err != nil {
		t.Fatal(err)
	}
}
