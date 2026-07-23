package era5hourly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"
	_ "time/tzdata"

	"nice-weather-finder/internal/weather"
)

const (
	windowStartHour       = 10
	windowEndHour         = 20
	wetHourlyThresholdMM  = .1
	maximumDewPointExcess = .01
	completeWeightEpsilon = 1e-6
	minimumTemperatureC   = -100
	maximumTemperatureC   = 70
	minimumDewPointC      = -100
	maximumDewPointC      = 45
	maximumWindKPH        = 250
	// Six MJ/m² in one hour is an average surface flux of 1.67 kW/m².
	// It is above plausible solar input and below the daily validator ceiling.
	maximumHourlySolarMJm2 = 6

	TimezoneContractVersion = "iana-civil-daytime-2006-2025-v1"
	climateFirstYear        = 2006
	climateLastYear         = 2025
)

// Cell identifies one ERA5 source cell and its civil timezone.
type Cell struct {
	SourceIndex         int
	Latitude, Longitude float64
	TimezoneID          string
	TimezoneFingerprint string
}

// DailyMetrics is the source-neutral typed boundary consumed by climate
// distribution builders. Date is a UTC midnight calendar label.
type DailyMetrics struct {
	CellIndex int
	Date      time.Time
	Latitude  float64
	Longitude float64
	Metrics   weather.DailyMetrics
}

// AggregationStats records bounded source and normalization evidence.
type AggregationStats struct {
	Frames                         int
	Days                           int
	HumiditySaturationClampedHours int
	MaximumDewPointExcessC         float64
}

// MonthSink starts one atomic month transaction. A transaction must not make
// added records visible until Commit succeeds.
type MonthSink interface {
	BeginMonth(context.Context, int, time.Month, []Cell) (MonthTransaction, error)
}

// MonthTransaction receives one month and either commits it completely or
// discards it completely.
type MonthTransaction interface {
	Add(DailyMetrics) error
	Commit() error
	Abort() error
}

type cellState struct {
	date                                   time.Time
	weight, windowHours                    float64
	temperature, rain, wet, solar, cloud   float64
	wind, dewPoint, relativeHumidity       float64
	minimumTemperature, maximumTemperature float64
}

type instantSample struct {
	temperature, dewPoint, cloud, wind, relativeHumidity float64
}

type zoneGroup struct {
	id           string
	location     *time.Location
	fingerprint  string
	cells        []int
	windows      map[string]civilWindow
	expectedDays int
}

type civilWindow struct {
	label      time.Time
	start, end time.Time
}

