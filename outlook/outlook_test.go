package outlook

import (
	"bytes"
	"image"
	"testing"
)

func TestCoastalPointAndSeamUseWorkersRowMajorRule(t *testing.T) {
	scores := &image.Gray{Pix: bytes.Repeat([]byte{255}, gridWidth*gridHeight), Stride: gridWidth, Rect: image.Rect(0, 0, gridWidth, gridHeight)}
	// The exact point is sea. Equally close land points select the first row.
	scores.Pix[199*gridWidth+0] = 85
	scores.Pix[201*gridWidth+0] = 70
	if got := nearestLandPoint(40, 180, scores); got != 199*gridWidth {
		t.Fatalf("selected point %d, want first equally near land point across the seam", got)
	}
	if got := nearestLandPoint(-61, 0, scores); got != -1 {
		t.Fatal("selected land outside the app's supported latitude")
	}
}

func TestRankingSeparatesPlacesAndOmitsPalestineHeadlines(t *testing.T) {
	scores := &image.Gray{Pix: bytes.Repeat([]byte{99}, gridWidth*gridHeight), Stride: gridWidth, Rect: image.Rect(0, 0, gridWidth, gridHeight)}
	data := []byte("Rome\tRome\tLazio, Italy\t41.9\t12.5\t2700000\nNear Rome\tNear Rome\tLazio, Italy\t42\t12.5\t200000\nMadrid\tMadrid\tMadrid, Spain\t40.4\t-3.7\t3300000\nAthens\tAthens\tAttica, Greece\t38\t23.7\t600000\nAl-Quds\tAl-Quds\tPalestine\t31.8\t35.2\t99999999\nYafa\tYafa\tPalestine\t32.1\t34.8\t99999999\n")
	places, err := readPlaces(data, scores)
	if err != nil {
		t.Fatal(err)
	}
	chosen := rankPlaces(places, "europe")
	if len(chosen) != 3 || chosen[0].Name != "Madrid" || chosen[1].Name != "Rome" || chosen[2].Name != "Athens" {
		t.Fatalf("unexpected headlines: %+v", chosen)
	}
	if len(places) != 4 {
		t.Fatal("Palestine places were not suppressed")
	}
}

func TestEuropeanHeadlinesIncludeIslandsButExcludeAsiaAndNorthAfrica(t *testing.T) {
	for _, example := range []struct {
		name, country       string
		latitude, longitude float64
		included            bool
	}{
		{"Las Palmas", "Spain", 28.12, -15.43, true},
		{"Funchal", "Portugal", 32.65, -16.91, true},
		{"Ponta Delgada", "Portugal", 37.74, -25.67, true},
		{"Valletta", "Malta", 35.90, 14.51, true},
		{"Nicosia", "Cyprus", 35.18, 33.36, true},
		{"Derbent", "Russia", 42.07, 48.29, false},
		{"Kazan", "Russia", 55.79, 49.12, false},
		{"Samara", "Russia", 53.2, 50.15, false},
		{"Novosibirsk", "Russia", 55.03, 82.92, false},
		{"Edirne", "Turkey", 41.68, 26.56, true},
		{"Istanbul", "Turkey", 41.01, 28.95, true},
		{"Gelibolu", "Turkey", 40.41, 26.67, true},
		{"Üsküdar", "Turkey", 41.02274, 29.01366, false},
		{"Çengelköy", "Turkey", 41.05062, 29.0551, false},
		{"Yeniköy", "Turkey", 41.12171, 29.06993, true},
		{"Cizre", "Turkey", 37.33, 42.19, false},
		{"Akçakale", "Turkey", 36.71, 38.95, false},
		{"Bursa", "Turkey", 40.20, 29.06, false},
		{"Çanakkale", "Turkey", 40.15, 26.41, false},
		{"Ceuta", "Spain", 35.89, -5.32, false},
		{"Melilla", "Spain", 35.29, -2.94, false},
		{"Casablanca", "Morocco", 33.59, -7.62, false},
		{"Beirut", "Lebanon", 33.89, 35.50, false},
	} {
		t.Run(example.name, func(t *testing.T) {
			place := Place{Region: example.country, Latitude: example.latitude, Longitude: example.longitude}
			if inRegion(place, "europe") != example.included {
				t.Fatalf("wrong European eligibility for %s", example.name)
			}
		})
	}
}

