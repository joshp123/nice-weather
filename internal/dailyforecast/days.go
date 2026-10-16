package dailyforecast

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/weather"
)

type dayAccumulator struct {
	temperature, dewPoint, cloud, wind, rain, wetHours, hours float64
	minTemperature, maxTemperature                            float64
}

func daytimeWindow(date time.Time, location *time.Location) (time.Time, time.Time) {
	year, month, day := date.Date()
	return time.Date(year, month, day, 10, 0, 0, 0, location).UTC(), time.Date(year, month, day, 20, 0, 0, 0, location).UTC()
}

type rainAdjustments struct {
	count            int
	largestDeficitMM float64
}

func accumulateInterval(dates []time.Time, groups []timezoneGroup, land []int, accumulators [][]dayAccumulator, frames [][]float32, priorRain []float32, adjustments *rainAdjustments, start, end time.Time) {
	duration := end.Sub(start).Hours()
	rainDelta := make([]float64, len(land))
	for landIndex, cell := range land {
		current, previous := float64(frames[5][cell]), float64(priorRain[cell])
		delta := current - previous
		if delta < 0 {
			adjustments.count++
			adjustments.largestDeficitMM = max(adjustments.largestDeficitMM, -delta*1000)
			delta = 0
			frames[5][cell] = priorRain[cell]
		}
		rainDelta[landIndex] = delta * 1000
	}
	for _, group := range groups {
		for day, date := range dates {
			windowStart, windowEnd := daytimeWindow(date, group.location)
			overlapStart, overlapEnd := start, end
			if windowStart.After(overlapStart) {
				overlapStart = windowStart
			}
			if windowEnd.Before(overlapEnd) {
				overlapEnd = windowEnd
			}
			overlap := overlapEnd.Sub(overlapStart).Hours()
			if overlap <= 0 {
				continue
			}
			weight := overlap
			for _, landIndex := range group.landIndices {
				cell := land[landIndex]
				temperature := float64(frames[0][cell]) - 273.15
				dewPoint := float64(frames[1][cell]) - 273.15
				cloud := float64(frames[2][cell]) * 100
				wind := math.Hypot(float64(frames[3][cell]), float64(frames[4][cell])) * 3.6
				if !finite(temperature) || !finite(dewPoint) || !finite(cloud) || !finite(wind) || !finite(rainDelta[landIndex]) {
					continue
				}
				accumulator := &accumulators[day][landIndex]
				if accumulator.hours == 0 {
					accumulator.minTemperature = temperature
					accumulator.maxTemperature = temperature
				}
				accumulator.minTemperature = min(accumulator.minTemperature, temperature)
				accumulator.maxTemperature = max(accumulator.maxTemperature, temperature)
				accumulator.temperature += temperature * weight
				accumulator.dewPoint += dewPoint * weight
				accumulator.cloud += cloud * weight
				accumulator.wind += wind * weight
				accumulator.rain += rainDelta[landIndex] * (overlap / duration)
				if float64(rainDelta[landIndex])/duration >= .10 {
					accumulator.wetHours += weight
				}
				accumulator.hours += weight
			}
		}
	}
}
func finite(value float64) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0) && math.Abs(float64(value)) < 1e15
}

