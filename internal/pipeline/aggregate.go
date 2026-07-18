package pipeline

import (
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

const (
	daytimeStartHour      = 10
	daytimeEndHour        = 20
	minimumCoverage       = 0.90
	completeWindowEpsilon = 1e-9
	wetRateMMPerHour      = 0.10
)

type Series map[Field][]Frame

type intervalKey struct{ start, end time.Time }

type sample struct {
	start, end                         time.Time
	temperature, dewPoint, cloud, rain *FloatGrid
	windU, windV, gust, solar          *FloatGrid
	gustMaximum                        bool
}

type accumulator struct {
	tempSum, tempWeight, minTemp, maxTemp float64
	dewSum, dewWeight                     float64
	cloudSum, cloudWeight                 float64
	rain, rainWeight, wetWeight           float64
	windSum, windWeight                   float64
	gustMax                               float64
	gustSeen                              bool
	solar, solarWeight                    float64
}

type utcWindow struct{ start, end time.Time }

// CellLocations resolves the fixed timezone once for each cell centre. The
// caller may retain the result across every forecast day in one build.
func CellLocations(spec delivery.GridSpec, locate func(lon, lat float64) (*time.Location, error)) ([]*time.Location, error) {
	if locate == nil || spec.Rows == 0 || spec.Cols == 0 {
		return nil, fmt.Errorf("grid and timezone locator are required")
	}
	locations := make([]*time.Location, int(spec.Rows)*int(spec.Cols))
	width := (spec.Bounds.East - spec.Bounds.West) / float64(spec.Cols)
	height := (spec.Bounds.North - spec.Bounds.South) / float64(spec.Rows)
	for row := uint32(0); row < spec.Rows; row++ {
		lat := spec.Bounds.North - (float64(row)+0.5)*height
		for col := uint32(0); col < spec.Cols; col++ {
			lon := spec.Bounds.West + (float64(col)+0.5)*width
			location, err := locate(lon, lat)
			if err != nil {
				return nil, fmt.Errorf("locate timezone at %.5f,%.5f: %w", lon, lat, err)
			}
			if location == nil {
				location = time.UTC
			}
			locations[int(row)*int(spec.Cols)+int(col)] = location
		}
	}
	return locations, nil
}

// AggregateDay computes one 10:00–20:00 local calendar day. Every mean is
// weighted by the actual overlap duration, so ICON's later three-hour steps do
// not count the same as its early hourly steps.
func AggregateDay(date time.Time, locations []*time.Location, input Series, nativeResolutionKM float64) (delivery.GridSpec, []weather.DailyMetrics, error) {
	series, spec, err := normalizedInput(input)
	if err != nil {
		return delivery.GridSpec{}, nil, err
	}
	count := int(spec.Rows) * int(spec.Cols)
	if len(locations) != count {
		return delivery.GridSpec{}, nil, fmt.Errorf("received %d timezones; want %d", len(locations), count)
	}
	if nativeResolutionKM <= 0 {
		return delivery.GridSpec{}, nil, fmt.Errorf("native resolution must be positive")
	}
	windows := make([]utcWindow, count)
	for cell, location := range locations {
		if location == nil {
			location = time.UTC
		}
		// date is a product calendar date, not an instant to translate into
		// every cell's zone (which would move western cells to yesterday).
		year, month, day := date.UTC().Date()
		windows[cell] = utcWindow{
			start: time.Date(year, month, day, daytimeStartHour, 0, 0, 0, location).UTC(),
			end:   time.Date(year, month, day, daytimeEndHour, 0, 0, 0, location).UTC(),
		}
	}
	accumulators := make([]accumulator, count)
	for index := range accumulators {
		accumulators[index].minTemp = math.Inf(1)
		accumulators[index].maxTemp = math.Inf(-1)
	}
	for _, current := range makeSamples(series) {
		intervalHours := current.end.Sub(current.start).Hours()
		if intervalHours <= 0 {
			continue
		}
		for cell := 0; cell < count; cell++ {
			overlap := overlapHours(current.start, current.end, windows[cell].start, windows[cell].end)
			if overlap <= 0 {
				continue
			}
			a := &accumulators[cell]
			if value, ok := gridValue(current.temperature, cell); ok {
				a.tempSum += value * overlap
				a.tempWeight += overlap
				a.minTemp = math.Min(a.minTemp, value)
				a.maxTemp = math.Max(a.maxTemp, value)
			}
			if value, ok := gridValue(current.dewPoint, cell); ok {
				a.dewSum += value * overlap
				a.dewWeight += overlap
			}
			if value, ok := gridValue(current.cloud, cell); ok {
				a.cloudSum += value * overlap
				a.cloudWeight += overlap
			}
			if value, ok := gridValue(current.rain, cell); ok {
				a.rain += value * overlap / intervalHours
				a.rainWeight += overlap
				if value/intervalHours >= wetRateMMPerHour {
					a.wetWeight += overlap
				}
			}
			if u, okU := gridValue(current.windU, cell); okU {
				if v, okV := gridValue(current.windV, cell); okV {
					a.windSum += math.Hypot(u, v) * overlap
					a.windWeight += overlap
				}
			}
			if value, ok := gridValue(current.gust, cell); ok {
				a.gustMax, a.gustSeen = math.Max(a.gustMax, value), true
			}
			if value, ok := gridValue(current.solar, cell); ok {
				a.solar += value * overlap / intervalHours
				a.solarWeight += overlap
			}
		}
	}
	metrics := make([]weather.DailyMetrics, count)
	windowHours := float64(daytimeEndHour - daytimeStartHour)
	for cell, a := range accumulators {
		if a.tempWeight < windowHours*minimumCoverage || a.dewWeight < windowHours*minimumCoverage ||
			a.cloudWeight < windowHours*minimumCoverage || a.rainWeight < windowHours*minimumCoverage || a.windWeight < windowHours*minimumCoverage {
			continue
		}
		metric := weather.DailyMetrics{
			ComfortableTempC: a.tempSum / a.tempWeight,
			MinTempC:         a.minTemp, MaxTempC: a.maxTemp,
			RainMM: a.rain, WetTimeFraction: weather.OptionalFloat{Value: a.wetWeight / windowHours, Available: true},
			CloudCoverPercent:  weather.OptionalFloat{Value: clamp(a.cloudSum/a.cloudWeight, 0, 100), Available: true},
			WindSpeedKPH:       a.windSum / a.windWeight,
			DewPointC:          weather.OptionalFloat{Value: a.dewSum / a.dewWeight, Available: true},
			NativeResolutionKM: nativeResolutionKM,
			Valid:              true,
		}
		if a.gustSeen {
			metric.GustKPH = weather.OptionalFloat{Value: math.Max(a.gustMax, metric.WindSpeedKPH), Available: true}
		}
		// Solar is optional evidence, but a partial sum is not evidence for the
		// complete daytime window. A cell receives solar only when every
		// overlapping interval had both direct and diffuse components (or the
		// complete aggregate-solar value for providers that publish one).
		if math.Abs(a.solarWeight-windowHours) <= completeWindowEpsilon {
			metric.SolarEnergyMJm2 = weather.OptionalFloat{Value: a.solar, Available: true}
		}
		metric.RelativeHumidityPercent = weather.OptionalFloat{Value: relativeHumidity(metric.ComfortableTempC, metric.DewPointC.Value), Available: true}
		if metric.Validate() == nil {
			metrics[cell] = metric
		}
	}
	return spec, metrics, nil
}

func normalizedInput(input Series) (Series, delivery.GridSpec, error) {
	required := []Field{Temperature, DewPoint, CloudCover, Rain, WindU, WindV}
	result := make(Series, len(input))
	var spec delivery.GridSpec
	for _, field := range required {
		if len(input[field]) == 0 {
			return nil, delivery.GridSpec{}, fmt.Errorf("forecast is missing required field %s", field)
		}
	}
	for field, frames := range input {
		normalized, err := NormalizeSeries(frames)
		if err != nil {
			return nil, delivery.GridSpec{}, fmt.Errorf("normalize %s: %w", field, err)
		}
		if len(normalized) == 0 {
			continue
		}
		if spec.Rows == 0 {
			spec = normalized[0].Grid.Spec
		}
		for _, frame := range normalized {
			if !sameSpec(frame.Grid.Spec, spec) {
				return nil, delivery.GridSpec{}, fmt.Errorf("forecast fields do not share one fixed grid")
			}
		}
		result[field] = normalized
	}
	if err := validateSolarComponents(result); err != nil {
		return nil, delivery.GridSpec{}, err
	}
	return result, spec, nil
}

func validateSolarComponents(series Series) error {
	direct, diffuse := series[SolarDirect], series[SolarDiffuse]
	if len(series[Solar]) > 0 && (len(direct) > 0 || len(diffuse) > 0) {
		return fmt.Errorf("aggregate solar and direct/diffuse solar representations cannot be mixed")
	}
	if err := validateNonOverlappingSolar(series[Solar]); err != nil {
		return err
	}
	if len(direct) == 0 && len(diffuse) == 0 {
		return nil
	}
	if len(direct) != len(diffuse) || len(direct) == 0 {
		return fmt.Errorf("direct and diffuse solar series must be complete together")
	}
	for index := range direct {
		if !direct[index].Start.Equal(diffuse[index].Start) || !direct[index].End.Equal(diffuse[index].End) {
			return fmt.Errorf("direct and diffuse solar intervals differ at %s", direct[index].End.Format(time.RFC3339))
		}
	}
	return validateNonOverlappingSolar(direct)
}

func validateNonOverlappingSolar(frames []Frame) error {
	for index := 1; index < len(frames); index++ {
		if frames[index].Start.Before(frames[index-1].End) {
			return fmt.Errorf("solar intervals overlap at %s", frames[index].Start.Format(time.RFC3339))
		}
	}
	return nil
}

func makeSamples(series Series) []sample {
	byInterval := map[intervalKey]*sample{}
	for field, frames := range series {
		for index := range frames {
			frame := &frames[index]
			key := intervalKey{start: frame.Start, end: frame.End}
			current := byInterval[key]
			if current == nil {
				current = &sample{start: frame.Start, end: frame.End}
				byInterval[key] = current
			}
			switch field {
			case Temperature:
				current.temperature = &frame.Grid
			case DewPoint:
				current.dewPoint = &frame.Grid
			case CloudCover:
				current.cloud = &frame.Grid
			case Rain:
				current.rain = &frame.Grid
			case WindU:
				current.windU = &frame.Grid
			case WindV:
				current.windV = &frame.Grid
			case Gust:
				current.gust, current.gustMaximum = &frame.Grid, frame.Semantics == IntervalMaximum
			case Solar, SolarDirect, SolarDiffuse:
				if current.solar == nil {
					current.solar = &frame.Grid
				} else {
					combined := addGrids(*current.solar, frame.Grid)
					current.solar = &combined
				}
			}
		}
	}
	result := make([]sample, 0, len(byInterval))
	for _, current := range byInterval {
		result = append(result, *current)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].start.Before(result[j].start) })
	return result
}

