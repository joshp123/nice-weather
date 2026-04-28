package dwd

import (
	"strings"
	"testing"
)

func TestParseGribGetData(t *testing.T) {
	values, err := ParseGribGetData(strings.NewReader(`Latitude Longitude Value
   29.500  -23.500 2.9210379028e+02
   29.500  -23.438 2.9212918091e+02
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("len(values) = %d, want 2", len(values))
	}
	if values[0].Lat != 29.5 || values[0].Lon != -23.5 || values[0].Value != 292.10379028 {
		t.Fatalf("unexpected first value: %+v", values[0])
	}
}

func TestParseGribGetDataRejectsEmptyInput(t *testing.T) {
	if _, err := ParseGribGetData(strings.NewReader("Latitude Longitude Value\n")); err == nil {
		t.Fatal("expected empty data to fail")
	}
}
