package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/appserver"
	"nice-weather-finder/internal/climatecanary"
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
					fmt.Printf("AgERA5 canary: %s block %d,%d; %s through %s; %d/%d days; %d complete + %d no-data cells in %dx%d; %d temperature repairs (largest %.3f C); transfer %d bytes in %d responses/%d chunks; compressed %d bytes (largest %d, response limit %d); decoded %d bytes total, %d retained peak; sampled peak heap %d bytes; %s\n",
						summary.Source, summary.Block.Latitude, summary.Block.Longitude,
						summary.FirstDate.Format(time.DateOnly), summary.LastDate.Format(time.DateOnly),
						summary.ProcessedDays, summary.ExpectedDays, summary.CompleteCells, summary.NoDataCells,
						summary.Width, summary.Height, summary.TemperatureRepairs, summary.MaximumTemperatureRepairC,
						summary.FetchedBytes, summary.Responses, summary.Chunks,
						summary.CompressedChunkBytes, summary.MaximumCompressedChunkBytes, summary.ChunkResponseLimitBytes,
						summary.DecodedChunkBytes, summary.PeakRetainedDecodedBytes, summary.SampledPeakHeapBytes, summary.Elapsed.Round(time.Millisecond))
				}
			}
		}
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
				var best destinations.BuildSummary
				best, err = destinations.BuildCurrent(ctx, *out)
				if err != nil {
					err = fmt.Errorf("place index published, but best-weather artifact failed: %w", err)
				} else {
					fmt.Printf("best weather: %d places -> %d bytes in %s\n", best.ScannedPlaces, best.Bytes, best.Elapsed.Round(time.Millisecond))
				}
			}
		}
	case "best-weather":
		flags := flag.NewFlagSet("best-weather", flag.ContinueOnError)
		out := flags.String("out", "./artifacts", "artifact directory")
		if err = flags.Parse(os.Args[2:]); err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			var summary destinations.BuildSummary
			summary, err = destinations.BuildCurrent(ctx, *out)
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
	fmt.Fprintln(os.Stderr, "usage: niceweather <fixture|update|global-canary|agera5-canary|serve|places|best-weather> [options]")
}
