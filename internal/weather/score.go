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
	Warmth  int
	Dryness int
	Sun     int
	Wind    int
	Comfort int
}

// Result is the complete product judgement for one day and place.
type Result struct {
	Available  bool
	Score      int
	Rating     string
	Components Components
}

type weights struct {
	warmth, dryness, sun, wind, comfort float64
}

// Score applies the single scoring path used by both map rendering and point
// details. Invalid data returns an unavailable result instead of a plausible
// looking zero.
func Score(m DailyMetrics, profile Profile) Result {
	if m.Validate() != nil || (profile != ProfileNice && profile != ProfileCycling) {
		return Result{}
	}

	components := Components{
		Warmth:  rounded(piecewise(m.ComfortableTempC, warmthCurve)),
		Dryness: drynessScore(m),
		Sun:     sunScore(m),
		Comfort: comfortScore(m),
	}

	var w weights
	if profile == ProfileCycling {
		components.Wind = cyclingWindScore(m.WindSpeedKPH, m.GustKPH)
		w = weights{warmth: .25, dryness: .30, sun: .10, wind: .25, comfort: .10}
	} else {
		components.Wind = niceWindScore(m.WindSpeedKPH, m.GustKPH)
		w = weights{warmth: .35, dryness: .25, sun: .20, wind: .10, comfort: .10}
	}

	value := float64(components.Warmth)*w.warmth + float64(components.Dryness)*w.dryness +
		float64(components.Sun)*w.sun + float64(components.Wind)*w.wind + float64(components.Comfort)*w.comfort
	value = applyDealbreakers(value, m, profile)
	score := rounded(value)
	return Result{
		Available:  true,
		Score:      score,
		Rating:     rating(score),
		Components: components,
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

func comfortScore(m DailyMetrics) int {
	score, weight := 0.0, 0.0
	if m.DewPointC.Available {
		score += .8 * piecewise(m.DewPointC.Value, dewPointCurve)
		weight += .8
	}
	if m.RelativeHumidityPercent.Available {
		score += .2 * piecewise(m.RelativeHumidityPercent.Value, humidityCurve)
		weight += .2
	}
	return rounded(score / weight)
}

func applyDealbreakers(score float64, m DailyMetrics, profile Profile) float64 {
	// Warmth is the main purpose of the product. A flawless but cold day must
	// not outrank a genuinely warm one.
	score = math.Min(score, piecewise(m.ComfortableTempC, temperatureCeilingCurve))
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
	warmthCurve             = []point{{8, 0}, {12, 18}, {16, 45}, {19, 72}, {22, 100}, {27, 100}, {30, 78}, {34, 35}, {38, 0}}
	temperatureCeilingCurve = []point{{8, 15}, {12, 30}, {16, 54}, {19, 75}, {22, 100}, {29, 100}, {32, 75}, {35, 45}, {38, 20}}
	rainCurve               = []point{{0, 100}, {.2, 96}, {1, 80}, {3, 48}, {6, 18}, {10, 0}}
	wetTimeCurve            = []point{{0, 100}, {.1, 92}, {.25, 70}, {.5, 30}, {.8, 0}}
	solarCurve              = []point{{0, 0}, {4, 20}, {8, 45}, {12, 70}, {18, 100}}
	cloudCurve              = []point{{0, 100}, {25, 90}, {50, 60}, {75, 25}, {100, 0}}
	dewPointCurve           = []point{{8, 100}, {12, 100}, {16, 84}, {18, 64}, {21, 30}, {24, 0}}
	humidityCurve           = []point{{50, 100}, {70, 100}, {80, 80}, {90, 50}, {100, 20}}
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

func rating(score int) string {
	switch {
	case score >= 85:
		return "Excellent"
	case score >= 70:
		return "Very good"
	case score >= 55:
		return "Good"
	case score >= 40:
		return "Mixed"
	default:
		return "Poor"
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
