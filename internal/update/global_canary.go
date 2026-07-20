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
	"sync/atomic"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/ifs"

	"github.com/ringsaturn/tzf"
)

const globalCanarySourceID = "ecmwf-ifs-control"

var globalCanaryBounds = delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}

// GlobalCanaryConfig identifies a private, disposable worldwide forecast
// build. Out is an isolated assembly root; it is never interpreted as a
// production artifact root and no current pointer is read or written.
type GlobalCanaryConfig struct {
	Out   string
	Cache string
	Now   func() time.Time
}

// GlobalCanarySummary is the exact evidence emitted by one worldwide IFS day.
type GlobalCanarySummary struct {
	Run               time.Time
	Date              time.Time
	MessageCount      int
	SourceBytes       int64
	StagedFiles       int
	StagedBytes       int64
	PreparedFiles     int
	PreparedBytes     int64
	PointTiles        int
	ScalarTiles       int
	OutputFiles       int
	OutputBytes       int64
	MaxMetricCells    uint64
	MaxDisplayRecords uint64
	MaxScalarBytes    uint64
	Elapsed           time.Duration
}

// RunGlobalCanary builds one real worldwide IFS day without publication.
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
	date, steps, err := globalCanaryDayAndSteps(now, run)
	if err != nil {
		return summary, err
	}
	summary.Run, summary.Date = run.Time.UTC(), date

	downloadRoot := filepath.Join(scratch, "downloads")
	staged, err := stageIFS(ctx, ifsClient, run, steps, downloadRoot)
	if err != nil {
		return summary, err
	}
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
	series, frames, err := prepareGlobalCanaryFrames(ctx, decoder, staged, preparedRoot)
	defer func() {
		for _, frame := range frames {
			resultErr = errors.Join(resultErr, frame.Close())
		}
	}()
	if err != nil {
		return summary, err
	}
	summary.PreparedFiles, summary.PreparedBytes, err = treeStats(preparedRoot)
	if err != nil {
		return summary, fmt.Errorf("measure prepared IFS frames: %w", err)
	}
	sourceSpec, err := globalCanarySeriesSpec(series)
	if err != nil {
		return summary, err
	}
	landAuthority := NewLandAuthority(cache, httpClient, decoder.Runner)
	landMask, err := landAuthority.CenterMask(ctx, sourceSpec)
	if err != nil {
		return summary, fmt.Errorf("Natural Earth worldwide source-centre mask: %w", err)
	}
	isLand, err := sourceCenterLandPredicate(landMask)
	if err != nil {
		return summary, err
	}
	locate, err := globalTimezoneLocator()
	if err != nil {
		return summary, err
	}
	releaseID := fmt.Sprintf("ifs-control-%s-%s", run.Time.UTC().Format("2006010215"), date.Format("20060102"))
	catalog, err := forecasttiles.BuildDay(ctx, forecasttiles.Config{
		Root: out, ReleaseID: releaseID, Period: date.Format("2006-01-02"), Tier: "global",
		Date: date, MaxZoom: 3, Series: series, Locate: locate, Land: isLand,
	})
	if err != nil {
		return summary, err
	}
	summary.PointTiles, summary.ScalarTiles = len(catalog.Points), len(catalog.Scalars)
	summary.MaxMetricCells = catalog.Stats.MaxMetricCells
	summary.MaxDisplayRecords = catalog.Stats.MaxDisplayRecords
	summary.MaxScalarBytes = catalog.Stats.MaxScalarBytes
	artifactRoot := filepath.Join(out, "forecast-days", date.Format("2006-01-02"), "global")
	summary.OutputFiles, summary.OutputBytes, err = treeStats(artifactRoot)
	if err != nil {
		return summary, fmt.Errorf("measure global canary output: %w", err)
	}
	summary.Elapsed = time.Since(started)
	return summary, nil
}

func globalCanaryDayAndSteps(now time.Time, run ifs.Run) (time.Time, []int, error) {
	available, err := ifs.ForecastSteps(run)
	if err != nil {
		return time.Time{}, nil, err
	}
	days, err := planDaysForCoverageCount(now, forecastCoverage{}, forecastCoverage{
		provider: "IFS", run: run.Time, maxForecastHours: available[len(available)-1],
	}, worldwideForecastPolicy, 1)
	if err != nil {
		return time.Time{}, nil, err
	}
	steps := stepsForDays(run.Time, days[:1], globalOutlookDecodeWindow, available)
	if len(steps) == 0 {
		return time.Time{}, nil, errors.New("worldwide IFS canary has no source steps")
	}
	return days[0].Date, steps, nil
}

func globalCanaryPrepareRequest(frame stagedFrame, scratch string) pipeline.DecodeRequest {
	return pipeline.DecodeRequest{
		Path: frame.path, SourceID: globalCanarySourceID, Field: frame.field, Run: frame.run.UTC(),
		NativeResolutionKM: frame.resolution, CellDegrees: frame.spacing, ScratchDirectory: scratch,
	}
}

func prepareGlobalCanaryFrames(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame, scratch string) (pipeline.PreparedSeries, []*pipeline.PreparedFrame, error) {
	frames := make([]*pipeline.PreparedFrame, len(staged))
	var completed atomic.Int64
	err := runWorkers(ctx, len(staged), func(workerCtx context.Context, index int) error {
		item := staged[index]
		prepared, err := decoder.PrepareFrame(workerCtx, globalCanaryPrepareRequest(item, scratch))
		if err != nil {
			return fmt.Errorf("prepare %s step %d: %w", item.field, item.step, err)
		}
		frames[index] = prepared
		count := completed.Add(1)
		if count%25 == 0 || count == int64(len(staged)) {
			fmt.Printf("prepared %d/%d worldwide IFS fields\n", count, len(staged))
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

func sourceCenterLandPredicate(mask pipeline.FloatGrid) (forecasttiles.IsLand, error) {
	if err := mask.Validate(); err != nil {
		return nil, fmt.Errorf("source-centre land mask: %w", err)
	}
	spec := mask.Spec
	if spec.Bounds != globalCanaryBounds {
		return nil, errors.New("source-centre land mask must cover the canonical world")
	}
	width := (spec.Bounds.East - spec.Bounds.West) / float64(spec.Cols)
	height := (spec.Bounds.North - spec.Bounds.South) / float64(spec.Rows)
	return func(longitude, latitude float64) (bool, error) {
		if math.IsNaN(longitude) || math.IsInf(longitude, 0) || math.IsNaN(latitude) || math.IsInf(latitude, 0) {
			return false, errors.New("land coordinate must be finite")
		}
		for longitude < -180 {
			longitude += 360
		}
		for longitude >= 180 {
			longitude -= 360
		}
		if latitude < -90 || latitude > 90 {
			return false, nil
		}
		column := int(math.Floor((longitude + 180) / width))
		row := int(math.Floor((90 - latitude) / height))
		if row == int(spec.Rows) && latitude == -90 {
			row--
		}
		if row < 0 || row >= int(spec.Rows) || column < 0 || column >= int(spec.Cols) {
			return false, nil
		}
		return mask.Values[row*int(spec.Cols)+column] == 1, nil
	}, nil
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
