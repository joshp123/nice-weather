package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastassembly"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/product"
	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/providers/ifs"
)

const forecastProductionMinimumFreeBytes = int64(6 << 30)

const (
	forecastSourcePrefix = "forecast-source-"
	forecastSourceMarker = ".nice-weather-forecast-source-v1"
	forecastSourceOwner  = "nice-weather-finder forecast source transaction v1\n"
)

// ForecastProductionConfig owns one complete worldwide forecast refresh.
// StagingRoot may be on a large external volume. ArtifactRoot always owns the
// private candidate and its atomic install into forecast/runs.
type ForecastProductionConfig struct {
	ArtifactRoot string
	StagingRoot  string
	CacheRoot    string
	Now          func() time.Time
}

// ForecastProductionSummary is bounded operational evidence from one refresh.
type ForecastProductionSummary struct {
	IndexID          string
	IFSRun           time.Time
	ICONRun          time.Time
	FirstDate        time.Time
	LastDate         time.Time
	GlobalDays       int
	EuropeDays       int
	IFSMessages      int
	ICONMessages     int
	StagedFiles      int
	StagedBytes      int64
	MaxPreparedFiles int
	MaxPreparedBytes int64
	BuiltProducts    int
	ReusedProducts   int
	OutputFiles      int
	OutputBytes      int64
	StageElapsed     time.Duration
	PrepareElapsed   time.Duration
	BuildElapsed     time.Duration
	PublishElapsed   time.Duration
	Elapsed          time.Duration
	CandidateRoot    string
	Pointer          forecastruns.Pointer
}

type forecastProductionDependencies struct {
	discoverIFS    func(context.Context) (ifs.Run, error)
	discoverDWD    func(context.Context, time.Time) (dwd.Run, error)
	stageIFS       func(context.Context, ifs.Run, []int, string) ([]stagedFrame, error)
	stageDWD       func(context.Context, dwd.Run, []int, string) ([]stagedFrame, error)
	newDecoder     func(delivery.GridBounds) (*pipeline.Decoder, error)
	land           func(context.Context, *pipeline.Decoder, string, string, uint8) (forecasttiles.TouchesLand, error)
	locate         func() (forecasttiles.LocateTimezone, error)
	prepare        func(context.Context, *pipeline.Decoder, []stagedFrame, string, string, string) (pipeline.PreparedSeries, func() error, error)
	build          func(context.Context, forecastassembly.Config) (forecastassembly.Product, error)
	buildRange     func(context.Context, forecastrange.Config) (forecastrange.Product, error)
	cleanCandidate func(string) error
	publish        func(context.Context, string, string) (forecastruns.Pointer, error)
	freeBytes      func(string) (int64, uint64, error)
}

