package forecasttiles

import (
	"context"
	"fmt"
	"math"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

func tileSourceWindow(source delivery.GridSpec, id tiledelivery.ID) (pipeline.GridWindow, bool) {
	worldPixels := int64(tiledelivery.TileSize) * (int64(1) << id.Zoom)
	westPixel := int64(id.X) * tiledelivery.TileSize
	northPixel := int64(id.Y) * tiledelivery.TileSize
	cellWidth := (source.Bounds.East - source.Bounds.West) / float64(source.Cols)
	cellHeight := (source.Bounds.North - source.Bounds.South) / float64(source.Rows)
	worldwide := source.Bounds.West == -180 && source.Bounds.East == 180
	minRow, maxRow, minCol, maxCol := int64(source.Rows), int64(-1), int64(source.Cols), int64(-1)
	for textureY := 0; textureY < tiledelivery.ScalarTextureSize; textureY++ {
		globalY := northPixel + int64(textureY-tiledelivery.ScalarGutter)
		if globalY < 0 || globalY >= worldPixels {
			continue
		}
		latitude := mercatorLatitude(globalY, worldPixels)
		for textureX := 0; textureX < tiledelivery.ScalarTextureSize; textureX++ {
			globalX := westPixel + int64(textureX-tiledelivery.ScalarGutter)
			longitude := (float64(globalX)+.5)/float64(worldPixels)*360 - 180
			canonicalLongitude := longitude
			for canonicalLongitude < -180 {
				canonicalLongitude += 360
			}
			for canonicalLongitude >= 180 {
				canonicalLongitude -= 360
			}
			if !worldwide && (canonicalLongitude < source.Bounds.West || canonicalLongitude >= source.Bounds.East) {
				continue
			}
			if latitude <= source.Bounds.South || latitude > source.Bounds.North {
				continue
			}
			columnCoordinate := (longitude-source.Bounds.West)/cellWidth - .5
			if worldwide {
				columnCoordinate = (longitude-source.Bounds.West)/cellWidth - .5
			} else {
				columnCoordinate = (canonicalLongitude-source.Bounds.West)/cellWidth - .5
			}
			rowCoordinate := (source.Bounds.North-latitude)/cellHeight - .5
			for _, row := range []int64{int64(math.Floor(rowCoordinate)), int64(math.Floor(rowCoordinate)) + 1} {
				if row >= 0 && row < int64(source.Rows) {
					minRow, maxRow = min(minRow, row), max(maxRow, row)
				}
			}
			for _, column := range []int64{int64(math.Floor(columnCoordinate)), int64(math.Floor(columnCoordinate)) + 1} {
				if worldwide || (column >= 0 && column < int64(source.Cols)) {
					minCol, maxCol = min(minCol, column), max(maxCol, column)
				}
			}
		}
	}
	if maxRow < minRow || maxCol < minCol {
		return pipeline.GridWindow{}, false
	}
	if worldwide {
		columns := int64(source.Cols)
		if maxCol-minCol+1 >= columns {
			minCol, maxCol = 0, columns-1
		}
		for minCol < 0 {
			minCol, maxCol = minCol+columns, maxCol+columns
		}
		for minCol >= columns {
			minCol, maxCol = minCol-columns, maxCol-columns
		}
	}
	return pipeline.GridWindow{
		Row: uint32(minRow), Column: uint32(minCol),
		Rows: uint32(maxRow - minRow + 1), Cols: uint32(maxCol - minCol + 1),
	}, true
}

func evaluateMaxTile(ctx context.Context, id tiledelivery.ID, window pipeline.DailyMetricsWindow, land TouchesLand) ([]delivery.DisplayScoreRecord, []delivery.DisplayScoreRecord, error) {
	texture := make([]delivery.DisplayScoreRecord, tiledelivery.ScalarTextureSize*tiledelivery.ScalarTextureSize)
	records := make([]delivery.DisplayScoreRecord, tiledelivery.TileSize*tiledelivery.TileSize)
	worldPixels := int64(tiledelivery.TileSize) * (int64(1) << id.Zoom)
	westPixel := int64(id.X) * tiledelivery.TileSize
	northPixel := int64(id.Y) * tiledelivery.TileSize
	for textureY := 0; textureY < tiledelivery.ScalarTextureSize; textureY++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		globalY := northPixel + int64(textureY-tiledelivery.ScalarGutter)
		if globalY < 0 || globalY >= worldPixels {
			continue
		}
		latitude := mercatorLatitude(globalY, worldPixels)
		for textureX := 0; textureX < tiledelivery.ScalarTextureSize; textureX++ {
			globalX := westPixel + int64(textureX-tiledelivery.ScalarGutter)
			canonicalX := globalX % worldPixels
			if canonicalX < 0 {
				canonicalX += worldPixels
			}
			longitude := (float64(canonicalX)+.5)/float64(worldPixels)*360 - 180
			west := float64(canonicalX)/float64(worldPixels)*360 - 180
			east := float64(canonicalX+1)/float64(worldPixels)*360 - 180
			north := mercatorLatitudeEdge(globalY, worldPixels)
			south := mercatorLatitudeEdge(globalY+1, worldPixels)
			isLand, err := land(delivery.GridBounds{West: west, East: east, North: north, South: south})
			if err != nil {
				return nil, nil, fmt.Errorf("land in texel %.5f..%.5f,%.5f..%.5f: %w", west, east, south, north, err)
			}
			if !isLand {
				isLand, err = sourceCellLandAnchor(longitude, latitude, delivery.GridBounds{West: west, East: east, North: north, South: south}, window.Spec, land)
				if err != nil {
					return nil, nil, err
				}
				if !isLand {
					continue
				}
			}
			record, ok, err := interpolateRecord(longitude, latitude, window)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			texture[textureY*tiledelivery.ScalarTextureSize+textureX] = record
			if textureX >= 1 && textureX <= tiledelivery.TileSize && textureY >= 1 && textureY <= tiledelivery.TileSize {
				records[(textureY-1)*tiledelivery.TileSize+(textureX-1)] = record
			}
		}
	}
	return texture, records, nil
}

