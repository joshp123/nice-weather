package dwd

import (
	"math"

	"nice-weather-finder-poc/internal/weatherinput"
)

type IconEUFields struct {
	T2MKelvin float64
	RelHum2M  float64
	TotPrecMM float64
	CloudPct  float64
	U10MMS    float64
	V10MMS    float64
}

func MapICONToWeatherInput(fields IconEUFields) weatherinput.WeatherInput {
	return weatherinput.WeatherInput{
		TempC:    fields.T2MKelvin - 273.15,
		Humidity: weatherinput.Clamp(fields.RelHum2M, 0, 100),
		RainMM:   fields.TotPrecMM,
		CloudPct: weatherinput.Clamp(fields.CloudPct, 0, 100),
		WindKMH:  math.Hypot(fields.U10MMS, fields.V10MMS) * 3.6,
	}
}
