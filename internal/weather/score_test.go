package weather

import (
	"math"
	"testing"
)

func idealDay() DailyMetrics {
	return DailyMetrics{
		ComfortableTempC: 24, MinTempC: 18, MaxTempC: 27,
		RainMM: 0, WetTimeFraction: known(0),
		SolarEnergyMJm2: known(19), CloudCoverPercent: known(20),
		WindSpeedKPH: 10, GustKPH: known(18),
		DewPointC: known(12), RelativeHumidityPercent: known(62),
		NativeResolutionKM: 7, Valid: true,
	}
}

func known(value float64) OptionalFloat {
	return OptionalFloat{Value: value, Available: true}
}

func TestIdealDayScoresHighly(t *testing.T) {
	for _, profile := range []Profile{ProfileNice, ProfileCycling} {
		result := Score(idealDay(), profile)
		if !result.Available || result.Score < 95 || result.Rating != "Excellent" {
			t.Fatalf("%s ideal day = %+v", profile, result)
		}
	}
}

func TestSixteenDegreesIsMediocre(t *testing.T) {
	day := idealDay()
	day.ComfortableTempC = 16
	result := Score(day, ProfileNice)
	if result.Components.Warmth != 45 {
		t.Fatalf("warmth = %d, want 45", result.Components.Warmth)
	}
	if result.Score > 54 || result.Rating != "Mixed" {
		t.Fatalf("16°C day = %+v; want a mixed score no higher than 54", result)
	}
}

func TestRatingThresholds(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{100, "Excellent"}, {85, "Excellent"},
		{84, "Very good"}, {70, "Very good"},
		{69, "Good"}, {55, "Good"},
		{54, "Mixed"}, {40, "Mixed"},
		{39, "Poor"}, {0, "Poor"},
	}
	for _, test := range tests {
		if got := rating(test.score); got != test.want {
			t.Errorf("rating(%d) = %q, want %q", test.score, got, test.want)
		}
	}
}

func TestCyclingDealbreakers(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
		max  int
	}{
		{"rain", func(day *DailyMetrics) { day.RainMM = 3 }, 45},
		{"wet time", func(day *DailyMetrics) { day.WetTimeFraction.Value = .35 }, 45},
		{"wind", func(day *DailyMetrics) { day.WindSpeedKPH = 28; day.GustKPH.Value = 28 }, 45},
		{"gusts", func(day *DailyMetrics) { day.GustKPH.Value = 40 }, 45},
		{"heavy rain", func(day *DailyMetrics) { day.RainMM = 8 }, 25},
		{"severe gusts", func(day *DailyMetrics) { day.GustKPH.Value = 55 }, 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			if got := Score(day, ProfileCycling).Score; got > test.max {
				t.Fatalf("score = %d, want no more than %d", got, test.max)
			}
		})
	}
}

func TestCyclingIsMoreSensitiveToRainAndWind(t *testing.T) {
	day := idealDay()
	day.RainMM = 3
	day.WindSpeedKPH = 28
	day.GustKPH.Value = 40
	nice := Score(day, ProfileNice).Score
	cycling := Score(day, ProfileCycling).Score
	if cycling >= nice {
		t.Fatalf("cycling score %d should be below nice score %d", cycling, nice)
	}
}

func TestGeneralDealbreakers(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
		max  int
	}{
		{"rain", func(day *DailyMetrics) { day.RainMM = 7 }, 50},
		{"wet time", func(day *DailyMetrics) { day.WetTimeFraction.Value = .6 }, 50},
		{"wind", func(day *DailyMetrics) { day.WindSpeedKPH = 40; day.GustKPH.Value = 40 }, 50},
		{"heavy rain", func(day *DailyMetrics) { day.RainMM = 12 }, 30},
		{"severe gusts", func(day *DailyMetrics) { day.GustKPH.Value = 70 }, 30},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			if got := Score(day, ProfileNice).Score; got > test.max {
				t.Fatalf("score = %d, want no more than %d", got, test.max)
			}
		})
	}
}

