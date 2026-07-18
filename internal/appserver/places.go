package appserver

import "strings"

type place struct {
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

var europePlaces = []place{
	{Name: "Amsterdam", Country: "Netherlands", Lat: 52.3676, Lon: 4.9041},
	{Name: "Athens", Country: "Greece", Lat: 37.9838, Lon: 23.7275},
	{Name: "Barcelona", Country: "Spain", Lat: 41.3874, Lon: 2.1686},
	{Name: "Berlin", Country: "Germany", Lat: 52.52, Lon: 13.405},
	{Name: "Bordeaux", Country: "France", Lat: 44.8378, Lon: -0.5792},
	{Name: "Copenhagen", Country: "Denmark", Lat: 55.6761, Lon: 12.5683},
	{Name: "Dubrovnik", Country: "Croatia", Lat: 42.6507, Lon: 18.0944},
	{Name: "Faro", Country: "Portugal", Lat: 37.0194, Lon: -7.9304},
	{Name: "Florence", Country: "Italy", Lat: 43.7696, Lon: 11.2558},
	{Name: "Girona", Country: "Spain", Lat: 41.9794, Lon: 2.8214},
	{Name: "Innsbruck", Country: "Austria", Lat: 47.2692, Lon: 11.4041},
	{Name: "Lisbon", Country: "Portugal", Lat: 38.7223, Lon: -9.1393},
	{Name: "London", Country: "United Kingdom", Lat: 51.5072, Lon: -0.1276},
	{Name: "Málaga", Country: "Spain", Lat: 36.7213, Lon: -4.4214},
	{Name: "Mallorca", Country: "Spain", Lat: 39.6953, Lon: 3.0176},
	{Name: "Marseille", Country: "France", Lat: 43.2965, Lon: 5.3698},
	{Name: "Nice", Country: "France", Lat: 43.7102, Lon: 7.262},
	{Name: "Oslo", Country: "Norway", Lat: 59.9139, Lon: 10.7522},
	{Name: "Palermo", Country: "Italy", Lat: 38.1157, Lon: 13.3615},
	{Name: "Porto", Country: "Portugal", Lat: 41.1579, Lon: -8.6291},
	{Name: "Prague", Country: "Czechia", Lat: 50.0755, Lon: 14.4378},
	{Name: "Rome", Country: "Italy", Lat: 41.9028, Lon: 12.4964},
	{Name: "Seville", Country: "Spain", Lat: 37.3891, Lon: -5.9845},
	{Name: "Stockholm", Country: "Sweden", Lat: 59.3293, Lon: 18.0686},
	{Name: "Valencia", Country: "Spain", Lat: 39.4699, Lon: -0.3763},
	{Name: "Vienna", Country: "Austria", Lat: 48.2082, Lon: 16.3738},
	{Name: "Zürich", Country: "Switzerland", Lat: 47.3769, Lon: 8.5417},
}

func searchPlaces(query string, limit int) []place {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	result := make([]place, 0, limit)
	for _, candidate := range europePlaces {
		name := strings.ToLower(candidate.Name)
		country := strings.ToLower(candidate.Country)
		if strings.HasPrefix(name, query) || strings.Contains(name, query) || strings.HasPrefix(country, query) {
			result = append(result, candidate)
			if len(result) == limit {
				break
			}
		}
	}
	return result
}
