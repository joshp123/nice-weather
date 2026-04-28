package weatherinput

import "testing"

func TestDaytimeForecastAggregateAveragesWeatherAndUsesAccumulatedRainDelta(t *testing.T) {
	got := DaytimeForecastAggregate([]WeatherInput{
		{TempC: 20, Humidity: 50, RainMM: 1, CloudPct: 20, WindKMH: 10},
		{TempC: 24, Humidity: 70, RainMM: 5, CloudPct: 60, WindKMH: 20},
	})
	want := WeatherInput{TempC: 22, Humidity: 60, RainMM: 4, CloudPct: 40, WindKMH: 15}
	if got != want {
		t.Fatalf("DaytimeForecastAggregate = %+v, want %+v", got, want)
	}
}
