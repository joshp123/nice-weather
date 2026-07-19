package delivery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"nice-weather-finder/internal/weather"
)

const (
	GridVersion    = 2
	gridHeaderSize = 128
	gridRecordSize = 22
)

const (
	recordValid = 1 << iota
	recordHasDewPoint
	recordHasGust
	recordHasSolar
	recordHasCloud
	recordHasHumidity
	recordHasWetTime
)

const recordKnownFlags = recordValid | recordHasDewPoint | recordHasGust | recordHasSolar | recordHasCloud | recordHasHumidity | recordHasWetTime

var gridMagic = [8]byte{'N', 'W', 'F', 'G', 'R', 'I', 'D', 0}

var dailyGridLayout = binaryGridLayout{name: "grid", magic: gridMagic, version: GridVersion, recordSize: gridRecordSize}

// GridBounds are the outer edges of a regular EPSG:4326 grid. Rows are stored
// north to south and columns west to east.
type GridBounds struct {
	West  float64 `json:"west"`
	East  float64 `json:"east"`
	South float64 `json:"south"`
	North float64 `json:"north"`
}

type GridSpec struct {
	Bounds GridBounds
	Rows   uint32
	Cols   uint32
}

// DailyRecord is the delivery DTO. Temperatures are tenths of a degree
// Celsius, precipitation tenths of a millimetre, wind tenths of a km/h,
// solar energy hundredths of an MJ/m2, and wet time thousandths of the local
// daytime window. Invalid records carry no weather meaning.
type DailyRecord struct {
	ComfortableTemperatureDeciC int16
	MinTemperatureDeciC         int16
	MaxTemperatureDeciC         int16
	DewPointDeciC               int16
	RainDeciMM                  uint16
	WindDeciKMH                 uint16
	GustDeciKMH                 uint16
	SolarCentiMJm2              uint16
	WetTimePermille             uint16
	CloudPercent                uint8
	HumidityPercent             uint8
	Valid                       bool
	HasDewPoint                 bool
	HasGust                     bool
	HasSolar                    bool
	HasCloud                    bool
	HasHumidity                 bool
	HasWetTime                  bool
}

type GridMetadata struct {
	Version    uint16
	Bounds     GridBounds
	Rows       uint32
	Cols       uint32
	RecordSize uint16
	SHA256     string
}

type GridReader struct {
	file   *os.File
	meta   GridMetadata
	mapper *WebMercatorMapper
}

func WriteGrid(path string, spec GridSpec, records []DailyRecord) (GridMetadata, error) {
	return writeBinaryGrid(path, spec, len(records), dailyGridLayout, func(dst []byte, index uint64) error {
		record := records[index]
		if err := validateRecord(record); err != nil {
			return err
		}
		encodeRecord(dst, record)
		return nil
	})
}