// RunForecastProduction stages each provider once, builds exact day/tier
// products one at a time and changes the forecast pointer only after the whole
// immutable run validates.
func RunForecastProduction(ctx context.Context, config ForecastProductionConfig) (ForecastProductionSummary, error) {
	if ctx == nil {
		return ForecastProductionSummary{}, errors.New("forecast production requires a context")
	}
	now := time.Now().UTC()
	if config.Now != nil {
		now = config.Now().UTC()
	}
	httpClient := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConns: 16, MaxIdleConnsPerHost: 8,
		DialContext:         (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}}
	ifsClient := ifs.NewClient(httpClient)
	ifsClient.Now = func() time.Time { return now }
	dependencies := forecastProductionDependencies{
		discoverIFS: ifsClient.DiscoverLatestCompleteWorldwide,
		discoverDWD: func(ctx context.Context, now time.Time) (dwd.Run, error) {
			return (dwd.Discoverer{Client: httpClient}).LatestCompleteRun(ctx, now)
		},
		stageIFS: func(ctx context.Context, run ifs.Run, steps []int, root string) ([]stagedFrame, error) {
			return stageIFS(ctx, ifsClient, run, steps, root)
		},
		stageDWD: func(ctx context.Context, run dwd.Run, steps []int, root string) ([]stagedFrame, error) {
			return stageDWD(ctx, httpClient, run, steps, root)
		},
		newDecoder: func(bounds delivery.GridBounds) (*pipeline.Decoder, error) {
			return pipeline.NewDecoder(nil, bounds)
		},
		land: func(ctx context.Context, decoder *pipeline.Decoder, cache, scratch string, zoom uint8) (forecasttiles.TouchesLand, error) {
			authority := NewLandAuthority(cache, httpClient, decoder.Runner)
			return globalCanaryWebMercatorLandPredicate(ctx, authority, scratch, zoom)
		},
		locate:     globalTimezoneLocator,
		prepare:    prepareStagedDay,
		build:      forecastassembly.BuildDay,
		buildRange: forecastrange.Build,
		cleanCandidate: func(root string) error {
			if err := forecastassembly.CleanAbandonedTransactions(root); err != nil {
				return err
			}
			return forecastrange.CleanAbandonedTransactions(root)
		},
		publish: forecastruns.Publish,
		freeBytes: func(path string) (int64, uint64, error) {
			var filesystem syscall.Statfs_t
			if err := syscall.Statfs(path, &filesystem); err != nil {
				return 0, 0, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return 0, 0, err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return 0, 0, errors.New("filesystem identity is unavailable")
			}
			return int64(filesystem.Bavail) * int64(filesystem.Bsize), uint64(stat.Dev), nil
		},
	}
	return runForecastProduction(ctx, config, now, dependencies)
}

