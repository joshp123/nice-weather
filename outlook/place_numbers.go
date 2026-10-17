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
func (inputs forecastPlaceInputs) judge(places []Place) error {
	for i := range places {
		place := &places[i]
		index := place.PointIndex
		row, column := index/gridWidth, index%gridWidth
		cell := inputs.ordinals[min(row/4, 179)*360+column/4]
		at := (cell*inputs.days + inputs.day) * 15
		packet := placenumbers.Packet{Source: "forecast", Records: [][]byte{inputs.records[at : at+15]}, OffsetsC: []float64{(float64(inputs.offsets.Pix[index]) - 128) / 10}, Weights: []float64{1}}
		numbers, err := placenumbers.PlaceNumbers(packet, placenumbers.Period{TotalDays: 1}, nil)
		if err != nil {
			return err
		}
		place.Score = int(math.Floor(numbers.Score))
	}
	return nil
}
