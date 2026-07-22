package update

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/tiledelivery"

	"github.com/ringsaturn/tzf"
)

const (
	globalCanarySourceID = "ecmwf-ifs-control"
	iconEUSourceID       = "dwd-icon-eu"
)

const (
	globalCanaryMaxZoom                = 3
	webMercatorHalfWorldMetres         = 20037508.342789244
	webMercatorMaximumLatitude         = 85.0511287798066
	webMercatorLandCoordinateTolerance = 1e-7
)

var globalCanaryBounds = delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}

// GlobalCanaryConfig identifies a private, disposable worldwide forecast
// build. Out is an isolated assembly root; it is never interpreted as a
// production artifact root and no current pointer is read or written.
type GlobalCanaryConfig struct {
	Out   string
	Cache string
	Now   func() time.Time
}

// GlobalCanarySummary is the exact evidence emitted by one complete
// fourteen-day worldwide IFS canary.
type GlobalCanarySummary struct {
	Run               time.Time
	FirstDate         time.Time
	LastDate          time.Time
	DayCount          int
	MessageCount      int
	SourceBytes       int64
	StagedFiles       int
	StagedBytes       int64
	MaxPreparedFiles  int
	MaxPreparedBytes  int64
	PointTiles        int
	ScalarTiles       int
	OutputFiles       int
	OutputBytes       int64
	MaxWorkingBytes   int64
	MaxMetricCells    uint64
	MaxDisplayRecords uint64
	MaxScalarBytes    uint64
	StageElapsed      time.Duration
	PrepareElapsed    time.Duration
	TileElapsed       time.Duration
	Elapsed           time.Duration
}

type globalCanaryDaySummary struct {
	Date          time.Time
	Messages      int
	PreparedFiles int
	PreparedBytes int64
	Catalog       forecasttiles.Catalog
	PrepareTime   time.Duration
	TileTime      time.Duration
}

type globalCanaryAssembler struct {
	prepare func(context.Context, []stagedFrame, string) (pipeline.PreparedSeries, func() error, error)
	build   func(context.Context, forecasttiles.Config) (forecasttiles.Catalog, error)
}

type globalCanaryPreparedDay struct {
	series  pipeline.PreparedSeries
	close   func() error
	files   int
	bytes   int64
	elapsed time.Duration
}

