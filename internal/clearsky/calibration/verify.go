// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
package main

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"nice-weather-finder/internal/clearsky"
)

type sample struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Ending    time.Time `json:"ending"`
}
type result struct {
	Global float64 `json:"global"`
	Normal float64 `json:"normal"`
	DNI    float64 `json:"dni"`
}

func main() {
	var inputs []sample
	if err := json.NewDecoder(os.Stdin).Decode(&inputs); err != nil {
		log.Fatal(err)
	}
	outputs := make([]result, len(inputs))
	for i, input := range inputs {
		model, err := clearsky.New(input.Latitude, input.Longitude, 0)
		if err != nil {
			log.Fatal(err)
		}
		global, normal := model.ForecastRadiation(clearsky.PrepareForecastHour(input.Ending))
		outputs[i] = result{Global: global, Normal: normal, DNI: clearsky.EstimateForecastDNI(.7*global, global, normal)}
	}
	if err := json.NewEncoder(os.Stdout).Encode(outputs); err != nil {
		log.Fatal(err)
	}
}
