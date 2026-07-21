package climate

import (
	"errors"
	"fmt"
	"math"
	"time"

	"nice-weather-finder/internal/weather"
)

// StreamingAccumulator builds all monthly climate distributions for one grid
// cell while retaining only fixed-size score histograms and rolling state.
// Days must arrive once, in calendar order, for the complete reference window.
type StreamingAccumulator struct {
	window    ReferenceWindow
	nextDate  time.Time
	finalDate time.Time
	latitude  float64
	longitude float64
	hasCell   bool
	months    [12]streamMonthAccumulator
	failed    error
	finalized bool
}

type streamMonthAccumulator struct {
	days            int
	comfortGoodDays int
	dryGoodDays     int
	brightGoodDays  int
	lastDate        time.Time
	nice            streamProfileAccumulator
	cycling         streamProfileAccumulator
}

type streamProfileAccumulator struct {
	sum             int
	scoreCounts     [101]int
	calmGoodDays    int
	ratings         RatingCounts
	longestPoorRun  int
	currentPoorRun  int
	sevenDayWindows int
	poorWeekWindows int
	recentPoor      [7]bool
	recentCount     int
}

// NewStreamingAccumulator starts a complete, chronological climate build.
func NewStreamingAccumulator(window ReferenceWindow) (*StreamingAccumulator, error) {
	if err := window.validate(); err != nil {
		return nil, err
	}
	return &StreamingAccumulator{
		window:    window,
		nextDate:  time.Date(window.FirstYear, time.January, 1, 0, 0, 0, 0, time.UTC),
		finalDate: time.Date(window.LastYear, time.December, 31, 0, 0, 0, 0, time.UTC),
	}, nil
}

// Add scores one normalized AgERA5 day. A rejected day poisons the
// accumulator so partial or ambiguous evidence cannot later be published.
func (accumulator *StreamingAccumulator) Add(day AgERA5Day) error {
	if accumulator == nil {
		return errors.New("nil climate streaming accumulator")
	}
	if accumulator.failed != nil {
		return accumulator.failed
	}
	if accumulator.finalized {
		return errors.New("climate streaming accumulator is already finalized")
	}
	date, err := time.Parse("2006-01-02", day.Date)
	if err != nil {
		return accumulator.fail(fmt.Errorf("invalid AgERA5 date %q", day.Date))
	}
	if date.After(accumulator.finalDate) {
		return accumulator.fail(fmt.Errorf("AgERA5 date %s is outside reference window %d–%d", day.Date, accumulator.window.FirstYear, accumulator.window.LastYear))
	}
	if !date.Equal(accumulator.nextDate) {
		if date.Before(accumulator.nextDate) {
			return accumulator.fail(fmt.Errorf("duplicate or out-of-order AgERA5 date %s; expected %s", day.Date, formatDate(accumulator.nextDate)))
		}
		return accumulator.fail(fmt.Errorf("incomplete AgERA5 stream: got %s; expected %s", day.Date, formatDate(accumulator.nextDate)))
	}
	metrics, err := day.Metrics()
	if err != nil {
		return accumulator.fail(fmt.Errorf("day %s: %w", day.Date, err))
	}
	if accumulator.hasCell {
		if math.Abs(day.Latitude-accumulator.latitude) > 1e-9 || math.Abs(day.Longitude-accumulator.longitude) > 1e-9 {
			return accumulator.fail(fmt.Errorf("day %s does not belong to climate cell %.9f,%.9f", day.Date, accumulator.latitude, accumulator.longitude))
		}
	} else {
		accumulator.latitude = day.Latitude
		accumulator.longitude = day.Longitude
		accumulator.hasCell = true
	}

	nice := weather.Score(metrics, weather.ProfileNice)
	cycling := weather.Score(metrics, weather.ProfileCycling)
	if !nice.Available || !cycling.Available {
		return accumulator.fail(fmt.Errorf("day %s is not scoreable", day.Date))
	}
	if nice.Components.Thermal != cycling.Components.Thermal || nice.Components.Dryness != cycling.Components.Dryness || nice.Components.Sun != cycling.Components.Sun {
		return accumulator.fail(fmt.Errorf("day %s has profile-dependent shared components", day.Date))
	}

	month := &accumulator.months[date.Month()-1]
	contiguous := !month.lastDate.IsZero() && date.Equal(month.lastDate.AddDate(0, 0, 1))
	if err := month.add(date, contiguous, nice, cycling); err != nil {
		return accumulator.fail(fmt.Errorf("day %s: %w", day.Date, err))
	}
	accumulator.nextDate = date.AddDate(0, 0, 1)
	return nil
}

func (accumulator *StreamingAccumulator) fail(err error) error {
	accumulator.failed = err
	return err
}

func (accumulator *streamMonthAccumulator) add(date time.Time, contiguous bool, nice, cycling weather.Result) error {
	if err := accumulator.nice.add(nice, contiguous); err != nil {
		return fmt.Errorf("Nice: %w", err)
	}
	if err := accumulator.cycling.add(cycling, contiguous); err != nil {
		return fmt.Errorf("Cycling: %w", err)
	}
	if isGoodRating(weather.Rating(nice.Components.Thermal)) {
		accumulator.comfortGoodDays++
	}
	if isGoodRating(weather.Rating(nice.Components.Dryness)) {
		accumulator.dryGoodDays++
	}
	if isGoodRating(weather.Rating(nice.Components.Sun)) {
		accumulator.brightGoodDays++
	}
	accumulator.days++
	accumulator.lastDate = date
	return nil
}

