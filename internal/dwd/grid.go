package dwd

import (
	"context"
	"fmt"
	"path/filepath"

	"nice-weather-finder-poc/internal/weatherinput"
)

type gribReader func(context.Context, string) ([]GridValue, error)

var stepZeroFiles = map[string]string{
	"t_2m":      "t_2m.grib2",
	"relhum_2m": "relhum_2m.grib2",
	"tot_prec":  "tot_prec.grib2",
	"clct":      "clct.grib2",
	"u_10m":     "u_10m.grib2",
	"v_10m":     "v_10m.grib2",
}

func LoadStepZeroGrid(ctx context.Context, dir string) (weatherinput.Grid, error) {
	return loadStepZeroGrid(ctx, dir, ReadGribValues)
}

func loadStepZeroGrid(ctx context.Context, dir string, read gribReader) (weatherinput.Grid, error) {
	values := map[string][]GridValue{}
	for key, file := range stepZeroFiles {
		fieldValues, err := read(ctx, filepath.Join(dir, file))
		if err != nil {
			return weatherinput.Grid{}, err
		}
		values[key] = fieldValues
	}

	t2m := values["t_2m"]
	if len(t2m) < 2 {
		return weatherinput.Grid{}, fmt.Errorf("DWD T_2M grid is too small")
	}
	for key, fieldValues := range values {
		if len(fieldValues) != len(t2m) {
			return weatherinput.Grid{}, fmt.Errorf("DWD field %s has %d values, want %d", key, len(fieldValues), len(t2m))
		}
		for i, value := range fieldValues {
			if value.Lon != t2m[i].Lon || value.Lat != t2m[i].Lat {
				return weatherinput.Grid{}, fmt.Errorf("DWD field %s coordinate mismatch at index %d: %.3f,%.3f != %.3f,%.3f", key, i, value.Lat, value.Lon, t2m[i].Lat, t2m[i].Lon)
			}
		}
	}

	meta, err := inferGrid(t2m)
	if err != nil {
		return weatherinput.Grid{}, err
	}
	grid := weatherinput.NewGrid(meta.West, meta.East, meta.South, meta.North, meta.StepDeg)
	inputs := make([]weatherinput.WeatherInput, grid.Width*grid.Height)
	for i := range t2m {
		x := i % grid.Width
		y := i / grid.Width
		target := y*grid.Width + x
		inputs[target] = MapICONToWeatherInput(IconEUFields{
			T2MKelvin: values["t_2m"][i].Value,
			RelHum2M:  values["relhum_2m"][i].Value,
			TotPrecMM: values["tot_prec"][i].Value,
			CloudPct:  values["clct"][i].Value,
			U10MMS:    values["u_10m"][i].Value,
			V10MMS:    values["v_10m"][i].Value,
		})
	}
	grid.Horizons["now"] = inputs
	return grid, nil
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
