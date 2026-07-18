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
	if result.Components.Thermal != 94 {
		t.Fatalf("comfort = %d, want 94", result.Components.Thermal)
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
		{39, "Poor"}, {25, "Poor"},
		{24, "Awful"}, {0, "Awful"},
	}
	for _, test := range tests {
		if got := Rating(test.score); got != test.want {
			t.Errorf("Rating(%d) = %q, want %q", test.score, got, test.want)
		}
	}
}

func TestThermalComfortDistinguishesHeatAndColdMoisture(t *testing.T) {
	hotDry := idealDay()
	hotDry.ComfortableTempC, hotDry.MaxTempC = 30, 34
	hotDry.DewPointC, hotDry.RelativeHumidityPercent = known(5), known(20)
	hotHumid := hotDry
	hotHumid.DewPointC, hotHumid.RelativeHumidityPercent = known(25), known(95)
	if dry, humid := Score(hotDry, ProfileNice), Score(hotHumid, ProfileNice); dry.Components.Thermal < 85 || humid.Components.Thermal != 0 || humid.Score > 39 || dry.Score <= humid.Score {
		t.Fatalf("hot dry = %+v, hot humid = %+v", dry, humid)
	}

	coldDry := idealDay()
	coldDry.ComfortableTempC, coldDry.MinTempC, coldDry.MaxTempC = 7, 3, 10
	coldDry.RainMM, coldDry.WetTimeFraction = 0, known(0)
	coldDry.CloudCoverPercent, coldDry.WindSpeedKPH = known(5), 5
	coldDry.DewPointC, coldDry.RelativeHumidityPercent = known(-5), known(35)
	coldDamp := coldDry
	coldDamp.RainMM, coldDamp.WetTimeFraction = 5, known(.8)
	coldDamp.CloudCoverPercent, coldDamp.WindSpeedKPH = known(100), 30
	coldDamp.GustKPH, coldDamp.DewPointC, coldDamp.RelativeHumidityPercent = known(45), known(7), known(100)
	if dry, damp := Score(coldDry, ProfileNice), Score(coldDamp, ProfileNice); dry.Components.Thermal < 60 || damp.Components.Thermal > 15 || damp.Rating != "Awful" || dry.Rating != "Poor" {
		t.Fatalf("cold dry = %+v, cold damp = %+v", dry, damp)
	}
}

func TestAwfulRequiresMoreThanColdAlone(t *testing.T) {
	coldDry := idealDay()
	coldDry.ComfortableTempC, coldDry.MinTempC, coldDry.MaxTempC = 5, 1, 8
	coldDry.RainMM, coldDry.WetTimeFraction = 0, known(0)
	coldDry.CloudCoverPercent, coldDry.WindSpeedKPH, coldDry.GustKPH = known(5), 5, known(8)
	coldDry.DewPointC, coldDry.RelativeHumidityPercent = known(-5), known(35)
	if got := Score(coldDry, ProfileNice); got.Rating != "Poor" || got.Components.Thermal < 50 {
		t.Fatalf("cold dry day = %+v; cold alone must not be awful", got)
	}

	maritime := coldDry
	maritime.ComfortableTempC, maritime.MinTempC, maritime.MaxTempC = 7, 4, 9
	maritime.RainMM, maritime.WetTimeFraction = 4, known(.65)
	maritime.CloudCoverPercent, maritime.WindSpeedKPH, maritime.GustKPH = known(95), 25, known(40)
	maritime.DewPointC, maritime.RelativeHumidityPercent = known(6), known(95)
	if got := Score(maritime, ProfileNice); got.Rating != "Awful" || got.Components.Thermal > 24 {
		t.Fatalf("ordinary cold maritime day = %+v; want awful compound conditions", got)
	}
}

func TestPhysicalTemperatureExtremesAreAwful(t *testing.T) {
	for _, test := range []struct {
		name        string
		temperature float64
		minimum     float64
		maximum     float64
	}{
		{name: "severe mountain cold", temperature: -10, minimum: -15, maximum: -5},
		{name: "extreme heat", temperature: 40, minimum: 34, maximum: 45},
	} {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			day.ComfortableTempC, day.MinTempC, day.MaxTempC = test.temperature, test.minimum, test.maximum
			if test.temperature < 0 {
				day.DewPointC, day.RelativeHumidityPercent = known(-20), known(20)
			} else {
				day.DewPointC, day.RelativeHumidityPercent = known(5), known(15)
			}
			result := Score(day, ProfileNice)
			if result.Rating != "Awful" || result.Score > 24 || result.Components.Thermal > 24 {
				t.Fatalf("%s = %+v; want an awful physical extreme", test.name, result)
			}
		})
	}
}

func TestEveryLensUsesCanonicalComponents(t *testing.T) {
	day := idealDay()
	result := Score(day, ProfileCycling)
	want := map[Lens]int{
		LensOverall: result.Score, LensComfort: result.Components.Thermal,
		LensDry: result.Components.Dryness, LensBright: result.Components.Sun,
		LensCalm: result.Components.Wind,
	}
	for lens, expected := range want {
		value, ok := LensValue(day, ProfileCycling, lens)
		if !ok || value != expected {
			t.Errorf("%s = %d,%v; want %d,true", lens, value, ok, expected)
		}
	}
	if _, ok := LensValue(day, ProfileNice, Lens("humidity")); ok {
		t.Fatal("unknown lens was available")
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

func TestThermalPeaksAtComfortableTemperatures(t *testing.T) {
	want := map[float64]int{-15: 0, -5: 8, 0: 25, 8: 68, 12: 82, 16: 94, 19: 100, 22: 100, 27: 100, 30: 88, 34: 45, 38: 8, 42: 0}
	for temperature, wantScore := range want {
		day := idealDay()
		day.MinTempC = math.Min(day.MinTempC, temperature)
		day.MaxTempC = math.Max(day.MaxTempC, temperature)
		day.ComfortableTempC = temperature
		if got := Score(day, ProfileNice).Components.Thermal; got != wantScore {
			t.Errorf("%v°C thermal = %d, want %d", temperature, got, wantScore)
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
			func(result Result) bool { return result.Components.Thermal == 100 },
		},
		{
			"humidity without dew point",
			func(day *DailyMetrics) { day.DewPointC = OptionalFloat{} },
			func(result Result) bool { return result.Components.Thermal >= 80 },
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
		values := []int{result.Score, result.Components.Thermal, result.Components.Dryness, result.Components.Sun, result.Components.Wind}
		for _, value := range values {
			if value < 0 || value > 100 {
				t.Fatalf("out-of-range value %d in %+v", value, result)
			}
		}
	}
}