func runForecastProduction(ctx context.Context, config ForecastProductionConfig, now time.Time, dependencies forecastProductionDependencies) (summary ForecastProductionSummary, resultErr error) {
	started := time.Now()
	if dependencies.discoverIFS == nil || dependencies.discoverDWD == nil || dependencies.stageIFS == nil || dependencies.stageDWD == nil ||
		dependencies.newDecoder == nil || dependencies.land == nil || dependencies.locate == nil || dependencies.prepare == nil ||
		dependencies.build == nil || dependencies.buildRange == nil || dependencies.cleanCandidate == nil || dependencies.publish == nil || dependencies.freeBytes == nil {
		return summary, errors.New("forecast production dependencies are incomplete")
	}
	artifactRoot, stagingRoot, cacheRoot, err := prepareForecastProductionRoots(config)
	if err != nil {
		return summary, err
	}
	locks, err := lockForecastProductionRoots(cacheRoot, artifactRoot, stagingRoot)
	if err != nil {
		return summary, err
	}
	defer func() {
		for index := len(locks) - 1; index >= 0; index-- {
			resultErr = errors.Join(resultErr, locks[index].Close())
		}
	}()
	if err := cleanForecastSourceTransactions(stagingRoot); err != nil {
		return summary, err
	}
	if err := preflightForecastProductionDisk(artifactRoot, stagingRoot, dependencies.freeBytes); err != nil {
		return summary, err
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}

	ifsRun, err := dependencies.discoverIFS(ctx)
	if err != nil {
		return summary, fmt.Errorf("discover complete worldwide IFS run: %w", err)
	}
	baselineDays, ifsSteps, err := globalCanaryDaysAndSteps(now, ifsRun)
	if err != nil {
		return summary, err
	}

	var iconRun dwd.Run
	if discovered, discoverErr := dependencies.discoverDWD(ctx, now); discoverErr != nil {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		return summary, fmt.Errorf("discover complete ICON-EU refinement run: %w", discoverErr)
	} else {
		iconRun = discovered
		if iconRun.StartedAt.IsZero() {
			return summary, errors.New("complete ICON-EU discovery returned an empty run")
		}
	}
	days := append([]dayPlan(nil), baselineDays...)
	if !iconRun.StartedAt.IsZero() {
		planned, planErr := planDaysForCoverageCount(now,
			forecastCoverage{provider: "ICON-EU", run: iconRun.StartedAt, maxForecastHours: dwd.MaxForecastHour},
			forecastCoverage{provider: "IFS", run: ifsRun.Time, maxForecastHours: ifsSteps[len(ifsSteps)-1]},
			worldwideForecastPolicy, productDays)
		if planErr != nil {
			return summary, planErr
		}
		for index := range days {
			if !planned[index].Date.Equal(days[index].Date) {
				return summary, errors.New("worldwide forecast plans disagree on local dates")
			}
			days[index].Detailed = index < 5 && planned[index].Detailed
			if days[index].Detailed {
				summary.EuropeDays++
			}
		}
	}

	summary.IFSRun = ifsRun.Time.UTC()
	summary.ICONRun = iconRun.StartedAt.UTC()
	summary.FirstDate = days[0].Date
	summary.LastDate = days[len(days)-1].Date
	summary.GlobalDays = len(days)
	summary.IndexID = forecastProductionID(ifsRun.Time, iconRun.StartedAt, summary.EuropeDays, summary.FirstDate, summary.LastDate)
	forecastRoot := filepath.Join(artifactRoot, "forecast")
	buildingRoot := filepath.Join(forecastRoot, ".candidate-"+summary.IndexID)
	finalRoot := filepath.Join(forecastRoot, "runs", summary.IndexID)
	summary.CandidateRoot = buildingRoot

	if exists, err := realDirectoryExists(finalRoot); err != nil {
		return summary, err
	} else if exists {
		summary.ReusedProducts = summary.GlobalDays + summary.EuropeDays + 1
		publishStarted := time.Now()
		summary.Pointer, err = dependencies.publish(ctx, artifactRoot, summary.IndexID)
		summary.PublishElapsed = time.Since(publishStarted)
		if err != nil {
			return summary, err
		}
		summary.CandidateRoot = finalRoot
		summary.OutputFiles, summary.OutputBytes, err = treeStats(finalRoot)
		summary.Elapsed = time.Since(started)
		return summary, err
	}
	if err := ensureRealDirectory(buildingRoot); err != nil {
		return summary, err
	}
	if err := dependencies.cleanCandidate(buildingRoot); err != nil {
		return summary, fmt.Errorf("clean private forecast candidate before retry: %w", err)
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if exists, statErr := realDirectoryExists(finalRoot); statErr == nil && exists {
			resultErr = fmt.Errorf("%w; installed immutable candidate retained for pointer retry at %s; source and prepared scratch were removed", resultErr, finalRoot)
			return
		}
		if entries, readErr := os.ReadDir(buildingRoot); readErr == nil && len(entries) > 0 {
			resultErr = fmt.Errorf("%w; private candidate retained for retry at %s; source and prepared scratch were removed", resultErr, buildingRoot)
		}
	}()

	sourceRoot, err := os.MkdirTemp(stagingRoot, forecastSourcePrefix)
	if err != nil {
		return summary, err
	}
	if err := markForecastSourceTransaction(sourceRoot); err != nil {
		return summary, errors.Join(err, os.RemoveAll(sourceRoot))
	}
	defer func() {
		if cleanupErr := os.RemoveAll(sourceRoot); cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean forecast source staging: %w", cleanupErr))
		}
	}()
	downloadRoot := filepath.Join(sourceRoot, "downloads")
	preparedRoot := filepath.Join(sourceRoot, "prepared")
	if err := os.MkdirAll(preparedRoot, 0o755); err != nil {
		return summary, err
	}

	stageStarted := time.Now()
	ifsStaged, err := dependencies.stageIFS(ctx, ifsRun, ifsSteps, downloadRoot)
	if err != nil {
		return summary, fmt.Errorf("stage complete worldwide IFS run: %w", err)
	}
	if err := validateGlobalCanaryStaged(ifsRun.Time, ifsSteps, ifsStaged); err != nil {
		return summary, err
	}
	summary.IFSMessages = len(ifsStaged)
	var iconStaged []stagedFrame
	if summary.EuropeDays > 0 {
		iconSteps := stepsForDWD(iconRun.StartedAt, days)
		iconStaged, err = dependencies.stageDWD(ctx, iconRun, iconSteps, downloadRoot)
		if err != nil {
			return summary, fmt.Errorf("stage selected ICON-EU refinement: %w", err)
		}
		if err := validateICONStaged(iconRun.StartedAt, iconSteps, iconStaged); err != nil {
			return summary, err
		}
		summary.ICONMessages = len(iconStaged)
	}
	summary.StageElapsed = time.Since(stageStarted)
	summary.StagedFiles, summary.StagedBytes, err = treeStats(downloadRoot)
	if err != nil {
		return summary, fmt.Errorf("measure staged forecast source: %w", err)
	}
	fmt.Printf("forecast sources: IFS %s, %d days/%d messages; ICON-EU %s, %d eligible days/%d messages; staging %d files/%d bytes in %s\n",
		summary.IFSRun.Format(time.RFC3339), summary.GlobalDays, summary.IFSMessages,
		optionalRun(summary.ICONRun), summary.EuropeDays, summary.ICONMessages,
		summary.StagedFiles, summary.StagedBytes, summary.StageElapsed.Round(time.Millisecond))

	globalDecoder, err := dependencies.newDecoder(globalCanaryBounds)
	if err != nil {
		return summary, err
	}
	europeDecoder, err := dependencies.newDecoder(product.Europe().Bounds())
	if err != nil {
		return summary, err
	}
	globalLand, err := dependencies.land(ctx, globalDecoder, cacheRoot, sourceRoot, 3)
	if err != nil {
		return summary, fmt.Errorf("prepare global forecast land mask: %w", err)
	}
	var europeLand forecasttiles.TouchesLand
	if summary.EuropeDays > 0 {
		europeLand, err = dependencies.land(ctx, europeDecoder, cacheRoot, sourceRoot, 5)
		if err != nil {
			return summary, fmt.Errorf("prepare ICON-EU forecast land mask: %w", err)
		}
	}
	locate, err := dependencies.locate()
	if err != nil {
		return summary, err
	}

	for index, day := range days {
		if err := buildForecastProductionTier(ctx, &summary, dependencies, buildingRoot, preparedRoot, day.Date,
			stagedForDayWindow(ifsStaged, day.Date, globalOutlookDecodeWindow), globalDecoder, locate,
			globalCanarySourceID, "worldwide IFS", forecastpoints.ProviderIFS, forecastpoints.TierGlobal, globalLand, index+1); err != nil {
			return summary, err
		}
		if day.Detailed {
			if err := buildForecastProductionTier(ctx, &summary, dependencies, buildingRoot, preparedRoot, day.Date,
				stagedForDayWindow(iconStaged, day.Date, europeDetailedDecodeWindow), europeDecoder, locate,
				iconEUSourceID, "ICON-EU", forecastpoints.ProviderICONEU, forecastpoints.TierEurope, europeLand, index+1); err != nil {
				return summary, err
			}
		}
	}
	rangeInputs, err := forecastProductionRangeInputs(buildingRoot, days)
	if err != nil {
		return summary, err
	}
	rangeRoot := filepath.Join(buildingRoot, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal)
	rangeReused, err := realDirectoryExists(rangeRoot)
	if err != nil {
		return summary, err
	}
	rangeStarted := time.Now()
	rangeProduct, err := dependencies.buildRange(ctx, forecastrange.Config{Root: buildingRoot, ReleaseID: summary.IndexID, Inputs: rangeInputs})
	if err != nil {
		return summary, fmt.Errorf("build worldwide 14-day forecast range: %w", err)
	}
	if rangeReused {
		summary.ReusedProducts++
	} else {
		summary.BuiltProducts++
	}
	summary.BuildElapsed += time.Since(rangeStarted)
	fmt.Printf("forecast range %s/global: %d files/%d bytes in %s\n", forecastrange.Period, rangeProduct.Stats.OutputFiles, rangeProduct.Stats.OutputBytes, time.Since(rangeStarted).Round(time.Millisecond))
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	if err := os.Rename(buildingRoot, finalRoot); err != nil {
		return summary, fmt.Errorf("install complete forecast candidate: %w", err)
	}
	if err := syncForecastProductionDirectory(filepath.Dir(finalRoot)); err != nil {
		return summary, fmt.Errorf("sync installed forecast candidate: %w", err)
	}
	summary.CandidateRoot = finalRoot

	publishStarted := time.Now()
	summary.Pointer, err = dependencies.publish(ctx, artifactRoot, summary.IndexID)
	summary.PublishElapsed = time.Since(publishStarted)
	if err != nil {
		return summary, err
	}
	summary.OutputFiles, summary.OutputBytes, err = treeStats(finalRoot)
	if err != nil {
		return summary, fmt.Errorf("forecast %s published, but measure installed run: %w", summary.IndexID, err)
	}
	summary.Elapsed = time.Since(started)
	return summary, nil
}

