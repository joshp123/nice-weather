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
	"nice-weather-finder/internal/climatecanary"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/destinations"
	"nice-weather-finder/internal/era5climate"
	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/product"
	"nice-weather-finder/internal/providers/agera5"
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
	case "agera5-canary":
		flags := flag.NewFlagSet("agera5-canary", flag.ContinueOnError)
		blockLatitude := flags.Int("block-lat", -1, "AgERA5 latitude block index (required, 0-112)")
		blockLongitude := flags.Int("block-lon", -1, "AgERA5 longitude block index (required, 0-112)")
		year := flags.Int("year", 0, "complete UTC calendar year (required)")
		staging := flags.String("staging", "./.cache/agera5-canary", "parent for disposable canary staging")
		synthetic := flags.Bool("synthetic", false, "exercise the canary without network access or a token")
		if err = flags.Parse(os.Args[2:]); err == nil {
			if flags.NArg() != 0 {
				err = fmt.Errorf("agera5-canary accepts no positional arguments")
			} else {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				var summary climatecanary.Summary
				summary, err = climatecanary.Run(ctx, climatecanary.Config{
					Block: agera5.BlockCoordinate{Latitude: *blockLatitude, Longitude: *blockLongitude},
					Year:  *year, StagingParent: *staging, TokenReader: os.Stdin, Synthetic: *synthetic,
				})
				if err == nil {
					fmt.Printf("AgERA5 canary: %s block %d,%d; %s through %s; %d/%d days; %d complete + %d no-data cells in %dx%d; %d extrema repairs (largest %.3f C), %d daytime-range clamps (%d below, %d above; largest %.3f C), %d dew-point caps (largest %.3f C); transfer %d bytes in %d responses/%d chunks; compressed %d bytes (largest %d, response limit %d); decoded %d bytes total, %d retained peak; sampled peak heap %d bytes; %s\n",
						summary.Source, summary.Block.Latitude, summary.Block.Longitude,
						summary.FirstDate.Format(time.DateOnly), summary.LastDate.Format(time.DateOnly),
						summary.ProcessedDays, summary.ExpectedDays, summary.CompleteCells, summary.NoDataCells,
						summary.Width, summary.Height, summary.TemperatureRepairs, summary.MaximumTemperatureRepairC,
						summary.ComfortTemperatureRepairs, summary.ComfortTemperatureBelowRangeRepairs, summary.ComfortTemperatureAboveRangeRepairs, summary.MaximumComfortTemperatureRepairC,
						summary.DewPointRepairs, summary.MaximumDewPointRepairC,
						summary.FetchedBytes, summary.Responses, summary.Chunks,
						summary.CompressedChunkBytes, summary.MaximumCompressedChunkBytes, summary.ChunkResponseLimitBytes,
						summary.DecodedChunkBytes, summary.PeakRetainedDecodedBytes, summary.SampledPeakHeapBytes, summary.Elapsed.Round(time.Millisecond))
				}
			}
		}
	case "climate-world":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runClimateWorldCommand(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr, weatherupdate.RunGlobalClimate)
	case "era5-history-month":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5HistoryMonthCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, era5hourly.BuildDailyShard)
	case "era5-climate-month":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5ClimateMonthCommand(ctx, os.Args[2:], os.Stdout, os.Stderr, buildERA5ClimateMonth)
	case "verify-climate":
		err = runVerifyClimateCommand(os.Args[2:], os.Stdout, os.Stderr, climatetiles.VerifyCompletePublication)
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
	fmt.Fprintln(output, "usage: niceweather <global-canary|forecast-world|agera5-canary|era5-history-month|era5-climate-month|climate-world|verify-climate|serve|places|best-weather> [options]")
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

type globalClimateRunner func(context.Context, weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error)

type climateVerificationRunner func(string, []climatetiles.VerificationProbe) (climatetiles.PublicationVerification, error)

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
	Elapsed     time.Duration
}

