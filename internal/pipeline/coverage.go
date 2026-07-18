package pipeline

import (
	"fmt"
	"math"
	"sort"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

const coverageBlockCells = 16

// CoverageCount is one coverage measurement over immutable expected support.
type CoverageCount struct {
	Finite  int
	Support int
}

func (count CoverageCount) Fraction() float64 {
	if count.Support == 0 {
		return 0
	}
	return float64(count.Finite) / float64(count.Support)
}

type FieldCoverage struct {
	Field      Field
	Frames     int
	WorstFrame CoverageCount
	WorstBlock CoverageCount
}

func (coverage FieldCoverage) Fraction() float64      { return coverage.WorstFrame.Fraction() }
func (coverage FieldCoverage) BlockFraction() float64 { return coverage.WorstBlock.Fraction() }

type ForecastCoverage struct {
	Global     CoverageCount
	WorstBlock CoverageCount
}

func (coverage ForecastCoverage) Fraction() float64      { return coverage.Global.Fraction() }
func (coverage ForecastCoverage) BlockFraction() float64 { return coverage.WorstBlock.Fraction() }

// SupportWithinBounds intersects land with a provider's documented, immutable
// regular-grid domain. Cell centres define support. This must not be inferred
// from the forecast values being checked: doing so would bless a hole shared by
// every field as if the provider did not cover it.
func SupportWithinBounds(land FloatGrid, domain delivery.GridBounds) (FloatGrid, error) {
	if err := validateCoverageMask(land); err != nil {
		return FloatGrid{}, err
	}
	if domain.West >= domain.East || domain.South >= domain.North {
		return FloatGrid{}, fmt.Errorf("provider support bounds are invalid")
	}
	support := FloatGrid{Spec: land.Spec, Values: make([]float32, len(land.Values))}
	width := (land.Spec.Bounds.East - land.Spec.Bounds.West) / float64(land.Spec.Cols)
	height := (land.Spec.Bounds.North - land.Spec.Bounds.South) / float64(land.Spec.Rows)
	for row := 0; row < int(land.Spec.Rows); row++ {
		lat := land.Spec.Bounds.North - (float64(row)+.5)*height
		for col := 0; col < int(land.Spec.Cols); col++ {
			index := row*int(land.Spec.Cols) + col
			lon := land.Spec.Bounds.West + (float64(col)+.5)*width
			if land.Values[index] == 1 && lon >= domain.West && lon <= domain.East && lat >= domain.South && lat <= domain.North {
				support.Values[index] = 1
			}
		}
	}
	return support, nil
}

// MeasureFieldCoverage checks every normalized frame independently. Reporting
// the worst frame and worst coarse spatial block prevents a concentrated hole
// from disappearing inside a large frame-by-cell average.
func MeasureFieldCoverage(input Series, support FloatGrid) ([]FieldCoverage, error) {
	if err := validateCoverageMask(support); err != nil {
		return nil, err
	}
	result := make([]FieldCoverage, 0, len(input))
	for field, frames := range input {
		normalized, err := NormalizeSeries(frames)
		if err != nil {
			return nil, fmt.Errorf("normalize %s coverage: %w", field, err)
		}
		coverage := FieldCoverage{Field: field, Frames: len(normalized)}
		for frameIndex, frame := range normalized {
			if !sameSpec(frame.Grid.Spec, support.Spec) {
				return nil, fmt.Errorf("%s coverage grid does not match expected support", field)
			}
			global, worstBlock := measureGridCoverage(frame.Grid.Values, support)
			if frameIndex == 0 || global.Fraction() < coverage.WorstFrame.Fraction() {
				coverage.WorstFrame = global
			}
			if frameIndex == 0 || worstBlock.Fraction() < coverage.WorstBlock.Fraction() {
				coverage.WorstBlock = worstBlock
			}
		}
		result = append(result, coverage)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Field < result[j].Field })
	return result, nil
}

// MeasureForecastCoverage compares publishable records with the same immutable
// expected support used for source frames.
func MeasureForecastCoverage(spec delivery.GridSpec, metrics []weather.DailyMetrics, support FloatGrid) (ForecastCoverage, error) {
	if err := validateCoverageMask(support); err != nil {
		return ForecastCoverage{}, err
	}
	if !sameSpec(spec, support.Spec) || len(metrics) != len(support.Values) {
		return ForecastCoverage{}, fmt.Errorf("forecast coverage grid does not match expected support")
	}
	valid := make([]float32, len(metrics))
	for index := range valid {
		valid[index] = float32(math.NaN())
		if metrics[index].Valid {
			valid[index] = 1
		}
	}
	global, worstBlock := measureGridCoverage(valid, support)
	if global.Support == 0 {
		return ForecastCoverage{}, fmt.Errorf("forecast expected support contains no land cells")
	}
	return ForecastCoverage{Global: global, WorstBlock: worstBlock}, nil
}

func measureGridCoverage(values []float32, support FloatGrid) (CoverageCount, CoverageCount) {
	global := CoverageCount{}
	worst := CoverageCount{}
	cols := int(support.Spec.Cols)
	rows := int(support.Spec.Rows)
	for blockRow := 0; blockRow < rows; blockRow += coverageBlockCells {
		for blockCol := 0; blockCol < cols; blockCol += coverageBlockCells {
			block := CoverageCount{}
			for row := blockRow; row < rows && row < blockRow+coverageBlockCells; row++ {
				for col := blockCol; col < cols && col < blockCol+coverageBlockCells; col++ {
					index := row*cols + col
					if support.Values[index] != 1 {
						continue
					}
					block.Support++
					global.Support++
					if finite32(values[index]) {
						block.Finite++
						global.Finite++
					}
				}
			}
			if block.Support > 0 && (worst.Support == 0 || block.Fraction() < worst.Fraction()) {
				worst = block
			}
		}
	}
	return global, worst
}

func validateCoverageMask(mask FloatGrid) error {
	if err := mask.Validate(); err != nil {
		return err
	}
	for index, value := range mask.Values {
		if value != 0 && value != 1 {
			return fmt.Errorf("coverage mask cell %d is not binary", index)
		}
	}
	return nil
}
