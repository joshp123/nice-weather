package climate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"nice-weather-finder/internal/weather"
)

// AgERA5Day is the normalized boundary from the official AgERA5 v2 CDS time-
// series product. Field names encode units so an extraction step cannot
// silently pass Kelvin, joules or metres into the weather domain.
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

// DecodeAgERA5 reads a bounded newline-delimited interchange for single-cell
// scoring conformance and source research. It is not a regional extraction or
// grouping pipeline. Authenticated CDS retrieval and extraction remain outside
// this seam.
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
		RainMM:            day.PrecipitationMM,
		WetTimeFraction:   weather.OptionalFloat{Value: day.PrecipitationDurationFrac, Available: true},
		SolarEnergyMJm2:   weather.OptionalFloat{Value: day.SolarEnergyMJm2, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: day.CloudCoverPercent, Available: true},
		WindSpeedKPH:      day.WindSpeedKPH,
		DewPointC:         weather.OptionalFloat{Value: day.DewPointC, Available: true},
		// Daytime temperature and 24-hour dew point are not synchronous, so
		// deriving and publishing relative humidity from them would imply a
		// measurement AgERA5 does not provide. Dew point remains canonical hot-
		// moisture evidence; cold dampness uses rain duration, cloud and wind.
		RelativeHumidityPercent: weather.OptionalFloat{},
		// AgERA5 is delivered on a 0.1° output grid, but the atmospheric source
		// information is ERA5 at roughly 30 km. Delivery metadata must describe
		// both rather than calling every field native 11 km weather.
		NativeResolutionKM: 30, Valid: true,
	}
	if err := metrics.Validate(); err != nil {
		return weather.DailyMetrics{}, err
	}
	return metrics, nil
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
