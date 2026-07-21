package climate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStreamingAccumulatorMatchesAggregateAndDeliveryForEveryMonth(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2023, LastYear: 2024}
	stream, err := NewStreamingAccumulator(window)
	if err != nil {
		t.Fatal(err)
	}
	var byMonth [12][]AgERA5Day
	for date := time.Date(window.FirstYear, time.January, 1, 0, 0, 0, 0, time.UTC); date.Year() <= window.LastYear; date = date.AddDate(0, 0, 1) {
		observation := variedDay(date)
		byMonth[date.Month()-1] = append(byMonth[date.Month()-1], observation)
		if err := stream.Add(observation); err != nil {
			t.Fatalf("add %s: %v", observation.Date, err)
		}
	}
	got, err := stream.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	for index := range got {
		want, err := Aggregate(byMonth[index], window)
		if err != nil {
			t.Fatalf("aggregate %s: %v", time.Month(index+1), err)
		}
		if !reflect.DeepEqual(got[index], want) {
			t.Fatalf("%s streaming distribution differs\ngot  %+v\nwant %+v", time.Month(index+1), got[index], want)
		}
		if want.Nice.PoorWeekWindows == 0 || want.Nice.LongestPoorRun != 3 {
			t.Fatalf("%s fixture did not exercise persistence: %+v", time.Month(index+1), want.Nice)
		}
		gotRecord, err := got[index].DeliveryRecord()
		if err != nil {
			t.Fatalf("streaming %s delivery: %v", time.Month(index+1), err)
		}
		wantRecord, err := want.DeliveryRecord()
		if err != nil {
			t.Fatalf("aggregate %s delivery: %v", time.Month(index+1), err)
		}
		if gotRecord != wantRecord {
			t.Fatalf("%s delivery differs\ngot  %+v\nwant %+v", time.Month(index+1), gotRecord, wantRecord)
		}
	}
}

func TestStreamingAccumulatorRejectsGapsDuplicatesOrderCellsAndMetrics(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	newStream := func(t *testing.T) *StreamingAccumulator {
		t.Helper()
		stream, err := NewStreamingAccumulator(window)
		if err != nil {
			t.Fatal(err)
		}
		return stream
	}

	t.Run("gap", func(t *testing.T) {
		stream := newStream(t)
		err := stream.Add(day("2024-01-02", 36.72, -4.42, 24, 0, 0, 15, 20, 12))
		if err == nil || !strings.Contains(err.Error(), "expected 2024-01-01") {
			t.Fatalf("gap error = %v", err)
		}
		if _, finalErr := stream.Finalize(); finalErr == nil || finalErr.Error() != err.Error() {
			t.Fatalf("poisoned finalize error = %v; want %v", finalErr, err)
		}
	})

	t.Run("duplicate or out of order", func(t *testing.T) {
		stream := newStream(t)
		first := day("2024-01-01", 36.72, -4.42, 24, 0, 0, 15, 20, 12)
		if err := stream.Add(first); err != nil {
			t.Fatal(err)
		}
		if err := stream.Add(first); err == nil || !strings.Contains(err.Error(), "duplicate or out-of-order") {
			t.Fatalf("duplicate error = %v", err)
		}
	})

	t.Run("mixed cell", func(t *testing.T) {
		stream := newStream(t)
		if err := stream.Add(day("2024-01-01", 36.72, -4.42, 24, 0, 0, 15, 20, 12)); err != nil {
			t.Fatal(err)
		}
		if err := stream.Add(day("2024-01-02", 36.72, -4.32, 24, 0, 0, 15, 20, 12)); err == nil || !strings.Contains(err.Error(), "does not belong") {
			t.Fatalf("mixed-cell error = %v", err)
		}
	})

	t.Run("invalid metrics", func(t *testing.T) {
		stream := newStream(t)
		invalid := day("2024-01-01", 36.72, -4.42, 24, -1, 0, 15, 20, 12)
		if err := stream.Add(invalid); err == nil || !strings.Contains(err.Error(), "day 2024-01-01") {
			t.Fatalf("invalid-metrics error = %v", err)
		}
	})
}

func TestStreamingAccumulatorRequiresCompleteWindowAndFinalizesOnce(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	stream, err := NewStreamingAccumulator(window)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Finalize(); err == nil || !strings.Contains(err.Error(), "expected 2024-01-01") {
		t.Fatalf("empty finalize error = %v", err)
	}
	for date := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC); date.Year() == 2024; date = date.AddDate(0, 0, 1) {
		if err := stream.Add(variedDay(date)); err != nil {
			t.Fatalf("add %s: %v", formatDate(date), err)
		}
	}
	result, err := stream.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if result[time.February-1].Days != 29 || result[time.February-1].ExpectedDays != 29 {
		t.Fatalf("leap February = %+v", result[time.February-1])
	}
	if _, err := stream.Finalize(); err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Fatalf("second finalize error = %v", err)
	}
	if err := stream.Add(day("2025-01-01", 36.72, -4.42, 24, 0, 0, 15, 20, 12)); err == nil || !strings.Contains(err.Error(), "already finalized") {
		t.Fatalf("post-finalize add error = %v", err)
	}
}

func TestStreamingAccumulatorRejectsDayAfterReferenceWindow(t *testing.T) {
	window := ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	stream, err := NewStreamingAccumulator(window)
	if err != nil {
		t.Fatal(err)
	}
	for date := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC); date.Year() == 2024; date = date.AddDate(0, 0, 1) {
		if err := stream.Add(variedDay(date)); err != nil {
			t.Fatalf("add %s: %v", formatDate(date), err)
		}
	}
	err = stream.Add(day("2025-01-01", 36.72, -4.42, 24, 0, 0, 15, 20, 12))
	if err == nil || !strings.Contains(err.Error(), "outside reference window") {
		t.Fatalf("post-window add error = %v", err)
	}
	if _, finalErr := stream.Finalize(); finalErr == nil || finalErr.Error() != err.Error() {
		t.Fatalf("poisoned finalize error = %v; want %v", finalErr, err)
	}
}

func TestStreamingHistogramMedianHandlesZeroAndEvenCounts(t *testing.T) {
	counts := [101]int{}
	counts[0] = 1
	counts[1] = 1
	if got := histogramMedian(counts, 2); got != 1 {
		t.Fatalf("median [0,1] = %d; want half-up 1", got)
	}
	counts = [101]int{}
	counts[0] = 2
	counts[100] = 1
	if got := histogramMedian(counts, 3); got != 0 {
		t.Fatalf("median [0,0,100] = %d; want 0", got)
	}
}

func variedDay(date time.Time) AgERA5Day {
	result := day(formatDate(date), 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	switch {
	case date.Day() >= 11 && date.Day() <= 13:
		// Exercise the three-consecutive-poor-day persistence rule inside
		// every calendar month and its reset between years.
		result = day(formatDate(date), 36.72, -4.42, 7, 5, .8, 2, 95, 30)
	case date.Day()%7 == 0:
		result = day(formatDate(date), 36.72, -4.42, 30, 0, 0, 17, 15, 12)
		result.DewPointC = 24
	case date.Day()%5 == 0:
		result = day(formatDate(date), 36.72, -4.42, 18, 1, .2, 9, 55, 20)
	}
	return result
}
