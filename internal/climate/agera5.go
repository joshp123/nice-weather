package climate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/weather"
)

// AgERA5Day is the normalized boundary from the official AgERA5 v2 ARCO
// store. Field names encode units so an extraction script cannot silently pass
// Kelvin, joules or metres into the weather domain.
type AgERA5Day struct {
	Date                      string  `json:"date"`
	Latitude                  float64 `json:"latitude"`
	Longitude                 float64 `json:"longitude"`
	DaytimeMeanTemperatureC   float64 `json:"daytimeMeanTemperatureC"`
	MinimumTemperatureC       float64 `json:"minimumTemperatureC"`
	MaximumTemperatureC       float64 `json:"maximumTemperatureC"`
	PrecipitationMM           float64 `json:"precipitationMm"`
	PrecipitationDurationFrac float64 `json:"precipitationDurationFraction"`
	SolarEnergyMJm2           float64 `json:"solarEnergyMJm2"`
	CloudCoverPercent         float64 `json:"cloudCoverPercent"`
	WindSpeedKPH              float64 `json:"windSpeedKmh"`
	DewPointC                 float64 `json:"dewPointC"`
}

// Distribution is one cell and calendar month after every observed day has
// been scored. It answers frequency questions that a synthetic mean day cannot.
type Distribution struct {
	Days          int
	ExpectedDays  int
	Coverage      float64
	MeanScore     float64
	MedianScore   int
	NiceFraction  float64
	AwfulFraction float64
	Ratings       map[string]int
}

