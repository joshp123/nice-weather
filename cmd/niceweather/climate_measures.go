package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nice-weather-finder/internal/climatefacts"
	"nice-weather-finder/internal/climatemeasures"
)

func runClimateMeasures(arguments []string) error {
	flags := flag.NewFlagSet("climate-measures", flag.ContinueOnError)
	index := flags.String("source-index", "earthmover", "frozen ERA5 source catalogue, or earthmover for the public archive")
	static := flags.String("static", "", "NWFSTAT1 static land fraction, ERA5 height and ground-height grid (.gz)")
	out := flags.String("out", "./artifacts", "batch output directory")
	year := flags.Int("year", 0, "one local calendar year, 2006..2025")
	row := flags.Int("tile-row", 0, "native 3-degree tile row, 0 north to 60 south")
	column := flags.Int("tile-column", 0, "native 3-degree tile column, 0 Greenwich eastward to 119")
	places := flags.String("places", "", "optional off-grid place specifications for source verification")
	site := flags.String("publish", "", "upload only this batch below climate-measures/ on this Site")
	publishPrefix := flags.String("publish-prefix", "climate-measures", "storage prefix: climate-measures or forecast/climate-measures-probe")
	tokenStdin := flags.Bool("token-stdin", false, "read publish token from standard input")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *static == "" {
		return fmt.Errorf("climate-measures requires --static")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := publishToken(*site, *tokenStdin)
	if err != nil {
		return err
	}
	options := climatemeasures.Options{SourceIndex: *index, StaticGrid: *static, Output: *out, TileRow: *row, TileColumn: *column, Year: *year}
	if *places != "" {
		options.Cells, err = readClimatePlaces(*places, *static)
		if err != nil {
			return err
		}
	}
	report, err := climatemeasures.Build(ctx, options, os.Stderr)
	if err != nil {
		return err
	}
	if *site != "" {
		if err = climatemeasures.Publish(ctx, report, *out, *site, token, *publishPrefix); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

type climatePlace struct {
	Latitude, Longitude, ElevationM float64
	Timezone                        string
	MonthlyTurbidity                *[12]float64
	SourceLatitude, SourceLongitude *float64
}

func readClimatePlaces(path, staticPath string) ([]climatefacts.Cell, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var places []climatePlace
	if err = json.Unmarshal(encoded, &places); err != nil {
		return nil, err
	}
	grid, err := climatefacts.ReadStatic(staticPath)
	if err != nil {
		return nil, err
	}
	cells := make([]climatefacts.Cell, 0, len(places))
	for index, place := range places {
		location, err := time.LoadLocation(place.Timezone)
		if err != nil {
			return nil, err
		}
		sourceLatitude, sourceLongitude := place.Latitude, place.Longitude
		if place.SourceLatitude != nil {
			sourceLatitude = *place.SourceLatitude
		}
		if place.SourceLongitude != nil {
			sourceLongitude = *place.SourceLongitude
		}
		row := int(math.Round((90 - sourceLatitude) * 4))
		column := int(math.Round(math.Mod(sourceLongitude+360, 360)*4)) % climatefacts.Width
		temperatureRow, temperatureColumn := grid.TemperatureCell(place.Latitude, place.Longitude, row, column)
		cells = append(cells, climatefacts.Cell{Index: uint32(index), Latitude: place.Latitude, Longitude: place.Longitude, ElevationM: place.ElevationM, Location: location, Row: row, Column: column, TemperatureRow: temperatureRow, TemperatureColumn: temperatureColumn, MonthlyTurbidity: place.MonthlyTurbidity})
	}
	return cells, nil
}
