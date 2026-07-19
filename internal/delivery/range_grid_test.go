package delivery

import (
	"testing"

	"nice-weather-finder/internal/weather"
)

func TestRangeRecordConvertsCanonicalSummaryWithoutLosingFields(t *testing.T) {
	summary := weather.RangeSummary{
		Days: 14, Score: 72, Rating: weather.Rating(72), MedianScore: 76,
		NiceDays: 10, AwfulDays: 2, LongestPoorRun: 2,
		Components: weather.Components{Thermal: 81, Dryness: 67, Sun: 79, Wind: 73},
	}
	record, err := RangeRecordFromSummary(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Valid || record.DayCount != 14 || record.Thermal != 81 || record.AwfulDays != 2 {
		t.Fatalf("unexpected compact record: %+v", record)
	}
	roundTrip, err := record.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip != summary {
		t.Fatalf("summary round trip = %+v; want %+v", roundTrip, summary)
	}

	badRating := summary
	badRating.Rating = "Excellent"
	if _, err := RangeRecordFromSummary(badRating); err == nil {
		t.Fatal("mismatched canonical rating was accepted")
	}
	tooLong := summary
	tooLong.Days = 256
	if _, err := RangeRecordFromSummary(tooLong); err == nil {
		t.Fatal("unrepresentable day count was accepted")
	}
	if _, err := (RangeRecord{}).Summary(); err == nil {
		t.Fatal("invalid cell was converted into a forecast summary")
	}
}

func TestRangeRecordAcceptsCanonicalSummariesAcrossScoreShapes(t *testing.T) {
	state := uint32(1)
	for sample := 0; sample < 1000; sample++ {
		dayCount := sample%14 + 1
		days := make([]weather.Result, dayCount)
		for index := range days {
			state = state*1664525 + 1013904223
			score := int(state % 101)
			days[index] = weather.Result{
				Available: true, Score: score, Rating: weather.Rating(score),
				Components: weather.Components{Thermal: score, Dryness: score, Sun: score, Wind: score},
			}
		}
		summary, err := weather.SummarizeRange(days)
		if err != nil {
			t.Fatal(err)
		}
		record, err := RangeRecordFromSummary(summary)
		if err != nil {
			t.Fatalf("canonical sample %d rejected: summary=%+v: %v", sample, summary, err)
		}
		roundTrip, err := record.Summary()
		if err != nil || roundTrip != summary {
			t.Fatalf("canonical sample %d did not round-trip: got=%+v err=%v want=%+v", sample, roundTrip, err, summary)
		}
	}
}

func TestRangeRecordRejectsInvalidValues(t *testing.T) {
	valid := rangeRecord(70, 14)
	cases := map[string]RangeRecord{}
	bad := valid
	bad.Score = 101
	cases["score"] = bad
	bad = valid
	bad.MedianScore = 101
	cases["median"] = bad
	bad = valid
	bad.Thermal = 101
	cases["component"] = bad
	bad = valid
	bad.DayCount = 0
	cases["zero days"] = bad
	bad = valid
	bad.NiceDays = 15
	cases["nice count"] = bad
	bad = valid
	bad.NiceDays, bad.AwfulDays = 13, 2
	cases["overlapping ratings"] = bad
	bad = valid
	bad.AwfulDays, bad.LongestPoorRun = 1, 0
	cases["awful without poor run"] = bad
	bad = valid
	bad.LongestPoorRun = 15
	cases["poor run"] = bad
	bad = valid
	bad.LongestPoorRun = 1
	cases["poor run overlaps Nice days"] = bad
	allAwful := rangeRecord(20, 14)
	allAwful.LongestPoorRun = 13
	cases["all Awful with short poor run"] = allAwful
	bad = valid
	bad.Valid = false
	cases["invalid with data"] = bad

	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateRangeRecord(record); err == nil {
				t.Fatal("invalid range record was accepted")
			}
		})
	}
}

func rangeRecord(score, days uint8) RangeRecord {
	record := RangeRecord{
		Score: score, MedianScore: score, Thermal: score, Dryness: score, Sun: score, Wind: score,
		DayCount: days, Valid: true,
	}
	if score >= 55 {
		record.NiceDays = days
	}
	if score <= 24 {
		record.AwfulDays = days
	}
	if score <= 39 {
		record.LongestPoorRun = days
	}
	return record
}
