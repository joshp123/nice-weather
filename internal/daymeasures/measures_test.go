package daymeasures_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math"
	"testing"
	"time"
	_ "time/tzdata"

	"nice-weather-finder/internal/dailyforecast"
	"nice-weather-finder/internal/daymeasures"
)

func TestLocalDayMeasuresAcrossOffsetsAndDaylightSaving(t *testing.T) {
	for _, test := range []struct {
		zone, date string
		dayHours   float64
	}{
		{"Europe/Amsterdam", "2020-03-29", 23},
		{"Europe/Amsterdam", "2020-10-25", 25},
		{"Asia/Kathmandu", "2020-01-01", 24},
		{"Pacific/Kiritimati", "2020-01-01", 24},
		{"Africa/Casablanca", "2011-04-03", 23},
		{"America/Havana", "2020-11-01", 25},
		{"America/Santiago", "2020-09-06", 23},
	} {
		t.Run(test.zone+test.date, func(t *testing.T) {
			date, _ := time.Parse("2006-01-02", test.date)
			location, err := time.LoadLocation(test.zone)
			if err != nil {
				t.Fatal(err)
			}
			start, end := daymeasures.LocalDayBounds(date, location)
			var hours []daymeasures.HourlyFacts
			for instant := start.UTC().Truncate(time.Hour).Add(-time.Hour); !instant.After(end.Add(2 * time.Hour)); instant = instant.Add(time.Hour) {
				elapsed := instant.Sub(start).Hours()
				hours = append(hours, daymeasures.HourlyFacts{Time: instant, TemperatureC: elapsed, DewPointC: 3, WindKPH: 12, RainMM: 2, SunAvailable: true, SunHours: .25, SunUpHours: .5})
			}
			day, err := daymeasures.Aggregate(date, location, hours)
			if err != nil {
				t.Fatal(err)
			}
			for index, temperature := range day.TemperatureC {
				expected := time.Date(date.Year(), date.Month(), date.Day(), 10+index, 0, 0, 0, location).Sub(start).Hours()
				if math.Abs(temperature-expected) > 1e-10 {
					t.Fatalf("local hour %d: got %g want %g", 10+index, temperature, expected)
				}
			}
			if day.DaytimeRainMM != 20 || day.WholeDayRainMM != 2*test.dayHours || day.WholeDayRainHours != test.dayHours || day.SunShare != .5 || day.SunUpHours != 5 || day.DewPointC != 3 || day.WindKPH != 12 {
				t.Fatalf("unexpected day: %+v", day)
			}
		})
	}
}

func TestSamoaSkippedLocalDate(t *testing.T) {
	date := time.Date(2011, 12, 30, 0, 0, 0, 0, time.UTC)
	location, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatal(err)
	}
	start, end := daymeasures.LocalDayBounds(date, location)
	if !start.Equal(end) || start.In(location).Format("2006-01-02") != "2011-12-31" {
		t.Fatalf("skipped local date has bounds %s..%s", start, end)
	}
	if _, err = daymeasures.Aggregate(date, location, nil); !errors.Is(err, daymeasures.ErrNonexistentLocalDay) {
		t.Fatalf("skipped local date: %v", err)
	}
}

func TestRainUsesPrecedingIntervalAndSunUsesSunlitTime(t *testing.T) {
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var hours []daymeasures.HourlyFacts
	for index := 0; index <= 24; index++ {
		hour := daymeasures.HourlyFacts{Time: date.Add(time.Duration(index) * time.Hour), TemperatureC: float64(index), DewPointC: 2, WindKPH: 4, SunAvailable: true}
		if index == 10 {
			hour.RainMM = 100
		} // 09..10: outside the daytime window.
		if index == 20 {
			hour.RainMM = 3
			hour.SunHours = .25
			hour.SunUpHours = .5
		} // 19..20: inside.
		hours = append(hours, hour)
	}
	day, err := daymeasures.Aggregate(date, time.UTC, hours)
	if err != nil {
		t.Fatal(err)
	}
	if day.DaytimeRainMM != 3 || day.WholeDayRainMM != 103 || day.SunShare != .5 || day.AfternoonHighC() != 19 {
		t.Fatalf("unexpected day: %+v", day)
	}
}

func TestForecastAdapterAndCompressedMeasureStream(t *testing.T) {
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var points []dailyforecast.MeasurePoint
	for hour := 0; hour <= 24; hour += 3 {
		points = append(points, dailyforecast.MeasurePoint{Time: date.Add(time.Duration(hour) * time.Hour), TemperatureK: 273.15 + float64(hour), DewPointK: 275.15, WindUMPS: 3, WindVMPS: 4, RainM: float64(hour) * .001})
	}
	day, err := dailyforecast.MeasuresForDay(date, time.UTC, points)
	if err != nil {
		t.Fatal(err)
	}
	if day.TemperatureC[0] != 10 || day.TemperatureC[9] != 19 || math.Abs(day.DaytimeRainMM-10) > 1e-10 || math.Abs(day.WholeDayRainMM-24) > 1e-10 || day.SunAvailable || day.GustAvailable {
		t.Fatalf("unexpected forecast day: %+v", day)
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer, err := daymeasures.NewWriter(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Write(123456, date, day); err != nil {
		t.Fatal(err)
	}
	if err = compressed.Close(); err != nil {
		t.Fatal(err)
	}
	uncompressed, err := gzip.NewReader(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := daymeasures.NewReader(uncompressed)
	if err != nil {
		t.Fatal(err)
	}
	cell, decodedDate, decoded, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if cell != 123456 || !decodedDate.Equal(date) || decoded.TemperatureC != day.TemperatureC || decoded.WholeDayRainMM != 24 || decoded.WindKPH != 18 || decoded.SunAvailable {
		t.Fatalf("stream round trip: cell=%d date=%s day=%+v", cell, decodedDate, decoded)
	}
	if _, _, _, err = reader.Read(); err != io.EOF {
		t.Fatalf("end of stream: %v", err)
	}
	if err = uncompressed.Close(); err != nil {
		t.Fatal(err)
	}
}
