package climatebuild

import (
	"math"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

func TestWorldwideGridSpecBindsExactAgERA5Crop(t *testing.T) {
	want := delivery.GridSpec{
		Bounds: delivery.GridBounds{West: -179.95, East: 179.95, South: -85.05, North: 85.05},
		Rows:   1701,
		Cols:   3599,
	}
	if got := WorldwideGridSpec(); got != want {
		t.Fatalf("worldwide grid = %+v; want %+v", got, want)
	}
	if err := delivery.ValidateGridSpec(want); err != nil {
		t.Fatalf("worldwide grid is not a valid delivery grid: %v", err)
	}
	if width := (want.Bounds.East - want.Bounds.West) / float64(want.Cols); math.Abs(width-.1) > 1e-12 {
		t.Fatalf("cell width = %.15g; want 0.1", width)
	}
	if height := (want.Bounds.North - want.Bounds.South) / float64(want.Rows); math.Abs(height-.1) > 1e-12 {
		t.Fatalf("cell height = %.15g; want 0.1", height)
	}
}

func TestWorldIndexMapsEveryWorldwideBoundary(t *testing.T) {
	tests := []struct {
		name                string
		sourceLatitude      int
		sourceLongitude     int
		wantRow, wantColumn int
	}{
		{"north west", 1750, 1, 0, 0},
		{"north east", 1750, 3599, 0, 3598},
		{"south west", 50, 1, 1700, 0},
		{"south east", 50, 3599, 1700, 3598},
		{"equator west", 900, 1, 850, 0},
		{"equator east", 900, 3599, 850, 3598},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, ok := WorldIndex(test.sourceLatitude, test.sourceLongitude)
			if !ok {
				t.Fatal("cell was cropped")
			}
			row, column := index/worldCols, index%worldCols
			if row != test.wantRow || column != test.wantColumn {
				t.Fatalf("world cell = (%d,%d); want (%d,%d)", row, column, test.wantRow, test.wantColumn)
			}
		})
	}
	last, ok := WorldIndex(50, 3599)
	if !ok || last != worldRows*worldCols-1 {
		t.Fatalf("last world index = %d,%t; want %d,true", last, ok, worldRows*worldCols-1)
	}
}

func TestWorldIndexRejectsCroppedAndOutOfSourceCells(t *testing.T) {
	tests := []struct {
		name                string
		latitude, longitude int
	}{
		{"south crop", 49, 1},
		{"north crop", 1751, 1},
		{"duplicate west dateline", 900, 0},
		{"below source latitude", -1, 1},
		{"above source latitude", 1801, 1},
		{"below source longitude", 900, -1},
		{"above source longitude", 900, 3600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if index, ok := WorldIndex(test.latitude, test.longitude); ok {
				t.Fatalf("cropped cell mapped to index %d", index)
			}
		})
	}
}

func TestLocateHandlesFullAndBoundaryBlocks(t *testing.T) {
	tests := []struct {
		name                        string
		block                       agera5.BlockCoordinate
		latitudeOffset, lonOffset   int
		wantLatitude, wantLongitude int
	}{
		{"first source cell", agera5.BlockCoordinate{}, 0, 0, 0, 0},
		{"first included latitude", agera5.BlockCoordinate{Latitude: 3, Longitude: 0}, 2, 1, 50, 1},
		{"last included latitude", agera5.BlockCoordinate{Latitude: 109, Longitude: 112}, 6, 15, 1750, 3599},
		{"last source cell", agera5.BlockCoordinate{Latitude: 112, Longitude: 112}, 8, 15, 1800, 3599},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			latitude, longitude, err := Locate(test.block, test.latitudeOffset, test.lonOffset)
			if err != nil {
				t.Fatal(err)
			}
			if latitude != test.wantLatitude || longitude != test.wantLongitude {
				t.Fatalf("source cell = (%d,%d); want (%d,%d)", latitude, longitude, test.wantLatitude, test.wantLongitude)
			}
		})
	}
	if _, _, err := Locate(agera5.BlockCoordinate{Latitude: 112, Longitude: 112}, 9, 0); err == nil {
		t.Fatal("accepted latitude offset beyond short boundary block")
	}
	if _, _, err := Locate(agera5.BlockCoordinate{Latitude: 112, Longitude: 112}, 0, 16); err == nil {
		t.Fatal("accepted longitude offset beyond short boundary block")
	}
	if _, _, err := Locate(agera5.BlockCoordinate{Latitude: 113}, 0, 0); err == nil {
		t.Fatal("accepted block outside the source grid")
	}
}

func TestGeometryMapsRepresentativeMalagaAndAmsterdamCells(t *testing.T) {
	tests := []struct {
		name                        string
		block                       agera5.BlockCoordinate
		latitudeOffset, lonOffset   int
		wantRow, wantColumn         int
		wantLatitude, wantLongitude float64
	}{
		{"Malaga", agera5.BlockCoordinate{Latitude: 79, Longitude: 54}, 3, 27, 483, 1754, 36.7, -4.5},
		{"Amsterdam", agera5.BlockCoordinate{Latitude: 89, Longitude: 57}, 0, 25, 326, 1848, 52.4, 4.9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceLatitude, sourceLongitude, err := Locate(test.block, test.latitudeOffset, test.lonOffset)
			if err != nil {
				t.Fatal(err)
			}
			latitude := -90 + float64(sourceLatitude)/10
			longitude := -180 + float64(sourceLongitude)/10
			if math.Abs(latitude-test.wantLatitude) > 1e-12 || math.Abs(longitude-test.wantLongitude) > 1e-12 {
				t.Fatalf("source centre = %.1f,%.1f; want %.1f,%.1f", latitude, longitude, test.wantLatitude, test.wantLongitude)
			}
			index, ok := WorldIndex(sourceLatitude, sourceLongitude)
			if !ok {
				t.Fatal("representative city cell was cropped")
			}
			row, column := index/worldCols, index%worldCols
			if row != test.wantRow || column != test.wantColumn {
				t.Fatalf("world cell = (%d,%d); want (%d,%d)", row, column, test.wantRow, test.wantColumn)
			}
		})
	}
}

