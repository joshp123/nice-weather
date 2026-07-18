package weather

import (
	"math"
	"strconv"
	"strings"
)

// Profile selects the meaning of nice weather.
type Profile string

const (
	ProfileNice    Profile = "nice"
	ProfileCycling Profile = "cycling"
)

// Components explains the score. Every component is in the range 0–100.
type Components struct {
	Thermal int
	Dryness int
	Sun     int
	Wind    int
}

// Lens selects one already-evaluated weather judgement for a map overlay. It
// never changes the scoring policy; it only chooses which part of Result is
// shown.
type Lens string

const (
	LensOverall Lens = "overall"
	LensComfort Lens = "comfort"
	LensDry     Lens = "dry"
	LensBright  Lens = "bright"
	LensCalm    Lens = "calm"
)

// Result is the complete product judgement for one day and place.
type Result struct {
	Available  bool
	Score      int
	Rating     string
	Components Components
}

type weights struct {
	warmth, dryness, sun, wind float64
}

// Score applies the single scoring path used by both map rendering and point
// details. Invalid data returns an unavailable result instead of a plausible
// looking zero.
func Score(m DailyMetrics, profile Profile) Result {
	if m.Validate() != nil || (profile != ProfileNice && profile != ProfileCycling) {
		return Result{}
	}

	components := Components{
		Thermal: thermalScore(m),
		Dryness: drynessScore(m),
		Sun:     sunScore(m),
	}

	var w weights
	if profile == ProfileCycling {
		components.Wind = cyclingWindScore(m.WindSpeedKPH, m.GustKPH)
		w = weights{warmth: .35, dryness: .30, sun: .10, wind: .25}
	} else {
		components.Wind = niceWindScore(m.WindSpeedKPH, m.GustKPH)
		w = weights{warmth: .45, dryness: .25, sun: .20, wind: .10}
	}

	value := float64(components.Thermal)*w.warmth + float64(components.Dryness)*w.dryness +
		float64(components.Sun)*w.sun + float64(components.Wind)*w.wind
	value = applyDealbreakers(value, m, profile, components)
	score := rounded(value)
	return Result{
		Available:  true,
		Score:      score,
		Rating:     Rating(score),
		Components: components,
	}
}

// LensValue returns the selected value from the canonical score. Rendering
// code uses this rather than duplicating component maths.
func LensValue(m DailyMetrics, profile Profile, lens Lens) (int, bool) {
	result := Score(m, profile)
	if !result.Available {
		return 0, false
	}
	switch lens {
	case LensOverall:
		return result.Score, true
	case LensComfort:
		return result.Components.Thermal, true
	case LensDry:
		return result.Components.Dryness, true
	case LensBright:
		return result.Components.Sun, true
	case LensCalm:
		return result.Components.Wind, true
	default:
		return 0, false
	}
}

func drynessScore(m DailyMetrics) int {
	rain := piecewise(m.RainMM, rainCurve)
	if !m.WetTimeFraction.Available {
		return rounded(rain)
	}
	return rounded(.65*rain + .35*piecewise(m.WetTimeFraction.Value, wetTimeCurve))
}

func niceWindScore(wind float64, gust OptionalFloat) int {
	sustained := piecewise(wind, niceWindCurve)
	if !gust.Available {
		return rounded(sustained)
	}
	return rounded(.7*sustained + .3*piecewise(gust.Value, niceGustCurve))
}

func cyclingWindScore(wind float64, gust OptionalFloat) int {
	sustained := piecewise(wind, cyclingWindCurve)
	if !gust.Available {
		return rounded(sustained)
	}
	return rounded(.65*sustained + .35*piecewise(gust.Value, cyclingGustCurve))
}

func sunScore(m DailyMetrics) int {
	score, weight := 0.0, 0.0
	if m.SolarEnergyMJm2.Available {
		score += .7 * piecewise(m.SolarEnergyMJm2.Value, solarCurve)
		weight += .7
	}
	if m.CloudCoverPercent.Available {
		score += .3 * piecewise(m.CloudCoverPercent.Value, cloudCurve)
		weight += .3
	}
	return rounded(score / weight)
}

