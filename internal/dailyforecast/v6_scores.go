package dailyforecast

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	scorebands "nice-weather-finder/hosting/sites"
	"os"
	"path/filepath"
	"time"
)

type v6ScoreCurve [][2]float64

var (
	v6ColdGrade      = v6ScoreCurve{{9, 0}, {11, 3}, {13, 10}, {15, 22}, {17, 40}, {19, 62}, {21, 85}, {23, 100}}
	v6HeatGrade      = v6ScoreCurve{{28, 100}, {31, 85}, {34, 60}, {37, 30}, {40, 10}, {43, 0}}
	v6EveningGrade   = v6ScoreCurve{{4, 0}, {8, 30}, {12, 65}, {16, 90}, {19, 100}}
	v6RainFactor     = v6ScoreCurve{{0, 1}, {.5, .95}, {1, .88}, {2, .74}, {4, .5}, {8, .22}, {15, .05}}
	v6WindFactor     = v6ScoreCurve{{15, 1}, {20, .97}, {25, .92}, {30, .85}, {40, .65}, {50, .4}, {65, .15}}
	v6HumidityFactor = v6ScoreCurve{{16, 1}, {18, .95}, {20, .85}, {22, .65}, {24, .4}, {26, .2}, {28, .1}}
	v6SunFactor      = v6ScoreCurve{{0, .75}, {.3, .85}, {.6, .95}, {.8, 1}}
)

func (curve v6ScoreCurve) at(x float64) float64 {
	if x <= curve[0][0] {
		return curve[0][1]
	}
	for i := 1; i < len(curve); i++ {
		if x <= curve[i][0] {
			left, right := curve[i-1], curve[i]
			return left[1] + float64((right[1]-left[1])*(x-left[0])/(right[0]-left[0]))
		}
	}
	return curve[len(curve)-1][1]
}

func v6HourlyFelt(values []float64, keys []int, hour int) float64 {
	if hour <= keys[0] {
		return values[0]
	}
	for i := 1; i < len(keys); i++ {
		if hour <= keys[i] {
			return values[i-1] + float64((values[i]-values[i-1])*float64(hour-keys[i-1])/float64(keys[i]-keys[i-1]))
		}
	}
	return values[len(values)-1]
}

func v6DefaultDayLevels(record []byte) (scores [11]float64) {
	daylight := max(1, int(record[14]))
	var decodedSun [6]float64
	var decodedShade [4]float64
	for i := range decodedSun {
		decodedSun[i] = float64(record[i])/3 - 30
	}
	for i := range decodedShade {
		decodedShade[i] = float64(record[6+i])/3 - 30
	}
	var sun, shade [11]float64
	for hour := range 11 {
		sun[hour] = v6HourlyFelt(decodedSun[:], []int{0, 2, 4, 6, 8, 10}, hour)
		shade[hour] = v6HourlyFelt(decodedShade[:], []int{2, 5, 8, 11}, hour)
	}
	rainFactor := v6RainFactor.at(math.Pow(float64(record[11])/50, 2))
	windFactor := v6WindFactor.at(float64(record[12]) / 2)
	humidityFactor := v6HumidityFactor.at(float64(record[10])/3 - 30)
	sunFactor := v6SunFactor.at(float64(record[13]) / float64(daylight))
	for level := range scores {
		offset := float64(-15 + 3*level)
		daytime := 0.0
		for hour := 0; hour < daylight; hour++ {
			daytime += min(v6ColdGrade.at(sun[hour]+offset), v6HeatGrade.at(shade[hour]+offset))
		}
		at21 := decodedShade[3] + offset
		evening := min(v6EveningGrade.at(at21), v6HeatGrade.at(at21))
		warmth := (daytime + 2*evening) / float64(daylight+2)
		scores[level] = warmth * rainFactor * windFactor * humidityFactor * sunFactor
	}
	return
}

