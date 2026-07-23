package weather

import (
	"errors"
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
	if validateForScoring(m) != nil || (profile != ProfileNice && profile != ProfileCycling) {
		return Result{}
	}
	components := sharedComponents(m)
	if profile == ProfileCycling {
		components.Wind = cyclingWindScore(m.WindSpeedKPH, m.GustKPH)
	} else {
		components.Wind = niceWindScore(m.WindSpeedKPH, m.GustKPH)
	}
	return scoreProfile(m, profile, components)
}

// ScoreProfiles evaluates both canonical profiles with one validation and one
// calculation of the shared physical components. It is for bulk products that
// always publish Nice and Cycling together; Score remains the single-profile
// entry point.
func ScoreProfiles(m DailyMetrics) (nice, cycling Result) {
	nice, cycling, _ = ScoreProfilesChecked(m)
	return nice, cycling
}

// ScoreProfilesChecked is the diagnostic form of ScoreProfiles. It preserves
// the same canonical scoring path while returning the validation error that
// made a provider day unavailable. Bulk builders use it so they do not have
// to validate once for diagnostics and then again while scoring.
func ScoreProfilesChecked(m DailyMetrics) (nice, cycling Result, err error) {
	if err := validateForScoring(m); err != nil {
		return Result{}, Result{}, err
	}
	shared := sharedComponents(m)
	niceWind := niceWindScore(m.WindSpeedKPH, m.GustKPH)
	niceComponents := shared
	niceComponents.Wind = niceWind
	cyclingComponents := shared
	cyclingComponents.Wind = cyclingWindScoreWithNice(m.WindSpeedKPH, m.GustKPH, niceWind)
	return scoreProfile(m, ProfileNice, niceComponents), scoreProfile(m, ProfileCycling, cyclingComponents), nil
}

func sharedComponents(m DailyMetrics) Components {
	return Components{
		Thermal: thermalScore(m),
		Dryness: drynessScore(m),
		Sun:     clearSkyScore(m),
	}
}

func validateForScoring(m DailyMetrics) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if !m.CloudCoverPercent.Available {
		return errors.New("cloud cover is required for clear-sky scoring")
	}
	return nil
}

