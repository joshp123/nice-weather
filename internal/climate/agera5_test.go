package climate

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

func TestAgERA5DistributionPreservesDefaultWindowAndPlaceSemantics(t *testing.T) {
	window := DefaultReferenceWindow()
	malaga := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	amsterdam := completeMonth(window, time.November, 52.37, 4.90, 7, 4, .65, 2, 95, 25)
	malagaResult, err := Aggregate(malaga, window)
	if err != nil {
		t.Fatal(err)
	}
	amsterdamResult, err := Aggregate(amsterdam, window)
	if err != nil {
		t.Fatal(err)
	}
	if malagaResult.Nice.GoodOrBetterFraction() != 1 || malagaResult.Nice.AwfulFraction() != 0 || amsterdamResult.Nice.AwfulFraction() != 1 || amsterdamResult.Nice.GoodOrBetterFraction() != 0 {
		t.Fatalf("Málaga=%+v Amsterdam=%+v", malagaResult.Nice, amsterdamResult.Nice)
	}
	if malagaResult.Days != 600 || malagaResult.ExpectedDays != 600 || malagaResult.Coverage != 1 || malagaResult.Nice.Ratings.total() != 600 || malagaResult.Cycling.Ratings.total() != 600 {
		t.Fatalf("complete November distribution = %+v", malagaResult)
	}
}

func TestAgERA5HotHumidIsNotAutomaticallyAwful(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	days := completeMonth(window, time.November, 1.35, 103.82, 30, 0, 0, 17, 15, 12)
	for index := range days {
		days[index].DewPointC = 24
	}
	result, err := Aggregate(days, window)
	if err != nil {
		t.Fatal(err)
	}
	record, err := result.DeliveryRecord()
	if err != nil {
		t.Fatal(err)
	}
	if record.Nice.AwfulPermille() != 0 || record.Nice.GoodOrBetterPercent() == 0 {
		t.Fatalf("30°C with 24°C dew point = %+v; should remain potentially nice, not automatically Awful", record.Nice)
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

func TestReferenceWindowCountsLeapDaysAndRejectsIncompleteInput(t *testing.T) {
	defaultFebruary, err := DefaultReferenceWindow().monthDates(time.February)
	if err != nil || len(defaultFebruary) != 565 {
		t.Fatalf("default February dates=%d err=%v; want 565", len(defaultFebruary), err)
	}
	window := ReferenceWindow{FirstYear: 2023, LastYear: 2024}
	complete := completeMonth(window, time.February, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	result, err := Aggregate(complete, window)
	if err != nil || result.Days != 57 || result.ExpectedDays != 57 {
		t.Fatalf("complete leap window=%+v err=%v", result, err)
	}
	missingLeapDay := make([]AgERA5Day, 0, len(complete)-1)
	for _, observation := range complete {
		if observation.Date != "2024-02-29" {
			missingLeapDay = append(missingLeapDay, observation)
		}
	}
	if _, err := Aggregate(missingLeapDay, window); err == nil || !strings.Contains(err.Error(), "have 56 of 57 days; 1 missing, first 2024-02-29") {
		t.Fatalf("missing leap day error = %v", err)
	}
	for _, invalid := range []ReferenceWindow{{}, {FirstYear: 2025, LastYear: 2024}, {FirstYear: -1, LastYear: 2024}, {FirstYear: 2024, LastYear: 9999}} {
		if _, err := Aggregate(complete, invalid); err == nil {
			t.Fatalf("invalid window %+v was accepted", invalid)
		}
	}
}

func TestAgERA5DistributionMeasuresOnlyCalendarContiguousPoorRuns(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 52.37, 4.90, 24, 0, 0, 15, 20, 12)
	for index := range days {
		if days[index].Date == "2024-11-30" || days[index].Date == "2025-11-01" || days[index].Date >= "2025-11-03" && days[index].Date <= "2025-11-05" {
			days[index] = day(days[index].Date, 52.37, 4.90, 7, 5, .8, 2, 95, 30)
		}
	}
	// Input order is irrelevant. The end of one November and the next
	// November's start are separated by eleven months, so they cannot join.
	sortByDateDescending(days)
	result, err := Aggregate(days, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nice.LongestPoorRun != 3 || result.Nice.SevenDayWindows != 480 || result.Nice.PoorWeekWindows != 3 || result.Nice.PoorWeekFraction() != float64(3)/480 {
		t.Fatalf("poor persistence = %+v; want one three-day run in 3 of 480 complete seven-day windows", result.Nice)
	}
}

func TestAgERA5DistributionRejectsDuplicateOutOfRangeAndMixedCellRows(t *testing.T) {
	window := DefaultReferenceWindow()
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
			days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
			if _, err := Aggregate(test.change(days), window); err == nil {
				t.Fatal("invalid distribution was accepted")
			}
		})
	}
}

func TestAgERA5DistributionMeanMedianAndRatingsUseDailyScores(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	poorStart := len(days) / 2
	for index := poorStart; index < len(days); index++ {
		days[index] = day(days[index].Date, 36.72, -4.42, 10, 3, .5, 5, 80, 25)
	}
	niceScore := score(t, days[0], weather.ProfileNice)
	poorScore := score(t, days[poorStart], weather.ProfileNice)
	result, err := Aggregate(days, window)
	if err != nil {
		t.Fatal(err)
	}
	wantMean := float64(niceScore+poorScore) / 2
	wantMedian := int(math.Round(float64(niceScore+poorScore) / 2))
	if result.Nice.MeanScore != wantMean || result.Nice.MedianScore != wantMedian || result.Nice.Ratings.total() != result.Days {
		t.Fatalf("distribution = %+v; want mean %.1f median %d", result.Nice, wantMean, wantMedian)
	}
}

