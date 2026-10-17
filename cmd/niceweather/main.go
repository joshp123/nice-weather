// Command niceweather builds and publishes the v6 ECMWF forecast.
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

	"nice-weather-finder/internal/dailyforecast"
	"nice-weather-finder/outlook"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: niceweather forecast|publish [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "forecast":
		err = runForecast(os.Args[2:])
	case "publish":
		err = runPublish(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q; use forecast or publish", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runForecast(arguments []string) error {
	flags := flag.NewFlagSet("forecast", flag.ContinueOnError)
	out := flags.String("out", "./artifacts", "artifact directory")
	v6Static := flags.String("v6-static", "https://nice.jjpcodes.com/data/v6", "v6 static directory or URL")
	placesSource := flags.String("places-source", "https://nice.jjpcodes.com/data", "place data directory or URL")
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
	report, err := dailyforecast.Build(ctx, *out, *v6Static, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Printf("forecast: %s; %d days; downloaded %d bytes; wrote %d files / %d bytes; %s; %s\n",
		report.RunTime.Format(time.RFC3339), len(report.Dates), report.DownloadedBytes,
		report.OutputFiles, report.OutputBytes, report.Elapsed.Round(time.Millisecond), report.Directory)
	outlooks, err := outlook.Generate(ctx, *out, *placesSource, *out, time.Now())
	if err != nil {
		return fmt.Errorf("daily outlook: %w", err)
	}
	for _, result := range outlooks {
		fmt.Println(result.Sentence)
	}
	if *site != "" {
		return publishForecast(ctx, report.Directory, *site, token)
	}
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

	fmt.Printf("published: %s; %d files / %d bytes; %s\n", report.RunID, report.Files, report.Bytes, report.Elapsed.Round(time.Millisecond))
	return nil
}
