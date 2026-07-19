package pipeline

import (
	"errors"
	"fmt"
	"math"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

// DenseForecastScoreSpec is the single display grid used by AIFS outlooks and
// the forecast window. It is an evaluated-score grid, not a claim about the
// native weather evidence. It matches the detailed forecast's 1104-by-680
// regular Europe grid while removing the coarse 28 km blocks from the decision
// surface.
func DenseForecastScoreSpec() delivery.GridSpec {
	return delivery.GridSpec{Bounds: EuropeGridBounds(), Rows: delivery.DenseForecastScoreRows, Cols: delivery.DenseForecastScoreCols}
}

// DenseDailyDisplayScores interpolates canonical score scalars into the dense
// display plane. Four complete neighbours are required for bilinear output.
// At a land coast, a partial neighbourhood falls back to its nearest valid
// contributor; target water always remains transparent.
func DenseDailyDisplayScores(source delivery.PointGeometry, records []delivery.DailyPointRecord, target delivery.GridSpec, land FloatGrid) (delivery.PointGeometry, []delivery.DisplayScoreRecord, error) {
	if len(records) != int(source.Rows)*int(source.Cols) {
		return delivery.PointGeometry{}, nil, errors.New("dense daily source geometry does not match records")
	}
	geometry, landPixels, err := denseLandPixels(target, land)
	if err != nil {
		return delivery.PointGeometry{}, nil, err
	}
	result := make([]delivery.DisplayScoreRecord, len(landPixels))
	sourceLand := denseSourceLand(source, geometry, landPixels)
	for index, isLand := range landPixels {
		if !isLand {
			continue
		}
		contributors := denseContributors(source, geometry, index)
		sourceIndexes, weights, interpolated := contributors.choose(func(index int) bool { return records[index].Weather.Valid }, sourceLand)
		if len(sourceIndexes) == 0 {
			continue
		}
		value := func(read func(delivery.DailyPointRecord) uint8) uint8 {
			return interpolateScore(sourceIndexes, weights, interpolated, func(index int) uint8 { return read(records[index]) })
		}
		result[index] = delivery.DisplayScoreRecord{
			Thermal:      value(func(r delivery.DailyPointRecord) uint8 { return r.Thermal }),
			Dryness:      value(func(r delivery.DailyPointRecord) uint8 { return r.Dryness }),
			Sun:          value(func(r delivery.DailyPointRecord) uint8 { return r.Sun }),
			NiceScore:    value(func(r delivery.DailyPointRecord) uint8 { return r.NiceScore }),
			NiceWind:     value(func(r delivery.DailyPointRecord) uint8 { return r.NiceWind }),
			CyclingScore: value(func(r delivery.DailyPointRecord) uint8 { return r.CyclingScore }),
			CyclingWind:  value(func(r delivery.DailyPointRecord) uint8 { return r.CyclingWind }),
			Valid:        true,
		}
	}
	return geometry, result, nil
}

// DenseRangePointRecords makes the forecast window's stored records the exact
// dense authority for both map pixels and selected summaries.
func DenseRangePointRecords(source delivery.PointGeometry, records []delivery.RangePointRecord, target delivery.GridSpec, land FloatGrid) (delivery.PointGeometry, []delivery.RangePointRecord, error) {
	if len(records) != int(source.Rows)*int(source.Cols) {
		return delivery.PointGeometry{}, nil, errors.New("dense range source geometry does not match records")
	}
	geometry, landPixels, err := denseLandPixels(target, land)
	if err != nil {
		return delivery.PointGeometry{}, nil, err
	}
	result := make([]delivery.RangePointRecord, len(landPixels))
	sourceLand := denseSourceLand(source, geometry, landPixels)
	for index, isLand := range landPixels {
		if !isLand {
			continue
		}
		contributors := denseContributors(source, geometry, index)
		sourceIndexes, weights, interpolated := contributors.choose(func(index int) bool { return records[index].Nice.Valid }, sourceLand)
		if len(sourceIndexes) == 0 {
			continue
		}
		value := func(read func(delivery.RangePointRecord) uint8) uint8 {
			return interpolateScore(sourceIndexes, weights, interpolated, func(index int) uint8 { return read(records[index]) })
		}
		point := delivery.RangePointRecord{}
		for day := 0; day < delivery.ForecastPointDayCount; day++ {
			dayIndex := day
			point.NiceDays[day] = value(func(r delivery.RangePointRecord) uint8 { return r.NiceDays[dayIndex] })
			point.CyclingDays[day] = value(func(r delivery.RangePointRecord) uint8 { return r.CyclingDays[dayIndex] })
		}
		thermal := value(func(r delivery.RangePointRecord) uint8 { return r.Nice.Thermal })
		dryness := value(func(r delivery.RangePointRecord) uint8 { return r.Nice.Dryness })
		sun := value(func(r delivery.RangePointRecord) uint8 { return r.Nice.Sun })
		point.Nice, err = denseRangeSummary(point.NiceDays, thermal, dryness, sun, value(func(r delivery.RangePointRecord) uint8 { return r.Nice.Wind }))
		if err != nil {
			return delivery.PointGeometry{}, nil, fmt.Errorf("dense range nice texel %d: %w", index, err)
		}
		point.Cycling, err = denseRangeSummary(point.CyclingDays, thermal, dryness, sun, value(func(r delivery.RangePointRecord) uint8 { return r.Cycling.Wind }))
		if err != nil {
			return delivery.PointGeometry{}, nil, fmt.Errorf("dense range cycling texel %d: %w", index, err)
		}
		result[index] = point
	}
	return geometry, result, nil
}

func denseRangeSummary(scores [delivery.ForecastPointDayCount]uint8, thermal, dryness, sun, wind uint8) (delivery.RangeRecord, error) {
	components := weather.Components{Thermal: int(thermal), Dryness: int(dryness), Sun: int(sun), Wind: int(wind)}
	days := make([]weather.Result, delivery.ForecastPointDayCount)
	for index, score := range scores {
		days[index] = weather.Result{Available: true, Score: int(score), Rating: weather.Rating(int(score)), Components: components}
	}
	summary, err := weather.SummarizeRange(days)
	if err != nil {
		return delivery.RangeRecord{}, err
	}
	return delivery.RangeRecordFromSummary(summary)
}

func denseLandPixels(target delivery.GridSpec, land FloatGrid) (delivery.PointGeometry, []bool, error) {
	if land.Spec != target || len(land.Values) != int(target.Rows)*int(target.Cols) {
		return delivery.PointGeometry{}, nil, errors.New("dense score land mask does not match target grid")
	}
	for index, value := range land.Values {
		if value != 0 && value != 1 {
			return delivery.PointGeometry{}, nil, fmt.Errorf("dense score land mask cell %d has value %g", index, value)
		}
	}
	geometry, err := delivery.NewPointGeometry(target)
	if err != nil {
		return delivery.PointGeometry{}, nil, err
	}
	mapper, err := delivery.NewWebMercatorMapper(target)
	if err != nil {
		return delivery.PointGeometry{}, nil, err
	}
	pixels := make([]bool, int(geometry.Rows)*int(geometry.Cols))
	for row := 0; row < int(geometry.Rows); row++ {
		for col := 0; col < int(geometry.Cols); col++ {
			_, _, ok := mapper.Select(row, col, func(sourceRow, sourceCol uint32) bool {
				return land.Values[int(sourceRow)*int(target.Cols)+int(sourceCol)] == 1
			})
			pixels[row*int(geometry.Cols)+col] = ok
		}
	}
	return geometry, pixels, nil
}

type scoreContributors struct {
	indexes  [4]int
	weights  [4]float64
	distance [4]float64
	inBounds [4]bool
}

func denseContributors(source, target delivery.PointGeometry, targetIndex int) scoreContributors {
	row, col := targetIndex/int(target.Cols), targetIndex%int(target.Cols)
	x := target.Quad.West + (float64(col)+.5)/float64(target.Cols)*(target.Quad.East-target.Quad.West)
	y := target.Quad.North + (float64(row)+.5)/float64(target.Rows)*(target.Quad.South-target.Quad.North)
	sourceX := (x-source.Quad.West)/(source.Quad.East-source.Quad.West)*float64(source.Cols) - .5
	sourceY := (y-source.Quad.North)/(source.Quad.South-source.Quad.North)*float64(source.Rows) - .5
	x0, y0 := int(math.Floor(sourceX)), int(math.Floor(sourceY))
	tx, ty := sourceX-float64(x0), sourceY-float64(y0)
	coords := [4][2]int{{y0, x0}, {y0, x0 + 1}, {y0 + 1, x0}, {y0 + 1, x0 + 1}}
	weights := [4]float64{(1 - tx) * (1 - ty), tx * (1 - ty), (1 - tx) * ty, tx * ty}
	var result scoreContributors
	result.weights = weights
	for index, coord := range coords {
		r, c := coord[0], coord[1]
		if r >= 0 && c >= 0 && r < int(source.Rows) && c < int(source.Cols) {
			result.inBounds[index] = true
			result.indexes[index] = r*int(source.Cols) + c
			dx, dy := sourceX-float64(c), sourceY-float64(r)
			result.distance[index] = dx*dx + dy*dy
		}
	}
	return result
}

func (contributors scoreContributors) choose(valid func(int) bool, sourceLand []bool) (indexes []int, weights []float64, interpolated bool) {
	all := true
	for index := range contributors.indexes {
		if !contributors.inBounds[index] || !valid(contributors.indexes[index]) {
			all = false
			break
		}
	}
	if all {
		return contributors.indexes[:], contributors.weights[:], true
	}
	// Missing weather over land is a provider gap, not a coastline. Keep it
	// transparent instead of disguising it with a neighbouring score.
	for index := range contributors.indexes {
		if contributors.inBounds[index] && !valid(contributors.indexes[index]) && sourceLand[contributors.indexes[index]] {
			return nil, nil, false
		}
	}
	best, bestDistance := -1, math.Inf(1)
	for index := range contributors.indexes {
		if contributors.inBounds[index] && valid(contributors.indexes[index]) && contributors.distance[index] < bestDistance {
			best, bestDistance = contributors.indexes[index], contributors.distance[index]
		}
	}
	if best < 0 {
		return nil, nil, false
	}
	return []int{best}, []float64{1}, false
}

func denseSourceLand(source, target delivery.PointGeometry, targetLand []bool) []bool {
	result := make([]bool, int(source.Rows)*int(source.Cols))
	for row := 0; row < int(source.Rows); row++ {
		for col := 0; col < int(source.Cols); col++ {
			x := source.Quad.West + (float64(col)+.5)/float64(source.Cols)*(source.Quad.East-source.Quad.West)
			y := source.Quad.North + (float64(row)+.5)/float64(source.Rows)*(source.Quad.South-source.Quad.North)
			targetCol := int(math.Floor((x - target.Quad.West) / (target.Quad.East - target.Quad.West) * float64(target.Cols)))
			targetRow := int(math.Floor((y - target.Quad.North) / (target.Quad.South - target.Quad.North) * float64(target.Rows)))
			if targetRow >= 0 && targetCol >= 0 && targetRow < int(target.Rows) && targetCol < int(target.Cols) {
				result[row*int(source.Cols)+col] = targetLand[targetRow*int(target.Cols)+targetCol]
			}
		}
	}
	return result
}

func interpolateScore(indexes []int, weights []float64, interpolated bool, value func(int) uint8) uint8 {
	if !interpolated {
		return value(indexes[0])
	}
	var total float64
	for index, sourceIndex := range indexes {
		total += weights[index] * float64(value(sourceIndex))
	}
	return uint8(math.Round(math.Max(0, math.Min(100, total))))
}
