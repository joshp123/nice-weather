package destinations

import (
	"fmt"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/weather"
)

func TestEvaluateUsesEveryPlaceAndReturnsCanonicalDiverseWeather(t *testing.T) {
	placesSeen := 0
	candidates := []places.Candidate{
		place(1, "Coastal city", 36.0, -5.0, 300_000, 5),
		place(2, "Nearby village", 36.1, -5.1, 800, 1),
		place(3, "Western city", 38.7, -9.1, 500_000, 5),
		place(4, "Island city", 28.1, -15.4, 380_000, 4),
		place(5, "Eastern city", 37.9, 23.7, 660_000, 5),
		place(6, "Southern city", 35.9, 14.5, 220_000, 4),
		place(7, "Atlantic city", 32.7, -16.9, 110_000, 4),
		place(8, "Cold city", 60.2, 24.9, 650_000, 5),
	}
	scores := map[int64]int{1: 98, 2: 98, 3: 96, 4: 95, 5: 94, 6: 93, 7: 92, 8: 30}
	result, err := Evaluate(func(visit func(places.Candidate) error) error {
		for _, item := range candidates {
			placesSeen++
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}, func(profile string, longitude, latitude float64) (delivery.RangeRecord, bool, error) {
		var id int64
		for _, item := range candidates {
			if item.Longitude == longitude && item.Latitude == latitude {
				id = item.GeoNameID
			}
		}
		score := scores[id]
		if profile == delivery.ProfileCycling {
			score -= 5
		}
		return rangeRecord(t, score), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if placesSeen != len(candidates) {
		t.Fatalf("walked %d places; want %d", placesSeen, len(candidates))
	}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		got := result[profile]
		if len(got) != 6 {
			t.Fatalf("%s returned %d results; want 6", profile, len(got))
		}
		if got[0].Name != "Coastal city" {
			t.Fatalf("%s first result = %q", profile, got[0].Name)
		}
		if got[0].Score != scores[1]-map[string]int{delivery.ProfileNice: 0, delivery.ProfileCycling: 5}[profile] {
			t.Fatalf("%s first score = %d", profile, got[0].Score)
		}
		for _, item := range got {
			if item.Name == "Nearby village" {
				t.Fatalf("%s retained a nearby duplicate", profile)
			}
		}
		if got[0].NiceDays != 14 {
			t.Fatalf("%s lost canonical evidence: %+v", profile, got[0])
		}
	}
}

func TestEvaluateFailsClosed(t *testing.T) {
	if _, err := Evaluate(func(func(places.Candidate) error) error { return fmt.Errorf("bad index") }, func(string, float64, float64) (delivery.RangeRecord, bool, error) {
		return delivery.RangeRecord{}, false, nil
	}); err == nil {
		t.Fatal("invalid place walk was accepted")
	}
	if _, err := Evaluate(func(visit func(places.Candidate) error) error { return visit(place(1, "Nowhere", 40, 0, 1, 1)) }, func(string, float64, float64) (delivery.RangeRecord, bool, error) {
		return delivery.RangeRecord{}, false, nil
	}); err == nil {
		t.Fatal("empty weather coverage was accepted")
	}
	five := []places.Candidate{
		place(1, "One", 36, -10, 10_000, 1), place(2, "Two", 37, -5, 10_000, 1),
		place(3, "Three", 38, 0, 10_000, 1), place(4, "Four", 39, 5, 10_000, 1),
		place(5, "Five", 40, 10, 10_000, 1),
	}
	if _, err := Evaluate(func(visit func(places.Candidate) error) error {
		for _, item := range five {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}, func(string, float64, float64) (delivery.RangeRecord, bool, error) {
		return rangeRecord(t, 90), true, nil
	}); err == nil {
		t.Fatal("shortlist smaller than the six-result browser contract was accepted")
	}
}

func place(id int64, name string, lat, lon float64, population int64, rank uint8) places.Candidate {
	return places.Candidate{GeoNameID: id, Name: name, Country: "Example", CountryCode: "EX", Latitude: lat, Longitude: lon, Population: population, FeatureRank: rank}
}

func rangeRecord(t *testing.T, score int) delivery.RangeRecord {
	t.Helper()
	summary := weather.RangeSummary{
		Days: 14, Score: score, Rating: weather.Rating(score), MedianScore: score,
		NiceDays: 14, Components: weather.Components{Thermal: score, Dryness: score, Sun: score, Wind: score},
	}
	record, err := delivery.RangeRecordFromSummary(summary)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
