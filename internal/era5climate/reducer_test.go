package era5climate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/weather"
)

func TestReduceMonthMatchesCanonicalScoringAndBoundsBatches(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2023, LastYear: 2024}
	cells := testCells(t, "UTC", "Europe/Amsterdam", "Asia/Singapore")
	shards, paths := writeShards(t, window, time.February, cells, variedMetrics)
	defer closeShards(shards)

	var batches [][2]int
	sink := &recordingSink{}
	err := reduceMonthWindow(t.Context(), window, Config{Month: time.February, BatchCells: 2}, shards, sink, func(first, count int) {
		batches = append(batches, [2]int{first, count})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, [][2]int{{0, 2}, {2, 1}}) {
		t.Fatalf("batches = %v; want [[0 2] [2 1]]", batches)
	}
	if sink.commits != 1 || sink.aborts != 0 {
		t.Fatalf("transaction commits=%d aborts=%d; want 1/0", sink.commits, sink.aborts)
	}
	if len(sink.results) != len(cells) {
		t.Fatalf("results = %d; want %d", len(sink.results), len(cells))
	}
	for cellIndex, result := range sink.results {
		if result.CellIndex != cellIndex || result.Latitude != cells[cellIndex].Latitude ||
			result.Longitude != cells[cellIndex].Longitude || result.Days != 57 {
			t.Fatalf("result %d identity = %+v", cellIndex, result)
		}
		observations, expected := directObservations(t, paths, window, time.February, cells, cellIndex)
		distribution, err := climate.AggregateObservations(observations, expected)
		if err != nil {
			t.Fatal(err)
		}
		want, err := distribution.DeliveryRecord()
		if err != nil {
			t.Fatal(err)
		}
		if result.Distribution != want {
			t.Fatalf("cell %d distribution differs from direct canonical scoring:\n got %+v\nwant %+v", cellIndex, result.Distribution, want)
		}
		if result.Physical.ClearSkyDayPercent != result.Distribution.BrightGoodPercent {
			t.Fatalf("cell %d clear-sky-day percent %d differs from canonical clear-sky probability %d", cellIndex, result.Physical.ClearSkyDayPercent, result.Distribution.BrightGoodPercent)
		}
		wantPhysical, err := summarizePhysical(observations, want.BrightGoodPercent)
		if err != nil {
			t.Fatal(err)
		}
		if result.Physical != wantPhysical {
			t.Fatalf("cell %d physical summary:\n got %+v\nwant %+v", cellIndex, result.Physical, wantPhysical)
		}
	}
}

func TestReduceMonthPublicContractRequiresAndCommitsCompleteDefaultWindow(t *testing.T) {
	partialWindow := climate.ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	cells := testCells(t, "UTC")
	partial, _ := writeShards(t, partialWindow, time.November, cells, variedMetrics)
	defer closeShards(partial)
	rejected := &recordingSink{}
	err := ReduceMonth(t.Context(), Config{Month: time.November}, partial, rejected)
	if err == nil || !strings.Contains(err.Error(), "want 20 for 2006–2025") ||
		rejected.commits != 0 || rejected.aborts != 1 {
		t.Fatalf("partial public reduction: commits=%d aborts=%d err=%v", rejected.commits, rejected.aborts, err)
	}

	window := climate.DefaultReferenceWindow()
	shards, _ := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)
	accepted := &recordingSink{}
	if err := ReduceMonth(t.Context(), Config{Month: time.November}, shards, accepted); err != nil {
		t.Fatal(err)
	}
	days := 0
	if len(accepted.results) == 1 {
		days = accepted.results[0].Days
	}
	if len(accepted.results) != 1 || days != 600 ||
		accepted.commits != 1 || accepted.aborts != 0 {
		t.Fatalf("complete public reduction: results=%d days=%d commits=%d aborts=%d",
			len(accepted.results), days, accepted.commits, accepted.aborts)
	}
}

