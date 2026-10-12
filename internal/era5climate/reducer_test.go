package era5climate

import (
	"math"
	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/weather"
	"reflect"
	"testing"
	"time"
)

func TestPhysicalQuantilesUseInclusiveLinearInterpolation(t *testing.T) {
	values := []float64{0, 10, 20, 30, 40}
	if got := quantile(values, .10); got != 4 {
		t.Fatalf("p10 = %v; want 4", got)
	}
	if got := quantile(values, .50); got != 20 {
		t.Fatalf("p50 = %v; want 20", got)
	}
	if got := quantile(values, .90); got != 36 {
		t.Fatalf("p90 = %v; want 36", got)
	}
	if !reflect.DeepEqual(values, []float64{0, 10, 20, 30, 40}) {
		t.Fatal("quantile mutated source values")
	}
}

func TestPhysicalSummaryCountsMuggyDaytimeMeansAt18C(t *testing.T) {
	dewPoints := []float64{17.9, 18, 21, 24}
	observations := make([]climate.DailyObservation, len(dewPoints))
	for index, dewPoint := range dewPoints {
		metrics := variedMetrics(2025, time.July, index+1, 0)
		metrics.DewPointC.Value = dewPoint
		observations[index] = climate.DailyObservation{
			Date:    time.Date(2025, time.July, index+1, 0, 0, 0, 0, time.UTC),
			Metrics: metrics,
		}
	}
	summary, err := summarizePhysical(observations, 50)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MuggyDayPercent != 75 {
		t.Fatalf("muggy daytime means = %d%%; want 75%% at dew point >=18°C", summary.MuggyDayPercent)
	}
	if summary.RelativeHumidityP50Percent != 60 {
		t.Fatalf("median daytime relative humidity = %v%%; want 60%%", summary.RelativeHumidityP50Percent)
	}
	if summary.ClearSkyDayPercent != 50 {
		t.Fatalf("clear-sky daily share = %d%%; want supplied canonical 50%%", summary.ClearSkyDayPercent)
	}
}

func variedMetrics(year int, _ time.Month, day, cell int) weather.DailyMetrics {
	offset := float64((year - 2000 + day + cell*3) % 17)
	temperature := 14 + offset
	rain := 0.0
	if day%4 == 0 {
		rain = float64(day%9) / 2
	}
	cloud := float64((day*11 + cell*17) % 101)
	dew := math.Min(temperature-1, 8+float64((day+cell)%16))
	return weather.DailyMetrics{
		ComfortableTempC: temperature,
		MinTempC:         temperature - 4,
		MaxTempC:         temperature + 4,
		RainMM:           rain,
		WetTimeFraction:  weather.OptionalFloat{Value: float64(day%5) / 10, Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: 12, Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: cloud, Available: true,
		},
		WindSpeedKPH: 5 + float64((day+cell)%20),
		DewPointC: weather.OptionalFloat{
			Value: dew, Available: true,
		},
		RelativeHumidityPercent: weather.OptionalFloat{
			Value: 60, Available: true,
		},
		NativeResolutionKM: era5hourly.NativeDetailKM,
		Valid:              true,
	}
}
