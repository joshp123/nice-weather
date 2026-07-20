package pipeline

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestAggregatePreparedDayWindowExactlyMatchesWholeGridCoreAndHalo(t *testing.T) {
	date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		spec   delivery.GridSpec
		window GridWindow
	}{
		{
			name:   "worldwide dateline",
			spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 3, Cols: 4},
			window: GridWindow{Row: 1, Column: 3, Rows: 1, Cols: 1},
		},
		{
			name:   "worldwide north pole",
			spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 3, Cols: 4},
			window: GridWindow{Row: 0, Column: 3, Rows: 1, Cols: 1},
		},
		{
			name:   "regional north west edge",
			spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 4, South: 0, North: 3}, Rows: 3, Cols: 4},
			window: GridWindow{Row: 0, Column: 0, Rows: 1, Cols: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := preparedAggregationSeries(date, test.spec)
			prepared := makePreparedSeries(t, raw)
			wholeSpec, whole, err := AggregateDay(date, utcLocations(test.spec), cloneSeries(raw), 28)
			if err != nil {
				t.Fatal(err)
			}
			locatorCalls := 0
			got, err := AggregatePreparedDayWindow(context.Background(), date, test.window, func(lon, lat float64) (*time.Location, error) {
				locatorCalls++
				if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
					t.Fatalf("timezone lookup received non-canonical coordinate %.2f,%.2f", lon, lat)
				}
				return time.UTC, nil
			}, prepared)
			if err != nil {
				t.Fatal(err)
			}
			if got.Core != test.window || got.OriginRow != int64(test.window.Row)-1 || got.OriginColumn != int64(test.window.Column)-1 || got.Spec.Rows != test.window.Rows+2 || got.Spec.Cols != test.window.Cols+2 {
				t.Fatalf("unexpected explicit window geometry: %+v", got)
			}
			want, validCells := wholeGridWindowMetrics(wholeSpec, whole, test.window)
			if !reflect.DeepEqual(got.Metrics, want) {
				t.Fatalf("prepared metrics differ from whole-grid aggregation\ngot:  %+v\nwant: %+v", got.Metrics, want)
			}
			if locatorCalls != validCells {
				t.Fatalf("timezone locator called %d times; want %d in-grid cells", locatorCalls, validCells)
			}
		})
	}
}

func TestAggregatePreparedDayWindowRejectsMismatchedSeriesKey(t *testing.T) {
	date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	raw := preparedAggregationSeries(date, spec)
	prepared := makePreparedSeries(t, raw)
	prepared[Temperature] = prepared[DewPoint]
	if _, err := AggregatePreparedDayWindow(context.Background(), date, GridWindow{Rows: 1, Cols: 1}, func(float64, float64) (*time.Location, error) {
		return time.UTC, nil
	}, prepared); err == nil {
		t.Fatal("mismatched prepared series key was accepted")
	}
}

func BenchmarkAggregatePreparedDayWindow(b *testing.B) {
	date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 32, Cols: 64}
	prepared := makePreparedSeries(b, preparedAggregationSeries(date, spec))
	window := GridWindow{Row: 8, Column: 16, Rows: 16, Cols: 16}
	locate := func(float64, float64) (*time.Location, error) { return time.UTC, nil }
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := AggregatePreparedDayWindow(context.Background(), date, window, locate, prepared); err != nil {
			b.Fatal(err)
		}
	}
}

func preparedAggregationSeries(date time.Time, spec delivery.GridSpec) Series {
	start := date.Add(10 * time.Hour)
	end := date.Add(20 * time.Hour)
	count := int(spec.Rows) * int(spec.Cols)
	values := func(base, increment float32) []float32 {
		result := make([]float32, count)
		for index := range result {
			result[index] = base + float32(index)*increment
		}
		return result
	}
	frame := func(field Field, start, end time.Time, semantics Semantics, units string, data []float32) Frame {
		return Frame{Field: field, Start: start, End: end, Semantics: semantics, Units: units, NativeResolutionKM: 28, Grid: FloatGrid{Spec: spec, Values: data}}
	}
	rainBaseline := values(.001, .0001)
	rainTotal := make([]float32, count)
	for index := range rainTotal {
		rainTotal[index] = rainBaseline[index] + .002
	}
	return Series{
		Temperature: {frame(Temperature, start, end, IntervalMean, "K", values(293.15, .1))},
		DewPoint:    {frame(DewPoint, start, end, IntervalMean, "K", values(283.15, .02))},
		CloudCover:  {frame(CloudCover, start, end, IntervalMean, "0-1", values(.2, .005))},
		Rain: {
			frame(Rain, date, start, CumulativeAccumulation, "m", rainBaseline),
			frame(Rain, date, end, CumulativeAccumulation, "m", rainTotal),
		},
		WindU: {frame(WindU, start, end, IntervalMean, "m/s", values(3, .01))},
		WindV: {frame(WindV, start, end, IntervalMean, "m/s", values(4, .01))},
	}
}

func makePreparedSeries(tb testing.TB, raw Series) PreparedSeries {
	tb.Helper()
	result := make(PreparedSeries, len(raw))
	for field, frames := range raw {
		for _, frame := range frames {
			directory, err := os.MkdirTemp(tb.TempDir(), "prepared-series-")
			if err != nil {
				tb.Fatal(err)
			}
			path := filepath.Join(directory, "field.f32")
			payload := make([]byte, len(frame.Grid.Values)*4)
			for index, value := range frame.Grid.Values {
				binary.LittleEndian.PutUint32(payload[index*4:index*4+4], math.Float32bits(value))
			}
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				tb.Fatal(err)
			}
			prepared := &PreparedFrame{
				path: path, directory: directory, metadata: metadataFromFrame(frame),
				wraps: frame.Grid.Spec.Bounds.West == -180 && frame.Grid.Spec.Bounds.East == 180,
			}
			tb.Cleanup(func() { _ = prepared.Close() })
			result[field] = append(result[field], prepared)
		}
	}
	return result
}

func cloneSeries(source Series) Series {
	result := make(Series, len(source))
	for field, frames := range source {
		result[field] = make([]Frame, len(frames))
		for index, frame := range frames {
			result[field][index] = frame
			result[field][index].Grid.Values = append([]float32(nil), frame.Grid.Values...)
		}
	}
	return result
}

func utcLocations(spec delivery.GridSpec) []*time.Location {
	result := make([]*time.Location, int(spec.Rows)*int(spec.Cols))
	for index := range result {
		result[index] = time.UTC
	}
	return result
}

func wholeGridWindowMetrics(spec delivery.GridSpec, whole []weather.DailyMetrics, window GridWindow) ([]weather.DailyMetrics, int) {
	rows, cols := int(window.Rows)+2, int(window.Cols)+2
	result := make([]weather.DailyMetrics, rows*cols)
	valid := 0
	wraps := spec.Bounds.West == -180 && spec.Bounds.East == 180
	for row := 0; row < rows; row++ {
		sourceRow := int64(window.Row) - 1 + int64(row)
		for column := 0; column < cols; column++ {
			sourceColumn := int64(window.Column) - 1 + int64(column)
			if sourceRow < 0 || sourceRow >= int64(spec.Rows) || (!wraps && (sourceColumn < 0 || sourceColumn >= int64(spec.Cols))) {
				continue
			}
			if wraps {
				sourceColumn = ((sourceColumn % int64(spec.Cols)) + int64(spec.Cols)) % int64(spec.Cols)
			}
			result[row*cols+column] = whole[int(sourceRow)*int(spec.Cols)+int(sourceColumn)]
			valid++
		}
	}
	return result, valid
}