// v6DefaultDayReasonLevels follows the browser's packed day-reason pass without changing its scores.
func v6DefaultDayReasonLevels(record []byte) (reasons [11]byte) {
	daylight := max(1, int(record[14]))
	var decodedSun [6]float64
	var decodedShade [4]float64
	for i := range decodedSun {
		decodedSun[i] = float64(record[i])/3 - 30
	}
	for i := range decodedShade {
		decodedShade[i] = float64(record[6+i])/3 - 30
	}
	rainFactor := v6RainFactor.at(math.Pow(float64(record[11])/50, 2))
	windFactor := v6WindFactor.at(float64(record[12]) / 2)
	humidityFactor := v6HumidityFactor.at(float64(record[10])/3 - 30)
	sunFactor := v6SunFactor.at(float64(record[13]) / float64(daylight))
	for level := range reasons {
		offset := float64(-15 + 3*level)
		daytime, lostToCold, lostToHeat := 0.0, 0.0, 0.0
		for hour := 0; hour < daylight; hour++ {
			cold := v6ColdGrade.at(v6HourlyFelt(decodedSun[:], []int{0, 2, 4, 6, 8, 10}, hour) + offset)
			heat := v6HeatGrade.at(v6HourlyFelt(decodedShade[:], []int{2, 5, 8, 11}, hour) + offset)
			if cold <= heat {
				lostToCold += 100 - cold
			} else {
				lostToHeat += 100 - heat
			}
			daytime += min(cold, heat)
		}
		at21 := decodedShade[3] + offset
		eveningCold, eveningHeat := v6EveningGrade.at(at21), v6HeatGrade.at(at21)
		if eveningCold <= eveningHeat {
			lostToCold += 2 * (100 - eveningCold)
		} else {
			lostToHeat += 2 * (100 - eveningHeat)
		}
		warmth := (daytime + 2*min(eveningCold, eveningHeat)) / float64(daylight+2)
		if lostToHeat > lostToCold {
			reasons[level] = 1
		}
		smallest := warmth / 100
		for index, factor := range [...]float64{rainFactor, windFactor, humidityFactor, sunFactor} {
			if factor < smallest {
				reasons[level], smallest = byte(index+2), factor
			}
		}
	}
	return
}

func v6FineDefaultReason(index int, offset byte, ordinals []int, reasons [][11]byte) byte {
	row, column := index/Width, index%Width
	ordinal := ordinals[min(row/4, 179)*360+column/4]
	level := max(0, min(10, ((float64(offset)-128)/10+15)/3))
	lower := min(int(math.Floor(level)), 9)
	if level-float64(lower) < .5 {
		return reasons[ordinal][lower]
	}
	return reasons[ordinal][lower+1]
}

func v6ReasonImageByte(reason byte, score float64) byte {
	severity := byte(0)
	if score < v6ScoreThresholds[3] {
		severity = 1
		if score < v6ScoreThresholds[2] {
			severity = 2
			if score < v6ScoreThresholds[1] {
				severity = 3
			}
		}
	}
	return reason*4 + severity
}

func v6NearestLandOrdinals(cells [][2]uint16) []int {
	land := make([]int, 180*360)
	for i := range land {
		land[i] = -1
	}
	for ordinal, cell := range cells {
		land[int(cell[0])*360+int(cell[1])] = ordinal
	}
	rowNearest := make([]int, len(land))
	rowDistance := make([]int, len(land))
	for row := range 180 {
		for column := range 360 {
			index := row*360 + column
			rowNearest[index], rowDistance[index] = -1, 1<<30
			for radius := 0; radius <= 180; radius++ {
				left := land[row*360+(column-radius+360)%360]
				right := land[row*360+(column+radius)%360]
				if left >= 0 || right >= 0 {
					rowNearest[index], rowDistance[index] = right, radius*radius
					if left >= 0 {
						rowNearest[index] = left
					}
					break
				}
			}
		}
	}
	ordinals := make([]int, len(land))
	for row := range 180 {
		for column := range 360 {
			bestDistance := 1 << 30
			for sourceRow := range 180 {
				index := sourceRow*360 + column
				distance := (sourceRow-row)*(sourceRow-row) + rowDistance[index]
				if distance < bestDistance {
					bestDistance = distance
					ordinals[row*360+column] = rowNearest[index]
				}
			}
		}
	}
	return ordinals
}

func v6FineDefaultScore(index int, offset byte, ordinals []int, levels [][11]float64) float64 {
	row, column := index/Width, index%Width
	ordinal := ordinals[min(row/4, 179)*360+column/4]
	level := max(0, min(10, ((float64(offset)-128)/10+15)/3))
	lower := min(int(math.Floor(level)), 9)
	fraction := level - float64(lower)
	return levels[ordinal][lower]*(1-fraction) + levels[ordinal][lower+1]*fraction
}

var v6ScoreThresholds = scorebands.Thresholds

