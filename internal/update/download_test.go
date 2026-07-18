package update

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/dwd"
)

func TestStagedForDayBoundsDecodedWorkingSet(t *testing.T) {
	run := time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	var dwdFrames []stagedFrame
	for step := 0; step <= 120; step++ {
		for field := 0; field < 9; field++ {
			dwdFrames = append(dwdFrames, stagedFrame{path: fmt.Sprintf("%d-%d", step, field), run: run, step: step})
		}
	}
	selected := stagedForDay(dwdFrames, date, true)
	if len(selected) != 172 {
		t.Fatalf("detailed day retained %d frames; want 171 in-window frames plus one predecessor for the synthetic field", len(selected))
	}

	var aifsFrames []stagedFrame
	for step := 0; step <= 360; step += 6 {
		for field := 0; field < 7; field++ {
			aifsFrames = append(aifsFrames, stagedFrame{path: fmt.Sprintf("%d-%d", step, field), run: run, step: step})
		}
	}
	selected = stagedForDay(aifsFrames, date, false)
	if len(selected) != 43 {
		t.Fatalf("outlook day retained %d frames; want 42 in-window frames plus one predecessor for the synthetic field", len(selected))
	}
}

func TestStagedForDayRetainsOnePrecedingFramePerField(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	run := date
	staged := []stagedFrame{
		{path: "temp-1", field: pipeline.Temperature, run: run, step: 1},
		{path: "temp-2", field: pipeline.Temperature, run: run, step: 2},
		{path: "solar-1", field: pipeline.SolarDirect, run: run, step: 1},
		{path: "solar-2", field: pipeline.SolarDirect, run: run, step: 2},
		{path: "temp-3", field: pipeline.Temperature, run: run, step: 3},
		{path: "solar-3", field: pipeline.SolarDirect, run: run, step: 3},
	}
	selected := stagedForDay(staged, date, true)
	paths := map[string]bool{}
	for _, frame := range selected {
		paths[frame.path] = true
	}
	for _, want := range []string{"temp-2", "solar-2", "temp-3", "solar-3"} {
		if !paths[want] {
			t.Errorf("missing %s from bounded day input", want)
		}
	}
	if paths["temp-1"] || paths["solar-1"] || len(selected) != 4 {
		t.Fatalf("retained more than one predecessor per field: %+v", selected)
	}
}

func TestStageDWDPublishesRequiredFieldsWhenPreferredGroupsFail(t *testing.T) {
	payload, err := hex.DecodeString("425a6839314159265359774bb01400000000800040200021184682ee48a70a120ee9760280")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/vmax_10m/") || strings.Contains(request.URL.Path, "/aswdifd_s/") {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		}
		header := make(http.Header)
		header.Set("Content-Length", fmt.Sprint(len(payload)))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(payload)), Header: header, ContentLength: int64(len(payload)), Request: request}, nil
	})}
	run := dwd.Run{StartedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	stage := t.TempDir()
	staged, err := stageDWD(context.Background(), client, run, []int{1}, stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != len(dwd.RequiredFields()) {
		t.Fatalf("staged %d fields; want %d required fields only", len(staged), len(dwd.RequiredFields()))
	}
	for _, frame := range staged {
		if frame.field == pipeline.Gust || frame.field == pipeline.SolarDirect || frame.field == pipeline.SolarDiffuse {
			t.Fatalf("partial preferred group escaped staging: %+v", frame)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(stage, "dwd", "asw*_s-001.grib2*")); err != nil || len(matches) != 0 {
		t.Fatalf("failed solar group left partial files %v: %v", matches, err)
	}
	if _, err := os.Stat(filepath.Join(stage, "dwd", "t_2m-001.grib2")); err != nil {
		t.Fatalf("required field was not retained: %v", err)
	}
}

func TestStageDWDRequiredFailureIsFatal(t *testing.T) {
	payload, err := hex.DecodeString("425a6839314159265359774bb01400000000800040200021184682ee48a70a120ee9760280")
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	ready := make(chan struct{})
	var readyOnce sync.Once
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/v_10m/") {
			<-ready
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		}
		if successes.Add(1) >= 4 {
			readyOnce.Do(func() { close(ready) })
		}
		header := make(http.Header)
		header.Set("Content-Length", fmt.Sprint(len(payload)))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(payload)), Header: header, ContentLength: int64(len(payload)), Request: request}, nil
	})}
	run := dwd.Run{StartedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	stage := t.TempDir()
	if _, err := stageDWD(context.Background(), client, run, []int{1}, stage); err == nil || !strings.Contains(err.Error(), "required ICON-EU fields") {
		t.Fatalf("required failure did not stop staging: %v", err)
	}
	if successes.Load() < 4 {
		t.Fatalf("test did not exercise a late failure: only %d successful siblings", successes.Load())
	}
	if matches, err := filepath.Glob(filepath.Join(stage, "dwd", "*.grib2*")); err != nil || len(matches) != 0 {
		t.Fatalf("failed required group left partial files %v: %v", matches, err)
	}
}

func TestDWDSolarSmokeNormalizesAdjacentRunAverages(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	series := pipeline.Series{}
	for _, field := range []pipeline.Field{pipeline.SolarDirect, pipeline.SolarDiffuse} {
		series[field] = []pipeline.Frame{
			smokeSolarFrame(field, run, run.Add(time.Hour), 100),
			smokeSolarFrame(field, run, run.Add(2*time.Hour), 150),
		}
	}
	if err := validateDWDSolarSmoke(series); err != nil {
		t.Fatal(err)
	}
	broken := pipeline.Series{
		pipeline.SolarDirect:  append([]pipeline.Frame(nil), series[pipeline.SolarDirect]...),
		pipeline.SolarDiffuse: append([]pipeline.Frame(nil), series[pipeline.SolarDiffuse]...),
	}
	broken[pipeline.SolarDiffuse][1].Grid.Values = []float32{float32(math.NaN())}
	if err := validateDWDSolarSmoke(broken); err == nil {
		t.Fatal("smoke accepted an interval without finite solar support")
	}
	delete(series, pipeline.SolarDiffuse)
	if err := validateDWDSolarSmoke(series); err == nil {
		t.Fatal("smoke accepted a lone solar component")
	}
	if err := validateDWDSolarSmoke(pipeline.Series{}); err == nil {
		t.Fatal("smoke passed without exercising solar normalization")
	}
}

func smokeSolarFrame(field pipeline.Field, start, end time.Time, value float32) pipeline.Frame {
	return pipeline.Frame{
		Field: field, Start: start, End: end, Semantics: pipeline.CumulativeMean, Units: "W m-2", NativeResolutionKM: 7,
		Grid: pipeline.FloatGrid{Spec: delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, Values: []float32{value}},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
