package tiles

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/landmask"
	"nice-weather-finder/internal/scoring"
	"nice-weather-finder/internal/weatherinput"
)

func TestPalettePixelsUseExactCategoryColors(t *testing.T) {
	cases := []struct {
		score int
		want  color.RGBA
	}{
		{49, color.RGBA{R: 0x9c, G: 0xa3, B: 0xaf, A: 0xff}},
		{50, color.RGBA{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}},
		{62, color.RGBA{R: 0x38, G: 0xbd, B: 0xf8, A: 0xff}},
		{72, color.RGBA{R: 0xa3, G: 0xe6, B: 0x35, A: 0xff}},
		{82, color.RGBA{R: 0x16, G: 0xa3, B: 0x4a, A: 0xff}},
		{92, color.RGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff}},
	}
	for _, tc := range cases {
		if got := ColorForScore(tc.score); got != tc.want {
			t.Fatalf("score %d color = %#v, want %#v", tc.score, got, tc.want)
		}
	}
}

func TestWeatherGridTilePixelsMatchScoringPath(t *testing.T) {
	inputs := weatherinput.NewGrid(0, 20, 40, 60, 1)
	values := make([]weatherinput.WeatherInput, inputs.Width*inputs.Height)
	for i := range values {
		values[i] = weatherinput.WeatherInput{TempC: 24, Humidity: 50, RainMM: 0, CloudPct: 20, WindKMH: 8}
	}
	inputs.Horizons["d1"] = values
	img := RenderWeatherGridTile(inputs, scoring.ProfileGeneral, "d1", 3, 4, 2)
	px, py := 35, 113
	lon, lat := lonLatForPixel(3, 4, 2, px, py)
	input, ok := inputs.InputAt("d1", lon, lat)
	if !ok {
		t.Fatalf("expected input for lon %.2f lat %.2f", lon, lat)
	}
	want := ColorForScore(scoring.Score(input, scoring.ProfileGeneral).Score)
	if got := img.RGBAAt(px, py); got != want {
		t.Fatalf("tile pixel = %#v, want %#v for lon %.2f lat %.2f input %#v", got, want, lon, lat, input)
	}
}

func TestWeatherGridTileAlphaOutsideEurope(t *testing.T) {
	inputs := weatherinput.NewGrid(0, 20, 40, 60, 1)
	inputs.Horizons["d1"] = make([]weatherinput.WeatherInput, inputs.Width*inputs.Height)
	img := RenderWeatherGridTile(inputs, scoring.ProfileGeneral, "d1", 3, 1, 1)
	if got := img.RGBAAt(0, 0); got.A != 0 {
		t.Fatalf("outside-Europe alpha = %d, want 0", got.A)
	}
}

func TestWeatherGridTileAlphaOutsideLand(t *testing.T) {
	inputs := weatherinput.NewGrid(0, 20, 40, 60, 1)
	values := make([]weatherinput.WeatherInput, inputs.Width*inputs.Height)
	for i := range values {
		values[i] = weatherinput.WeatherInput{TempC: 24, Humidity: 50, RainMM: 0, CloudPct: 20, WindKMH: 8}
	}
	inputs.Horizons["d1"] = values
	mask := loadTileTestMask(t, `[[[8,47],[12,47],[12,51],[8,51],[8,47]]]`)
	inputs.ApplyLandMask(mask.TouchesCell)
	img := RenderWeatherGridTile(inputs, scoring.ProfileGeneral, "d1", 3, 4, 2)
	if got := img.RGBAAt(52, 183); got.A == 0 {
		t.Fatal("inside-land pixel is transparent, want colored weather")
	}
	if got := img.RGBAAt(35, 113); got.A != 0 {
		t.Fatalf("outside-land alpha = %d, want 0", got.A)
	}
}

func loadTileTestMask(t *testing.T, coordinates string) landmask.Mask {
	t.Helper()
	path := filepath.Join(t.TempDir(), "land.geojson")
	data := `{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Polygon","coordinates":` + coordinates + `}}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	mask, err := landmask.LoadGeoJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	return mask
}