// sourceCellLandAnchor preserves narrow islands and coastal slivers that are
// present in the land authority at the honest provider-cell scale but vanish
// at the smaller display-texel scale. Only the display texel containing that
// provider cell's centre may inherit its all-touched support, so neighbouring
// water texels do not become a blocky copy of the source cell.
func sourceCellLandAnchor(longitude, latitude float64, target delivery.GridBounds, source delivery.GridSpec, land TouchesLand) (bool, error) {
	originalLongitude := longitude
	span := source.Bounds.East - source.Bounds.West
	for longitude < source.Bounds.West {
		longitude += 360
	}
	for longitude >= source.Bounds.East && span < 360 {
		longitude -= 360
	}
	longitudeShift := longitude - originalLongitude
	target.West += longitudeShift
	target.East += longitudeShift
	cellWidth := (source.Bounds.East - source.Bounds.West) / float64(source.Cols)
	cellHeight := (source.Bounds.North - source.Bounds.South) / float64(source.Rows)
	column := int(math.Floor((longitude - source.Bounds.West) / cellWidth))
	row := int(math.Floor((source.Bounds.North - latitude) / cellHeight))
	if row < 0 || column < 0 || row >= int(source.Rows) || column >= int(source.Cols) {
		return false, nil
	}
	west := source.Bounds.West + float64(column)*cellWidth
	north := source.Bounds.North - float64(row)*cellHeight
	centreLongitude := west + cellWidth/2
	centreLatitude := north - cellHeight/2
	if centreLongitude < target.West || centreLongitude >= target.East || centreLatitude <= target.South || centreLatitude > target.North {
		return false, nil
	}
	sourceCell := delivery.GridBounds{West: west, East: west + cellWidth, North: north, South: north - cellHeight}
	for sourceCell.West < -180 {
		sourceCell.West += 360
		sourceCell.East += 360
	}
	for sourceCell.West >= 180 {
		sourceCell.West -= 360
		sourceCell.East -= 360
	}
	return land(sourceCell)
}

