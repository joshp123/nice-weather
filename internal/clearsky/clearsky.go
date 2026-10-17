// Package clearsky estimates forecast direct radiation from accumulated SSRD.
package clearsky

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"math"
	"sync"
)

//go:embed linke-quarter-degree.bin.gz
var compressedLinke []byte
var linkeOnce sync.Once
var linke []byte
var linkeError error

// Model holds the local atmospheric and terrain inputs, with no weather taste.
type Model struct {
	latitude, longitude, altitude, pressure float64
	monthly                                 [12]float64
}

// New uses pvlib's monthly Linke climatology sampled on the global 0.25° grid.
func New(latitude, longitude, altitude float64) (*Model, error) {
	linkeOnce.Do(func() {
		var reader *gzip.Reader
		reader, linkeError = gzip.NewReader(bytes.NewReader(compressedLinke))
		if linkeError != nil {
			return
		}
		defer reader.Close()
		linke, linkeError = io.ReadAll(reader)
		if linkeError == nil && len(linke) != 721*1440*12 {
			linkeError = fmt.Errorf("Linke grid has %d bytes", len(linke))
		}
	})
	if linkeError != nil {
		return nil, linkeError
	}
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, fmt.Errorf("coordinates outside Earth: %g,%g", latitude, longitude)
	}
	row := int(math.Round((90 - latitude) * 4))
	column := int(math.Round(positiveDegrees(longitude)*4)) % 1440
	var monthly [12]float64
	for month := range monthly {
		monthly[month] = float64(linke[(row*1440+column)*12+month]) / 20
	}
	return &Model{latitude: latitude, longitude: longitude, altitude: altitude, pressure: 100 * math.Pow((44331.514-altitude)/11880.516, 1/0.1902632), monthly: monthly}, nil
}

func (m *Model) turbidity(year, dayOfYear int) float64 {
	days := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		days[1] = 29
	}
	previousMiddle := -15.5
	previousValue := m.monthly[11]
	elapsed := 0
	for month, count := range days {
		middle := float64(elapsed) + float64(count)/2
		if float64(dayOfYear) <= middle {
			return previousValue + (m.monthly[month]-previousValue)*(float64(dayOfYear)-previousMiddle)/(middle-previousMiddle)
		}
		previousMiddle = middle
		previousValue = m.monthly[month]
		elapsed += count
	}
	return previousValue + (m.monthly[0]-previousValue)*(float64(dayOfYear)-previousMiddle)/(float64(elapsed)+15.5-previousMiddle)
}

func positiveDegrees(value float64) float64 { return math.Mod(math.Mod(value, 360)+360, 360) }
