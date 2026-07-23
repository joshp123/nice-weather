// Package era5hourly validates pinned hourly ERA5 frames and converts them
// into the canonical daily weather boundary.
package era5hourly

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	GridRows       = 721
	GridColumns    = 1440
	SourceHours    = 756048
	FieldCount     = 7
	HeaderBytes    = 128
	TrailerBytes   = sha256.Size
	scalarBytes    = 4
	SchemaVersion  = 1
	SnapshotID     = "C2QSEC9FP3SJ6MT4D94G"
	NativeDetailKM = 30

	frameMagic = "NWE5HR01"
)

var sourceContractSHA256 = [sha256.Size]byte{
	0x06, 0x0a, 0x69, 0x8c, 0xd6, 0x62, 0xcd, 0x89,
	0x38, 0x75, 0x73, 0xfa, 0x3a, 0xef, 0x3b, 0xd4,
	0x59, 0xa9, 0xda, 0x28, 0x15, 0x46, 0x95, 0x75,
	0x98, 0x0e, 0xcd, 0x3c, 0xca, 0x3e, 0x10, 0xc8,
}

var sourceEpoch = time.Date(1940, 1, 1, 0, 0, 0, 0, time.UTC)

// Frame is one complete source hour. Fields are float32 source values in
// row-major latitude/longitude order. Accumulated rain and solar values cover
// the one-hour interval ending at ValidTime.
type Frame struct {
	ValidTime     time.Time
	Rows, Columns int
	TemperatureK  []float32
	DewPointK     []float32
	RainM         []float32
	SolarJM2      []float32
	Cloud         []float32
	WindUMPS      []float32
	WindVMPS      []float32
}

func (frame Frame) fields() [FieldCount][]float32 {
	return [FieldCount][]float32{
		frame.TemperatureK,
		frame.DewPointK,
		frame.RainM,
		frame.SolarJM2,
		frame.Cloud,
		frame.WindUMPS,
		frame.WindVMPS,
	}
}

func (frame Frame) validateShape() error {
	if frame.Rows <= 0 || frame.Columns <= 0 || frame.Rows > GridRows || frame.Columns > GridColumns {
		return errors.New("ERA5 hourly frame has invalid geometry")
	}
	want := frame.Rows * frame.Columns
	for index, field := range frame.fields() {
		if len(field) != want {
			return fmt.Errorf("ERA5 hourly field %d has %d cells; want %d", index, len(field), want)
		}
	}
	return nil
}

// FrameSource emits complete consecutive source hours and ends with io.EOF.
type FrameSource interface {
	Next() (Frame, error)
}

// Decoder reads the fixed transport frame. It retains one decoded hour only.
type Decoder struct {
	input         io.Reader
	rows, columns int
	previousTime  time.Time
}

// NewDecoder accepts only the pinned worldwide ERA5 geometry.
func NewDecoder(input io.Reader) (*Decoder, error) {
	return newDecoder(input, GridRows, GridColumns)
}

func newDecoder(input io.Reader, rows, columns int) (*Decoder, error) {
	if input == nil || rows <= 0 || columns <= 0 || rows > GridRows || columns > GridColumns {
		return nil, errors.New("ERA5 hourly decoder requires a reader and valid geometry")
	}
	return &Decoder{input: input, rows: rows, columns: columns}, nil
}

// Next validates source identity, time, layout, finite float values and the
// payload checksum before returning a frame.
func (decoder *Decoder) Next() (Frame, error) {
	if decoder == nil || decoder.input == nil {
		return Frame{}, errors.New("ERA5 hourly decoder is unavailable")
	}
	header := make([]byte, HeaderBytes)
	n, err := io.ReadFull(decoder.input, header)
	if errors.Is(err, io.EOF) && n == 0 {
		return Frame{}, io.EOF
	}
	if err != nil {
		return Frame{}, fmt.Errorf("read ERA5 hourly header: %w", err)
	}
	if string(header[:8]) != frameMagic ||
		binary.LittleEndian.Uint16(header[8:10]) != SchemaVersion ||
		binary.LittleEndian.Uint16(header[10:12]) != HeaderBytes ||
		int(binary.LittleEndian.Uint16(header[12:14])) != decoder.rows ||
		int(binary.LittleEndian.Uint16(header[14:16])) != decoder.columns ||
		binary.LittleEndian.Uint16(header[16:18]) != FieldCount ||
		binary.LittleEndian.Uint16(header[18:20]) != scalarBytes {
		return Frame{}, errors.New("ERA5 hourly frame header or geometry changed")
	}
	payloadBytes := uint64(FieldCount * decoder.rows * decoder.columns * scalarBytes)
	if binary.LittleEndian.Uint64(header[28:36]) != payloadBytes {
		return Frame{}, errors.New("ERA5 hourly frame payload length changed")
	}
	if string(header[36:56]) != SnapshotID || !bytes.Equal(header[56:88], sourceContractSHA256[:]) || !allZero(header[88:]) {
		return Frame{}, errors.New("ERA5 hourly source contract changed")
	}
	unixSeconds := int64(binary.LittleEndian.Uint64(header[20:28]))
	if unixSeconds%3600 != 0 {
		return Frame{}, errors.New("ERA5 hourly valid time is not a whole UTC hour")
	}
	validTime := time.Unix(unixSeconds, 0).UTC()
	if validTime.Before(sourceEpoch) || !validTime.Before(sourceEpoch.Add(SourceHours*time.Hour)) {
		return Frame{}, errors.New("ERA5 hourly valid time is outside the pinned source axis")
	}
	if !decoder.previousTime.IsZero() && !validTime.Equal(decoder.previousTime.Add(time.Hour)) {
		return Frame{}, fmt.Errorf("ERA5 hourly frames are not consecutive: got %s after %s", validTime.Format(time.RFC3339), decoder.previousTime.Format(time.RFC3339))
	}

	cells := decoder.rows * decoder.columns
	raw := make([]byte, cells*scalarBytes)
	fields := [FieldCount][]float32{}
	digest := sha256.New()
	for fieldIndex := range fields {
		if _, err := io.ReadFull(decoder.input, raw); err != nil {
			return Frame{}, fmt.Errorf("read ERA5 hourly field %d: %w", fieldIndex, err)
		}
		if _, err := digest.Write(raw); err != nil {
			return Frame{}, fmt.Errorf("hash ERA5 hourly field %d: %w", fieldIndex, err)
		}
		values := make([]float32, cells)
		for index := range values {
			value := math.Float32frombits(binary.LittleEndian.Uint32(raw[index*scalarBytes:]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return Frame{}, fmt.Errorf("ERA5 hourly field %d contains a non-finite value at cell %d", fieldIndex, index)
			}
			values[index] = value
		}
		fields[fieldIndex] = values
	}
	trailer := make([]byte, TrailerBytes)
	if _, err := io.ReadFull(decoder.input, trailer); err != nil {
		return Frame{}, fmt.Errorf("read ERA5 hourly trailer: %w", err)
	}
	if !bytes.Equal(trailer, digest.Sum(nil)) {
		return Frame{}, errors.New("ERA5 hourly payload checksum mismatch")
	}
	decoder.previousTime = validTime
	return Frame{
		ValidTime: validTime, Rows: decoder.rows, Columns: decoder.columns,
		TemperatureK: fields[0], DewPointK: fields[1], RainM: fields[2],
		SolarJM2: fields[3], Cloud: fields[4], WindUMPS: fields[5], WindVMPS: fields[6],
	}, nil
}

func allZero(input []byte) bool {
	for _, value := range input {
		if value != 0 {
			return false
		}
	}
	return true
}