func v6FillTemperatureOffsets(offsets []byte) ([]byte, error) {
	const lastLatitudeRow = 600 // 60°S; Antarctica stays excluded.
	const infinity = 1 << 30
	nearestColumn := make([]int, Width*(lastLatitudeRow+1))
	horizontalDistance := make([]int, len(nearestColumn))
	for index := range horizontalDistance {
		horizontalDistance[index] = infinity
	}
	for row := 0; row <= lastLatitudeRow; row++ {
		lastSource := -infinity
		for column := -Width; column < Width; column++ {
			if offsets[row*Width+(column+Width)%Width] != 255 {
				lastSource = column
			}
			if column >= 0 && lastSource != -infinity {
				index := row*Width + column
				nearestColumn[index] = (lastSource + Width) % Width
				horizontalDistance[index] = (column - lastSource) * (column - lastSource)
			}
		}
		lastSource = infinity
		for column := 2*Width - 1; column >= 0; column-- {
			if offsets[row*Width+column%Width] != 255 {
				lastSource = column
			}
			if column < Width && lastSource != infinity {
				index := row*Width + column
				distance := (column - lastSource) * (column - lastSource)
				if distance < horizontalDistance[index] {
					nearestColumn[index] = lastSource % Width
					horizontalDistance[index] = distance
				}
			}
		}
	}
	filled := bytes.Repeat([]byte{255}, Width*Height)
	sourceRows := make([]int, lastLatitudeRow+1)
	firstRows := make([]int, len(sourceRows))
	for column := range Width {
		last := -1
		for row := 0; row <= lastLatitudeRow; row++ {
			if horizontalDistance[row*Width+column] == infinity {
				continue
			}
			first := -infinity
			for last >= 0 {
				previous := sourceRows[last]
				numerator := horizontalDistance[row*Width+column] + row*row - horizontalDistance[previous*Width+column] - previous*previous
				first = int(math.Floor(float64(numerator)/float64(2*(row-previous)))) + 1
				if first > firstRows[last] {
					break
				}
				last--
			}
			if last < 0 {
				first = -infinity
			}
			last++
			sourceRows[last], firstRows[last] = row, first
		}
		if last < 0 {
			return nil, fmt.Errorf("v6 temperature offsets have no land north of 60°S")
		}
		candidate := 0
		for row := 0; row <= lastLatitudeRow; row++ {
			for candidate < last && firstRows[candidate+1] <= row {
				candidate++
			}
			sourceRow := sourceRows[candidate]
			filled[row*Width+column] = offsets[sourceRow*Width+nearestColumn[sourceRow*Width+column]]
		}
	}
	return filled, nil
}

// RegenerateScoreImages redraws an existing forecast without fetching ECMWF or changing its day records.
func RegenerateScoreImages(ctx context.Context, source, staticSource, output string, log io.Writer) error {
	encoded, err := readV6StaticFile(ctx, source, "v6/forecast/current.json")
	if err != nil {
		return err
	}
	var pointer v6Pointer
	if err := json.Unmarshal(encoded, &pointer); err != nil {
		return err
	}
	if pointer.Run == "" || filepath.Base(pointer.Run) != pointer.Run || pointer.Days != v6Days {
		return fmt.Errorf("invalid v6 forecast pointer")
	}
	run, err := time.Parse("2006-01-02", pointer.FirstDate)
	if err != nil {
		return err
	}
	cellBytes, err := readV6StaticFile(ctx, staticSource, "cells.bin")
	if err != nil {
		return err
	}
	if len(cellBytes) == 0 || len(cellBytes)%4 != 0 {
		return fmt.Errorf("invalid v6 cells.bin size")
	}
	cells := make([][2]uint16, len(cellBytes)/4)
	for index := range cells {
		cells[index] = [2]uint16{binary.LittleEndian.Uint16(cellBytes[index*4:]), binary.LittleEndian.Uint16(cellBytes[index*4+2:])}
		if cells[index][0] >= 180 || cells[index][1] >= 360 {
			return fmt.Errorf("invalid v6 cell %d", index)
		}
	}
	relativeDirectory := "v6/forecast/" + pointer.Run
	records, err := readV6StaticFile(ctx, source, relativeDirectory+"/days.bin")
	if err != nil {
		return err
	}
	if len(records) != len(cells)*v6Days*15 {
		return fmt.Errorf("invalid v6 days.bin size: got %d, want %d", len(records), len(cells)*v6Days*15)
	}
	directory := filepath.Join(output, filepath.FromSlash(relativeDirectory))
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	return writeV6ScoreImages(ctx, staticSource, directory, cells, records, run, log)
}

func v6CanonicalScore(score float64) float64 {
	return math.Floor(score*10000+.5) / 10000
}

