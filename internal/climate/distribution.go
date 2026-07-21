package climate

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

const (
	defaultReferenceFirstYear = 2006
	defaultReferenceLastYear  = 2025
)

// ReferenceWindow is the inclusive set of complete calendar years used for a
// historical distribution. Callers choose the window; the delivery codec does
// not encode or imply one.
type ReferenceWindow struct {
	FirstYear int
	LastYear  int
}

// DefaultReferenceWindow preserves the current 2006–2025 climate product until
// the data build selects and records a newer policy.
func DefaultReferenceWindow() ReferenceWindow {
	return ReferenceWindow{FirstYear: defaultReferenceFirstYear, LastYear: defaultReferenceLastYear}
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

// RatingCounts is a complete count of canonical daily judgements.
type RatingCounts struct {
	Awful     int
	Poor      int
	Mixed     int
	Good      int
	VeryGood  int
	Excellent int
}

func (counts RatingCounts) values() [6]int {
	return [6]int{counts.Awful, counts.Poor, counts.Mixed, counts.Good, counts.VeryGood, counts.Excellent}
}

func (counts RatingCounts) total() int {
	total := 0
	for _, count := range counts.values() {
		total += count
	}
	return total
}

func (counts *RatingCounts) add(rating string) error {
	switch rating {
	case "Awful":
		counts.Awful++
	case "Poor":
		counts.Poor++
	case "Mixed":
		counts.Mixed++
	case "Good":
		counts.Good++
	case "Very good":
		counts.VeryGood++
	case "Excellent":
		counts.Excellent++
	default:
		return fmt.Errorf("unknown canonical rating %q", rating)
	}
	return nil
}

// ProfileDistribution is the historical evidence that depends on a product
// profile. MeanScore and LongestPoorRun are retained for build QA only; they
// are deliberately absent from the delivery record.
type ProfileDistribution struct {
	MeanScore       float64
	MedianScore     int
	CalmGoodDays    int
	LongestPoorRun  int
	SevenDayWindows int
	PoorWeekWindows int
	Ratings         RatingCounts
}

// CellDistribution is one complete cell and month. Physical component chances
// are shared because Nice and Cycling use the same comfort, dry and bright
// component policy.
type CellDistribution struct {
	Days            int
	ExpectedDays    int
	Coverage        float64
	ComfortGoodDays int
	DryGoodDays     int
	BrightGoodDays  int
	Nice            ProfileDistribution
	Cycling         ProfileDistribution
}

type scoredObservation struct {
	date         time.Time
	niceScore    int
	cyclingScore int
}

type profileAccumulator struct {
	sum      int
	scores   []int
	calmGood int
	ratings  RatingCounts
}

func (accumulator *profileAccumulator) add(result weather.Result) error {
	if !result.Available {
		return errors.New("weather result is not scoreable")
	}
	accumulator.sum += result.Score
	accumulator.scores = append(accumulator.scores, result.Score)
	if isGoodRating(weather.Rating(result.Components.Wind)) {
		accumulator.calmGood++
	}
	return accumulator.ratings.add(result.Rating)
}

// Aggregate evaluates every historical day once for both canonical profiles.
// It rejects partial windows so missing observations cannot masquerade as
// climate probability.
func Aggregate(days []AgERA5Day, window ReferenceWindow) (CellDistribution, error) {
	if err := window.validate(); err != nil {
		return CellDistribution{}, err
	}
	if len(days) == 0 {
		return CellDistribution{}, errors.New("at least one AgERA5 day is required")
	}
	firstDate, err := time.Parse("2006-01-02", days[0].Date)
	if err != nil {
		return CellDistribution{}, fmt.Errorf("invalid first AgERA5 date %q", days[0].Date)
	}
	month := firstDate.Month()
	expectedDates, err := window.monthDates(month)
	if err != nil {
		return CellDistribution{}, err
	}

	observations := make([]scoredObservation, len(days))
	seenDates := make(map[string]struct{}, len(days))
	nice := profileAccumulator{scores: make([]int, 0, len(days))}
	cycling := profileAccumulator{scores: make([]int, 0, len(days))}
	comfortGood, dryGood, brightGood := 0, 0, 0
	for index, day := range days {
		date, parseErr := time.Parse("2006-01-02", day.Date)
		if parseErr != nil || date.Year() < window.FirstYear || date.Year() > window.LastYear || date.Month() != month || math.Abs(day.Latitude-days[0].Latitude) > 1e-9 || math.Abs(day.Longitude-days[0].Longitude) > 1e-9 {
			return CellDistribution{}, fmt.Errorf("AgERA5 distribution requires one cell and month from %d–%d", window.FirstYear, window.LastYear)
		}
		if _, exists := seenDates[day.Date]; exists {
			return CellDistribution{}, fmt.Errorf("duplicate AgERA5 date %s", day.Date)
		}
		seenDates[day.Date] = struct{}{}
		metrics, metricsErr := day.Metrics()
		if metricsErr != nil {
			return CellDistribution{}, fmt.Errorf("day %s: %w", day.Date, metricsErr)
		}
		niceResult := weather.Score(metrics, weather.ProfileNice)
		cyclingResult := weather.Score(metrics, weather.ProfileCycling)
		if !niceResult.Available || !cyclingResult.Available {
			return CellDistribution{}, fmt.Errorf("day %s is not scoreable", day.Date)
		}
		if niceResult.Components.Thermal != cyclingResult.Components.Thermal || niceResult.Components.Dryness != cyclingResult.Components.Dryness || niceResult.Components.Sun != cyclingResult.Components.Sun {
			return CellDistribution{}, fmt.Errorf("day %s has profile-dependent shared components", day.Date)
		}
		if err := nice.add(niceResult); err != nil {
			return CellDistribution{}, fmt.Errorf("day %s Nice: %w", day.Date, err)
		}
		if err := cycling.add(cyclingResult); err != nil {
			return CellDistribution{}, fmt.Errorf("day %s Cycling: %w", day.Date, err)
		}
		if isGoodRating(weather.Rating(niceResult.Components.Thermal)) {
			comfortGood++
		}
		if isGoodRating(weather.Rating(niceResult.Components.Dryness)) {
			dryGood++
		}
		if isGoodRating(weather.Rating(niceResult.Components.Sun)) {
			brightGood++
		}
		observations[index] = scoredObservation{date: date, niceScore: niceResult.Score, cyclingScore: cyclingResult.Score}
	}
	if len(seenDates) != len(expectedDates) {
		missing := make([]string, 0, len(expectedDates))
		for _, expected := range expectedDates {
			if _, ok := seenDates[expected]; !ok {
				missing = append(missing, expected)
			}
		}
		firstMissing := "none"
		if len(missing) > 0 {
			firstMissing = missing[0]
		}
		return CellDistribution{}, fmt.Errorf("incomplete AgERA5 %s distribution: have %d of %d days; %d missing, first %s", month, len(seenDates), len(expectedDates), len(missing), firstMissing)
	}

	sort.Slice(observations, func(left, right int) bool { return observations[left].date.Before(observations[right].date) })
	niceDistribution := finishProfile(nice, observations, func(item scoredObservation) int { return item.niceScore })
	cyclingDistribution := finishProfile(cycling, observations, func(item scoredObservation) int { return item.cyclingScore })
	return CellDistribution{
		Days: len(days), ExpectedDays: len(expectedDates), Coverage: 1,
		ComfortGoodDays: comfortGood,
		DryGoodDays:     dryGood,
		BrightGoodDays:  brightGood,
		Nice:            niceDistribution,
		Cycling:         cyclingDistribution,
	}, nil
}

func finishProfile(accumulator profileAccumulator, observations []scoredObservation, scoreOf func(scoredObservation) int) ProfileDistribution {
	scores := append([]int(nil), accumulator.scores...)
	sort.Ints(scores)
	median := scores[len(scores)/2]
	if len(scores)%2 == 0 {
		median = int(math.Round(float64(scores[len(scores)/2-1]+scores[len(scores)/2]) / 2))
	}
	poorWeeks, sevenDayWindows := 0, 0
	for start := 0; start+7 <= len(observations); start++ {
		window := observations[start : start+7]
		if !calendarContiguous(window) {
			continue
		}
		sevenDayWindows++
		poorRun, hasPoorSpell := 0, false
		for _, item := range window {
			if isPoorRating(weather.Rating(scoreOf(item))) {
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
		if !isPoorRating(weather.Rating(scoreOf(item))) {
			poorRun = 0
			continue
		}
		poorRun++
		if poorRun > longestPoorRun {
			longestPoorRun = poorRun
		}
	}
	total := float64(len(observations))
	return ProfileDistribution{
		MeanScore: float64(accumulator.sum) / total, MedianScore: median,
		CalmGoodDays:   accumulator.calmGood,
		LongestPoorRun: longestPoorRun, SevenDayWindows: sevenDayWindows,
		PoorWeekWindows: poorWeeks,
		Ratings:         accumulator.ratings,
	}
}

// GoodOrBetterFraction derives the chance of a Good, Very good or Excellent
// day from the one canonical rating count.
func (profile ProfileDistribution) GoodOrBetterFraction() float64 {
	total := profile.Ratings.total()
	if total == 0 {
		return 0
	}
	return float64(profile.Ratings.Good+profile.Ratings.VeryGood+profile.Ratings.Excellent) / float64(total)
}

// AwfulFraction derives the chance of an Awful day from the canonical counts.
func (profile ProfileDistribution) AwfulFraction() float64 {
	total := profile.Ratings.total()
	if total == 0 {
		return 0
	}
	return float64(profile.Ratings.Awful) / float64(total)
}

func (profile ProfileDistribution) CalmGoodFraction() float64 {
	return dayFraction(profile.CalmGoodDays, profile.Ratings.total())
}

func (profile ProfileDistribution) PoorWeekFraction() float64 {
	return dayFraction(profile.PoorWeekWindows, profile.SevenDayWindows)
}

func (distribution CellDistribution) ComfortGoodFraction() float64 {
	return dayFraction(distribution.ComfortGoodDays, distribution.Days)
}

func (distribution CellDistribution) DryGoodFraction() float64 {
	return dayFraction(distribution.DryGoodDays, distribution.Days)
}

func (distribution CellDistribution) BrightGoodFraction() float64 {
	return dayFraction(distribution.BrightGoodDays, distribution.Days)
}

func dayFraction(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func calendarContiguous(observations []scoredObservation) bool {
	for index := 1; index < len(observations); index++ {
		if !observations[index].date.Equal(observations[index-1].date.AddDate(0, 0, 1)) {
			return false
		}
	}
	return true
}

func isGoodRating(rating string) bool {
	return rating == "Good" || rating == "Very good" || rating == "Excellent"
}

func isPoorRating(rating string) bool {
	return rating == "Poor" || rating == "Awful"
}

// DeliveryRecord converts complete equal-weight daily evidence into the fixed
// delivery contract. Rating shares use largest-remainder allocation and always
// total exactly 1000 permille.
func (distribution CellDistribution) DeliveryRecord() (delivery.ClimateDistributionRecord, error) {
	if distribution.Days <= 0 || distribution.ExpectedDays != distribution.Days || distribution.Coverage != 1 {
		return delivery.ClimateDistributionRecord{}, errors.New("only a complete climate distribution can be delivered")
	}
	nice, err := deliveryProfile(distribution.Nice, distribution.Days)
	if err != nil {
		return delivery.ClimateDistributionRecord{}, fmt.Errorf("Nice climate distribution: %w", err)
	}
	cycling, err := deliveryProfile(distribution.Cycling, distribution.Days)
	if err != nil {
		return delivery.ClimateDistributionRecord{}, fmt.Errorf("Cycling climate distribution: %w", err)
	}
	comfort, err := roundedPercent(distribution.ComfortGoodDays, distribution.Days)
	if err != nil {
		return delivery.ClimateDistributionRecord{}, fmt.Errorf("comfort climate distribution: %w", err)
	}
	dry, err := roundedPercent(distribution.DryGoodDays, distribution.Days)
	if err != nil {
		return delivery.ClimateDistributionRecord{}, fmt.Errorf("dry climate distribution: %w", err)
	}
	bright, err := roundedPercent(distribution.BrightGoodDays, distribution.Days)
	if err != nil {
		return delivery.ClimateDistributionRecord{}, fmt.Errorf("bright climate distribution: %w", err)
	}
	record := delivery.ClimateDistributionRecord{
		ComfortGoodPercent: comfort, DryGoodPercent: dry, BrightGoodPercent: bright,
		Nice: nice, Cycling: cycling, Valid: true,
	}
	encoded := make([]byte, delivery.ClimateDistributionRecordBytes)
	if err := delivery.EncodeClimateDistributionRecord(encoded, record); err != nil {
		return delivery.ClimateDistributionRecord{}, err
	}
	return record, nil
}

func deliveryProfile(profile ProfileDistribution, days int) (delivery.ClimateProfileDistribution, error) {
	if profile.MedianScore < 0 || profile.MedianScore > 100 || profile.Ratings.total() != days {
		return delivery.ClimateProfileDistribution{}, errors.New("invalid score or rating counts")
	}
	calm, err := roundedPercent(profile.CalmGoodDays, days)
	if err != nil {
		return delivery.ClimateProfileDistribution{}, fmt.Errorf("calm probability: %w", err)
	}
	poorWeek, err := roundedPermille(profile.PoorWeekWindows, profile.SevenDayWindows)
	if err != nil {
		return delivery.ClimateProfileDistribution{}, fmt.Errorf("poor-week probability: %w", err)
	}
	shares, err := allocatePermille(profile.Ratings.values())
	if err != nil {
		return delivery.ClimateProfileDistribution{}, err
	}
	return delivery.ClimateProfileDistribution{
		MedianScore: uint8(profile.MedianScore), CalmGoodPercent: calm, PoorWeekPermille: poorWeek,
		RatingShares: delivery.ClimateRatingShares{
			Awful: shares[0], Poor: shares[1], Mixed: shares[2], Good: shares[3], VeryGood: shares[4], Excellent: shares[5],
		},
	}, nil
}

func roundedPercent(numerator, denominator int) (uint8, error) {
	value, err := roundedRatio(numerator, denominator, 100)
	return uint8(value), err
}

func roundedPermille(numerator, denominator int) (uint16, error) {
	return roundedRatio(numerator, denominator, 1000)
}

func roundedRatio(numerator, denominator, scale int) (uint16, error) {
	if denominator <= 0 || numerator < 0 || numerator > denominator || scale <= 0 || scale > 1000 {
		return 0, fmt.Errorf("invalid probability ratio %d/%d at scale %d", numerator, denominator, scale)
	}
	scaled := numerator * scale
	result := scaled / denominator
	if (scaled%denominator)*2 >= denominator {
		result++
	}
	return uint16(result), nil
}

func allocatePermille(counts [6]int) ([6]uint16, error) {
	total := 0
	for _, count := range counts {
		if count < 0 {
			return [6]uint16{}, errors.New("negative climate rating count")
		}
		total += count
	}
	if total == 0 {
		return [6]uint16{}, errors.New("climate rating counts are empty")
	}
	result := [6]uint16{}
	remainders := [6]int{}
	allocated := 0
	for index, count := range counts {
		scaled := count * 1000
		result[index] = uint16(scaled / total)
		allocated += int(result[index])
		remainders[index] = scaled % total
	}
	for remaining := 1000 - allocated; remaining > 0; remaining-- {
		largest := 0
		for index := 1; index < len(remainders); index++ {
			if remainders[index] > remainders[largest] {
				largest = index
			}
		}
		result[largest]++
		remainders[largest] = -1
	}
	return result, nil
}
