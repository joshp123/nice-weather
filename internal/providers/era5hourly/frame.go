// Package era5hourly validates pinned hourly ERA5 frames and converts them
// into the canonical daily weather boundary.
package era5hourly

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

const (
	GridRows       = 721
	GridColumns    = 1440
	FieldCount     = 7
	SnapshotID     = "C2QSEC9FP3SJ6MT4D94G"
	NativeDetailKM = 30
)

var sourceContractSHA256 = [sha256.Size]byte{
	0x06, 0x0a, 0x69, 0x8c, 0xd6, 0x62, 0xcd, 0x89,
	0x38, 0x75, 0x73, 0xfa, 0x3a, 0xef, 0x3b, 0xd4,
	0x59, 0xa9, 0xda, 0x28, 0x15, 0x46, 0x95, 0x75,
	0x98, 0x0e, 0xcd, 0x3c, 0xca, 0x3e, 0x10, 0xc8,
}

// Frame is one complete source hour. Fields are float32 source values in
// row-major latitude/longitude order. Accumulated rain and solar values cover
// the one-hour interval ending at ValidTime.
type Frame struct {
	ValidTime     time.Time
	Rows, Columns int
	planOrder     bool
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

func allZero(input []byte) bool {
	for _, value := range input {
		if value != 0 {
			return false
		}
	}
	return true
}
