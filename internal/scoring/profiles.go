package scoring

type Profile string

const (
	ProfileGeneral Profile = "general"
	ProfileCycling Profile = "cycling"
)

type Components struct {
	Temp     float64 `json:"temp"`
	Humidity float64 `json:"humidity"`
	Rain     float64 `json:"rain"`
	Cloud    float64 `json:"cloud"`
	Wind     float64 `json:"wind"`
}

type Weights struct {
	Temp     float64
	Humidity float64
	Rain     float64
	Cloud    float64
	Wind     float64
}

func ProfileWeights(profile Profile) Weights {
	switch profile {
	case ProfileCycling:
		return Weights{Temp: 0.25, Humidity: 0.05, Rain: 0.35, Cloud: 0.10, Wind: 0.25}
	default:
		return Weights{Temp: 0.40, Humidity: 0.05, Rain: 0.25, Cloud: 0.20, Wind: 0.10}
	}
}
