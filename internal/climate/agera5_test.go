package climate

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

func TestAgERA5DistributionScoresDaysBeforeAggregating(t *testing.T) {
	malaga := completeMonth(time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	amsterdam := completeMonth(time.November, 52.37, 4.90, 7, 4, .65, 2, 95, 25)
	malagaResult, err := Aggregate(malaga, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	amsterdamResult, err := Aggregate(amsterdam, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	if malagaResult.NiceFraction != 1 || malagaResult.AwfulFraction != 0 || amsterdamResult.AwfulFraction != 1 || amsterdamResult.NiceFraction != 0 {
		t.Fatalf("Málaga=%+v Amsterdam=%+v", malagaResult, amsterdamResult)
	}
	if malagaResult.Days != 600 || malagaResult.ExpectedDays != 600 || malagaResult.Coverage != 1 || ratingTotal(malagaResult.Ratings) != 600 {
		t.Fatalf("complete November distribution = %+v", malagaResult)
	}
}

func TestDecodeAgERA5IsStrictAndBounded(t *testing.T) {
	input := `{"date":"2025-11-01","latitude":36.72,"longitude":-4.42,"daytimeMeanTemperatureC":24,"minimumTemperatureC":18,"maximumTemperatureC":27,"precipitationMm":0,"precipitationDurationFraction":0,"solarEnergyMJm2":15,"cloudCoverPercent":20,"windSpeedKmh":12,"dewPointC":12}`
	rows, err := DecodeAgERA5(strings.NewReader(input), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err := DecodeAgERA5(strings.NewReader(input+"\n"+input), 1); err == nil {
		t.Fatal("row limit was ignored")
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `"date":"2025-11-01"`, `"date":"bad"`, 1)), 1); err == nil {
		t.Fatal("invalid row was accepted")
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `}`, `,"unexpected":true}`, 1)), 1); err == nil {
		t.Fatal("unknown field was accepted")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		t.Fatal(err)
	}
	for field := range fields {
		missing := make(map[string]json.RawMessage, len(fields)-1)
		nullValue := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			if key != field {
				missing[key] = value
			}
			nullValue[key] = value
		}
		encoded, _ := json.Marshal(missing)
		if _, err := DecodeAgERA5(strings.NewReader(string(encoded)), 1); err == nil {
			t.Fatalf("missing required field %q was accepted", field)
		}
		nullValue[field] = json.RawMessage("null")
		encoded, _ = json.Marshal(nullValue)
		if _, err := DecodeAgERA5(strings.NewReader(string(encoded)), 1); err == nil {
			t.Fatalf("null required field %q was accepted", field)
		}
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `"precipitationMm":0`, `"precipitationMm":0,"precipitationMm":1`, 1)), 1); err == nil {
		t.Fatal("duplicate field was accepted")
	}
}

