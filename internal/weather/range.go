package weather

import (
	"errors"
	"sort"
)

const (
	niceDayMinimum  = 55
	poorDayMaximum  = 39
	awfulDayMaximum = 24
)

// RangeSummary is the transparent judgement for one place across a concrete
// forecast window. Score and component values are arithmetic means of the
// already-evaluated daily judgements. Counts and the longest poor run preserve
// the variation that a mean alone would hide.
type RangeSummary struct {
	Days           int
	Score          int
	Rating         string
	MedianScore    int
	NiceDays       int
	AwfulDays      int
	LongestPoorRun int
	Components     Components
}

// SummarizeRange combines canonical daily results without scoring weather a
// second time. A forecast window is publishable only when every promised day
// is available; callers must not silently shorten the range around missing
// data.
func SummarizeRange(days []Result) (RangeSummary, error) {
	if len(days) == 0 {
		return RangeSummary{}, errors.New("at least one forecast day is required")
	}

	scores := make([]int, len(days))
	var scoreSum, thermalSum, drynessSum, sunSum, windSum int
	poorRun := 0
	result := RangeSummary{Days: len(days)}
	for index, day := range days {
		if !day.Available {
			return RangeSummary{}, errors.New("forecast range contains an unavailable day")
		}
		scores[index] = day.Score
		scoreSum += day.Score
		thermalSum += day.Components.Thermal
		drynessSum += day.Components.Dryness
		sunSum += day.Components.Sun
		windSum += day.Components.Wind
		if day.Score >= niceDayMinimum {
			result.NiceDays++
		}
		if day.Score <= awfulDayMaximum {
			result.AwfulDays++
		}
		if day.Score <= poorDayMaximum {
			poorRun++
			if poorRun > result.LongestPoorRun {
				result.LongestPoorRun = poorRun
			}
		} else {
			poorRun = 0
		}
	}

	result.Score = rounded(float64(scoreSum) / float64(len(days)))
	result.Rating = Rating(result.Score)
	result.Components = Components{
		Thermal: rounded(float64(thermalSum) / float64(len(days))),
		Dryness: rounded(float64(drynessSum) / float64(len(days))),
		Sun:     rounded(float64(sunSum) / float64(len(days))),
		Wind:    rounded(float64(windSum) / float64(len(days))),
	}
	sort.Ints(scores)
	result.MedianScore = scores[len(scores)/2]
	if len(scores)%2 == 0 {
		result.MedianScore = rounded(float64(scores[len(scores)/2-1]+scores[len(scores)/2]) / 2)
	}
	return result, nil
}