func TestAgERA5DistributionSharesPhysicalLensesAndKeepsProfileCalmMonotonic(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 25)
	for index := len(days) / 2; index < len(days); index++ {
		// Comfort stays good while half of the real days become wet and dark.
		days[index] = day(days[index].Date, 36.72, -4.42, 24, 4, .65, 2, 95, 25)
	}
	result, err := Aggregate(days, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.ComfortGoodFraction() != 1 || result.DryGoodFraction() != .5 || result.BrightGoodFraction() != .5 || result.Nice.CalmGoodFraction() != 1 || result.Cycling.CalmGoodFraction() != 0 {
		t.Fatalf("distribution = %+v", result)
	}
	record, err := result.DeliveryRecord()
	if err != nil {
		t.Fatal(err)
	}
	if record.Cycling.CalmGoodPercent > record.Nice.CalmGoodPercent || record.ComfortGoodPercent != 100 || record.DryGoodPercent != 50 || record.BrightGoodPercent != 50 {
		t.Fatalf("delivery record = %+v", record)
	}
}

func TestClimateDeliveryUsesDeterministicLargestRemainderAndMapScalars(t *testing.T) {
	distribution := CellDistribution{
		Days: 6, ExpectedDays: 6, Coverage: 1,
		ComfortGoodDays: 4, DryGoodDays: 3, BrightGoodDays: 6,
		Nice: ProfileDistribution{
			MedianScore: 50, CalmGoodDays: 4, SevenDayWindows: 3, PoorWeekWindows: 1,
			Ratings: RatingCounts{Awful: 1, Poor: 1, Mixed: 1, Good: 1, VeryGood: 1, Excellent: 1},
		},
		Cycling: ProfileDistribution{
			MedianScore: 40, CalmGoodDays: 3, SevenDayWindows: 3, PoorWeekWindows: 2,
			Ratings: RatingCounts{Awful: 2, Poor: 1, Mixed: 1, Good: 1, VeryGood: 1},
		},
	}
	record, err := distribution.DeliveryRecord()
	if err != nil {
		t.Fatal(err)
	}
	wantNice := [6]uint16{167, 167, 167, 167, 166, 166}
	gotNice := [6]uint16{record.Nice.RatingShares.Awful, record.Nice.RatingShares.Poor, record.Nice.RatingShares.Mixed, record.Nice.RatingShares.Good, record.Nice.RatingShares.VeryGood, record.Nice.RatingShares.Excellent}
	if gotNice != wantNice {
		t.Fatalf("largest remainder = %v; want %v", gotNice, wantNice)
	}
	if record.ComfortGoodPercent != 67 || record.Nice.CalmGoodPercent != 67 || record.Nice.PoorWeekPermille != 333 || record.Cycling.PoorWeekPermille != 667 {
		t.Fatalf("rounded delivery record = %+v", record)
	}
	if record.Nice.GoodOrBetterPermille() != 499 || record.Nice.GoodOrBetterPercent() != 50 || record.Nice.AwfulPercent() != 17 {
		t.Fatalf("derived map scalars = %+v", record.Nice)
	}
	for _, counts := range [][6]int{{1, 1, 1, 1, 1, 1}, {1, 1, 1, 0, 0, 0}, {0, 0, 0, 7, 11, 13}} {
		shares, err := allocatePermille(counts)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, share := range shares {
			total += int(share)
		}
		if total != 1000 {
			t.Fatalf("%v allocated %v, total %d", counts, shares, total)
		}
	}
	if percent, err := roundedPercent(87, 600); err != nil || percent != 15 {
		t.Fatalf("87/600 percent = %d, %v; want exact half-up 15", percent, err)
	}
	if permille, err := roundedPermille(201, 400); err != nil || permille != 503 {
		t.Fatalf("201/400 permille = %d, %v; want exact half-up 503", permille, err)
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
	return AgERA5Day{
		Date: date, Latitude: lat, Longitude: lon,
		DaytimeMeanTemperatureC: temperature, MinimumTemperatureC: temperature - 4, MaximumTemperatureC: temperature + 4,
		PrecipitationMM: rain, PrecipitationDurationFrac: wet, SolarEnergyMJm2: solar,
		CloudCoverPercent: cloud, WindSpeedKPH: wind, DewPointC: dewPoint,
	}
}

func completeMonth(window ReferenceWindow, month time.Month, lat, lon, temperature, rain, wet, solar, cloud, wind float64) []AgERA5Day {
	dates, err := window.monthDates(month)
	if err != nil {
		panic(err)
	}
	result := make([]AgERA5Day, 0, len(dates))
	for _, date := range dates {
		result = append(result, day(date, lat, lon, temperature, rain, wet, solar, cloud, wind))
	}
	return result
}

func sortByDateDescending(days []AgERA5Day) {
	for left, right := 0, len(days)-1; left < right; left, right = left+1, right-1 {
		days[left], days[right] = days[right], days[left]
	}
}

func score(t *testing.T, observation AgERA5Day, profile weather.Profile) int {
	t.Helper()
	metrics, err := observation.Metrics()
	if err != nil {
		t.Fatal(err)
	}
	return weather.Score(metrics, profile).Score
}