func TestBadConditionsNeverIncreaseScore(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		edit   func(*DailyMetrics, float64)
	}{
		{"rain", []float64{0, .2, 1, 3, 6, 10}, func(day *DailyMetrics, value float64) { day.RainMM = value }},
		{"wet time", []float64{0, .1, .25, .5, .8}, func(day *DailyMetrics, value float64) { day.WetTimeFraction.Value = value }},
		{"cloud", []float64{0, 25, 50, 75, 100}, func(day *DailyMetrics, value float64) { day.CloudCoverPercent.Value = value }},
		{"wind", []float64{8, 14, 20, 28, 35, 42}, func(day *DailyMetrics, value float64) { day.WindSpeedKPH = value; day.GustKPH = OptionalFloat{} }},
		{"dew point", []float64{8, 12, 16, 18, 21, 24}, func(day *DailyMetrics, value float64) { day.DewPointC.Value = value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous := 101
			for _, value := range test.values {
				day := idealDay()
				test.edit(&day, value)
				got := Score(day, ProfileCycling).Score
				if got > previous {
					t.Fatalf("score increased from %d to %d at %v", previous, got, value)
				}
				previous = got
			}
		})
	}
}

func TestWarmthPeaksAtComfortableTemperatures(t *testing.T) {
	want := map[float64]int{8: 0, 12: 18, 16: 45, 19: 72, 22: 100, 27: 100, 30: 78, 34: 35, 38: 0}
	for temperature, wantScore := range want {
		day := idealDay()
		day.MinTempC = math.Min(day.MinTempC, temperature)
		day.MaxTempC = math.Max(day.MaxTempC, temperature)
		day.ComfortableTempC = temperature
		if got := Score(day, ProfileNice).Components.Warmth; got != wantScore {
			t.Errorf("%v°C warmth = %d, want %d", temperature, got, wantScore)
		}
	}
}

