// Written by AI on 2026.10.16 by model GPT-6.1 Sol.
// Offline preparation of the complementary global ice-surface elevation grid.
package main

import (
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"
)

const sourceURL = "https://data.pmel.noaa.gov/socat/erddap/griddap/etopo180.csv?altitude[0:15:10800][0:15:21585]"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	inputPath := flag.String("csv", "", "read an already downloaded NOAA ERDDAP subset instead of downloading")
	outputPath := flag.String("out", "etopo-quarter-degree.bin", "float32LE grid, 90N to 90S and longitude0 to359.75")
	flag.Parse()
	var input io.ReadCloser
	if *inputPath != "" {
		file, err := os.Open(*inputPath)
		if err != nil {
			return err
		}
		input = file
	} else {
		client := http.Client{Timeout: 5 * time.Minute}
		response, err := client.Get(sourceURL)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("NOAA elevation: %s", response.Status)
		}
		input = response.Body
	}
	defer input.Close()
	reader := csv.NewReader(input)
	header, err := reader.Read()
	if err != nil {
		return err
	}
	if len(header) != 3 || header[0] != "latitude" || header[1] != "longitude" || header[2] != "altitude" {
		return fmt.Errorf("unexpected NOAA CSV columns: %v", header)
	}
	if _, err = reader.Read(); err != nil {
		return err
	}
	values := make([]float32, 721*1440)
	for index := range values {
		record, err := reader.Read()
		if err != nil {
			return fmt.Errorf("NOAA record %d: %w", index, err)
		}
		latitude, err := strconv.ParseFloat(record[0], 64)
		if err != nil {
			return err
		}
		longitude, err := strconv.ParseFloat(record[1], 64)
		if err != nil {
			return err
		}
		height, err := strconv.ParseFloat(record[2], 64)
		if err != nil {
			return err
		}
		sourceRow, sourceColumn := index/1440, index%1440
		if math.Abs(latitude-(-90+float64(sourceRow)*.25)) > 1e-8 || math.Abs(longitude-(-180+float64(sourceColumn)*.25)) > 1e-8 {
			return fmt.Errorf("NOAA record %d has unexpected coordinate %g,%g", index, latitude, longitude)
		}
		if math.IsNaN(height) || height == 32767 {
			return fmt.Errorf("NOAA elevation missing at %g,%g", latitude, longitude)
		}
		row, column := 720-sourceRow, (sourceColumn+720)%1440
		values[row*1440+column] = float32(height)
	}
	if _, err = reader.Read(); err != io.EOF {
		return fmt.Errorf("unexpected trailing NOAA records: %v", err)
	}
	output, err := os.Create(*outputPath)
	if err != nil {
		return err
	}
	if err = binary.Write(output, binary.LittleEndian, values); err != nil {
		output.Close()
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	fmt.Printf("%d global surface heights written to %s\nSource: %s\n", len(values), *outputPath, sourceURL)
	return nil
}