func v6TypicalDayScore(thresholdCounts [7]byte, days byte) float64 {
	for threshold := len(v6ScoreThresholds) - 2; threshold >= 0; threshold-- {
		share := float64(thresholdCounts[threshold]) / float64(days)
		if share < .5-1e-9 {
			continue
		}
		if threshold == 5 {
			return v6CanonicalScore(max(85, 85+(share-.5)*30))
		}
		nextShare := float64(thresholdCounts[threshold+1]) / float64(days)
		lower, upper := v6ScoreThresholds[threshold], v6ScoreThresholds[threshold+1]
		return v6CanonicalScore(max(lower, lower+(upper-lower)*(share-.5)/(share-nextShare)))
	}
	return 0
}

func writeV6ScoreImages(ctx context.Context, staticSource, outputDirectory string, cells [][2]uint16, records []byte, run time.Time, log io.Writer) error {
	started := time.Now()
	offsets := map[time.Month][]byte{}
	for day := range v6Days {
		month := run.AddDate(0, 0, day).Month()
		if _, loaded := offsets[month]; loaded {
			continue
		}
		name := fmt.Sprintf("climate/offsets-%02d.png", month)
		body, err := readV6StaticFile(ctx, staticSource, name)
		if err != nil {
			return err
		}
		decoded, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("v6 %s: %w", name, err)
		}
		gray, ok := decoded.(*image.Gray)
		if !ok || gray.Bounds() != image.Rect(0, 0, Width, Height) {
			return fmt.Errorf("v6 %s must be %d × %d, 8-bit greyscale", name, Width, Height)
		}
		offsets[month], err = v6FillTemperatureOffsets(gray.Pix)
		if err != nil {
			return fmt.Errorf("v6 %s: %w", name, err)
		}
	}
	ordinals := v6NearestLandOrdinals(cells)
	levels := make([][11]float64, len(cells))
	reasonLevels := make([][11]byte, len(cells))
	pointsLostByReason := make([][6]float64, Width*Height)
	thresholdCounts := make([][7]byte, Width*Height)
	counts := make([]byte, Width*Height)
	var totalBytes int64
	writeImage := func(name string, pixels []byte) error {
		var encoded bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&encoded, &image.Gray{Pix: pixels, Stride: Width, Rect: image.Rect(0, 0, Width, Height)}); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outputDirectory, name), encoded.Bytes(), 0644); err != nil {
			return err
		}
		totalBytes += int64(encoded.Len())
		return nil
	}
	for day := range v6Days {
		if err := ctx.Err(); err != nil {
			return err
		}
		for cell := range cells {
			start := (cell*v6Days + day) * 15
			levels[cell] = v6DefaultDayLevels(records[start : start+15])
			reasonLevels[cell] = v6DefaultDayReasonLevels(records[start : start+15])
		}
		pixels := bytes.Repeat([]byte{255}, Width*Height)
		reasonPixels := bytes.Repeat([]byte{255}, Width*Height)
		for index, offset := range offsets[run.AddDate(0, 0, day).Month()] {
			if offset == 255 {
				continue
			}
			score := v6CanonicalScore(v6FineDefaultScore(index, offset, ordinals, levels))
			pixels[index] = byte(math.Floor(score))
			reason := v6FineDefaultReason(index, offset, ordinals, reasonLevels)
			reasonPixels[index] = v6ReasonImageByte(reason, score)
			pointsLostByReason[index][reason] += max(0, 100-score) / v6Days
			for threshold, minimum := range v6ScoreThresholds {
				if score >= minimum {
					thresholdCounts[index][threshold]++
				}
			}
			counts[index]++
		}
		if err := writeImage(fmt.Sprintf("scores-typical-day-day-%02d.png", day), pixels); err != nil {
			return err
		}
		if err := writeImage(fmt.Sprintf("reasons-typical-day-day-%02d.png", day), reasonPixels); err != nil {
			return err
		}
	}
	pixels := bytes.Repeat([]byte{255}, Width*Height)
	reasonPixels := bytes.Repeat([]byte{255}, Width*Height)
	for index, count := range counts {
		if count == v6Days {
			score := v6TypicalDayScore(thresholdCounts[index], count)
			pixels[index] = byte(math.Floor(score))
			leading := byte(0)
			for reason := byte(1); reason < 6; reason++ {
				if pointsLostByReason[index][reason] > pointsLostByReason[index][leading] {
					leading = reason
				}
			}
			reasonPixels[index] = v6ReasonImageByte(leading, score)
		}
	}
	if err := writeImage("scores-typical-day-all.png", pixels); err != nil {
		return err
	}
	if err := writeImage("reasons-typical-day-all.png", reasonPixels); err != nil {
		return err
	}
	fmt.Fprintf(log, "v6 score and reason images: %d images; %d bytes; %s\n", 2*(v6Days+1), totalBytes, time.Since(started).Round(time.Millisecond))
	return nil
}
