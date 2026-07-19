package pipeline

import (
	"fmt"
	"math"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

// SampleMetricsAtRepresentativeCells deliberately reduces a finer regular grid
// onto a coarser forecast grid. Each target cell receives the closest valid
// source-cell centre contained by that target cell. This keeps narrow coasts
// and islands whose target centre is water without reaching into a neighbouring
// target cell. It never fills a source hole or samples coarse data onto a finer
// grid.
func SampleMetricsAtRepresentativeCells(sourceSpec delivery.GridSpec, source []weather.DailyMetrics, targetSpec delivery.GridSpec) ([]weather.DailyMetrics, error) {
	if len(source) != int(sourceSpec.Rows)*int(sourceSpec.Cols) {
		return nil, fmt.Errorf("source metrics do not match source grid")
	}
	indices, err := representativeSourceIndices(sourceSpec, targetSpec, func(index int) bool {
		return source[index].Valid
	})
	if err != nil {
		return nil, err
	}
	result := make([]weather.DailyMetrics, len(indices))
	for targetIndex, sourceIndex := range indices {
		if sourceIndex >= 0 && source[sourceIndex].Valid {
			result[targetIndex] = source[sourceIndex]
		}
	}
	return result, nil
}

// SampleMaskAtRepresentativeCells projects provider support through the exact
// same contained-cell rule used for detailed weather. A coarse coastal cell is
// expected when at least one supported fine-grid centre lies within it; support
// never leaks across a target-cell boundary.
func SampleMaskAtRepresentativeCells(source FloatGrid, targetSpec delivery.GridSpec) (FloatGrid, error) {
	if err := source.Validate(); err != nil {
		return FloatGrid{}, fmt.Errorf("source mask: %w", err)
	}
	for index, value := range source.Values {
		if value != 0 && value != 1 {
			return FloatGrid{}, fmt.Errorf("source mask cell %d is not binary", index)
		}
	}
	indices, err := representativeSourceIndices(source.Spec, targetSpec, func(index int) bool {
		return source.Values[index] == 1
	})
	if err != nil {
		return FloatGrid{}, err
	}
	result := FloatGrid{Spec: targetSpec, Values: make([]float32, len(indices))}
	for targetIndex, sourceIndex := range indices {
		if sourceIndex >= 0 {
			result.Values[targetIndex] = source.Values[sourceIndex]
		}
	}
	return result, nil
}

func representativeSourceIndices(sourceSpec, targetSpec delivery.GridSpec, usable func(index int) bool) ([]int, error) {
	if err := delivery.ValidateGridSpec(sourceSpec); err != nil {
		return nil, fmt.Errorf("source grid: %w", err)
	}
	if err := delivery.ValidateGridSpec(targetSpec); err != nil {
		return nil, fmt.Errorf("target grid: %w", err)
	}
	sourceCellWidth := (sourceSpec.Bounds.East - sourceSpec.Bounds.West) / float64(sourceSpec.Cols)
	sourceCellHeight := (sourceSpec.Bounds.North - sourceSpec.Bounds.South) / float64(sourceSpec.Rows)
	targetCellWidth := (targetSpec.Bounds.East - targetSpec.Bounds.West) / float64(targetSpec.Cols)
	targetCellHeight := (targetSpec.Bounds.North - targetSpec.Bounds.South) / float64(targetSpec.Rows)
	if targetCellWidth+1e-12 < sourceCellWidth || targetCellHeight+1e-12 < sourceCellHeight {
		return nil, fmt.Errorf("target grid must not be finer than source grid")
	}
	if usable == nil {
		return nil, fmt.Errorf("source-cell availability is required")
	}

	result := make([]int, int(targetSpec.Rows)*int(targetSpec.Cols))
	for index := range result {
		result[index] = -1
	}
	for row := uint32(0); row < targetSpec.Rows; row++ {
		targetNorth := targetSpec.Bounds.North - float64(row)*targetCellHeight
		targetSouth := targetNorth - targetCellHeight
		centreLat := (targetNorth + targetSouth) / 2
		firstSourceRow := int(math.Ceil((sourceSpec.Bounds.North-targetNorth)/sourceCellHeight - .5))
		lastSourceRow := int(math.Floor((sourceSpec.Bounds.North-targetSouth)/sourceCellHeight - .5))
		firstSourceRow = max(0, firstSourceRow)
		lastSourceRow = min(int(sourceSpec.Rows)-1, lastSourceRow)
		for col := uint32(0); col < targetSpec.Cols; col++ {
			targetWest := targetSpec.Bounds.West + float64(col)*targetCellWidth
			targetEast := targetWest + targetCellWidth
			centreLon := (targetWest + targetEast) / 2
			firstSourceCol := int(math.Ceil((targetWest-sourceSpec.Bounds.West)/sourceCellWidth - .5))
			lastSourceCol := int(math.Floor((targetEast-sourceSpec.Bounds.West)/sourceCellWidth - .5))
			firstSourceCol = max(0, firstSourceCol)
			lastSourceCol = min(int(sourceSpec.Cols)-1, lastSourceCol)
			preferredRow := int(math.Floor((sourceSpec.Bounds.North - centreLat) / sourceCellHeight))
			preferredCol := int(math.Floor((centreLon - sourceSpec.Bounds.West) / sourceCellWidth))
			preferredIndex := preferredRow*int(sourceSpec.Cols) + preferredCol
			if preferredRow >= 0 && preferredRow < int(sourceSpec.Rows) && preferredCol >= 0 && preferredCol < int(sourceSpec.Cols) {
				preferredLat := sourceSpec.Bounds.North - (float64(preferredRow)+.5)*sourceCellHeight
				preferredLon := sourceSpec.Bounds.West + (float64(preferredCol)+.5)*sourceCellWidth
				ownerRow := int(math.Floor((targetSpec.Bounds.North - preferredLat) / targetCellHeight))
				ownerCol := int(math.Floor((preferredLon - targetSpec.Bounds.West) / targetCellWidth))
				if ownerRow == int(row) && ownerCol == int(col) && usable(preferredIndex) {
					result[int(row)*int(targetSpec.Cols)+int(col)] = preferredIndex
					continue
				}
			}
			bestIndex, bestDistance := -1, math.Inf(1)
			for sourceRow := firstSourceRow; sourceRow <= lastSourceRow; sourceRow++ {
				sourceLat := sourceSpec.Bounds.North - (float64(sourceRow)+.5)*sourceCellHeight
				for sourceCol := firstSourceCol; sourceCol <= lastSourceCol; sourceCol++ {
					sourceLon := sourceSpec.Bounds.West + (float64(sourceCol)+.5)*sourceCellWidth
					ownerRow := int(math.Floor((targetSpec.Bounds.North - sourceLat) / targetCellHeight))
					ownerCol := int(math.Floor((sourceLon - targetSpec.Bounds.West) / targetCellWidth))
					if ownerRow != int(row) || ownerCol != int(col) {
						continue
					}
					index := sourceRow*int(sourceSpec.Cols) + sourceCol
					if !usable(index) {
						continue
					}
					dx := (sourceLon - centreLon) / targetCellWidth
					dy := (sourceLat - centreLat) / targetCellHeight
					distance := dx*dx + dy*dy
					if distance < bestDistance {
						bestIndex, bestDistance = index, distance
					}
				}
			}
			result[int(row)*int(targetSpec.Cols)+int(col)] = bestIndex
		}
	}
	return result, nil
}

// ForecastRangeRecords follows the only scoring path: each already-normalized
// daily metric is scored once, then the canonical range summarizer combines
// those daily judgements. A missing promised day invalidates that target cell.
func ForecastRangeRecords(spec delivery.GridSpec, days [][]weather.DailyMetrics, profile weather.Profile) ([]delivery.RangeRecord, error) {
	if profile != weather.ProfileNice && profile != weather.ProfileCycling {
		return nil, fmt.Errorf("forecast range profile must be nice or cycling")
	}
	availability, err := ForecastRangeAvailability(spec, days)
	if err != nil {
		return nil, err
	}
	cells := len(availability)
	records := make([]delivery.RangeRecord, cells)
	results := make([]weather.Result, len(days))
	for cell := 0; cell < cells; cell++ {
		if !availability[cell].Valid {
			continue
		}
		for day := range days {
			results[day] = weather.Score(days[day][cell], profile)
		}
		summary, err := weather.SummarizeRange(results)
		if err != nil {
			return nil, fmt.Errorf("summarize forecast range cell %d: %w", cell, err)
		}
		record, err := delivery.RangeRecordFromSummary(summary)
		if err != nil {
			return nil, fmt.Errorf("encode forecast range cell %d: %w", cell, err)
		}
		records[cell] = record
	}
	return records, nil
}

// ForecastRangeAvailability is the one definition of complete-window support.
// A target cell is available only when every promised day has valid weather.
// The returned metrics intentionally carry no physical values; they are a mask
// for the existing coverage measurement boundary.
func ForecastRangeAvailability(spec delivery.GridSpec, days [][]weather.DailyMetrics) ([]weather.DailyMetrics, error) {
	if err := delivery.ValidateGridSpec(spec); err != nil {
		return nil, err
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("at least one forecast day is required")
	}
	cells := int(spec.Rows) * int(spec.Cols)
	for index, day := range days {
		if len(day) != cells {
			return nil, fmt.Errorf("forecast day %d does not match target grid", index)
		}
	}
	availability := make([]weather.DailyMetrics, cells)
	for cell := 0; cell < cells; cell++ {
		availability[cell].Valid = true
		for day := range days {
			if !days[day][cell].Valid {
				availability[cell].Valid = false
				break
			}
		}
	}
	return availability, nil
}
