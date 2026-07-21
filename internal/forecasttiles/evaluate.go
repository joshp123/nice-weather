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

func evaluateMaxTile(ctx context.Context, id tiledelivery.ID, window pipeline.DailyMetricsWindow, land IsLand) ([]delivery.DisplayScoreRecord, []delivery.DisplayScoreRecord, error) {
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
			isLand, err := land(longitude, latitude)
			if err != nil {
				return nil, nil, fmt.Errorf("land at %.5f,%.5f: %w", longitude, latitude, err)
			}
			if !isLand {
				continue
			}
			record, ok := interpolateRecord(longitude, latitude, window)
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

func interpolateRecord(longitude, latitude float64, window pipeline.DailyMetricsWindow) (delivery.DisplayScoreRecord, bool) {
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
		return delivery.DisplayScoreRecord{}, false
	}
	indexes := [4]int{y0*int(window.Spec.Cols) + x0, y0*int(window.Spec.Cols) + x0 + 1, (y0+1)*int(window.Spec.Cols) + x0, (y0+1)*int(window.Spec.Cols) + x0 + 1}
	records := [4]delivery.DisplayScoreRecord{}
	for index, sourceIndex := range indexes {
		metrics := window.Metrics[sourceIndex]
		if !metrics.Valid {
			return delivery.DisplayScoreRecord{}, false
		}
		records[index] = scoreRecord(metrics)
		if !records[index].Valid {
			return delivery.DisplayScoreRecord{}, false
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
	}, true
}

func scoreRecord(metrics weather.DailyMetrics) delivery.DisplayScoreRecord {
	nice, cycling := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
	if !nice.Available || !cycling.Available {
		return delivery.DisplayScoreRecord{}
	}
	return delivery.DisplayScoreRecord{
		Thermal: uint8(nice.Components.Thermal), Dryness: uint8(nice.Components.Dryness), Sun: uint8(nice.Components.Sun),
		NiceScore: uint8(nice.Score), NiceWind: uint8(nice.Components.Wind),
		CyclingScore: uint8(cycling.Score), CyclingWind: uint8(cycling.Components.Wind), Valid: true,
	}
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
