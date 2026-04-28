package dwd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"nice-weather-finder-poc/internal/weatherinput"
)

type gribReader func(context.Context, string) ([]GridValue, error)

var DaytimeSteps = []int{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}

func LoadDaytimeGrid(ctx context.Context, dir string) (weatherinput.Grid, error) {
	return loadDaytimeGrid(ctx, dir, DaytimeSteps, ReadGribValues)
}

func loadDaytimeGrid(ctx context.Context, dir string, steps []int, read gribReader) (weatherinput.Grid, error) {
	if len(steps) == 0 {
		return weatherinput.Grid{}, fmt.Errorf("DWD daytime grid needs at least one forecast step")
	}
	stepInputs := make([][]weatherinput.WeatherInput, 0, len(steps))
	var meta gridMeta
	for stepIndex, step := range steps {
		values, err := loadStepFields(ctx, dir, step, read)
		if err != nil {
			return weatherinput.Grid{}, err
		}
		t2m := values["t_2m"]
		if len(t2m) < 2 {
			return weatherinput.Grid{}, fmt.Errorf("DWD T_2M grid is too small")
		}
		if err := validateAlignedFields(values, t2m); err != nil {
			return weatherinput.Grid{}, err
		}
		stepMeta, err := inferGrid(t2m)
		if err != nil {
			return weatherinput.Grid{}, err
		}
		if stepIndex == 0 {
			meta = stepMeta
		} else if stepMeta != meta {
			return weatherinput.Grid{}, fmt.Errorf("DWD step %03d grid metadata changed: %+v != %+v", step, stepMeta, meta)
		}
		inputs := make([]weatherinput.WeatherInput, len(t2m))
		for i := range t2m {
			inputs[i] = MapICONToWeatherInput(IconEUFields{
				T2MKelvin: values["t_2m"][i].Value,
				RelHum2M:  values["relhum_2m"][i].Value,
				TotPrecMM: values["tot_prec"][i].Value,
				CloudPct:  values["clct"][i].Value,
				U10MMS:    values["u_10m"][i].Value,
				V10MMS:    values["v_10m"][i].Value,
			})
		}
		stepInputs = append(stepInputs, inputs)
	}

	grid := weatherinput.NewGrid(meta.West, meta.East, meta.South, meta.North, meta.StepDeg)
	daytime := make([]weatherinput.WeatherInput, grid.Width*grid.Height)
	for i := range stepInputs[0] {
		samples := make([]weatherinput.WeatherInput, len(stepInputs))
		for stepIndex := range stepInputs {
			samples[stepIndex] = stepInputs[stepIndex][i]
		}
		x := i % grid.Width
		y := i / grid.Width
		daytime[y*grid.Width+x] = weatherinput.DaytimeForecastAggregate(samples)
	}
	grid.Horizons["d1"] = daytime
	return grid, nil
}

func loadStepFields(ctx context.Context, dir string, step int, read gribReader) (map[string][]GridValue, error) {
	values := map[string][]GridValue{}
	for _, key := range RequiredFolders {
		path, err := stepFilePath(dir, key, step)
		if err != nil {
			return nil, err
		}
		fieldValues, err := read(ctx, path)
		if err != nil {
			return nil, err
		}
		values[key] = fieldValues
	}
	return values, nil
}

func validateAlignedFields(values map[string][]GridValue, reference []GridValue) error {
	for key, fieldValues := range values {
		if len(fieldValues) != len(reference) {
			return fmt.Errorf("DWD field %s has %d values, want %d", key, len(fieldValues), len(reference))
		}
		for i, value := range fieldValues {
			if value.Lon != reference[i].Lon || value.Lat != reference[i].Lat {
				return fmt.Errorf("DWD field %s coordinate mismatch at index %d: %.3f,%.3f != %.3f,%.3f", key, i, value.Lat, value.Lon, reference[i].Lat, reference[i].Lon)
			}
		}
	}
	return nil
}

func stepFilePath(dir, field string, step int) (string, error) {
	shortPath := filepath.Join(dir, fmt.Sprintf("%s_%03d.grib2", field, step))
	if dir == "" {
		return shortPath, nil
	}
	if _, err := os.Stat(shortPath); err == nil {
		return shortPath, nil
	}
	gribName, ok := gribNames[field]
	if !ok {
		return "", fmt.Errorf("unsupported DWD field %q", field)
	}
	matches, err := filepath.Glob(filepath.Join(dir, fmt.Sprintf("*_%03d_%s.grib2", step, gribName)))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("DWD field %s step %03d: found %d files, want 1", field, step, len(matches))
	}
	return matches[0], nil
}

type gridMeta struct {
	West    float64
	East    float64
	South   float64
	North   float64
	StepDeg float64
}

func inferGrid(values []GridValue) (gridMeta, error) {
	width := 1
	for width < len(values) && values[width].Lat == values[0].Lat {
		width++
	}
	if width == 1 || len(values)%width != 0 {
		return gridMeta{}, fmt.Errorf("DWD grid is not rectangular")
	}
	height := len(values) / width
	step := (values[width-1].Lon - values[0].Lon) / float64(width-1)
	return gridMeta{
		West:    values[0].Lon,
		East:    values[width-1].Lon,
		South:   values[0].Lat,
		North:   values[(height-1)*width].Lat,
		StepDeg: step,
	}, nil
}
