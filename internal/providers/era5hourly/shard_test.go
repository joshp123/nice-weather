package era5hourly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

func TestDailyShardRoundTripAndByteLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2025-11.ned")
	cells := []Cell{dailyShardTestCell(t, 0, "UTC")}
	metrics := shardTestMetrics()
	metrics.ComfortableTempC = 23.5
	metrics.MinTempC = 17.25
	metrics.MaxTempC = 28.75
	metrics.RainMM = 1.5
	metrics.WetTimeFraction.Value = .35
	metrics.SolarEnergyMJm2.Value = 12.5
	metrics.CloudCoverPercent.Value = 42.25
	metrics.WindSpeedKPH = 14.5
	metrics.DewPointC.Value = 11.75
	metrics.RelativeHumidityPercent.Value = 51.5
	writeDailyShardFixture(t, path, 2025, time.November, cells, metrics)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := newDailyShardIdentity(2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:8]) != dailyShardMagic ||
		binary.LittleEndian.Uint16(raw[8:10]) != dailyShardSchemaVersion ||
		binary.LittleEndian.Uint16(raw[10:12]) != dailyShardHeaderBytes ||
		binary.LittleEndian.Uint16(raw[12:14]) != DailyShardRecordBytes ||
		binary.LittleEndian.Uint16(raw[14:16]) != SchemaVersion ||
		binary.LittleEndian.Uint32(raw[16:20]) != 2025 ||
		binary.LittleEndian.Uint16(raw[20:22]) != uint16(time.November) ||
		binary.LittleEndian.Uint16(raw[22:24]) != 30 ||
		binary.LittleEndian.Uint64(raw[24:32]) != 1 ||
		binary.LittleEndian.Uint64(raw[32:40]) != 30 ||
		binary.LittleEndian.Uint64(raw[40:48]) != uint64(len(identity.expected)) ||
		binary.LittleEndian.Uint64(raw[48:56]) != 30*DailyShardRecordBytes {
		t.Fatalf("unexpected ERA5 daily shard header: %x", raw[:56])
	}
	if string(raw[56:76]) != SnapshotID ||
		!bytes.Equal(raw[76:108], sourceContractSHA256[:]) ||
		!bytes.Equal(raw[140:172], identity.planSHA[:]) {
		t.Fatal("header did not bind the exact source and cell plan")
	}
	recordContractSHA := sha256.Sum256([]byte(dailyShardRecordContract))
	if !bytes.Equal(raw[236:268], recordContractSHA[:]) || !allZero(raw[268:dailyShardHeaderBytes]) {
		t.Fatal("header did not bind the record/derivation contract")
	}
	firstRecord := raw[dailyShardHeaderBytes+len(identity.expected):]
	doubleFields := []struct {
		offset int
		want   float64
	}{
		{0, 23.5}, {8, 17.25}, {16, 28.75}, {24, 1.5},
		{40, 12.5}, {48, 42.25}, {56, 14.5}, {64, 11.75}, {72, 51.5},
	}
	for _, field := range doubleFields {
		if got := math.Float64frombits(binary.LittleEndian.Uint64(firstRecord[field.offset:])); math.Float64bits(got) != math.Float64bits(field.want) {
			t.Errorf("record float64 at %d = %v, want %v", field.offset, got, field.want)
		}
	}
	if got := binary.LittleEndian.Uint32(firstRecord[32:36]); got != 12_600 {
		t.Fatalf("wet daytime seconds = %d, want 12600", got)
	}
	if !allZero(firstRecord[36:40]) {
		t.Fatal("reserved record bytes are not zero")
	}

	shard, err := OpenDailyShard(path, 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()
	day, available, err := shard.Read(0, 0)
	if err != nil || !available {
		t.Fatalf("read first day: available=%v err=%v", available, err)
	}
	if day.CellIndex != 0 || day.Date.Format(time.DateOnly) != "2025-11-01" ||
		day.Latitude != cells[0].Latitude || day.Longitude != cells[0].Longitude {
		t.Fatalf("decoded day identity = %+v", day)
	}
	if err := day.Metrics.Validate(); err != nil {
		t.Fatalf("decoded DailyMetrics did not validate: %v", err)
	}
	if !day.Metrics.Valid || day.Metrics.NativeResolutionKM != NativeDetailKM ||
		day.Metrics.GustKPH.Available || !day.Metrics.WetTimeFraction.Available ||
		day.Metrics.WetTimeFraction.Value != .35 ||
		!day.Metrics.SolarEnergyMJm2.Available || !day.Metrics.CloudCoverPercent.Available ||
		!day.Metrics.DewPointC.Available || !day.Metrics.RelativeHumidityPercent.Available {
		t.Fatalf("decoded evidence contract = %+v", day.Metrics)
	}
	if _, _, err := weather.ScoreProfilesChecked(day.Metrics); err != nil {
		t.Fatalf("decoded canonical score failed: %v", err)
	}
	for _, values := range [][2]float64{
		{day.Metrics.ComfortableTempC, metrics.ComfortableTempC},
		{day.Metrics.MinTempC, metrics.MinTempC},
		{day.Metrics.MaxTempC, metrics.MaxTempC},
		{day.Metrics.RainMM, metrics.RainMM},
		{day.Metrics.SolarEnergyMJm2.Value, metrics.SolarEnergyMJm2.Value},
		{day.Metrics.CloudCoverPercent.Value, metrics.CloudCoverPercent.Value},
		{day.Metrics.WindSpeedKPH, metrics.WindSpeedKPH},
		{day.Metrics.DewPointC.Value, metrics.DewPointC.Value},
		{day.Metrics.RelativeHumidityPercent.Value, metrics.RelativeHumidityPercent.Value},
	} {
		if math.Float64bits(values[0]) != math.Float64bits(values[1]) {
			t.Errorf("float64 roundtrip changed %.17g to %.17g", values[1], values[0])
		}
	}
}

