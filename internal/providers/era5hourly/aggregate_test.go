package era5hourly

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

type sliceSource struct {
	frames []Frame
	index  int
}

func (source *sliceSource) Next() (Frame, error) {
	if source.index == len(source.frames) {
		return Frame{}, io.EOF
	}
	frame := source.frames[source.index]
	source.index++
	return frame, nil
}

type recordingSink struct {
	addErrorAt  int
	commitErr   error
	abortErr    error
	transaction *recordingTransaction
}

type recordingTransaction struct {
	owner              *recordingSink
	pending, visible   []DailyMetrics
	adds               int
	committed, aborted bool
}

func (sink *recordingSink) BeginMonth(_ context.Context, _ int, _ time.Month, _ []Cell) (MonthTransaction, error) {
	sink.transaction = &recordingTransaction{owner: sink}
	return sink.transaction, nil
}

func (transaction *recordingTransaction) Add(day DailyMetrics) error {
	transaction.adds++
	if transaction.owner.addErrorAt > 0 && transaction.adds == transaction.owner.addErrorAt {
		return errors.New("injected add failure")
	}
	transaction.pending = append(transaction.pending, day)
	return nil
}

func (transaction *recordingTransaction) Commit() error {
	if transaction.owner.commitErr != nil {
		return transaction.owner.commitErr
	}
	transaction.visible = append([]DailyMetrics(nil), transaction.pending...)
	transaction.pending = nil
	transaction.committed = true
	return nil
}

func (transaction *recordingTransaction) Abort() error {
	transaction.pending = nil
	transaction.aborted = true
	return transaction.owner.abortErr
}

