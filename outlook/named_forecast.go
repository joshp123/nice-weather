// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package outlook

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"nice-weather-finder/internal/placenumbers"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type NamedCoordinate struct{ Latitude, Longitude float64 }

// ForecastNamedNumbers evaluates the same per-place spatial footprint as the
// app and Worker. Slot 14 retains the full stretch's median-before-spatial-blend.
func ForecastNamedNumbers(ctx context.Context, source, staticSource string, coordinates []NamedCoordinate, emit func(int, []placenumbers.Numbers, []bool) error) (Pointer, error) {
	var pointer Pointer
	body, err := readSource(ctx, source, "v6/forecast/current.json")
	if err != nil {
		return pointer, err
	}
	if err = json.Unmarshal(body, &pointer); err != nil {
		return pointer, err
	}
	if pointer.Run == "" || pointer.Days != 14 {
		return pointer, fmt.Errorf("invalid named forecast pointer")
	}
	firstDate, err := time.Parse("2006-01-02", pointer.FirstDate)
	if err != nil {
		return pointer, err
	}
	inputsByMonth := make(map[int]forecastPlaceInputs)
	inputsByDay := make([]forecastPlaceInputs, pointer.Days)
	for day := range pointer.Days {
		month := int(firstDate.AddDate(0, 0, day).Month())
		inputs, loaded := inputsByMonth[month]
		if !loaded {
			inputs, err = loadForecastPlaceInputs(ctx, source, staticSource, pointer, day, month)
			if err != nil {
				return pointer, err
			}
			inputsByMonth[month] = inputs
		}
		inputs.day = day
		inputsByDay[day] = inputs
	}
	for slot := 0; slot <= pointer.Days; slot++ {
		if err = ctx.Err(); err != nil {
			return pointer, err
		}
		cached := make(map[int]placenumbers.Numbers)
		numbers := make([]placenumbers.Numbers, len(coordinates))
		available := make([]bool, len(coordinates))
		for index, coordinate := range coordinates {
			if slot < pointer.Days {
				numbers[index], available[index], err = inputsByDay[slot].numbers(coordinate.Latitude, coordinate.Longitude, cached)
			} else {
				numbers[index], available[index], err = namedForecastStretch(inputsByDay, coordinate, cached)
			}
			if err != nil {
				return pointer, err
			}
		}
		if err = emit(slot, numbers, available); err != nil {
			return pointer, err
		}
	}
	return pointer, nil
}

func namedForecastStretch(inputs []forecastPlaceInputs, coordinate NamedCoordinate, cached map[int]placenumbers.Numbers) (placenumbers.Numbers, bool, error) {
	footprint := placenumbers.MapPointWeights(coordinate.Latitude, coordinate.Longitude, func(index int) bool {
		for _, input := range inputs {
			if input.offsets.Pix[index] == 255 {
				return false
			}
		}
		return true
	})
	if len(footprint) == 0 {
		return placenumbers.Numbers{}, false, nil
	}
	points := make([]placenumbers.WeightedNumbers, 0, len(footprint))
	for _, point := range footprint {
		numbers, found := cached[point.Index]
		if !found {
			row, column := point.Index/gridWidth, point.Index%gridWidth
			packet := placenumbers.Packet{Source: "forecast"}
			for _, input := range inputs {
				cell := input.ordinals[min(row/4, 179)*360+column/4]
				at := (cell*input.days + input.day) * 15
				packet.Records = append(packet.Records, input.records[at:at+15])
				packet.OffsetsC = append(packet.OffsetsC, (float64(input.offsets.Pix[point.Index])-128)/10)
				packet.Weights = append(packet.Weights, 1)
			}
			var err error
			numbers, err = placenumbers.PlaceNumbers(packet, placenumbers.Period{TotalDays: len(inputs)}, nil)
			if err != nil {
				return placenumbers.Numbers{}, false, err
			}
			cached[point.Index] = numbers
		}
		points = append(points, placenumbers.WeightedNumbers{Numbers: numbers, Weight: point.Weight})
	}
	return placenumbers.BlendPlaceNumbers(points), true, nil
}

