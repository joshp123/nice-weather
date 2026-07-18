package delivery

import (
	"math"
	"testing"
)

func TestWebMercatorRecordIndexMatchesEveryOverlayPixelCentre(t *testing.T) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   13, Cols: 17,
	}
	width, height, err := WebMercatorImageDimensions(spec)
	if err != nil {
		t.Fatal(err)
	}
	westX, eastX, northY, southY := mapLibreImageQuad(spec)
	mapper, err := NewWebMercatorMapper(spec)
	if err != nil {
		t.Fatal(err)
	}
	for pixelRow := 0; pixelRow < height; pixelRow++ {
		fractionY := (float64(pixelRow) + .5) / float64(height)
		normalizedY := northY + fractionY*(southY-northY)
		lat := math.Atan(math.Sinh((.5-normalizedY)*2*math.Pi)) * 180 / math.Pi
		for pixelCol := 0; pixelCol < width; pixelCol++ {
			normalizedX := westX + (float64(pixelCol)+.5)/float64(width)*(eastX-westX)
			lon := normalizedX*360 - 180
			row, col, ok := WebMercatorRecordIndex(spec, lon, lat)
			wantRow, wantCol, wantOK := mapper.Select(pixelRow, pixelCol, func(uint32, uint32) bool { return true })
			if !ok || !wantOK || row != wantRow || col != wantCol {
				t.Fatalf("pixel (%d,%d) centre mapped to record (%d,%d), ok=%v", pixelRow, pixelCol, row, col, ok)
			}
		}
	}
}

func TestWebMercatorPixelIncludesMapLibreCornerRounding(t *testing.T) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   425, Cols: 690,
	}
	// Independently calculated from MapLibre GL 5.24.0 ImageSource's z2,
	// EXTENT=8192 rounded quad. The first displayed column boundary is
	// -23.905119522758152, while the unsnapped boundary is -23.9.
	row, col, ok := WebMercatorPixel(spec, -23.9025, 50)
	if !ok || col != 1 {
		t.Fatalf("rounding-shifted boundary mapped to pixel (%d,%d), ok=%v; want column 1", row, col, ok)
	}
	// The independently calculated first displayed row boundary is
	// 71.46878307812548, while the unsnapped boundary is 71.46826051111876.
	row, col, ok = WebMercatorPixel(spec, 10, 71.4685)
	if !ok || row != 1 {
		t.Fatalf("rounding-shifted boundary mapped to pixel (%d,%d), ok=%v; want row 1", row, col, ok)
	}
}

func TestWebMercatorRecordIndexOwnsOuterEdges(t *testing.T) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   425, Cols: 690,
	}
	tests := []struct {
		lon, lat float64
		col      uint32
	}{
		{spec.Bounds.West, spec.Bounds.North, 0},
		{spec.Bounds.East, spec.Bounds.North, spec.Cols - 1},
		{spec.Bounds.West, spec.Bounds.South, 0},
		{spec.Bounds.East, spec.Bounds.South, spec.Cols - 1},
	}
	for _, test := range tests {
		row, col, ok := WebMercatorRecordIndex(spec, test.lon, test.lat)
		if !ok || col != test.col || (test.lat == spec.Bounds.North && row != 0) || (test.lat == spec.Bounds.South && row != spec.Rows-1) {
			t.Fatalf("edge (%g,%g) mapped to (%d,%d), ok=%v", test.lon, test.lat, row, col, ok)
		}
	}
}

func TestWebMercatorPixelAcceptsVisibleSnappedFringe(t *testing.T) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   425, Cols: 690,
	}
	// MapLibre 5.24 snaps the west edge to -24.005126953125. A click on
	// that visible fringe must resolve to the same first texel it displays.
	row, col, ok := WebMercatorPixel(spec, -24.0025, 50)
	if !ok || col != 0 {
		t.Fatalf("visible snapped fringe mapped to pixel (%d,%d), ok=%v; want column 0", row, col, ok)
	}
	if _, _, ok := WebMercatorPixel(spec, -24.01, 50); ok {
		t.Fatal("coordinate beyond the snapped image quad was accepted")
	}
	if _, _, ok := WebMercatorPixel(spec, 10, 410); ok {
		t.Fatal("periodic projection alias accepted an impossible latitude")
	}
	if _, _, ok := WebMercatorPixel(spec, 370, 50); ok {
		t.Fatal("wrapped longitude outside the geographic domain was accepted")
	}
}

