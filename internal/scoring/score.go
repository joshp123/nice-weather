package scoring

import (
	"math"

	"nice-weather-finder-poc/internal/weatherinput"
)

type Category struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Color string `json:"color"`
}

type Result struct {
	Score      int        `json:"score"`
	Category   Category   `json:"category"`
	Components Components `json:"components"`
}

var Categories = []Category{
	{Code: "not_nice", Label: "Not Nice", Color: "#9ca3af"},
	{Code: "fair", Label: "Fair", Color: "#7dd3fc"},
	{Code: "nice", Label: "Nice", Color: "#38bdf8"},
	{Code: "beautiful", Label: "Beautiful", Color: "#a3e635"},
	{Code: "perfect", Label: "Perfect", Color: "#16a34a"},
	{Code: "pristine", Label: "Pristine", Color: "#f59e0b"},
}

func Score(input weatherinput.WeatherInput, profile Profile) Result {
	components := ComponentScores(input)
	value := weightedScore(components, ProfileWeights(profile))
	if profile == ProfileCycling {
		value = math.Min(value, weightedScore(components, ProfileWeights(ProfileGeneral)))
		value = math.Min(value, components.Rain)
		value = math.Min(value, components.Wind)
	}
	score := int(math.Round(weatherinput.Clamp(value, 0, 100)))
	return Result{Score: score, Category: CategoryFor(score), Components: components}
}

func weightedScore(components Components, weights Weights) float64 {
	return components.Temp*weights.Temp +
		components.Humidity*weights.Humidity +
		components.Rain*weights.Rain +
		components.Cloud*weights.Cloud +
		components.Wind*weights.Wind
}

func CategoryFor(score int) Category {
	switch {
	case score < 50:
		return Categories[0]
	case score < 62:
		return Categories[1]
	case score < 72:
		return Categories[2]
	case score < 82:
		return Categories[3]
	case score < 92:
		return Categories[4]
	default:
		return Categories[5]
	}
}
