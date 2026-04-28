package dwd

import (
	"context"
	"testing"
)

func TestInferGrid(t *testing.T) {
	meta, err := inferGrid([]GridValue{
		{Lat: 34, Lon: -1, Value: 1},
		{Lat: 34, Lon: -0.5, Value: 2},
		{Lat: 34, Lon: 0, Value: 3},
		{Lat: 34.5, Lon: -1, Value: 4},
		{Lat: 34.5, Lon: -0.5, Value: 5},
		{Lat: 34.5, Lon: 0, Value: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.West != -1 || meta.East != 0 || meta.South != 34 || meta.North != 34.5 || meta.StepDeg != 0.5 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestLoadStepZeroGridRejectsCoordinateMismatch(t *testing.T) {
	ctx := context.Background()
	reader := func(_ context.Context, path string) ([]GridValue, error) {
		values := []GridValue{
			{Lat: 34, Lon: -1, Value: 280},
			{Lat: 34, Lon: -0.5, Value: 281},
			{Lat: 34.5, Lon: -1, Value: 282},
			{Lat: 34.5, Lon: -0.5, Value: 283},
		}
		if path == "relhum_2m.grib2" {
			values[2].Lon = 99
		}
		return values, nil
	}
	if _, err := loadStepZeroGrid(ctx, "", reader); err == nil {
		t.Fatal("expected coordinate mismatch to fail")
	}
}
