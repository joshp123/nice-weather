package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/appserver"
	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/destinations"
	"nice-weather-finder/internal/era5climate"
	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/era5publication"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/product"
	"nice-weather-finder/internal/providers/era5hourly"
	weatherupdate "nice-weather-finder/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	domain := product.Europe()
	var err error
	switch os.Args[1] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		web := flags.String("web", "./dist", "frontend build directory")
		artifacts := flags.String("artifacts", "./artifacts", "artifact directory")
		listen := flags.String("listen", appserver.DefaultListen, "listen address")
		if err = flags.Parse(os.Args[2:]); err == nil {
			err = appserver.Serve(appserver.Config{WebRoot: *web, ArtifactRoot: *artifacts, Listen: *listen})
		}
	case "global-canary":
		flags := flag.NewFlagSet("global-canary", flag.ContinueOnError)
		out := flags.String("out", "./.cache/global-canary-output", "isolated canary output directory")
		cache := flags.String("cache", "./.cache/weather", "weather and Natural Earth cache directory")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			var summary weatherupdate.GlobalCanarySummary
			summary, err = weatherupdate.RunGlobalCanary(ctx, weatherupdate.GlobalCanaryConfig{Out: *out, Cache: *cache})
			if err == nil {
				fmt.Printf("global canary: IFS %s; %d worldwide days %s through %s; %d messages; source %d bytes; staging %d files/%d bytes; max prepared %d files/%d bytes; output %d point tiles + %d scalar tiles, %d files/%d bytes; max working bytes=%d; bounded maxima metrics=%d records=%d scalar=%d bytes; stage=%s prepare=%s tiles=%s total=%s\n",
					summary.Run.Format(time.RFC3339), summary.DayCount, summary.FirstDate.Format("2006-01-02"), summary.LastDate.Format("2006-01-02"), summary.MessageCount, summary.SourceBytes,
					summary.StagedFiles, summary.StagedBytes, summary.MaxPreparedFiles, summary.MaxPreparedBytes,
					summary.PointTiles, summary.ScalarTiles, summary.OutputFiles, summary.OutputBytes,
					summary.MaxWorkingBytes, summary.MaxMetricCells, summary.MaxDisplayRecords, summary.MaxScalarBytes,
					summary.StageElapsed.Round(time.Millisecond), summary.PrepareElapsed.Round(time.Millisecond), summary.TileElapsed.Round(time.Millisecond), summary.Elapsed.Round(time.Millisecond))
			}
		}
	case "forecast-world":
		flags := flag.NewFlagSet("forecast-world", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		staging := flags.String("staging", "./.cache/forecast-staging", "source and prepared-file staging directory")
		cache := flags.String("cache", "./.cache/weather", "Natural Earth cache directory")
		if err = flags.Parse(os.Args[2:]); err == nil {
			if flags.NArg() != 0 {
				err = errors.New("forecast-world accepts no positional arguments")
			} else {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				var summary weatherupdate.ForecastProductionSummary
				summary, err = weatherupdate.RunForecastProduction(ctx, weatherupdate.ForecastProductionConfig{
					ArtifactRoot: *out, StagingRoot: *staging, CacheRoot: *cache,
				})
				if err == nil {
					fmt.Printf("forecast publication: %s; IFS %s; ICON-EU %s; %d global + %d Europe-detail days %s through %s; staged %d files/%d bytes; max prepared %d files/%d bytes; built %d and reused %d products; installed %d files/%d bytes; stage=%s prepare=%s build=%s publish=%s total=%s\n",
						summary.IndexID, summary.IFSRun.Format(time.RFC3339), formatOptionalTime(summary.ICONRun),
						summary.GlobalDays, summary.EuropeDays, summary.FirstDate.Format(time.DateOnly), summary.LastDate.Format(time.DateOnly),
						summary.StagedFiles, summary.StagedBytes, summary.MaxPreparedFiles, summary.MaxPreparedBytes,
						summary.BuiltProducts, summary.ReusedProducts, summary.OutputFiles, summary.OutputBytes,
						summary.StageElapsed.Round(time.Millisecond), summary.PrepareElapsed.Round(time.Millisecond), summary.BuildElapsed.Round(time.Millisecond),
						summary.PublishElapsed.Round(time.Millisecond), summary.Elapsed.Round(time.Millisecond))
					var best destinations.WorldwideBuildSummary
					var built bool
					best, built, err = buildBestWeatherWhenReady(ctx, *out)
					if err != nil {
						err = fmt.Errorf("worldwide forecast published, but best-weather artifact failed: %w", err)
					} else if built {
						fmt.Printf("best weather worldwide: %d places -> %d bytes in %s\n", best.ScannedPlaces, best.Bytes, best.Elapsed.Round(time.Millisecond))
					} else {
						fmt.Println("best weather: waiting for the worldwide place index")
					}
				}
			}
		}
	case "era5-history-month":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5HistoryMonthCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, era5hourly.BuildDailyShard)
	case "era5-climate-month":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5ClimateMonthCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, buildERA5ClimateMonth)
	case "era5-publication-authority":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5PublicationAuthorityCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, era5publication.Build)
	case "verify-climate":
		err = runVerifyClimateCommand(os.Args[2:], os.Stdout, os.Stderr, era5climateproduct.VerifyCompletePublication)
	case "places":
		flags := flag.NewFlagSet("places", flag.ContinueOnError)
		source := flags.String("source", "", "directory containing pinned GeoNames extracts")
		out := flags.String("out", "./artifacts", "artifact directory")
		sourceDate := flags.String("source-date", "", "GeoNames extract date in YYYY-MM-DD")
		releaseID := flags.String("release-id", "", "immutable release ID (default places-YYYYMMDD)")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if *releaseID == "" {
				*releaseID = "places-" + strings.ReplaceAll(*sourceDate, "-", "")
			}
			var summary places.Summary
			summary, err = places.BuildAndPublishGlobal(ctx, *source, *out, *sourceDate, *releaseID, domain.Bounds())
			if err == nil {
				fmt.Printf("places: %d places, %d keys, %d shards, %.1f MiB gzip\n", summary.PlaceCount, summary.SearchKeyCount, summary.ShardCount, float64(summary.GzipBytes)/(1024*1024))
				var best destinations.WorldwideBuildSummary
				var built bool
				best, built, err = buildBestWeatherWhenReady(ctx, *out)
				if err != nil {
					err = fmt.Errorf("place index published, but best-weather artifact failed: %w", err)
				} else if built {
					fmt.Printf("best weather: %d places -> %d bytes in %s\n", best.ScannedPlaces, best.Bytes, best.Elapsed.Round(time.Millisecond))
				} else {
					fmt.Println("best weather: waiting for the worldwide forecast")
				}
			}
		}
	case "best-weather":
		flags := flag.NewFlagSet("best-weather", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			var summary destinations.WorldwideBuildSummary
			summary, err = destinations.BuildWorldwideCurrent(ctx, *out)
			if err == nil {
				fmt.Printf("best weather: %d places -> %d bytes in %s\n", summary.ScannedPlaces, summary.Bytes, summary.Elapsed.Round(time.Millisecond))
			}
		}
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "niceweather:", err)
		os.Exit(1)
	}
}

