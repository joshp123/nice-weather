package climatemeasures

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nice-weather-finder/internal/clearsky"
	"nice-weather-finder/internal/climatefacts"
	"nice-weather-finder/internal/daymeasures"
)

// This optional comparison consumes retained hourly research, rather than
// manufactured weather. Run prepare_reference.py, then set NWF_REFERENCE.
func TestRetainedResearchThroughProductionMeasures(t *testing.T) {
	directory := os.Getenv("NWF_REFERENCE")
	if directory == "" {
		t.Skip("NWF_REFERENCE is not set")
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.bin.gz"))
	if err != nil || len(paths) != 12 {
		t.Fatalf("need twelve research place inputs: %d %v", len(paths), err)
	}
	grid, err := climatefacts.ReadStatic(os.Getenv("NWF_STATIC"))
	if err != nil {
		t.Fatal(err)
	}
	var summary []map[string]any
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		compressed, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		var size uint32
		if err = binary.Read(compressed, binary.LittleEndian, &size); err != nil {
			t.Fatal(err)
		}
		header := make([]byte, size)
		if _, err = io.ReadFull(compressed, header); err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Name                                                                                                                    string
			Latitude, Longitude, ElevationM, SourceElevationM, SelectedLatitude, SelectedLongitude, NativeLatitude, NativeLongitude float64
			Timezone, Start                                                                                                         string
			MonthlyTurbidity                                                                                                        [12]float64
			Hours                                                                                                                   int
			Dates                                                                                                                   []string
		}
		if err = json.Unmarshal(header, &meta); err != nil {
			t.Fatal(err)
		}
		var series [6][]float32
		for field := range series {
			series[field] = make([]float32, meta.Hours)
			if err = binary.Read(compressed, binary.LittleEndian, series[field]); err != nil {
				t.Fatal(err)
			}
		}
		expected := make([][17]float64, len(meta.Dates))
		if err = binary.Read(compressed, binary.LittleEndian, expected); err != nil {
			t.Fatal(err)
		}
		compressed.Close()
		file.Close()
		start, err := time.Parse(time.RFC3339, meta.Start)
		if err != nil {
			t.Fatal(err)
		}
		location, err := time.LoadLocation(meta.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		row := int(math.Round((90 - meta.NativeLatitude) * 4))
		column := int(math.Round(math.Mod(meta.NativeLongitude+360, 360)*4)) % 1440
		temperatureRow, temperatureColumn := grid.TemperatureCell(meta.Latitude, meta.Longitude, row, column)
		selectedLatitude, selectedLongitude := climatefacts.Coordinates(temperatureRow, temperatureColumn)
		if selectedLatitude != meta.SelectedLatitude || math.Remainder(selectedLongitude-meta.SelectedLongitude, 360) != 0 {
			t.Fatalf("%s selected %.2f,%.2f expected %.2f,%.2f", meta.Name, selectedLatitude, selectedLongitude, meta.SelectedLatitude, meta.SelectedLongitude)
		}
		sourceElevation := float64(grid.SourceElevationM[temperatureRow*1440+temperatureColumn])
		if math.Abs(sourceElevation-meta.SourceElevationM) > .001 {
			t.Fatalf("%s source height mismatch", meta.Name)
		}
		cell := climatefacts.Cell{Latitude: meta.Latitude, Longitude: meta.Longitude, ElevationM: meta.ElevationM, Location: location}
		solar := clearsky.NewWithMonthlyTurbidity(meta.Latitude, meta.Longitude, meta.ElevationM, meta.MonthlyTurbidity)
		ephemeris := make([]clearsky.Hour, meta.Hours)
		for index := range ephemeris {
			ephemeris[index] = clearsky.PrepareHour(start.Add(time.Duration(index) * time.Hour))
		}
		hours := hourlyFacts(start, cell, sourceElevation, solar, ephemeris, series)
		var encoded bytes.Buffer
		writer, err := daymeasures.NewWriter(&encoded)
		if err != nil {
			t.Fatal(err)
		}
		maxima := [5]float64{}
		wholeRainDays := 0
		var originalMeanDewDifference, originalMeanWindDifference float64
		for index, dateText := range meta.Dates {
			date, err := time.Parse("2006-01-02", dateText)
			if err != nil {
				t.Fatal(err)
			}
			dayStart, dayEnd := daymeasures.LocalDayBounds(date, location)
			low := max(0, int(dayStart.Sub(start)/time.Hour)-1)
			high := min(len(hours), int(dayEnd.Sub(start)/time.Hour)+2)
			measures, err := daymeasures.Aggregate(date, location, hours[low:high])
			if err != nil {
				t.Fatalf("%s %s: %v", meta.Name, dateText, err)
			}
			if err = writer.Write(0, date, measures); err != nil {
				t.Fatal(err)
			}
			originalMeanDewDifference += math.Abs(measures.DewPointC-expected[index][15]) / float64(len(meta.Dates))
			originalMeanWindDifference += math.Abs(measures.WindKPH-expected[index][16]) / float64(len(meta.Dates))
		}
		reader, err := daymeasures.NewReader(&encoded)
		if err != nil {
			t.Fatal(err)
		}
		for index, dateText := range meta.Dates {
			_, _, measures, err := reader.Read()
			if err != nil {
				t.Fatal(err)
			}
			for hour, value := range measures.TemperatureC {
				maxima[0] = max(maxima[0], math.Abs(value-expected[index][hour]))
			}
			maxima[1] = max(maxima[1], math.Abs(measures.DewPointC-expected[index][10]))
			maxima[2] = max(maxima[2], math.Abs(measures.DaytimeRainMM-expected[index][11]))
			if !math.IsNaN(expected[index][12]) {
				maxima[2] = max(maxima[2], math.Abs(measures.WholeDayRainMM-expected[index][12]))
				wholeRainDays++
			}
			maxima[3] = max(maxima[3], math.Abs(measures.SunShare-expected[index][13]))
			maxima[4] = max(maxima[4], math.Abs(measures.WindKPH-expected[index][14]))
			for measure, tolerance := range []float64{.0051, .0051, .0501, .000051, .0051} {
				if maxima[measure] > tolerance {
					t.Fatalf("%s %s measure%d difference%.8f exceeds%.8f; day rain %.5f/%.5f, whole rain %.5f/%.5f", meta.Name, dateText, measure, maxima[measure], tolerance, measures.DaytimeRainMM, expected[index][11], measures.WholeDayRainMM, expected[index][12])
				}
			}
		}
		entry := map[string]any{"place": meta.Name, "days": len(meta.Dates), "wholeDayRainDays": wholeRainDays, "maximumStoredDifferences": maxima, "meanAbsoluteDifferenceFromOriginal11SampleDewC": originalMeanDewDifference, "meanAbsoluteDifferenceFromOriginal11SampleWindKPH": originalMeanWindDifference}
		summary = append(summary, entry)
		t.Logf("%s %d days; maximum stored difference temp/dew/rain/sun/wind %v", meta.Name, len(meta.Dates), maxima)
	}
	if path := os.Getenv("NWF_REFERENCE_REPORT"); path != "" {
		encoded, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, encoded, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