func TestInvalidAndMissingDataAreUnavailable(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
	}{
		{"invalid flag", func(day *DailyMetrics) { day.Valid = false }},
		{"missing value", func(day *DailyMetrics) { day.RainMM = math.NaN() }},
		{"bad humidity", func(day *DailyMetrics) { day.RelativeHumidityPercent.Value = 101 }},
		{"bad range", func(day *DailyMetrics) { day.MinTempC = 30; day.MaxTempC = 20 }},
		{"missing resolution", func(day *DailyMetrics) { day.NativeResolutionKM = 0 }},
		{"Kelvin passed as Celsius", func(day *DailyMetrics) { day.ComfortableTempC = 297; day.MaxTempC = 300 }},
		{"implausible solar energy", func(day *DailyMetrics) { day.SolarEnergyMJm2.Value = 600 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			result := Score(day, ProfileNice)
			if result.Available || Summary(day) != "No forecast data" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

var benchmarkResult Result

func BenchmarkScore(b *testing.B) {
	day := idealDay()
	for b.Loop() {
		benchmarkResult = Score(day, ProfileCycling)
	}
}

func TestUnknownProfileIsUnavailable(t *testing.T) {
	if got := Score(idealDay(), Profile("sailing")); got.Available {
		t.Fatalf("unexpected available result: %+v", got)
	}
}

func TestOptionalEvidenceIsRenormalised(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*DailyMetrics)
		check func(Result) bool
	}{
		{
			"solar without cloud",
			func(day *DailyMetrics) { day.CloudCoverPercent = OptionalFloat{} },
			func(result Result) bool { return result.Components.Sun == 100 },
		},
		{
			"cloud without solar",
			func(day *DailyMetrics) { day.SolarEnergyMJm2 = OptionalFloat{} },
			func(result Result) bool { return result.Components.Sun == 92 },
		},
		{
			"dew point without humidity",
			func(day *DailyMetrics) { day.RelativeHumidityPercent = OptionalFloat{} },
			func(result Result) bool { return result.Components.Comfort == 100 },
		},
		{
			"humidity without dew point",
			func(day *DailyMetrics) { day.DewPointC = OptionalFloat{} },
			func(result Result) bool { return result.Components.Comfort == 100 },
		},
		{
			"wind without gusts",
			func(day *DailyMetrics) { day.GustKPH = OptionalFloat{} },
			func(result Result) bool { return result.Components.Wind == 100 },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			result := Score(day, ProfileNice)
			if !result.Available || !test.check(result) {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestMissingWetTimeUsesRainEvidenceOnly(t *testing.T) {
	day := idealDay()
	day.RainMM = 1
	day.WetTimeFraction = OptionalFloat{}

	result := Score(day, ProfileNice)
	if !result.Available {
		t.Fatal("climate metrics without wet-time evidence should remain available")
	}
	if result.Components.Dryness != 80 {
		t.Fatalf("dryness = %d, want rain-only score 80", result.Components.Dryness)
	}
	if got := Summary(day); got != "24°C · some rain · sunny · light wind" {
		t.Fatalf("summary = %q", got)
	}

	day.WetTimeFraction = known(0)
	withDryTiming := Score(day, ProfileNice)
	if withDryTiming.Components.Dryness != 87 {
		t.Fatalf("dryness with explicit dry timing = %d, want 87", withDryTiming.Components.Dryness)
	}
}

func TestRequiredEvidenceGroupsCannotBeMissing(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
	}{
		{"sun evidence", func(day *DailyMetrics) {
			day.SolarEnergyMJm2 = OptionalFloat{}
			day.CloudCoverPercent = OptionalFloat{}
		}},
		{"comfort evidence", func(day *DailyMetrics) {
			day.DewPointC = OptionalFloat{}
			day.RelativeHumidityPercent = OptionalFloat{}
		}},
		{"hidden optional value", func(day *DailyMetrics) { day.GustKPH = OptionalFloat{Value: 18} }},
		{"hidden wet-time value", func(day *DailyMetrics) { day.WetTimeFraction = OptionalFloat{Value: .25} }},
		{"optional NaN", func(day *DailyMetrics) { day.GustKPH = OptionalFloat{Value: math.NaN()} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			if result := Score(day, ProfileNice); result.Available {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestCrossFieldConsistency(t *testing.T) {
	tests := []struct {
		name      string
		edit      func(*DailyMetrics)
		available bool
	}{
		{"dew point tolerance", func(day *DailyMetrics) { day.DewPointC.Value = day.MaxTempC + 2 }, true},
		{"dew point above temperature", func(day *DailyMetrics) { day.DewPointC.Value = day.MaxTempC + 2.1 }, false},
		{"gust tolerance", func(day *DailyMetrics) { day.WindSpeedKPH = 20; day.GustKPH.Value = 17 }, true},
		{"gust below sustained wind", func(day *DailyMetrics) { day.WindSpeedKPH = 20; day.GustKPH.Value = 16.9 }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			if got := Score(day, ProfileNice).Available; got != test.available {
				t.Fatalf("available = %v, want %v", got, test.available)
			}
		})
	}
}

func TestSummaryUsesOnlyObservedConditions(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
		want string
	}{
		{"ideal", func(*DailyMetrics) {}, "24°C · dry · sunny · light wind"},
		{"mixed", func(day *DailyMetrics) {
			day.RainMM = 1.2
			day.SolarEnergyMJm2.Value = 7
			day.CloudCoverPercent.Value = 70
			day.WindSpeedKPH = 20
		}, "24°C · some rain · mixed cloud · breezy"},
		{"poor", func(day *DailyMetrics) {
			day.RainMM = 6
			day.SolarEnergyMJm2.Value = 3
			day.CloudCoverPercent.Value = 90
			day.WindSpeedKPH = 30
			day.GustKPH.Value = 35
		}, "24°C · wet · overcast · windy"},
		{"severe gusts", func(day *DailyMetrics) {
			day.WindSpeedKPH = 12
			day.GustKPH.Value = 55
		}, "24°C · dry · sunny · severe gusts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			if got := Summary(day); got != test.want {
				t.Fatalf("summary = %q, want %q", got, test.want)
			}
		})
	}
}

func TestComponentsAndScoreStayInRange(t *testing.T) {
	days := []DailyMetrics{idealDay(), {
		ComfortableTempC: 34, MinTempC: 28, MaxTempC: 40,
		RainMM: 100, WetTimeFraction: known(1), SolarEnergyMJm2: known(0),
		CloudCoverPercent: known(100), WindSpeedKPH: 100, GustKPH: known(150),
		DewPointC: known(30), RelativeHumidityPercent: known(100),
		NativeResolutionKM: 28, Valid: true,
	}}
	for _, day := range days {
		result := Score(day, ProfileCycling)
		values := []int{result.Score, result.Components.Warmth, result.Components.Dryness, result.Components.Sun, result.Components.Wind, result.Components.Comfort}
		for _, value := range values {
			if value < 0 || value > 100 {
				t.Fatalf("out-of-range value %d in %+v", value, result)
			}
		}
	}
}
