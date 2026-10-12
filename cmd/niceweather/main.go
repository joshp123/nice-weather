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
	"nice-weather-finder/internal/destinations"
	"nice-weather-finder/internal/era5annual"
	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/era5publication"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/product"
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
	case "forecast-products":
		flags := flag.NewFlagSet("forecast-products", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		source := flags.String("from", "", "active source snapshot identity")
		index := flags.String("index", "", "new snapshot identity")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err = weatherupdate.RebuildForecastProducts(ctx, *out, *source, *index)
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
	case "era5-climate-year":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = runERA5ClimateYearCommand(ctx, os.Args[2:], os.Stdout, os.Stderr)
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
	fmt.Fprintln(output, "usage: niceweather <forecast-world|forecast-products|era5-climate-year|era5-publication-authority|verify-climate|serve|places|best-weather> [options]")
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

func runERA5ClimateYearCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("era5-climate-year", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := era5annual.Config{Progress: stderr}
	flags.StringVar(&config.PlanPath, "plan", "", "accepted worldwide source plan path (required)")
	flags.StringVar(&config.PlanSHA256, "plan-sha256", "", "trusted compressed source plan SHA-256 (required)")
	flags.StringVar(&config.AuthorityPath, "publication-authority", "", "trusted centre-on-land authority path (required)")
	flags.StringVar(&config.AuthoritySHA256, "publication-authority-sha256", "", "trusted authority SHA-256 (required)")
	flags.StringVar(&config.DisplayMaskPath, "display-mask", "", "canonical centre-on-land mask path (required)")
	flags.StringVar(&config.WorkRoot, "work", "", "large-disk annual source and checkpoint directory (required)")
	flags.StringVar(&config.ProductRoot, "out", "", "climate artifact root (required)")
	flags.Func("source-tile", "process only this native source tile row,column; repeat for a bounded run", func(value string) error {
		config.SourceTiles = append(config.SourceTiles, value)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("era5-climate-year accepts no positional arguments")
	}
	summary, err := era5annual.Build(ctx, config)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ERA5 annual: %d/%d source tiles complete; %d source cells; %d validated months; elapsed=%s\n", summary.CompletedTiles, summary.Tiles, summary.Cells, summary.Months, summary.Elapsed.Round(time.Second))
	return nil
}