func usage() {
	writeUsage(os.Stderr)
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: niceweather <global-canary|forecast-world|era5-history-month|era5-publication-authority|era5-climate-month|verify-climate|serve|places|best-weather> [options]")
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return "not selected"
	}
	return value.Format(time.RFC3339)
}

func buildBestWeatherWhenReady(ctx context.Context, artifactRoot string) (destinations.WorldwideBuildSummary, bool, error) {
	for _, pointer := range []string{filepath.Join(artifactRoot, "forecast", "current.json"), filepath.Join(artifactRoot, "places", "current.json")} {
		if _, err := os.Stat(pointer); err != nil {
			if os.IsNotExist(err) {
				return destinations.WorldwideBuildSummary{}, false, nil
			}
			return destinations.WorldwideBuildSummary{}, false, err
		}
	}
	summary, err := destinations.BuildWorldwideCurrent(ctx, artifactRoot)
	return summary, err == nil, err
}

type climateVerificationRunner func(string) (era5climateproduct.PublicationVerification, error)

type era5HistoryMonthRunner func(context.Context, era5hourly.DailyShardBuildConfig) (era5hourly.DailyShardBuildSummary, error)

type era5ClimateMonthConfig struct {
	PlanPath                   string
	PlanSHA256                 string
	PublicationAuthority       string
	PublicationAuthoritySHA256 string
	DisplayMask                string
	ShardRoot                  string
	ProductRoot                string
	ReleaseID                  string
}

type era5ClimateMonthSummary struct {
	ReleaseID   string
	Period      string
	Shards      int
	ShardBytes  uint64
	Cells       int
	CellDays    uint64
	OutputFiles int
	OutputBytes uint64
	IndexState  era5climateproduct.IndexState
	Months      int
	Elapsed     time.Duration
}

type era5ClimateMonthRunner func(context.Context, era5ClimateMonthConfig) (era5ClimateMonthSummary, error)

