package scoring

import (
	"testing"

	"nice-weather-finder-poc/internal/weatherinput"
)

func TestTemperatureCurveMatchesWarmBias(t *testing.T) {
	cases := []struct {
		temp float64
		want float64
	}{
		{16, 45},
		{22, 100},
		{27, 100},
		{39, 0},
	}
	for _, tc := range cases {
		got := ComponentScores(weatherinput.WeatherInput{TempC: tc.temp}).Temp
		if got != tc.want {
			t.Fatalf("temp %.1f score = %.1f, want %.1f", tc.temp, got, tc.want)
		}
	}
}

func TestCategoryColors(t *testing.T) {
	cases := []struct {
		score int
		color string
	}{
		{49, "#9ca3af"},
		{50, "#7dd3fc"},
		{62, "#38bdf8"},
		{72, "#a3e635"},
		{82, "#16a34a"},
		{92, "#facc15"},
	}
	for _, tc := range cases {
		got := CategoryFor(tc.score).Color
		if got != tc.color {
			t.Fatalf("score %d color = %s, want %s", tc.score, got, tc.color)
		}
	}
}

func TestCyclingIsNeverMorePermissiveThanGeneral(t *testing.T) {
	cases := []weatherinput.WeatherInput{
		{TempC: 24, Humidity: 50, RainMM: 0, CloudPct: 20, WindKMH: 8},
		{TempC: 24, Humidity: 50, RainMM: 3, CloudPct: 20, WindKMH: 8},
		{TempC: 24, Humidity: 50, RainMM: 0, CloudPct: 20, WindKMH: 32},
		{TempC: 16, Humidity: 80, RainMM: 2, CloudPct: 80, WindKMH: 25},
	}
	for _, input := range cases {
		general := Score(input, ProfileGeneral).Score
		cycling := Score(input, ProfileCycling).Score
		if cycling > general {
			t.Fatalf("cycling score %d must not exceed general score %d for %#v", cycling, general, input)
		}
	}
}

func TestCyclingPerfectRequiresGoodRainAndWind(t *testing.T) {
	input := weatherinput.WeatherInput{TempC: 24, Humidity: 50, RainMM: 0, CloudPct: 20, WindKMH: 32}
	if got := Score(input, ProfileCycling).Score; got >= 82 {
		t.Fatalf("windy cycling score = %d, want below Perfect", got)
	}
}
