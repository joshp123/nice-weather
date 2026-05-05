package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"nice-weather-finder/internal/artifact"
	"nice-weather-finder/internal/dwd"
	"nice-weather-finder/internal/grid"
	"nice-weather-finder/internal/landmask"
	"nice-weather-finder/internal/scoring"
	"nice-weather-finder/internal/server"
	"nice-weather-finder/internal/tiles"
	"nice-weather-finder/internal/timezone"
	"nice-weather-finder/internal/weatherinput"
)

var profiles = []scoring.Profile{scoring.ProfileGeneral, scoring.ProfileCycling}
var staleAfterHours = 12
var cachedRunsToKeep = 2

var anchors = []struct {
	name string
	lon  float64
	lat  float64
}{
	{"Rotterdam", 4.4777, 51.9244},
	{"Granada", -3.5986, 37.1773},
	{"Palma de Mallorca", 2.6502, 39.5696},
	{"Biarritz", -1.5586, 43.4832},
	{"Lisbon", -9.1393, 38.7223},
	{"Newark-on-Trent", -0.8093, 53.0765},
	{"Palermo", 13.3615, 38.1157},
	{"Florence", 11.2558, 43.7696},
	{"Kalamata", 22.1142, 37.0360},
	{"Zurich", 8.5417, 47.3769},
	{"Budapest", 19.0402, 47.4979},
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "update":
		err = runUpdate(os.Args[2:])
	case "inspect-dwd":
		err = runInspectDWD(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ExitOnError)
	cache := flags.String("cache", "./.cache/dwd/forecast", "DWD ICON-EU forecast cache directory")
	land := flags.String("land", "./.cache/land/ne_50m_land.geojson", "Natural Earth land GeoJSON used to mask sea pixels")
	out := flags.String("out", "./artifacts", "published artifact root")
	days := flags.Int("days", 5, "number of local forecast dates to publish")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *days < 1 || *days > 5 {
		return fmt.Errorf("--days must be between 1 and 5 for ICON-EU")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	steps := dwd.ForecastSteps()
	run, err := dwd.LatestCompleteRunForLocalDays(ctx, time.Now(), steps, *days)
	if err != nil {
		return err
	}
	fmt.Printf("latest complete DWD ICON-EU run with %d full local forecast dates: %s\n", *days, run.ID)
	if currentForecastReusable(*out, run.ID) {
		if err := dwd.PruneCachedRuns(*cache, cachedRunsToKeep); err != nil {
			return err
		}
		fmt.Printf("published forecast already uses DWD ICON-EU run %s, tile max zoom %d, and renderer %d; no update needed\n", run.ID, tiles.MaxZoom, tiles.RendererVersion)
		return nil
	}

	runDir, err := dwd.DownloadRun(ctx, run, steps, *cache)
	if err != nil {
		return err
	}
	fmt.Printf("cached DWD ICON-EU files in %s\n", runDir)

	locator, err := timezone.NewLocator()
	if err != nil {
		return err
	}
	horizons := dwd.FullLocalHorizonDates(run.StartedAt, *days)
	forecast, err := dwd.LoadLocalDaytimeGrid(ctx, runDir, run.StartedAt, horizons, locator.Location)
	if err != nil {
		return err
	}
	inputs := forecast.Grid
	mask, err := landmask.LoadGeoJSON(*land)
	if err != nil {
		return err
	}
	inputs.ApplyLandMask(mask.TouchesCell)

	releaseName := run.ID + "-" + time.Now().UTC().Format("20060102T150405Z")
	releaseDir, err := artifact.NewReleaseDir(*out, releaseName)
	if err != nil {
		return err
	}
	manifest := buildManifest(inputs, run.ID, run.StartedAt, forecast.Horizons)
	tileCount, err := writeForecastTiles(releaseDir, manifest.Layers, inputs)
	if err != nil {
		return err
	}
	if err := writeManifest(releaseDir, manifest); err != nil {
		return err
	}
	if err := artifact.PublishRelease(*out, releaseDir); err != nil {
		return err
	}
	if err := artifact.PruneReleases(*out, 2); err != nil {
		return err
	}
	if err := dwd.PruneCachedRuns(*cache, cachedRunsToKeep); err != nil {
		return err
	}
	fmt.Printf("published %s\n", filepath.Join(*out, "current"))
	fmt.Printf("wrote %d DWD ICON-EU layers and %d raster tiles\n", len(manifest.Layers), tileCount)
	return nil
}

func currentForecastReusable(out, run string) bool {
	data, err := os.ReadFile(filepath.Join(artifact.PublishedRoot(out), "manifest.json"))
	if err != nil {
		return false
	}
	var manifest artifact.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	return manifest.Sources.Forecast.Run == run && manifest.Grid.TileMaxZoom == tiles.MaxZoom && manifest.Grid.TileRenderer == tiles.RendererVersion
}

func runInspectDWD(args []string) error {
	flags := flag.NewFlagSet("inspect-dwd", flag.ExitOnError)
	file := flags.String("file", "", "DWD ICON-EU GRIB2 file to inspect")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("--file is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	values, err := dwd.ReadGribValues(ctx, *file)
	if err != nil {
		return err
	}
	minValue := values[0].Value
	maxValue := values[0].Value
	for _, value := range values[1:] {
		if value.Value < minValue {
			minValue = value.Value
		}
		if value.Value > maxValue {
			maxValue = value.Value
		}
	}
	fmt.Printf("decoded %d grid values\n", len(values))
	fmt.Printf("first %.3f %.3f %.3f\n", values[0].Lat, values[0].Lon, values[0].Value)
	fmt.Printf("last %.3f %.3f %.3f\n", values[len(values)-1].Lat, values[len(values)-1].Lon, values[len(values)-1].Value)
	fmt.Printf("min %.3f max %.3f\n", minValue, maxValue)
	return nil
}

type tileJob struct {
	layer  artifact.Layer
	scores tiles.ScoreGrid
	z      int
	x      int
	y      int
}

func writeForecastTiles(out string, layers []artifact.Layer, inputs weatherinput.Grid) (int, error) {
	jobs := make(chan tileJob)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	workerCount := runtime.GOMAXPROCS(0)

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				img := tiles.RenderScoreGridTile(job.scores, job.z, job.x, job.y)
				path := artifact.TilePath(out, job.layer.Basis, job.layer.Profile, job.layer.Horizon, job.z, job.x, job.y)
				if err := tiles.WritePNG(path, img); err != nil {
					select {
					case errs <- err:
					default:
					}
				}
			}
		}()
	}

	tileCount := 0
	for _, layer := range layers {
		scores := tiles.NewScoreGrid(inputs, scoring.Profile(layer.Profile), layer.Horizon)
		for z := 0; z <= tiles.MaxZoom; z++ {
			for x := 0; x < 1<<z; x++ {
				for y := 0; y < 1<<z; y++ {
					if !tileTouchesEurope(z, x, y) {
						continue
					}
					select {
					case err := <-errs:
						close(jobs)
						wg.Wait()
						return tileCount, err
					case jobs <- tileJob{layer: layer, scores: scores, z: z, x: x, y: y}:
						tileCount++
					}
				}
			}
		}
	}

	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		return tileCount, err
	default:
		return tileCount, nil
	}
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	webDir := flags.String("web", "./dist", "frontend build directory")
	artifactDir := flags.String("artifacts", "./artifacts", "generated artifact directory")
	addr := flags.String("addr", "127.0.0.1:4173", "listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return server.Serve(*webDir, *artifactDir, *addr)
}

