package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/tiledelivery"
)

func TestGlobalCanaryUsesExactlyFourteenCompleteWorldwideDays(t *testing.T) {
	run := ifs.Run{Time: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	days, steps, err := globalCanaryDaysAndSteps(time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 14 || days[0].Date.Format("2006-01-02") != "2026-07-19" || days[13].Date.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("canary days = %+v", days)
	}
	available, err := ifs.ForecastSteps(run)
	if err != nil {
		t.Fatal(err)
	}
	want := stepsForDays(run.Time, days, globalOutlookDecodeWindow, available)
	if !reflect.DeepEqual(steps, want) || steps[0] != 6 || steps[len(steps)-1] != 360 {
		t.Fatalf("canary steps = %v, want exact 6..360 envelope", steps)
	}
	firstWindow := globalOutlookDecodeWindow.start(days[0].Date)
	if valid := run.Time.Add(time.Duration(steps[0]) * time.Hour); !valid.Before(firstWindow) {
		t.Fatalf("step %d is not the one preceding worldwide window %s", steps[0], firstWindow)
	}
	if valid := run.Time.Add(time.Duration(steps[1]) * time.Hour); valid.Before(firstWindow) {
		t.Fatalf("step %d still precedes worldwide window %s", steps[1], firstWindow)
	}
	lastProductBoundary := globalProductWindow.end(days[len(days)-1].Date)
	if valid := run.Time.Add(time.Duration(steps[len(steps)-1]) * time.Hour); valid.Before(lastProductBoundary) {
		t.Fatalf("last step %d ends before worldwide product boundary %s", steps[len(steps)-1], lastProductBoundary)
	}
}

func TestAssembleGlobalCanaryDaysIsTransactionalOrderedAndDayBounded(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	preparedRoot := filepath.Join(root, "prepared")
	if err := os.MkdirAll(filepath.Join(out, ".global-canary-abandoned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "unrelated.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(preparedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, steps, err := globalCanaryDaysAndSteps(run.Add(time.Hour), ifs.Run{Time: run})
	if err != nil {
		t.Fatal(err)
	}
	staged := syntheticGlobalCanaryStaged(run, steps)
	var preparedDates, builtDates []string
	maxScratchFiles := 0
	firstFile := filepath.Join(preparedRoot, "first.f32")
	if err := os.WriteFile(firstFile, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := &globalCanaryPreparedDay{
		series: pipeline.PreparedSeries{pipeline.Temperature: {}}, files: 1, bytes: 5,
		close: func() error { return os.Remove(firstFile) },
	}
	assembler := globalCanaryAssembler{
		prepare: func(_ context.Context, selected []stagedFrame, scratch string) (pipeline.PreparedSeries, func() error, error) {
			date := selectedDayDate(t, days, staged, selected)
			preparedDates = append(preparedDates, date)
			path := filepath.Join(scratch, date+".f32")
			if err := os.WriteFile(path, []byte(date), 0o600); err != nil {
				return nil, nil, err
			}
			files, _, err := treeStats(scratch)
			if err != nil {
				return nil, nil, err
			}
			maxScratchFiles = max(maxScratchFiles, files)
			return pipeline.PreparedSeries{pipeline.Temperature: {}}, func() error { return os.Remove(path) }, nil
		},
		build: func(_ context.Context, config forecasttiles.Config) (forecasttiles.Catalog, error) {
			builtDates = append(builtDates, config.Period)
			path := filepath.Join(config.Root, "forecast-days", config.Period, config.Tier, "forecast-tiles.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return forecasttiles.Catalog{}, err
			}
			if err := os.WriteFile(path, []byte(config.Period), 0o644); err != nil {
				return forecasttiles.Catalog{}, err
			}
			return syntheticGlobalCatalog(config), nil
		},
	}
	summaries, installed, err := assembleGlobalCanaryDays(context.Background(), out, "run-14-days", run, days, staged, preparedRoot, timeZoneUTC, landEverywhere, first, assembler)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 14 || len(preparedDates) != 13 || len(builtDates) != 14 {
		t.Fatalf("assembled=%d prepared=%d built=%d", len(summaries), len(preparedDates), len(builtDates))
	}
	for index, day := range days {
		want := day.Date.Format("2006-01-02")
		if builtDates[index] != want || (index > 0 && preparedDates[index-1] != want) {
			t.Fatalf("day %d order: prepared=%v built=%v", index, preparedDates, builtDates)
		}
		if _, err := os.Stat(filepath.Join(installed, "forecast-days", want, "global", "forecast-tiles.json")); err != nil {
			t.Fatalf("installed day %s: %v", want, err)
		}
		wantMessages := len(stagedForDayWindow(staged, day.Date, globalOutlookDecodeWindow))
		if summaries[index].Messages != wantMessages || summaries[index].PreparedFiles != 1 || len(summaries[index].Catalog.Points) != 1 || len(summaries[index].Catalog.Scalars) != 1 {
			t.Fatalf("day %s summary = %+v; want %d messages and one bounded synthetic asset", want, summaries[index], wantMessages)
		}
	}
	files, bytes, err := treeStats(installed)
	if err != nil || files != productDays || bytes != productDays*int64(len("2006-01-02")) {
		t.Fatalf("installed aggregate = %d files/%d bytes, %v", files, bytes, err)
	}
	if maxScratchFiles != 1 {
		t.Fatalf("max prepared scratch files = %d, want one synthetic day", maxScratchFiles)
	}
	if matches, _ := filepath.Glob(filepath.Join(out, ".global-canary-*")); len(matches) != 0 {
		t.Fatalf("transaction staging remains: %v", matches)
	}
	if payload, err := os.ReadFile(filepath.Join(out, "unrelated.txt")); err != nil || string(payload) != "keep" {
		t.Fatalf("transaction cleanup touched unrelated output: %q, %v", payload, err)
	}
	if _, err := os.Stat(filepath.Join(out, "current")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canary published current: %v", err)
	}
}

func TestAssembleGlobalCanaryDaysCancellationRemovesIncompleteRunAndScratch(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	preparedRoot := filepath.Join(root, "prepared")
	if err := os.MkdirAll(preparedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, steps, err := globalCanaryDaysAndSteps(run.Add(time.Hour), ifs.Run{Time: run})
	if err != nil {
		t.Fatal(err)
	}
	staged := syntheticGlobalCanaryStaged(run, steps)
	first := &globalCanaryPreparedDay{series: pipeline.PreparedSeries{pipeline.Temperature: {}}, close: func() error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	built := 0
	assembler := globalCanaryAssembler{
		prepare: func(_ context.Context, _ []stagedFrame, scratch string) (pipeline.PreparedSeries, func() error, error) {
			path := filepath.Join(scratch, "day.f32")
			if err := os.WriteFile(path, []byte("day"), 0o600); err != nil {
				return nil, nil, err
			}
			return pipeline.PreparedSeries{pipeline.Temperature: {}}, func() error { return os.Remove(path) }, nil
		},
		build: func(_ context.Context, config forecasttiles.Config) (forecasttiles.Catalog, error) {
			built++
			if built == 3 {
				cancel()
				return forecasttiles.Catalog{}, context.Canceled
			}
			path := filepath.Join(config.Root, "forecast-days", config.Period, config.Tier, "forecast-tiles.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return forecasttiles.Catalog{}, err
			}
			if err := os.WriteFile(path, []byte(config.Period), 0o644); err != nil {
				return forecasttiles.Catalog{}, err
			}
			return syntheticGlobalCatalog(config), nil
		},
	}
	_, _, err = assembleGlobalCanaryDays(ctx, out, "cancelled-run", run, days, staged, preparedRoot, timeZoneUTC, landEverywhere, first, assembler)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled assembly error = %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(out, ".global-canary-*")); len(matches) != 0 {
		t.Fatalf("cancelled transaction staging remains: %v", matches)
	}
	if _, err := os.Stat(filepath.Join(out, "canary-runs", "cancelled-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled run installed: %v", err)
	}
	files, _, err := treeStats(preparedRoot)
	if err != nil || files != 0 {
		t.Fatalf("cancelled prepared scratch = %d files, %v", files, err)
	}
}

func TestValidateGlobalCanaryStagedRejectsProviderRunMixing(t *testing.T) {
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, steps, err := globalCanaryDaysAndSteps(run.Add(time.Hour), ifs.Run{Time: run})
	if err != nil || len(days) != 14 {
		t.Fatal(err)
	}
	staged := syntheticGlobalCanaryStaged(run, steps)
	staged[len(staged)/2].run = run.Add(-24 * time.Hour)
	if err := validateGlobalCanaryStaged(run, steps, staged); err == nil || !strings.Contains(err.Error(), "uses run") {
		t.Fatalf("mixed-run validation error = %v", err)
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

func TestWebMercatorTouchedLandPredicateDoesNotBleedIntoNeighbouringTexel(t *testing.T) {
	const zoom = 3
	width := int(tiledelivery.TileSize) << zoom
	pixels := make([]byte, width*width)
	const landColumn, landRow = 1024, 1024
	pixels[landRow*width+landColumn] = 1
	isLand, err := webMercatorTouchedLandPredicate(pixels, zoom)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := isLand(webMercatorTestTexelBounds(width, landColumn, landRow)); err != nil || !got {
		t.Fatalf("marked display texel = %v, %v; want land", got, err)
	}
	if got, err := isLand(webMercatorTestTexelBounds(width, landColumn+1, landRow)); err != nil || got {
		t.Fatalf("neighbouring display texel = %v, %v; want water", got, err)
	}
	expanded := webMercatorTestTexelBounds(width, landColumn, landRow)
	expanded.East = webMercatorTestTexelBounds(width, landColumn+1, landRow).East
	if got, err := isLand(expanded); err != nil || !got {
		t.Fatalf("source-cell-sized lookup spanning marked texel = %v, %v; want land", got, err)
	}
	if _, err := isLand(delivery.GridBounds{West: 0, East: 1, South: 0, North: math.NaN()}); err == nil {
		t.Fatal("non-finite land lookup was accepted")
	}
}

func TestGlobalCanaryWebMercatorLandPredicateRasterizesExactDisplayGrid(t *testing.T) {
	archive := testLandZip(t)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	const zoom = 0
	width := int(tiledelivery.TileSize) << zoom
	pixels := make([]byte, width*width)
	pixels[20*width+10] = 1
	runner := &globalCanaryLandFakeRunner{payload: pixels}
	authority := newLandAuthority(t.TempDir(), server.Client(), runner, landSource{
		URL: server.URL, Size: int64(len(archive)), SHA256: hex.EncodeToString(digest[:]),
	})
	isLand, err := globalCanaryWebMercatorLandPredicate(context.Background(), authority, t.TempDir(), zoom)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := isLand(webMercatorTestTexelBounds(width, 10, 20)); err != nil || !got {
		t.Fatalf("rasterized display texel = %v, %v; want land", got, err)
	}
	if len(runner.invocations) != 2 || runner.invocations[0].command != "ogr2ogr" || runner.invocations[1].command != "gdal_rasterize" {
		t.Fatalf("land commands = %+v; want projection then rasterization", runner.invocations)
	}
	projection := strings.Join(runner.invocations[0].arguments, " ")
	for _, required := range []string{"-f GPKG", "-t_srs EPSG:3857", "-nlt MULTIPOLYGON", "-nln land"} {
		if !strings.Contains(projection, required) {
			t.Errorf("Web Mercator ogr2ogr args %q missing %q", projection, required)
		}
	}
	arguments := strings.Join(runner.invocations[1].arguments, " ")
	for _, required := range []string{
		"-at", "-a_srs EPSG:3857",
		"-te -20037508.342789244 -20037508.342789244 20037508.342789244 20037508.342789244",
		"-ts 256 256", "-l land",
	} {
		if !strings.Contains(arguments, required) {
			t.Errorf("Web Mercator gdal_rasterize args %q missing %q", arguments, required)
		}
	}
}

type globalCanaryLandInvocation struct {
	command   string
	arguments []string
}

type globalCanaryLandFakeRunner struct {
	payload     []byte
	invocations []globalCanaryLandInvocation
}

func (runner *globalCanaryLandFakeRunner) Run(_ context.Context, command string, arguments ...string) ([]byte, error) {
	runner.invocations = append(runner.invocations, globalCanaryLandInvocation{
		command: command, arguments: append([]string(nil), arguments...),
	})
	switch command {
	case "ogr2ogr":
		if len(arguments) < 2 {
			return nil, errors.New("ogr2ogr destination is missing")
		}
		return nil, os.WriteFile(arguments[len(arguments)-2], []byte("projected fixture"), 0o600)
	case "gdal_rasterize":
		return nil, os.WriteFile(arguments[len(arguments)-1], runner.payload, 0o600)
	default:
		return nil, errors.New("unexpected command " + command)
	}
}

func webMercatorTestTexelBounds(width, column, row int) delivery.GridBounds {
	longitude := func(edge int) float64 {
		return float64(edge)/float64(width)*360 - 180
	}
	latitude := func(edge int) float64 {
		return math.Atan(math.Sinh(math.Pi*(1-2*float64(edge)/float64(width)))) * 180 / math.Pi
	}
	return delivery.GridBounds{
		West: longitude(column), East: longitude(column + 1),
		North: latitude(row), South: latitude(row + 1),
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

func syntheticGlobalCanaryStaged(run time.Time, steps []int) []stagedFrame {
	result := make([]stagedFrame, 0, len(steps)*len(ifs.RequiredSurfaceFields()))
	for _, step := range steps {
		for _, parameter := range ifs.RequiredSurfaceFields() {
			field, err := ifsPipelineField(parameter)
			if err != nil {
				panic(err)
			}
			result = append(result, stagedFrame{
				path: fmt.Sprintf("/%s-%03d.grib2", parameter, step), field: field, step: step,
				run: run, resolution: 28, spacing: .25,
			})
		}
	}
	return result
}

func selectedDayDate(t *testing.T, days []dayPlan, staged, selected []stagedFrame) string {
	t.Helper()
	selectedSet := stagedFrameSet(selected)
	for _, day := range days {
		want := stagedFrameSet(stagedForDayWindow(staged, day.Date, globalOutlookDecodeWindow))
		if reflect.DeepEqual(selectedSet, want) {
			return day.Date.Format("2006-01-02")
		}
	}
	t.Fatalf("selected frame window does not match one planned day: %v", selectedSet)
	return ""
}

func stagedFrameSet(frames []stagedFrame) map[string]bool {
	result := make(map[string]bool, len(frames))
	for _, frame := range frames {
		result[fmt.Sprintf("%s/%d", frame.field, frame.step)] = true
	}
	return result
}

func syntheticGlobalCatalog(config forecasttiles.Config) forecasttiles.Catalog {
	return forecasttiles.Catalog{
		Version: 1, ReleaseID: config.ReleaseID, Period: config.Period, Tier: config.Tier,
		Date: config.Date.Format("2006-01-02"), MaxZoom: config.MaxZoom,
		Points: []forecasttiles.PointTile{{Zoom: 3}}, Scalars: []forecasttiles.ScalarTile{{Zoom: 3}},
	}
}

func timeZoneUTC(_, _ float64) (*time.Location, error) { return time.UTC, nil }
func landEverywhere(delivery.GridBounds) (bool, error) { return true, nil }
