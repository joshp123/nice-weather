package era5hourly

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/weather"
)

const (
	DailyShardRecordBytes = 80

	dailyShardHeaderBytes     = 288
	dailyShardSchemaVersion   = 1
	dailyShardMagic           = "NWE5DM01"
	dailyShardValidationBytes = 1024 * 1024
	dailyShardRecordContract  = "daily-metrics-v2|f64:comfortable,min,max,rain|u32:wet-daytime-seconds/36000|u32:reserved-zero|f64:solar,cloud,wind,dew-point,relative-humidity|" +
		"civil-local-daytime:10:00-20:00|wet-hour:mm>=0.1"
)

// DailyShardState is the result of preparing one immutable year-month shard.
// Exactly one field is set. Existing lets a caller resume without constructing
// or reading the hourly source; Sink builds a shard that does not yet exist.
type DailyShardState struct {
	Existing *DailyShard
	Sink     MonthSink
}

// PrepareDailyShard validates and opens an existing immutable shard, or
// returns an atomic sink for building it. An invalid existing file is never
// replaced.
func PrepareDailyShard(path string, year int, month time.Month, cells []Cell) (DailyShardState, error) {
	identity, err := newDailyShardIdentity(year, month, cells)
	if err != nil {
		return DailyShardState{}, err
	}
	shard, err := openDailyShard(path, identity)
	if err == nil {
		return DailyShardState{Existing: shard}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return DailyShardState{}, err
	}
	if path == "" || filepath.Base(path) == "." {
		return DailyShardState{}, errors.New("ERA5 daily shard path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return DailyShardState{}, fmt.Errorf("create ERA5 daily shard directory: %w", err)
	}
	return DailyShardState{Sink: &dailyShardSink{path: path, identity: identity}}, nil
}

// OpenDailyShard validates the complete file against the exact ordered cell
// plan before returning a bounded random-access reader.
func OpenDailyShard(path string, year int, month time.Month, cells []Cell) (*DailyShard, error) {
	identity, err := newDailyShardIdentity(year, month, cells)
	if err != nil {
		return nil, err
	}
	return openDailyShard(path, identity)
}

// DailyShard is a validated immutable daily-metrics shard.
type DailyShard struct {
	file      *os.File
	identity  dailyShardIdentity
	payloadAt int64
}

// Read returns the source-neutral daily record at the zero-based calendar day
// offset. A civil date that did not exist in the cell's timezone is explicitly
// unavailable.
func (shard *DailyShard) Read(cellIndex, dayOffset int) (DailyMetrics, bool, error) {
	if shard == nil || shard.file == nil {
		return DailyMetrics{}, false, errors.New("ERA5 daily shard is closed")
	}
	if cellIndex < 0 || cellIndex >= len(shard.identity.cells) ||
		dayOffset < 0 || dayOffset >= shard.identity.days {
		return DailyMetrics{}, false, errors.New("ERA5 daily shard record is outside the cell-month")
	}
	recordIndex := cellIndex*shard.identity.days + dayOffset
	if !bitSet(shard.identity.expected, recordIndex) {
		return DailyMetrics{}, false, nil
	}
	offset := shard.payloadAt + int64(recordIndex*DailyShardRecordBytes)
	var record [DailyShardRecordBytes]byte
	if _, err := shard.file.ReadAt(record[:], offset); err != nil {
		return DailyMetrics{}, false, fmt.Errorf("read ERA5 daily shard record: %w", err)
	}
	metrics, err := decodeDailyShardRecord(record[:])
	if err != nil {
		return DailyMetrics{}, false, err
	}
	cell := shard.identity.cells[cellIndex]
	return DailyMetrics{
		CellIndex: cellIndex,
		Date:      time.Date(shard.identity.year, shard.identity.month, dayOffset+1, 0, 0, 0, 0, time.UTC),
		Latitude:  cell.Latitude,
		Longitude: cell.Longitude,
		Metrics:   metrics,
	}, true, nil
}

// Walk visits every available record in cell-major, calendar-day order. It
// skips civil dates that did not exist. Each walk owns a buffered section
// reader, so independent walks do not share a mutable file offset.
func (shard *DailyShard) Walk(visit func(DailyMetrics) error) error {
	if shard == nil || shard.file == nil || visit == nil {
		return errors.New("ERA5 daily shard walk is unavailable")
	}
	payloadBytes := int64(mustPayloadBytes(shard.identity))
	reader := bufio.NewReaderSize(
		io.NewSectionReader(shard.file, shard.payloadAt, payloadBytes),
		dailyShardValidationBytes,
	)
	var record [DailyShardRecordBytes]byte
	for cellIndex, cell := range shard.identity.cells {
		for dayOffset := 0; dayOffset < shard.identity.days; dayOffset++ {
			if _, err := io.ReadFull(reader, record[:]); err != nil {
				return fmt.Errorf("walk ERA5 daily shard payload: %w", err)
			}
			recordIndex := cellIndex*shard.identity.days + dayOffset
			if !bitSet(shard.identity.expected, recordIndex) {
				continue
			}
			metrics, err := decodeDailyShardRecord(record[:])
			if err != nil {
				return fmt.Errorf("walk ERA5 daily shard record %d: %w", recordIndex, err)
			}
			if err := visit(DailyMetrics{
				CellIndex: cellIndex,
				Date:      time.Date(shard.identity.year, shard.identity.month, dayOffset+1, 0, 0, 0, 0, time.UTC),
				Latitude:  cell.Latitude,
				Longitude: cell.Longitude,
				Metrics:   metrics,
			}); err != nil {
				return fmt.Errorf("visit ERA5 daily shard record %d: %w", recordIndex, err)
			}
		}
	}
	return nil
}

func (shard *DailyShard) Close() error {
	if shard == nil || shard.file == nil {
		return nil
	}
	err := shard.file.Close()
	shard.file = nil
	return err
}

type dailyShardIdentity struct {
	year, days int
	month      time.Month
	cells      []Cell
	expected   []byte
	planSHA    [sha256.Size]byte
}

func newDailyShardIdentity(year int, month time.Month, cells []Cell) (dailyShardIdentity, error) {
	if year < climateFirstYear || year > climateLastYear ||
		month < time.January || month > time.December || len(cells) == 0 {
		return dailyShardIdentity{}, errors.New("ERA5 daily shard requires a supported month and cells")
	}
	groups, err := prepareGroups(year, month, cells)
	if err != nil {
		return dailyShardIdentity{}, err
	}
	if err := validateSourceCoordinates(cells); err != nil {
		return dailyShardIdentity{}, err
	}
	days := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	recordCount, ok := checkedProduct(len(cells), days)
	if !ok {
		return dailyShardIdentity{}, errors.New("ERA5 daily shard record count overflows")
	}
	expected := make([]byte, bitsetBytes(recordCount))
	for _, group := range groups {
		for _, window := range group.windows {
			if window.end.Sub(window.start) != 10*time.Hour {
				return dailyShardIdentity{}, fmt.Errorf("ERA5 timezone %s has a non-10-hour daytime window on %s", group.id, window.label.Format(time.DateOnly))
			}
		}
		for _, cellIndex := range group.cells {
			for day := 1; day <= days; day++ {
				if _, exists := group.windows[dateKey(year, month, day)]; exists {
					setBit(expected, cellIndex*days+day-1)
				}
			}
		}
	}
	copiedCells := append([]Cell(nil), cells...)
	return dailyShardIdentity{
		year: year, month: month, days: days, cells: copiedCells,
		expected: expected, planSHA: dailyShardPlanSHA(copiedCells),
	}, nil
}

func dailyShardPlanSHA(cells []Cell) [sha256.Size]byte {
	digest := sha256.New()
	var scalar [8]byte
	binary.LittleEndian.PutUint64(scalar[:], uint64(len(cells)))
	_, _ = digest.Write(scalar[:])
	for index, cell := range cells {
		binary.LittleEndian.PutUint64(scalar[:], uint64(index))
		_, _ = digest.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], uint64(cell.SourceIndex))
		_, _ = digest.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], math.Float64bits(cell.Latitude))
		_, _ = digest.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], math.Float64bits(cell.Longitude))
		_, _ = digest.Write(scalar[:])
		writeDigestString(digest, cell.TimezoneID)
		writeDigestString(digest, cell.TimezoneFingerprint)
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func writeDigestString(writer io.Writer, value string) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = io.WriteString(writer, value)
}

