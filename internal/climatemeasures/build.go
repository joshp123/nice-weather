// Package climatemeasures builds streamable local-day facts, without scores.
package climatemeasures

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"nice-weather-finder/internal/clearsky"
	"nice-weather-finder/internal/climatefacts"
	"nice-weather-finder/internal/daymeasures"
)

type Options struct {
	SourceIndex, StaticGrid, Output string
	TileRow, TileColumn, Year       int
	Cells                           []climatefacts.Cell
}

type Report struct {
	SourceSnapshot    string  `json:"sourceSnapshot"`
	TileRow           int     `json:"tileRow"`
	TileColumn        int     `json:"tileColumn"`
	Year              int     `json:"year"`
	Cells             int     `json:"cells"`
	Days              int     `json:"days"`
	Records           int     `json:"records"`
	SkippedLocalDates int     `json:"skippedLocalDates"`
	SourceChunks      int     `json:"sourceChunks"`
	ReadBytes         int64   `json:"readBytes"`
	WrittenBytes      int64   `json:"writtenBytes"`
	PeakHeapBytes     uint64  `json:"peakHeapBytes"`
	PeakResidentBytes uint64  `json:"peakResidentBytes"`
	WallSeconds       float64 `json:"wallSeconds"`
	Path              string  `json:"path"`
	Resumed           bool    `json:"resumed"`
	InputIdentity     string  `json:"inputIdentity"`
	OutputSHA256      string  `json:"outputSHA256"`
}

