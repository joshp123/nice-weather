package agera5

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"
)

const (
	DefaultRoot          = "https://arco.datastores.ecmwf.int/cadl-arco-geo-001/arco/sis_agrometeorological_indicators/all/geoChunked.zarr"
	maxMetadataBytes     = 64 << 10
	timeChunkLength      = 2112
	latitudeChunkLength  = 16
	longitudeChunkLength = 32
	timeCoordinateChunk  = 17260
	latitudeCount        = 1801
	longitudeCount       = 3600
	fillValue            = float32(-9999)
)

var sourceFirstDate = time.Date(1979, time.January, 1, 0, 0, 0, 0, time.UTC)

// Field is one of the exact nine AgERA5 arrays used by the climate product.
type Field string

const (
	DaytimeMeanTemperature Field = "Temperature_Air_2m_Mean_Day_Time"
	MinimumTemperature     Field = "Temperature_Air_2m_Min_24h"
	MaximumTemperature     Field = "Temperature_Air_2m_Max_24h"
	MeanDewPoint           Field = "Dew_Point_Temperature_2m_Mean_24h"
	Precipitation          Field = "Precipitation_Flux"
	PrecipitationDuration  Field = "Precipitation_Duration_Fraction"
	SolarRadiation         Field = "Solar_Radiation_Flux"
	CloudCover             Field = "Cloud_Cover_Mean_24h"
	MeanWindSpeed          Field = "Wind_Speed_10m_Mean_24h"
)

var fieldContracts = []struct {
	field Field
	unit  string
}{
	{DaytimeMeanTemperature, "K"},
	{MinimumTemperature, "K"},
	{MaximumTemperature, "K"},
	{MeanDewPoint, "K"},
	{Precipitation, "mm d-1"},
	{PrecipitationDuration, "1.0"},
	{SolarRadiation, "J m-2 d-1"},
	{CloudCover, "(0-1)"},
	{MeanWindSpeed, "m s-1"},
}

func RequiredFields() []Field {
	result := make([]Field, len(fieldContracts))
	for index, contract := range fieldContracts {
		result[index] = contract.field
	}
	return result
}

// Metadata is the validated AgERA5 layout. Planning remains disabled until the
// coordinate chunks prove the source epoch and axis order.
type Metadata struct {
	timeLength       int
	coordinatesValid bool
}

func (m Metadata) TimeLength() int { return m.timeLength }

type consolidatedMetadata struct {
	Format   int                        `json:"zarr_consolidated_format"`
	Metadata map[string]json.RawMessage `json:"metadata"`
}

type arrayDescriptor struct {
	Chunks             []int           `json:"chunks"`
	Compressor         compressor      `json:"compressor"`
	DType              string          `json:"dtype"`
	FillValue          json.RawMessage `json:"fill_value"`
	Filters            json.RawMessage `json:"filters"`
	Order              string          `json:"order"`
	Shape              []int           `json:"shape"`
	ZarrFormat         int             `json:"zarr_format"`
	DimensionSeparator *string         `json:"dimension_separator,omitempty"`
}

type compressor struct {
	BlockSize int    `json:"blocksize"`
	Level     int    `json:"clevel"`
	Name      string `json:"cname"`
	ID        string `json:"id"`
	Shuffle   int    `json:"shuffle"`
}

type fieldAttributes struct {
	Dimensions  []string `json:"_ARRAY_DIMENSIONS"`
	GridMapping string   `json:"grid_mapping"`
	Unit        string   `json:"units"`
	Missing     float64  `json:"missing_value"`
}

type coordinateAttributes struct {
	Dimensions []string `json:"_ARRAY_DIMENSIONS"`
	Unit       string   `json:"units"`
	Calendar   string   `json:"calendar"`
}

