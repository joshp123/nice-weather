package delivery

import (
	"crypto/sha256"
	"math"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/weather"
)

func TestRangeGridRoundTripsProfileFilesAndPointRules(t *testing.T) {
	dir := t.TempDir()
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 4, South: 50, North: 52}, Rows: 2, Cols: 4}
	niceRecords := []RangeRecord{
		rangeRecord(82, 14), rangeRecord(63, 14), {}, rangeRecord(91, 14),
		rangeRecord(55, 14), rangeRecord(35, 14), rangeRecord(23, 14), rangeRecord(74, 14),
	}
	cyclingRecords := append([]RangeRecord(nil), niceRecords...)
	cyclingRecords[0] = rangeRecord(61, 14)

	nicePath := filepath.Join(dir, "nice.nwr")
	cyclingPath := filepath.Join(dir, "cycling.nwr")
	niceMeta, err := WriteRangeGrid(nicePath, spec, niceRecords)
	if err != nil {
		t.Fatal(err)
	}
	cyclingMeta, err := WriteRangeGrid(cyclingPath, spec, cyclingRecords)
	if err != nil {
		t.Fatal(err)
	}
	if niceMeta.Version != RangeGridVersion || niceMeta.RecordSize != rangeGridRecordSize || niceMeta.Rows != spec.Rows || niceMeta.Cols != spec.Cols || len(niceMeta.SHA256) != 64 {
		t.Fatalf("unexpected range metadata: %+v", niceMeta)
	}
	if niceMeta.SHA256 == cyclingMeta.SHA256 {
		t.Fatal("profile-specific range grids have the same content hash")
	}

	reader, err := OpenRangeGrid(nicePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Metadata() != niceMeta {
		t.Fatalf("read metadata %+v; want %+v", reader.Metadata(), niceMeta)
	}
	checks := []struct {
		lon, lat float64
		want     RangeRecord
		valid    bool
	}{
		{0, 52, niceRecords[0], true},
		{3.9, 52, niceRecords[3], true},
		{1, 51, niceRecords[1], true},
		{2.2, 51.7, RangeRecord{}, false},
		{3.9, 50, niceRecords[7], true},
		{-0.01, 51, RangeRecord{}, false},
		{4.01, 51, RangeRecord{}, false},
		{math.NaN(), 51, RangeRecord{}, false},
	}
	for _, check := range checks {
		got, valid, err := reader.RecordAt(check.lon, check.lat)
		if err != nil {
			t.Fatalf("RecordAt(%v, %v): %v", check.lon, check.lat, err)
		}
		if valid != check.valid || got != check.want {
			t.Errorf("RecordAt(%v, %v) = (%+v, %v); want (%+v, %v)", check.lon, check.lat, got, valid, check.want, check.valid)
		}
	}

	if _, err := OpenGrid(nicePath); err == nil {
		t.Fatal("daily grid reader accepted a range grid")
	}
	dailyPath := filepath.Join(dir, "day.nwg")
	if _, err := WriteGrid(dailyPath, GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, []DailyRecord{{
		ComfortableTemperatureDeciC: 200, MinTemperatureDeciC: 190, MaxTemperatureDeciC: 210,
		CloudPercent: 50, HumidityPercent: 60, Valid: true, HasCloud: true, HasHumidity: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRangeGrid(dailyPath); err == nil {
		t.Fatal("range grid reader accepted a daily grid")
	}
}

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

func TestWriteRangeGridRejectsInvalidRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.nwr")
	spec := GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
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
			if _, err := WriteRangeGrid(path, spec, []RangeRecord{record}); err == nil {
				t.Fatal("invalid range record was accepted")
			}
		})
	}
	if _, err := WriteRangeGrid(path, GridSpec{Bounds: spec.Bounds, Rows: 2, Cols: 1}, []RangeRecord{valid}); err == nil {
		t.Fatal("range grid shape mismatch was accepted")
	}
}

func TestRangeGridRejectsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.nwr")
	if _, err := WriteRangeGrid(valid, GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, []RangeRecord{rangeRecord(70, 14)}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"truncated":       original[:100],
		"bad magic":       mutate(original, 0),
		"bad version":     mutate(original, 8),
		"bad record size": mutate(original, 12),
		"changed bounds":  mutate(original, 24),
		"changed payload": mutate(original, gridHeaderSize),
		"unknown flags":   mutate(original, gridHeaderSize+10),
		"reserved record": mutate(original, gridHeaderSize+11),
		"reserved header": mutate(original, 100),
		"extra bytes":     append(append([]byte(nil), original...), 0),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".nwr")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateRangeGrid(path); err == nil {
				t.Fatal("corrupt range grid was accepted")
			}
		})
	}
}

func TestRangeGridRejectsChecksumCorrectImpossiblePoorRuns(t *testing.T) {
	tests := []struct {
		name   string
		record RangeRecord
	}{
		{"all days Awful", rangeRecord(20, 14)},
		{"eight Awful days need a run of two", RangeRecord{
			Score: 50, MedianScore: 50, Thermal: 50, Dryness: 50, Sun: 50, Wind: 50,
			AwfulDays: 8, LongestPoorRun: 2, DayCount: 14, Valid: true,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "impossible-run.nwr")
			if _, err := WriteRangeGrid(path, GridSpec{Bounds: GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, []RangeRecord{test.record}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[gridHeaderSize+8] = 1
			digest := sha256.New()
			_, _ = digest.Write(data[:64])
			_, _ = digest.Write(data[gridHeaderSize:])
			copy(data[64:96], digest.Sum(nil))
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateRangeGrid(path); err == nil {
				t.Fatal("checksum-correct impossible poor run was accepted")
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
