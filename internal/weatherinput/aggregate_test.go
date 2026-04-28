package weatherinput

import "testing"

func TestDailyAggregateAveragesWeatherAndSumsRain(t *testing.T) {
	got := DailyAggregate([]WeatherInput{
		{TempC: 20, Humidity: 50, RainMM: 1, CloudPct: 20, WindKMH: 10},
		{TempC: 24, Humidity: 70, RainMM: 3, CloudPct: 60, WindKMH: 20},
	})
	want := WeatherInput{TempC: 22, Humidity: 60, RainMM: 4, CloudPct: 40, WindKMH: 15}
	if got != want {
		t.Fatalf("DailyAggregate = %+v, want %+v", got, want)
	}
}