// parseMetadata binds the adapter to the measured Zarr-v2 field contract.
// Unrelated source arrays and descriptive attributes are ignored.
func parseMetadata(reader io.Reader) (Metadata, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
	if err != nil {
		return Metadata{}, fmt.Errorf("read AgERA5 metadata: %w", err)
	}
	if len(data) > maxMetadataBytes {
		return Metadata{}, fmt.Errorf("AgERA5 metadata exceeds %d-byte limit", maxMetadataBytes)
	}
	var document consolidatedMetadata
	if err := decodeStrict(data, &document); err != nil {
		return Metadata{}, fmt.Errorf("parse AgERA5 metadata: %w", err)
	}
	if document.Format != 1 || document.Metadata == nil {
		return Metadata{}, errors.New("AgERA5 requires consolidated metadata format 1")
	}

	timeLength := 0
	for _, contract := range fieldContracts {
		descriptor, err := parseArrayDescriptor(document.Metadata[string(contract.field)+"/.zarray"])
		if err != nil {
			return Metadata{}, fmt.Errorf("AgERA5 array %s: %w", contract.field, err)
		}
		if err := validateWeatherArray(descriptor); err != nil {
			return Metadata{}, fmt.Errorf("AgERA5 array %s: %w", contract.field, err)
		}
		if timeLength == 0 {
			timeLength = descriptor.Shape[0]
		} else if descriptor.Shape[0] != timeLength {
			return Metadata{}, fmt.Errorf("AgERA5 array %s has a different time length", contract.field)
		}
		if err := validateFieldAttributes(document.Metadata[string(contract.field)+"/.zattrs"], contract.unit); err != nil {
			return Metadata{}, fmt.Errorf("AgERA5 array %s attributes: %w", contract.field, err)
		}
	}
	coordinates := []struct {
		name  string
		dtype string
		shape int
		chunk int
		unit  string
	}{
		{"time", "<i4", timeLength, timeCoordinateChunk, "days since 1900-01-01"},
		{"latitude", "<f8", latitudeCount, latitudeCount, "degrees_north"},
		{"longitude", "<f8", longitudeCount, longitudeCount, "degrees_east"},
	}
	for _, coordinate := range coordinates {
		if err := validateCoordinateMetadata(document.Metadata, coordinate.name, coordinate.dtype, coordinate.shape, coordinate.chunk, coordinate.unit); err != nil {
			return Metadata{}, err
		}
	}
	return Metadata{timeLength: timeLength}, nil
}

func parseArrayDescriptor(raw json.RawMessage) (arrayDescriptor, error) {
	if len(raw) == 0 {
		return arrayDescriptor{}, errors.New("missing .zarray metadata")
	}
	var descriptor arrayDescriptor
	if err := decodeStrict(raw, &descriptor); err != nil {
		return arrayDescriptor{}, err
	}
	return descriptor, nil
}

func validateWeatherArray(value arrayDescriptor) error {
	if !slices.Equal(value.Chunks, []int{timeChunkLength, latitudeChunkLength, longitudeChunkLength}) || len(value.Shape) != 3 || value.Shape[0] <= 0 || value.Shape[1] != latitudeCount || value.Shape[2] != longitudeCount {
		return fmt.Errorf("unexpected shape %v or chunks %v", value.Shape, value.Chunks)
	}
	if value.DType != "<f4" || value.Order != "C" || value.ZarrFormat != 2 {
		return fmt.Errorf("requires little-endian float32 C-order Zarr v2, got dtype=%q order=%q version=%d", value.DType, value.Order, value.ZarrFormat)
	}
	if !bytes.Equal(bytes.TrimSpace(value.FillValue), []byte("-9999.0")) && !bytes.Equal(bytes.TrimSpace(value.FillValue), []byte("-9999")) {
		return fmt.Errorf("unexpected fill value %s", value.FillValue)
	}
	if !bytes.Equal(bytes.TrimSpace(value.Filters), []byte("null")) {
		return errors.New("filters must be null")
	}
	if value.Compressor != (compressor{BlockSize: 0, Level: 5, Name: "lz4", ID: "blosc", Shuffle: 1}) {
		return fmt.Errorf("requires Blosc LZ4 level 5 byte-shuffle with automatic block size, got %+v", value.Compressor)
	}
	if value.DimensionSeparator != nil && *value.DimensionSeparator != "." {
		return fmt.Errorf("unsupported chunk-key separator %q", *value.DimensionSeparator)
	}
	return nil
}

func validateFieldAttributes(raw json.RawMessage, unit string) error {
	if len(raw) == 0 {
		return errors.New("missing .zattrs metadata")
	}
	var value fieldAttributes
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if !slices.Equal(value.Dimensions, []string{"time", "latitude", "longitude"}) || value.GridMapping != "crs" || value.Unit != unit || math.IsNaN(value.Missing) || math.IsInf(value.Missing, 0) || value.Missing != -9999 {
		return errors.New("unexpected dimensions, units, grid mapping or missing value")
	}
	return nil
}

