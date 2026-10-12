package update

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/providers/ifs"
)

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
	selected := stagedForDayWindow(staged, date, europeDetailedDecodeWindow)
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

func TestStagedForWorldwideDayCrossesBothUTCDates(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	var staged []stagedFrame
	for step := 0; step <= 48; step += 6 {
		staged = append(staged, stagedFrame{path: fmt.Sprint(step), field: pipeline.Temperature, run: run, step: step})
	}
	selected := stagedForDayWindow(staged, date, globalOutlookDecodeWindow)
	var steps []int
	for _, frame := range selected {
		steps = append(steps, frame.step)
	}
	if fmt.Sprint(steps) != "[6 12 18 24 30 36 42 48]" {
		t.Fatalf("worldwide day staged steps %v", steps)
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

func TestStageIFSFailureRemovesSuccessfulSiblings(t *testing.T) {
	run := ifs.Run{Time: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	fields := ifs.RequiredSurfaceFields()
	var index strings.Builder
	for fieldIndex, field := range fields {
		fmt.Fprintf(&index, `{"domain":"g","date":"20260718","time":"1200","type":"fc","stream":"oper","step":"6","levtype":"sfc","param":"%s","_offset":%d,"_length":20}`+"\n", field, fieldIndex*20)
	}
	grib := make([]byte, 20)
	copy(grib, "GRIB")
	grib[7] = 2
	binary.BigEndian.PutUint64(grib[8:16], uint64(len(grib)))
	copy(grib[16:], "7777")
	client := ifs.NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".index") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(index.String())), Header: make(http.Header), ContentLength: int64(index.Len()), Request: request}, nil
		}
		requested := request.Header.Get("Range")
		if requested == "bytes=80-99" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		}
		header := http.Header{"Content-Range": {strings.Replace(requested, "=", " ", 1) + "/200"}}
		return &http.Response{StatusCode: http.StatusPartialContent, Body: io.NopCloser(bytes.NewReader(grib)), Header: header, ContentLength: 20, Request: request}, nil
	})})
	client.Root = "https://example.test"
	stage := t.TempDir()
	if _, err := stageIFS(context.Background(), client, run, []int{6}, stage); err == nil {
		t.Fatal("expected required IFS download failure")
	}
	if matches, err := filepath.Glob(filepath.Join(stage, "ifs", "*.grib2")); err != nil || len(matches) != 0 {
		t.Fatalf("failed IFS group left partial files %v: %v", matches, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