// RunGlobalCanary builds exactly fourteen worldwide IFS days without
// publication. Downloads are shared by the run. Prepared frames are bounded
// to one day and removed before the next day starts.
func RunGlobalCanary(ctx context.Context, config GlobalCanaryConfig) (summary GlobalCanarySummary, resultErr error) {
	started := time.Now()
	if config.Out == "" || config.Cache == "" {
		return summary, errors.New("global canary output and cache directories are required")
	}
	out, err := filepath.Abs(config.Out)
	if err != nil {
		return summary, fmt.Errorf("resolve global canary output: %w", err)
	}
	cache, err := filepath.Abs(config.Cache)
	if err != nil {
		return summary, fmt.Errorf("resolve global canary cache: %w", err)
	}
	if filepath.Clean(out) == filepath.Clean(cache) {
		return summary, errors.New("global canary output and cache must be separate directories")
	}
	for _, published := range []string{"current", "previous", "releases"} {
		if _, statErr := os.Lstat(filepath.Join(out, published)); statErr == nil {
			return summary, fmt.Errorf("global canary output contains production release state %q", published)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return summary, fmt.Errorf("inspect global canary output: %w", statErr)
		}
	}
	// Share the production cache lock, but do not perform the production
	// updater's abandoned-staging cleanup from this private canary.
	cacheLock, err := acquireDirectoryLock(cache)
	if err != nil {
		return summary, err
	}
	defer func() { resultErr = errors.Join(resultErr, cacheLock.Close()) }()
	outputLock, err := acquireDirectoryLock(out)
	if err != nil {
		return summary, err
	}
	defer func() { resultErr = errors.Join(resultErr, outputLock.Close()) }()

	now := time.Now().UTC()
	if config.Now != nil {
		now = config.Now().UTC()
	}
	scratchParent := filepath.Join(cache, "global-canary-scratch")
	if err := os.MkdirAll(scratchParent, 0o755); err != nil {
		return summary, err
	}
	scratch, err := os.MkdirTemp(scratchParent, "run-")
	if err != nil {
		return summary, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(scratch)) }()

	httpClient := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConns: 16, MaxIdleConnsPerHost: 8,
		DialContext:         (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}}
	ifsClient := ifs.NewClient(httpClient)
	ifsClient.Now = func() time.Time { return now }
	run, err := ifsClient.DiscoverLatestCompleteWorldwide(ctx)
	if err != nil {
		return summary, err
	}
	days, steps, err := globalCanaryDaysAndSteps(now, run)
	if err != nil {
		return summary, err
	}
	summary.Run = run.Time.UTC()
	summary.FirstDate = days[0].Date
	summary.LastDate = days[len(days)-1].Date
	summary.DayCount = len(days)

	downloadRoot := filepath.Join(scratch, "downloads")
	stageStarted := time.Now()
	staged, err := stageIFS(ctx, ifsClient, run, steps, downloadRoot)
	if err != nil {
		return summary, err
	}
	summary.StageElapsed = time.Since(stageStarted)
	summary.MessageCount = len(staged)
	summary.StagedFiles, summary.StagedBytes, err = treeStats(downloadRoot)
	if err != nil {
		return summary, fmt.Errorf("measure IFS staging: %w", err)
	}
	// Exact-range downloads validate every indexed length before committing the
	// file, so the on-disk byte count is also the selected source-message count.
	summary.SourceBytes = summary.StagedBytes

	decoder, err := pipeline.NewDecoder(nil, globalCanaryBounds)
	if err != nil {
		return summary, err
	}
	preparedRoot := filepath.Join(scratch, "prepared")
	if err := os.MkdirAll(preparedRoot, 0o755); err != nil {
		return summary, err
	}
	if err := validateGlobalCanaryStaged(run.Time, steps, staged); err != nil {
		return summary, err
	}
	// Every selected message uses the same IFS grid. Keep the first bounded day
	// alive through land-mask creation, then consume it as day one. It must not
	// be converted twice merely to discover geometry.
	firstSelected := stagedForDayWindow(staged, days[0].Date, globalOutlookDecodeWindow)
	firstStarted := time.Now()
	firstSeries, closeFirst, err := prepareGlobalCanaryDay(ctx, decoder, firstSelected, preparedRoot)
	if err != nil {
		return summary, err
	}
	firstPrepareElapsed := time.Since(firstStarted)
	defer func() { resultErr = errors.Join(resultErr, closeFirst()) }()
	firstFiles, firstBytes, err := treeStats(preparedRoot)
	if err != nil {
		return summary, fmt.Errorf("measure first prepared IFS day: %w", err)
	}
	if _, err := globalCanarySeriesSpec(firstSeries); err != nil {
		return summary, err
	}
	landAuthority := NewLandAuthority(cache, httpClient, decoder.Runner)
	isLand, err := globalCanaryWebMercatorLandPredicate(ctx, landAuthority, scratch, globalCanaryMaxZoom)
	if err != nil {
		return summary, fmt.Errorf("Natural Earth worldwide display mask: %w", err)
	}
	locate, err := globalTimezoneLocator()
	if err != nil {
		return summary, err
	}
	releaseID := fmt.Sprintf("ifs-control-%s-%s-%s", run.Time.UTC().Format("2006010215"), summary.FirstDate.Format("20060102"), summary.LastDate.Format("20060102"))
	assembler := globalCanaryAssembler{
		prepare: func(ctx context.Context, selected []stagedFrame, root string) (pipeline.PreparedSeries, func() error, error) {
			return prepareGlobalCanaryDay(ctx, decoder, selected, root)
		},
		build: forecasttiles.BuildDay,
	}
	firstPrepared := &globalCanaryPreparedDay{
		series: firstSeries, close: closeFirst, files: firstFiles, bytes: firstBytes, elapsed: firstPrepareElapsed,
	}
	daySummaries, installed, err := assembleGlobalCanaryDays(ctx, out, releaseID, run.Time, days, staged, preparedRoot, locate, isLand, firstPrepared, assembler)
	if err != nil {
		return summary, err
	}
	for _, day := range daySummaries {
		summary.MaxPreparedFiles = max(summary.MaxPreparedFiles, day.PreparedFiles)
		summary.MaxPreparedBytes = max(summary.MaxPreparedBytes, day.PreparedBytes)
		summary.PointTiles += len(day.Catalog.Points)
		summary.ScalarTiles += len(day.Catalog.Scalars)
		summary.MaxMetricCells = max(summary.MaxMetricCells, day.Catalog.Stats.MaxMetricCells)
		summary.MaxDisplayRecords = max(summary.MaxDisplayRecords, day.Catalog.Stats.MaxDisplayRecords)
		summary.MaxScalarBytes = max(summary.MaxScalarBytes, day.Catalog.Stats.MaxScalarBytes)
		summary.PrepareElapsed += day.PrepareTime
		summary.TileElapsed += day.TileTime
	}
	summary.OutputFiles, summary.OutputBytes, err = treeStats(installed)
	if err != nil {
		return summary, fmt.Errorf("measure global canary output: %w", err)
	}
	// The assembly tree grows monotonically. Its final size plus the retained
	// downloads and largest one-day prepared set is the conservative peak.
	summary.MaxWorkingBytes = summary.StagedBytes + summary.MaxPreparedBytes + summary.OutputBytes
	summary.Elapsed = time.Since(started)
	return summary, nil
}

