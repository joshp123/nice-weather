package delivery

import "math"

// mapLibreTileExtent is MapLibre GL's pinned internal tile extent. ImageSource
// rounds its four Mercator corners to this coordinate grid before rendering.
const mapLibreTileExtent = 8192

// A fallback cell must cover at least one quarter of the displayed texel.
// This is the two-dimensional worst case for half-pixel alignment at the same
// resolution. Product-geometry tests prove every source cell reaches the
// floor. Smaller contact is subpixel coastline bleed, not useful coverage.
const minimumSourceOverlapFraction = 0.25

// WebMercatorMapper precomputes the fixed geometry shared by every texel in a
// period. Construct it once when rendering or opening a grid.
type WebMercatorMapper struct {
	spec                                     GridSpec
	width, height                            int
	quadWest, quadEast, quadNorth, quadSouth float64
	sourceWest, sourceEast, columnWidth      float64
	latitudeHeight                           float64
}

func NewWebMercatorMapper(spec GridSpec) (*WebMercatorMapper, error) {
	width, height, err := WebMercatorImageDimensions(spec)
	if err != nil {
		return nil, err
	}
	quadWest, quadEast, quadNorth, quadSouth := mapLibreImageQuad(spec)
	sourceWest := webMercatorX(spec.Bounds.West)
	sourceEast := webMercatorX(spec.Bounds.East)
	return &WebMercatorMapper{
		spec: spec, width: width, height: height,
		quadWest: quadWest, quadEast: quadEast, quadNorth: quadNorth, quadSouth: quadSouth,
		sourceWest: sourceWest, sourceEast: sourceEast,
		columnWidth:    (sourceEast - sourceWest) / float64(spec.Cols),
		latitudeHeight: (spec.Bounds.North - spec.Bounds.South) / float64(spec.Rows),
	}, nil
}

// WebMercatorRecordIndex maps a coordinate through the exact overlay pixel
// MapLibre displays at that coordinate, then returns the source-grid record
// used to colour that pixel. This is the canonical point/image parity rule.
func WebMercatorRecordIndex(spec GridSpec, lon, lat float64) (row, col uint32, ok bool) {
	mapper, err := NewWebMercatorMapper(spec)
	if err != nil {
		return 0, 0, false
	}
	destinationRow, destinationCol, ok := mapper.Pixel(lon, lat)
	if !ok {
		return 0, 0, false
	}
	return mapper.Select(destinationRow, destinationCol, func(uint32, uint32) bool { return true })
}

// SelectWebMercatorSource chooses the usable source cell with the largest
// projected overlap with a displayed texel. A valid coastal cell therefore
// survives when the texel centre falls in a neighbouring no-data cell. The
// renderer and point lookup must use this same selector.
func SelectWebMercatorSource(spec GridSpec, pixelRow, pixelCol int, usable func(row, col uint32) bool) (row, col uint32, ok bool) {
	mapper, err := NewWebMercatorMapper(spec)
	if err != nil {
		return 0, 0, false
	}
	return mapper.Select(pixelRow, pixelCol, usable)
}

func (mapper *WebMercatorMapper) Select(pixelRow, pixelCol int, usable func(row, col uint32) bool) (row, col uint32, ok bool) {
	if mapper == nil || pixelRow < 0 || pixelRow >= mapper.height || pixelCol < 0 || pixelCol >= mapper.width || usable == nil {
		return 0, 0, false
	}
	pixelWest := mapper.quadWest + float64(pixelCol)/float64(mapper.width)*(mapper.quadEast-mapper.quadWest)
	pixelEast := mapper.quadWest + float64(pixelCol+1)/float64(mapper.width)*(mapper.quadEast-mapper.quadWest)
	pixelNorth := mapper.quadNorth + float64(pixelRow)/float64(mapper.height)*(mapper.quadSouth-mapper.quadNorth)
	pixelSouth := mapper.quadNorth + float64(pixelRow+1)/float64(mapper.height)*(mapper.quadSouth-mapper.quadNorth)
	pixelArea := (pixelEast - pixelWest) * (pixelSouth - pixelNorth)

	overlapWest := math.Max(pixelWest, mapper.sourceWest)
	overlapEast := math.Min(pixelEast, mapper.sourceEast)
	if overlapWest >= overlapEast {
		return 0, 0, false
	}
	firstCol := clampIndex(int(math.Floor((overlapWest-mapper.sourceWest)/mapper.columnWidth)), int(mapper.spec.Cols))
	lastCol := clampIndex(int(math.Floor((overlapEast-mapper.sourceWest)/mapper.columnWidth)), int(mapper.spec.Cols))

	pixelNorthLatitude := latitudeFromNormalizedWebMercatorY(pixelNorth)
	pixelSouthLatitude := latitudeFromNormalizedWebMercatorY(pixelSouth)
	overlapNorthLatitude := math.Min(pixelNorthLatitude, mapper.spec.Bounds.North)
	overlapSouthLatitude := math.Max(pixelSouthLatitude, mapper.spec.Bounds.South)
	if overlapSouthLatitude >= overlapNorthLatitude {
		return 0, 0, false
	}
	firstRow := clampIndex(int(math.Floor((mapper.spec.Bounds.North-overlapNorthLatitude)/mapper.latitudeHeight)), int(mapper.spec.Rows))
	lastRow := clampIndex(int(math.Floor((mapper.spec.Bounds.North-overlapSouthLatitude)/mapper.latitudeHeight)), int(mapper.spec.Rows))

	bestOverlap := -1.0
	for candidateRow := firstRow; candidateRow <= lastRow; candidateRow++ {
		sourceNorthLatitude := mapper.spec.Bounds.North - float64(candidateRow)*mapper.latitudeHeight
		sourceSouthLatitude := sourceNorthLatitude - mapper.latitudeHeight
		sourceNorthY := normalizedWebMercatorY(sourceNorthLatitude)
		sourceSouthY := normalizedWebMercatorY(sourceSouthLatitude)
		overlapY := math.Min(pixelSouth, sourceSouthY) - math.Max(pixelNorth, sourceNorthY)
		if overlapY <= 0 {
			continue
		}
		for candidateCol := firstCol; candidateCol <= lastCol; candidateCol++ {
			sourceCellWest := mapper.sourceWest + float64(candidateCol)*mapper.columnWidth
			sourceCellEast := sourceCellWest + mapper.columnWidth
			overlapX := math.Min(pixelEast, sourceCellEast) - math.Max(pixelWest, sourceCellWest)
			if overlapX <= 0 {
				continue
			}
			overlap := overlapX * overlapY
			if overlap/pixelArea < minimumSourceOverlapFraction || !usable(uint32(candidateRow), uint32(candidateCol)) {
				continue
			}
			if overlap > bestOverlap {
				row, col, ok, bestOverlap = uint32(candidateRow), uint32(candidateCol), true, overlap
			}
		}
	}
	return row, col, ok
}

