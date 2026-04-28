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

func TestLoadDaytimeGridAggregatesForecastWindow(t *testing.T) {
	ctx := context.Background()
	reader := func(_ context.Context, path string) ([]GridValue, error) {
		value := 0.0
		switch path {
		case "t_2m_010.grib2":
			value = 293.15
		case "t_2m_020.grib2":
			value = 297.15
		case "relhum_2m_010.grib2":
			value = 50
		case "relhum_2m_020.grib2":
			value = 70
		case "tot_prec_010.grib2":
			value = 2
		case "tot_prec_020.grib2":
			value = 6
		case "clct_010.grib2":
			value = 20
		case "clct_020.grib2":
			value = 60
		case "u_10m_010.grib2", "u_10m_020.grib2":
			value = 3
		case "v_10m_010.grib2", "v_10m_020.grib2":
			value = 4
		default:
			t.Fatalf("unexpected path %s", path)
		}
		return fourCellValues(value), nil
	}
	grid, err := loadDaytimeGrid(ctx, "", []int{10, 20}, reader)
	if err != nil {
		t.Fatal(err)
	}
	got := grid.Horizons["d1"][0]
	if got.TempC != 22 || got.Humidity != 60 || got.RainMM != 4 || got.CloudPct != 40 || got.WindKMH != 18 {
		t.Fatalf("unexpected daytime input: %+v", got)
	}
}

func TestLoadDaytimeGridRejectsCoordinateMismatch(t *testing.T) {
	ctx := context.Background()
	reader := func(_ context.Context, path string) ([]GridValue, error) {
		values := fourCellValues(280)
		if path == "relhum_2m_010.grib2" {
			values[2].Lon = 99
		}
		return values, nil
	}
	if _, err := loadDaytimeGrid(ctx, "", []int{10}, reader); err == nil {
		t.Fatal("expected coordinate mismatch to fail")
	}
}

func fourCellValues(value float64) []GridValue {
	return []GridValue{
		{Lat: 34, Lon: -1, Value: value},
		{Lat: 34, Lon: -0.5, Value: value},
		{Lat: 34.5, Lon: -1, Value: value},
		{Lat: 34.5, Lon: -0.5, Value: value},
	}
}