func TestAgERA5DistributionRequiresEveryDayInReferencePeriod(t *testing.T) {
	complete := completeMonth(time.February, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	result, err := Aggregate(complete, weather.ProfileNice)
	if err != nil || result.Days != 565 || result.ExpectedDays != 565 || result.Coverage != 1 {
		t.Fatalf("complete February distribution=%+v err=%v", result, err)
	}

	missingDay := append([]AgERA5Day(nil), complete...)
	missingDay = append(missingDay[:100], missingDay[101:]...)
	if _, err := Aggregate(missingDay, weather.ProfileNice); err == nil || !strings.Contains(err.Error(), "have 564 of 565 days; 1 missing") {
		t.Fatalf("missing day error = %v", err)
	}

	missingYear := make([]AgERA5Day, 0, len(complete)-28)
	for _, observation := range complete {
		if !strings.HasPrefix(observation.Date, "2011-") {
			missingYear = append(missingYear, observation)
		}
	}
	if _, err := Aggregate(missingYear, weather.ProfileNice); err == nil || !strings.Contains(err.Error(), "have 537 of 565 days; 28 missing") {
		t.Fatalf("incomplete year error = %v", err)
	}
}

func TestAgERA5DistributionMeasuresOnlyCalendarContiguousPoorRuns(t *testing.T) {
	days := completeMonth(time.November, 52.37, 4.90, 24, 0, 0, 15, 20, 12)
	for index := range days {
		if days[index].Date == "2024-11-30" || days[index].Date == "2025-11-01" || days[index].Date >= "2025-11-03" && days[index].Date <= "2025-11-05" {
			days[index] = day(days[index].Date, 52.37, 4.90, 7, 5, .8, 2, 95, 30)
		}
	}
	// Input order is irrelevant. A Poor day at the end of 2024 and another at
	// the start of 2025 must not become a two-day run across the eleven-month
	// calendar gap between observations.
	sortByDateDescending(days)
	result, err := Aggregate(days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	if result.LongestPoorRun != 3 || result.SevenDayWindows != 480 || result.PoorWeekWindows != 3 || result.PoorWeekFraction != float64(3)/480 {
		t.Fatalf("poor persistence = %+v; want one three-day run in 3 of 480 complete seven-day windows", result)
	}
}

func TestAgERA5DistributionRejectsDuplicateOutOfRangeAndMixedCellRows(t *testing.T) {
	tests := []struct {
		name   string
		change func([]AgERA5Day) []AgERA5Day
	}{
		{"duplicate", func(days []AgERA5Day) []AgERA5Day { return append(days, days[0]) }},
		{"year", func(days []AgERA5Day) []AgERA5Day { days[0].Date = "2005-11-01"; return days }},
		{"month", func(days []AgERA5Day) []AgERA5Day { days[0].Date = "2006-12-01"; return days }},
		{"cell", func(days []AgERA5Day) []AgERA5Day { days[0].Longitude += .1; return days }},
		{"invalid metrics", func(days []AgERA5Day) []AgERA5Day { days[0].PrecipitationMM = -1; return days }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			days := completeMonth(time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
			if _, err := Aggregate(test.change(days), weather.ProfileNice); err == nil {
				t.Fatal("invalid distribution was accepted")
			}
		})
	}
}

func TestAgERA5DistributionMeanMedianAndRatingsUseDailyScores(t *testing.T) {
	days := completeMonth(time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	poorStart := len(days) / 2
	for index := poorStart; index < len(days); index++ {
		days[index] = day(days[index].Date, 36.72, -4.42, 10, 3, .5, 5, 80, 25)
	}
	niceScore := score(t, days[0])
	poorScore := score(t, days[poorStart])
	result, err := Aggregate(days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	wantMean := float64(niceScore+poorScore) / 2
	wantMedian := int(math.Round(float64(niceScore+poorScore) / 2))
	if result.MeanScore != wantMean || result.MedianScore != wantMedian || ratingTotal(result.Ratings) != result.Days {
		t.Fatalf("distribution = %+v; want mean %.1f median %d", result, wantMean, wantMedian)
	}
}

func TestAgERA5DistributionLensesMeasureChanceOfGoodDailyComponent(t *testing.T) {
	days := completeMonth(time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	for index := len(days) / 2; index < len(days); index++ {
		// Keep comfort and calm good while making the day wet and dark. The
		// fractions must count canonical daily component judgements rather than
		// score a synthetic monthly mean.
		days[index] = day(days[index].Date, 36.72, -4.42, 24, 4, .65, 2, 95, 12)
	}
	result, err := Aggregate(days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	if result.ComfortGoodFraction != 1 || result.DryGoodFraction != .5 || result.BrightGoodFraction != .5 || result.CalmGoodFraction != 1 {
		t.Fatalf("component good-day fractions = comfort %.2f dry %.2f bright %.2f calm %.2f", result.ComfortGoodFraction, result.DryGoodFraction, result.BrightGoodFraction, result.CalmGoodFraction)
	}
}

func TestAgERA5DistributionCalmFractionUsesSelectedProfile(t *testing.T) {
	days := completeMonth(time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 25)
	nice, err := Aggregate(days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	cycling, err := Aggregate(days, weather.ProfileCycling)
	if err != nil {
		t.Fatal(err)
	}
	if nice.CalmGoodFraction != 1 || cycling.CalmGoodFraction != 0 {
		t.Fatalf("calm fraction Nice=%.2f Cycling=%.2f", nice.CalmGoodFraction, cycling.CalmGoodFraction)
	}
}

func TestAgERA5DoesNotInventSynchronousRelativeHumidity(t *testing.T) {
	metrics, err := day("2025-11-01", 36.72, -4.42, 30, 0, 0, 15, 20, 12).Metrics()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RelativeHumidityPercent.Available || !metrics.DewPointC.Available || metrics.NativeResolutionKM != 30 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func day(date string, lat, lon, temperature, rain, wet, solar, cloud, wind float64) AgERA5Day {
	dewPoint := temperature - 10
	if wet > .5 {
		dewPoint = temperature - .2
	}
	return AgERA5Day{Date: date, Latitude: lat, Longitude: lon, DaytimeMeanTemperatureC: temperature, MinimumTemperatureC: temperature - 4, MaximumTemperatureC: temperature + 4, PrecipitationMM: rain, PrecipitationDurationFrac: wet, SolarEnergyMJm2: solar, CloudCoverPercent: cloud, WindSpeedKPH: wind, DewPointC: dewPoint}
}

func completeMonth(month time.Month, lat, lon, temperature, rain, wet, solar, cloud, wind float64) []AgERA5Day {
	result := make([]AgERA5Day, 0, 31*(referenceLastYear-referenceFirstYear+1))
	for _, date := range referenceMonthDates(month) {
		result = append(result, day(date, lat, lon, temperature, rain, wet, solar, cloud, wind))
	}
	return result
}

func sortByDateDescending(days []AgERA5Day) {
	for left, right := 0, len(days)-1; left < right; left, right = left+1, right-1 {
		days[left], days[right] = days[right], days[left]
	}
}

func score(t *testing.T, observation AgERA5Day) int {
	t.Helper()
	metrics, err := observation.Metrics()
	if err != nil {
		t.Fatal(err)
	}
	return weather.Score(metrics, weather.ProfileNice).Score
}

func ratingTotal(ratings map[string]int) int {
	total := 0
	for _, count := range ratings {
		total += count
	}
	return total
}
