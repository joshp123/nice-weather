package dwd

import (
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

func TestValidateAlignedFieldsRejectsCoordinateMismatch(t *testing.T) {
	reference := fourCellValues(280)
	values := map[string][]GridValue{
		"t_2m":      reference,
		"relhum_2m": fourCellValues(60),
	}
	values["relhum_2m"][2].Lon = 99
	if err := validateAlignedFields(values, reference); err == nil {
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
