package dwd

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type GridValue struct {
	Lon   float64
	Lat   float64
	Value float64
}

func ParseGribGetData(reader io.Reader) ([]GridValue, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var values []GridValue
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Latitude ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("grib_get_data line %d has %d fields, want 3", lineNumber, len(fields))
		}
		lat, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return nil, fmt.Errorf("grib_get_data line %d latitude: %w", lineNumber, err)
		}
		lon, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("grib_get_data line %d longitude: %w", lineNumber, err)
		}
		value, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, fmt.Errorf("grib_get_data line %d value: %w", lineNumber, err)
		}
		values = append(values, GridValue{Lon: lon, Lat: lat, Value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("grib_get_data contained no values")
	}
	return values, nil
}
