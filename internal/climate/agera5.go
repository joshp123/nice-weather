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

// Distribution is one complete 2006–2025 cell/month after every observed day
// has been scored. Overall and component fractions are chances of a real day
// receiving at least a Good canonical judgement.
type Distribution struct {
	Days                int
	ExpectedDays        int
	Coverage            float64
	MeanScore           float64
	MedianScore         int
	NiceFraction        float64
	AwfulFraction       float64
	ComfortGoodFraction float64
	DryGoodFraction     float64
	BrightGoodFraction  float64
	CalmGoodFraction    float64
	LongestPoorRun      int
	SevenDayWindows     int
	PoorWeekWindows     int
	PoorWeekFraction    float64
	Ratings             map[string]int
}

const (
	referenceFirstYear = 2006
	referenceLastYear  = 2025
)

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

func Aggregate(days []AgERA5Day, profile weather.Profile) (Distribution, error) {
	if len(days) == 0 {
		return Distribution{}, errors.New("at least one AgERA5 day is required")
	}
	firstDate, err := time.Parse("2006-01-02", days[0].Date)
	if err != nil {
		return Distribution{}, fmt.Errorf("invalid first AgERA5 date %q", days[0].Date)
	}
	month := firstDate.Month()
	expectedDates := referenceMonthDates(month)
	type observation struct {
		date  time.Time
		score int
	}
	scores := make([]int, len(days))
	observations := make([]observation, len(days))
	seenDates := make(map[string]struct{}, len(days))
	ratings := map[string]int{"Awful": 0, "Poor": 0, "Mixed": 0, "Good": 0, "Very good": 0, "Excellent": 0}
	sum, nice, awful := 0, 0, 0
	comfortGood, dryGood, brightGood, calmGood := 0, 0, 0, 0
	for index, day := range days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || date.Year() < referenceFirstYear || date.Year() > referenceLastYear || date.Month() != month || math.Abs(day.Latitude-days[0].Latitude) > 1e-9 || math.Abs(day.Longitude-days[0].Longitude) > 1e-9 {
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
		observations[index] = observation{date: date, score: result.Score}
		ratings[result.Rating]++
		if isGoodRating(result.Rating) {
			nice++
		}
		if result.Rating == "Awful" {
			awful++
		}
		if isGoodRating(weather.Rating(result.Components.Thermal)) {
			comfortGood++
		}
		if isGoodRating(weather.Rating(result.Components.Dryness)) {
			dryGood++
		}
		if isGoodRating(weather.Rating(result.Components.Sun)) {
			brightGood++
		}
		if isGoodRating(weather.Rating(result.Components.Wind)) {
			calmGood++
		}
	}
	if len(seenDates) != len(expectedDates) {
		missing := make([]string, 0, len(expectedDates)-len(seenDates))
		for _, expected := range expectedDates {
			if _, ok := seenDates[expected]; !ok {
				missing = append(missing, expected)
			}
		}
		firstMissing := "none"
		if len(missing) > 0 {
			firstMissing = missing[0]
		}
		return Distribution{}, fmt.Errorf("incomplete AgERA5 %s distribution: have %d of %d days; %d missing, first %s", month, len(seenDates), len(expectedDates), len(missing), firstMissing)
	}
	sort.Ints(scores)
	median := scores[len(scores)/2]
	if len(scores)%2 == 0 {
		median = int(math.Round(float64(scores[len(scores)/2-1]+scores[len(scores)/2]) / 2))
	}
	sort.Slice(observations, func(left, right int) bool { return observations[left].date.Before(observations[right].date) })
	poorWeeks, sevenDayWindows := 0, 0
	for start := 0; start+7 <= len(observations); start++ {
		window := observations[start : start+7]
		complete := true
		for index := 1; index < len(window); index++ {
			if !window[index].date.Equal(window[index-1].date.AddDate(0, 0, 1)) {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		sevenDayWindows++
		poorRun, hasPoorSpell := 0, false
		for _, item := range window {
			if rating := weather.Rating(item.score); rating == "Poor" || rating == "Awful" {
				poorRun++
				if poorRun >= 3 {
					hasPoorSpell = true
				}
			} else {
				poorRun = 0
			}
		}
		if hasPoorSpell {
			poorWeeks++
		}
	}
	longestPoorRun, poorRun := 0, 0
	for index, item := range observations {
		if index > 0 && !item.date.Equal(observations[index-1].date.AddDate(0, 0, 1)) {
			poorRun = 0
		}
		rating := weather.Rating(item.score)
		if rating != "Poor" && rating != "Awful" {
			poorRun = 0
			continue
		}
		poorRun++
		if poorRun > longestPoorRun {
			longestPoorRun = poorRun
		}
	}
	poorWeekFraction := 0.0
	if sevenDayWindows > 0 {
		poorWeekFraction = float64(poorWeeks) / float64(sevenDayWindows)
	}
	return Distribution{
		Days:                len(days),
		ExpectedDays:        len(expectedDates),
		Coverage:            1,
		MeanScore:           float64(sum) / float64(len(days)),
		MedianScore:         median,
		NiceFraction:        float64(nice) / float64(len(days)),
		AwfulFraction:       float64(awful) / float64(len(days)),
		ComfortGoodFraction: float64(comfortGood) / float64(len(days)),
		DryGoodFraction:     float64(dryGood) / float64(len(days)),
		BrightGoodFraction:  float64(brightGood) / float64(len(days)),
		CalmGoodFraction:    float64(calmGood) / float64(len(days)),
		LongestPoorRun:      longestPoorRun,
		SevenDayWindows:     sevenDayWindows,
		PoorWeekWindows:     poorWeeks,
		PoorWeekFraction:    poorWeekFraction,
		Ratings:             ratings,
	}, nil
}

func isGoodRating(rating string) bool {
	return rating == "Good" || rating == "Very good" || rating == "Excellent"
}

func referenceMonthDates(month time.Month) []string {
	dates := make([]string, 0, 31*(referenceLastYear-referenceFirstYear+1))
	for year := referenceFirstYear; year <= referenceLastYear; year++ {
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for day := 1; day <= lastDay; day++ {
			dates = append(dates, time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
		}
	}
	return dates
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