func thermalScore(m DailyMetrics) int {
	base := piecewise(m.ComfortableTempC, thermalCurve)
	// Warm-air moisture is best described by dew point: 30°C with a low dew
	// point can feel excellent while the same temperature with a 24°C dew
	// point is oppressive. Relative humidity is used only when dew point is not
	// available because it changes meaning with temperature.
	heat := 100.0
	if m.ComfortableTempC >= 18 {
		if m.DewPointC.Available {
			heat = piecewise(m.DewPointC.Value, dewPointCurve)
		} else {
			heat = piecewise(m.RelativeHumidityPercent.Value, hotHumidityCurve)
		}
	}

	// Cold damp misery is a compound condition, not "humidity" by itself.
	// Wetness, grey sky and wind progressively matter below 16°C. A cold, dry,
	// bright and calm day therefore remains comfortable even though it is not
	// warm enough to score well overall.
	coldDamp := 100.0
	if m.ComfortableTempC < 16 {
		damp := 0.0
		if m.WetTimeFraction.Available {
			damp = math.Max(damp, m.WetTimeFraction.Value)
		} else {
			damp = math.Max(damp, math.Min(1, m.RainMM/6))
		}
		if m.CloudCoverPercent.Available {
			damp = .55*damp + .45*math.Max(0, (m.CloudCoverPercent.Value-45)/55)
		}
		if m.RelativeHumidityPercent.Available {
			damp = .75*damp + .25*math.Max(0, (m.RelativeHumidityPercent.Value-70)/30)
		}
		windExposure := math.Max(0, math.Min(1, (m.WindSpeedKPH-8)/24))
		damp = math.Min(1, .75*damp+.25*windExposure)
		coldWeight := math.Min(1, math.Max(0, (16-m.ComfortableTempC)/9))
		coldDamp = 100 * (1 - coldWeight*damp)
	}
	return rounded(math.Min(base, math.Min(heat, coldDamp)))
}

func applyDealbreakers(score float64, m DailyMetrics, profile Profile, components Components) float64 {
	// Warmth is the main purpose of the product. A flawless but cold day must
	// not outrank a genuinely warm one.
	score = math.Min(score, piecewise(m.ComfortableTempC, temperatureCeilingCurve))
	if m.ComfortableTempC >= 28 && components.Thermal <= 25 {
		// Oppressive heat cannot average its way into a good day.
		score = math.Min(score, 39)
	}
	if m.ComfortableTempC <= 12 && components.Thermal <= 24 {
		// Cold, damp exposure is the distinction between merely cold and
		// genuinely awful northern-maritime weather.
		score = math.Min(score, 24)
	}
	if profile == ProfileCycling {
		if m.RainMM >= 8 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .65) || m.WindSpeedKPH >= 38 || (m.GustKPH.Available && m.GustKPH.Value >= 55) {
			return math.Min(score, 25)
		}
		if m.RainMM >= 3 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .35) || m.WindSpeedKPH >= 28 || (m.GustKPH.Available && m.GustKPH.Value >= 40) {
			return math.Min(score, 45)
		}
		return score
	}
	if m.RainMM >= 12 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .8) || m.WindSpeedKPH >= 48 || (m.GustKPH.Available && m.GustKPH.Value >= 70) {
		return math.Min(score, 30)
	}
	if m.RainMM >= 7 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .6) || m.WindSpeedKPH >= 40 || (m.GustKPH.Available && m.GustKPH.Value >= 60) {
		return math.Min(score, 50)
	}
	return score
}

type point struct{ input, score float64 }