func OpenGrid(path string) (*GridReader, error) {
	file, meta, mapper, err := openBinaryGrid(path, dailyGridLayout, func(src []byte) error {
		_, err := decodeRecord(src)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &GridReader{file: file, meta: meta, mapper: mapper}, nil
}

func ValidateGrid(path string) (GridMetadata, error) {
	reader, err := OpenGrid(path)
	if err != nil {
		return GridMetadata{}, err
	}
	defer reader.Close()
	return reader.Metadata(), nil
}

func (reader *GridReader) Close() error { return reader.file.Close() }

func (reader *GridReader) Metadata() GridMetadata { return reader.meta }

// RecordAt returns the record used by the score-overlay pixel displayed at the
// coordinate. Points outside MapLibre's rounded image quad are rejected; the
// quad may extend a fraction beyond the source grid's declared bounds.
func (reader *GridReader) RecordAt(lon, lat float64) (DailyRecord, bool, error) {
	pixelRow, pixelCol, ok := reader.mapper.Pixel(lon, lat)
	if !ok {
		return DailyRecord{}, false, nil
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
		return DailyRecord{}, false, readErr
	}
	if !ok {
		return DailyRecord{}, false, nil
	}
	record, err := reader.record(row, col)
	if err != nil {
		return DailyRecord{}, false, err
	}
	return record, record.Valid, nil
}

func (reader *GridReader) record(row, col uint32) (DailyRecord, error) {
	offset := int64(gridHeaderSize) + int64((uint64(row)*uint64(reader.meta.Cols)+uint64(col))*gridRecordSize)
	var encoded [gridRecordSize]byte
	if _, err := reader.file.ReadAt(encoded[:], offset); err != nil {
		return DailyRecord{}, err
	}
	record, err := decodeRecord(encoded[:])
	if err != nil {
		return DailyRecord{}, err
	}
	return record, nil
}

func validateGridSpec(spec GridSpec) error {
	return validateGridSpecForRecordSize(spec, gridRecordSize)
}

// ValidateGridSpec checks the shared regular-grid geometry without coupling a
// caller to either daily or range record bytes.
func ValidateGridSpec(spec GridSpec) error {
	return validateGridSpec(spec)
}

func validateGridSpecForRecordSize(spec GridSpec, recordSize uint16) error {
	b := spec.Bounds
	if recordSize == 0 {
		return errors.New("grid record size must be positive")
	}
	if spec.Rows == 0 || spec.Cols == 0 {
		return errors.New("grid rows and columns must be positive")
	}
	if !finite(b.West) || !finite(b.East) || !finite(b.South) || !finite(b.North) || b.West >= b.East || b.South >= b.North {
		return errors.New("grid bounds are invalid")
	}
	if b.West < -180 || b.East > 180 || b.South < -90 || b.North > 90 {
		return errors.New("grid bounds exceed EPSG:4326")
	}
	count := uint64(spec.Rows) * uint64(spec.Cols)
	if count > uint64(math.MaxInt64-gridHeaderSize)/uint64(recordSize) {
		return errors.New("grid is too large")
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validateRecord(record DailyRecord) error {
	if !record.Valid {
		if record != (DailyRecord{}) {
			return errors.New("invalid record must contain only zero values")
		}
		return nil
	}
	comfort, minimum, maximum := int(record.ComfortableTemperatureDeciC), int(record.MinTemperatureDeciC), int(record.MaxTemperatureDeciC)
	if minimum > maximum || comfort < minimum-50 || comfort > maximum+50 {
		return errors.New("record comfortable temperature is inconsistent with its range")
	}
	if minimum < -1000 || maximum > 700 || comfort < -1000 || comfort > 700 || (record.HasDewPoint && (record.DewPointDeciC < -1000 || record.DewPointDeciC > 700)) {
		return errors.New("record temperature is outside supported range")
	}
	if record.RainDeciMM > 10000 || record.WindDeciKMH > 5000 || record.GustDeciKMH > 5000 || record.SolarCentiMJm2 > 6000 {
		return errors.New("record weather value is outside supported physical range")
	}
	if record.WetTimePermille > 1000 {
		return errors.New("record wet time exceeds one whole daytime window")
	}
	if !record.HasWetTime && record.WetTimePermille != 0 {
		return errors.New("missing wet time must be zero")
	}
	if record.HasCloud && record.CloudPercent > 100 {
		return errors.New("record cloud percentage exceeds 100")
	}
	if record.HasHumidity && record.HumidityPercent > 100 {
		return errors.New("record humidity percentage exceeds 100")
	}
	if !record.HasDewPoint && record.DewPointDeciC != 0 {
		return errors.New("missing dew point must be zero")
	}
	if !record.HasGust && record.GustDeciKMH != 0 {
		return errors.New("missing gust must be zero")
	}
	if !record.HasSolar && record.SolarCentiMJm2 != 0 {
		return errors.New("missing solar energy must be zero")
	}
	if !record.HasCloud && record.CloudPercent != 0 {
		return errors.New("missing cloud cover must be zero")
	}
	if !record.HasHumidity && record.HumidityPercent != 0 {
		return errors.New("missing humidity must be zero")
	}
	if !record.HasSolar && !record.HasCloud {
		return errors.New("record needs solar or cloud evidence")
	}
	if !record.HasDewPoint && !record.HasHumidity {
		return errors.New("record needs dew point or humidity evidence")
	}
	if record.HasDewPoint && int(record.DewPointDeciC) > maximum+20 {
		return errors.New("record dew point exceeds maximum temperature")
	}
	if record.HasGust && int(record.GustDeciKMH)+30 < int(record.WindDeciKMH) {
		return errors.New("record gust is materially below sustained wind")
	}
	return nil
}

// RecordFromWeather is the only checked conversion from canonical weather
// facts into the compact delivery representation.
func RecordFromWeather(metrics weather.DailyMetrics) (DailyRecord, error) {
	if err := metrics.Validate(); err != nil {
		return DailyRecord{}, err
	}
	comfort, err := quantizeInt16(metrics.ComfortableTempC, 10, "comfortable temperature")
	if err != nil {
		return DailyRecord{}, err
	}
	minimum, err := quantizeInt16(metrics.MinTempC, 10, "minimum temperature")
	if err != nil {
		return DailyRecord{}, err
	}
	maximum, err := quantizeInt16(metrics.MaxTempC, 10, "maximum temperature")
	if err != nil {
		return DailyRecord{}, err
	}
	rain, err := quantizeUint16(metrics.RainMM, 10, "rain")
	if err != nil {
		return DailyRecord{}, err
	}
	wind, err := quantizeUint16(metrics.WindSpeedKPH, 10, "wind")
	if err != nil {
		return DailyRecord{}, err
	}
	record := DailyRecord{ComfortableTemperatureDeciC: comfort, MinTemperatureDeciC: minimum, MaxTemperatureDeciC: maximum, RainDeciMM: rain, WindDeciKMH: wind, Valid: true}
	if metrics.WetTimeFraction.Available {
		record.WetTimePermille, err = quantizeUint16(metrics.WetTimeFraction.Value, 1000, "wet time")
		record.HasWetTime = true
	}
	if metrics.DewPointC.Available {
		record.DewPointDeciC, err = quantizeInt16(metrics.DewPointC.Value, 10, "dew point")
		record.HasDewPoint = true
	}
	if err == nil && metrics.GustKPH.Available {
		record.GustDeciKMH, err = quantizeUint16(metrics.GustKPH.Value, 10, "gust")
		record.HasGust = true
	}
	if err == nil && metrics.SolarEnergyMJm2.Available {
		record.SolarCentiMJm2, err = quantizeUint16(metrics.SolarEnergyMJm2.Value, 100, "solar energy")
		record.HasSolar = true
	}
	if err == nil && metrics.CloudCoverPercent.Available {
		var value uint16
		value, err = quantizeUint16(metrics.CloudCoverPercent.Value, 1, "cloud cover")
		record.CloudPercent, record.HasCloud = uint8(value), true
	}
	if err == nil && metrics.RelativeHumidityPercent.Available {
		var value uint16
		value, err = quantizeUint16(metrics.RelativeHumidityPercent.Value, 1, "humidity")
		record.HumidityPercent, record.HasHumidity = uint8(value), true
	}
	if err != nil {
		return DailyRecord{}, err
	}
	if err := validateRecord(record); err != nil {
		return DailyRecord{}, err
	}
	return record, nil
}

func (record DailyRecord) WeatherMetrics(nativeResolutionKM float64) (weather.DailyMetrics, error) {
	if err := validateRecord(record); err != nil {
		return weather.DailyMetrics{}, err
	}
	if !record.Valid {
		return weather.DailyMetrics{}, errors.New("no weather at this cell")
	}
	metrics := weather.DailyMetrics{
		ComfortableTempC:   float64(record.ComfortableTemperatureDeciC) / 10,
		MinTempC:           float64(record.MinTemperatureDeciC) / 10,
		MaxTempC:           float64(record.MaxTemperatureDeciC) / 10,
		RainMM:             float64(record.RainDeciMM) / 10,
		WetTimeFraction:    weather.OptionalFloat{Value: float64(record.WetTimePermille) / 1000, Available: record.HasWetTime},
		WindSpeedKPH:       float64(record.WindDeciKMH) / 10,
		NativeResolutionKM: nativeResolutionKM,
		Valid:              true,
	}
	metrics.DewPointC = weather.OptionalFloat{Value: float64(record.DewPointDeciC) / 10, Available: record.HasDewPoint}
	metrics.GustKPH = weather.OptionalFloat{Value: float64(record.GustDeciKMH) / 10, Available: record.HasGust}
	metrics.SolarEnergyMJm2 = weather.OptionalFloat{Value: float64(record.SolarCentiMJm2) / 100, Available: record.HasSolar}
	metrics.CloudCoverPercent = weather.OptionalFloat{Value: float64(record.CloudPercent), Available: record.HasCloud}
	metrics.RelativeHumidityPercent = weather.OptionalFloat{Value: float64(record.HumidityPercent), Available: record.HasHumidity}
	if err := metrics.Validate(); err != nil {
		return weather.DailyMetrics{}, err
	}
	return metrics, nil
}

func quantizeInt16(value, scale float64, name string) (int16, error) {
	quantized := math.Round(value * scale)
	if !finite(quantized) || quantized < math.MinInt16 || quantized > math.MaxInt16 {
		return 0, fmt.Errorf("%s exceeds delivery range", name)
	}
	return int16(quantized), nil
}

func quantizeUint16(value, scale float64, name string) (uint16, error) {
	quantized := math.Round(value * scale)
	if !finite(quantized) || quantized < 0 || quantized > math.MaxUint16 {
		return 0, fmt.Errorf("%s exceeds delivery range", name)
	}
	return uint16(quantized), nil
}

func encodeRecord(dst []byte, record DailyRecord) {
	for index := range dst {
		dst[index] = 0
	}
	if !record.Valid {
		return
	}
	binary.LittleEndian.PutUint16(dst[0:2], uint16(record.ComfortableTemperatureDeciC))
	binary.LittleEndian.PutUint16(dst[2:4], uint16(record.MinTemperatureDeciC))
	binary.LittleEndian.PutUint16(dst[4:6], uint16(record.MaxTemperatureDeciC))
	binary.LittleEndian.PutUint16(dst[6:8], uint16(record.DewPointDeciC))
	binary.LittleEndian.PutUint16(dst[8:10], record.RainDeciMM)
	binary.LittleEndian.PutUint16(dst[10:12], record.WindDeciKMH)
	binary.LittleEndian.PutUint16(dst[12:14], record.GustDeciKMH)
	binary.LittleEndian.PutUint16(dst[14:16], record.SolarCentiMJm2)
	binary.LittleEndian.PutUint16(dst[16:18], record.WetTimePermille)
	dst[18] = record.CloudPercent
	dst[19] = record.HumidityPercent
	dst[20] = recordFlags(record)
}

func decodeRecord(src []byte) (DailyRecord, error) {
	if len(src) != gridRecordSize {
		return DailyRecord{}, errors.New("invalid record size")
	}
	flags := src[20]
	if flags&^recordKnownFlags != 0 || src[21] != 0 {
		return DailyRecord{}, errors.New("record contains unknown flags or reserved data")
	}
	if flags&recordValid == 0 {
		if !allZero(src) {
			return DailyRecord{}, errors.New("invalid record contains data")
		}
		return DailyRecord{}, nil
	}
	record := DailyRecord{
		ComfortableTemperatureDeciC: int16(binary.LittleEndian.Uint16(src[0:2])),
		MinTemperatureDeciC:         int16(binary.LittleEndian.Uint16(src[2:4])),
		MaxTemperatureDeciC:         int16(binary.LittleEndian.Uint16(src[4:6])),
		DewPointDeciC:               int16(binary.LittleEndian.Uint16(src[6:8])),
		RainDeciMM:                  binary.LittleEndian.Uint16(src[8:10]),
		WindDeciKMH:                 binary.LittleEndian.Uint16(src[10:12]),
		GustDeciKMH:                 binary.LittleEndian.Uint16(src[12:14]),
		SolarCentiMJm2:              binary.LittleEndian.Uint16(src[14:16]),
		WetTimePermille:             binary.LittleEndian.Uint16(src[16:18]),
		CloudPercent:                src[18],
		HumidityPercent:             src[19],
		Valid:                       true,
		HasDewPoint:                 flags&recordHasDewPoint != 0,
		HasGust:                     flags&recordHasGust != 0,
		HasSolar:                    flags&recordHasSolar != 0,
		HasCloud:                    flags&recordHasCloud != 0,
		HasHumidity:                 flags&recordHasHumidity != 0,
		HasWetTime:                  flags&recordHasWetTime != 0,
	}
	if err := validateRecord(record); err != nil {
		return DailyRecord{}, err
	}
	return record, nil
}

func recordFlags(record DailyRecord) uint8 {
	flags := uint8(recordValid)
	if record.HasDewPoint {
		flags |= recordHasDewPoint
	}
	if record.HasGust {
		flags |= recordHasGust
	}
	if record.HasSolar {
		flags |= recordHasSolar
	}
	if record.HasCloud {
		flags |= recordHasCloud
	}
	if record.HasHumidity {
		flags |= recordHasHumidity
	}
	if record.HasWetTime {
		flags |= recordHasWetTime
	}
	return flags
}