// DecodeAgERA5 reads a bounded newline-delimited interchange emitted by the
// authenticated ARCO extraction step. The official Zarr access itself remains
// outside this pure product boundary.
func DecodeAgERA5(reader io.Reader, maximumRows int) ([]AgERA5Day, error) {
	if maximumRows <= 0 {
		return nil, errors.New("positive AgERA5 row limit is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	rows := make([]AgERA5Day, 0)
	for scanner.Scan() {
		if len(rows) == maximumRows {
			return nil, fmt.Errorf("AgERA5 input exceeds %d rows", maximumRows)
		}
		row, err := decodeAgERA5Day(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode AgERA5 row %d: %w", len(rows)+1, err)
		}
		if _, err := row.Metrics(); err != nil {
			return nil, fmt.Errorf("AgERA5 row %d: %w", len(rows)+1, err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read AgERA5 rows: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("AgERA5 input is empty")
	}
	return rows, nil
}

func decodeAgERA5Day(input []byte) (AgERA5Day, error) {
	required := map[string]struct{}{
		"date": {}, "latitude": {}, "longitude": {}, "daytimeMeanTemperatureC": {},
		"minimumTemperatureC": {}, "maximumTemperatureC": {}, "precipitationMm": {},
		"precipitationDurationFraction": {}, "solarEnergyMJm2": {}, "cloudCoverPercent": {},
		"windSpeedKmh": {}, "dewPointC": {},
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return AgERA5Day{}, errors.New("AgERA5 row must be a JSON object")
	}
	fields := make(map[string]json.RawMessage, len(required))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return AgERA5Day{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return AgERA5Day{}, errors.New("AgERA5 field name is not a string")
		}
		if _, ok := required[key]; !ok {
			return AgERA5Day{}, fmt.Errorf("unknown field %q", key)
		}
		if _, duplicate := fields[key]; duplicate {
			return AgERA5Day{}, fmt.Errorf("duplicate field %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return AgERA5Day{}, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return AgERA5Day{}, fmt.Errorf("field %q cannot be null", key)
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return AgERA5Day{}, err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return AgERA5Day{}, fmt.Errorf("trailing JSON value %v", token)
	}
	for key := range required {
		if _, ok := fields[key]; !ok {
			return AgERA5Day{}, fmt.Errorf("missing field %q", key)
		}
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return AgERA5Day{}, err
	}
	var row AgERA5Day
	if err := json.Unmarshal(normalized, &row); err != nil {
		return AgERA5Day{}, err
	}
	return row, nil
}

func (day AgERA5Day) Metrics() (weather.DailyMetrics, error) {
	if _, err := time.Parse("2006-01-02", day.Date); err != nil || !finite(day.Latitude, day.Longitude) || day.Latitude < -90 || day.Latitude > 90 || day.Longitude < -180 || day.Longitude > 180 || day.PrecipitationDurationFrac < 0 || day.PrecipitationDurationFrac > 1 {
		return weather.DailyMetrics{}, errors.New("invalid date, coordinate or precipitation duration")
	}
	metrics := weather.DailyMetrics{
		ComfortableTempC: day.DaytimeMeanTemperatureC,
		MinTempC:         day.MinimumTemperatureC, MaxTempC: day.MaximumTemperatureC,
		RainMM:                  day.PrecipitationMM,
		WetTimeFraction:         weather.OptionalFloat{Value: day.PrecipitationDurationFrac, Available: true},
		SolarEnergyMJm2:         weather.OptionalFloat{Value: day.SolarEnergyMJm2, Available: true},
		CloudCoverPercent:       weather.OptionalFloat{Value: day.CloudCoverPercent, Available: true},
		WindSpeedKPH:            day.WindSpeedKPH,
		DewPointC:               weather.OptionalFloat{Value: day.DewPointC, Available: true},
		RelativeHumidityPercent: weather.OptionalFloat{Value: relativeHumidity(day.DaytimeMeanTemperatureC, day.DewPointC), Available: true},
		NativeResolutionKM:      11, Valid: true,
	}
	if err := metrics.Validate(); err != nil {
		return weather.DailyMetrics{}, err
	}
	return metrics, nil
}

func relativeHumidity(temperatureC, dewPointC float64) float64 {
	ratio := math.Exp(17.625*dewPointC/(243.04+dewPointC)) / math.Exp(17.625*temperatureC/(243.04+temperatureC))
	return math.Max(0, math.Min(100, ratio*100))
}

func Aggregate(days []AgERA5Day, profile weather.Profile) (Distribution, error) {
	if len(days) == 0 {
		return Distribution{}, errors.New("at least one AgERA5 day is required")
	}
	firstDate, _ := time.Parse("2006-01-02", days[0].Date)
	scores := make([]int, len(days))
	seenDates := make(map[string]struct{}, len(days))
	ratings := map[string]int{"Awful": 0, "Poor": 0, "Mixed": 0, "Good": 0, "Very good": 0, "Excellent": 0}
	sum, nice, awful := 0, 0, 0
	for index, day := range days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || date.Year() < 2006 || date.Year() > 2025 || date.Month() != firstDate.Month() || math.Abs(day.Latitude-days[0].Latitude) > 1e-9 || math.Abs(day.Longitude-days[0].Longitude) > 1e-9 {
			return Distribution{}, errors.New("AgERA5 distribution requires one cell and month of year from 2006–2025")
		}
		if _, exists := seenDates[day.Date]; exists {
			return Distribution{}, fmt.Errorf("duplicate AgERA5 date %s", day.Date)
		}
		seenDates[day.Date] = struct{}{}
		metrics, err := day.Metrics()
		if err != nil {
			return Distribution{}, fmt.Errorf("day %s: %w", day.Date, err)
		}
		result := weather.Score(metrics, profile)
		if !result.Available {
			return Distribution{}, fmt.Errorf("day %s is not scoreable", day.Date)
		}
		scores[index], sum = result.Score, sum+result.Score
		ratings[result.Rating]++
		if result.Score >= 55 {
			nice++
		}
		if result.Score <= 24 {
			awful++
		}
	}
	sort.Ints(scores)
	median := scores[len(scores)/2]
	if len(scores)%2 == 0 {
		median = (scores[len(scores)/2-1] + scores[len(scores)/2]) / 2
	}
	expectedDays := 0
	for year := 2006; year <= 2025; year++ {
		expectedDays += time.Date(year, firstDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	return Distribution{Days: len(days), ExpectedDays: expectedDays, Coverage: float64(len(days)) / float64(expectedDays), MeanScore: float64(sum) / float64(len(days)), MedianScore: median, NiceFraction: float64(nice) / float64(len(days)), AwfulFraction: float64(awful) / float64(len(days)), Ratings: ratings}, nil
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