type dailyShardSink struct {
	path     string
	identity dailyShardIdentity
	begun    bool
}

func (sink *dailyShardSink) BeginMonth(ctx context.Context, year int, month time.Month, cells []Cell) (MonthTransaction, error) {
	if sink == nil || ctx == nil {
		return nil, errors.New("ERA5 daily shard sink is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sink.begun {
		return nil, errors.New("ERA5 daily shard sink was already started")
	}
	identity, err := newDailyShardIdentity(year, month, cells)
	if err != nil {
		return nil, err
	}
	if !sameDailyShardIdentity(identity, sink.identity) {
		return nil, errors.New("ERA5 daily shard sink identity changed")
	}
	file, err := os.CreateTemp(filepath.Dir(sink.path), ".era5-daily-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create ERA5 daily shard transaction: %w", err)
	}
	transaction := &dailyShardTransaction{
		finalPath: sink.path,
		temporary: file.Name(),
		file:      file,
		identity:  sink.identity,
		present:   make([]byte, len(sink.identity.expected)),
	}
	totalBytes, err := transaction.totalBytes()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if err := file.Truncate(totalBytes); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("size ERA5 daily shard transaction: %w", err)
	}
	sink.begun = true
	return transaction, nil
}

func sameDailyShardIdentity(left, right dailyShardIdentity) bool {
	return left.year == right.year && left.month == right.month && left.days == right.days &&
		len(left.cells) == len(right.cells) && left.planSHA == right.planSHA &&
		bytes.Equal(left.expected, right.expected)
}

