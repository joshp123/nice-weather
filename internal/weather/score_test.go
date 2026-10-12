package weather

import (
	"math"
	"slices"
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

func TestPublishedEuropeanGustBoundaryScoresIdenticallyAcrossArchitectures(t *testing.T) {
	day := DailyMetrics{
		ComfortableTempC: -5, MinTempC: -5.2, MaxTempC: -4.4,
		DewPointC: known(-9.3), RainMM: 3.7, WetTimeFraction: known(.862),
		WindSpeedKPH: 36.8, GustKPH: known(63.5), SolarEnergyMJm2: known(1.18),
		CloudCoverPercent: known(100), RelativeHumidityPercent: known(72),
		NativeResolutionKM: 7, Valid: true,
	}
	nice, cycling, err := ScoreProfilesChecked(day)
	if err != nil {
		t.Fatal(err)
	}
	if nice.Components != (Components{Thermal: 8, Wind: 4}) || nice.Score != 4 || cycling.Components != (Components{Thermal: 8}) || cycling.Score != 3 {
		t.Fatalf("published Oct 12 European record: Nice %+v, Cycling %+v", nice, cycling)
	}
	t.Logf("Nice %d wind %d; Cycling %d wind %d", nice.Score, nice.Components.Wind, cycling.Score, cycling.Components.Wind)
}

func TestIdealDayScoresHighly(t *testing.T) {
	for _, profile := range []Profile{ProfileNice, ProfileCycling} {
		result := Score(idealDay(), profile)
		if !result.Available || result.Score < 95 || result.Rating != "Excellent" {
			t.Fatalf("%s ideal day = %+v", profile, result)
		}
	}
}

func TestScoreProfilesExactlyMatchesSingleProfilePath(t *testing.T) {
	days := []DailyMetrics{idealDay()}
	for _, temperature := range []float64{-10, 5, 16, 24, 30, 38} {
		day := idealDay()
		day.ComfortableTempC = temperature
		day.MinTempC, day.MaxTempC = temperature-4, temperature+4
		day.DewPointC = known(min(temperature, 24))
		days = append(days, day)
	}
	for _, wind := range []float64{0, 20, 28, 40, 60} {
		day := idealDay()
		day.WindSpeedKPH, day.GustKPH = wind, known(wind+10)
		days = append(days, day)
	}
	for _, rain := range []float64{0, 1, 3, 8, 12} {
		day := idealDay()
		day.RainMM = rain
		day.WetTimeFraction = known(min(1, rain/12))
		days = append(days, day)
	}
	for index, day := range days {
		nice, cycling, err := ScoreProfilesChecked(day)
		if err != nil {
			t.Fatal(err)
		}
		if want := Score(day, ProfileNice); nice != want {
			t.Fatalf("day %d Nice = %+v, want %+v", index, nice, want)
		}
		if want := Score(day, ProfileCycling); cycling != want {
			t.Fatalf("day %d Cycling = %+v, want %+v", index, cycling, want)
		}
	}
	invalid := idealDay()
	invalid.Valid = false
	if nice, cycling, err := ScoreProfilesChecked(invalid); nice != (Result{}) || cycling != (Result{}) || err == nil || err.Error() != "weather data is marked invalid" {
		t.Fatalf("checked invalid profiles = %+v %+v, error %v", nice, cycling, err)
	}
}

func TestScoreProfilesCheckedAllocatesNothingForValidMetrics(t *testing.T) {
	day := idealDay()
	allocations := testing.AllocsPerRun(1000, func() {
		nice, cycling, err := ScoreProfilesChecked(day)
		if err != nil || !nice.Available || !cycling.Available {
			panic("valid fixture became unscoreable")
		}
	})
	if allocations != 0 {
		t.Fatalf("checked profile allocations = %.2f; want 0", allocations)
	}
}

func TestSixteenDegreesIsMediocre(t *testing.T) {
	day := idealDay()
	day.ComfortableTempC = 16
	result := Score(day, ProfileNice)
	if result.Components.Thermal != 54 {
		t.Fatalf("comfort = %d, want 54", result.Components.Thermal)
	}
	if result.Score != result.Components.Thermal || result.Rating != "Mixed" {
		t.Fatalf("16°C day = %+v; want visible Comfort to explain the Mixed overall score", result)
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
	oppressive := hotDry
	oppressive.DewPointC, oppressive.RelativeHumidityPercent = known(24), known(relativeHumidityFromDewPoint(30, 24))
	saturated := hotDry
	saturated.DewPointC, saturated.RelativeHumidityPercent = known(30), known(100)
	oppressiveFallback := oppressive
	oppressiveFallback.DewPointC = OptionalFloat{}
	saturatedFallback := saturated
	saturatedFallback.DewPointC = OptionalFloat{}
	if dry, humid, humidFallback, extreme, extremeFallback := Score(hotDry, ProfileNice), Score(oppressive, ProfileNice), Score(oppressiveFallback, ProfileNice), Score(saturated, ProfileNice), Score(saturatedFallback, ProfileNice); dry.Components.Thermal < 85 || humid.Components.Thermal > 24 || humid.Rating != "Awful" || humidFallback != humid || extreme.Components.Thermal != 0 || extreme.Rating != "Awful" || extremeFallback.Rating != "Awful" || dry.Score <= humid.Score || humid.Score < extreme.Score {
		t.Fatalf("hot dry = %+v, oppressive = %+v, RH fallback = %+v, saturated heat = %+v, saturated fallback = %+v", dry, humid, humidFallback, extreme, extremeFallback)
	}

	for _, dewPoint := range []float64{5, 18, 20, 22, 24, 26, 30} {
		withDewPoint := hotDry
		withDewPoint.DewPointC = known(dewPoint)
		withDewPoint.RelativeHumidityPercent = known(relativeHumidityFromDewPoint(30, dewPoint))
		withHumidity := withDewPoint
		withHumidity.DewPointC = OptionalFloat{}
		direct, fallback := Score(withDewPoint, ProfileNice), Score(withHumidity, ProfileNice)
		if direct != fallback {
			t.Fatalf("30°C / %.0f°C dew point = %+v, equivalent RH fallback = %+v", dewPoint, direct, fallback)
		}
	}
	lowHumidity := hotDry
	lowHumidity.DewPointC = OptionalFloat{}
	lowHumidity.RelativeHumidityPercent = known(20)
	if got := Score(lowHumidity, ProfileNice); got.Rating != "Excellent" {
		t.Fatalf("30°C / 20%% RH fallback = %+v; want Excellent", got)
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
	if dry, damp := Score(coldDry, ProfileNice), Score(coldDamp, ProfileNice); dry.Components.Thermal < 25 || damp.Components.Thermal > 15 || damp.Rating != "Awful" || dry.Rating != "Poor" {
		t.Fatalf("cold dry = %+v, cold damp = %+v", dry, damp)
	}
}

func TestPhysicalWeatherScenarios(t *testing.T) {
	tests := []struct {
		name        string
		day         DailyMetrics
		wantRating  string
		wantThermal int
		minScore    int
	}{
		{
			name: "Singapore tropical humidity",
			day: DailyMetrics{
				ComfortableTempC: 28, MinTempC: 25, MaxTempC: 31,
				RainMM: 2, WetTimeFraction: OptionalFloat{},
				SolarEnergyMJm2: known(12), CloudCoverPercent: known(98.8),
				WindSpeedKPH: 8, GustKPH: OptionalFloat{},
				DewPointC: known(23.9), RelativeHumidityPercent: known(79),
				NativeResolutionKM: 28, Valid: true,
			},
			wantRating: "Awful", wantThermal: 2,
		},
		{
			name: "warm Dutch summer",
			day: DailyMetrics{
				ComfortableTempC: 25, MinTempC: 18, MaxTempC: 27,
				RainMM: 0, WetTimeFraction: known(0),
				SolarEnergyMJm2: known(18), CloudCoverPercent: known(20),
				WindSpeedKPH: 10, GustKPH: known(18),
				DewPointC: known(16), RelativeHumidityPercent: known(57),
				NativeResolutionKM: 7, Valid: true,
			},
			wantRating: "Excellent", wantThermal: 100, minScore: 95,
		},
		{
			name: "Aswan dry heat",
			day: DailyMetrics{
				ComfortableTempC: 30.9, MinTempC: 23, MaxTempC: 35,
				RainMM: 0, WetTimeFraction: OptionalFloat{},
				SolarEnergyMJm2: known(19), CloudCoverPercent: known(1.8),
				WindSpeedKPH: 11, GustKPH: OptionalFloat{},
				DewPointC: known(8.9), RelativeHumidityPercent: known(26),
				NativeResolutionKM: 28, Valid: true,
			},
			wantRating: "Very good", wantThermal: 78, minScore: 78,
		},
		{
			name: "Malaga mild clear season",
			day: DailyMetrics{
				ComfortableTempC: 16.3, MinTempC: 12, MaxTempC: 20,
				RainMM: 0, WetTimeFraction: OptionalFloat{},
				SolarEnergyMJm2: known(10), CloudCoverPercent: known(42),
				WindSpeedKPH: 10, GustKPH: OptionalFloat{},
				DewPointC: known(8.3), RelativeHumidityPercent: known(59),
				NativeResolutionKM: 28, Valid: true,
			},
			wantRating: "Good", wantThermal: 56, minScore: 55,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Score(test.day, ProfileNice)
			if !result.Available || result.Rating != test.wantRating || result.Components.Thermal != test.wantThermal || result.Score < test.minScore {
				t.Fatalf("result = %+v; want rating %s, comfort %d, score >= %d", result, test.wantRating, test.wantThermal, test.minScore)
			}
		})
	}
}

func TestDewPointComfortUsesWeatherSparkBands(t *testing.T) {
	for dewPoint, want := range map[float64]int{
		13: 100,
		16: 100,
		18: 85,
		21: 55,
		24: 0,
	} {
		day := idealDay()
		day.DewPointC = known(dewPoint)
		day.RelativeHumidityPercent = known(relativeHumidityFromDewPoint(day.ComfortableTempC, dewPoint))
		if got := Score(day, ProfileNice).Components.Thermal; got != want {
			t.Errorf("%.0f°C dew point comfort = %d; want %d", dewPoint, got, want)
		}
	}
}

func relativeHumidityFromDewPoint(temperatureC, dewPointC float64) float64 {
	const a, b = 17.625, 243.04
	return 100 * math.Exp(a*dewPointC/(b+dewPointC)-a*temperatureC/(b+temperatureC))
}

func TestAwfulRequiresMoreThanColdAlone(t *testing.T) {
	coldDry := idealDay()
	coldDry.ComfortableTempC, coldDry.MinTempC, coldDry.MaxTempC = 5, 1, 8
	coldDry.RainMM, coldDry.WetTimeFraction = 0, known(0)
	coldDry.CloudCoverPercent, coldDry.WindSpeedKPH, coldDry.GustKPH = known(5), 5, known(8)
	coldDry.DewPointC, coldDry.RelativeHumidityPercent = known(-5), known(35)
	if got := Score(coldDry, ProfileNice); got.Rating != "Poor" || got.Components.Thermal != 25 {
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

func TestCyclingDoesNotMakeOrdinaryWindLookCalmer(t *testing.T) {
	day := idealDay()
	day.WindSpeedKPH = 15
	day.GustKPH = OptionalFloat{}
	nice, cycling := Score(day, ProfileNice), Score(day, ProfileCycling)
	if nice.Components.Wind != 93 || cycling.Components.Wind != 93 {
		t.Fatalf("15 km/h Calm: Nice %d, Cycling %d; want 93 for both", nice.Components.Wind, cycling.Components.Wind)
	}
}

func TestCyclingRainAndWindComponentsNeverOutrankNice(t *testing.T) {
	tests := []struct {
		name string
		edit func(*DailyMetrics)
	}{
		{"ordinary sustained wind", func(day *DailyMetrics) { day.WindSpeedKPH = 15; day.GustKPH = OptionalFloat{} }},
		{"moderate sustained wind", func(day *DailyMetrics) { day.WindSpeedKPH = 20; day.GustKPH = OptionalFloat{} }},
		{"strong sustained wind", func(day *DailyMetrics) { day.WindSpeedKPH = 35; day.GustKPH = OptionalFloat{} }},
		{"moderate gusts", func(day *DailyMetrics) { day.WindSpeedKPH = 10; day.GustKPH = known(30) }},
		{"strong gusts", func(day *DailyMetrics) { day.WindSpeedKPH = 10; day.GustKPH = known(40) }},
		{"light rain", func(day *DailyMetrics) { day.RainMM = 1 }},
		{"ride-ending rain", func(day *DailyMetrics) { day.RainMM = 3 }},
		{"persistent rain", func(day *DailyMetrics) { day.WetTimeFraction = known(.65) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			nice, cycling := Score(day, ProfileNice), Score(day, ProfileCycling)
			if cycling.Components.Wind > nice.Components.Wind {
				t.Fatalf("Cycling Calm %d outranks Nice Calm %d: nice=%+v cycling=%+v", cycling.Components.Wind, nice.Components.Wind, nice, cycling)
			}
			if cycling.Components.Dryness != nice.Components.Dryness {
				t.Fatalf("profile changed the shared Dry fact: Nice %d, Cycling %d", nice.Components.Dryness, cycling.Components.Dryness)
			}
		})
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

func TestDealbreakersAreVisibleInTheirComponent(t *testing.T) {
	tests := []struct {
		name      string
		profile   Profile
		edit      func(*DailyMetrics)
		component func(Components) int
	}{
		{"cycling rain", ProfileCycling, func(day *DailyMetrics) { day.RainMM = 3 }, func(c Components) int { return c.Dryness }},
		{"cycling wet time", ProfileCycling, func(day *DailyMetrics) { day.WetTimeFraction.Value = .35 }, func(c Components) int { return c.Dryness }},
		{"cycling wind", ProfileCycling, func(day *DailyMetrics) { day.WindSpeedKPH = 28 }, func(c Components) int { return c.Wind }},
		{"cycling gusts", ProfileCycling, func(day *DailyMetrics) { day.GustKPH.Value = 40 }, func(c Components) int { return c.Wind }},
		{"general rain", ProfileNice, func(day *DailyMetrics) { day.RainMM = 7 }, func(c Components) int { return c.Dryness }},
		{"general wet time", ProfileNice, func(day *DailyMetrics) { day.WetTimeFraction.Value = .6 }, func(c Components) int { return c.Dryness }},
		{"general wind", ProfileNice, func(day *DailyMetrics) { day.WindSpeedKPH = 40 }, func(c Components) int { return c.Wind }},
		{"general gusts", ProfileNice, func(day *DailyMetrics) { day.GustKPH.Value = 60 }, func(c Components) int { return c.Wind }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			day := idealDay()
			test.edit(&day)
			result := Score(day, test.profile)
			if visible := test.component(result.Components); Rating(visible) == "Good" || Rating(visible) == "Very good" || Rating(visible) == "Excellent" {
				t.Fatalf("dealbreaker is hidden: result=%+v relevant component=%d", result, visible)
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
	want := map[float64]int{-15: 0, -5: 8, 0: 20, 5: 25, 8: 35, 12: 45, 16: 54, 19: 75, 22: 100, 27: 100, 30: 88, 34: 45, 38: 8, 42: 0}
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

func TestOptionalEvidenceUsesCanonicalInputs(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*DailyMetrics)
		check func(Result) bool
	}{
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
	if withDryTiming.Components.Dryness != 80 {
		t.Fatalf("dryness with explicit dry timing = %d, want rain-limited score 80", withDryTiming.Components.Dryness)
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

func TestClearSkyIgnoresSolarEnergyAndRequiresCloudCover(t *testing.T) {
	lowSolar := idealDay()
	lowSolar.SolarEnergyMJm2 = known(1)
	highSolar := idealDay()
	highSolar.SolarEnergyMJm2 = known(30)
	if low, high := Score(lowSolar, ProfileNice), Score(highSolar, ProfileNice); !low.Available || low != high {
		t.Fatalf("equal cloud cover produced different scores: low solar=%+v high solar=%+v", low, high)
	}
	if low, high := Summary(lowSolar), Summary(highSolar); low != high || low != "24°C · dry · sunny · light wind" {
		t.Fatalf("equal cloud cover produced different summaries: %q and %q", low, high)
	}

	missingCloud := idealDay()
	missingCloud.CloudCoverPercent = OptionalFloat{}
	if result := Score(missingCloud, ProfileNice); result.Available {
		t.Fatalf("score without cloud evidence = %+v", result)
	}
	if got := Summary(missingCloud); got != "No forecast data" {
		t.Fatalf("summary without cloud evidence = %q", got)
	}
}

func TestSummaryNamesMaterialThermalDiscomfort(t *testing.T) {
	hotDry := idealDay()
	hotDry.ComfortableTempC, hotDry.MinTempC, hotDry.MaxTempC = 35, 28, 38
	hotDry.DewPointC, hotDry.RelativeHumidityPercent = known(5), known(20)

	hotHumid := idealDay()
	hotHumid.ComfortableTempC, hotHumid.MinTempC, hotHumid.MaxTempC = 27.9, 24, 31
	hotHumid.DewPointC, hotHumid.RelativeHumidityPercent = known(27.9), known(100)

	coldDry := idealDay()
	coldDry.ComfortableTempC, coldDry.MinTempC, coldDry.MaxTempC = 7, 3, 10
	coldDry.WindSpeedKPH, coldDry.GustKPH = 5, known(8)
	coldDry.DewPointC, coldDry.RelativeHumidityPercent = known(-5), known(35)

	coldDamp := coldDry
	coldDamp.RainMM, coldDamp.WetTimeFraction = 5, known(.8)
	coldDamp.SolarEnergyMJm2, coldDamp.CloudCoverPercent = known(0), known(100)
	coldDamp.WindSpeedKPH, coldDamp.GustKPH = 30, known(45)
	coldDamp.DewPointC, coldDamp.RelativeHumidityPercent = known(7), known(100)

	tests := []struct {
		name       string
		day        DailyMetrics
		want       string
		wantNotice bool
	}{
		{"hot dry", hotDry, "35°C · too hot · dry · sunny · light wind", true},
		{"hot humid just above the comfort plateau", hotHumid, "28°C · too hot · dry · sunny · light wind", true},
		{"cold dry", coldDry, "7°C · too cold · dry · sunny · light wind", true},
		{"cold damp", coldDamp, "7°C · too cold · wet · overcast · strong gusts", true},
		{"comfortable", idealDay(), "24°C · dry · sunny · light wind", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Score(test.day, ProfileNice)
			thermalRating := Rating(result.Components.Thermal)
			if test.wantNotice && thermalRating != "Poor" && thermalRating != "Awful" {
				t.Fatalf("thermal component = %+v; notice must describe a Poor or Awful thermal result", result)
			}
			if got := Summary(test.day); got != test.want {
				t.Fatalf("summary = %q, want %q", got, test.want)
			}
			code, err := EncodeSummary(test.day)
			if err != nil || len(SummaryParts(code)) != 3+btoi(test.wantNotice) {
				t.Fatalf("summary code = %d, parts = %v, err = %v", code, SummaryParts(code), err)
			}
		})
	}
}

func TestSummaryCodeBoundaries(t *testing.T) {
	tests := []struct {
		code uint8
		want []string
	}{
		{79, []string{"wet", "sunny", "severe gusts"}},
		{80, []string{"too hot", "dry", "overcast", "light wind"}},
		{159, []string{"too hot", "wet", "sunny", "severe gusts"}},
		{160, []string{"too cold", "dry", "overcast", "light wind"}},
		{239, []string{"too cold", "wet", "sunny", "severe gusts"}},
	}
	for _, test := range tests {
		if got := SummaryParts(test.code); !slices.Equal(got, test.want) {
			t.Errorf("SummaryParts(%d) = %v, want %v", test.code, got, test.want)
		}
	}
	for _, code := range []uint8{240, 255} {
		if got := SummaryParts(code); got != nil {
			t.Errorf("SummaryParts(%d) = %v, want invalid", code, got)
		}
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
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
