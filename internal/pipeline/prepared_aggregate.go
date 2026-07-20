package pipeline

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

// PreparedSeries groups disk-backed source messages by weather field.
type PreparedSeries map[Field][]*PreparedFrame

// DailyMetricsWindow is one core GridWindow with exactly one cell of halo.
// OriginRow and OriginColumn locate the top-left halo cell in the source grid;
// Metrics use Spec row-major order and the core starts at row 1, column 1.
// Worldwide longitude origins may be outside the canonical grid at a wrapped
// antimeridian edge. Latitude and regional longitude halos remain invalid.
type DailyMetricsWindow struct {
	Spec         delivery.GridSpec
	OriginRow    int64
	OriginColumn int64
	Core         GridWindow
	Metrics      []weather.DailyMetrics
}

// AggregatePreparedDayWindow reads each prepared source message once and
// delegates physical normalization, interval reconstruction and cumulative
// differencing exactly once to AggregateDay. It never constructs a whole-grid
// DailyMetrics plane.
func AggregatePreparedDayWindow(
	ctx context.Context,
	date time.Time,
	window GridWindow,
	locate func(lon, lat float64) (*time.Location, error),
	input PreparedSeries,
) (DailyMetricsWindow, error) {
	if err := ctx.Err(); err != nil {
		return DailyMetricsWindow{}, err
	}
	if locate == nil {
		return DailyMetricsWindow{}, fmt.Errorf("timezone locator is required")
	}

	fields := make([]Field, 0, len(input))
	for field := range input {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i] < fields[j] })

	raw := make(Series, len(input))
	var sourceSpec delivery.GridSpec
	var windowSpec delivery.GridSpec
	var originRow, originColumn int64
	var nativeResolutionKM float64
	for _, field := range fields {
		prepared := input[field]
		frames := make([]Frame, 0, len(prepared))
		for index, item := range prepared {
			read, err := item.ReadWindow(ctx, window)
			if err != nil {
				return DailyMetricsWindow{}, fmt.Errorf("read prepared %s frame %d: %w", field, index, err)
			}
			metadata := read.metadata
			if metadata.Field != field {
				return DailyMetricsWindow{}, fmt.Errorf("prepared series key %s contains %s", field, metadata.Field)
			}
			if sourceSpec.Rows == 0 {
				sourceSpec = metadata.Grid
				originRow, originColumn = read.OriginRow, read.OriginColumn
				windowSpec = preparedWindowSpec(sourceSpec, read)
				nativeResolutionKM = metadata.NativeResolutionKM
			} else if !sameSpec(metadata.Grid, sourceSpec) || read.OriginRow != originRow || read.OriginColumn != originColumn || !sameSpec(preparedWindowSpec(metadata.Grid, read), windowSpec) {
				return DailyMetricsWindow{}, fmt.Errorf("prepared forecast fields do not share one fixed window")
			} else if !sameResolution(metadata.NativeResolutionKM, nativeResolutionKM) {
				return DailyMetricsWindow{}, fmt.Errorf("prepared forecast fields do not share one native resolution")
			}
			frames = append(frames, Frame{
				Field: metadata.Field, Start: metadata.Start, End: metadata.End, Semantics: metadata.Semantics,
				Units: metadata.Units, PackingError: metadata.PackingError, PackingQuantum: metadata.PackingQuantum,
				NativeResolutionKM: metadata.NativeResolutionKM,
				Grid:               FloatGrid{Spec: windowSpec, Values: read.Values},
			})
		}
		raw[field] = frames
	}
	if sourceSpec.Rows == 0 {
		return DailyMetricsWindow{}, fmt.Errorf("prepared forecast is empty")
	}

	locations, err := preparedWindowLocations(sourceSpec, windowSpec, originRow, originColumn, locate)
	if err != nil {
		return DailyMetricsWindow{}, err
	}
	aggregatedSpec, metrics, err := AggregateDay(date, locations, raw, nativeResolutionKM)
	if err != nil {
		return DailyMetricsWindow{}, err
	}
	if !sameSpec(aggregatedSpec, windowSpec) {
		return DailyMetricsWindow{}, fmt.Errorf("aggregated prepared window geometry changed")
	}
	return DailyMetricsWindow{
		Spec: aggregatedSpec, OriginRow: originRow, OriginColumn: originColumn,
		Core: window, Metrics: metrics,
	}, nil
}

func preparedWindowLocations(
	source, window delivery.GridSpec,
	originRow, originColumn int64,
	locate func(lon, lat float64) (*time.Location, error),
) ([]*time.Location, error) {
	locations := make([]*time.Location, int(window.Rows)*int(window.Cols))
	longitudeCell := (source.Bounds.East - source.Bounds.West) / float64(source.Cols)
	latitudeCell := (source.Bounds.North - source.Bounds.South) / float64(source.Rows)
	wraps := source.Bounds.West == -180 && source.Bounds.East == 180
	for row := uint32(0); row < window.Rows; row++ {
		sourceRow := originRow + int64(row)
		for column := uint32(0); column < window.Cols; column++ {
			index := int(row)*int(window.Cols) + int(column)
			sourceColumn := originColumn + int64(column)
			if sourceRow < 0 || sourceRow >= int64(source.Rows) || (!wraps && (sourceColumn < 0 || sourceColumn >= int64(source.Cols))) {
				locations[index] = time.UTC
				continue
			}
			if wraps {
				sourceColumn = ((sourceColumn % int64(source.Cols)) + int64(source.Cols)) % int64(source.Cols)
			}
			lon := source.Bounds.West + (float64(sourceColumn)+.5)*longitudeCell
			lat := source.Bounds.North - (float64(sourceRow)+.5)*latitudeCell
			location, err := locate(lon, lat)
			if err != nil {
				return nil, fmt.Errorf("locate timezone at %.5f,%.5f: %w", lon, lat, err)
			}
			if location == nil {
				location = time.UTC
			}
			locations[index] = location
		}
	}
	return locations, nil
}

func sameResolution(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}