func globalCanaryDaysAndSteps(now time.Time, run ifs.Run) ([]dayPlan, []int, error) {
	available, err := ifs.ForecastSteps(run)
	if err != nil {
		return nil, nil, err
	}
	days, err := planDaysForCoverageCount(now, forecastCoverage{}, forecastCoverage{
		provider: "IFS", run: run.Time, maxForecastHours: available[len(available)-1],
	}, worldwideForecastPolicy, productDays)
	if err != nil {
		return nil, nil, err
	}
	steps := stepsForDays(run.Time, days, globalOutlookDecodeWindow, available)
	if len(steps) == 0 {
		return nil, nil, errors.New("worldwide IFS canary has no source steps")
	}
	return days, steps, nil
}

func validateGlobalCanaryStaged(run time.Time, steps []int, staged []stagedFrame) error {
	want := make(map[pipeline.Field]map[int]bool)
	for _, parameter := range ifs.RequiredSurfaceFields() {
		field, err := ifsPipelineField(parameter)
		if err != nil {
			return err
		}
		want[field] = make(map[int]bool, len(steps))
		for _, step := range steps {
			want[field][step] = true
		}
	}
	seen := make(map[pipeline.Field]map[int]bool, len(want))
	for _, frame := range staged {
		if !frame.run.Equal(run.UTC()) {
			return fmt.Errorf("staged IFS frame uses run %s; want %s", frame.run.Format(time.RFC3339), run.UTC().Format(time.RFC3339))
		}
		if frame.resolution != 28 || frame.spacing != .25 {
			return errors.New("staged worldwide IFS frame changed source resolution")
		}
		stepsForField, ok := want[frame.field]
		if !ok || !stepsForField[frame.step] {
			return fmt.Errorf("unexpected staged IFS %s step %d", frame.field, frame.step)
		}
		if seen[frame.field] == nil {
			seen[frame.field] = make(map[int]bool)
		}
		if seen[frame.field][frame.step] {
			return fmt.Errorf("duplicate staged IFS %s step %d", frame.field, frame.step)
		}
		seen[frame.field][frame.step] = true
	}
	for field, fieldSteps := range want {
		for step := range fieldSteps {
			if !seen[field][step] {
				return fmt.Errorf("missing staged IFS %s step %d", field, step)
			}
		}
	}
	return nil
}

