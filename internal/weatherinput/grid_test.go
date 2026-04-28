package weatherinput

import "testing"

func TestGridInputAtRejectsMaskedSeaCells(t *testing.T) {
	grid := NewGrid(0, 1, 0, 1, 1)
	grid.Horizons["d1"] = []WeatherInput{
		{TempC: 20},
		{TempC: 21},
		{TempC: 22},
		{TempC: 23},
	}
	grid.ApplyLandMask(func(lon, lat, halfStep float64) bool {
		return lon == 1 && lat == 1
	})
	if _, ok := grid.InputAt("d1", 0, 0); ok {
		t.Fatal("expected masked sea cell to be missing")
	}
	got, ok := grid.InputAt("d1", 1, 1)
	if !ok || got.TempC != 23 {
		t.Fatalf("land cell = %#v, %v; want temp 23", got, ok)
	}
}

func TestGridLandMaskKeepsCellsWhoseFootprintTouchesLand(t *testing.T) {
	grid := NewGrid(0, 2, 0, 2, 1)
	grid.Horizons["d1"] = make([]WeatherInput, grid.Width*grid.Height)
	for i := range grid.Horizons["d1"] {
		grid.Horizons["d1"][i] = WeatherInput{TempC: 20 + float64(i)}
	}
	grid.ApplyLandMask(func(lon, lat, halfStep float64) bool {
		return lon+halfStep >= 1.45 && lon-halfStep <= 1.55 && lat+halfStep >= 1.45 && lat-halfStep <= 1.55
	})
	if _, ok := grid.InputAt("d1", 1, 1); !ok {
		t.Fatal("expected land-touching cell to remain visible")
	}
	if _, ok := grid.InputAt("d1", 0, 0); ok {
		t.Fatal("expected distant sea cell to stay hidden")
	}
}
