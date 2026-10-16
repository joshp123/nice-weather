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
	"os"
	"os/signal"
	"syscall"
	"time"

	"nice-weather-finder/internal/climateconvert"
	"nice-weather-finder/internal/dailyforecast"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: niceweather forecast|climate-convert [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "forecast":
		err = runForecast(os.Args[2:])
	case "climate-convert":
		err = runClimateConvert(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q; use forecast or climate-convert", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runForecast(arguments []string) error {
	flags := flag.NewFlagSet("forecast", flag.ContinueOnError)
	out := flags.String("out", "./artifacts", "artifact directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := dailyforecast.Build(ctx, *out, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Printf("forecast: %s; %d days; downloaded %d bytes; wrote %d files / %d bytes; %s; %s\n",
		report.RunTime.Format(time.RFC3339), len(report.Dates), report.DownloadedBytes,
		report.OutputFiles, report.OutputBytes, report.Elapsed.Round(time.Millisecond), report.Directory)
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