func TestBlockLandMaskReversesLatitudeIntoSourceBlockOrder(t *testing.T) {
	world := emptyWorldwideMask()
	block := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
	setSourceLand(t, &world, 1264, 1728)
	setSourceLand(t, &world, 1279, 1759)

	land, err := BlockLandMask(world, block)
	if err != nil {
		t.Fatal(err)
	}
	if len(land) != 16*32 {
		t.Fatalf("block mask has %d cells; want 512", len(land))
	}
	if !land[0] || !land[15*32+31] {
		t.Fatalf("source-order land cells missing: first=%t last=%t", land[0], land[15*32+31])
	}
	if land[15*32] || land[31] {
		t.Fatal("north-to-south world rows were copied without reversing into source order")
	}
}

func TestBlockLandMaskKeepsCroppedCellsFalse(t *testing.T) {
	world := emptyWorldwideMask()
	block := agera5.BlockCoordinate{Latitude: 3, Longitude: 0}
	setSourceLand(t, &world, 50, 1)
	setSourceLand(t, &world, 63, 31)

	land, err := BlockLandMask(world, block)
	if err != nil {
		t.Fatal(err)
	}
	for longitudeOffset := 0; longitudeOffset < 32; longitudeOffset++ {
		if land[longitudeOffset] || land[32+longitudeOffset] {
			t.Fatalf("cropped latitude row became land at longitude offset %d", longitudeOffset)
		}
	}
	for latitudeOffset := 0; latitudeOffset < 16; latitudeOffset++ {
		if land[latitudeOffset*32] {
			t.Fatalf("cropped longitude zero became land at latitude offset %d", latitudeOffset)
		}
	}
	if !land[2*32+1] || !land[15*32+31] {
		t.Fatal("included cells next to the crop were lost")
	}
}

func TestBlockLandMaskKeepsDatelineSidesDistinct(t *testing.T) {
	world := emptyWorldwideMask()
	setSourceLand(t, &world, 900, 1)
	setSourceLand(t, &world, 900, 3599)

	west, err := BlockLandMask(world, agera5.BlockCoordinate{Latitude: 56, Longitude: 0})
	if err != nil {
		t.Fatal(err)
	}
	east, err := BlockLandMask(world, agera5.BlockCoordinate{Latitude: 56, Longitude: 112})
	if err != nil {
		t.Fatal(err)
	}
	if west[4*32] || !west[4*32+1] {
		t.Fatalf("west dateline cells = duplicate:%t included:%t", west[4*32], west[4*32+1])
	}
	if len(east) != 16*16 || !east[4*16+15] {
		t.Fatalf("east dateline block shape/value = %d/%t", len(east), east[4*16+15])
	}
}

func TestBlockLandMaskRejectsWrongGeometryAndNonBinaryValues(t *testing.T) {
	wrong := pipeline.FloatGrid{
		Spec:   delivery.GridSpec{Bounds: WorldwideGridSpec().Bounds, Rows: 1, Cols: 1},
		Values: []float32{1},
	}
	if _, err := BlockLandMask(wrong, agera5.BlockCoordinate{}); err == nil || !strings.Contains(err.Error(), "canonical worldwide") {
		t.Fatalf("wrong-geometry error = %v", err)
	}

	world := emptyWorldwideMask()
	index, ok := WorldIndex(900, 1)
	if !ok {
		t.Fatal("fixture cell was cropped")
	}
	world.Values[index] = float32(math.NaN())
	if _, err := BlockLandMask(world, agera5.BlockCoordinate{Latitude: 56, Longitude: 0}); err == nil || !strings.Contains(err.Error(), "want 0 or 1") {
		t.Fatalf("non-binary error = %v", err)
	}

	broken := emptyWorldwideMask()
	broken.Values = broken.Values[:len(broken.Values)-1]
	if _, err := BlockLandMask(broken, agera5.BlockCoordinate{}); err == nil || !strings.Contains(err.Error(), "float grid has 6121898 values; want 6121899") {
		t.Fatalf("short-mask error = %v", err)
	}
}

func emptyWorldwideMask() pipeline.FloatGrid {
	spec := WorldwideGridSpec()
	return pipeline.FloatGrid{Spec: spec, Values: make([]float32, int(spec.Rows)*int(spec.Cols))}
}

func setSourceLand(t *testing.T, world *pipeline.FloatGrid, latitudeIndex, longitudeIndex int) {
	t.Helper()
	index, ok := WorldIndex(latitudeIndex, longitudeIndex)
	if !ok {
		t.Fatalf("fixture source cell (%d,%d) is outside the world crop", latitudeIndex, longitudeIndex)
	}
	world.Values[index] = 1
}