type dailyShardTransaction struct {
	finalPath string
	temporary string
	file      *os.File
	identity  dailyShardIdentity
	present   []byte
	finished  bool
}

func (transaction *dailyShardTransaction) Add(day DailyMetrics) error {
	if transaction == nil || transaction.file == nil || transaction.finished {
		return errors.New("ERA5 daily shard transaction is closed")
	}
	if day.CellIndex < 0 || day.CellIndex >= len(transaction.identity.cells) {
		return errors.New("ERA5 daily shard cell index is outside the plan")
	}
	if day.Date.Location() != time.UTC || day.Date.Hour() != 0 || day.Date.Minute() != 0 ||
		day.Date.Second() != 0 || day.Date.Nanosecond() != 0 {
		return errors.New("ERA5 daily shard date must be a UTC midnight label")
	}
	year, month, calendarDay := day.Date.Date()
	if year != transaction.identity.year || month != transaction.identity.month ||
		calendarDay < 1 || calendarDay > transaction.identity.days {
		return errors.New("ERA5 daily shard date is outside the month")
	}
	cell := transaction.identity.cells[day.CellIndex]
	if math.Float64bits(day.Latitude) != math.Float64bits(cell.Latitude) ||
		math.Float64bits(day.Longitude) != math.Float64bits(cell.Longitude) {
		return errors.New("ERA5 daily shard record coordinate changed")
	}
	recordIndex := day.CellIndex*transaction.identity.days + calendarDay - 1
	if !bitSet(transaction.identity.expected, recordIndex) {
		return errors.New("ERA5 daily shard record uses a nonexistent civil date")
	}
	if bitSet(transaction.present, recordIndex) {
		return errors.New("ERA5 daily shard record is duplicated")
	}
	var record [DailyShardRecordBytes]byte
	if err := encodeDailyShardRecord(record[:], day.Metrics); err != nil {
		return err
	}
	payloadAt := int64(dailyShardHeaderBytes + len(transaction.identity.expected))
	offset := payloadAt + int64(recordIndex*DailyShardRecordBytes)
	if _, err := transaction.file.WriteAt(record[:], offset); err != nil {
		return fmt.Errorf("write ERA5 daily shard record: %w", err)
	}
	setBit(transaction.present, recordIndex)
	return nil
}

