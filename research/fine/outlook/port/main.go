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

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 16*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		var input struct {
			NativeFile string                `json:"nativeFile"`
			ID         string                `json:"id"`
			Packet     placenumbers.Packet   `json:"packet"`
			Periods    []placenumbers.Period `json:"periods"`
			Taste      *placenumbers.Taste   `json:"taste"`
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
		for _, period := range input.Periods {
			value, err := placenumbers.PlaceNumbers(input.Packet, period, input.Taste)
			if err != nil {
				panic(err)
			}
			values = append(values, value)
		}
		var months []placenumbers.MonthFacts
		if input.Packet.Source != "forecast" {
			var err error
			months, err = placenumbers.PlaceMonths(input.Packet, input.Taste)
			if err != nil {
				panic(err)
			}
		}
		body, err := json.Marshal(struct {
			ID      string                    `json:"id"`
			Numbers []placenumbers.Numbers    `json:"numbers"`
			Months  []placenumbers.MonthFacts `json:"months"`
		}{input.ID, values, months})
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(writer, string(body))
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}
