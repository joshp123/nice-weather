// Package pipeline turns provider GRIB messages into the one canonical daily
// weather representation used by release grids and map overlays.
package pipeline

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/delivery"
)

type Field string

const (
	Temperature  Field = "temperature"
	DewPoint     Field = "dew_point"
	CloudCover   Field = "cloud_cover"
	Rain         Field = "rain"
	WindU        Field = "wind_u"
	WindV        Field = "wind_v"
	Gust         Field = "gust"
	Solar        Field = "solar"
	SolarDirect  Field = "solar_direct"
	SolarDiffuse Field = "solar_diffuse"
)

type Semantics uint8

const (
	Instant Semantics = iota
	IntervalMean
	IntervalMaximum
	IntervalAccumulation
	CumulativeAccumulation
	CumulativeMean
)

func isSolarField(field Field) bool {
	return field == Solar || field == SolarDirect || field == SolarDiffuse
}

// FloatGrid is a regular EPSG:4326 grid. Values are row-major, north to south.
// Invalid cells are NaN; there is no second validity representation to drift.
type FloatGrid struct {
	Spec   delivery.GridSpec
	Values []float32
}

func (g FloatGrid) Validate() error {
	if g.Spec.Rows == 0 || g.Spec.Cols == 0 {
		return errors.New("float grid dimensions must be positive")
	}
	if int64(len(g.Values)) != int64(g.Spec.Rows)*int64(g.Spec.Cols) {
		return fmt.Errorf("float grid has %d values; want %d", len(g.Values), uint64(g.Spec.Rows)*uint64(g.Spec.Cols))
	}
	return nil
}

type Frame struct {
	Field     Field
	Start     time.Time
	End       time.Time
	Semantics Semantics
	Units     string
	// PackingError is the maximum absolute source packing error, expressed in
	// Units. Cumulative differencing uses both adjacent frames' errors instead
	// of an arbitrary global epsilon.
	PackingError float64
	// PackingQuantum is the exact increment of the decoded GRIB values in
	// Units. It is derived from binaryScaleFactor and decimalScaleFactor.
	PackingQuantum     float64
	NativeResolutionKM float64
	Grid               FloatGrid
}

func (f Frame) validate() error {
	if f.Field == "" || f.Start.IsZero() || f.End.IsZero() || f.End.Before(f.Start) {
		return errors.New("forecast frame identity or interval is invalid")
	}
	if f.NativeResolutionKM <= 0 || math.IsNaN(f.NativeResolutionKM) || math.IsInf(f.NativeResolutionKM, 0) {
		return errors.New("forecast frame native resolution must be positive")
	}
	if f.PackingError < 0 || math.IsNaN(f.PackingError) || math.IsInf(f.PackingError, 0) {
		return errors.New("forecast frame packing error must be finite and non-negative")
	}
	if f.PackingQuantum < 0 || math.IsNaN(f.PackingQuantum) || math.IsInf(f.PackingQuantum, 0) {
		return errors.New("forecast frame packing quantum must be finite and non-negative")
	}
	return f.Grid.Validate()
}

func sameSpec(left, right delivery.GridSpec) bool {
	return left.Rows == right.Rows && left.Cols == right.Cols && left.Bounds == right.Bounds
}

func sortedFrames(frames []Frame) []Frame {
	result := append([]Frame(nil), frames...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].End.Equal(result[j].End) {
			return result[i].Start.Before(result[j].Start)
		}
		return result[i].End.Before(result[j].End)
	})
	return result
}

func finite32(value float32) bool   { return !float32NaN(value) && !math.IsInf(float64(value), 0) }
func float32NaN(value float32) bool { return value != value }