func TestReduceMonthPreservesLeapDaysAndNonexistentCivilDates(t *testing.T) {
	t.Run("leap day", func(t *testing.T) {
		window := climate.ReferenceWindow{FirstYear: 2023, LastYear: 2024}
		cells := testCells(t, "UTC")
		shards, _ := writeShards(t, window, time.February, cells, variedMetrics)
		defer closeShards(shards)
		sink := &recordingSink{}
		if err := reduceMonthWindow(t.Context(), window, Config{Month: time.February}, shards, sink, nil); err != nil {
			t.Fatal(err)
		}
		result := sink.results[0]
		if result.Days != 57 {
			t.Fatalf("February days = %d; want 57 including 2024-02-29", result.Days)
		}
	})

	t.Run("Apia skipped 2011-12-30", func(t *testing.T) {
		window := climate.ReferenceWindow{FirstYear: 2011, LastYear: 2011}
		cells := testCells(t, "UTC", "Pacific/Apia")
		shards, _ := writeShards(t, window, time.December, cells, variedMetrics)
		defer closeShards(shards)
		sink := &recordingSink{}
		if err := reduceMonthWindow(t.Context(), window, Config{Month: time.December, BatchCells: 1}, shards, sink, nil); err != nil {
			t.Fatal(err)
		}
		results := sink.results
		if len(results) != 2 || results[0].Days != 31 || results[1].Days != 30 {
			t.Fatalf("UTC/Apia day counts = %v; want 31/30", []int{results[0].Days, results[1].Days})
		}
		if totalShares(results[1]) != 1000 {
			t.Fatalf("Apia rating shares total = %d; want 1000", totalShares(results[1]))
		}
	})
}

func TestReduceMonthRejectsYearAndCellPlanAmbiguity(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2023, LastYear: 2024}
	cells := testCells(t, "UTC", "Europe/Amsterdam")
	shards, _ := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)
	reduce := func(candidates []*era5hourly.DailyShard) error {
		return reduceMonthWindow(t.Context(), window, Config{Month: time.November}, candidates, &recordingSink{}, nil)
	}
	if err := reduce(shards[:1]); err == nil || !strings.Contains(err.Error(), "year shards") {
		t.Fatalf("missing year error = %v", err)
	}
	if err := reduce([]*era5hourly.DailyShard{shards[0], shards[0]}); err == nil || !strings.Contains(err.Error(), "unique ordered year") {
		t.Fatalf("duplicate year error = %v", err)
	}
	if err := reduce([]*era5hourly.DailyShard{shards[1], shards[0]}); err == nil || !strings.Contains(err.Error(), "unique ordered year") {
		t.Fatalf("reordered year error = %v", err)
	}

	reordered := []era5hourly.Cell{cells[1], cells[0]}
	other, _ := writeShards(t, climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}, time.November, reordered, variedMetrics)
	defer closeShards(other)
	if err := reduce([]*era5hourly.DailyShard{shards[0], other[0]}); err == nil || !strings.Contains(err.Error(), "different ordered cell plan") {
		t.Fatalf("reordered cell error = %v", err)
	}
}

