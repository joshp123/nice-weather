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
	"nice-weather-finder/internal/climatecanary"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/destinations"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/product"
	"nice-weather-finder/internal/providers/agera5"
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
	case "fixture":
		flags := flag.NewFlagSet("fixture", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		if err = flags.Parse(os.Args[2:]); err == nil {
			err = appserver.GenerateFixture(*out)
		}
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		web := flags.String("web", "./dist", "frontend build directory")
		artifacts := flags.String("artifacts", "./artifacts", "artifact directory")
		listen := flags.String("listen", appserver.DefaultListen, "listen address")
		if err = flags.Parse(os.Args[2:]); err == nil {
			err = appserver.Serve(appserver.Config{WebRoot: *web, ArtifactRoot: *artifacts, Listen: *listen})
		}
	case "update":
		flags := flag.NewFlagSet("update", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		cache := flags.String("cache", "./.cache/weather", "weather download and climate cache")
		smoke := flags.Bool("smoke", false, "verify one live step per source without publishing")
		refresh := flags.Bool("refresh", false, "rebuild even when current already uses the latest source runs")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err = weatherupdate.Run(ctx, weatherupdate.Config{Out: *out, Cache: *cache, Domain: domain, Smoke: *smoke, Refresh: *refresh})
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
	fmt.Fprintln(os.Stderr, "usage: niceweather <fixture|update|global-canary|forecast-world|agera5-canary|climate-world|verify-climate|serve|places|best-weather> [options]")
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
		{Label: "Isla Cristina in November", Period: "month-11", Longitude: -7.3, Latitude: 37.2, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Amsterdam in January", Period: "month-01", Longitude: 4.9, Latitude: 52.4, Expectation: climatetiles.ProbeRequiresData},
		{Label: "London in January", Period: "month-01", Longitude: -0.1, Latitude: 51.5, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Singapore in May", Period: "month-05", Longitude: 103.8, Latitude: 1.3, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Cairo in May", Period: "month-05", Longitude: 31.2, Latitude: 30.0, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Tromsø in January", Period: "month-01", Longitude: 19.0, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresData},
		{Label: "Tromsø in July", Period: "month-07", Longitude: 19.0, Latitude: 69.7, Expectation: climatetiles.ProbeRequiresData},
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
