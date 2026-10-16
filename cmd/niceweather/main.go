// Command niceweather builds Nice Weather Finder's data files.
//
//	niceweather forecast --out ./artifacts
//	    Turns the latest ECMWF open-data run into forecast/<run>/ (daily).
//	niceweather climate-convert --from <ERA5 product> --out ./artifacts
//	    Turns the 2006–2025 ERA5 day-scored result into climate/ (rarely).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/climateconvert"
	"nice-weather-finder/internal/climateperiods"
	"nice-weather-finder/internal/dailyforecast"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: niceweather forecast|publish|climate-convert [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "forecast":
		err = runForecast(os.Args[2:])
	case "publish":
		err = runPublish(os.Args[2:])
	case "climate-convert":
		err = runClimateConvert(os.Args[2:])
	case "climate-periods":
		flags := flag.NewFlagSet("climate-periods", flag.ContinueOnError)
		climate := flags.String("climate", "./artifacts/climate", "converted climate directory")
		places := flags.String("places", "./artifacts/places.tsv", "place list from hosting/sites/places.mjs")
		if err = flags.Parse(os.Args[2:]); err == nil {
			err = climateperiods.Build(*climate, *places)
		}
	default:
		err = fmt.Errorf("unknown command %q; use forecast, publish or climate-convert", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runForecast(arguments []string) error {
	flags := flag.NewFlagSet("forecast", flag.ContinueOnError)
	out := flags.String("out", "./artifacts", "artifact directory")
	site := flags.String("publish", "", "publish the completed forecast to this Site URL")
	tokenStdin := flags.Bool("token-stdin", false, "read the publish token from standard input instead of NICE_WEATHER_PUBLISH_TOKEN")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := publishToken(*site, *tokenStdin)
	if err != nil {
		return err
	}
	report, err := dailyforecast.Build(ctx, *out, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Printf("forecast: %s; %d days; downloaded %d bytes; wrote %d files / %d bytes; %s; %s\n",
		report.RunTime.Format(time.RFC3339), len(report.Dates), report.DownloadedBytes,
		report.OutputFiles, report.OutputBytes, report.Elapsed.Round(time.Millisecond), report.Directory)
	if *site != "" {
		return publishForecast(ctx, report.Directory, *site, token)
	}
	return nil
}

func runClimateConvert(arguments []string) error {
	flags := flag.NewFlagSet("climate-convert", flag.ContinueOnError)
	from := flags.String("from", "./.cache/era5-product", "finished ERA5 product directory")
	out := flags.String("out", "./artifacts", "output directory (its climate/ must not exist)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	started := time.Now()
	summary, err := climateconvert.Convert(*from, *out)
	if err != nil {
		return err
	}
	fmt.Printf("climate: %d months, %d land cells a month; %d files / %d bytes in %s\n",
		summary.Months, summary.LandCells, summary.Files, summary.Bytes, time.Since(started).Round(time.Millisecond))
	return nil
}

func publishToken(site string, fromStdin bool) (string, error) {
	if site == "" {
		return "", nil
	}
	token := os.Getenv("NICE_WEATHER_PUBLISH_TOKEN")
	os.Unsetenv("NICE_WEATHER_PUBLISH_TOKEN")
	if fromStdin {
		encoded, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
		if err != nil {
			return "", err
		}
		token = string(encoded)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("publishing requires --token-stdin or NICE_WEATHER_PUBLISH_TOKEN")
	}
	return token, nil
}

func runPublish(arguments []string) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	from := flags.String("from", "", "completed forecast run directory")
	site := flags.String("site", "", "Site URL")
	tokenStdin := flags.Bool("token-stdin", false, "read the publish token from standard input")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *from == "" || *site == "" {
		return fmt.Errorf("publish requires --from and --site")
	}
	token, err := publishToken(*site, *tokenStdin)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return publishForecast(ctx, *from, *site, token)
}

func publishForecast(ctx context.Context, directory, site, token string) error {
	report, err := dailyforecast.Publish(ctx, directory, site, token)
	if err != nil {
		return err
	}
	if report.AlreadyLive {
		fmt.Printf("forecast %s is already live\n", report.RunID)
		return nil
	}
	fmt.Printf("published: %s; %d files / %d bytes; %s\n", report.RunID, report.Files, report.Bytes, report.Elapsed.Round(time.Millisecond))
	return nil
}