func Build(ctx context.Context, options Options, log io.Writer) (report Report, err error) {
	started := time.Now()
	if options.Year < 2006 || options.Year > 2025 {
		return report, fmt.Errorf("climate measures year must be 2006..2025")
	}
	report.TileRow, report.TileColumn, report.Year = options.TileRow, options.TileColumn, options.Year
	directory := filepath.Join(options.Output, "climate-measures", fmt.Sprintf("r%02d-c%03d", options.TileRow, options.TileColumn))
	report.Path = filepath.Join(directory, fmt.Sprintf("%d.bin.gz", options.Year))
	checkpoint := filepath.Join(directory, fmt.Sprintf("%d.report.json", options.Year))
	grid, err := climatefacts.ReadStatic(options.StaticGrid)
	if err != nil {
		return report, err
	}
	cells := options.Cells
	if cells == nil {
		cells, err = grid.TileCells(options.TileRow, options.TileColumn)
		if err != nil {
			return report, err
		}
	}
	report.Cells = len(cells)
	if len(cells) == 0 {
		return report, fmt.Errorf("tile contains no land cells")
	}
	identity, err := batchIdentity(options, cells)
	if err != nil {
		return report, err
	}
	report.InputIdentity = identity
	if encoded, readErr := os.ReadFile(checkpoint); readErr == nil {
		var prior Report
		if err = json.Unmarshal(encoded, &prior); err != nil {
			return report, err
		}
		if prior.InputIdentity != identity {
			return report, fmt.Errorf("completed batch has different inputs; choose a separate output directory")
		}
		sum, hashErr := fileSHA256(report.Path)
		if hashErr != nil {
			return report, hashErr
		}
		if sum != prior.OutputSHA256 {
			return report, fmt.Errorf("completed batch checksum does not match its checkpoint")
		}
		prior.Path = report.Path
		prior.Resumed = true
		return prior, nil
	}
	archive, err := climatefacts.OpenArchive(ctx, options.SourceIndex)
	if err != nil {
		return report, err
	}
	report.SourceSnapshot = archive.Snapshot
	epoch := time.Date(1940, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(options.Year, 1, 1, 0, 0, 0, 0, time.UTC).Add(-48 * time.Hour)
	end := time.Date(options.Year+1, 1, 1, 0, 0, 0, 0, time.UTC).Add(48 * time.Hour)
	firstHour, lastHour := int(start.Sub(epoch)/time.Hour), int(end.Sub(epoch)/time.Hour)
	valuesByField := make([][][]float32, 6)
	for field := range valuesByField {
		valuesByField[field] = make([][]float32, len(cells))
		for cell := range cells {
			valuesByField[field][cell] = make([]float32, lastHour-firstHour)
		}
	}
	keys := make([]climatefacts.ChunkKey, 0)
	needed := map[climatefacts.ChunkKey]bool{}
	for field := climatefacts.Temperature; field <= climatefacts.NorthWind; field++ {
		for _, cell := range cells {
			row, column := cell.Row, cell.Column
			if field == climatefacts.Temperature {
				row, column = cell.TemperatureRow, cell.TemperatureColumn
			}
			for timeChunk := firstHour / 8736; timeChunk <= (lastHour-1)/8736; timeChunk++ {
				key := climatefacts.ChunkKey{Field: field, Time: uint16(timeChunk), Row: uint8(row / 12), Column: uint8(column / 12)}
				if !needed[key] {
					needed[key] = true
					keys = append(keys, key)
				}
			}
		}
	}
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		values, readErr := archive.ReadChunk(ctx, key)
		if readErr != nil {
			return report, readErr
		}
		for cellIndex, cell := range cells {
			row, column := cell.Row, cell.Column
			if key.Field == climatefacts.Temperature {
				row, column = cell.TemperatureRow, cell.TemperatureColumn
			}
			if row/12 != int(key.Row) || column/12 != int(key.Column) {
				continue
			}
			for hour := max(firstHour, int(key.Time)*8736); hour < min(lastHour, (int(key.Time)+1)*8736); hour++ {
				valuesByField[key.Field][cellIndex][hour-firstHour] = values[(hour%8736)*144+(row%12)*12+column%12]
			}
		}
		observeHeap(&report)
		fmt.Fprintf(log, "ERA5 %s %d/%d/%d: %d values\n", climatefacts.FieldNames[key.Field], key.Time, key.Row, key.Column, len(values))
	}
	report.SourceChunks = len(keys)
	solarHours := make([]clearsky.Hour, lastHour-firstHour)
	for index := range solarHours {
		solarHours[index] = clearsky.PrepareHour(start.Add(time.Duration(index) * time.Hour))
	}
	if err = os.MkdirAll(directory, 0755); err != nil {
		return report, err
	}
	file, err := os.Create(report.Path + ".partial")
	if err != nil {
		return report, err
	}
	defer file.Close()
	compressed, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		return report, err
	}
	writer, err := daymeasures.NewWriter(compressed)
	if err != nil {
		return report, err
	}
	firstDate := time.Date(options.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	lastDate := firstDate.AddDate(1, 0, 0)
	report.Days = int(lastDate.Sub(firstDate) / (24 * time.Hour))
	for cellIndex, cell := range cells {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		var solar *clearsky.Model
		if cell.MonthlyTurbidity != nil {
			solar = clearsky.NewWithMonthlyTurbidity(cell.Latitude, cell.Longitude, cell.ElevationM, *cell.MonthlyTurbidity)
		} else {
			solar, err = clearsky.New(cell.Latitude, cell.Longitude, cell.ElevationM)
			if err != nil {
				return report, err
			}
		}
		temperatureElevation := float64(grid.SourceElevationM[cell.TemperatureRow*climatefacts.Width+cell.TemperatureColumn])
		var series [6][]float32
		for field := range series {
			series[field] = valuesByField[field][cellIndex]
		}
		hours := hourlyFacts(start, cell, temperatureElevation, solar, solarHours, series)
		for date := firstDate; date.Before(lastDate); date = date.AddDate(0, 0, 1) {
			dayStart, dayEnd := daymeasures.LocalDayBounds(date, cell.Location)
			if dayStart.Equal(dayEnd) {
				report.SkippedLocalDates++
				continue
			}
			low := max(0, int(dayStart.Sub(start)/time.Hour)-1)
			high := min(len(hours), int(dayEnd.Sub(start)/time.Hour)+2)
			measures, measureErr := daymeasures.Aggregate(date, cell.Location, hours[low:high])
			if measureErr != nil {
				return report, fmt.Errorf("cell %d %s: %w", cell.Index, date.Format("2006-01-02"), measureErr)
			}
			if err = writer.Write(cell.Index, date, measures); err != nil {
				return report, err
			}
			report.Records++
		}
		observeHeap(&report)
	}
	if err = compressed.Close(); err != nil {
		return report, err
	}
	if err = file.Close(); err != nil {
		return report, err
	}
	if err = os.Rename(report.Path+".partial", report.Path); err != nil {
		return report, err
	}
	info, err := os.Stat(report.Path)
	if err != nil {
		return report, err
	}
	report.WrittenBytes = info.Size()
	report.OutputSHA256, err = fileSHA256(report.Path)
	if err != nil {
		return report, err
	}
	report.ReadBytes = archive.ReadBytes.Load()
	report.WallSeconds = time.Since(started).Seconds()
	observeHeap(&report)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err = os.WriteFile(checkpoint+".partial", encoded, 0644); err != nil {
		return report, err
	}
	if err = os.Rename(checkpoint+".partial", checkpoint); err != nil {
		return report, err
	}
	return report, nil
}