type era5PublicationAuthorityRunner func(context.Context, era5publication.Config) (era5publication.Summary, error)

func runERA5PublicationAuthorityCommand(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	run era5PublicationAuthorityRunner,
) error {
	if ctx == nil || stdout == nil || stderr == nil || run == nil {
		return errors.New("era5-publication-authority command dependencies are unavailable")
	}
	flags := flag.NewFlagSet("era5-publication-authority", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "absolute accepted gzip JSON cell plan path (required)")
	planSHA := flags.String("plan-sha256", "", "trusted compressed plan SHA-256 (required)")
	landCache := flags.String("land-cache", "", "absolute Natural Earth cache directory (required)")
	out := flags.String("out", "", "absolute output directory for the mask and authority sidecar (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("era5-publication-authority accepts no positional arguments")
	}
	config := era5publication.Config{
		PlanPath: *plan, PlanSHA256: *planSHA,
		LandCacheRoot: *landCache, OutputRoot: *out,
	}
	for _, required := range []struct{ name, value string }{
		{"--plan", config.PlanPath}, {"--plan-sha256", config.PlanSHA256},
		{"--land-cache", config.LandCacheRoot}, {"--out", config.OutputRoot},
	} {
		if required.value == "" {
			return fmt.Errorf("era5-publication-authority %s is required", required.name)
		}
	}
	summary, err := run(ctx, config)
	if err != nil {
		return err
	}
	state := "built"
	if summary.Existing {
		state = "already valid"
	}
	_, err = fmt.Fprintf(stdout,
		"ERA5 publication authority %s: %d land centres; display mask SHA-256 %s; authority SHA-256 %s; %s.\n",
		state, summary.CentreLandCells, summary.DisplayMaskSHA256,
		summary.PublicationAuthoritySHA256, summary.OutputRoot,
	)
	if err != nil {
		return fmt.Errorf("write ERA5 publication authority summary: %w", err)
	}
	return nil
}

func runERA5ClimateMonthCommand(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	run era5ClimateMonthRunner,
) error {
	if ctx == nil || stdout == nil || stderr == nil || run == nil {
		return errors.New("era5-climate-month command dependencies are unavailable")
	}
	flags := flag.NewFlagSet("era5-climate-month", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "absolute accepted gzip JSON cell plan path (required)")
	planSHA := flags.String("plan-sha256", "", "trusted compressed plan SHA-256 (required)")
	authority := flags.String("publication-authority", "", "absolute checksum-trusted publication-authority path (required)")
	authoritySHA := flags.String("publication-authority-sha256", "", "trusted publication-authority SHA-256 (required)")
	displayMask := flags.String("display-mask", "", "absolute canonical display-mask path (required)")
	shards := flags.String("shards", "", "absolute directory containing 2006-2025 YYYY-MM.ned shards (required)")
	out := flags.String("out", "", "absolute ERA5 climate product root (required)")
	releaseID := flags.String("release-id", "", "immutable month release ID (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("era5-climate-month accepts no positional arguments")
	}
	config := era5ClimateMonthConfig{
		PlanPath: *plan, PlanSHA256: *planSHA,
		PublicationAuthority: *authority, PublicationAuthoritySHA256: *authoritySHA,
		DisplayMask: *displayMask,
		ShardRoot:   *shards, ProductRoot: *out, ReleaseID: *releaseID,
	}
	for _, required := range []struct{ name, value string }{
		{"--plan", config.PlanPath}, {"--plan-sha256", config.PlanSHA256},
		{"--publication-authority", config.PublicationAuthority},
		{"--publication-authority-sha256", config.PublicationAuthoritySHA256},
		{"--display-mask", config.DisplayMask}, {"--shards", config.ShardRoot},
		{"--out", config.ProductRoot}, {"--release-id", config.ReleaseID},
	} {
		if required.value == "" {
			return fmt.Errorf("era5-climate-month %s is required", required.name)
		}
	}
	summary, err := run(ctx, config)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"ERA5 climate month built and published: %s; release %s; %s index with %d month(s); %d shards/%d bytes; %d cells/%d complete local days; %d files/%d bytes; %s.\n",
		summary.Period, summary.ReleaseID, summary.IndexState, summary.Months,
		summary.Shards, summary.ShardBytes,
		summary.Cells, summary.CellDays, summary.OutputFiles, summary.OutputBytes,
		summary.Elapsed.Round(time.Millisecond),
	)
	if err != nil {
		return fmt.Errorf("write ERA5 climate month summary: %w", err)
	}
	return nil
}