func forecastProductionRangeInputs(root string, days []dayPlan) ([]forecastrange.InputCatalog, error) {
	if len(days) != productDays {
		return nil, errors.New("forecast range requires the exact 14-day production plan")
	}
	inputs := make([]forecastrange.InputCatalog, len(days))
	for index, day := range days {
		date := day.Date.Format(time.DateOnly)
		relative := filepath.ToSlash(filepath.Join("forecast-days", date, forecastpoints.TierGlobal, "catalog.json"))
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect forecast range input %s: %w", date, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > forecastcatalog.MaximumCatalogBytes {
			return nil, fmt.Errorf("forecast range input %s is not a bounded regular catalogue", date)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read forecast range input %s safely: %w", date, err)
		}
		if len(payload) == 0 || len(payload) > forecastcatalog.MaximumCatalogBytes || int64(len(payload)) != info.Size() {
			return nil, fmt.Errorf("forecast range input %s changed while reading", date)
		}
		digest := sha256.Sum256(payload)
		inputs[index] = forecastrange.InputCatalog{Date: date, Path: relative, Bytes: uint64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
	}
	return inputs, nil
}

func buildForecastProductionTier(
	ctx context.Context,
	summary *ForecastProductionSummary,
	dependencies forecastProductionDependencies,
	buildingRoot, preparedRoot string,
	date time.Time,
	selected []stagedFrame,
	decoder *pipeline.Decoder,
	locate forecasttiles.LocateTimezone,
	sourceID, progressLabel, provider, tier string,
	land forecasttiles.TouchesLand,
	dayNumber int,
) (resultErr error) {
	period := date.Format(time.DateOnly)
	final := filepath.Join(buildingRoot, "forecast-days", period, tier)
	if exists, err := realDirectoryExists(final); err != nil {
		return err
	} else if exists {
		summary.ReusedProducts++
		fmt.Printf("forecast day %d/%d %s %s: reused complete private product\n", dayNumber, productDays, period, tier)
		return nil
	}
	if len(selected) == 0 {
		return fmt.Errorf("forecast day %s %s has no staged source messages", period, tier)
	}
	prepareStarted := time.Now()
	series, closePrepared, err := dependencies.prepare(ctx, decoder, selected, preparedRoot, sourceID, progressLabel)
	if err != nil {
		return fmt.Errorf("prepare forecast day %s %s: %w", period, tier, err)
	}
	preparedOpen := true
	defer func() {
		if preparedOpen {
			resultErr = errors.Join(resultErr, closePrepared())
		}
	}()
	preparedFiles, preparedBytes, err := treeStats(preparedRoot)
	if err != nil {
		return err
	}
	prepareElapsed := time.Since(prepareStarted)
	summary.PrepareElapsed += prepareElapsed
	summary.MaxPreparedFiles = max(summary.MaxPreparedFiles, preparedFiles)
	summary.MaxPreparedBytes = max(summary.MaxPreparedBytes, preparedBytes)

	buildStarted := time.Now()
	product, buildErr := dependencies.build(ctx, forecastassembly.Config{
		Root: buildingRoot, ReleaseID: summary.IndexID, Provider: provider, Tier: tier,
		Date: date, Series: series, Locate: locate, Land: land,
	})
	buildElapsed := time.Since(buildStarted)
	closeErr := closePrepared()
	preparedOpen = false
	if buildErr != nil {
		return errors.Join(fmt.Errorf("build forecast day %s %s: %w", period, tier, buildErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("clean prepared forecast day %s %s: %w", period, tier, closeErr)
	}
	remainingFiles, _, err := treeStats(preparedRoot)
	if err != nil {
		return fmt.Errorf("inspect prepared forecast cleanup: %w", err)
	}
	if remainingFiles != 0 {
		return fmt.Errorf("forecast day %s %s left %d prepared files", period, tier, remainingFiles)
	}
	summary.BuildElapsed += buildElapsed
	summary.BuiltProducts++
	fmt.Printf("forecast day %d/%d %s %s: %d messages; prepared %d files/%d bytes in %s; built %d files/%d bytes in %s\n",
		dayNumber, productDays, period, tier, len(selected), preparedFiles, preparedBytes, prepareElapsed.Round(time.Millisecond),
		product.Stats.OutputFiles, product.Stats.OutputBytes, buildElapsed.Round(time.Millisecond))
	return nil
}

func prepareForecastProductionRoots(config ForecastProductionConfig) (artifactRoot, stagingRoot, cacheRoot string, err error) {
	for name, value := range map[string]*string{
		"artifact": &config.ArtifactRoot, "staging": &config.StagingRoot, "cache": &config.CacheRoot,
	} {
		if *value == "" {
			return "", "", "", fmt.Errorf("forecast production %s root is required", name)
		}
		absolute, absErr := filepath.Abs(*value)
		if absErr != nil {
			return "", "", "", absErr
		}
		*value = filepath.Clean(absolute)
	}
	artifactRoot, stagingRoot, cacheRoot = config.ArtifactRoot, config.StagingRoot, config.CacheRoot
	for _, root := range []string{artifactRoot, filepath.Join(artifactRoot, "forecast"), filepath.Join(artifactRoot, "forecast", "runs"), stagingRoot, cacheRoot} {
		if err := ensureRealDirectory(root); err != nil {
			return "", "", "", err
		}
	}
	forecastRoot := filepath.Join(artifactRoot, "forecast")
	if stagingRoot == forecastRoot || strings.HasPrefix(stagingRoot, forecastRoot+string(filepath.Separator)) {
		return "", "", "", errors.New("forecast source staging must be outside the forecast artifact tree")
	}
	return artifactRoot, stagingRoot, cacheRoot, nil
}

func ensureRealDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("forecast production directory %s must be a real directory", path)
	}
	return nil
}

