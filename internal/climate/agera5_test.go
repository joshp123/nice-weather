package climate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nice-weather-finder/internal/weather"
)

func TestAgERA5DistributionScoresDaysBeforeAggregating(t *testing.T) {
	malaga := make([]AgERA5Day, 10)
	amsterdam := make([]AgERA5Day, 10)
	for index := range malaga {
		malaga[index] = day(fmt.Sprintf("2025-11-%02d", index+1), 36.72, -4.42, 24, 0, 0, 15, 20, 12)
		amsterdam[index] = day(fmt.Sprintf("2025-11-%02d", index+1), 52.37, 4.90, 7, 4, .65, 2, 95, 25)
	}
	malagaResult, err := Aggregate(malaga, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	amsterdamResult, err := Aggregate(amsterdam, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	if malagaResult.NiceFraction != 1 || malagaResult.AwfulFraction != 0 || amsterdamResult.AwfulFraction != 1 || amsterdamResult.NiceFraction != 0 {
		t.Fatalf("Málaga=%+v Amsterdam=%+v", malagaResult, amsterdamResult)
	}
}

func TestDecodeAgERA5IsStrictAndBounded(t *testing.T) {
	input := `{"date":"2025-11-01","latitude":36.72,"longitude":-4.42,"daytimeMeanTemperatureC":24,"minimumTemperatureC":18,"maximumTemperatureC":27,"precipitationMm":0,"precipitationDurationFraction":0,"solarEnergyMJm2":15,"cloudCoverPercent":20,"windSpeedKmh":12,"dewPointC":12}`
	rows, err := DecodeAgERA5(strings.NewReader(input), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err := DecodeAgERA5(strings.NewReader(input+"\n"+input), 1); err == nil {
		t.Fatal("row limit was ignored")
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `"date":"2025-11-01"`, `"date":"bad"`, 1)), 1); err == nil {
		t.Fatal("invalid row was accepted")
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `}`, `,"unexpected":true}`, 1)), 1); err == nil {
		t.Fatal("unknown field was accepted")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		t.Fatal(err)
	}
	for field := range fields {
		missing := make(map[string]json.RawMessage, len(fields)-1)
		nullValue := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			if key != field {
				missing[key] = value
			}
			nullValue[key] = value
		}
		encoded, _ := json.Marshal(missing)
		if _, err := DecodeAgERA5(strings.NewReader(string(encoded)), 1); err == nil {
			t.Fatalf("missing required field %q was accepted", field)
		}
		nullValue[field] = json.RawMessage("null")
		encoded, _ = json.Marshal(nullValue)
		if _, err := DecodeAgERA5(strings.NewReader(string(encoded)), 1); err == nil {
			t.Fatalf("null required field %q was accepted", field)
		}
	}
	if _, err := DecodeAgERA5(strings.NewReader(strings.Replace(input, `"precipitationMm":0`, `"precipitationMm":0,"precipitationMm":1`, 1)), 1); err == nil {
		t.Fatal("duplicate field was accepted")
	}
}

func TestAgERA5DistributionCombinesReferenceYearsAndRejectsDuplicateDates(t *testing.T) {
	first := day("2025-11-01", 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	otherYear := day("2006-11-02", 36.72, -4.42, 24, 0, 0, 15, 20, 12)
	result, err := Aggregate([]AgERA5Day{first, otherYear}, weather.ProfileNice)
	if err != nil || result.Days != 2 || result.ExpectedDays != 600 || result.Coverage != float64(2)/600 {
		t.Fatalf("multi-year November distribution=%+v err=%v", result, err)
	}
	if _, err := Aggregate([]AgERA5Day{first, first}, weather.ProfileNice); err == nil {
		t.Fatal("duplicate dates were accepted")
	}
}

func TestAgERA5DistributionMeasuresObservedPoorWeekRisk(t *testing.T) {
	days := make([]AgERA5Day, 10)
	for index := range days {
		temperature, rain, wet, solar, cloud, wind := 24.0, 0.0, 0.0, 15.0, 20.0, 12.0
		if index >= 2 && index <= 4 {
			temperature, rain, wet, solar, cloud, wind = 7, 5, .8, 2, 95, 30
		}
		days[index] = day(fmt.Sprintf("2025-11-%02d", index+1), 52.37, 4.90, temperature, rain, wet, solar, cloud, wind)
	}
	result, err := Aggregate(days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	if result.PoorWeekWindows != 4 || result.PoorWeekFraction != .75 {
		t.Fatalf("poor-week result = %+v; want 3 of 4 complete seven-day windows", result)
	}
}

func TestAgERA5DoesNotInventSynchronousRelativeHumidity(t *testing.T) {
	metrics, err := day("2025-11-01", 36.72, -4.42, 30, 0, 0, 15, 20, 12).Metrics()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RelativeHumidityPercent.Available || !metrics.DewPointC.Available || metrics.NativeResolutionKM != 30 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func day(date string, lat, lon, temperature, rain, wet, solar, cloud, wind float64) AgERA5Day {
	dewPoint := temperature - 10
	if wet > .5 {
		dewPoint = temperature - .2
	}
	return AgERA5Day{Date: date, Latitude: lat, Longitude: lon, DaytimeMeanTemperatureC: temperature, MinimumTemperatureC: temperature - 4, MaximumTemperatureC: temperature + 4, PrecipitationMM: rain, PrecipitationDurationFrac: wet, SolarEnergyMJm2: solar, CloudCoverPercent: cloud, WindSpeedKPH: wind, DewPointC: dewPoint}
}