var (
	thermalCurve            = []point{{-15, 0}, {-5, 8}, {0, 25}, {5, 55}, {8, 68}, {12, 82}, {16, 94}, {19, 100}, {27, 100}, {30, 88}, {34, 45}, {38, 8}, {42, 0}}
	temperatureCeilingCurve = []point{{-15, 0}, {-5, 10}, {0, 20}, {5, 25}, {8, 35}, {12, 45}, {16, 54}, {19, 75}, {22, 100}, {29, 100}, {32, 75}, {35, 45}, {38, 25}, {40, 15}, {45, 0}}
	rainCurve               = []point{{0, 100}, {.2, 96}, {1, 80}, {3, 48}, {6, 18}, {10, 0}}
	wetTimeCurve            = []point{{0, 100}, {.1, 92}, {.25, 70}, {.5, 30}, {.8, 0}}
	solarCurve              = []point{{0, 0}, {4, 20}, {8, 45}, {12, 70}, {18, 100}}
	cloudCurve              = []point{{0, 100}, {25, 90}, {50, 60}, {75, 25}, {100, 0}}
	dewPointCurve           = []point{{8, 100}, {12, 100}, {16, 84}, {18, 64}, {21, 30}, {24, 0}}
	hotHumidityCurve        = []point{{40, 100}, {60, 90}, {75, 65}, {90, 30}, {100, 0}}
	niceWindCurve           = []point{{0, 100}, {12, 100}, {20, 82}, {30, 48}, {45, 0}}
	niceGustCurve           = []point{{0, 100}, {20, 100}, {35, 75}, {50, 35}, {65, 0}}
	cyclingWindCurve        = []point{{0, 100}, {14, 100}, {20, 78}, {28, 38}, {35, 10}, {42, 0}}
	cyclingGustCurve        = []point{{0, 100}, {20, 100}, {30, 70}, {40, 30}, {50, 0}}
)

func piecewise(value float64, points []point) float64 {
	if value <= points[0].input {
		return points[0].score
	}
	for index := 1; index < len(points); index++ {
		if value <= points[index].input {
			left, right := points[index-1], points[index]
			position := (value - left.input) / (right.input - left.input)
			return left.score + position*(right.score-left.score)
		}
	}
	return points[len(points)-1].score
}

func rounded(value float64) int {
	return int(math.Round(math.Max(0, math.Min(100, value))))
}

// Rating returns the canonical semantic band for any evaluated score or
// component. It is used by the API so the browser never owns category policy.
func Rating(score int) string {
	switch {
	case score >= 85:
		return "Excellent"
	case score >= 70:
		return "Very good"
	case score >= 55:
		return "Good"
	case score >= 40:
		return "Mixed"
	case score >= 25:
		return "Poor"
	default:
		return "Awful"
	}
}

// Summary describes observed conditions without trying to explain or market
// the score. Invalid data has one clear fallback.
func Summary(m DailyMetrics) string {
	if m.Validate() != nil {
		return "No forecast data"
	}
	parts := []string{temperaturePhrase(m.ComfortableTempC), rainPhrase(m), sunPhrase(m), windPhrase(m.WindSpeedKPH, m.GustKPH)}
	return strings.Join(parts, " · ")
}

func temperaturePhrase(celsius float64) string {
	return strconv.Itoa(int(math.Round(celsius))) + "°C"
}

func rainPhrase(m DailyMetrics) string {
	switch {
	case m.RainMM >= 5 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .5):
		return "wet"
	case m.RainMM >= 1 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .25):
		return "some rain"
	case m.RainMM >= .2 || (m.WetTimeFraction.Available && m.WetTimeFraction.Value >= .1):
		return "brief rain"
	default:
		return "dry"
	}
}

func sunPhrase(m DailyMetrics) string {
	switch {
	case m.SolarEnergyMJm2.Available && m.SolarEnergyMJm2.Value >= 16 && (!m.CloudCoverPercent.Available || m.CloudCoverPercent.Value < 40):
		return "sunny"
	case (m.SolarEnergyMJm2.Available && m.SolarEnergyMJm2.Value >= 10) || (m.CloudCoverPercent.Available && m.CloudCoverPercent.Value < 55):
		return "bright"
	case (m.SolarEnergyMJm2.Available && m.SolarEnergyMJm2.Value >= 5) || (m.CloudCoverPercent.Available && m.CloudCoverPercent.Value < 80):
		return "mixed cloud"
	default:
		return "overcast"
	}
}

func windPhrase(kph float64, gust OptionalFloat) string {
	switch {
	case gust.Available && gust.Value >= 55:
		return "severe gusts"
	case gust.Available && gust.Value >= 40:
		return "strong gusts"
	case kph <= 12:
		return "light wind"
	case kph <= 24:
		return "breezy"
	default:
		return "windy"
	}
}
