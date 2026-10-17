package dailyforecast

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
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
	cellX := float64(index%Width)/4 - .5
	cellY := float64(index/Width)/4 - .5
	baseX, baseY := int(math.Floor(cellX)), int(math.Floor(cellY))
	fractionX, fractionY := cellX-float64(baseX), cellY-float64(baseY)
	level := max(0, min(10, ((float64(offset)-128)/10+15)/3))
	lower := min(int(math.Floor(level)), 9)
	fractionLevel := level - float64(lower)
	type contribution struct {
		key    int
		weight float64
	}
	var contributions [8]contribution
	count := 0
	for row := range 2 {
		for column := range 2 {
			ordinal := ordinals[max(0, min(179, baseY+row))*360+(baseX+column+360)%360]
			weightX, weightY := 1-fractionX, 1-fractionY
			if column == 1 {
				weightX = fractionX
			}
			if row == 1 {
				weightY = fractionY
			}
			spatialWeight := weightX * weightY
			for upper := range 2 {
				levelWeight := 1 - fractionLevel
				if upper == 1 {
					levelWeight = fractionLevel
				}
				weight := spatialWeight * levelWeight
				if weight == 0 {
					continue
				}
				key := ordinal*11 + lower + upper
				existing := 0
				for existing < count && contributions[existing].key != key {
					existing++
				}
				if existing == count {
					contributions[count].key = key
					count++
				}
				contributions[existing].weight += weight
			}
		}
	}
	score := 0.0
	for _, contribution := range contributions[:count] {
		score += float64(levels[contribution.key/11][contribution.key%11] * contribution.weight)
	}
	return score
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
		offsets[month] = gray.Pix
	}
	ordinals := v6NearestLandOrdinals(cells)
	levels := make([][11]float64, len(cells))
	sums := make([]float64, Width*Height)
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
		}
		pixels := bytes.Repeat([]byte{255}, Width*Height)
		for index, offset := range offsets[run.AddDate(0, 0, day).Month()] {
			if offset == 255 {
				continue
			}
			score := v6FineDefaultScore(index, offset, ordinals, levels)
			pixels[index] = byte(math.Floor(score + .5))
			sums[index] += score
			counts[index]++
		}
		if err := writeImage(fmt.Sprintf("scores-day-%02d.png", day), pixels); err != nil {
			return err
		}
	}
	pixels := bytes.Repeat([]byte{255}, Width*Height)
	for index, count := range counts {
		if count == v6Days {
			pixels[index] = byte(math.Floor(sums[index]/v6Days + .5))
		}
	}
	if err := writeImage("scores-all.png", pixels); err != nil {
		return err
	}
	fmt.Fprintf(log, "v6 score images: %d images; %d bytes; %s\n", v6Days+1, totalBytes, time.Since(started).Round(time.Millisecond))
	return nil
}