func (transaction *dailyShardTransaction) Commit() error {
	if transaction == nil || transaction.file == nil || transaction.finished {
		return errors.New("ERA5 daily shard transaction is closed")
	}
	if !bytes.Equal(transaction.present, transaction.identity.expected) {
		return errors.New("ERA5 daily shard is incomplete")
	}
	payloadSHA, err := transaction.payloadSHA()
	if err != nil {
		return err
	}
	header, err := encodeDailyShardHeader(transaction.identity, payloadSHA)
	if err != nil {
		return err
	}
	if _, err := transaction.file.WriteAt(header, 0); err != nil {
		return fmt.Errorf("write ERA5 daily shard header: %w", err)
	}
	if _, err := transaction.file.WriteAt(transaction.present, dailyShardHeaderBytes); err != nil {
		return fmt.Errorf("write ERA5 daily shard completeness: %w", err)
	}
	if err := transaction.file.Sync(); err != nil {
		return fmt.Errorf("sync ERA5 daily shard: %w", err)
	}
	if err := transaction.file.Close(); err != nil {
		return fmt.Errorf("close ERA5 daily shard: %w", err)
	}
	transaction.file = nil
	if err := os.Link(transaction.temporary, transaction.finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("immutable ERA5 daily shard already exists")
		}
		return fmt.Errorf("install ERA5 daily shard: %w", err)
	}
	directory, err := os.Open(filepath.Dir(transaction.finalPath))
	if err != nil {
		removeErr := os.Remove(transaction.finalPath)
		return fmt.Errorf("open ERA5 daily shard directory: %w", errors.Join(err, removeErr))
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		removeErr := os.Remove(transaction.finalPath)
		return fmt.Errorf("sync ERA5 daily shard directory: %w", errors.Join(err, removeErr))
	}
	transaction.finished = true
	_ = os.Remove(transaction.temporary)
	return nil
}