func validateCoordinateMetadata(values map[string]json.RawMessage, name, dtype string, shape, chunk int, unit string) error {
	descriptor, err := parseArrayDescriptor(values[name+"/.zarray"])
	if err != nil {
		return fmt.Errorf("AgERA5 coordinate %s: %w", name, err)
	}
	if descriptor.DType != dtype || descriptor.Order != "C" || descriptor.ZarrFormat != 2 || !slices.Equal(descriptor.Shape, []int{shape}) || !slices.Equal(descriptor.Chunks, []int{chunk}) || !bytes.Equal(bytes.TrimSpace(descriptor.Filters), []byte("null")) || descriptor.Compressor != (compressor{BlockSize: 0, Level: 5, Name: "lz4", ID: "blosc", Shuffle: 1}) || (descriptor.DimensionSeparator != nil && *descriptor.DimensionSeparator != ".") {
		return fmt.Errorf("AgERA5 coordinate %s has an unexpected Zarr layout", name)
	}
	var attributes coordinateAttributes
	if err := json.Unmarshal(values[name+"/.zattrs"], &attributes); err != nil {
		return fmt.Errorf("AgERA5 coordinate %s attributes: %w", name, err)
	}
	if !slices.Equal(attributes.Dimensions, []string{name}) || attributes.Unit != unit || (name == "time" && attributes.Calendar != "proleptic_gregorian") {
		return fmt.Errorf("AgERA5 coordinate %s has unexpected dimensions, units or calendar", name)
	}
	return nil
}

func (m Metadata) bindCoordinates(timeChunks [][]byte, latitudeChunk, longitudeChunk []byte) (Metadata, error) {
	if m.timeLength <= 0 || len(timeChunks) != blockCount(m.timeLength, timeCoordinateChunk) {
		return Metadata{}, errors.New("AgERA5 time coordinate chunks are incomplete")
	}
	epoch := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)
	expectedFirstDay := daysBetween(epoch, sourceFirstDate)
	for chunkIndex, compressed := range timeChunks {
		decoded, err := decodeBlosc(compressed, timeCoordinateChunk*4, 4)
		if err != nil {
			return Metadata{}, fmt.Errorf("decode AgERA5 time coordinate chunk %d: %w", chunkIndex, err)
		}
		logicalCount := min(timeCoordinateChunk, m.timeLength-chunkIndex*timeCoordinateChunk)
		for offset := 0; offset < logicalCount; offset++ {
			actual := int(int32(binary.LittleEndian.Uint32(decoded[offset*4:])))
			expected := expectedFirstDay + chunkIndex*timeCoordinateChunk + offset
			if actual != expected {
				return Metadata{}, fmt.Errorf("AgERA5 time coordinate %d is %d, want %d", chunkIndex*timeCoordinateChunk+offset, actual, expected)
			}
		}
	}
	if err := validateFloat64Axis(latitudeChunk, latitudeCount, -90, 0.1, "latitude"); err != nil {
		return Metadata{}, err
	}
	if err := validateFloat64Axis(longitudeChunk, longitudeCount, -180, 0.1, "longitude"); err != nil {
		return Metadata{}, err
	}
	m.coordinatesValid = true
	return m, nil
}

func validateFloat64Axis(compressed []byte, count int, first, step float64, name string) error {
	decoded, err := decodeBlosc(compressed, count*8, 8)
	if err != nil {
		return fmt.Errorf("decode AgERA5 %s coordinate: %w", name, err)
	}
	for index := 0; index < count; index++ {
		actual := math.Float64frombits(binary.LittleEndian.Uint64(decoded[index*8:]))
		expected := first + float64(index)*step
		if math.IsNaN(actual) || math.IsInf(actual, 0) || math.Abs(actual-expected) > 1e-9 {
			return fmt.Errorf("AgERA5 %s coordinate %d is %.12g, want %.12g", name, index, actual, expected)
		}
	}
	return nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON value %v", token)
	}
	return nil
}

// BlockCoordinate identifies one 16-row by 32-column source-grid block.
type BlockCoordinate struct {
	Latitude  int
	Longitude int
}

// ChunkRef identifies one field chunk. FirstDate and LastDate are the dates
// requested inside this time chunk, inclusive.
type ChunkRef struct {
	field       Field
	time        int
	block       BlockCoordinate
	firstDate   time.Time
	lastDate    time.Time
	firstOffset int
	lastOffset  int
	timeLength  int
}