func scoreProfile(m DailyMetrics, profile Profile, components Components) Result {
	var w weights
	if profile == ProfileCycling {
		w = weights{warmth: .35, dryness: .30, sun: .10, wind: .25}
	} else {
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
	// A short downpour and hours of drizzle are different ways to lose a dry
	// day. The better dimension must not average away the worse one.
	return rounded(math.Min(rain, piecewise(m.WetTimeFraction.Value, wetTimeCurve)))
}

func niceWindScore(wind float64, gust OptionalFloat) int {
	sustained := piecewise(wind, niceWindCurve)
	if !gust.Available {
		return rounded(sustained)
	}
	return rounded(math.Min(sustained, piecewise(gust.Value, niceGustCurve)))
}

func cyclingWindScore(wind float64, gust OptionalFloat) int {
	return cyclingWindScoreWithNice(wind, gust, niceWindScore(wind, gust))
}

func cyclingWindScoreWithNice(wind float64, gust OptionalFloat, nice int) int {
	cycling := piecewise(wind, cyclingWindCurve)
	if gust.Available {
		cycling = math.Min(cycling, piecewise(gust.Value, cyclingGustCurve))
	}
	// Cycling is the stricter wind judgement. Keep that relationship explicit
	// even if either policy curve changes: choosing Cycling must never make an
	// identical sustained wind or gust look calmer than Nice.
	return min(rounded(cycling), nice)
}

func clearSkyScore(m DailyMetrics) int {
	return rounded(piecewise(m.CloudCoverPercent.Value, cloudCurve))
}

func thermalScore(m DailyMetrics) int {
	base := piecewise(m.ComfortableTempC, thermalCurve)
	// Dew point measures how much moisture the air contains. It separates a warm
	// Dutch summer day from oppressive tropical humidity without confusing high
	// cool-weather relative humidity with muggy heat. Relative humidity is only a
	// fallback when the provider does not supply dew point.
	heat := 100.0
	if m.DewPointC.Available {
		heat = piecewise(m.DewPointC.Value, dewPointCurve)
	} else if m.RelativeHumidityPercent.Available {
		heat = piecewise(dewPointFromRelativeHumidity(m.ComfortableTempC, m.RelativeHumidityPercent.Value), dewPointCurve)
	}

	// Cold damp misery is a compound condition, not "humidity" by itself.
	// Wetness, grey sky and wind progressively matter below 16°C. Cold and dry
	// is better than cold and damp, but this warm-weather product still treats
	// both as less desirable than a genuinely warm day.
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
	// Warmth is the main purpose of the product, so Overall cannot outrank its
	// visible Comfort component. This keeps the judgement legible instead of
	// applying a second hidden temperature policy after component scoring.
	score = math.Min(score, float64(components.Thermal))
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

const (
	thermalIdealMinimumC = 22
	thermalIdealMaximumC = 27
)

var (
	thermalCurve     = []point{{-15, 0}, {-5, 8}, {0, 20}, {5, 25}, {8, 35}, {12, 45}, {16, 54}, {19, 75}, {thermalIdealMinimumC, 100}, {thermalIdealMaximumC, 100}, {30, 88}, {34, 45}, {38, 8}, {42, 0}}
	rainCurve        = []point{{0, 100}, {.2, 96}, {1, 80}, {3, 48}, {6, 18}, {10, 0}}
	wetTimeCurve     = []point{{0, 100}, {.1, 92}, {.25, 70}, {.5, 30}, {.8, 0}}
	cloudCurve       = []point{{0, 100}, {25, 90}, {50, 60}, {75, 25}, {100, 0}}
	dewPointCurve    = []point{{16, 100}, {18, 85}, {21, 55}, {24, 0}}
	niceWindCurve    = []point{{0, 100}, {12, 100}, {20, 82}, {30, 48}, {45, 0}}
	niceGustCurve    = []point{{0, 100}, {20, 100}, {35, 75}, {50, 35}, {65, 0}}
	cyclingWindCurve = []point{{0, 100}, {14, 100}, {20, 78}, {28, 38}, {35, 10}, {42, 0}}
	cyclingGustCurve = []point{{0, 100}, {20, 100}, {30, 70}, {40, 30}, {50, 0}}
)

// dewPointFromRelativeHumidity normalises fallback moisture evidence into the
// same physical quantity and policy used when a provider supplies dew point.
// Magnus constants are appropriate for ordinary near-surface temperatures.
func dewPointFromRelativeHumidity(temperatureC, relativeHumidityPercent float64) float64 {
	if relativeHumidityPercent <= 0 {
		return -100
	}
	const a, b = 17.625, 243.04
	gamma := math.Log(relativeHumidityPercent/100) + a*temperatureC/(b+temperatureC)
	return b * gamma / (a - gamma)
}

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

// Summary describes observed conditions and names severe thermal discomfort
// when it is itself a Poor or Awful component. Invalid data has one clear
// fallback.
func Summary(m DailyMetrics) string {
	code, err := EncodeSummary(m)
	if err != nil {
		return "No forecast data"
	}
	parts := SummaryParts(code)
	parts = append([]string{temperaturePhrase(m.ComfortableTempC)}, parts...)
	return strings.Join(parts, " · ")
}

// EncodeSummary freezes the thermal notice and three descriptive phrase
// choices beside the Go scoring path. Point assets carry this byte so the
// browser only maps codes to copy; it does not re-evaluate weather thresholds.
// The mixed-radix layout fits 3*4*4*5 states in one byte and leaves 240..255
// invalid. Thermal-neutral codes 0..79 retain their previous representation.
func EncodeSummary(m DailyMetrics) (uint8, error) {
	if err := validateForScoring(m); err != nil {
		return 0, err
	}
	rain := map[string]uint8{"dry": 0, "brief rain": 1, "some rain": 2, "wet": 3}[rainPhrase(m)]
	sun := map[string]uint8{"overcast": 0, "mixed cloud": 1, "bright": 2, "sunny": 3}[clearSkyPhrase(m)]
	wind, ok := map[string]uint8{"light wind": 0, "breezy": 1, "windy": 2, "strong gusts": 3, "severe gusts": 4}[windPhrase(m.WindSpeedKPH, m.GustKPH)]
	if !ok {
		return 0, errors.New("weather summary phrase is not encodable")
	}
	thermal := map[string]uint8{"": 0, "too hot": 1, "too cold": 2}[thermalNotice(m)]
	return rain + 4*sun + 16*wind + 80*thermal, nil
}

func SummaryParts(code uint8) []string {
	if code >= 240 {
		return nil
	}
	thermal, remainder := code/80, code%80
	wind, remainder := remainder/16, remainder%16
	sun, rain := remainder/4, remainder%4
	parts := make([]string, 0, 4)
	if thermal > 0 {
		parts = append(parts, []string{"", "too hot", "too cold"}[thermal])
	}
	return append(parts,
		[]string{"dry", "brief rain", "some rain", "wet"}[rain],
		[]string{"overcast", "mixed cloud", "bright", "sunny"}[sun],
		[]string{"light wind", "breezy", "windy", "strong gusts", "severe gusts"}[wind])
}

func thermalNotice(m DailyMetrics) string {
	rating := Rating(thermalScore(m))
	if rating != "Poor" && rating != "Awful" {
		return ""
	}
	switch {
	case m.ComfortableTempC > thermalIdealMaximumC:
		return "too hot"
	case m.ComfortableTempC < thermalIdealMinimumC:
		return "too cold"
	default:
		return ""
	}
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

func clearSkyPhrase(m DailyMetrics) string {
	switch {
	case m.CloudCoverPercent.Value < 25:
		return "sunny"
	case m.CloudCoverPercent.Value < 55:
		return "bright"
	case m.CloudCoverPercent.Value < 80:
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
