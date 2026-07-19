package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"nice-weather-finder/internal/appserver"
	"nice-weather-finder/internal/places"
	weatherupdate "nice-weather-finder/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
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
		listen := flags.String("listen", "127.0.0.1:5198", "listen address")
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
			err = weatherupdate.Run(ctx, weatherupdate.Config{Out: *out, Cache: *cache, Smoke: *smoke, Refresh: *refresh})
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
			summary, err = places.BuildAndPublishEurope(ctx, *source, *out, *sourceDate, *releaseID)
			if err == nil {
				fmt.Printf("places: %d places, %d keys, %d shards, %.1f MiB gzip\n", summary.PlaceCount, summary.SearchKeyCount, summary.ShardCount, float64(summary.GzipBytes)/(1024*1024))
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
	fmt.Fprintln(os.Stderr, "usage: niceweather <fixture|update|serve|places> [options]")
}