func interpolateRecord(longitude, latitude float64, window pipeline.DailyMetricsWindow) (delivery.DisplayScoreRecord, bool, error) {
	span := window.Spec.Bounds.East - window.Spec.Bounds.West
	for longitude < window.Spec.Bounds.West {
		longitude += 360
	}
	for longitude >= window.Spec.Bounds.East && span < 360 {
		longitude -= 360
	}
	cellWidth := span / float64(window.Spec.Cols)
	cellHeight := (window.Spec.Bounds.North - window.Spec.Bounds.South) / float64(window.Spec.Rows)
	x := (longitude-window.Spec.Bounds.West)/cellWidth - .5
	y := (window.Spec.Bounds.North-latitude)/cellHeight - .5
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	if x0 < 0 || y0 < 0 || x0+1 >= int(window.Spec.Cols) || y0+1 >= int(window.Spec.Rows) {
		return delivery.DisplayScoreRecord{}, false, nil
	}
	indexes := [4]int{y0*int(window.Spec.Cols) + x0, y0*int(window.Spec.Cols) + x0 + 1, (y0+1)*int(window.Spec.Cols) + x0, (y0+1)*int(window.Spec.Cols) + x0 + 1}
	records := [4]delivery.DisplayScoreRecord{}
	for index, sourceIndex := range indexes {
		metrics := window.Metrics[sourceIndex]
		if !metrics.Valid {
			return delivery.DisplayScoreRecord{}, false, nil
		}
		var err error
		records[index], err = scoreRecord(metrics)
		if err != nil {
			return delivery.DisplayScoreRecord{}, false, err
		}
		if !records[index].Valid {
			return delivery.DisplayScoreRecord{}, false, nil
		}
	}
	tx, ty := x-float64(x0), y-float64(y0)
	weights := [4]float64{(1 - tx) * (1 - ty), tx * (1 - ty), (1 - tx) * ty, tx * ty}
	value := func(read func(delivery.DisplayScoreRecord) uint8) uint8 {
		total := 0.0
		for index := range records {
			total += weights[index] * float64(read(records[index]))
		}
		return uint8(math.Round(total))
	}
	return delivery.DisplayScoreRecord{
		Thermal:      value(func(r delivery.DisplayScoreRecord) uint8 { return r.Thermal }),
		Dryness:      value(func(r delivery.DisplayScoreRecord) uint8 { return r.Dryness }),
		Sun:          value(func(r delivery.DisplayScoreRecord) uint8 { return r.Sun }),
		NiceScore:    value(func(r delivery.DisplayScoreRecord) uint8 { return r.NiceScore }),
		NiceWind:     value(func(r delivery.DisplayScoreRecord) uint8 { return r.NiceWind }),
		CyclingScore: value(func(r delivery.DisplayScoreRecord) uint8 { return r.CyclingScore }),
		CyclingWind:  value(func(r delivery.DisplayScoreRecord) uint8 { return r.CyclingWind }),
		Valid:        true,
	}, true, nil
}

func scoreRecord(metrics weather.DailyMetrics) (delivery.DisplayScoreRecord, error) {
	weatherRecord, err := delivery.RecordFromWeather(metrics)
	if err != nil {
		return delivery.DisplayScoreRecord{}, err
	}
	point, err := delivery.DailyPointRecordFromWeather(weatherRecord)
	if err != nil {
		return delivery.DisplayScoreRecord{}, err
	}
	return delivery.DisplayScoreRecord{
		Thermal: point.Thermal, Dryness: point.Dryness, Sun: point.Sun,
		NiceScore: point.NiceScore, NiceWind: point.NiceWind,
		CyclingScore: point.CyclingScore, CyclingWind: point.CyclingWind, Valid: point.Weather.Valid,
	}, nil
}

func textureSampler(id tiledelivery.ID, texture []delivery.DisplayScoreRecord, field Field) tiledelivery.EvaluatedScoreSampler {
	west := int64(id.X)*tiledelivery.TileSize - tiledelivery.ScalarGutter
	north := int64(id.Y)*tiledelivery.TileSize - tiledelivery.ScalarGutter
	world := int64(tiledelivery.TileSize) * (int64(1) << id.Zoom)
	return func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		x := sample.GlobalX
		for x < west {
			x += world
		}
		for x >= west+tiledelivery.ScalarTextureSize {
			x -= world
		}
		column, row := int(x-west), int(sample.GlobalY-north)
		if row < 0 || column < 0 || row >= tiledelivery.ScalarTextureSize || column >= tiledelivery.ScalarTextureSize {
			return 0, false, errorsNewSampleOutside()
		}
		record := texture[row*tiledelivery.ScalarTextureSize+column]
		return fieldValue(record, field)
	}
}

func fieldValue(record delivery.DisplayScoreRecord, field Field) (uint8, bool, error) {
	if !record.Valid {
		return 0, false, nil
	}
	switch field {
	case FieldThermal:
		return record.Thermal, true, nil
	case FieldDryness:
		return record.Dryness, true, nil
	case FieldSun:
		return record.Sun, true, nil
	case FieldNiceScore:
		return record.NiceScore, true, nil
	case FieldNiceWind:
		return record.NiceWind, true, nil
	case FieldCyclingScore:
		return record.CyclingScore, true, nil
	case FieldCyclingWind:
		return record.CyclingWind, true, nil
	default:
		return 0, false, fmt.Errorf("unknown scalar field %q", field)
	}
}

func errorsNewSampleOutside() error {
	return fmt.Errorf("scalar sampler requested a texel outside its prepared texture")
}
func mercatorLatitude(globalY, worldPixels int64) float64 {
	y := (float64(globalY) + .5) / float64(worldPixels)
	return math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
}

func mercatorLatitudeEdge(globalY, worldPixels int64) float64 {
	y := float64(globalY) / float64(worldPixels)
	return math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
}
