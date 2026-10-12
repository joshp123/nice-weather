package climate

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

func TestClimateDistributionPreservesDefaultWindowAndPlaceSemantics(t *testing.T) {
	window := DefaultReferenceWindow()
	malaga := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	amsterdam := completeMonth(window, time.November, 52.37, 4.90, 7, 4, .65, 2, 95, 25)
	malagaResult, err := aggregateMonth(malaga, window)
	if err != nil {
		t.Fatal(err)
	}
	amsterdamResult, err := aggregateMonth(amsterdam, window)
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

func TestClimateHotHumidIsAwful(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	days := completeMonth(window, time.November, 1.35, 103.82, 30, 0, 0, 17, 15, 12)
	for index := range days {
		days[index].Metrics.DewPointC = weather.OptionalFloat{Value: 24, Available: true}
	}
	result, err := aggregateMonth(days, window)
	if err != nil {
		t.Fatal(err)
	}
	record, err := result.DeliveryRecord()
	if err != nil {
		t.Fatal(err)
	}
	if record.Nice.AwfulPermille() != 1000 || record.Nice.GoodOrBetterPercent() != 0 {
		t.Fatalf("30°C with 24°C dew point = %+v; want every day Awful", record.Nice)
	}
}

func TestReferenceWindowCountsLeapDaysAndRejectsIncompleteInput(t *testing.T) {
	defaultFebruary, err := DefaultReferenceWindow().monthDates(time.February)
	if err != nil || len(defaultFebruary) != 565 {
		t.Fatalf("default February dates=%d err=%v; want 565", len(defaultFebruary), err)
	}
	window := ReferenceWindow{FirstYear: 2023, LastYear: 2024}
	complete := completeMonth(window, time.February, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	result, err := aggregateMonth(complete, window)
	if err != nil || result.Days != 57 || result.ExpectedDays != 57 {
		t.Fatalf("complete leap window=%+v err=%v", result, err)
	}
	missingLeapDay := make([]DailyObservation, 0, len(complete)-1)
	for _, observation := range complete {
		if observation.Date.Format(time.DateOnly) != "2024-02-29" {
			missingLeapDay = append(missingLeapDay, observation)
		}
	}
	if _, err := aggregateMonth(missingLeapDay, window); err == nil || !strings.Contains(err.Error(), "have 56 of 57 days; 1 missing, first 2024-02-29") {
		t.Fatalf("missing leap day error = %v", err)
	}
	for _, invalid := range []ReferenceWindow{{}, {FirstYear: 2025, LastYear: 2024}, {FirstYear: -1, LastYear: 2024}, {FirstYear: 2024, LastYear: 9999}} {
		if _, err := aggregateMonth(complete, invalid); err == nil {
			t.Fatalf("invalid window %+v was accepted", invalid)
		}
	}
}

func TestClimateDistributionMeasuresOnlyCalendarContiguousPoorRuns(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 52.37, 4.90, 24, 0, 0, 15, 20, 12)
	for index := range days {
		if days[index].Date.Format(time.DateOnly) == "2024-11-30" || days[index].Date.Format(time.DateOnly) == "2025-11-01" || days[index].Date.Format(time.DateOnly) >= "2025-11-03" && days[index].Date.Format(time.DateOnly) <= "2025-11-05" {
			days[index] = day(days[index].Date.Format(time.DateOnly), 52.37, 4.90, 7, 5, .8, 2, 95, 30)
		}
	}
	// Input order is irrelevant. The end of one November and the next
	// November's start are separated by eleven months, so they cannot join.
	sortByDateDescending(days)
	result, err := aggregateMonth(days, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nice.LongestPoorRun != 3 || result.Nice.SevenDayWindows != 480 || result.Nice.PoorWeekWindows != 3 {
		t.Fatalf("poor persistence = %+v; want one three-day run in 3 of 480 complete seven-day windows", result.Nice)
	}
}

func TestClimateDistributionRejectsDuplicateOutOfRangeAndMixedCellRows(t *testing.T) {
	window := DefaultReferenceWindow()
	tests := []struct {
		name   string
		change func([]DailyObservation) []DailyObservation
	}{
		{"duplicate", func(days []DailyObservation) []DailyObservation { return append(days, days[0]) }},
		{"year", func(days []DailyObservation) []DailyObservation {
			days[0].Date, _ = time.Parse(time.DateOnly, "2005-11-01")
			return days
		}},
		{"month", func(days []DailyObservation) []DailyObservation {
			days[0].Date, _ = time.Parse(time.DateOnly, "2006-12-01")
			return days
		}},
		{"cell", func(days []DailyObservation) []DailyObservation { days[0].Longitude += .1; return days }},
		{"invalid metrics", func(days []DailyObservation) []DailyObservation { days[0].Metrics.RainMM = -1; return days }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
			if _, err := aggregateMonth(test.change(days), window); err == nil {
				t.Fatal("invalid distribution was accepted")
			}
		})
	}
}

func TestClimateDistributionMeanMedianAndRatingsUseDailyScores(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	poorStart := len(days) / 2
	for index := poorStart; index < len(days); index++ {
		days[index] = day(days[index].Date.Format(time.DateOnly), 36.72, -4.42, 10, 3, .5, 5, 80, 25)
	}
	niceScore := score(t, days[0], weather.ProfileNice)
	poorScore := score(t, days[poorStart], weather.ProfileNice)
	result, err := aggregateMonth(days, window)
	if err != nil {
		t.Fatal(err)
	}
	wantMean := float64(niceScore+poorScore) / 2
	wantMedian := int(math.Round(float64(niceScore+poorScore) / 2))
	if result.Nice.MeanScore != wantMean || result.Nice.MedianScore != wantMedian || result.Nice.Ratings.total() != result.Days {
		t.Fatalf("distribution = %+v; want mean %.1f median %d", result.Nice, wantMean, wantMedian)
	}
}

func TestClimateDistributionSharesPhysicalLensesAndKeepsProfileCalmMonotonic(t *testing.T) {
	window := DefaultReferenceWindow()
	days := completeMonth(window, time.November, 36.72, -4.42, 24, 0, 0, 15, 20, 25)
	for index := len(days) / 2; index < len(days); index++ {
		// Half of the real days become wet, overcast and oppressively humid.
		days[index] = day(days[index].Date.Format(time.DateOnly), 36.72, -4.42, 24, 4, .65, 2, 95, 25)
	}
	result, err := aggregateMonth(days, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.ComfortGoodDays != result.Days/2 || result.DryGoodDays != result.Days/2 || result.BrightGoodDays != result.Days/2 || result.Nice.CalmGoodDays != result.Days || result.Cycling.CalmGoodDays != 0 {
		t.Fatalf("distribution = %+v", result)
	}
	record, err := result.DeliveryRecord()
	if err != nil {
		t.Fatal(err)
	}
	if record.Cycling.CalmGoodPercent > record.Nice.CalmGoodPercent || record.ComfortGoodPercent != 50 || record.DryGoodPercent != 50 || record.BrightGoodPercent != 50 {
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

func TestAllocatePermilleAllocatesNothing(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		shares, err := allocatePermille([6]int{17, 23, 31, 41, 53, 67})
		if err != nil || shares == ([6]uint16{}) {
			panic("allocation fixture failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("allocatePermille allocations = %.2f; want 0", allocations)
	}
}

func completeMonth(window ReferenceWindow, month time.Month, lat, lon, temperature, rain, wet, solar, cloud, wind float64) []DailyObservation {
	dates, err := window.monthDates(month)
	if err != nil {
		panic(err)
	}
	result := make([]DailyObservation, 0, len(dates))
	for _, date := range dates {
		result = append(result, day(date, lat, lon, temperature, rain, wet, solar, cloud, wind))
	}
	return result
}

func sortByDateDescending(days []DailyObservation) {
	for left, right := 0, len(days)-1; left < right; left, right = left+1, right-1 {
		days[left], days[right] = days[right], days[left]
	}
}

func score(t *testing.T, observation DailyObservation, profile weather.Profile) int {
	t.Helper()
	return weather.Score(observation.Metrics, profile).Score
}

func day(date string, lat, lon, temperature, rain, wet, solar, cloud, wind float64) DailyObservation {
	dewPoint := temperature - 10
	if wet > .5 {
		dewPoint = temperature - .2
	}
	parsedDate, _ := time.Parse(time.DateOnly, date)
	return DailyObservation{Date: parsedDate, Latitude: lat, Longitude: lon, Metrics: weather.DailyMetrics{
		ComfortableTempC: temperature, MinTempC: temperature - 4, MaxTempC: temperature + 4,
		RainMM: rain, WetTimeFraction: weather.OptionalFloat{Value: wet, Available: true},
		SolarEnergyMJm2:   weather.OptionalFloat{Value: solar, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: cloud, Available: true},
		WindSpeedKPH:      wind, DewPointC: weather.OptionalFloat{Value: dewPoint, Available: true},
		NativeResolutionKM: 28, Valid: true,
	}}
}

func aggregateMonth(days []DailyObservation, window ReferenceWindow) (CellDistribution, error) {
	if len(days) == 0 {
		return AggregateObservations(nil, nil)
	}
	dates, err := window.monthDates(days[0].Date.Month())
	if err != nil {
		return CellDistribution{}, err
	}
	expected := make([]time.Time, len(dates))
	for index, date := range dates {
		expected[index], _ = time.Parse(time.DateOnly, date)
	}
	return AggregateObservations(days, expected)
}

func (window ReferenceWindow) validate() error {
	if window.FirstYear < 1 || window.LastYear > 9998 || window.FirstYear > window.LastYear {
		return fmt.Errorf("invalid climate reference window %d–%d", window.FirstYear, window.LastYear)
	}
	return nil
}

func (window ReferenceWindow) monthDates(month time.Month) ([]string, error) {
	if err := window.validate(); err != nil {
		return nil, err
	}
	if month < time.January || month > time.December {
		return nil, fmt.Errorf("invalid climate month %d", month)
	}
	dates := make([]string, 0, 31*(window.LastYear-window.FirstYear+1))
	for year := window.FirstYear; year <= window.LastYear; year++ {
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for day := 1; day <= lastDay; day++ {
			dates = append(dates, time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
		}
	}
	return dates, nil
}
