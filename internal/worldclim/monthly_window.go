package worldclim

import (
	"math"

	"nice-weather-finder/internal/weatherinput"
)

type MonthlyFields struct {
	TAvgC       float64
	PrecMM      float64
	SRadKJM2Day float64
	WindMS      float64
	VaprKPA     float64
	Latitude    float64
	Month       int
}

func MapWorldClimToWeatherInput(fields MonthlyFields) weatherinput.WeatherInput {
	days := daysInMonth(fields.Month)
	temp := fields.TAvgC
	return weatherinput.WeatherInput{
		TempC:    temp,
		Humidity: weatherinput.Clamp(100*fields.VaprKPA/saturationVaporPressure(temp), 0, 100),
		RainMM:   fields.PrecMM / float64(days),
		CloudPct: cloudProxy(fields.SRadKJM2Day, fields.Latitude),
		WindKMH:  fields.WindMS * 3.6,
	}
}

func saturationVaporPressure(tempC float64) float64 {
	return 0.6108 * math.Exp((17.27*tempC)/(tempC+237.3))
}

func cloudProxy(sradKJM2Day, latitude float64) float64 {
	// Simple approximation of the FAO clear-sky-radiation ratio.
	clearSkyMJ := 22.0 + 6.0*math.Cos(latitude*math.Pi/180)
	if clearSkyMJ <= 0 {
		clearSkyMJ = 22
	}
	ratio := weatherinput.Clamp((sradKJM2Day/1000)/clearSkyMJ, 0.33, 1.0)
	return 100 * (1 - ratio) / (1 - 0.33)
}

func daysInMonth(month int) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		return 28
	default:
		return 31
	}
}