func TestAggregateMonthUsesExactFractionalCivilWindowAndHourlyPhysics(t *testing.T) {
	cells := []Cell{testCell(t, 0, 27.7, 85.3, "Asia/Kathmandu")}
	first, last, err := RequiredEnvelope(2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(time.Date(2025, 11, 1, 4, 0, 0, 0, time.UTC)) ||
		!last.Equal(time.Date(2025, 11, 30, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("envelope = %s..%s", first, last)
	}
	frames := syntheticFrames(first, last, 1, func(validTime time.Time, _ int) [FieldCount]float32 {
		hour := float32(validTime.Hour())
		return [FieldCount]float32{
			hour + 273.15,
			hour - 10 + 273.15,
			hour * .0001,
			hour * 100_000,
			hour / 100,
			hour,
			0,
		}
	})
	sink := &recordingSink{}
	stats, err := AggregateMonth(context.Background(), &sliceSource{frames: frames}, 2025, time.November, cells, sink)
	if err != nil {
		t.Fatal(err)
	}
	days := sink.transaction.visible
	if stats.Frames != len(frames) || stats.Days != 30 || len(days) != 30 || !sink.transaction.committed || sink.transaction.aborted {
		t.Fatalf("stats=%+v days=%d transaction=%+v", stats, len(days), sink.transaction)
	}
	day := days[0].Metrics
	assertClose(t, day.ComfortableTempC, 9.25)
	assertClose(t, day.MinTempC, 4.25)
	assertClose(t, day.MaxTempC, 14.25)
	assertClose(t, day.RainMM, 9.75)
	assertClose(t, day.WetTimeFraction.Value, 1)
	assertClose(t, day.SolarEnergyMJm2.Value, 9.75)
	assertClose(t, day.CloudCoverPercent.Value, 9.25)
	assertClose(t, day.WindSpeedKPH, 33.3)
	assertClose(t, day.DewPointC.Value, -.75)
	if day.RelativeHumidityPercent.Value <= 45 || day.RelativeHumidityPercent.Value >= 55 {
		t.Fatalf("relative humidity = %.6f, want synchronous hourly mean near 50%%", day.RelativeHumidityPercent.Value)
	}
	if _, _, err := weather.ScoreProfilesChecked(day); err != nil {
		t.Fatalf("canonical scorer rejected ERA5 metrics: %v", err)
	}
}

func TestAggregateMonthHandlesDSTAndMultipleCivilZones(t *testing.T) {
	cells := []Cell{
		testCell(t, 0, 52.37, 4.9, "Europe/Amsterdam"),
		testCell(t, 1, -43.95, -176.56, "Pacific/Chatham"),
	}
	first, last, err := RequiredEnvelope(2025, time.March, cells)
	if err != nil {
		t.Fatal(err)
	}
	frames := syntheticFrames(first, last, 2, func(_ time.Time, _ int) [FieldCount]float32 {
		return [FieldCount]float32{297.15, 287.15, 0, 1_000_000, .1, 0, 0}
	})
	sink := &recordingSink{}
	stats, err := AggregateMonth(context.Background(), &sliceSource{frames: frames}, 2025, time.March, cells, sink)
	if err != nil {
		t.Fatal(err)
	}
	counts := [2]int{}
	for _, day := range sink.transaction.visible {
		counts[day.CellIndex]++
		assertClose(t, day.Metrics.SolarEnergyMJm2.Value, 10)
	}
	if counts != [2]int{31, 31} || stats.Days != 62 {
		t.Fatalf("counts=%v stats=%+v", counts, stats)
	}
}

func TestAggregateMonthOmitsSkippedCivilDates(t *testing.T) {
	cells := []Cell{
		testCell(t, 0, -13.83, -171.75, "Pacific/Apia"),
		testCell(t, 1, -9.38, -171.25, "Pacific/Fakaofo"),
	}
	first, last, err := RequiredEnvelope(2011, time.December, cells)
	if err != nil {
		t.Fatal(err)
	}
	frames := syntheticFrames(first, last, 2, func(_ time.Time, _ int) [FieldCount]float32 {
		return [FieldCount]float32{297.15, 287.15, 0, 1_000_000, .1, 0, 0}
	})
	sink := &recordingSink{}
	stats, err := AggregateMonth(context.Background(), &sliceSource{frames: frames}, 2011, time.December, cells, sink)
	if err != nil {
		t.Fatal(err)
	}
	counts := [2]int{}
	for _, day := range sink.transaction.visible {
		counts[day.CellIndex]++
		if day.Date.Equal(time.Date(2011, 12, 30, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("invented skipped civil date for cell %d", day.CellIndex)
		}
	}
	if counts != [2]int{30, 30} || stats.Days != 60 {
		t.Fatalf("counts=%v stats=%+v", counts, stats)
	}
}

func TestSourceCoordinatesAreBoundToThePinnedWorldwideGrid(t *testing.T) {
	valid := []Cell{
		{SourceIndex: 151*GridColumns + 20, Latitude: 52.25, Longitude: 5},
		{SourceIndex: 536*GridColumns + 734, Latitude: -44, Longitude: -176.5},
	}
	if err := validateSourceCoordinates(valid); err != nil {
		t.Fatal(err)
	}
	invalid := append([]Cell(nil), valid...)
	invalid[1].Longitude = 183.5
	if err := validateSourceCoordinates(invalid); err == nil {
		t.Fatal("coordinate/source-index mismatch was accepted")
	}
}

func TestTimezoneContractRejectsIdentityDrift(t *testing.T) {
	cell := testCell(t, 0, 0, 0, "UTC")
	cell.TimezoneFingerprint = "changed"
	if _, _, err := RequiredEnvelope(2025, time.November, []Cell{cell}); err == nil {
		t.Fatal("timezone contract drift was accepted")
	}
}

func TestAggregateMonthClampsOnlyTinySynchronousDewPointCrossings(t *testing.T) {
	cells := []Cell{testCell(t, 0, 0, 0, "UTC")}
	first, last, err := RequiredEnvelope(2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	run := func(excess float32) (AggregationStats, error) {
		frames := syntheticFrames(first, last, 1, func(_ time.Time, _ int) [FieldCount]float32 {
			return [FieldCount]float32{300, 300 + excess, 0, 1_000_000, .1, 0, 0}
		})
		return AggregateMonth(context.Background(), &sliceSource{frames: frames}, 2025, time.November, cells, &recordingSink{})
	}
	stats, err := run(.005)
	if err != nil {
		t.Fatal(err)
	}
	if stats.HumiditySaturationClampedHours == 0 || stats.MaximumDewPointExcessC <= 0 || stats.MaximumDewPointExcessC > maximumDewPointExcess {
		t.Fatalf("repair evidence = %+v", stats)
	}
	if _, err := run(.02); err == nil {
		t.Fatal("material dew-point crossing was accepted")
	}
}

func TestAggregateMonthAbortsEveryPartialOrInvalidMonth(t *testing.T) {
	cells := []Cell{testCell(t, 0, 0, 0, "UTC")}
	first, last, err := RequiredEnvelope(2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	good := syntheticFrames(first, last, 1, func(_ time.Time, _ int) [FieldCount]float32 {
		return [FieldCount]float32{295, 285, 0, 1_000_000, .1, 0, 0}
	})
	tests := []struct {
		name   string
		frames []Frame
		sink   *recordingSink
	}{
		{"missing precursor", good[1:], &recordingSink{}},
		{"missing final", good[:len(good)-1], &recordingSink{}},
		{"time gap", append(append([]Frame{}, good[:5]...), good[6:]...), &recordingSink{}},
		{"negative rain", mutateFrame(good, 10, func(frame *Frame) { frame.RainM[0] = -.1 }), &recordingSink{}},
		{"bad cloud", mutateFrame(good, 10, func(frame *Frame) { frame.Cloud[0] = 2 }), &recordingSink{}},
		{"hot corruption", mutateFrame(good, 10, func(frame *Frame) { frame.TemperatureK[0] = 500 }), &recordingSink{}},
		{"cold dew corruption", mutateFrame(good, 10, func(frame *Frame) { frame.DewPointK[0] = 100 }), &recordingSink{}},
		{"wind corruption", mutateFrame(good, 10, func(frame *Frame) { frame.WindUMPS[0] = 100 }), &recordingSink{}},
		{"solar corruption", mutateFrame(good, 10, func(frame *Frame) { frame.SolarJM2[0] = 50_000_000 }), &recordingSink{}},
		{"late add failure", good, &recordingSink{addErrorAt: 20}},
		{"commit failure", good, &recordingSink{commitErr: errors.New("injected commit failure")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AggregateMonth(context.Background(), &sliceSource{frames: test.frames}, 2025, time.November, cells, test.sink)
			if err == nil {
				t.Fatal("invalid month was accepted")
			}
			transaction := test.sink.transaction
			if transaction == nil || !transaction.aborted || transaction.committed || len(transaction.visible) != 0 || len(transaction.pending) != 0 {
				t.Fatalf("partial transaction survived: %+v", transaction)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	sink := &recordingSink{}
	if _, err := AggregateMonth(cancelled, &sliceSource{frames: good}, 2025, time.November, cells, sink); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if sink.transaction == nil || !sink.transaction.aborted {
		t.Fatal("cancelled transaction was not aborted")
	}
}

func testCell(t *testing.T, sourceIndex int, latitude, longitude float64, timezoneID string) Cell {
	t.Helper()
	fingerprint, err := TimezoneFingerprint(timezoneID)
	if err != nil {
		t.Fatal(err)
	}
	return Cell{
		SourceIndex: sourceIndex, Latitude: latitude, Longitude: longitude,
		TimezoneID: timezoneID, TimezoneFingerprint: fingerprint,
	}
}

func syntheticFrames(first, last time.Time, cells int, values func(time.Time, int) [FieldCount]float32) []Frame {
	var result []Frame
	for current := first; !current.After(last); current = current.Add(time.Hour) {
		fields := [FieldCount][]float32{}
		for field := range fields {
			fields[field] = make([]float32, cells)
		}
		for cell := 0; cell < cells; cell++ {
			currentValues := values(current, cell)
			for field := range fields {
				fields[field][cell] = currentValues[field]
			}
		}
		result = append(result, Frame{
			ValidTime: current, Rows: 1, Columns: cells,
			TemperatureK: fields[0], DewPointK: fields[1], RainM: fields[2],
			SolarJM2: fields[3], Cloud: fields[4], WindUMPS: fields[5], WindVMPS: fields[6],
		})
	}
	return result
}

func mutateFrame(source []Frame, index int, mutate func(*Frame)) []Frame {
	result := append([]Frame(nil), source...)
	frame := result[index]
	for fieldIndex, field := range frame.fields() {
		copied := append([]float32(nil), field...)
		switch fieldIndex {
		case 0:
			frame.TemperatureK = copied
		case 1:
			frame.DewPointK = copied
		case 2:
			frame.RainM = copied
		case 3:
			frame.SolarJM2 = copied
		case 4:
			frame.Cloud = copied
		case 5:
			frame.WindUMPS = copied
		case 6:
			frame.WindVMPS = copied
		}
	}
	mutate(&frame)
	result[index] = frame
	return result
}

func assertClose(t *testing.T, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-4 {
		t.Fatalf("got %.9g, want %.9g", actual, expected)
	}
}