func lockForecastProductionRoots(roots ...string) ([]*updateLock, error) {
	locks := make([]*updateLock, 0, len(roots))
	seen := make(map[string]bool)
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		lock, err := acquireDirectoryLock(root)
		if err != nil {
			for index := len(locks) - 1; index >= 0; index-- {
				_ = locks[index].Close()
			}
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func preflightForecastProductionDisk(artifactRoot, stagingRoot string, free func(string) (int64, uint64, error)) error {
	if free == nil {
		return errors.New("forecast production disk preflight is unavailable")
	}
	checked := make(map[uint64]string)
	for _, root := range []string{artifactRoot, stagingRoot} {
		available, device, err := free(root)
		if err != nil {
			return fmt.Errorf("measure free space at %s: %w", root, err)
		}
		if _, ok := checked[device]; ok {
			continue
		}
		checked[device] = root
		if available < forecastProductionMinimumFreeBytes {
			return fmt.Errorf("forecast production needs at least %d bytes free on %s; found %d", forecastProductionMinimumFreeBytes, root, available)
		}
		fmt.Printf("forecast disk preflight: %s has %d bytes free; minimum %d\n", root, available, forecastProductionMinimumFreeBytes)
	}
	return nil
}

func cleanForecastSourceTransactions(stagingRoot string) error {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), forecastSourcePrefix) {
			continue
		}
		path := filepath.Join(stagingRoot, entry.Name())
		owned, err := isOwnedForecastSourceTransaction(path)
		if err != nil {
			return fmt.Errorf("inspect abandoned forecast source staging %s: %w", entry.Name(), err)
		}
		if !owned {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("clean abandoned forecast source staging %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func markForecastSourceTransaction(root string) error {
	marker := filepath.Join(root, forecastSourceMarker)
	if err := os.WriteFile(marker, []byte(forecastSourceOwner), 0o600); err != nil {
		return fmt.Errorf("mark forecast source transaction: %w", err)
	}
	return nil
}

func isOwnedForecastSourceTransaction(root string) (bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, nil
	}
	marker := filepath.Join(root, forecastSourceMarker)
	markerInfo, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		return false, err
	}
	return string(contents) == forecastSourceOwner, nil
}