func clampIndex(index, count int) int {
	if index < 0 {
		return 0
	}
	if index >= count {
		return count - 1
	}
	return index
}

// WebMercatorPixel returns the exact overlay texel selected at a coordinate.
// Rows and columns are zero-based from the north-west corner.
func WebMercatorPixel(spec GridSpec, lon, lat float64) (row, col int, ok bool) {
	mapper, err := NewWebMercatorMapper(spec)
	if err != nil {
		return 0, 0, false
	}
	return mapper.Pixel(lon, lat)
}

func (mapper *WebMercatorMapper) Pixel(lon, lat float64) (row, col int, ok bool) {
	if mapper == nil {
		return 0, 0, false
	}
	if !finite(lon) || !finite(lat) || lon < -180 || lon > 180 || lat < -webMercatorMaxLatitude || lat > webMercatorMaxLatitude {
		return 0, 0, false
	}
	pointX := webMercatorX(lon)
	pointY := normalizedWebMercatorY(lat)
	if pointX < mapper.quadWest || pointX > mapper.quadEast || pointY < mapper.quadNorth || pointY > mapper.quadSouth {
		return 0, 0, false
	}
	col = int(math.Floor((pointX - mapper.quadWest) / (mapper.quadEast - mapper.quadWest) * float64(mapper.width)))
	if col == mapper.width {
		col--
	}
	row = int(math.Floor((pointY - mapper.quadNorth) / (mapper.quadSouth - mapper.quadNorth) * float64(mapper.height)))
	if row == mapper.height {
		row--
	}
	return row, col, true
}

// mapLibreImageQuad reproduces MapLibre GL 5.24 ImageSource.setCoordinates:
// choose the containing tile zoom, convert normalized Mercator corners to an
// 8192-unit tile grid, then round with JavaScript Math.round semantics.
func mapLibreImageQuad(spec GridSpec) (westX, eastX, northY, southY float64) {
	westX = webMercatorX(spec.Bounds.West)
	eastX = webMercatorX(spec.Bounds.East)
	northY = normalizedWebMercatorY(spec.Bounds.North)
	southY = normalizedWebMercatorY(spec.Bounds.South)
	span := math.Max(eastX-westX, southY-northY)
	zoom := math.Max(0, math.Floor(-math.Log(span)/math.Ln2))
	coordinateScale := math.Exp2(zoom) * mapLibreTileExtent
	snap := func(value float64) float64 {
		return math.Floor(value*coordinateScale+0.5) / coordinateScale
	}
	return snap(westX), snap(eastX), snap(northY), snap(southY)
}

func webMercatorX(longitude float64) float64 { return (longitude + 180) / 360 }

func normalizedWebMercatorY(latitude float64) float64 {
	return 0.5 - webMercatorY(latitude)/(2*math.Pi)
}

func latitudeFromNormalizedWebMercatorY(y float64) float64 {
	return math.Atan(math.Sinh((0.5-y)*2*math.Pi)) * 180 / math.Pi
}

func webMercatorY(latitude float64) float64 {
	radians := latitude * math.Pi / 180
	return math.Asinh(math.Tan(radians))
}
