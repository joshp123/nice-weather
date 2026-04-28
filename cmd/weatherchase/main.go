package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"nice-weather-finder-poc/internal/artifact"
	"nice-weather-finder-poc/internal/dwd"
	"nice-weather-finder-poc/internal/grid"
	"nice-weather-finder-poc/internal/scoring"
	"nice-weather-finder-poc/internal/server"
	"nice-weather-finder-poc/internal/tiles"
	"nice-weather-finder-poc/internal/weatherinput"
)

var profiles = []scoring.Profile{scoring.ProfileGeneral, scoring.ProfileCycling}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = runBuild(os.Args[2:])
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

func runBuild(args []string) error {
	flags := flag.NewFlagSet("build", flag.ExitOnError)
	dir := flags.String("dir", "./.cache/dwd/highres", "directory containing DWD ICON-EU GRIB2 files")
	out := flags.String("out", "./public/artifacts", "artifact output directory")
	runLabel := flags.String("run", "local step 000", "forecast run label for the manifest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := os.RemoveAll(*out); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inputs, err := dwd.LoadStepZeroGrid(ctx, *dir)
	if err != nil {
		return err
	}
	manifest := buildManifest(inputs, *runLabel)
	tileCount, err := writeForecastTiles(*out, manifest.Layers, inputs)
	if err != nil {
		return err
	}
	if err := artifact.Validate(manifest); err != nil {
		return err
	}
	data, err := manifest.JSON()
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(*out, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", manifestPath)
	fmt.Printf("wrote %d DWD ICON-EU layers and %d raster tiles\n", len(manifest.Layers), tileCount)
	return nil
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
	layer artifact.Layer
	z     int
	x     int
	y     int
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
				profile := scoring.Profile(job.layer.Profile)
				img := tiles.RenderWeatherGridTile(inputs, profile, job.layer.Horizon, job.z, job.x, job.y)
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
		for z := 0; z <= 6; z++ {
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
					case jobs <- tileJob{layer: layer, z: z, x: x, y: y}:
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
	dir := flags.String("dir", "./public", "directory to serve")
	addr := flags.String("addr", "127.0.0.1:4173", "listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return server.Serve(*dir, *addr)
}

func buildManifest(inputs weatherinput.Grid, runLabel string) artifact.Manifest {
	layers := make([]artifact.Layer, 0, len(profiles))
	for _, profile := range profiles {
		layers = append(layers, artifact.Layer{Basis: "forecast", Profile: string(profile), Horizon: "now", TileURL: artifact.TileURL("forecast", string(profile), "now")})
	}
	return artifact.Manifest{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Grid:        artifact.Grid{CRS: "EPSG:4326", CellDeg: inputs.StepDeg, Region: "Europe"},
		Sources: artifact.Sources{
			Forecast: artifact.ForecastSource{Name: "DWD ICON-EU", Run: runLabel, ResolutionKMApprox: 7, Horizons: []string{"now"}},
		},
		Layers: layers,
		Legend: legend(),
	}
}

func legend() artifact.Legend {
	return artifact.Legend{Min: 0, Max: 100, Categories: []artifact.LegendCategory{
		{Label: "Not Nice", Color: "#9ca3af", Min: 0, Max: 49},
		{Label: "Fair", Color: "#7dd3fc", Min: 50, Max: 61},
		{Label: "Nice", Color: "#38bdf8", Min: 62, Max: 71},
		{Label: "Beautiful", Color: "#a3e635", Min: 72, Max: 81},
		{Label: "Perfect", Color: "#16a34a", Min: 82, Max: 91},
		{Label: "Pristine", Color: "#facc15", Min: 92, Max: 100},
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
	fmt.Fprintln(os.Stderr, "usage: weatherchase build --dir ./.cache/dwd/highres --out ./public/artifacts --run 'YYYYMMDDHH step 000' | weatherchase inspect-dwd --file ./file.grib2 | weatherchase serve --dir ./public --addr 127.0.0.1:4173")
}