func addGrids(left, right FloatGrid) FloatGrid {
	values := make([]float32, len(left.Values))
	for index := range values {
		if !finite32(left.Values[index]) || !finite32(right.Values[index]) {
			values[index] = float32(math.NaN())
		} else {
			values[index] = left.Values[index] + right.Values[index]
		}
	}
	return FloatGrid{Spec: left.Spec, Values: values}
}

func gridValue(grid *FloatGrid, cell int) (float64, bool) {
	if grid == nil || cell >= len(grid.Values) || !finite32(grid.Values[cell]) {
		return 0, false
	}
	return float64(grid.Values[cell]), true
}

func overlapHours(aStart, aEnd, bStart, bEnd time.Time) float64 {
	start, end := aStart, aEnd
	if bStart.After(start) {
		start = bStart
	}
	if bEnd.Before(end) {
		end = bEnd
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start).Hours()
}

func relativeHumidity(temperature, dewPoint float64) float64 {
	// Magnus formula, sufficiently accurate for surface comfort scoring.
	numerator := math.Exp((17.625 * dewPoint) / (243.04 + dewPoint))
	denominator := math.Exp((17.625 * temperature) / (243.04 + temperature))
	return clamp(100*numerator/denominator, 0, 100)
}

func clamp(value, low, high float64) float64 { return math.Max(low, math.Min(high, value)) }