func (transaction *dailyShardTransaction) Abort() error {
	if transaction == nil || transaction.finished {
		return nil
	}
	transaction.finished = true
	var closeErr error
	if transaction.file != nil {
		closeErr = transaction.file.Close()
		transaction.file = nil
	}
	removeErr := os.Remove(transaction.temporary)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

func (transaction *dailyShardTransaction) totalBytes() (int64, error) {
	recordCount, ok := checkedProduct(len(transaction.identity.cells), transaction.identity.days)
	if !ok {
		return 0, errors.New("ERA5 daily shard record count overflows")
	}
	payloadBytes, ok := checkedProduct(recordCount, DailyShardRecordBytes)
	if !ok || payloadBytes > math.MaxInt64-dailyShardHeaderBytes-len(transaction.identity.expected) {
		return 0, errors.New("ERA5 daily shard byte count overflows")
	}
	return int64(dailyShardHeaderBytes + len(transaction.identity.expected) + payloadBytes), nil
}

func (transaction *dailyShardTransaction) payloadSHA() ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	payloadAt := int64(dailyShardHeaderBytes + len(transaction.identity.expected))
	if _, err := transaction.file.Seek(payloadAt, io.SeekStart); err != nil {
		return result, fmt.Errorf("seek ERA5 daily shard payload: %w", err)
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(transaction.file, int64(mustPayloadBytes(transaction.identity))))
	if err != nil {
		return result, fmt.Errorf("hash ERA5 daily shard payload: %w", err)
	}
	if written != int64(mustPayloadBytes(transaction.identity)) {
		return result, errors.New("ERA5 daily shard payload is truncated")
	}
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func encodeDailyShardRecord(destination []byte, metrics weather.DailyMetrics) error {
	if len(destination) != DailyShardRecordBytes {
		return errors.New("ERA5 daily shard record has the wrong size")
	}
	if err := metrics.Validate(); err != nil {
		return fmt.Errorf("invalid ERA5 daily shard metrics: %w", err)
	}
	if !metrics.WetTimeFraction.Available || !metrics.SolarEnergyMJm2.Available ||
		!metrics.CloudCoverPercent.Available || metrics.GustKPH.Available ||
		!metrics.DewPointC.Available || !metrics.RelativeHumidityPercent.Available ||
		metrics.NativeResolutionKM != NativeDetailKM {
		return errors.New("ERA5 daily shard metrics do not match the source-neutral evidence contract")
	}
	floatValues := [...]float64{
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
	for index, value := range floatValues[:4] {
		binary.LittleEndian.PutUint64(destination[index*8:], math.Float64bits(value))
	}
	wetSeconds := uint32(math.Round(metrics.WetTimeFraction.Value * 10 * 60 * 60))
	if float64(wetSeconds)/(10*60*60) != metrics.WetTimeFraction.Value {
		return errors.New("ERA5 daily shard wet time is not an exact whole-second duration")
	}
	binary.LittleEndian.PutUint32(destination[32:36], wetSeconds)
	binary.LittleEndian.PutUint32(destination[36:40], 0)
	for index, value := range floatValues[4:] {
		binary.LittleEndian.PutUint64(destination[40+index*8:], math.Float64bits(value))
	}
	if _, err := decodeDailyShardRecord(destination); err != nil {
		return fmt.Errorf("ERA5 daily shard record is invalid: %w", err)
	}
	return nil
}

func decodeDailyShardRecord(source []byte) (weather.DailyMetrics, error) {
	if len(source) != DailyShardRecordBytes {
		return weather.DailyMetrics{}, errors.New("ERA5 daily shard record has the wrong size")
	}
	values := [9]float64{}
	for index := range values[:4] {
		values[index] = math.Float64frombits(binary.LittleEndian.Uint64(source[index*8:]))
	}
	wetSeconds := binary.LittleEndian.Uint32(source[32:36])
	if wetSeconds > 10*60*60 {
		return weather.DailyMetrics{}, errors.New("ERA5 daily shard wet time exceeds the daytime window")
	}
	if binary.LittleEndian.Uint32(source[36:40]) != 0 {
		return weather.DailyMetrics{}, errors.New("ERA5 daily shard reserved record bytes are not zero")
	}
	for index := range values[4:] {
		values[4+index] = math.Float64frombits(binary.LittleEndian.Uint64(source[40+index*8:]))
	}
	metrics := weather.DailyMetrics{
		ComfortableTempC: values[0],
		MinTempC:         values[1],
		MaxTempC:         values[2],
		RainMM:           values[3],
		WetTimeFraction:  weather.OptionalFloat{Value: float64(wetSeconds) / (10 * 60 * 60), Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: values[4], Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: values[5], Available: true,
		},
		WindSpeedKPH: values[6],
		DewPointC: weather.OptionalFloat{
			Value: values[7], Available: true,
		},
		RelativeHumidityPercent: weather.OptionalFloat{
			Value: values[8], Available: true,
		},
		NativeResolutionKM: NativeDetailKM,
		Valid:              true,
	}
	if err := metrics.Validate(); err != nil {
		return weather.DailyMetrics{}, fmt.Errorf("invalid ERA5 daily shard record: %w", err)
	}
	return metrics, nil
}

func encodeDailyShardHeader(identity dailyShardIdentity, payloadSHA [sha256.Size]byte) ([]byte, error) {
	recordCount, ok := checkedProduct(len(identity.cells), identity.days)
	if !ok {
		return nil, errors.New("ERA5 daily shard record count overflows")
	}
	payloadBytes, ok := checkedProduct(recordCount, DailyShardRecordBytes)
	if !ok {
		return nil, errors.New("ERA5 daily shard payload size overflows")
	}
	header := make([]byte, dailyShardHeaderBytes)
	copy(header[:8], dailyShardMagic)
	binary.LittleEndian.PutUint16(header[8:10], dailyShardSchemaVersion)
	binary.LittleEndian.PutUint16(header[10:12], dailyShardHeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], DailyShardRecordBytes)
	binary.LittleEndian.PutUint16(header[14:16], SchemaVersion)
	binary.LittleEndian.PutUint32(header[16:20], uint32(identity.year))
	binary.LittleEndian.PutUint16(header[20:22], uint16(identity.month))
	binary.LittleEndian.PutUint16(header[22:24], uint16(identity.days))
	binary.LittleEndian.PutUint64(header[24:32], uint64(len(identity.cells)))
	binary.LittleEndian.PutUint64(header[32:40], uint64(recordCount))
	binary.LittleEndian.PutUint64(header[40:48], uint64(len(identity.expected)))
	binary.LittleEndian.PutUint64(header[48:56], uint64(payloadBytes))
	copy(header[56:76], SnapshotID)
	copy(header[76:108], sourceContractSHA256[:])
	timezoneSHA := sha256.Sum256([]byte(TimezoneContractVersion))
	copy(header[108:140], timezoneSHA[:])
	copy(header[140:172], identity.planSHA[:])
	completenessSHA := sha256.Sum256(identity.expected)
	copy(header[172:204], completenessSHA[:])
	copy(header[204:236], payloadSHA[:])
	recordContractSHA := sha256.Sum256([]byte(dailyShardRecordContract))
	copy(header[236:268], recordContractSHA[:])
	return header, nil
}

func openDailyShard(path string, identity dailyShardIdentity) (*DailyShard, error) {
	if path == "" {
		return nil, errors.New("ERA5 daily shard path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*DailyShard, error) {
		_ = file.Close()
		return nil, err
	}
	header := make([]byte, dailyShardHeaderBytes)
	if _, err := io.ReadFull(file, header); err != nil {
		return closeOnError(fmt.Errorf("read ERA5 daily shard header: %w", err))
	}
	payloadSHA, err := validateDailyShardHeader(header, identity)
	if err != nil {
		return closeOnError(err)
	}
	expectedSize := int64(dailyShardHeaderBytes + len(identity.expected) + mustPayloadBytes(identity))
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("stat ERA5 daily shard: %w", err))
	}
	if info.Size() != expectedSize {
		return closeOnError(fmt.Errorf("ERA5 daily shard size is %d; want %d", info.Size(), expectedSize))
	}
	completeness := make([]byte, len(identity.expected))
	if _, err := io.ReadFull(file, completeness); err != nil {
		return closeOnError(fmt.Errorf("read ERA5 daily shard completeness: %w", err))
	}
	if !bytes.Equal(completeness, identity.expected) {
		return closeOnError(errors.New("ERA5 daily shard completeness does not match the civil-day plan"))
	}
	bufferedPayload := bufio.NewReaderSize(file, dailyShardValidationBytes)
	digest := sha256.New()
	record := make([]byte, DailyShardRecordBytes)
	for recordIndex := 0; recordIndex < len(identity.cells)*identity.days; recordIndex++ {
		if _, err := io.ReadFull(bufferedPayload, record); err != nil {
			return closeOnError(fmt.Errorf("read ERA5 daily shard payload: %w", err))
		}
		_, _ = digest.Write(record)
		if bitSet(identity.expected, recordIndex) {
			if _, err := decodeDailyShardRecord(record); err != nil {
				return closeOnError(fmt.Errorf("ERA5 daily shard record %d: %w", recordIndex, err))
			}
		} else if !allZero(record) {
			return closeOnError(fmt.Errorf("ERA5 daily shard nonexistent civil date %d contains data", recordIndex))
		}
	}
	if !bytes.Equal(digest.Sum(nil), payloadSHA[:]) {
		return closeOnError(errors.New("ERA5 daily shard payload checksum mismatch"))
	}
	return &DailyShard{
		file: file, identity: identity,
		payloadAt: int64(dailyShardHeaderBytes + len(identity.expected)),
	}, nil
}

