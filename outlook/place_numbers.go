// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package outlook

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"nice-weather-finder/internal/placenumbers"
)

// forecastPlaceInputs carries the same compact days and monthly felt offset
// consumed by the app and Worker. The rendered overview is not a ranking input.
type forecastPlaceInputs struct {
	records   []byte
	offsets   *image.Gray
	ordinals  []int
	days, day int
}

func loadForecastPlaceInputs(ctx context.Context, source, staticSource string, pointer Pointer, day, month int) (forecastPlaceInputs, error) {
	var result forecastPlaceInputs
	cells, err := readSource(ctx, staticSource, "cells.bin")
	if err != nil {
		return result, err
	}
	body, err := readSource(ctx, staticSource, fmt.Sprintf("climate/offsets-%02d.png", month))
	if err != nil {
		return result, err
	}
	decoded, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	offsets, ok := decoded.(*image.Gray)
	if !ok || offsets.Bounds() != image.Rect(0, 0, gridWidth, gridHeight) {
		return result, fmt.Errorf("forecast place offsets must be a %d × %d greyscale image", gridWidth, gridHeight)
	}
	records, err := readSource(ctx, source, fmt.Sprintf("v6/forecast/%s/days.bin", pointer.Run))
	if err != nil {
		return result, err
	}
	if len(cells) == 0 || len(cells)%4 != 0 || len(records) != len(cells)/4*pointer.Days*15 {
		return result, fmt.Errorf("forecast cell/record sizes differ")
	}
	pairs := make([][2]uint16, len(cells)/4)
	for i := range pairs {
		pairs[i] = [2]uint16{binary.LittleEndian.Uint16(cells[i*4:]), binary.LittleEndian.Uint16(cells[i*4+2:])}
	}
	return forecastPlaceInputs{records: records, offsets: offsets, ordinals: placenumbers.CellOrdinals(pairs), days: pointer.Days, day: day}, nil
}
func (inputs forecastPlaceInputs) numbers(latitude, longitude float64, cached map[int]placenumbers.Numbers) (placenumbers.Numbers, bool, error) {
	footprint := placenumbers.MapPointWeights(latitude, longitude, func(index int) bool { return inputs.offsets.Pix[index] != 255 })
	if len(footprint) == 0 {
		return placenumbers.Numbers{}, false, nil
	}
	points := make([]placenumbers.WeightedNumbers, 0, len(footprint))
	for _, point := range footprint {
		numbers, found := cached[point.Index]
		if !found {
			row, column := point.Index/gridWidth, point.Index%gridWidth
			cell := inputs.ordinals[min(row/4, 179)*360+column/4]
			at := (cell*inputs.days + inputs.day) * 15
			packet := placenumbers.Packet{Source: "forecast", Records: [][]byte{inputs.records[at : at+15]}, OffsetsC: []float64{(float64(inputs.offsets.Pix[point.Index]) - 128) / 10}, Weights: []float64{1}}
			var err error
			numbers, err = placenumbers.PlaceNumbers(packet, placenumbers.Period{TotalDays: 1}, nil)
			if err != nil {
				return placenumbers.Numbers{}, false, err
			}
			cached[point.Index] = numbers
		}
		points = append(points, placenumbers.WeightedNumbers{Numbers: numbers, Weight: point.Weight})
	}
	return placenumbers.BlendPlaceNumbers(points), true, nil
}

func (inputs forecastPlaceInputs) judge(places []Place) error {
	cached := make(map[int]placenumbers.Numbers)
	for i := range places {
		place := &places[i]
		numbers, available, err := inputs.numbers(place.Latitude, place.Longitude, cached)
		if err != nil {
			return err
		}
		if !available {
			return fmt.Errorf("forecast place %s has no map footprint", place.Name)
		}
		place.Score = int(math.Floor(numbers.Score))
		place.FloatScore = numbers.Score
		place.NiceDayCount = numbers.NiceDayCount
		place.PerfectDayCount = numbers.PerfectDayCount
	}
	return nil
}