func TestWorldHeadlinesPreferLargerCitiesOnTiedScores(t *testing.T) {
	places := []Place{
		{Name: "Small perfect town", Population: 290000, Score: 100, Longitude: 80},
		{Name: "Third", Population: 300000, Score: 99, Longitude: 40},
		{Name: "Second", Population: 500000, Score: 99, Longitude: 20},
		{Name: "Nearby", Population: 900000, Score: 99, Longitude: 1},
		{Name: "First", Population: 1000000, Score: 99, Longitude: 0},
	}
	chosen := rankPlaces(places, "world")
	if len(chosen) != 3 || chosen[0].Name != "First" || chosen[1].Name != "Second" || chosen[2].Name != "Third" {
		t.Fatalf("unexpected world headlines: %+v", chosen)
	}
}

func TestWinningSuburbUsesNearbyCityWithSimilarWeather(t *testing.T) {
	winner := Place{Name: "Pomigliano d'Arco", Population: 38921, Latitude: 40.91, Longitude: 14.39, Score: 98}
	naples := Place{Name: "Naples", Population: 900000, Latitude: 40.85, Longitude: 14.27, Score: 93}
	candidates := []Place{
		winner, naples,
		{Name: "Too cold", Population: 2000000, Latitude: 40.85, Longitude: 14.27, Score: 92},
		{Name: "Too far", Population: 3000000, Latitude: 42, Longitude: 14.27, Score: 98},
	}
	for index := range candidates {
		candidates[index].FloatScore = float64(candidates[index].Score)
	}
	winner.FloatScore = float64(winner.Score)
	if got := largestNearbyPlace(winner, candidates); got.Name != "Naples" || got.Score != 93 {
		t.Fatalf("unexpected nearby name or city score: %+v", got)
	}
}

func TestNearbyNamesKeepBothCitiesAndWinningSpotsSeparated(t *testing.T) {
	places := []Place{
		{Name: "First winner", Population: 10000, Score: 100},
		{Name: "First city", Population: 100000, Score: 96, Longitude: .4},
		{Name: "Second winner", Population: 10000, Score: 99, Longitude: 2.3},
		{Name: "Second city", Population: 100000, Score: 95, Longitude: 1.9},
		{Name: "Third", Population: 10000, Score: 98, Longitude: 10},
		{Name: "Fourth", Population: 10000, Score: 97, Longitude: 20},
	}
	// Synthetic positions are on the equator: use World-sized populations.
	for index := range places {
		places[index].Population *= 100
		places[index].FloatScore = float64(places[index].Score)
		places[index].NiceDayCount = float64(places[index].Score)
	}
	chosen := rankPlaces(places, "world")
	if len(chosen) != 3 || chosen[0].Name != "Third" || chosen[1].Name != "Fourth" || chosen[2].Name != "First city" {
		t.Fatalf("nearby city names lost separation: %+v", chosen)
	}
}

func TestRankingUsesWholeNiceDaysThenPerfectDaysThenPopulation(t *testing.T) {
	places := []Place{
		{Name: "More perfect days", Slug: "perfect", Population: 500000, Longitude: 20, NiceDayCount: 1, PerfectDayCount: 1, Score: 80, FloatScore: 80},
		{Name: "Larger city", Slug: "large", Population: 1000000, Longitude: 40, NiceDayCount: .9999, PerfectDayCount: 0, Score: 100, FloatScore: 100},
		{Name: "Smaller city", Slug: "small", Population: 300000, Longitude: 60, NiceDayCount: 1, PerfectDayCount: 0, Score: 99, FloatScore: 99},
		{Name: "More nice days", Slug: "nice", Population: 300000, Longitude: 80, NiceDayCount: 2, PerfectDayCount: 0, Score: 70, FloatScore: 70},
	}
	chosen := rankPlaces(places, "world")
	if len(chosen) != 3 || chosen[0].Name != "More nice days" || chosen[1].Name != "More perfect days" || chosen[2].Name != "Larger city" {
		t.Fatalf("wrong count ranking: %+v", chosen)
	}
	if populationThreshold("europe") != 50000 || populationThreshold("usa") != 50000 {
		t.Fatal("continent population floor differs")
	}
}