// BuildNamedForecast writes one compact, gzip-compressed file in the completed
// run. All exact-coordinate catalogue entries are included, without population
// or country filtering. The climate build verifies every map label is in it.
func BuildNamedForecast(ctx context.Context, source, placesSource, staticSource, out string) (string, int64, error) {
	body, err := readSource(ctx, placesSource, "places.tsv")
	if err != nil {
		return "", 0, err
	}
	coordinates := []NamedCoordinate{}
	seen := map[NamedCoordinate]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			return "", 0, fmt.Errorf("invalid named place row")
		}
		lat, e := strconv.ParseFloat(fields[3], 64)
		if e != nil {
			return "", 0, e
		}
		lon, e := strconv.ParseFloat(fields[4], 64)
		if e != nil {
			return "", 0, e
		}
		coordinate := NamedCoordinate{lat, lon}
		if !seen[coordinate] {
			seen[coordinate] = true
			coordinates = append(coordinates, coordinate)
		}
	}
	sort.Slice(coordinates, func(i, j int) bool {
		if coordinates[i].Latitude != coordinates[j].Latitude {
			return coordinates[i].Latitude < coordinates[j].Latitude
		}
		return coordinates[i].Longitude < coordinates[j].Longitude
	})
	count := len(coordinates)
	plain := make([]byte, 32+count*8+count*15*15)
	copy(plain, "NWNP")
	binary.LittleEndian.PutUint16(plain[4:], 1)
	plain[6], plain[7] = 2, 15
	binary.LittleEndian.PutUint32(plain[8:], uint32(count))
	for index, coordinate := range coordinates {
		binary.LittleEndian.PutUint32(plain[32+index*8:], uint32(int32(math.Floor(coordinate.Latitude*1e5+.5))))
		binary.LittleEndian.PutUint32(plain[36+index*8:], uint32(int32(math.Floor(coordinate.Longitude*1e5+.5))))
	}
	pointer, err := ForecastNamedNumbers(ctx, source, staticSource, coordinates, func(slot int, numbers []placenumbers.Numbers, available []bool) error {
		start := 32 + count*8 + slot*count*15
		for index, value := range numbers {
			score := uint32(0xffffff)
			if available[index] {
				score = uint32(math.Floor(value.Score*10000 + .5))
			}
			at := start + index*3
			plain[at], plain[at+1], plain[at+2] = byte(score), byte(score>>8), byte(score>>16)
			for field, share := range []float64{value.NiceDayShare, value.PerfectDayShare, value.AwfulDayShare} {
				binary.LittleEndian.PutUint32(plain[start+count*3+(field*count+index)*4:], math.Float32bits(float32(share)))
			}
		}
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if _, err = writer.Write(plain); err != nil {
		return "", 0, err
	}
	if err = writer.Close(); err != nil {
		return "", 0, err
	}
	directory := filepath.Join(out, "v6", "forecast", pointer.Run)
	if err = os.MkdirAll(directory, 0755); err != nil {
		return "", 0, err
	}
	filename := filepath.Join(directory, "named.bin.gz")
	if err = os.WriteFile(filename, compressed.Bytes(), 0644); err != nil {
		return "", 0, err
	}
	manifest := map[string]any{"written_by": "AI, GPT-6.1 Sol, 2026.10.18", "format": "nwnp-v1", "run": pointer.Run, "places": count, "days": pointer.Days, "slots": 15, "bytes": compressed.Len(), "uncompressedBytes": len(plain), "sha256": fmt.Sprintf("%x", sha256.Sum256(compressed.Bytes())), "places_sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "method": "Existing shared forecast model: per-native-point period judgement, then normalized 4×4 B-spline blending. Fourteen single-day slots and the complete 14-day stretch. Scores uint24 at 1e-4; shares float32; unsupported coordinates remain missing."}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	if err = os.WriteFile(filepath.Join(directory, "named-build.json"), append(encoded, '\n'), 0644); err != nil {
		return "", 0, err
	}
	return filename, int64(compressed.Len()), nil
}
