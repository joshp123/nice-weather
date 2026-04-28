package weatherinput

func Clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func DaytimeForecastAggregate(samples []WeatherInput) WeatherInput {
	if len(samples) == 0 {
		return WeatherInput{}
	}
	var total WeatherInput
	for _, sample := range samples {
		total.TempC += sample.TempC
		total.Humidity += sample.Humidity
		total.RainMM += sample.RainMM
		total.CloudPct += sample.CloudPct
		total.WindKMH += sample.WindKMH
	}
	count := float64(len(samples))
	rainMM := samples[len(samples)-1].RainMM - samples[0].RainMM
	if rainMM < 0 {
		rainMM = 0
	}
	return WeatherInput{
		TempC:    total.TempC / count,
		Humidity: total.Humidity / count,
		RainMM:   rainMM,
		CloudPct: total.CloudPct / count,
		WindKMH:  total.WindKMH / count,
	}
}