func TestCoastalSourceCellsSurviveNeighbouringNoData(t *testing.T) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   425, Cols: 690,
	}
	tests := []struct {
		name     string
		lon, lat float64
		row      uint32
	}{
		{"Nice", 7.262, 43.7102, 277},
		{"Faro", -7.9304, 37.0194, 344},
		{"Brighton", -0.1372, 50.8225, 206},
		{"Cagliari", 9.1217, 39.2238, 322},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pixelRow, pixelCol, ok := WebMercatorPixel(spec, test.lon, test.lat)
			if !ok {
				t.Fatal("coordinate is outside the overlay")
			}
			cellWidth := (spec.Bounds.East - spec.Bounds.West) / float64(spec.Cols)
			wantCol := uint32(math.Floor((test.lon - spec.Bounds.West) / cellWidth))
			row, col, ok := SelectWebMercatorSource(spec, pixelRow, pixelCol, func(row, col uint32) bool {
				return row == test.row && col == wantCol
			})
			if !ok || row != test.row || col != wantCol {
				t.Fatalf("coastal cell (%d,%d) was lost; selected (%d,%d), ok=%v", test.row, wantCol, row, col, ok)
			}
		})
	}
}

func TestOverlapFallbackDoesNotTurnHairlineContactIntoLand(t *testing.T) {
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	if row, col, ok := SelectWebMercatorSource(spec, 0, 1, func(_ uint32, col uint32) bool { return col == 0 }); ok {
		t.Fatalf("hairline overlap selected land cell (%d,%d) for the water texel", row, col)
	}
}

func TestProductionOverlaysRepresentEverySourceCellWithAtMostFourCandidates(t *testing.T) {
	tests := []struct {
		name       string
		rows, cols uint32
	}{
		{"ICON-EU", 680, 1104},
		{"AIFS", 170, 276},
		{"CHELSA", 425, 690},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := GridSpec{
				Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
				Rows:   test.rows, Cols: test.cols,
			}
			width, height, err := WebMercatorImageDimensions(spec)
			if err != nil {
				t.Fatal(err)
			}
			represented := make([]bool, int(spec.Rows)*int(spec.Cols))
			for pixelRow := 0; pixelRow < height; pixelRow++ {
				for pixelCol := 0; pixelCol < width; pixelCol++ {
					candidateCount := 0
					_, _, _ = SelectWebMercatorSource(spec, pixelRow, pixelCol, func(row, col uint32) bool {
						candidateCount++
						represented[int(row)*int(spec.Cols)+int(col)] = true
						return true
					})
					if candidateCount > 4 {
						t.Fatalf("pixel (%d,%d) considered %d candidates", pixelRow, pixelCol, candidateCount)
					}
				}
			}
			for index, ok := range represented {
				if !ok {
					t.Fatalf("source cell (%d,%d) is not represented", index/int(spec.Cols), index%int(spec.Cols))
				}
			}
		})
	}
}

func BenchmarkWebMercatorMapperSelect(b *testing.B) {
	spec := GridSpec{
		Bounds: GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   425, Cols: 690,
	}
	mapper, err := NewWebMercatorMapper(spec)
	if err != nil {
		b.Fatal(err)
	}
	pixelRow, pixelCol, ok := mapper.Pixel(7.262, 43.7102)
	if !ok {
		b.Fatal("Nice is outside the overlay")
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, _, ok := mapper.Select(pixelRow, pixelCol, func(row, col uint32) bool {
			return row == 277 && col == 312
		}); !ok {
			b.Fatal("coastal fallback failed")
		}
	}
}