func buildERA5ClimateMonth(ctx context.Context, config era5ClimateMonthConfig) (summary era5ClimateMonthSummary, err error) {
	started := time.Now()
	if ctx == nil {
		return summary, errors.New("ERA5 climate month requires a context")
	}
	for _, candidate := range []struct{ name, path string }{
		{"plan", config.PlanPath}, {"publication authority", config.PublicationAuthority},
		{"display mask", config.DisplayMask},
		{"shard root", config.ShardRoot}, {"product root", config.ProductRoot},
	} {
		if candidate.path == "" || !filepath.IsAbs(candidate.path) || filepath.Clean(candidate.path) != candidate.path || candidate.path == string(filepath.Separator) {
			return summary, fmt.Errorf("ERA5 climate %s must be a clean absolute path other than the filesystem root", candidate.name)
		}
	}
	plan, err := era5hourly.LoadCellPlan(config.PlanPath, config.PlanSHA256)
	if err != nil {
		return summary, err
	}
	authority, err := era5climateproduct.LoadPublicationAuthority(
		config.PublicationAuthority, config.PublicationAuthoritySHA256, plan,
	)
	if err != nil {
		return summary, err
	}
	displayMask, err := era5climateproduct.LoadDisplayMaskWithAuthority(config.DisplayMask, plan, authority)
	if err != nil {
		return summary, fmt.Errorf("load ERA5 climate display mask: %w", err)
	}
	window := climate.DefaultReferenceWindow()
	cellDays := uint64(0)
	for index := 0; index < plan.CellCount(); index++ {
		days, ok := plan.ExpectedDayCount(index)
		if !ok || days < 1 {
			return summary, fmt.Errorf("ERA5 climate plan cell %d lacks its exact local-day count", index)
		}
		cellDays += uint64(days)
	}

	summary = era5ClimateMonthSummary{
		ReleaseID: config.ReleaseID,
		Period:    fmt.Sprintf("month-%02d", plan.Month()),
		Shards:    window.LastYear - window.FirstYear + 1,
		Cells:     plan.CellCount(),
		CellDays:  cellDays,
	}
	err = withERA5DailyShards(
		ctx, config.ShardRoot, time.Month(plan.Month()), plan.Cells(), plan.SHA256(),
		func(shards []*era5hourly.DailyShard, shardBytes uint64) error {
			summary.ShardBytes = shardBytes
			builder, err := era5climateproduct.New(era5climateproduct.Config{
				StagingRoot: config.ProductRoot,
				ReleaseID:   config.ReleaseID,
				MaximumZoom: era5climateproduct.MaximumZoom,
			}, plan, displayMask)
			if err != nil {
				return err
			}
			if err := era5climate.ReduceMonth(ctx, era5climate.Config{Month: time.Month(plan.Month())}, shards, builder); err != nil {
				return err
			}
			releaseRoot := filepath.Join(config.ProductRoot, "releases", config.ReleaseID)
			catalog, err := era5climateproduct.ValidateRelease(releaseRoot, plan, displayMask)
			if err != nil {
				return fmt.Errorf("validate installed ERA5 climate release: %w", err)
			}
			if catalog.ReleaseID != config.ReleaseID || catalog.Period != summary.Period {
				return errors.New("installed ERA5 climate release identity changed")
			}
			summary.OutputFiles, summary.OutputBytes, err = inspectERA5ClimateRelease(releaseRoot)
			if err != nil {
				return err
			}
			publication, err := era5climateproduct.PublishValidatedMonth(ctx, config.ProductRoot, catalog)
			if err != nil {
				return fmt.Errorf("publish ERA5 climate index: %w", err)
			}
			summary.IndexState = publication.Index.State
			summary.Months = len(publication.Index.Months)
			return nil
		},
		era5hourly.OpenDailyShardForPlan,
		func(shard *era5hourly.DailyShard) error { return shard.Close() },
	)
	if err != nil {
		return era5ClimateMonthSummary{}, err
	}
	summary.Elapsed = time.Since(started)
	return summary, nil
}

type era5DailyShardOpener func(string, int, time.Month, []era5hourly.Cell, string) (*era5hourly.DailyShard, error)
type era5DailyShardCloser func(*era5hourly.DailyShard) error

