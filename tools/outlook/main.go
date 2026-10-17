// Command outlook creates posters from an existing forecast without downloading weather again.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nice-weather-finder/outlook"
)

func main() {
	source := flag.String("source", "https://nice.jjpcodes.com/data", "published data URL or local artifacts directory")
	places := flag.String("places-source", "", "place data directory or URL (defaults to --source)")
	static := flag.String("static-source", "", "v6 static directory or URL (defaults to <places-source>/v6)")
	out := flag.String("out", "./artifacts", "output artifacts directory")
	date := flag.String("today", "", "UTC publication date YYYY-MM-DD (defaults to today)")
	flag.Parse()
	if *places == "" {
		*places = *source
	}
	now := time.Now()
	var err error
	if *date != "" {
		now, err = time.Parse("2006-01-02", *date)
	}
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var outlooks []outlook.Outlook
		if *static == "" {
			outlooks, err = outlook.Generate(ctx, *source, *places, *out, now)
		} else {
			outlooks, err = outlook.GenerateWithStatic(ctx, *source, *places, *static, *out, now)
		}
		for _, result := range outlooks {
			fmt.Println(result.Sentence)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