func buildManifest(inputs weatherinput.Grid, runLabel string, runStartedAt time.Time, horizons []dwd.Horizon) artifact.Manifest {
	horizonIDs := make([]string, 0, len(horizons))
	horizonDetails := make([]artifact.ForecastHorizon, 0, len(horizons))
	for _, horizon := range horizons {
		horizonIDs = append(horizonIDs, horizon.ID)
		horizonDetails = append(horizonDetails, artifact.ForecastHorizon{
			ID:          horizon.ID,
			Label:       horizon.Label,
			LocalDate:   horizon.LocalDate,
			WindowStart: "10:00",
			WindowEnd:   "20:00",
		})
	}

	layers := make([]artifact.Layer, 0, len(profiles)*len(horizonIDs))
	for _, profile := range profiles {
		for _, horizon := range horizonIDs {
			layers = append(layers, artifact.Layer{Basis: "forecast", Profile: string(profile), Horizon: horizon, TileURL: artifact.TileURL("forecast", string(profile), horizon)})
		}
	}
	runStartedAtText := ""
	if !runStartedAt.IsZero() {
		runStartedAtText = runStartedAt.UTC().Format(time.RFC3339)
	}
	return artifact.Manifest{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Grid:        artifact.Grid{CRS: "EPSG:4326", CellDeg: inputs.StepDeg, Region: "Europe", TileMaxZoom: tiles.MaxZoom, TileRenderer: tiles.RendererVersion},
		Sources: artifact.Sources{
			Forecast: artifact.ForecastSource{
				Name:               "DWD ICON-EU",
				Run:                runLabel,
				RunStartedAt:       runStartedAtText,
				ResolutionKMApprox: 7,
				Aggregation:        "10:00-20:00 local time per grid cell: average temp/humidity/cloud/wind, accumulated rain delta",
				StaleAfterHours:    staleAfterHours,
				Horizons:           horizonIDs,
				HorizonDetails:     horizonDetails,
			},
		},
		Layers:  layers,
		Anchors: buildAnchors(inputs, horizonIDs),
		Legend:  legend(),
	}
}