func prepareGlobalCanaryDay(ctx context.Context, decoder *pipeline.Decoder, selected []stagedFrame, scratch string) (pipeline.PreparedSeries, func() error, error) {
	return prepareStagedDay(ctx, decoder, selected, scratch, globalCanarySourceID, "worldwide IFS")
}

// prepareStagedDay converts one provider's already staged day envelope once.
// The returned close function owns every prepared backing file and removes it
// after the exact day/tier assembly finishes.
func prepareStagedDay(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame, scratch, sourceID, progressLabel string) (pipeline.PreparedSeries, func() error, error) {
	series, frames, err := prepareStagedFrames(ctx, decoder, staged, scratch, sourceID, progressLabel)
	if err != nil {
		return nil, nil, err
	}
	return series, func() error {
		var closeErr error
		for _, frame := range frames {
			closeErr = errors.Join(closeErr, frame.Close())
		}
		return closeErr
	}, nil
}

func assembleGlobalCanaryDays(
	ctx context.Context,
	out, releaseID string,
	run time.Time,
	days []dayPlan,
	staged []stagedFrame,
	preparedRoot string,
	locate forecasttiles.LocateTimezone,
	land forecasttiles.TouchesLand,
	first *globalCanaryPreparedDay,
	assembler globalCanaryAssembler,
) (summaries []globalCanaryDaySummary, installed string, resultErr error) {
	if len(days) != productDays {
		return nil, "", fmt.Errorf("global canary requires exactly %d days; got %d", productDays, len(days))
	}
	if run.Location() != time.UTC || run.Hour() != 12 || run.Minute() != 0 || run.Second() != 0 || run.Nanosecond() != 0 {
		return nil, "", errors.New("global canary requires one exact 12 UTC IFS run")
	}
	if out == "" || !filepath.IsAbs(out) || releaseID == "" || preparedRoot == "" || !filepath.IsAbs(preparedRoot) || locate == nil || land == nil || first == nil || first.series == nil || first.close == nil || assembler.prepare == nil || assembler.build == nil {
		return nil, "", errors.New("global canary assembly is incomplete")
	}
	for index, day := range days {
		if !day.Date.Equal(productDate(day.Date)) {
			return nil, "", fmt.Errorf("global canary day %d is not a UTC calendar date", index+1)
		}
		if index > 0 && !day.Date.Equal(days[index-1].Date.AddDate(0, 0, 1)) {
			return nil, "", errors.New("global canary dates must be consecutive and ordered")
		}
	}

	final := filepath.Join(out, "canary-runs", releaseID)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, "", err
	}
	if err := cleanGlobalCanaryTransactions(out); err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(final); err == nil {
		return nil, "", fmt.Errorf("immutable global canary run already exists: %s", final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	staging, err := os.MkdirTemp(out, ".global-canary-")
	if err != nil {
		return nil, "", err
	}
	installedOK := false
	defer func() {
		if !installedOK {
			resultErr = errors.Join(resultErr, os.RemoveAll(staging))
		}
	}()

	summaries = make([]globalCanaryDaySummary, 0, productDays)
	for index, day := range days {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		selected := stagedForDayWindow(staged, day.Date, globalOutlookDecodeWindow)
		if len(selected) == 0 {
			return nil, "", fmt.Errorf("global canary day %s has no staged messages", day.Date.Format("2006-01-02"))
		}

		prepared := first
		if index > 0 {
			prepareStarted := time.Now()
			series, closeDay, err := assembler.prepare(ctx, selected, preparedRoot)
			if err != nil {
				return nil, "", fmt.Errorf("prepare global canary day %s: %w", day.Date.Format("2006-01-02"), err)
			}
			preparedFiles, preparedBytes, statErr := treeStats(preparedRoot)
			if statErr != nil {
				return nil, "", errors.Join(fmt.Errorf("measure prepared global canary day %s: %w", day.Date.Format("2006-01-02"), statErr), closeDay())
			}
			prepared = &globalCanaryPreparedDay{
				series: series, close: closeDay, files: preparedFiles, bytes: preparedBytes, elapsed: time.Since(prepareStarted),
			}
		}

		buildStarted := time.Now()
		catalog, buildErr := assembler.build(ctx, forecasttiles.Config{
			Root: staging, ReleaseID: releaseID, Period: day.Date.Format("2006-01-02"), Tier: "global",
			Date: day.Date, MaxZoom: globalCanaryMaxZoom, Series: prepared.series, Locate: locate, Land: land,
		})
		buildElapsed := time.Since(buildStarted)
		closeErr := prepared.close()
		remainingFiles, _, statErr := treeStats(preparedRoot)
		if buildErr != nil {
			return nil, "", errors.Join(fmt.Errorf("build global canary day %s: %w", day.Date.Format("2006-01-02"), buildErr), closeErr, statErr)
		}
		if closeErr != nil {
			return nil, "", fmt.Errorf("close prepared global canary day %s: %w", day.Date.Format("2006-01-02"), closeErr)
		}
		if statErr != nil {
			return nil, "", fmt.Errorf("inspect cleaned global canary scratch: %w", statErr)
		}
		if remainingFiles != 0 {
			return nil, "", fmt.Errorf("global canary day %s left %d prepared files", day.Date.Format("2006-01-02"), remainingFiles)
		}
		if catalog.ReleaseID != releaseID || catalog.Period != day.Date.Format("2006-01-02") || catalog.Date != catalog.Period || catalog.Tier != "global" || catalog.MaxZoom != globalCanaryMaxZoom {
			return nil, "", fmt.Errorf("global canary day %s returned mismatched catalog identity", day.Date.Format("2006-01-02"))
		}
		catalogPath := filepath.Join(staging, "forecast-days", catalog.Period, catalog.Tier, "forecast-tiles.json")
		info, err := os.Stat(catalogPath)
		if err != nil {
			return nil, "", fmt.Errorf("global canary day %s did not install its catalog: %w", day.Date.Format("2006-01-02"), err)
		}
		if !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("global canary day %s catalog is not a regular file", day.Date.Format("2006-01-02"))
		}
		summaries = append(summaries, globalCanaryDaySummary{
			Date: day.Date, Messages: len(selected), PreparedFiles: prepared.files, PreparedBytes: prepared.bytes,
			Catalog: catalog, PrepareTime: prepared.elapsed, TileTime: buildElapsed,
		})
		fmt.Printf("global canary day %d/%d %s: %d messages; prepared %d files/%d bytes in %s; tiles %d point + %d scalar in %s\n",
			index+1, len(days), day.Date.Format("2006-01-02"), len(selected), prepared.files, prepared.bytes,
			prepared.elapsed.Round(time.Millisecond), len(catalog.Points), len(catalog.Scalars), buildElapsed.Round(time.Millisecond))
	}
	if len(summaries) != productDays {
		return nil, "", fmt.Errorf("global canary assembled %d/%d days", len(summaries), productDays)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, "", err
	}
	if err := os.Rename(staging, final); err != nil {
		return nil, "", fmt.Errorf("install complete global canary run: %w", err)
	}
	installedOK = true
	return summaries, final, nil
}