func TestReduceMonthRejectsPostOpenCorruption(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	cells := testCells(t, "UTC")
	shards, paths := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes := int64(30 * era5hourly.DailyShardRecordBytes)
	payloadAt := info.Size() - payloadBytes
	file, err := os.OpenFile(paths[0], os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reserved [4]byte
	binary.LittleEndian.PutUint32(reserved[:], 1)
	if _, err := file.WriteAt(reserved[:], payloadAt+36); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	err = reduceMonthWindow(t.Context(), window, Config{Month: time.November}, shards, sink, nil)
	if err == nil || !strings.Contains(err.Error(), "reserved record bytes") {
		t.Fatalf("corruption error = %v", err)
	}
	if sink.commits != 0 || sink.aborts != 1 {
		t.Fatalf("corrupt transaction commits=%d aborts=%d; want 0/1", sink.commits, sink.aborts)
	}
}

func TestReduceMonthRevalidatesValidPayloadAfterAllResultsAreStaged(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	cells := testCells(t, "UTC", "Europe/Amsterdam")
	shards, paths := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)
	sink := &recordingSink{}
	sink.onAdd = func() {
		if sink.adds != len(cells) {
			return
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		payloadAt := info.Size() - int64(len(cells)*30*era5hourly.DailyShardRecordBytes)
		file, err := os.OpenFile(paths[0], os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		var changed [8]byte
		binary.LittleEndian.PutUint64(changed[:], math.Float64bits(25))
		_, writeErr := file.WriteAt(changed[:], payloadAt)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			t.Fatal(err)
		}
	}
	err := reduceMonthWindow(t.Context(), window, Config{Month: time.November}, shards, sink, nil)
	if err == nil || !strings.Contains(err.Error(), "payload changed after validation") {
		t.Fatalf("valid-to-valid mutation error = %v", err)
	}
	if len(sink.results) != len(cells) || sink.commits != 0 || sink.aborts != 1 {
		t.Fatalf("mutated transaction staged=%d commits=%d aborts=%d; want %d/0/1",
			len(sink.results), sink.commits, sink.aborts, len(cells))
	}
}

func TestReduceMonthRejectsPayloadChangedOnlyWhileItWasConsumed(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	cells := testCells(t, "UTC", "Europe/Amsterdam")
	shards, paths := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)

	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	payloadAt := info.Size() - int64(len(cells)*30*era5hourly.DailyShardRecordBytes)
	secondCellAt := payloadAt + int64(30*era5hourly.DailyShardRecordBytes)
	file, err := os.OpenFile(paths[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	original := make([]byte, era5hourly.DailyShardRecordBytes)
	if _, err := file.ReadAt(original, secondCellAt); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), original...)
	binary.LittleEndian.PutUint64(changed[:8], math.Float64bits(25))

	sink := &recordingSink{}
	sink.onAdd = func() {
		var writeErr error
		switch sink.adds {
		case 1:
			_, writeErr = file.WriteAt(changed, secondCellAt)
		case 2:
			_, writeErr = file.WriteAt(original, secondCellAt)
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	err = reduceMonthWindow(t.Context(), window, Config{Month: time.November, BatchCells: 1}, shards, sink, nil)
	if err == nil || !strings.Contains(err.Error(), "consumed payload differs") {
		t.Fatalf("transient mutation error = %v", err)
	}
	if len(sink.results) != len(cells) || sink.commits != 0 || sink.aborts != 1 {
		t.Fatalf("transient mutation staged=%d commits=%d aborts=%d; want %d/0/1",
			len(sink.results), sink.commits, sink.aborts, len(cells))
	}
}

func TestReduceMonthHonoursCancellationAndSinkFailure(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2025, LastYear: 2025}
	cells := testCells(t, "UTC", "Europe/Amsterdam")
	shards, _ := writeShards(t, window, time.November, cells, variedMetrics)
	defer closeShards(shards)

	ctx, cancel := context.WithCancel(context.Background())
	sink := &recordingSink{}
	sink.onAdd = func() {
		cancel()
	}
	err := reduceMonthWindow(ctx, window, Config{Month: time.November, BatchCells: 1}, shards, sink, nil)
	if !errors.Is(err, context.Canceled) || len(sink.results) != 1 || sink.commits != 0 || sink.aborts != 1 {
		t.Fatalf("cancel result: results=%d commits=%d aborts=%d err=%v", len(sink.results), sink.commits, sink.aborts, err)
	}

	sentinel := errors.New("stop")
	failing := &recordingSink{addErrAt: 2, addErr: sentinel}
	err = reduceMonthWindow(t.Context(), window, Config{Month: time.November}, shards, failing, nil)
	if !errors.Is(err, sentinel) || failing.commits != 0 || failing.aborts != 1 {
		t.Fatalf("sink failure: commits=%d aborts=%d err=%v", failing.commits, failing.aborts, err)
	}

	failedCommit := &recordingSink{commitErr: sentinel}
	err = reduceMonthWindow(t.Context(), window, Config{Month: time.November}, shards, failedCommit, nil)
	if !errors.Is(err, sentinel) || failedCommit.commits != 1 || failedCommit.aborts != 1 {
		t.Fatalf("commit failure: commits=%d aborts=%d err=%v", failedCommit.commits, failedCommit.aborts, err)
	}
}

func TestPhysicalQuantilesUseInclusiveLinearInterpolation(t *testing.T) {
	values := []float64{0, 10, 20, 30, 40}
	if got := quantile(values, .10); got != 4 {
		t.Fatalf("p10 = %v; want 4", got)
	}
	if got := quantile(values, .50); got != 20 {
		t.Fatalf("p50 = %v; want 20", got)
	}
	if got := quantile(values, .90); got != 36 {
		t.Fatalf("p90 = %v; want 36", got)
	}
	if !reflect.DeepEqual(values, []float64{0, 10, 20, 30, 40}) {
		t.Fatal("quantile mutated source values")
	}
}

func TestPhysicalSummaryCountsMuggyDaytimeMeansAt18C(t *testing.T) {
	dewPoints := []float64{17.9, 18, 21, 24}
	observations := make([]climate.DailyObservation, len(dewPoints))
	for index, dewPoint := range dewPoints {
		metrics := variedMetrics(2025, time.July, index+1, 0)
		metrics.DewPointC.Value = dewPoint
		observations[index] = climate.DailyObservation{
			Date:    time.Date(2025, time.July, index+1, 0, 0, 0, 0, time.UTC),
			Metrics: metrics,
		}
	}
	summary, err := summarizePhysical(observations, 50)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MuggyDayPercent != 75 {
		t.Fatalf("muggy daytime means = %d%%; want 75%% at dew point >=18°C", summary.MuggyDayPercent)
	}
	if summary.RelativeHumidityP50Percent != 60 {
		t.Fatalf("median daytime relative humidity = %v%%; want 60%%", summary.RelativeHumidityP50Percent)
	}
	if summary.ClearSkyDayPercent != 50 {
		t.Fatalf("clear-sky daily share = %d%%; want supplied canonical 50%%", summary.ClearSkyDayPercent)
	}
}

func directObservations(
	t *testing.T,
	paths []string,
	window climate.ReferenceWindow,
	month time.Month,
	cells []era5hourly.Cell,
	cellIndex int,
) ([]climate.DailyObservation, []time.Time) {
	t.Helper()
	var observations []climate.DailyObservation
	var expected []time.Time
	for index, path := range paths {
		year := window.FirstYear + index
		shard, err := era5hourly.OpenDailyShard(path, year, month, cells)
		if err != nil {
			t.Fatal(err)
		}
		err = shard.WalkCellRange(cellIndex, 1, sha256.New(), func(day era5hourly.DailyMetrics, available bool) error {
			if available {
				expected = append(expected, day.Date)
				observations = append(observations, climate.DailyObservation{
					Date: day.Date, Latitude: day.Latitude, Longitude: day.Longitude, Metrics: day.Metrics,
				})
			}
			return nil
		})
		closeErr := shard.Close()
		if err != nil || closeErr != nil {
			t.Fatal(errors.Join(err, closeErr))
		}
	}
	return observations, expected
}

func writeShards(
	t *testing.T,
	window climate.ReferenceWindow,
	month time.Month,
	cells []era5hourly.Cell,
	metrics func(year int, month time.Month, day, cell int) weather.DailyMetrics,
) ([]*era5hourly.DailyShard, []string) {
	t.Helper()
	directory := t.TempDir()
	var shards []*era5hourly.DailyShard
	var paths []string
	for year := window.FirstYear; year <= window.LastYear; year++ {
		path := filepath.Join(directory, time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")+".ned")
		state, err := era5hourly.PrepareDailyShard(path, year, month, cells)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := state.Sink.BeginMonth(t.Context(), year, month, cells)
		if err != nil {
			t.Fatal(err)
		}
		for cellIndex, cell := range cells {
			for day := 1; day <= time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day(); day++ {
				if !civilDateExists(year, month, day, cell.TimezoneID) {
					continue
				}
				err := transaction.Add(era5hourly.DailyMetrics{
					CellIndex: cellIndex,
					Date:      time.Date(year, month, day, 0, 0, 0, 0, time.UTC),
					Latitude:  cell.Latitude, Longitude: cell.Longitude,
					Metrics: metrics(year, month, day, cellIndex),
				})
				if err != nil {
					t.Fatalf("add %d-%02d-%02d cell %d: %v", year, month, day, cellIndex, err)
				}
			}
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		shard, err := era5hourly.OpenDailyShard(path, year, month, cells)
		if err != nil {
			t.Fatal(err)
		}
		shards = append(shards, shard)
		paths = append(paths, path)
	}
	return shards, paths
}

func testCells(t *testing.T, timezoneIDs ...string) []era5hourly.Cell {
	t.Helper()
	cells := make([]era5hourly.Cell, len(timezoneIDs))
	for index, timezoneID := range timezoneIDs {
		fingerprint, err := era5hourly.TimezoneFingerprint(timezoneID)
		if err != nil {
			t.Fatal(err)
		}
		cells[index] = era5hourly.Cell{
			SourceIndex: index,
			Latitude:    90,
			Longitude:   float64(index) * .25,
			TimezoneID:  timezoneID, TimezoneFingerprint: fingerprint,
		}
	}
	return cells
}

func civilDateExists(year int, month time.Month, day int, timezoneID string) bool {
	location, err := time.LoadLocation(timezoneID)
	if err != nil {
		return false
	}
	local := time.Date(year, month, day, 12, 0, 0, 0, location)
	gotYear, gotMonth, gotDay := local.Date()
	return gotYear == year && gotMonth == month && gotDay == day
}

func variedMetrics(year int, _ time.Month, day, cell int) weather.DailyMetrics {
	offset := float64((year - 2000 + day + cell*3) % 17)
	temperature := 14 + offset
	rain := 0.0
	if day%4 == 0 {
		rain = float64(day%9) / 2
	}
	cloud := float64((day*11 + cell*17) % 101)
	dew := math.Min(temperature-1, 8+float64((day+cell)%16))
	return weather.DailyMetrics{
		ComfortableTempC: temperature,
		MinTempC:         temperature - 4,
		MaxTempC:         temperature + 4,
		RainMM:           rain,
		WetTimeFraction:  weather.OptionalFloat{Value: float64(day%5) / 10, Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: 12, Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: cloud, Available: true,
		},
		WindSpeedKPH: 5 + float64((day+cell)%20),
		DewPointC: weather.OptionalFloat{
			Value: dew, Available: true,
		},
		RelativeHumidityPercent: weather.OptionalFloat{
			Value: 60, Available: true,
		},
		NativeResolutionKM: era5hourly.NativeDetailKM,
		Valid:              true,
	}
}

func totalShares(result CellResult) int {
	shares := result.Distribution.Nice.RatingShares
	return int(shares.Awful + shares.Poor + shares.Mixed + shares.Good + shares.VeryGood + shares.Excellent)
}

type recordingSink struct {
	results   []CellResult
	adds      int
	commits   int
	aborts    int
	addErrAt  int
	addErr    error
	commitErr error
	abortErr  error
	onAdd     func()
}

func (sink *recordingSink) Add(result CellResult) error {
	sink.adds++
	if sink.addErrAt > 0 && sink.adds == sink.addErrAt {
		return sink.addErr
	}
	sink.results = append(sink.results, result)
	if sink.onAdd != nil {
		sink.onAdd()
	}
	return nil
}

func (sink *recordingSink) Commit() error {
	sink.commits++
	return sink.commitErr
}

func (sink *recordingSink) Abort() error {
	sink.aborts++
	return sink.abortErr
}

func closeShards(shards []*era5hourly.DailyShard) {
	for _, shard := range shards {
		_ = shard.Close()
	}
}