func batchIdentity(options Options, cells []climatefacts.Cell) (string, error) {
	staticHash, err := fileSHA256(options.StaticGrid)
	if err != nil {
		return "", err
	}
	sourceIdentity := options.SourceIndex
	if sourceIdentity == "earthmover" || sourceIdentity == "" {
		sourceIdentity = climatefacts.SnapshotID
	} else if !strings.Contains(sourceIdentity, "://") {
		sourceIdentity, err = fileSHA256(options.SourceIndex)
		if err != nil {
			return "", err
		}
	}
	type cellIdentity struct {
		Index                                          uint32
		Latitude, Longitude, Elevation                 float64
		Row, Column, TemperatureRow, TemperatureColumn int
		Timezone                                       string
		Turbidity                                      *[12]float64
	}
	identityCells := make([]cellIdentity, len(cells))
	for index, cell := range cells {
		identityCells[index] = cellIdentity{cell.Index, cell.Latitude, cell.Longitude, cell.ElevationM, cell.Row, cell.Column, cell.TemperatureRow, cell.TemperatureColumn, cell.Location.String(), cell.MonthlyTurbidity}
	}
	encoded, err := json.Marshal(struct {
		Schema, Year, TileRow, TileColumn int
		Static, Source                    string
		Cells                             []cellIdentity
	}{daymeasures.SchemaVersion, options.Year, options.TileRow, options.TileColumn, staticHash, sourceIdentity, identityCells})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func hourlyFacts(start time.Time, cell climatefacts.Cell, sourceElevation float64, solar *clearsky.Model, solarHours []clearsky.Hour, series [6][]float32) []daymeasures.HourlyFacts {
	lapseCorrection := .0065 * (sourceElevation - cell.ElevationM)
	hours := make([]daymeasures.HourlyFacts, len(solarHours))
	for index := range hours {
		valueAt := func(field climatefacts.Field) float64 { return float64(series[field][index]) }
		horizontal, normal, sunUp := solar.Hour(solarHours[index])
		hours[index] = daymeasures.HourlyFacts{Time: start.Add(time.Duration(index) * time.Hour), TemperatureC: valueAt(climatefacts.Temperature) - 273.15 + lapseCorrection, DewPointC: valueAt(climatefacts.DewPoint) - 273.15, RainMM: max(0, valueAt(climatefacts.Rain)*1000), WindKPH: math.Hypot(valueAt(climatefacts.EastWind), valueAt(climatefacts.NorthWind)) * 3.6, SunHours: clearsky.SunshineHours(valueAt(climatefacts.DirectRadiation)/3600, horizontal, normal, sunUp), SunUpHours: sunUp, SunAvailable: true}
	}
	return hours
}

func observeHeap(report *Report) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	report.PeakHeapBytes = max(report.PeakHeapBytes, memory.HeapInuse)
	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "VmHWM:" {
				value, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					report.PeakResidentBytes = max(report.PeakResidentBytes, value*1024)
				}
			}
		}
	}
}