func cleanGlobalCanaryTransactions(out string) error {
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".global-canary-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(out, entry.Name())); err != nil {
			return fmt.Errorf("remove abandoned global canary transaction: %w", err)
		}
	}
	return nil
}

func stagedPrepareRequest(frame stagedFrame, scratch, sourceID string) pipeline.DecodeRequest {
	return pipeline.DecodeRequest{
		Path: frame.path, SourceID: sourceID, Field: frame.field, Run: frame.run.UTC(),
		NativeResolutionKM: frame.resolution, CellDegrees: frame.spacing, ScratchDirectory: scratch,
	}
}

func globalCanaryPrepareRequest(frame stagedFrame, scratch string) pipeline.DecodeRequest {
	return stagedPrepareRequest(frame, scratch, globalCanarySourceID)
}

func prepareStagedFrames(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame, scratch, sourceID, progressLabel string) (pipeline.PreparedSeries, []*pipeline.PreparedFrame, error) {
	if sourceID == "" || progressLabel == "" {
		return nil, nil, errors.New("prepared forecast source identity is required")
	}
	frames := make([]*pipeline.PreparedFrame, len(staged))
	var completed atomic.Int64
	err := runWorkers(ctx, len(staged), func(workerCtx context.Context, index int) error {
		item := staged[index]
		prepared, err := decoder.PrepareFrame(workerCtx, stagedPrepareRequest(item, scratch, sourceID))
		if err != nil {
			return fmt.Errorf("prepare %s step %d: %w", item.field, item.step, err)
		}
		frames[index] = prepared
		count := completed.Add(1)
		if count%25 == 0 || count == int64(len(staged)) {
			fmt.Printf("prepared %d/%d %s fields\n", count, len(staged), progressLabel)
		}
		return nil
	})
	if err != nil {
		closePreparedFrames(frames)
		return nil, nil, err
	}

	// Workers write fixed indexes. Assemble only after every conversion
	// succeeds so each field retains the provider's deterministic input order.
	series := make(pipeline.PreparedSeries)
	for index, item := range staged {
		series[item.field] = append(series[item.field], frames[index])
	}
	return series, frames, nil
}