func writeDay(directory, date string, land []int, accumulators []dayAccumulator, counts [2][]byte, completeDays []byte) (int, error) {
	path := filepath.Join(directory, date)
	if err := os.Mkdir(path, 0755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Join(directory, "points"), 0755); err != nil {
		return 0, err
	}
	layers := [7][]byte{}
	for layer := range layers {
		layers[layer] = make([]byte, Width*Height)
		for cell := range layers[layer] {
			layers[layer][cell] = noData
		}
	}
	records := make([]byte, Width*Height*PointRecordBytes)
	for offset := range records {
		records[offset] = noData
	}
	valid := 0
	for landIndex, cell := range land {
		accumulated := accumulators[landIndex]
		if math.Abs(float64(accumulated.hours)-10) > 1e-4 {
			continue
		}
		hours := float64(accumulated.hours)
		temperature := float64(accumulated.temperature) / hours
		dewPoint := float64(accumulated.dewPoint) / hours
		humidity := min(100.0, max(0.0, 100*math.Exp(17.625*dewPoint/(243.04+dewPoint)-17.625*temperature/(243.04+temperature))))
		metrics := weather.DailyMetrics{ComfortableTempC: temperature, MinTempC: float64(accumulated.minTemperature), MaxTempC: float64(accumulated.maxTemperature), DewPointC: weather.OptionalFloat{Value: dewPoint, Available: true}, RelativeHumidityPercent: weather.OptionalFloat{Value: humidity, Available: true}, CloudCoverPercent: weather.OptionalFloat{Value: min(100, max(0, float64(accumulated.cloud)/hours)), Available: true}, WindSpeedKPH: float64(accumulated.wind) / hours, RainMM: float64(accumulated.rain), WetTimeFraction: weather.OptionalFloat{Value: float64(accumulated.wetHours) / hours, Available: true}, NativeResolutionKM: ResolutionDegrees * 111.2, Valid: true}
		nice, cycling, err := weather.ScoreProfilesChecked(metrics)
		if err != nil {
			return 0, fmt.Errorf("%s at %.2f,%.2f: %w", date, 90-float64(cell/Width)*ResolutionDegrees, -180+float64(cell%Width)*ResolutionDegrees, err)
		}
		scores := [7]byte{byte(nice.Score), byte(cycling.Score), byte(nice.Components.Thermal), byte(nice.Components.Dryness), byte(nice.Components.Sun), byte(nice.Components.Wind), byte(cycling.Components.Wind)}
		for layer, score := range scores {
			layers[layer][cell] = score
		}
		record := PointRecord{TemperatureTenthsC: int16(math.Round(temperature * 10)), DewPointTenthsC: int16(math.Round(dewPoint * 10)), RainTenthsMM: uint16(math.Round(metrics.RainMM * 10)), WetPercent: roundedByte(metrics.WetTimeFraction.Value * 100), CloudPercent: roundedByte(metrics.CloudCoverPercent.Value), WindKPH: roundedByte(metrics.WindSpeedKPH), GustKPH: noData, Scores: scores}
		record.encode(records[cell*PointRecordBytes : (cell+1)*PointRecordBytes])
		for profile, score := range []int{nice.Score, cycling.Score} {
			if counts[profile][cell] == noData {
				counts[profile][cell] = 0
			}
			if score >= 55 {
				counts[profile][cell]++
			}
		}
		completeDays[cell]++
		valid++
	}
	if valid == 0 {
		return 0, fmt.Errorf("%s has no complete land daytime windows", date)
	}
	for layer, name := range Layers {
		if err := writePNG(filepath.Join(path, name+".png"), layers[layer]); err != nil {
			return 0, err
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "points", date+".bin"), records, 0644); err != nil {
		return 0, err
	}
	return valid, nil
}
func roundedByte(value float64) byte { return byte(min(254, max(0, math.Round(value)))) }
func (record PointRecord) encode(destination []byte) {
	binary.LittleEndian.PutUint16(destination[0:2], uint16(record.TemperatureTenthsC))
	binary.LittleEndian.PutUint16(destination[2:4], uint16(record.DewPointTenthsC))
	binary.LittleEndian.PutUint16(destination[4:6], record.RainTenthsMM)
	destination[6] = record.WetPercent
	destination[7] = record.CloudPercent
	destination[8] = record.WindKPH
	destination[9] = record.GustKPH
	copy(destination[10:17], record.Scores[:])
}
func writePNG(path string, values []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	encodeErr := encoder.Encode(file, &image.Gray{Pix: values, Stride: Width, Rect: image.Rect(0, 0, Width, Height)})
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
