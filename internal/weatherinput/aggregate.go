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

func DailyAggregate(samples []WeatherInput) WeatherInput {
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
	return WeatherInput{
		TempC:    total.TempC / count,
		Humidity: total.Humidity / count,
		RainMM:   total.RainMM,
		CloudPct: total.CloudPct / count,
		WindKMH:  total.WindKMH / count,
	}
}