func TestDailyShardTransactionRejectsBadRecordsAndMissingDays(t *testing.T) {
	cells := []Cell{dailyShardTestCell(t, 0, "UTC")}
	newTransaction := func(t *testing.T) MonthTransaction {
		t.Helper()
		state, err := PrepareDailyShard(filepath.Join(t.TempDir(), "month.ned"), 2025, time.November, cells)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := state.Sink.BeginMonth(t.Context(), 2025, time.November, cells)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = transaction.Abort() })
		return transaction
	}
	valid := DailyMetrics{
		CellIndex: 0,
		Date:      time.Date(2025, time.November, 1, 0, 0, 0, 0, time.UTC),
		Latitude:  cells[0].Latitude,
		Longitude: cells[0].Longitude,
		Metrics:   shardTestMetrics(),
	}

	transaction := newTransaction(t)
	if err := transaction.Add(valid); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add(valid); err == nil {
		t.Fatal("duplicate record was accepted")
	}
	if err := transaction.Commit(); err == nil {
		t.Fatal("incomplete month was committed")
	}

	tests := []struct {
		name string
		edit func(*DailyMetrics)
	}{
		{"cell index", func(day *DailyMetrics) { day.CellIndex = 1 }},
		{"date", func(day *DailyMetrics) { day.Date = day.Date.AddDate(0, 1, 0) }},
		{"non-midnight date", func(day *DailyMetrics) { day.Date = day.Date.Add(time.Hour) }},
		{"coordinate", func(day *DailyMetrics) { day.Longitude += .25 }},
		{"invalid metrics", func(day *DailyMetrics) { day.Metrics.Valid = false }},
		{"gust evidence", func(day *DailyMetrics) {
			day.Metrics.GustKPH = weather.OptionalFloat{Value: 12, Available: true}
		}},
		{"missing wet time", func(day *DailyMetrics) { day.Metrics.WetTimeFraction = weather.OptionalFloat{} }},
		{"wrong resolution", func(day *DailyMetrics) { day.Metrics.NativeResolutionKM = 28 }},
		{"fractional wet second", func(day *DailyMetrics) { day.Metrics.WetTimeFraction.Value = 1.0 / 7 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := valid
			test.edit(&day)
			if err := newTransaction(t).Add(day); err == nil {
				t.Fatal("bad record was accepted")
			}
		})
	}

	state, err := PrepareDailyShard(filepath.Join(t.TempDir(), "identity.ned"), 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]Cell(nil), cells...)
	changed[0] = dailyShardTestCell(t, 1, "UTC")
	if _, err := state.Sink.BeginMonth(t.Context(), 2025, time.November, changed); err == nil {
		t.Fatal("changed cell plan was accepted")
	}
}

func TestDailyShardAbortLeavesNoFinalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aborted.ned")
	cells := []Cell{dailyShardTestCell(t, 0, "UTC")}
	state, err := PrepareDailyShard(path, 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := state.Sink.BeginMonth(t.Context(), 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errorsIsNotExist(err) {
		t.Fatalf("aborted shard became visible: %v", err)
	}
}

func TestDailyShardRejectsCorruptionTruncationAndWrongIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "valid.ned")
	cells := []Cell{
		dailyShardTestCell(t, 0, "UTC"),
		dailyShardTestCell(t, 1, "Europe/Amsterdam"),
	}
	writeDailyShardFixture(t, path, 2025, time.November, cells, shardTestMetrics())
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := newDailyShardIdentity(2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	payloadAt := dailyShardHeaderBytes + len(identity.expected)
	tests := []struct {
		name string
		edit func([]byte) []byte
	}{
		{"source identity", func(data []byte) []byte { data[76] ^= 1; return data }},
		{"record contract", func(data []byte) []byte { data[236] ^= 1; return data }},
		{"payload checksum", func(data []byte) []byte { data[204] ^= 1; return data }},
		{"payload corruption", func(data []byte) []byte { data[payloadAt+1] ^= 1; return data }},
		{"reserved record bytes", func(data []byte) []byte { data[payloadAt+36] = 1; return data }},
		{"truncated", func(data []byte) []byte { return data[:len(data)-1] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corrupt := test.edit(append([]byte(nil), valid...))
			corruptPath := filepath.Join(root, test.name+".ned")
			if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if shard, err := OpenDailyShard(corruptPath, 2025, time.November, cells); err == nil {
				_ = shard.Close()
				t.Fatal("corrupt shard was accepted")
			}
		})
	}

	reordered := []Cell{cells[1], cells[0]}
	if shard, err := OpenDailyShard(path, 2025, time.November, reordered); err == nil {
		_ = shard.Close()
		t.Fatal("wrong ordered cell identity was accepted")
	}
	if shard, err := OpenDailyShard(path, 2024, time.November, cells); err == nil {
		_ = shard.Close()
		t.Fatal("wrong year identity was accepted")
	}
}

func TestDailyShardExistingResultResumesBeforeSourceConstruction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "complete.ned")
	cells := []Cell{dailyShardTestCell(t, 0, "UTC")}
	writeDailyShardFixture(t, path, 2025, time.November, cells, shardTestMetrics())

	state, err := PrepareDailyShard(path, 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	if state.Existing == nil || state.Sink != nil {
		t.Fatalf("resume result = %+v", state)
	}
	defer state.Existing.Close()
	sourceConstructed := false
	if state.Sink != nil {
		sourceConstructed = true
	}
	if sourceConstructed {
		t.Fatal("existing shard required source construction")
	}
}

func TestDailyShardSupportsConcurrentReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.ned")
	cells := []Cell{
		dailyShardTestCell(t, 0, "UTC"),
		dailyShardTestCell(t, 1, "Europe/Amsterdam"),
	}
	writeDailyShardFixture(t, path, 2025, time.November, cells, shardTestMetrics())
	shard, err := OpenDailyShard(path, 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()

	var workers sync.WaitGroup
	failures := make(chan error, 32)
	for worker := 0; worker < 32; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for cellIndex := range cells {
				for dayOffset := 0; dayOffset < 30; dayOffset++ {
					day, available, err := shard.Read(cellIndex, dayOffset)
					if err != nil {
						failures <- err
						return
					}
					if !available || day.CellIndex != cellIndex || day.Date.Day() != dayOffset+1 {
						failures <- errors.New("concurrent ERA5 daily shard read returned the wrong record")
						return
					}
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestDailyShardWalkIsCellMajorSkipsMissingDatesAndStopsOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walk.ned")
	cells := []Cell{
		dailyShardTestCell(t, 0, "Pacific/Apia"),
		dailyShardTestCell(t, 1, "UTC"),
	}
	writeDailyShardFixture(t, path, 2011, time.December, cells, shardTestMetrics())
	shard, err := OpenDailyShard(path, 2011, time.December, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()

	var records []DailyMetrics
	if err := shard.Walk(func(day DailyMetrics) error {
		records = append(records, day)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 61 {
		t.Fatalf("walk returned %d records, want 61", len(records))
	}
	if records[0].CellIndex != 0 || records[0].Date.Day() != 1 ||
		records[28].CellIndex != 0 || records[28].Date.Day() != 29 ||
		records[29].CellIndex != 0 || records[29].Date.Day() != 31 ||
		records[30].CellIndex != 1 || records[30].Date.Day() != 1 ||
		records[60].CellIndex != 1 || records[60].Date.Day() != 31 {
		t.Fatalf("walk order or skipped-date behavior changed: first=%+v boundary=%+v/%+v/%+v last=%+v",
			records[0], records[28], records[29], records[30], records[60])
	}
	stop := errors.New("stop")
	visits := 0
	err = shard.Walk(func(DailyMetrics) error {
		visits++
		if visits == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || visits != 3 {
		t.Fatalf("callback stop: visits=%d err=%v", visits, err)
	}
}

func TestDailyShardIdentityAndCellRangePreserveOrderedCalendarSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cell-range.ned")
	cells := []Cell{
		dailyShardTestCell(t, 0, "UTC"),
		dailyShardTestCell(t, 1, "Pacific/Apia"),
		dailyShardTestCell(t, 2, "Europe/Amsterdam"),
	}
	writeDailyShardFixture(t, path, 2011, time.December, cells, shardTestMetrics())
	shard, err := OpenDailyShard(path, 2011, time.December, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()

	identity, err := shard.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.Year != 2011 || identity.Month != time.December || identity.Days != 31 ||
		identity.CellCount != 3 || identity.PlanSHA256 == ([sha256.Size]byte{}) {
		t.Fatalf("identity = %+v", identity)
	}
	cell, err := shard.Cell(0)
	if err != nil {
		t.Fatal(err)
	}
	cell.TimezoneID = "changed"
	again, err := shard.Cell(0)
	if err != nil || again.TimezoneID != "UTC" {
		t.Fatalf("cell accessor exposed mutable shard state: cell=%+v err=%v", again, err)
	}
	if err := shard.VerifyPayload(t.Context()); err != nil {
		t.Fatalf("valid payload revalidation: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := shard.VerifyPayload(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled payload revalidation = %v; want context cancellation", err)
	}

	type slot struct {
		cell, day int
		available bool
	}
	var slots []slot
	digest := sha256.New()
	if err := shard.WalkCellRange(1, 2, digest, func(day DailyMetrics, available bool) error {
		slots = append(slots, slot{cell: day.CellIndex, day: day.Date.Day(), available: available})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 62 || slots[0] != (slot{cell: 1, day: 1, available: true}) ||
		slots[29] != (slot{cell: 1, day: 30, available: false}) ||
		slots[30] != (slot{cell: 1, day: 31, available: true}) ||
		slots[31] != (slot{cell: 2, day: 1, available: true}) ||
		slots[61] != (slot{cell: 2, day: 31, available: true}) {
		t.Fatalf("cell-range calendar slots changed: count=%d boundary=%+v/%+v/%+v/%+v/%+v",
			len(slots), slots[0], slots[29], slots[30], slots[31], slots[61])
	}
	stop := errors.New("stop")
	visits := 0
	err = shard.WalkCellRange(0, 1, sha256.New(), func(DailyMetrics, bool) error {
		visits++
		if visits == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || visits != 2 {
		t.Fatalf("cell-range callback stop: visits=%d err=%v", visits, err)
	}
	for _, invalid := range [][2]int{{-1, 1}, {0, 0}, {2, 2}} {
		if err := shard.WalkCellRange(invalid[0], invalid[1], sha256.New(), func(DailyMetrics, bool) error { return nil }); err == nil {
			t.Fatalf("cell range %v unexpectedly succeeded", invalid)
		}
	}
}

func TestDailyShardSupportsConcurrentWalks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-walk.ned")
	cells := []Cell{
		dailyShardTestCell(t, 0, "UTC"),
		dailyShardTestCell(t, 1, "Europe/Amsterdam"),
	}
	writeDailyShardFixture(t, path, 2025, time.November, cells, shardTestMetrics())
	shard, err := OpenDailyShard(path, 2025, time.November, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()

	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			count := 0
			if err := shard.Walk(func(DailyMetrics) error {
				count++
				return nil
			}); err != nil {
				failures <- err
			} else if count != 60 {
				failures <- fmt.Errorf("walk returned %d records, want 60", count)
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestDailyShardApiaSkippedCivilDateIsExplicitlyUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apia-2011-12.ned")
	cells := []Cell{dailyShardTestCell(t, 0, "Pacific/Apia")}
	writeDailyShardFixture(t, path, 2011, time.December, cells, shardTestMetrics())
	shard, err := OpenDailyShard(path, 2011, time.December, cells)
	if err != nil {
		t.Fatal(err)
	}
	defer shard.Close()
	if _, available, err := shard.Read(0, 29); err != nil || available {
		t.Fatalf("Apia 2011-12-30 available=%v err=%v", available, err)
	}
	if _, available, err := shard.Read(0, 28); err != nil || !available {
		t.Fatalf("Apia 2011-12-29 available=%v err=%v", available, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := shard.identity
	if bitSet(identity.expected, 29) {
		t.Fatal("expected-day mask includes nonexistent Apia date")
	}
	if !allZero(raw[dailyShardHeaderBytes+len(identity.expected)+29*DailyShardRecordBytes : dailyShardHeaderBytes+len(identity.expected)+30*DailyShardRecordBytes]) {
		t.Fatal("nonexistent Apia date contains a record")
	}
}

func TestDailyShardRecordPreservesDealbreakerScores(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		edit   func(*weather.DailyMetrics, float64)
	}{
		{
			name:   "wet time",
			values: wholeSecondNeighborhood(.1, .35, .6, .65, .8),
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.WetTimeFraction.Value = value
			},
		},
		{
			name:   "rain",
			values: floatNeighborhood(3, 7, 8, 12),
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.RainMM = value
			},
		},
		{
			name:   "wind",
			values: floatNeighborhood(28, 38, 40, 48),
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.WindSpeedKPH = value
			},
		},
	}
	for _, test := range tests {
		for _, value := range test.values {
			metrics := shardTestMetrics()
			test.edit(&metrics, value)
			beforeNice, beforeCycling, err := weather.ScoreProfilesChecked(metrics)
			if err != nil {
				t.Fatalf("%s %.9g before encode: %v", test.name, value, err)
			}
			var record [DailyShardRecordBytes]byte
			if err := encodeDailyShardRecord(record[:], metrics); err != nil {
				t.Fatalf("%s %.9g encode: %v", test.name, value, err)
			}
			decoded, err := decodeDailyShardRecord(record[:])
			if err != nil {
				t.Fatalf("%s %.9g decode: %v", test.name, value, err)
			}
			afterNice, afterCycling, err := weather.ScoreProfilesChecked(decoded)
			if err != nil {
				t.Fatalf("%s %.9g after decode: %v", test.name, value, err)
			}
			if beforeNice != afterNice || beforeCycling != afterCycling {
				t.Errorf("%s %.9g score changed: before nice=%+v cycling=%+v; after nice=%+v cycling=%+v; decoded=%.9g",
					test.name, value, beforeNice, beforeCycling, afterNice, afterCycling,
					fieldValue(test.name, decoded))
			}
		}
	}
}

func TestDailyShardRecordPreservesCompletePhysicalBreakpointCorpus(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		edit   func(*weather.DailyMetrics, float64)
	}{
		{
			name:   "temperature",
			values: []float64{-15, -5, 0, 5, 8, 12, 16, 19, 22, 27, 30, 34, 38, 42},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.ComfortableTempC = value
				metrics.MinTempC, metrics.MaxTempC = value-4, value+4
				metrics.DewPointC.Value = math.Min(value, 12)
			},
		},
		{
			name:   "rain",
			values: []float64{0, .2, 1, 3, 6, 7, 8, 10, 12},
			edit:   func(metrics *weather.DailyMetrics, value float64) { metrics.RainMM = value },
		},
		{
			name:   "wet time",
			values: []float64{0, .1, .25, .35, .5, .6, .65, .8},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.WetTimeFraction.Value = value
			},
		},
		{
			name:   "solar",
			values: []float64{0, 4, 8, 12, 18},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.SolarEnergyMJm2.Value = value
			},
		},
		{
			name:   "cloud",
			values: []float64{0, 25, 50, 75, 100},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.CloudCoverPercent.Value = value
			},
		},
		{
			name:   "wind",
			values: []float64{0, 12, 14, 20, 28, 30, 35, 38, 40, 42, 45, 48},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.WindSpeedKPH = value
			},
		},
		{
			name:   "dew point",
			values: []float64{8, 16, 20, 24, 26, 28, 30},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.MaxTempC = 34
				metrics.DewPointC.Value = value
			},
		},
		{
			name:   "relative humidity",
			values: []float64{0, 25, 50, 75, 100},
			edit: func(metrics *weather.DailyMetrics, value float64) {
				metrics.RelativeHumidityPercent.Value = value
			},
		},
	}
	for _, test := range tests {
		for _, value := range test.values {
			metrics := shardTestMetrics()
			test.edit(&metrics, value)
			assertDailyShardScoreParity(t, test.name, value, metrics)
		}
	}
}

func assertDailyShardScoreParity(t *testing.T, name string, value float64, metrics weather.DailyMetrics) {
	t.Helper()
	beforeNice, beforeCycling, err := weather.ScoreProfilesChecked(metrics)
	if err != nil {
		t.Fatalf("%s %.9g before encode: %v", name, value, err)
	}
	var record [DailyShardRecordBytes]byte
	if err := encodeDailyShardRecord(record[:], metrics); err != nil {
		t.Fatalf("%s %.9g encode: %v", name, value, err)
	}
	decoded, err := decodeDailyShardRecord(record[:])
	if err != nil {
		t.Fatalf("%s %.9g decode: %v", name, value, err)
	}
	afterNice, afterCycling, err := weather.ScoreProfilesChecked(decoded)
	if err != nil {
		t.Fatalf("%s %.9g after decode: %v", name, value, err)
	}
	if beforeNice != afterNice || beforeCycling != afterCycling {
		t.Errorf("%s %.9g score changed: before nice=%+v cycling=%+v; after nice=%+v cycling=%+v",
			name, value, beforeNice, beforeCycling, afterNice, afterCycling)
	}
	beforeValues := [...]float64{
		metrics.ComfortableTempC,
		metrics.MinTempC,
		metrics.MaxTempC,
		metrics.RainMM,
		metrics.SolarEnergyMJm2.Value,
		metrics.CloudCoverPercent.Value,
		metrics.WindSpeedKPH,
		metrics.DewPointC.Value,
		metrics.RelativeHumidityPercent.Value,
	}
	afterValues := [...]float64{
		decoded.ComfortableTempC,
		decoded.MinTempC,
		decoded.MaxTempC,
		decoded.RainMM,
		decoded.SolarEnergyMJm2.Value,
		decoded.CloudCoverPercent.Value,
		decoded.WindSpeedKPH,
		decoded.DewPointC.Value,
		decoded.RelativeHumidityPercent.Value,
	}
	for index := range beforeValues {
		if math.Float64bits(beforeValues[index]) != math.Float64bits(afterValues[index]) {
			t.Errorf("%s %.9g physical field %d changed from %.17g to %.17g",
				name, value, index, beforeValues[index], afterValues[index])
		}
	}
	if math.Float64bits(metrics.WetTimeFraction.Value) != math.Float64bits(decoded.WetTimeFraction.Value) {
		t.Errorf("%s %.9g wet time changed from %.17g to %.17g",
			name, value, metrics.WetTimeFraction.Value, decoded.WetTimeFraction.Value)
	}
}

func shardTestMetrics() weather.DailyMetrics {
	return weather.DailyMetrics{
		ComfortableTempC: 24,
		MinTempC:         18,
		MaxTempC:         27,
		RainMM:           0,
		WetTimeFraction:  weather.OptionalFloat{Value: 0, Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: 18, Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: 20, Available: true,
		},
		WindSpeedKPH: 8,
		DewPointC: weather.OptionalFloat{
			Value: 12, Available: true,
		},
		RelativeHumidityPercent: weather.OptionalFloat{
			Value: 45, Available: true,
		},
		NativeResolutionKM: NativeDetailKM,
		Valid:              true,
	}
}

func fieldValue(name string, metrics weather.DailyMetrics) float64 {
	switch name {
	case "wet time":
		return metrics.WetTimeFraction.Value
	case "rain":
		return metrics.RainMM
	case "wind":
		return metrics.WindSpeedKPH
	default:
		return 0
	}
}

func dailyShardTestCell(t *testing.T, sourceIndex int, timezoneID string) Cell {
	t.Helper()
	fingerprint, err := TimezoneFingerprint(timezoneID)
	if err != nil {
		t.Fatal(err)
	}
	row, column := sourceIndex/GridColumns, sourceIndex%GridColumns
	longitude := float64(column) * .25
	if longitude > 180 {
		longitude -= 360
	}
	return Cell{
		SourceIndex: sourceIndex,
		Latitude:    90 - float64(row)*.25,
		Longitude:   longitude,
		TimezoneID:  timezoneID, TimezoneFingerprint: fingerprint,
	}
}

func writeDailyShardFixture(t *testing.T, path string, year int, month time.Month, cells []Cell, metrics weather.DailyMetrics) {
	t.Helper()
	state, err := PrepareDailyShard(path, year, month, cells)
	if err != nil {
		t.Fatal(err)
	}
	if state.Existing != nil || state.Sink == nil {
		t.Fatalf("new shard state = %+v", state)
	}
	transaction, err := state.Sink.BeginMonth(t.Context(), year, month, cells)
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Abort()
		}
	}()
	identity, err := newDailyShardIdentity(year, month, cells)
	if err != nil {
		t.Fatal(err)
	}
	for cellIndex, cell := range cells {
		for dayOffset := 0; dayOffset < identity.days; dayOffset++ {
			if !bitSet(identity.expected, cellIndex*identity.days+dayOffset) {
				continue
			}
			if err := transaction.Add(DailyMetrics{
				CellIndex: cellIndex,
				Date:      time.Date(year, month, dayOffset+1, 0, 0, 0, 0, time.UTC),
				Latitude:  cell.Latitude,
				Longitude: cell.Longitude,
				Metrics:   metrics,
			}); err != nil {
				t.Fatalf("add cell %d day %d: %v", cellIndex, dayOffset+1, err)
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func floatNeighborhood(thresholds ...float64) []float64 {
	values := make([]float64, 0, len(thresholds)*3)
	for _, threshold := range thresholds {
		values = append(values,
			math.Nextafter(threshold, math.Inf(-1)),
			threshold,
			math.Nextafter(threshold, math.Inf(1)),
		)
	}
	return values
}

func wholeSecondNeighborhood(thresholds ...float64) []float64 {
	values := make([]float64, 0, len(thresholds)*3)
	for _, threshold := range thresholds {
		seconds := int(math.Round(threshold * 10 * 60 * 60))
		values = append(values,
			float64(seconds-1)/(10*60*60),
			float64(seconds)/(10*60*60),
			float64(seconds+1)/(10*60*60),
		)
	}
	return values
}