func (reference ChunkRef) Key() string {
	return fmt.Sprintf("%s/%d.%d.%d", reference.field, reference.time, reference.block.Latitude, reference.block.Longitude)
}

func (reference ChunkRef) Field() Field           { return reference.field }
func (reference ChunkRef) FirstDate() time.Time   { return reference.firstDate }
func (reference ChunkRef) LastDate() time.Time    { return reference.lastDate }
func (reference ChunkRef) Block() BlockCoordinate { return reference.block }

// PlanChunks returns exact time-major keys for an inclusive UTC date range and
// one source-grid block.
func (m Metadata) PlanChunks(firstDate, lastDate time.Time, block BlockCoordinate) ([]ChunkRef, error) {
	if m.timeLength <= 0 || !m.coordinatesValid {
		return nil, errors.New("coordinate-validated AgERA5 metadata is required")
	}
	if !isUTCDate(firstDate) || !isUTCDate(lastDate) || lastDate.Before(firstDate) {
		return nil, errors.New("AgERA5 range must be inclusive UTC calendar dates in ascending order")
	}
	if block.Latitude < 0 || block.Latitude >= blockCount(latitudeCount, latitudeChunkLength) || block.Longitude < 0 || block.Longitude >= blockCount(longitudeCount, longitudeChunkLength) {
		return nil, fmt.Errorf("AgERA5 spatial block is outside the 113 by 113 chunk grid: %+v", block)
	}
	storeLastDate := sourceFirstDate.AddDate(0, 0, m.timeLength-1)
	if firstDate.Before(sourceFirstDate) || lastDate.After(storeLastDate) {
		return nil, fmt.Errorf("AgERA5 range %s to %s is outside %s to %s", firstDate.Format(time.DateOnly), lastDate.Format(time.DateOnly), sourceFirstDate.Format(time.DateOnly), storeLastDate.Format(time.DateOnly))
	}
	firstChunk := daysBetween(sourceFirstDate, firstDate) / timeChunkLength
	lastChunk := daysBetween(sourceFirstDate, lastDate) / timeChunkLength
	result := make([]ChunkRef, 0, (lastChunk-firstChunk+1)*len(fieldContracts))
	for timeChunk := firstChunk; timeChunk <= lastChunk; timeChunk++ {
		requestedFirst, requestedLast := firstDate, lastDate
		chunkFirst := sourceFirstDate.AddDate(0, 0, timeChunk*timeChunkLength)
		chunkLast := chunkFirst.AddDate(0, 0, timeChunkLength-1)
		if chunkFirst.After(requestedFirst) {
			requestedFirst = chunkFirst
		}
		if chunkLast.Before(requestedLast) {
			requestedLast = chunkLast
		}
		for _, contract := range fieldContracts {
			result = append(result, ChunkRef{
				field: contract.field, time: timeChunk, block: block,
				firstDate: requestedFirst, lastDate: requestedLast,
				firstOffset: daysBetween(chunkFirst, requestedFirst),
				lastOffset:  daysBetween(chunkFirst, requestedLast),
				timeLength:  m.timeLength,
			})
		}
	}
	return result, nil
}

func isUTCDate(value time.Time) bool {
	return value.Location() == time.UTC && value.Hour() == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

func daysBetween(first, last time.Time) int { return int(last.Sub(first) / (24 * time.Hour)) }
func blockCount(length, chunk int) int      { return (length + chunk - 1) / chunk }

func validateReference(reference ChunkRef) error {
	if !slices.Contains(RequiredFields(), reference.field) {
		return fmt.Errorf("unknown AgERA5 field %q", reference.field)
	}
	logicalTimeCount := min(timeChunkLength, reference.timeLength-reference.time*timeChunkLength)
	if reference.timeLength <= 0 || reference.time < 0 || reference.time >= blockCount(reference.timeLength, timeChunkLength) || reference.block.Latitude < 0 || reference.block.Latitude >= blockCount(latitudeCount, latitudeChunkLength) || reference.block.Longitude < 0 || reference.block.Longitude >= blockCount(longitudeCount, longitudeChunkLength) || reference.firstOffset < 0 || reference.lastOffset < reference.firstOffset || reference.lastOffset >= logicalTimeCount || !isUTCDate(reference.firstDate) || !isUTCDate(reference.lastDate) || daysBetween(reference.firstDate, reference.lastDate) != reference.lastOffset-reference.firstOffset {
		return errors.New("AgERA5 chunk reference was not created from validated metadata")
	}
	return nil
}
