// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// JSONL driver for independently comparing the Go port with the real JS loader.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"nice-weather-finder/internal/placenumbers"
	"os"
)

type spatialPacket struct {
	Index  int                 `json:"index"`
	Packet placenumbers.Packet `json:"packet"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 16*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		var input struct {
			Latitude       float64               `json:"latitude"`
			Longitude      float64               `json:"longitude"`
			SpatialPackets []spatialPacket       `json:"spatialPackets"`
			NativeFile     string                `json:"nativeFile"`
			ID             string                `json:"id"`
			Packet         placenumbers.Packet   `json:"packet"`
			Periods        []placenumbers.Period `json:"periods"`
			Taste          *placenumbers.Taste   `json:"taste"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &input); err != nil {
			panic(err)
		}
		if input.NativeFile != "" {
			file, err := os.Open(input.NativeFile)
			if err != nil {
				panic(err)
			}
			zip, err := gzip.NewReader(file)
			if err != nil {
				panic(err)
			}
			body, err := io.ReadAll(zip)
			if err != nil {
				panic(err)
			}
			zip.Close()
			file.Close()
			dates := input.Packet.Replay.PhysicalResponse.Dates
			replay, err := placenumbers.DecodeReplay(body, dates)
			if err != nil {
				panic(err)
			}
			input.Packet.Replay = replay
		}
		var values []placenumbers.Numbers
		var spatialWeights []placenumbers.PointWeight
		for _, period := range input.Periods {
			if input.SpatialPackets != nil {
				packets := make(map[int]placenumbers.Packet)
				for _, point := range input.SpatialPackets {
					packets[point.Index] = point.Packet
				}
				footprint := placenumbers.MapPointWeights(input.Latitude, input.Longitude, func(index int) bool { _, found := packets[index]; return found })
				spatialWeights = footprint
				var points []placenumbers.WeightedNumbers
				for _, point := range footprint {
					numbers, err := placenumbers.PlaceNumbers(packets[point.Index], period, input.Taste)
					if err != nil {
						panic(err)
					}
					points = append(points, placenumbers.WeightedNumbers{Numbers: numbers, Weight: point.Weight})
				}
				values = append(values, placenumbers.BlendPlaceNumbers(points))
			} else {
				value, err := placenumbers.PlaceNumbers(input.Packet, period, input.Taste)
				if err != nil {
					panic(err)
				}
				values = append(values, value)
			}
		}
		var months []placenumbers.MonthFacts
		if input.SpatialPackets == nil && input.Packet.Source != "forecast" {
			var err error
			months, err = placenumbers.PlaceMonths(input.Packet, input.Taste)
			if err != nil {
				panic(err)
			}
		}
		body, err := json.Marshal(struct {
			ID             string                     `json:"id"`
			SpatialWeights []placenumbers.PointWeight `json:"spatialWeights,omitempty"`
			Numbers        []placenumbers.Numbers     `json:"numbers"`
			Months         []placenumbers.MonthFacts  `json:"months"`
		}{input.ID, spatialWeights, values, months})
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(writer, string(body))
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}