func withERA5DailyShards(
	ctx context.Context,
	root string,
	month time.Month,
	cells []era5hourly.Cell,
	planSHA256 string,
	use func([]*era5hourly.DailyShard, uint64) error,
	open era5DailyShardOpener,
	closeShard era5DailyShardCloser,
) (returnErr error) {
	if ctx == nil || use == nil || open == nil || closeShard == nil {
		return errors.New("ERA5 climate shard lifecycle is unavailable")
	}
	window := climate.DefaultReferenceWindow()
	shards := make([]*era5hourly.DailyShard, 0, window.LastYear-window.FirstYear+1)
	defer func() {
		for index := len(shards) - 1; index >= 0; index-- {
			returnErr = errors.Join(returnErr, closeShard(shards[index]))
		}
	}()
	var shardBytes uint64
	for year := window.FirstYear; year <= window.LastYear; year++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, fmt.Sprintf("%04d-%02d.ned", year, month))
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect ERA5 climate shard %d: %w", year, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
			return fmt.Errorf("ERA5 climate shard %d must be a non-empty regular file", year)
		}
		shard, err := open(path, year, month, cells, planSHA256)
		if err != nil {
			return fmt.Errorf("open ERA5 climate shard %d: %w", year, err)
		}
		shards = append(shards, shard)
		shardBytes += uint64(info.Size())
	}
	return use(shards, shardBytes)
}

func inspectERA5ClimateRelease(root string) (files int, bytes uint64, err error) {
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("installed ERA5 climate release contains symlink %s", path)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installed ERA5 climate release contains non-regular file %s", path)
		}
		files++
		bytes += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if files == 0 || bytes == 0 {
		return 0, 0, errors.New("installed ERA5 climate release is empty")
	}
	return files, bytes, nil
}

func runERA5HistoryMonthCommand(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	run era5HistoryMonthRunner,
) error {
	if ctx == nil || stdout == nil || stderr == nil || run == nil {
		return errors.New("era5-history-month command dependencies are unavailable")
	}
	flags := flag.NewFlagSet("era5-history-month", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "absolute accepted gzip JSON cell plan path (required)")
	planSHA := flags.String("plan-sha256", "", "trusted compressed plan SHA-256 (required)")
	out := flags.String("out", "", "absolute immutable daily shard path (required)")
	staging := flags.String("staging", "", "absolute large-disk staging directory (required)")
	year := flags.Int("year", 0, "complete civil year from 2006 to 2025 (required)")
	month := flags.Int("month", 0, "calendar month from 1 to 12 (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("era5-history-month accepts no positional arguments")
	}
	if *planSHA == "" {
		return errors.New("--plan-sha256 is required")
	}
	summary, err := run(ctx, era5hourly.DailyShardBuildConfig{
		PlanPath: *plan, PlanSHA256: *planSHA,
		OutputPath: *out, StagingRoot: *staging,
		Year: *year, Month: time.Month(*month),
	})
	if err != nil {
		return err
	}
	if summary.Diagnostics != "" {
		if _, err = fmt.Fprintf(stderr, "ERA5 hourly source diagnostics:\n%s\n", summary.Diagnostics); err != nil {
			return fmt.Errorf("write ERA5 source diagnostics: %w", err)
		}
	}
	if summary.Existing {
		_, err = fmt.Fprintf(stdout, "ERA5 daily history already valid: %04d-%02d; %d cells; %d bytes; plan %s; checked in %s.\n",
			*year, *month, summary.Cells, summary.ShardBytes, summary.PlanSHA256, summary.Elapsed.Round(time.Millisecond))
		return err
	}
	_, err = fmt.Fprintf(stdout, "ERA5 daily history built: %04d-%02d; %d cells; %d frames -> %d local days; %d source bytes in %d requests; %d frame bytes; %d shard bytes; plan %s; %s.\n",
		*year, *month, summary.Cells, summary.Frames, summary.Days,
		summary.ResponseBytes, summary.Requests, summary.FrameBytes, summary.ShardBytes,
		summary.PlanSHA256, summary.Elapsed.Round(time.Millisecond))
	return err
}

func runVerifyClimateCommand(args []string, stdout, stderr io.Writer, verify climateVerificationRunner) error {
	if stdout == nil || stderr == nil {
		return errors.New("verify-climate output streams are unavailable")
	}
	if verify == nil {
		return errors.New("verify-climate runner is unavailable")
	}
	flags := flag.NewFlagSet("verify-climate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "./artifacts", "artifact directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("verify-climate accepts no positional arguments")
	}
	verification, err := verify(*out)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Climate publication valid: 12 months, %d-%d, index %s.\n",
		verification.Index.ReferencePeriod.FirstYear, verification.Index.ReferencePeriod.LastYear, verification.Pointer.IndexID); err != nil {
		return fmt.Errorf("write climate verification summary: %w", err)
	}
	return nil
}