func closePreparedFrames(frames []*pipeline.PreparedFrame) {
	for _, frame := range frames {
		_ = frame.Close()
	}
}

func globalCanarySeriesSpec(series pipeline.PreparedSeries) (delivery.GridSpec, error) {
	var spec delivery.GridSpec
	for _, frames := range series {
		for _, frame := range frames {
			metadata, err := frame.Metadata()
			if err != nil {
				return delivery.GridSpec{}, err
			}
			if metadata.SourceID != globalCanarySourceID {
				return delivery.GridSpec{}, fmt.Errorf("prepared source is %q; want %q", metadata.SourceID, globalCanarySourceID)
			}
			if spec.Rows == 0 {
				spec = metadata.Grid
			} else if spec != metadata.Grid {
				return delivery.GridSpec{}, errors.New("worldwide prepared fields do not share one grid")
			}
		}
	}
	if spec.Rows == 0 {
		return delivery.GridSpec{}, errors.New("worldwide prepared series is empty")
	}
	return spec, nil
}

func globalCanaryWebMercatorLandPredicate(ctx context.Context, authority *LandAuthority, scratch string, zoom uint8) (forecasttiles.TouchesLand, error) {
	if authority == nil || authority.runner == nil {
		return nil, errors.New("land authority is not configured")
	}
	shapePath, err := authority.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	if zoom > 5 {
		return nil, errors.New("display land mask zoom exceeds the bounded canary limit")
	}
	width := int(tiledelivery.TileSize) << zoom
	temporary, err := os.MkdirTemp(scratch, ".global-canary-land-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	projected := filepath.Join(temporary, "land.gpkg")
	projectionArguments := []string{
		"-f", "GPKG", "-t_srs", "EPSG:3857", "-nlt", "MULTIPOLYGON", "-nln", "land",
		projected, shapePath,
	}
	if _, err := authority.runner.Run(ctx, "ogr2ogr", projectionArguments...); err != nil {
		return nil, fmt.Errorf("project Natural Earth land into Web Mercator: %w", err)
	}
	output := filepath.Join(temporary, "land.bil")
	arguments := []string{
		"-q", "-of", "EHdr", "-ot", "Byte", "-burn", "1", "-init", "0", "-at",
		"-a_srs", "EPSG:3857",
		"-te", landCoordinate(-webMercatorHalfWorldMetres), landCoordinate(-webMercatorHalfWorldMetres), landCoordinate(webMercatorHalfWorldMetres), landCoordinate(webMercatorHalfWorldMetres),
		"-ts", fmt.Sprint(width), fmt.Sprint(width),
		"-l", "land", projected, output,
	}
	if _, err := authority.runner.Run(ctx, "gdal_rasterize", arguments...); err != nil {
		return nil, fmt.Errorf("rasterize Web Mercator Natural Earth land: %w", err)
	}
	pixels, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("read Web Mercator Natural Earth land: %w", err)
	}
	return webMercatorTouchedLandPredicate(pixels, zoom)
}