func (accumulator *streamProfileAccumulator) add(result weather.Result, contiguous bool) error {
	if result.Score < 0 || result.Score > 100 {
		return fmt.Errorf("score %d is outside 0–100", result.Score)
	}
	if !contiguous {
		accumulator.currentPoorRun = 0
		accumulator.recentCount = 0
	}
	poor := isPoorRating(result.Rating)
	if poor {
		accumulator.currentPoorRun++
		if accumulator.currentPoorRun > accumulator.longestPoorRun {
			accumulator.longestPoorRun = accumulator.currentPoorRun
		}
	} else {
		accumulator.currentPoorRun = 0
	}
	if accumulator.recentCount < len(accumulator.recentPoor) {
		accumulator.recentPoor[accumulator.recentCount] = poor
		accumulator.recentCount++
	} else {
		copy(accumulator.recentPoor[:], accumulator.recentPoor[1:])
		accumulator.recentPoor[len(accumulator.recentPoor)-1] = poor
	}
	if accumulator.recentCount == len(accumulator.recentPoor) {
		accumulator.sevenDayWindows++
		if containsThreePoorDays(accumulator.recentPoor) {
			accumulator.poorWeekWindows++
		}
	}
	accumulator.sum += result.Score
	accumulator.scoreCounts[result.Score]++
	if isGoodRating(weather.Rating(result.Components.Wind)) {
		accumulator.calmGoodDays++
	}
	return accumulator.ratings.add(result.Rating)
}

func containsThreePoorDays(days [7]bool) bool {
	run := 0
	for _, poor := range days {
		if !poor {
			run = 0
			continue
		}
		run++
		if run == 3 {
			return true
		}
	}
	return false
}

// Finalize returns January through December only after every expected date has
// been accepted. It never returns a partial climate product.
func (accumulator *StreamingAccumulator) Finalize() ([12]CellDistribution, error) {
	if accumulator == nil {
		return [12]CellDistribution{}, errors.New("nil climate streaming accumulator")
	}
	if accumulator.failed != nil {
		return [12]CellDistribution{}, accumulator.failed
	}
	if accumulator.finalized {
		return [12]CellDistribution{}, errors.New("climate streaming accumulator is already finalized")
	}
	completeAt := accumulator.finalDate.AddDate(0, 0, 1)
	if !accumulator.nextDate.Equal(completeAt) {
		return [12]CellDistribution{}, fmt.Errorf("incomplete AgERA5 stream: expected %s next", formatDate(accumulator.nextDate))
	}

	var result [12]CellDistribution
	for index := range accumulator.months {
		month := time.Month(index + 1)
		expected := daysInReferenceMonth(accumulator.window, month)
		if accumulator.months[index].days != expected {
			return [12]CellDistribution{}, fmt.Errorf("incomplete AgERA5 %s distribution: have %d of %d days", month, accumulator.months[index].days, expected)
		}
		result[index] = accumulator.months[index].finish(expected)
	}
	accumulator.finalized = true
	return result, nil
}

func (accumulator streamMonthAccumulator) finish(expected int) CellDistribution {
	return CellDistribution{
		Days: accumulator.days, ExpectedDays: expected, Coverage: 1,
		ComfortGoodDays: accumulator.comfortGoodDays,
		DryGoodDays:     accumulator.dryGoodDays,
		BrightGoodDays:  accumulator.brightGoodDays,
		Nice:            accumulator.nice.finish(accumulator.days),
		Cycling:         accumulator.cycling.finish(accumulator.days),
	}
}

func (accumulator streamProfileAccumulator) finish(days int) ProfileDistribution {
	return ProfileDistribution{
		MeanScore:       float64(accumulator.sum) / float64(days),
		MedianScore:     histogramMedian(accumulator.scoreCounts, days),
		CalmGoodDays:    accumulator.calmGoodDays,
		LongestPoorRun:  accumulator.longestPoorRun,
		SevenDayWindows: accumulator.sevenDayWindows,
		PoorWeekWindows: accumulator.poorWeekWindows,
		Ratings:         accumulator.ratings,
	}
}

func histogramMedian(counts [101]int, total int) int {
	lowerPosition := (total - 1) / 2
	upperPosition := total / 2
	lower, upper, seen := 0, 0, 0
	foundLower := false
	for score, count := range counts {
		seen += count
		if seen > lowerPosition && !foundLower {
			lower = score
			foundLower = true
		}
		if seen > upperPosition {
			upper = score
			break
		}
	}
	return (lower + upper + 1) / 2
}

func daysInReferenceMonth(window ReferenceWindow, month time.Month) int {
	total := 0
	for year := window.FirstYear; year <= window.LastYear; year++ {
		total += time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	return total
}

func formatDate(date time.Time) string {
	return date.Format("2006-01-02")
}
