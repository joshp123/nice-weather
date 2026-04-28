package weatherinput

type WeatherInput struct {
	TempC    float64 `json:"tempC"`
	Humidity float64 `json:"humidity"`
	RainMM   float64 `json:"rainMm"`
	CloudPct float64 `json:"cloudPct"`
	WindKMH  float64 `json:"windKmh"`
}
