package delivery

import (
	"nice-weather-finder/internal/weather"
	"testing"
)

func TestRangePointCodecExportsEstablishedCheckedLayout(t *testing.T) {
	record := testRangePointRecord(70)
	encoded := make([]byte, RangePointRecordBytes)
	if err := EncodeRangePointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRangePointRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != record {
		t.Fatalf("decoded range record = %+v; want %+v", decoded, record)
	}
	if err := EncodeRangePointRecord(encoded[:len(encoded)-1], record); err == nil {
		t.Fatal("short range record destination was accepted")
	}
	encoded[43] = 2
	if _, err := DecodeRangePointRecord(encoded); err == nil {
		t.Fatal("unknown range record flags were accepted")
	}
}

func testRangePointRecord(score int) RangePointRecord {
	results := make([]weather.Result, ForecastPointDayCount)
	var days [ForecastPointDayCount]uint8
	for index := range results {
		results[index] = weather.Result{Available: true, Score: score, Rating: weather.Rating(score), Components: weather.Components{Thermal: score, Dryness: score, Sun: score, Wind: score}}
		days[index] = uint8(score)
	}
	summary, _ := weather.SummarizeRange(results)
	record, _ := RangeRecordFromSummary(summary)
	return RangePointRecord{Nice: record, Cycling: record, NiceDays: days, CyclingDays: days}
}
