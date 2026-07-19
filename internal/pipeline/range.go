package pipeline

import (
	"fmt"
	"math"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

// SampleMetricsAtCellCenters deliberately reduces a finer regular grid onto a
// coarser forecast grid. Each target cell receives the source cell containing
// its centre; unavailable source cells remain unavailable. It never fills
// holes or samples a coarse source onto a finer target.
func SampleMetricsAtCellCenters(sourceSpec delivery.GridSpec, source []weather.DailyMetrics, targetSpec delivery.GridSpec) ([]weather.DailyMetrics, error) {
	if len(source) != int(sourceSpec.Rows)*int(sourceSpec.Cols) {
		return nil, fmt.Errorf("source metrics do not match source grid")
	}
	indices, err := cellCenterSourceIndices(sourceSpec, targetSpec)
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

// SampleMaskAtCellCenters projects provider support through the exact same
// cell-centre mapping used for detailed weather. A coarse coastal cell is
// expected only when its centre lands on a supported fine-grid cell.
func SampleMaskAtCellCenters(source FloatGrid, targetSpec delivery.GridSpec) (FloatGrid, error) {
	if err := source.Validate(); err != nil {
		return FloatGrid{}, fmt.Errorf("source mask: %w", err)
	}
	for index, value := range source.Values {
		if value != 0 && value != 1 {
			return FloatGrid{}, fmt.Errorf("source mask cell %d is not binary", index)
		}
	}
	indices, err := cellCenterSourceIndices(source.Spec, targetSpec)
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

func cellCenterSourceIndices(sourceSpec, targetSpec delivery.GridSpec) ([]int, error) {
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

	result := make([]int, int(targetSpec.Rows)*int(targetSpec.Cols))
	for index := range result {
		result[index] = -1
	}
	for row := uint32(0); row < targetSpec.Rows; row++ {
		lat := targetSpec.Bounds.North - (float64(row)+0.5)*targetCellHeight
		sourceRow := int(math.Floor((sourceSpec.Bounds.North - lat) / sourceCellHeight))
		if sourceRow < 0 || sourceRow >= int(sourceSpec.Rows) {
			continue
		}
		for col := uint32(0); col < targetSpec.Cols; col++ {
			lon := targetSpec.Bounds.West + (float64(col)+0.5)*targetCellWidth
			sourceCol := int(math.Floor((lon - sourceSpec.Bounds.West) / sourceCellWidth))
			if sourceCol < 0 || sourceCol >= int(sourceSpec.Cols) {
				continue
			}
			result[int(row)*int(targetSpec.Cols)+int(col)] = sourceRow*int(sourceSpec.Cols) + sourceCol
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
