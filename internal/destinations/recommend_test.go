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

func TestEvaluateWorldwideExcludesTinySettlementsWithoutAPlaceAllowList(t *testing.T) {
	candidates := []places.Candidate{
		place(1, "Tiny settlement", 0, 0, 8_000, 4),
		place(2, "Small national capital", 10, 10, 8_000, 5),
	}
	for index := 0; index < resultCount-1; index++ {
		population := int64(100_000)
		if index == 0 {
			population = 50_000
		}
		candidates = append(candidates, place(int64(index+3), fmt.Sprintf("City %d", index+1), float64(-50+index*20), float64(-100+index*35), population, 2))
	}
	result, err := EvaluateWorldwide(func(visit func(places.Candidate) error) error {
		for _, item := range candidates {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}, func(_ string, longitude, _ float64) (delivery.RangeRecord, bool, error) {
		if longitude == 0 {
			return rangeRecord(t, 100), true, nil
		}
		return rangeRecord(t, 90), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		for _, item := range result[profile] {
			if item.GeoNameID == 1 {
				t.Fatalf("%s recommended a tiny settlement: %+v", profile, result[profile])
			}
		}
		if result[profile][0].GeoNameID != 2 {
			t.Fatalf("%s excluded a national capital: %+v", profile, result[profile])
		}
	}
}

func TestEvaluateWorldwidePrefersAUsefulGlobalSpreadWithoutCuratingPlaces(t *testing.T) {
	candidates := []places.Candidate{
		place(1, "Iberian coast", 36.7, -4.4, 600_000, 5),
		place(2, "Nearby Iberian coast", 37.0, -4.2, 900_000, 5),
		place(3, "Eastern Mediterranean", 35.1, 33.4, 350_000, 5),
		place(4, "North American coast", 34.0, -118.2, 3_800_000, 5),
		place(5, "Southern Africa", -33.9, 18.4, 4_600_000, 5),
		place(6, "South America", -23.5, -46.6, 12_000_000, 5),
		place(7, "East Asia", 35.7, 139.7, 14_000_000, 5),
		place(8, "Oceania", -33.9, 151.2, 5_300_000, 5),
	}
	scores := map[int64]int{1: 99, 2: 100, 3: 98, 4: 97, 5: 96, 6: 95, 7: 94, 8: 93}
	result, err := EvaluateWorldwide(func(visit func(places.Candidate) error) error {
		for _, item := range candidates {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}, func(profile string, longitude, latitude float64) (delivery.RangeRecord, bool, error) {
		for _, item := range candidates {
			if item.Longitude == longitude && item.Latitude == latitude {
				return rangeRecord(t, scores[item.GeoNameID]), true, nil
			}
		}
		return delivery.RangeRecord{}, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		got := result[profile]
		if len(got) != resultCount {
			t.Fatalf("%s returned %d worldwide results", profile, len(got))
		}
		for _, item := range got {
			if item.GeoNameID == 1 {
				t.Fatalf("%s retained the lower-scoring near-duplicate: %+v", profile, got)
			}
		}
		if got[0].GeoNameID != 2 {
			t.Fatalf("%s lost the genuinely best European result: %+v", profile, got)
		}
		for left := range got {
			for right := left + 1; right < len(got); right++ {
				if distance := greatCircleKM(got[left].Latitude, got[left].Longitude, got[right].Latitude, got[right].Longitude); distance < worldSpacingKM {
					t.Fatalf("%s results %q and %q are only %.0f km apart", profile, got[left].Name, got[right].Name, distance)
				}
			}
		}
	}
}

func TestEvaluateWorldwideRelaxesSpacingOnlyEnoughToFillTheAnswer(t *testing.T) {
	candidates := make([]places.Candidate, resultCount)
	for index := range candidates {
		candidates[index] = place(int64(index+1), fmt.Sprintf("Position %d", index+1), 0, float64(index*9), 100_000, 4)
	}
	result, err := EvaluateWorldwide(func(visit func(places.Candidate) error) error {
		for _, item := range candidates {
			if err := visit(item); err != nil {
				return err
			}
		}
		return nil
	}, func(_ string, longitude, _ float64) (delivery.RangeRecord, bool, error) {
		return rangeRecord(t, 95-int(longitude/9)), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{delivery.ProfileNice, delivery.ProfileCycling} {
		if len(result[profile]) != resultCount {
			t.Fatalf("%s fallback returned %d results", profile, len(result[profile]))
		}
		for index, item := range result[profile] {
			if item.GeoNameID != int64(index+1) {
				t.Fatalf("%s fallback changed canonical ranking: %+v", profile, result[profile])
			}
		}
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