func webMercatorTouchedLandPredicate(pixels []byte, zoom uint8) (forecasttiles.TouchesLand, error) {
	if zoom > 5 {
		return nil, errors.New("display land mask zoom exceeds the bounded canary limit")
	}
	width := int(tiledelivery.TileSize) << zoom
	if len(pixels) != width*width {
		return nil, fmt.Errorf("display land mask has %d bytes; want %d", len(pixels), width*width)
	}
	for index, value := range pixels {
		if value != 0 && value != 1 {
			return nil, fmt.Errorf("display land mask cell %d has value %d; want 0 or 1", index, value)
		}
	}
	return func(bounds delivery.GridBounds) (bool, error) {
		coordinates := []float64{bounds.West, bounds.East, bounds.South, bounds.North}
		for _, coordinate := range coordinates {
			if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
				return false, errors.New("land lookup bounds must be finite")
			}
		}
		mercatorNorth := webMercatorMaximumLatitude
		if bounds.West < -180 || bounds.East > 180 || bounds.West >= bounds.East || bounds.South >= bounds.North || bounds.North <= -mercatorNorth || bounds.South >= mercatorNorth {
			return false, errors.New("land lookup bounds are outside Web Mercator")
		}
		bounds.North = min(bounds.North, mercatorNorth)
		bounds.South = max(bounds.South, -mercatorNorth)
		west := (bounds.West + 180) / 360 * float64(width)
		east := (bounds.East + 180) / 360 * float64(width)
		north := webMercatorPixelEdge(bounds.North, width)
		south := webMercatorPixelEdge(bounds.South, width)
		// Use only raster texels whose centres are inside the requested bounds.
		// This keeps a wider provider-cell lookup from borrowing land that lies
		// in the outside part of a merely overlapping coastal display texel.
		firstColumn := max(0, int(math.Ceil(west-.5-webMercatorLandCoordinateTolerance)))
		lastColumn := min(width-1, int(math.Ceil(east-.5-webMercatorLandCoordinateTolerance))-1)
		firstRow := max(0, int(math.Ceil(north-.5-webMercatorLandCoordinateTolerance)))
		lastRow := min(width-1, int(math.Ceil(south-.5-webMercatorLandCoordinateTolerance))-1)
		if firstColumn > lastColumn || firstRow > lastRow {
			return false, errors.New("land lookup bounds contain no display texel")
		}
		for row := firstRow; row <= lastRow; row++ {
			for column := firstColumn; column <= lastColumn; column++ {
				if pixels[row*width+column] == 1 {
					return true, nil
				}
			}
		}
		return false, nil
	}, nil
}

func webMercatorPixelEdge(latitude float64, width int) float64 {
	radians := latitude * math.Pi / 180
	return (1 - math.Asinh(math.Tan(radians))/math.Pi) / 2 * float64(width)
}

func globalTimezoneLocator() (forecasttiles.LocateTimezone, error) {
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return nil, err
	}
	cache := make(map[string]*time.Location)
	return func(longitude, latitude float64) (*time.Location, error) {
		name := finder.GetTimezoneName(longitude, latitude)
		if name == "" {
			return time.UTC, nil
		}
		if location, ok := cache[name]; ok {
			return location, nil
		}
		location, err := time.LoadLocation(name)
		if err == nil {
			cache[name] = location
		}
		return location, err
	}, nil
}