type era5ClimateMonthRunner func(context.Context, era5ClimateMonthConfig) (era5ClimateMonthSummary, error)

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
		"ERA5 climate month built: %s; release %s; %d shards/%d bytes; %d cells/%d complete local days; %d files/%d bytes; %s.\n",
		summary.Period, summary.ReleaseID, summary.Shards, summary.ShardBytes,
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
			return err
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
	verification, err := verify(*out, defaultClimateVerificationProbes())
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Climate publication valid: 12 months, %d-%d, index %s.\n",
		verification.Index.ReferencePeriod.FirstYear, verification.Index.ReferencePeriod.LastYear, verification.Pointer.IndexID); err != nil {
		return fmt.Errorf("write climate verification summary: %w", err)
	}
	for _, result := range verification.Probes {
		if !result.Record.Valid {
			if _, err := fmt.Fprintf(stdout, "%s: no data; scalar map is transparent.\n", result.Probe.Label); err != nil {
				return fmt.Errorf("write climate verification probe: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "%s: Nice %d%%; awful %d%%; poor week %.1f%%; median %d; cycling %d%%; comfort %d%%; dry %d%%; bright %d%%; calm %d%%; point and scalar match.\n",
			result.Probe.Label, result.Record.Nice.GoodOrBetterPercent(), result.Record.Nice.AwfulPercent(), float64(result.Record.Nice.PoorWeekPermille)/10, result.Record.Nice.MedianScore, result.Record.Cycling.GoodOrBetterPercent(),
			result.Record.ComfortGoodPercent, result.Record.DryGoodPercent, result.Record.BrightGoodPercent, result.Record.Nice.CalmGoodPercent); err != nil {
			return fmt.Errorf("write climate verification probe: %w", err)
		}
	}
	return nil
}

// Coordinates are exact 0.1-degree source centres, so each native point and
// rendered maximum-zoom texel have one unambiguous owner.
func defaultClimateVerificationProbes() []climatetiles.VerificationProbe {
	return []climatetiles.VerificationProbe{
		{Label: "Málaga in November", Period: "month-11", Longitude: -4.5, Latitude: 36.7, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Isla Cristina exact cell in November", Period: "month-11", Longitude: -7.3, Latitude: 37.2, Expectation: climatetiles.ProbeRequiresNoData},
		{Label: "Nearest Isla Cristina climate cell in November", Period: "month-11", Longitude: -7.4, Latitude: 37.2, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Amsterdam in January", Period: "month-01", Longitude: 4.9, Latitude: 52.4, Expectation: climatetiles.ProbeRequiresData},
		{Label: "London in January", Period: "month-01", Longitude: -0.1, Latitude: 51.5, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Singapore in May", Period: "month-05", Longitude: 103.8, Latitude: 1.3, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Cairo in May", Period: "month-05", Longitude: 31.2, Latitude: 30.0, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Tromsø exact cell in January", Period: "month-01", Longitude: 19.0, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresNoData},
		{Label: "Nearest Tromsø climate cell in January", Period: "month-01", Longitude: 18.9, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Tromsø exact cell in July", Period: "month-07", Longitude: 19.0, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresNoData},
		{Label: "Nearest Tromsø climate cell in July", Period: "month-07", Longitude: 18.9, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Canberra in January", Period: "month-01", Longitude: 149.1, Latitude: -35.3, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Canberra in July", Period: "month-07", Longitude: 149.1, Latitude: -35.3, Expectation: climatetiles.ProbeRequiresData},
		{Label: "São Paulo in January", Period: "month-01", Longitude: -46.6, Latitude: -23.6, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Jungfrau in January", Period: "month-01", Longitude: 8.0, Latitude: 46.5, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Fiji in January", Period: "month-01", Longitude: 178.4, Latitude: -18.1, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Gulf of Guinea in January", Period: "month-01", Longitude: 0.0, Latitude: 0.0, Expectation: climatetiles.ProbeRequiresNoData},
		{Label: "East dateline ocean in January", Period: "month-01", Longitude: 179.9, Latitude: 0.0, Expectation: climatetiles.ProbeRequiresNoData},
		{Label: "West dateline ocean in January", Period: "month-01", Longitude: -179.9, Latitude: 0.0, Expectation: climatetiles.ProbeRequiresNoData},
	}
}

func runClimateWorldCommand(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	run globalClimateRunner,
) error {
	if ctx == nil {
		return errors.New("climate-world requires a context")
	}
	if run == nil {
		return errors.New("climate-world runner is unavailable")
	}
	if stdin == nil {
		return errors.New("climate-world requires the AgERA5 bearer token on standard input")
	}
	if stdout == nil || stderr == nil {
		return errors.New("climate-world output streams are unavailable")
	}
	flags := flag.NewFlagSet("climate-world", flag.ContinueOnError)
	flags.SetOutput(stderr)
	month := flags.Int("month", 0, "calendar month to publish, from 1 to 12 (required)")
	out := flags.String("out", "", "existing artifact directory (required)")
	checkpoints := flags.String("checkpoints", "", "existing absolute checkpoint directory (required)")
	landCache := flags.String("land-cache", "", "existing Natural Earth cache directory (required)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("climate-world accepts no positional arguments")
	}
	if *month < int(time.January) || *month > int(time.December) {
		return errors.New("climate-world --month must be between 1 and 12")
	}
	if err := validateClimateWorldDirectory("--out", *out, false); err != nil {
		return err
	}
	if err := validateClimateWorldDirectory("--checkpoints", *checkpoints, true); err != nil {
		return err
	}
	if err := validateClimateWorldDirectory("--land-cache", *landCache, false); err != nil {
		return err
	}
	selectedMonth := time.Month(*month)
	if _, err := fmt.Fprintf(stdout, "Worldwide climate: building %s from daily AgERA5 data for 2006-2025. Valid checkpoints will be reused.\n", selectedMonth); err != nil {
		return fmt.Errorf("write climate-world progress: %w", err)
	}
	summary, err := run(ctx, weatherupdate.GlobalClimateConfig{
		ArtifactRoot:   *out,
		CheckpointRoot: *checkpoints,
		LandCacheRoot:  *landCache,
		Period:         fmt.Sprintf("month-%02d", selectedMonth),
		Month:          selectedMonth,
		TokenReader:    stdin,
	})
	if err != nil {
		return err
	}
	if !summary.MainPointerUnchanged {
		return errors.New("worldwide climate build did not prove that the main weather release stayed unchanged")
	}
	if !summary.SelectedMonthInstalled || summary.Pointer.IndexID == "" || summary.MonthsAvailable < 1 || summary.MonthsAvailable > 12 ||
		(summary.MonthsAvailable == 12 && summary.IndexState != climatetiles.IndexStateComplete) ||
		(summary.MonthsAvailable < 12 && summary.IndexState != climatetiles.IndexStatePreview) {
		return errors.New("worldwide climate build returned an invalid year-index summary")
	}
	if _, err := fmt.Fprintf(stdout, "Blocks: %d reused, %d built, %d water-only skipped. Source: %d responses, %d bytes.\n",
		summary.BlocksReused, summary.BlocksBuilt, summary.BlocksSkipped,
		summary.SourceResponses, summary.SourceResponseBytes); err != nil {
		return fmt.Errorf("write climate-world block summary: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "Output: %s installed; %d/12 months available; %s index pointer %s; %d point blocks, %d scalar tiles; %d checkpoint bytes, %d artifact bytes.\n",
		summary.ReleaseID, summary.MonthsAvailable, summary.IndexState, summary.Pointer.IndexID, summary.PointBlocks, summary.ScalarTiles,
		summary.CheckpointBytes, summary.ArtifactBytes); err != nil {
		return fmt.Errorf("write climate-world output summary: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "Quality: %d AgERA5-unsupported land cells left transparent; %d crossed-extrema repairs, largest %.3f C; %d daytime-range clamps beyond 5 C tolerance, largest %.3f C; %d dew-point caps, largest %.3f C; main weather release unchanged; elapsed %s.\n",
		summary.UnsupportedLandCells, summary.TemperatureRepairs, summary.MaximumInversionC,
		summary.ComfortTemperatureRepairs, summary.MaximumComfortTemperatureAdjustmentC,
		summary.DewPointRepairs, summary.MaximumDewPointCapC, summary.Elapsed.Round(time.Second)); err != nil {
		return fmt.Errorf("write climate-world quality summary: %w", err)
	}
	return nil
}

func validateClimateWorldDirectory(flagName, path string, absolute bool) error {
	if path == "" {
		return fmt.Errorf("climate-world %s is required", flagName)
	}
	if absolute && filepath.Clean(path) != path {
		return fmt.Errorf("climate-world %s must be a clean directory path", flagName)
	}
	if absolute && (!filepath.IsAbs(path) || path == string(filepath.Separator)) {
		return fmt.Errorf("climate-world %s must be an absolute directory other than the filesystem root", flagName)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect climate-world %s directory: %w", flagName, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("climate-world %s must be an existing real directory", flagName)
	}
	return nil
}
