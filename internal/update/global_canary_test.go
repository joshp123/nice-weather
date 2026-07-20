package update

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/ifs"
)

func TestGlobalCanaryUsesTheExistingWorldwideStepEnvelope(t *testing.T) {
	run := ifs.Run{Time: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	date, steps, err := globalCanaryDayAndSteps(time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), run)
	if err != nil {
		t.Fatal(err)
	}
	if got := date.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("canary date = %s, want first complete worldwide date", got)
	}
	want := []int{6, 9, 12, 15, 18, 21, 24, 27, 30, 33, 36, 39, 42, 45, 48}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("canary steps = %v, want %v", steps, want)
	}
	firstWindow := globalOutlookDecodeWindow.start(date)
	if valid := run.Time.Add(time.Duration(steps[0]) * time.Hour); !valid.Before(firstWindow) {
		t.Fatalf("step %d is not the one preceding worldwide window %s", steps[0], firstWindow)
	}
	if valid := run.Time.Add(time.Duration(steps[1]) * time.Hour); valid.Before(firstWindow) {
		t.Fatalf("step %d still precedes worldwide window %s", steps[1], firstWindow)
	}
	lastWindow := globalOutlookDecodeWindow.end(date)
	if valid := run.Time.Add(time.Duration(steps[len(steps)-1]) * time.Hour); valid.After(lastWindow) {
		t.Fatalf("last step %d exceeds worldwide window %s", steps[len(steps)-1], lastWindow)
	}
}

func TestGlobalCanaryPrepareRequestBindsControlSourceAndExactRun(t *testing.T) {
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	frame := stagedFrame{path: "/tmp/field.grib2", field: pipeline.DewPoint, step: 9, run: run, resolution: 28, spacing: .25}
	request := globalCanaryPrepareRequest(frame, "/tmp/prepared")
	if request.SourceID != "ecmwf-ifs-control" || !request.Run.Equal(run) {
		t.Fatalf("prepared identity = %q %s", request.SourceID, request.Run)
	}
	if request.Path != frame.path || request.Field != frame.field || request.NativeResolutionKM != 28 || request.CellDegrees != .25 || request.ScratchDirectory != "/tmp/prepared" {
		t.Fatalf("prepared request changed source facts: %+v", request)
	}
}

func TestSourceCenterLandPredicateIsDeterministicAcrossDateLine(t *testing.T) {
	mask := pipeline.FloatGrid{
		Spec: delivery.GridSpec{Bounds: globalCanaryBounds, Rows: 2, Cols: 4},
		Values: []float32{
			1, 0, 0, 1,
			0, 1, 1, 0,
		},
	}
	isLand, err := sourceCenterLandPredicate(mask)
	if err != nil {
		t.Fatal(err)
	}
	for _, longitude := range []float64{-180, 180, 540, -540} {
		got, err := isLand(longitude, 45)
		if err != nil || !got {
			t.Fatalf("land(%g,45) = %v, %v; want canonical western cell", longitude, got, err)
		}
	}
	if got, err := isLand(-89, 45); err != nil || got {
		t.Fatalf("land(-89,45) = %v, %v; want water", got, err)
	}
	if got, err := isLand(-89, -45); err != nil || !got {
		t.Fatalf("land(-89,-45) = %v, %v; want land", got, err)
	}
	if _, err := isLand(0, math.NaN()); err == nil {
		t.Fatal("non-finite land lookup was accepted")
	}
}

func TestGlobalCanaryRejectsAProductionArtifactRoot(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	out := filepath.Join(root, "production-artifacts")
	current := filepath.Join(out, "current", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "production pointer sentinel"
	if err := os.WriteFile(current, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RunGlobalCanary(context.Background(), GlobalCanaryConfig{Out: out, Cache: cache, Now: func() time.Time {
		return time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	}})
	if err == nil || err.Error() != `global canary output contains production release state "current"` {
		t.Fatalf("production-root canary error = %v", err)
	}
	payload, err := os.ReadFile(current)
	if err != nil || string(payload) != sentinel {
		t.Fatalf("current publication changed: %q, %v", payload, err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected production root still touched cache: %v", err)
	}
}

func TestCancelledGlobalCanaryCleansScratchAndDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	out := filepath.Join(root, "isolated-canary")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunGlobalCanary(ctx, GlobalCanaryConfig{Out: out, Cache: cache})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled canary error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(cache, "global-canary-scratch"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled canary left scratch entries: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(out, "current")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled canary published current: %v", err)
	}
}