func validateDailyShardHeader(header []byte, identity dailyShardIdentity) ([sha256.Size]byte, error) {
	var payloadSHA [sha256.Size]byte
	recordCount, ok := checkedProduct(len(identity.cells), identity.days)
	payloadBytes, payloadOK := checkedProduct(recordCount, DailyShardRecordBytes)
	timezoneSHA := sha256.Sum256([]byte(TimezoneContractVersion))
	completenessSHA := sha256.Sum256(identity.expected)
	recordContractSHA := sha256.Sum256([]byte(dailyShardRecordContract))
	if len(header) != dailyShardHeaderBytes ||
		string(header[:8]) != dailyShardMagic ||
		binary.LittleEndian.Uint16(header[8:10]) != dailyShardSchemaVersion ||
		binary.LittleEndian.Uint16(header[10:12]) != dailyShardHeaderBytes ||
		binary.LittleEndian.Uint16(header[12:14]) != DailyShardRecordBytes ||
		binary.LittleEndian.Uint16(header[14:16]) != SchemaVersion ||
		binary.LittleEndian.Uint32(header[16:20]) != uint32(identity.year) ||
		binary.LittleEndian.Uint16(header[20:22]) != uint16(identity.month) ||
		binary.LittleEndian.Uint16(header[22:24]) != uint16(identity.days) ||
		!ok || !payloadOK ||
		binary.LittleEndian.Uint64(header[24:32]) != uint64(len(identity.cells)) ||
		binary.LittleEndian.Uint64(header[32:40]) != uint64(recordCount) ||
		binary.LittleEndian.Uint64(header[40:48]) != uint64(len(identity.expected)) ||
		binary.LittleEndian.Uint64(header[48:56]) != uint64(payloadBytes) {
		return payloadSHA, errors.New("ERA5 daily shard header changed")
	}
	if string(header[56:76]) != SnapshotID ||
		!bytes.Equal(header[76:108], sourceContractSHA256[:]) {
		return payloadSHA, errors.New("ERA5 daily shard source identity changed")
	}
	if !bytes.Equal(header[108:140], timezoneSHA[:]) ||
		!bytes.Equal(header[140:172], identity.planSHA[:]) ||
		!bytes.Equal(header[172:204], completenessSHA[:]) ||
		!bytes.Equal(header[236:268], recordContractSHA[:]) {
		return payloadSHA, errors.New("ERA5 daily shard cell or timezone identity changed")
	}
	if !allZero(header[268:]) {
		return payloadSHA, errors.New("ERA5 daily shard reserved header bytes are not zero")
	}
	copy(payloadSHA[:], header[204:236])
	return payloadSHA, nil
}

func checkedProduct(left, right int) (int, bool) {
	if left < 0 || right < 0 || (left != 0 && right > math.MaxInt/left) {
		return 0, false
	}
	return left * right, true
}

func mustPayloadBytes(identity dailyShardIdentity) int {
	return len(identity.cells) * identity.days * DailyShardRecordBytes
}

func bitsetBytes(bits int) int { return (bits + 7) / 8 }

func setBit(bitset []byte, index int) {
	bitset[index/8] |= 1 << uint(index%8)
}

func bitSet(bitset []byte, index int) bool {
	return bitset[index/8]&(1<<uint(index%8)) != 0
}
