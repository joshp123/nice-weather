package dwd

import "testing"

func TestFileURL(t *testing.T) {
	got, err := FileURL("00", "t_2m", "2026042800", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://opendata.dwd.de/weather/nwp/icon-eu/grib/00/t_2m/icon-eu_europe_regular-lat-lon_single-level_2026042800_003_T_2M.grib2.bz2"
	if got != want {
		t.Fatalf("FileURL = %s, want %s", got, want)
	}
}

func TestFileURLRejectsUnknownFolder(t *testing.T) {
	if _, err := FileURL("00", "uv", "2026042800", 0); err == nil {
		t.Fatal("expected unknown folder to fail")
	}
}
