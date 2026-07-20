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
	"nice-weather-finder/internal/providers/dwd"
)

// This opt-in regression audits every ICON-EU total-precipitation message used
// by the five detailed product days in the frozen 2026071800 run. Downloads are
// bounded to one field, live in t.TempDir, and never enter release publication.
func TestLiveDWDRequiredRainHorizon(t *testing.T) {
	if os.Getenv("NWF_DWD_RAIN_LIVE") != "1" {
		t.Skip("set NWF_DWD_RAIN_LIVE=1 for the frozen ICON-EU rain audit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	run := dwd.Run{StartedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	days := make([]dayPlan, 5)
	for index := range days {
		days[index] = dayPlan{Date: run.StartedAt.AddDate(0, 0, index), Detailed: true}
	}
	steps := stepsForDWD(run.StartedAt, days)
	if len(steps) != 90 || steps[0] != 2 || steps[len(steps)-1] != 117 {
		t.Fatalf("frozen detailed horizon steps = %v; want 90 steps 2..117", steps)
	}
	root := t.TempDir()
	destination := filepath.Join(root, "dwd")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	field, err := dwd.FieldByName("total_precipitation")
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageDWDFields(ctx, &http.Client{Timeout: 45 * time.Second}, run, steps, destination, root, []dwd.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	series, err := decodeFrames(ctx, testDecoder(t), staged)
	if err != nil {
		t.Fatal(err)
	}
	frames := series[pipeline.Rain]
	if len(frames) != len(steps) {
		t.Fatalf("decoded %d rain frames; want %d", len(frames), len(steps))
	}

	highWater := append([]float32(nil), frames[0].Grid.Values...)
	highWaterError := make([]float64, len(highWater))
	highWaterFrame := make([]int, len(highWater))
	for cell := range highWaterError {
		highWaterError[cell] = frames[0].PackingError
	}
	maxDeficit, maxQuantumExcess := 0.0, 0.0
	var violations []string
	artifactCells := make(map[int]bool)
	for index := 1; index < len(frames); index++ {
		declines, beyondQuantum := 0, 0
		stepMax := 0.0
		for cell, current := range frames[index].Grid.Values {
			if math.IsNaN(float64(current)) || math.IsNaN(float64(highWater[cell])) {
				continue
			}
			if current < highWater[cell] {
				declines++
				deficit := float64(highWater[cell] - current)
				quantum := math.Max(frames[index].PackingQuantum, frames[highWaterFrame[cell]].PackingQuantum)
				if deficit > stepMax {
					stepMax = deficit
				}
				if deficit > maxDeficit {
					maxDeficit = deficit
				}
				if deficit > quantum {
					beyondQuantum++
					if deficit-quantum > maxQuantumExcess {
						maxQuantumExcess = deficit - quantum
					}
					if len(violations) < 12 {
						row, col := cell/int(frames[index].Grid.Spec.Cols), cell%int(frames[index].Grid.Spec.Cols)
						width := (frames[index].Grid.Spec.Bounds.East - frames[index].Grid.Spec.Bounds.West) / float64(frames[index].Grid.Spec.Cols)
						height := (frames[index].Grid.Spec.Bounds.North - frames[index].Grid.Spec.Bounds.South) / float64(frames[index].Grid.Spec.Rows)
						lon := frames[index].Grid.Spec.Bounds.West + (float64(col)+.5)*width
						lat := frames[index].Grid.Spec.Bounds.North - (float64(row)+.5)*height
						currentRaw, _ := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-l", fmt.Sprintf("%.6f,%.6f,1", lat, lon), "-p", "value,units,packingError,startStep,endStep,stepType,binaryScaleFactor,bitsPerValue", staged[index].path)
						highRaw, _ := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-l", fmt.Sprintf("%.6f,%.6f,1", lat, lon), "-p", "value,units,packingError,startStep,endStep,stepType,binaryScaleFactor,bitsPerValue", staged[highWaterFrame[cell]].path)
						violations = append(violations, fmt.Sprintf("step=%d cell=%d lon=%.4f lat=%.4f highStep=%d high=%.9g current=%.9g deficit=%.9g quantum=%.9g highRaw=%q currentRaw=%q", steps[index], cell, lon, lat, steps[highWaterFrame[cell]], highWater[cell], current, deficit, quantum, strings.TrimSpace(string(highRaw)), strings.TrimSpace(string(currentRaw))))
					}
				}
				if deficit > frames[index].PackingError+highWaterError[cell] && deficit <= quantum {
					artifactCells[cell] = true
				}
			}
			if current >= highWater[cell] {
				highWater[cell] = current
				highWaterError[cell] = frames[index].PackingError
				highWaterFrame[cell] = index
			}
		}
		t.Logf("step %d packingError=%.12g mm declines=%d beyond-quantum=%d max-deficit=%.9g mm", steps[index], frames[index].PackingError, declines, beyondQuantum, stepMax)
	}
	t.Logf("full ICON-EU rain horizon: max-deficit=%.9g mm max-quantum-excess=%.9g mm", maxDeficit, maxQuantumExcess)

	land := NewLandAuthority(root, http.DefaultClient, pipeline.ExecRunner{})
	if _, err := land.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	mask, err := land.Mask(ctx, frames[0].Grid.Spec)
	if err != nil {
		t.Fatal(err)
	}
	support, err := expectedForecastSupport(mask, testProductDomain().Bounds(), true)
	if err != nil {
		t.Fatal(err)
	}
	supportedArtifacts := 0
	for cell := range artifactCells {
		if support.Values[cell] != 1 {
			continue
		}
		supportedArtifacts++
		row, col := cell/int(support.Spec.Cols), cell%int(support.Spec.Cols)
		width := (support.Spec.Bounds.East - support.Spec.Bounds.West) / float64(support.Spec.Cols)
		height := (support.Spec.Bounds.North - support.Spec.Bounds.South) / float64(support.Spec.Rows)
		lon := support.Spec.Bounds.West + (float64(col)+.5)*width
		lat := support.Spec.Bounds.North - (float64(row)+.5)*height
		t.Logf("supported exact-quantum artifact: cell=%d lon=%.4f lat=%.4f", cell, lon, lat)
		for _, sourceStep := range []int{108, 111} {
			frameIndex := 0
			for steps[frameIndex] != sourceStep {
				frameIndex++
			}
			for _, sourceLat := range []float64{lat - .03125, lat + .03125} {
				for _, sourceLon := range []float64{lon - .03125, lon + .03125} {
					raw, rawErr := pipeline.ExecRunner{}.Run(ctx, "grib_ls", "-l", fmt.Sprintf("%.6f,%.6f,1", sourceLat, sourceLon), "-p", "value,units,packingError,startStep,endStep,stepType,binaryScaleFactor,decimalScaleFactor,bitsPerValue", staged[frameIndex].path)
					if rawErr != nil {
						t.Fatal(rawErr)
					}
					t.Logf("raw supported artifact step=%d query=%.5f,%.5f: %s", sourceStep, sourceLat, sourceLon, strings.TrimSpace(string(raw)))
				}
			}
		}
	}
	if len(artifactCells) != 4 || supportedArtifacts != 2 || maxDeficit != math.Ldexp(1, -8) || maxQuantumExcess != 0 {
		t.Fatalf("frozen ICON-EU artifact envelope changed: cells=%d supported=%d max-deficit=%.12g excess=%.12g", len(artifactCells), supportedArtifacts, maxDeficit, maxQuantumExcess)
	}
	frameByStep := make(map[int]pipeline.Frame, len(steps))
	for index, step := range steps {
		frameByStep[step] = frames[index]
	}
	for _, day := range days {
		selected := stagedForDay(staged, day.Date, true)
		dayFrames := make([]pipeline.Frame, 0, len(selected))
		for _, item := range selected {
			dayFrames = append(dayFrames, frameByStep[item.step])
		}
		coverage, err := pipeline.MeasureFieldCoverage(pipeline.Series{pipeline.Rain: dayFrames}, support)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("day %s rain: %s", day.Date.Format("2006-01-02"), formatFieldCoverage(coverage))
		if _, err := enforceFieldCoveragePolicy(day.Date, "DWD ICON-EU", pipeline.Series{pipeline.Rain: dayFrames}, coverage); err != nil {
			t.Fatalf("%v; raw high-water violations: %s", err, strings.Join(violations, "; "))
		}
	}
}
