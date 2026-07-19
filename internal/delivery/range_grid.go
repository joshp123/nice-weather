package delivery

import (
	"errors"
	"fmt"
	"os"

	"nice-weather-finder/internal/weather"
)

const (
	RangeGridVersion    = 1
	rangeGridRecordSize = 12
	rangeRecordValid    = 1
	maxRangeDayCount    = 255
)

var rangeGridLayout = binaryGridLayout{
	name:       "range grid",
	magic:      [8]byte{'N', 'W', 'F', 'R', 'A', 'N', 'G', 'E'},
	version:    RangeGridVersion,
	recordSize: rangeGridRecordSize,
}

// RangeRecord is the compact delivery representation of weather.RangeSummary.
// One range-grid file represents one profile, so profile identity belongs in
// its release path/metadata rather than being repeated in every cell.
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

type RangeGridReader struct {
	file   *os.File
	meta   GridMetadata
	mapper *WebMercatorMapper
}

func WriteRangeGrid(path string, spec GridSpec, records []RangeRecord) (GridMetadata, error) {
	return writeBinaryGrid(path, spec, len(records), rangeGridLayout, func(dst []byte, index uint64) error {
		record := records[index]
		if err := validateRangeRecord(record); err != nil {
			return err
		}
		encodeRangeRecord(dst, record)
		return nil
	})
}

func OpenRangeGrid(path string) (*RangeGridReader, error) {
	file, meta, mapper, err := openBinaryGrid(path, rangeGridLayout, func(src []byte) error {
		_, err := decodeRangeRecord(src)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &RangeGridReader{file: file, meta: meta, mapper: mapper}, nil
}

func ValidateRangeGrid(path string) (GridMetadata, error) {
	reader, err := OpenRangeGrid(path)
	if err != nil {
		return GridMetadata{}, err
	}
	defer reader.Close()
	return reader.Metadata(), nil
}

func (reader *RangeGridReader) Close() error { return reader.file.Close() }

func (reader *RangeGridReader) Metadata() GridMetadata { return reader.meta }

// RecordAt follows the same MapLibre projection and coastal-cell selection as
// the daily grid, so a range point lookup resolves to the cell shown on its
// scalar map rather than a second geographic interpretation.
func (reader *RangeGridReader) RecordAt(lon, lat float64) (RangeRecord, bool, error) {
	pixelRow, pixelCol, ok := reader.mapper.Pixel(lon, lat)
	if !ok {
		return RangeRecord{}, false, nil
	}
	var readErr error
	row, col, ok := reader.mapper.Select(pixelRow, pixelCol, func(row, col uint32) bool {
		record, err := reader.record(row, col)
		if err != nil {
			readErr = err
			return false
		}
		return record.Valid
	})
	if readErr != nil {
		return RangeRecord{}, false, readErr
	}
	if !ok {
		return RangeRecord{}, false, nil
	}
	record, err := reader.record(row, col)
	if err != nil {
		return RangeRecord{}, false, err
	}
	return record, record.Valid, nil
}

func (reader *RangeGridReader) record(row, col uint32) (RangeRecord, error) {
	offset := int64(gridHeaderSize) + int64((uint64(row)*uint64(reader.meta.Cols)+uint64(col))*rangeGridRecordSize)
	var encoded [rangeGridRecordSize]byte
	if _, err := reader.file.ReadAt(encoded[:], offset); err != nil {
		return RangeRecord{}, err
	}
	return decodeRangeRecord(encoded[:])
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

func encodeRangeRecord(dst []byte, record RangeRecord) {
	if !record.Valid {
		return
	}
	dst[0] = record.Score
	dst[1] = record.MedianScore
	dst[2] = record.Thermal
	dst[3] = record.Dryness
	dst[4] = record.Sun
	dst[5] = record.Wind
	dst[6] = record.NiceDays
	dst[7] = record.AwfulDays
	dst[8] = record.LongestPoorRun
	dst[9] = record.DayCount
	dst[10] = rangeRecordValid
}

func decodeRangeRecord(src []byte) (RangeRecord, error) {
	if len(src) != rangeGridRecordSize {
		return RangeRecord{}, errors.New("invalid range record size")
	}
	flags := src[10]
	if flags&^rangeRecordValid != 0 || src[11] != 0 {
		return RangeRecord{}, errors.New("range record contains unknown flags or reserved data")
	}
	if flags&rangeRecordValid == 0 {
		if !allZero(src) {
			return RangeRecord{}, errors.New("invalid range record contains data")
		}
		return RangeRecord{}, nil
	}
	record := RangeRecord{
		Score: src[0], MedianScore: src[1], Thermal: src[2], Dryness: src[3], Sun: src[4], Wind: src[5],
		NiceDays: src[6], AwfulDays: src[7], LongestPoorRun: src[8], DayCount: src[9], Valid: true,
	}
	if err := validateRangeRecord(record); err != nil {
		return RangeRecord{}, err
	}
	return record, nil
}
