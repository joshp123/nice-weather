// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"nice-weather-finder/outlook"
	"os"
	"time"
)

func main() {
	source := flag.String("source", "", "forecast data root")
	placesSource := flag.String("places", "", "places.tsv directory")
	static := flag.String("static", "", "v6 static grid directory")
	date := flag.String("now", "", "UTC date before the forecast day")
	flag.Parse()
	var places []outlook.Place
	var tomorrow string
	if *source != "" {
		now, err := time.Parse("2006-01-02", *date)
		if err != nil {
			log.Fatal(err)
		}
		places, tomorrow, err = outlook.ForecastCandidates(context.Background(), *source, *placesSource, *static, now)
		if err != nil {
			log.Fatal(err)
		}
	} else if err := json.NewDecoder(os.Stdin).Decode(&places); err != nil {
		log.Fatal(err)
	}
	ranked := make(map[string][]outlook.Place)
	for _, region := range outlook.Regions {
		ranked[region.ID] = outlook.RankPlaces(places, region.ID)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Date       string                     `json:"date"`
		Candidates []outlook.Place            `json:"candidates"`
		Ranked     map[string][]outlook.Place `json:"ranked"`
	}{tomorrow, places, ranked}); err != nil {
		log.Fatal(err)
	}
}