func writeManifest(out string, manifest artifact.Manifest) error {
	if err := artifact.Validate(manifest); err != nil {
		return err
	}
	data, err := manifest.JSON()
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(out, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", manifestPath)
	return nil
}

func buildAnchors(inputs weatherinput.Grid, horizons []string) []artifact.Anchor {
	result := make([]artifact.Anchor, 0, len(anchors))
	for _, anchor := range anchors {
		anchorHorizons := map[string]artifact.AnchorHorizon{}
		for _, horizon := range horizons {
			input, ok := inputs.InputAt(horizon, anchor.lon, anchor.lat)
			if !ok {
				continue
			}
			anchorHorizons[horizon] = artifact.AnchorHorizon{
				Input: artifact.AnchorInput{TempC: round1(input.TempC), Humidity: round1(input.Humidity), RainMM: round1(input.RainMM), CloudPct: round1(input.CloudPct), WindKMH: round1(input.WindKMH)},
				Scores: map[string]artifact.AnchorScore{
					string(scoring.ProfileGeneral): anchorScore(input, scoring.ProfileGeneral),
					string(scoring.ProfileCycling): anchorScore(input, scoring.ProfileCycling),
				},
			}
		}
		if len(anchorHorizons) == 0 {
			continue
		}
		result = append(result, artifact.Anchor{
			Name:     anchor.name,
			Lon:      anchor.lon,
			Lat:      anchor.lat,
			Horizons: anchorHorizons,
		})
	}
	return result
}

func anchorScore(input weatherinput.WeatherInput, profile scoring.Profile) artifact.AnchorScore {
	score := scoring.Score(input, profile)
	return artifact.AnchorScore{
		Score:    score.Score,
		Category: score.Category.Label,
		Color:    score.Category.Color,
		Temp:     round1(score.Components.Temp),
		Rain:     round1(score.Components.Rain),
		Cloud:    round1(score.Components.Cloud),
		Wind:     round1(score.Components.Wind),
		Humidity: round1(score.Components.Humidity),
		Why:      fmt.Sprintf("temp %.1f, rain %.1f, cloud %.1f, wind %.1f, humidity %.1f", score.Components.Temp, score.Components.Rain, score.Components.Cloud, score.Components.Wind, score.Components.Humidity),
	}
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func legend() artifact.Legend {
	return artifact.Legend{Min: 0, Max: 100, Categories: []artifact.LegendCategory{
		{Label: "Not Nice", Color: "#9ca3af", Min: 0, Max: 49},
		{Label: "Fair", Color: "#7dd3fc", Min: 50, Max: 61},
		{Label: "Nice", Color: "#38bdf8", Min: 62, Max: 71},
		{Label: "Beautiful", Color: "#a3e635", Min: 72, Max: 81},
		{Label: "Perfect", Color: "#16a34a", Min: 82, Max: 91},
		{Label: "Pristine", Color: "#f59e0b", Min: 92, Max: 100},
	}}
}

func tileTouchesEurope(z, x, y int) bool {
	west, north := tileLonLat(z, x, y)
	east, south := tileLonLat(z, x+1, y+1)
	return east >= grid.EuropeBounds.West && west <= grid.EuropeBounds.East &&
		north >= grid.EuropeBounds.South && south <= grid.EuropeBounds.North
}

func tileLonLat(z, x, y int) (float64, float64) {
	n := float64(uint(1) << z)
	lon := float64(x)/n*360 - 180
	latRad := math.Atan(math.Sinh(math.Pi * (1 - 2*float64(y)/n)))
	lat := latRad * 180 / math.Pi
	return lon, lat
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: weatherchase update --cache ./.cache/dwd/forecast --land ./.cache/land/ne_50m_land.geojson --out ./artifacts --days 5 | weatherchase inspect-dwd --file ./file.grib2 | weatherchase serve --web ./dist --artifacts ./artifacts --addr 127.0.0.1:4173")
}
