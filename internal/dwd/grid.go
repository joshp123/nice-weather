package dwd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type gribReader func(context.Context, string) ([]GridValue, error)

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