func forecastProductionID(ifsRun, iconRun time.Time, europeDays int, first, last time.Time) string {
	id := fmt.Sprintf("ifs-%s-%s-%s", ifsRun.UTC().Format("2006010215"), first.Format("20060102"), last.Format("20060102"))
	if europeDays > 0 {
		return id + "-icon-" + iconRun.UTC().Format("2006010215")
	}
	return id + "-global"
}

func validateICONStaged(run time.Time, steps []int, staged []stagedFrame) error {
	if len(steps) == 0 {
		return errors.New("selected ICON-EU refinement has no source steps")
	}
	required := make(map[pipeline.Field]bool)
	for _, field := range dwd.RequiredFields() {
		mapped, err := dwdPipelineField(field.Name)
		if err != nil {
			return err
		}
		required[mapped] = true
	}
	wantSteps := make(map[int]bool, len(steps))
	for _, step := range steps {
		wantSteps[step] = true
	}
	seen := make(map[pipeline.Field]map[int]bool)
	for _, frame := range staged {
		if !frame.run.Equal(run.UTC()) || frame.resolution != 7 || frame.spacing != .0625 || !wantSteps[frame.step] {
			return errors.New("staged ICON-EU frame changed run, resolution or requested horizon")
		}
		if seen[frame.field] == nil {
			seen[frame.field] = make(map[int]bool)
		}
		if seen[frame.field][frame.step] {
			return fmt.Errorf("duplicate staged ICON-EU %s step %d", frame.field, frame.step)
		}
		seen[frame.field][frame.step] = true
	}
	complete := func(field pipeline.Field) bool {
		if len(seen[field]) != len(wantSteps) {
			return false
		}
		for step := range wantSteps {
			if !seen[field][step] {
				return false
			}
		}
		return true
	}
	for field := range required {
		if !complete(field) {
			return fmt.Errorf("missing required staged ICON-EU %s series", field)
		}
	}
	if len(seen[pipeline.Gust]) != 0 && !complete(pipeline.Gust) {
		return errors.New("staged ICON-EU gust series is partial")
	}
	direct, diffuse := len(seen[pipeline.SolarDirect]), len(seen[pipeline.SolarDiffuse])
	if (direct != 0 || diffuse != 0) && (!complete(pipeline.SolarDirect) || !complete(pipeline.SolarDiffuse)) {
		return errors.New("staged ICON-EU solar pair is partial")
	}
	for field := range seen {
		if !required[field] && field != pipeline.Gust && field != pipeline.SolarDirect && field != pipeline.SolarDiffuse {
			return fmt.Errorf("unexpected staged ICON-EU field %s", field)
		}
	}
	return nil
}

func realDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("forecast candidate entry %s is not a real directory", path)
	}
	return true, nil
}

func syncForecastProductionDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func optionalRun(run time.Time) string {
	if run.IsZero() {
		return "not selected"
	}
	return run.Format(time.RFC3339)
}
