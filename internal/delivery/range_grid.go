package delivery

import (
	"errors"
	"fmt"

	"nice-weather-finder/internal/weather"
)

const (
	maxRangeDayCount = 255
)

// RangeRecord is the compact representation of weather.RangeSummary embedded
// in the shared two-profile NWFPOINT forecast-window record.
type RangeRecord struct {
	Score          uint8
	MedianScore    uint8
	Thermal        uint8
	Dryness        uint8
	Sun            uint8
	Wind           uint8
	NiceDays       uint8
	AwfulDays      uint8
	LongestPoorRun uint8
	DayCount       uint8
	Valid          bool
}

// RangeRecordFromSummary is the only checked conversion from the canonical
// forecast-window judgement into compact delivery bytes.
func RangeRecordFromSummary(summary weather.RangeSummary) (RangeRecord, error) {
	if err := validateRangeSummary(summary); err != nil {
		return RangeRecord{}, err
	}
	return RangeRecord{
		Score: uint8(summary.Score), MedianScore: uint8(summary.MedianScore),
		Thermal: uint8(summary.Components.Thermal), Dryness: uint8(summary.Components.Dryness),
		Sun: uint8(summary.Components.Sun), Wind: uint8(summary.Components.Wind),
		NiceDays: uint8(summary.NiceDays), AwfulDays: uint8(summary.AwfulDays),
		LongestPoorRun: uint8(summary.LongestPoorRun), DayCount: uint8(summary.Days), Valid: true,
	}, nil
}

func (record RangeRecord) Summary() (weather.RangeSummary, error) {
	if err := validateRangeRecord(record); err != nil {
		return weather.RangeSummary{}, err
	}
	if !record.Valid {
		return weather.RangeSummary{}, errors.New("no forecast range at this cell")
	}
	score := int(record.Score)
	return weather.RangeSummary{
		Days: int(record.DayCount), Score: score, Rating: weather.Rating(score), MedianScore: int(record.MedianScore),
		NiceDays: int(record.NiceDays), AwfulDays: int(record.AwfulDays), LongestPoorRun: int(record.LongestPoorRun),
		Components: weather.Components{Thermal: int(record.Thermal), Dryness: int(record.Dryness), Sun: int(record.Sun), Wind: int(record.Wind)},
	}, nil
}

func validateRangeSummary(summary weather.RangeSummary) error {
	if summary.Days <= 0 || summary.Days > maxRangeDayCount {
		return errors.New("range day count is outside delivery range")
	}
	if summary.Rating != weather.Rating(summary.Score) {
		return errors.New("range rating does not match its score")
	}
	return validateRangeValues(
		summary.Score, summary.MedianScore,
		summary.Components.Thermal, summary.Components.Dryness, summary.Components.Sun, summary.Components.Wind,
		summary.NiceDays, summary.AwfulDays, summary.LongestPoorRun, summary.Days,
	)
}

func validateRangeRecord(record RangeRecord) error {
	if !record.Valid {
		if record != (RangeRecord{}) {
			return errors.New("invalid range record must contain only zero values")
		}
		return nil
	}
	return validateRangeValues(
		int(record.Score), int(record.MedianScore),
		int(record.Thermal), int(record.Dryness), int(record.Sun), int(record.Wind),
		int(record.NiceDays), int(record.AwfulDays), int(record.LongestPoorRun), int(record.DayCount),
	)
}

func validateRangeValues(score, median, thermal, dryness, sun, wind, niceDays, awfulDays, longestPoorRun, dayCount int) error {
	for _, field := range [...]struct {
		name  string
		value int
	}{
		{"score", score}, {"median score", median}, {"thermal score", thermal},
		{"dryness score", dryness}, {"sun score", sun}, {"wind score", wind},
	} {
		if field.value < 0 || field.value > 100 {
			return fmt.Errorf("range %s is outside 0..100", field.name)
		}
	}
	if dayCount <= 0 || dayCount > maxRangeDayCount {
		return errors.New("range day count is outside delivery range")
	}
	if niceDays < 0 || awfulDays < 0 || niceDays > dayCount || awfulDays > dayCount || niceDays+awfulDays > dayCount {
		return errors.New("range Nice/Awful counts are inconsistent with day count")
	}
	minimumPoorRun := 0
	if awfulDays > 0 {
		separators := dayCount - awfulDays + 1
		minimumPoorRun = (awfulDays + separators - 1) / separators
	}
	if longestPoorRun < minimumPoorRun || longestPoorRun > dayCount-niceDays {
		return errors.New("range longest poor run is inconsistent with day count")
	}
	return nil
}