// AggregateMonth converts complete consecutive source hours into canonical
// local-day weather metrics. It retains one source frame and one fixed state
// record per selected cell. It does not score, persist or publish.
func AggregateMonth(
	ctx context.Context,
	source FrameSource,
	year int,
	month time.Month,
	cells []Cell,
	sink MonthSink,
) (stats AggregationStats, returnErr error) {
	if ctx == nil || source == nil || year < climateFirstYear || year > climateLastYear || month < time.January || month > time.December || len(cells) == 0 || sink == nil {
		return stats, errors.New("ERA5 hourly aggregation requires a source, month, cells and sink")
	}
	groups, err := prepareGroups(year, month, cells)
	if err != nil {
		return stats, err
	}
	firstRequired, lastRequired := requiredEnvelope(groups)
	transaction, err := sink.BeginMonth(ctx, year, month, cells)
	if err != nil {
		return stats, fmt.Errorf("begin ERA5 month transaction: %w", err)
	}
	if transaction == nil {
		return stats, errors.New("ERA5 month sink returned no transaction")
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if abortErr := transaction.Abort(); abortErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("abort ERA5 month transaction: %w", abortErr))
		}
	}()
	states := make([]cellState, len(cells))
	for index := range states {
		resetState(&states[index])
	}
	previousSamples := make([]instantSample, len(cells))
	currentSamples := make([]instantSample, len(cells))
	previousSet := make([]bool, len(cells))
	daysByCell := make([]int, len(cells))
	var previousFrameTime time.Time
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		frame, nextErr := source.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return stats, nextErr
		}
		stats.Frames++
		if err := frame.validateShape(); err != nil {
			return stats, err
		}
		if stats.Frames == 1 && frame.Rows == GridRows && frame.Columns == GridColumns {
			if err := validateSourceCoordinates(cells); err != nil {
				return stats, err
			}
		}
		if stats.Frames == 1 && !frame.ValidTime.Equal(firstRequired) {
			return stats, fmt.Errorf("ERA5 hourly month begins at %s; want precursor %s", frame.ValidTime.Format(time.RFC3339), firstRequired.Format(time.RFC3339))
		}
		if !previousFrameTime.IsZero() && !frame.ValidTime.Equal(previousFrameTime.Add(time.Hour)) {
			return stats, errors.New("ERA5 hourly month has a time gap")
		}
		if frame.ValidTime.After(lastRequired) {
			return stats, fmt.Errorf("ERA5 hourly month contains a frame after %s", lastRequired.Format(time.RFC3339))
		}
		for index, cell := range cells {
			if cell.SourceIndex >= frame.Rows*frame.Columns {
				return stats, fmt.Errorf("ERA5 source index %d is outside the frame", cell.SourceIndex)
			}
			sample, err := readInstantSample(frame, cell.SourceIndex, &stats)
			if err != nil {
				return stats, fmt.Errorf("ERA5 cell %d hour %s: %w", index, frame.ValidTime.Format(time.RFC3339), err)
			}
			currentSamples[index] = sample
		}
		for _, group := range groups {
			localEnd := frame.ValidTime.In(group.location)
			localYear, localMonth, localDay := localEnd.Date()
			if localYear != year || localMonth != month {
				continue
			}
			window, exists := group.windows[dateKey(localYear, localMonth, localDay)]
			if !exists {
				continue
			}
			label, windowStart, windowEnd := window.label, window.start, window.end
			intervalStart := frame.ValidTime.Add(-time.Hour)
			overlapStart, overlapEnd := later(intervalStart, windowStart), earlier(frame.ValidTime, windowEnd)
			if !overlapEnd.After(overlapStart) {
				continue
			}
			weight := overlapEnd.Sub(overlapStart).Hours()
			startFraction := overlapStart.Sub(intervalStart).Hours()
			endFraction := overlapEnd.Sub(intervalStart).Hours()
			for _, cellIndex := range group.cells {
				cell := cells[cellIndex]
				if !previousSet[cellIndex] {
					return stats, fmt.Errorf("ERA5 cell %d lacks the temperature precursor for %s", cellIndex, label.Format(time.DateOnly))
				}
				state := &states[cellIndex]
				if !state.date.IsZero() && !state.date.Equal(label) {
					if err := flushCell(cellIndex, cells, state, daysByCell, transaction, &stats); err != nil {
						return stats, err
					}
				}
				if state.date.IsZero() {
					state.date = label
					state.windowHours = windowEnd.Sub(windowStart).Hours()
				}
				if err := addHour(state, frame, cell.SourceIndex, previousSamples[cellIndex], currentSamples[cellIndex], weight, startFraction, endFraction); err != nil {
					return stats, fmt.Errorf("ERA5 cell %d day %s: %w", cellIndex, label.Format(time.DateOnly), err)
				}
			}
		}
		previousSamples, currentSamples = currentSamples, previousSamples
		for index := range cells {
			previousSet[index] = true
		}
		previousFrameTime = frame.ValidTime
	}
	if stats.Frames == 0 || !previousFrameTime.Equal(lastRequired) {
		return stats, fmt.Errorf("ERA5 hourly month ends at %s; want %s", previousFrameTime.Format(time.RFC3339), lastRequired.Format(time.RFC3339))
	}
	for index := range states {
		if !states[index].date.IsZero() {
			if err := flushCell(index, cells, &states[index], daysByCell, transaction, &stats); err != nil {
				return stats, err
			}
		}
	}
	for _, group := range groups {
		for _, index := range group.cells {
			if daysByCell[index] != group.expectedDays {
				return stats, fmt.Errorf("ERA5 cell %d emitted %d days; want %d valid civil days", index, daysByCell[index], group.expectedDays)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if err := transaction.Commit(); err != nil {
		return stats, fmt.Errorf("commit ERA5 month transaction: %w", err)
	}
	committed = true
	return stats, nil
}

func prepareGroups(year int, month time.Month, cells []Cell) ([]zoneGroup, error) {
	seenSource := make(map[int]struct{}, len(cells))
	byZone := make(map[string]*zoneGroup)
	for index, cell := range cells {
		if cell.SourceIndex < 0 || cell.TimezoneID == "" || cell.TimezoneFingerprint == "" ||
			!finite(cell.Latitude, cell.Longitude) || cell.Latitude < -90 || cell.Latitude > 90 || cell.Longitude < -180 || cell.Longitude > 180 {
			return nil, fmt.Errorf("ERA5 cell %d is invalid", index)
		}
		if _, exists := seenSource[cell.SourceIndex]; exists {
			return nil, fmt.Errorf("ERA5 source index %d is repeated", cell.SourceIndex)
		}
		seenSource[cell.SourceIndex] = struct{}{}
		group := byZone[cell.TimezoneID]
		if group == nil {
			location, err := time.LoadLocation(cell.TimezoneID)
			if err != nil {
				return nil, fmt.Errorf("load ERA5 cell %d timezone %q: %w", index, cell.TimezoneID, err)
			}
			fingerprint, err := TimezoneFingerprint(cell.TimezoneID)
			if err != nil {
				return nil, err
			}
			if fingerprint != cell.TimezoneFingerprint {
				return nil, fmt.Errorf("ERA5 cell %d timezone contract changed for %s", index, cell.TimezoneID)
			}
			windows, err := monthWindows(year, month, location)
			if err != nil {
				return nil, fmt.Errorf("ERA5 timezone %s: %w", cell.TimezoneID, err)
			}
			group = &zoneGroup{
				id: cell.TimezoneID, location: location, fingerprint: fingerprint,
				windows: windows, expectedDays: len(windows),
			}
			byZone[cell.TimezoneID] = group
		} else if group.fingerprint != cell.TimezoneFingerprint {
			return nil, fmt.Errorf("ERA5 timezone %s has conflicting contracts", cell.TimezoneID)
		}
		group.cells = append(group.cells, index)
	}
	names := make([]string, 0, len(byZone))
	for name := range byZone {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]zoneGroup, 0, len(names))
	for _, name := range names {
		result = append(result, *byZone[name])
	}
	return result, nil
}

var timezoneFingerprintCache sync.Map

// TimezoneFingerprint binds the IANA timezone ID to every 10:00-20:00 civil
// window used by the 2006-2025 climate contract.
func TimezoneFingerprint(timezoneID string) (string, error) {
	if cached, ok := timezoneFingerprintCache.Load(timezoneID); ok {
		return cached.(string), nil
	}
	location, err := time.LoadLocation(timezoneID)
	if err != nil {
		return "", fmt.Errorf("load timezone %q: %w", timezoneID, err)
	}
	fingerprint, err := timezoneFingerprint(timezoneID, location)
	if err != nil {
		return "", err
	}
	timezoneFingerprintCache.Store(timezoneID, fingerprint)
	return fingerprint, nil
}

func timezoneFingerprint(timezoneID string, location *time.Location) (string, error) {
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s\n%s\n", TimezoneContractVersion, timezoneID)
	for year := climateFirstYear; year <= climateLastYear; year++ {
		for month := time.January; month <= time.December; month++ {
			lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for day := 1; day <= lastDay; day++ {
				window, exists, err := civilDayWindow(year, month, day, location)
				if err != nil {
					return "", fmt.Errorf("fingerprint timezone %s: %w", timezoneID, err)
				}
				if !exists {
					_, _ = fmt.Fprintf(digest, "%s skipped\n", dateKey(year, month, day))
					continue
				}
				_, startOffset := window.start.In(location).Zone()
				_, endOffset := window.end.In(location).Zone()
				_, _ = fmt.Fprintf(digest, "%s %d %d %d %d\n", dateKey(year, month, day), window.start.Unix(), window.end.Unix(), startOffset, endOffset)
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func monthWindows(year int, month time.Month, location *time.Location) (map[string]civilWindow, error) {
	result := make(map[string]civilWindow)
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	for day := 1; day <= lastDay; day++ {
		window, exists, err := civilDayWindow(year, month, day, location)
		if err != nil {
			return nil, err
		}
		if exists {
			result[dateKey(year, month, day)] = window
		}
	}
	return result, nil
}

func civilDayWindow(year int, month time.Month, day int, location *time.Location) (civilWindow, bool, error) {
	label := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	start := time.Date(year, month, day, windowStartHour, 0, 0, 0, location)
	end := time.Date(year, month, day, windowEndHour, 0, 0, 0, location)
	if !matchesCivil(start, year, month, day, windowStartHour, location) ||
		!matchesCivil(end, year, month, day, windowEndHour, location) {
		if !civilDateExists(year, month, day, location) {
			return civilWindow{}, false, nil
		}
		return civilWindow{}, false, fmt.Errorf("%s has a nonexistent daytime-window boundary", dateKey(year, month, day))
	}
	if !end.After(start) || end.Sub(start) > 24*time.Hour {
		return civilWindow{}, false, fmt.Errorf("%s has an invalid daytime window", dateKey(year, month, day))
	}
	return civilWindow{label: label, start: start.UTC(), end: end.UTC()}, true, nil
}

func civilDateExists(year int, month time.Month, day int, location *time.Location) bool {
	for hour := 0; hour < 24; hour++ {
		if matchesCivil(time.Date(year, month, day, hour, 0, 0, 0, location), year, month, day, hour, location) {
			return true
		}
	}
	return false
}

func matchesCivil(value time.Time, year int, month time.Month, day, hour int, location *time.Location) bool {
	local := value.In(location)
	localYear, localMonth, localDay := local.Date()
	return localYear == year && localMonth == month && localDay == day && local.Hour() == hour && local.Minute() == 0 && local.Second() == 0
}

func dateKey(year int, month time.Month, day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func validateSourceCoordinates(cells []Cell) error {
	for index, cell := range cells {
		if cell.SourceIndex >= GridRows*GridColumns {
			return fmt.Errorf("ERA5 source index %d is outside the worldwide grid", cell.SourceIndex)
		}
		row, column := cell.SourceIndex/GridColumns, cell.SourceIndex%GridColumns
		latitude := 90 - float64(row)*.25
		longitude := float64(column) * .25
		if longitude > 180 {
			longitude -= 360
		}
		if math.Abs(cell.Latitude-latitude) > 1e-9 || math.Abs(cell.Longitude-longitude) > 1e-9 {
			return fmt.Errorf("ERA5 cell %d coordinate %.9g,%.9g does not match source index %d at %.9g,%.9g", index, cell.Latitude, cell.Longitude, cell.SourceIndex, latitude, longitude)
		}
	}
	return nil
}

// RequiredEnvelope returns the inclusive valid-time bounds that the hourly
// producer must stream for one complete civil-local month.
func RequiredEnvelope(year int, month time.Month, cells []Cell) (time.Time, time.Time, error) {
	if year < climateFirstYear || year > climateLastYear || month < time.January || month > time.December || len(cells) == 0 {
		return time.Time{}, time.Time{}, errors.New("ERA5 hourly envelope requires a supported month and cells")
	}
	groups, err := prepareGroups(year, month, cells)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	first, last := requiredEnvelope(groups)
	return first, last, nil
}

func requiredEnvelope(groups []zoneGroup) (time.Time, time.Time) {
	var first, last time.Time
	for _, group := range groups {
		for _, window := range group.windows {
			start := window.start.Truncate(time.Hour)
			end := window.end
			if !end.Equal(end.Truncate(time.Hour)) {
				end = end.Truncate(time.Hour).Add(time.Hour)
			}
			if first.IsZero() || start.Before(first) {
				first = start
			}
			if last.IsZero() || end.After(last) {
				last = end
			}
		}
	}
	return first, last
}

func readInstantSample(frame Frame, sourceIndex int, stats *AggregationStats) (instantSample, error) {
	temperature := float64(frame.TemperatureK[sourceIndex]) - 273.15
	dewPoint := float64(frame.DewPointK[sourceIndex]) - 273.15
	rain := float64(frame.RainM[sourceIndex]) * 1000
	solar := float64(frame.SolarJM2[sourceIndex]) / 1_000_000
	cloud := float64(frame.Cloud[sourceIndex])
	wind := math.Hypot(float64(frame.WindUMPS[sourceIndex]), float64(frame.WindVMPS[sourceIndex])) * 3.6
	excess := math.Max(dewPoint-temperature, 0)
	if excess > maximumDewPointExcess+1e-9 {
		return instantSample{}, fmt.Errorf("dew point exceeds synchronous temperature by %.6f C", excess)
	}
	if excess > 0 {
		stats.HumiditySaturationClampedHours++
		stats.MaximumDewPointExcessC = math.Max(stats.MaximumDewPointExcessC, excess)
	}
	dewPoint = math.Min(dewPoint, temperature)
	humidity := relativeHumidity(temperature, dewPoint)
	if temperature < minimumTemperatureC || temperature > maximumTemperatureC ||
		dewPoint < minimumDewPointC || dewPoint > maximumDewPointC ||
		wind > maximumWindKPH ||
		rain < 0 || solar < 0 || solar > maximumHourlySolarMJm2 || cloud < 0 || cloud > 1 ||
		!finite(temperature, dewPoint, humidity, rain, solar, cloud, wind) {
		return instantSample{}, errors.New("hourly fields are outside their physical range")
	}
	return instantSample{
		temperature:      temperature,
		dewPoint:         dewPoint,
		cloud:            cloud * 100,
		wind:             wind,
		relativeHumidity: humidity,
	}, nil
}

func addHour(state *cellState, frame Frame, sourceIndex int, previous, current instantSample, weight, startFraction, endFraction float64) error {
	rain := float64(frame.RainM[sourceIndex]) * 1000
	solar := float64(frame.SolarJM2[sourceIndex]) / 1_000_000
	startTemperature := interpolate(previous.temperature, current.temperature, startFraction)
	endTemperature := interpolate(previous.temperature, current.temperature, endFraction)
	state.temperature += intervalMean(previous.temperature, current.temperature, startFraction, endFraction) * weight
	state.minimumTemperature = math.Min(state.minimumTemperature, math.Min(startTemperature, endTemperature))
	state.maximumTemperature = math.Max(state.maximumTemperature, math.Max(startTemperature, endTemperature))
	state.rain += rain * weight
	if rain >= wetHourlyThresholdMM {
		state.wet += weight
	}
	state.solar += solar * weight
	state.cloud += intervalMean(previous.cloud, current.cloud, startFraction, endFraction) * weight
	state.wind += intervalMean(previous.wind, current.wind, startFraction, endFraction) * weight
	state.dewPoint += intervalMean(previous.dewPoint, current.dewPoint, startFraction, endFraction) * weight
	state.relativeHumidity += intervalMean(previous.relativeHumidity, current.relativeHumidity, startFraction, endFraction) * weight
	state.weight += weight
	return nil
}

func interpolate(previous, current, fraction float64) float64 {
	return previous + (current-previous)*fraction
}

func intervalMean(previous, current, startFraction, endFraction float64) float64 {
	return interpolate(previous, current, (startFraction+endFraction)/2)
}

func flushCell(index int, cells []Cell, state *cellState, daysByCell []int, transaction MonthTransaction, stats *AggregationStats) error {
	if math.Abs(state.weight-state.windowHours) > completeWeightEpsilon {
		return fmt.Errorf("ERA5 cell %d day %s has %.9g weighted hours; want %.9g", index, state.date.Format(time.DateOnly), state.weight, state.windowHours)
	}
	metrics := weather.DailyMetrics{
		ComfortableTempC:        state.temperature / state.weight,
		MinTempC:                state.minimumTemperature,
		MaxTempC:                state.maximumTemperature,
		RainMM:                  state.rain,
		WetTimeFraction:         weather.OptionalFloat{Value: state.wet / state.windowHours, Available: true},
		SolarEnergyMJm2:         weather.OptionalFloat{Value: state.solar, Available: true},
		CloudCoverPercent:       weather.OptionalFloat{Value: state.cloud / state.weight, Available: true},
		WindSpeedKPH:            state.wind / state.weight,
		DewPointC:               weather.OptionalFloat{Value: state.dewPoint / state.weight, Available: true},
		RelativeHumidityPercent: weather.OptionalFloat{Value: state.relativeHumidity / state.weight, Available: true},
		NativeResolutionKM:      NativeDetailKM,
		Valid:                   true,
	}
	if err := metrics.Validate(); err != nil {
		return fmt.Errorf("ERA5 cell %d day %s: %w", index, state.date.Format(time.DateOnly), err)
	}
	cell := cells[index]
	if err := transaction.Add(DailyMetrics{CellIndex: index, Date: state.date, Latitude: cell.Latitude, Longitude: cell.Longitude, Metrics: metrics}); err != nil {
		return fmt.Errorf("add ERA5 cell %d day %s: %w", index, state.date.Format(time.DateOnly), err)
	}
	daysByCell[index]++
	stats.Days++
	resetState(state)
	return nil
}

func resetState(state *cellState) {
	*state = cellState{minimumTemperature: math.Inf(1), maximumTemperature: math.Inf(-1)}
}

func relativeHumidity(temperature, dewPoint float64) float64 {
	const a, b = 17.625, 243.04
	return math.Max(0, math.Min(100, 100*math.Exp(a*dewPoint/(b+dewPoint)-a*temperature/(b+temperature))))
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func later(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func earlier(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}
