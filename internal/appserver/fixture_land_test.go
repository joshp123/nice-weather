package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

func TestFixtureLandMaskIdentityAndLandmarks(t *testing.T) {
	if fixtureLandSourceURL != "https://naciscdn.org/naturalearth/5.1.1/10m/physical/ne_10m_land.zip" ||
		fixtureLandSourceSHA256 != "e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc" {
		t.Fatal("fixture Natural Earth source identity drifted")
	}
	if fixtureGridSpec.Bounds != (delivery.GridBounds{West: -25, East: 45, South: 30, North: 72}) ||
		fixtureGridSpec.Rows != 168 || fixtureGridSpec.Cols != 280 {
		t.Fatalf("fixture grid changed without regenerating its land mask: %+v", fixtureGridSpec)
	}
	mask, err := fixtureLandMask()
	if err != nil {
		t.Fatal(err)
	}
	if len(mask) != 5_880 {
		t.Fatalf("fixture land bitset has %d bytes; want 5880", len(mask))
	}
	digest := sha256.Sum256(mask)
	if got := hex.EncodeToString(digest[:]); got != "c510f56eca3192073670ef5468ff49ad22d4796390ff8bd3042a395fc8630ddf" {
		t.Fatalf("fixture land bitset SHA256 = %s", got)
	}
	for name, point := range map[string]struct {
		lon, lat float64
	}{
		"Amsterdam": {4.9041, 52.3676},
		"Malaga":    {-4.4214, 36.7213},
		"Reykjavik": {-21.9426, 64.1466},
		"Madeira":   {-16.9, 32.75},
	} {
		if !fixtureLandCell(mask, fixtureCellIndex(t, point.lon, point.lat)) {
			t.Errorf("%s is water in the fixture mask", name)
		}
	}
	if fixtureLandCell(mask, fixtureCellIndex(t, -15, 45)) {
		t.Error("the open Atlantic is land in the fixture mask")
	}
}

func TestFixtureReleaseHasInvalidOceanAndTransparentSea(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	if err := GenerateFixture(root); err != nil {
		t.Fatal(err)
	}
	current, ok := delivery.CurrentDir(root)
	if !ok {
		t.Fatal("fixture was not published")
	}
	releaseRoot, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := delivery.ValidateProductionRelease(releaseRoot)
	if err != nil {
		t.Fatalf("fixture does not pass production release validation: %v", err)
	}
	if manifest.ProductContract != delivery.ProductContract || manifest.DataStatus != delivery.DataStatusSample || len(manifest.Forecast.Days) != 14 || manifest.Climate == nil || len(manifest.Climate.Months) != 12 {
		t.Fatalf("fixture production contract is incomplete: %+v", manifest)
	}

	day := manifest.Forecast.Days[0]
	grid, err := delivery.OpenGrid(filepath.Join(releaseRoot, filepath.FromSlash(day.GridURL)))
	if err != nil {
		t.Fatal(err)
	}
	defer grid.Close()
	if _, valid, err := grid.RecordAt(-15, 45); err != nil || valid {
		t.Fatalf("Atlantic grid record valid=%t err=%v; want invalid", valid, err)
	}
	landRecord, valid, err := grid.RecordAt(4.9041, 52.3676)
	if err != nil || !valid {
		t.Fatalf("Amsterdam grid record valid=%t err=%v; want valid", valid, err)
	}

	asset := day.Images[delivery.ProfileNice]
	file, err := os.Open(filepath.Join(releaseRoot, filepath.FromSlash(asset.URL)))
	if err != nil {
		t.Fatal(err)
	}
	imageData, err := png.Decode(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	for name, point := range map[string]struct {
		lon, lat float64
	}{
		"Amsterdam": {4.9041, 52.3676},
		"Atlantic":  {-15, 45},
	} {
		x, y := fixtureImagePixel(point.lon, point.lat, asset.Width, asset.Height)
		_, _, _, alpha := imageData.At(x, y).RGBA()
		if name == "Atlantic" && alpha != 0 {
			t.Errorf("Atlantic overlay alpha = %d; want transparent", alpha)
		}
		if name == "Amsterdam" && alpha == 0 {
			t.Error("Amsterdam overlay pixel is transparent")
		}
	}

	landMetric, err := landRecord.WeatherMetrics(1)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSpec := delivery.GridSpec{
		Bounds: delivery.GridBounds{West: 4, East: 5, South: 52, North: 53},
		Rows:   1,
		Cols:   1,
	}
	var canonicalPNG bytes.Buffer
	if err := pipeline.RenderWebMercatorOverlay(&canonicalPNG, canonicalSpec, []weather.DailyMetrics{landMetric}, weather.ProfileNice, fixtureLegend); err != nil {
		t.Fatal(err)
	}
	canonicalImage, err := png.Decode(bytes.NewReader(canonicalPNG.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	landX, landY := fixtureImagePixel(4.9041, 52.3676, asset.Width, asset.Height)
	actualR, actualG, actualB, actualA := imageData.At(landX, landY).RGBA()
	wantR, wantG, wantB, wantA := canonicalImage.At(0, 0).RGBA()
	if actualR != wantR || actualG != wantG || actualB != wantB || actualA != wantA {
		t.Fatalf("fixture land pixel RGBA = (%d,%d,%d,%d); canonical renderer = (%d,%d,%d,%d)", actualR, actualG, actualB, actualA, wantR, wantG, wantB, wantA)
	}
}

func fixtureCellIndex(t *testing.T, lon, lat float64) int {
	t.Helper()
	spec := fixtureGridSpec
	if lon < spec.Bounds.West || lon >= spec.Bounds.East || lat <= spec.Bounds.South || lat > spec.Bounds.North {
		t.Fatalf("point %.4f,%.4f is outside the fixture grid", lon, lat)
	}
	col := int((lon - spec.Bounds.West) / (spec.Bounds.East - spec.Bounds.West) * float64(spec.Cols))
	row := int((spec.Bounds.North - lat) / (spec.Bounds.North - spec.Bounds.South) * float64(spec.Rows))
	return row*int(spec.Cols) + col
}

func fixtureImagePixel(lon, lat float64, width, height int) (int, int) {
	bounds := fixtureGridSpec.Bounds
	x := int((lon - bounds.West) / (bounds.East - bounds.West) * float64(width))
	northY := math.Asinh(math.Tan(bounds.North * math.Pi / 180))
	southY := math.Asinh(math.Tan(bounds.South * math.Pi / 180))
	pointY := math.Asinh(math.Tan(lat * math.Pi / 180))
	y := int((northY - pointY) / (northY - southY) * float64(height))
	return x, y
}
