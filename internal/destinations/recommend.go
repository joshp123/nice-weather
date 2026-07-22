// Package destinations turns canonical forecast-window judgements and the
// complete place index into a small, geographically useful shortlist.
package destinations

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/places"
)

const (
	resultCount        = 6
	bucketDegrees      = 0.5
	primarySpacingKM   = 400.0
	secondarySpacingKM = 250.0
	worldSpacingKM     = 1_500.0
	worldFallbackKM    = 900.0
)

type Lookup func(profile string, longitude, latitude float64) (delivery.RangeRecord, bool, error)
type Walk func(func(places.Candidate) error) error

// Result carries only server-evaluated weather judgement. The browser uses it
// to choose a map point; it never reweights or rescales these values.
type Result struct {
	GeoNameID   int64   `json:"geonameId"`
	Name        string  `json:"name"`
	Admin1      string  `json:"admin1,omitempty"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Score       int     `json:"score"`
	NiceDays    int     `json:"niceDays"`
}

type candidate struct {
	result                                 Result
	featureRank                            uint8
	population                             int64
	awfulDays, longestPoorRun, medianScore int
	comfort, dryness, brightness, calm     int
}

type bucket struct{ latitude, longitude int }

// Evaluate visits the full place population once, applies the canonical range
// record at every settlement, then returns a small spatially diverse shortlist
// for each profile. The 0.5-degree buckets only remove near-duplicates; they do
// not alter or smooth any weather score.
func Evaluate(walk Walk, lookup Lookup) (map[string][]Result, error) {
	return evaluate(walk, lookup, []float64{primarySpacingKM, secondarySpacingKM, 0})
}

// EvaluateWorldwide applies the same canonical evidence and place-quality
// rules as Evaluate, but requires useful global separation before relaxing to
// the established local deduplication distance. It has no country or city
// allow-list: every settlement in the validated place release competes.
func EvaluateWorldwide(walk Walk, lookup Lookup) (map[string][]Result, error) {
	return evaluate(walk, lookup, []float64{worldSpacingKM, worldFallbackKM, primarySpacingKM, 0})
}

func evaluate(walk Walk, lookup Lookup, spacings []float64) (map[string][]Result, error) {
	if walk == nil || lookup == nil {
		return nil, errors.New("destination walk and lookup are required")
	}
	if len(spacings) == 0 || spacings[len(spacings)-1] != 0 {
		return nil, errors.New("destination spacing policy must end with an unspaced fallback")
	}
	profiles := []string{delivery.ProfileNice, delivery.ProfileCycling}
	best := map[string]map[bucket]candidate{
		delivery.ProfileNice: {}, delivery.ProfileCycling: {},
	}
	if err := walk(func(place places.Candidate) error {
		// Recommendations are viable bases for a traveller, not a contest to find
		// the warmest named settlement. Keep population centres and national
		// capitals; the complete place search still accepts smaller destinations.
		if place.Population < 50_000 && place.FeatureRank < 5 {
			return nil
		}
		key := bucket{
			latitude:  int(math.Floor((place.Latitude + 90) / bucketDegrees)),
			longitude: int(math.Floor((place.Longitude + 180) / bucketDegrees)),
		}
		for _, profile := range profiles {
			record, valid, err := lookup(profile, place.Longitude, place.Latitude)
			if err != nil {
				return fmt.Errorf("lookup %s weather for place %d: %w", profile, place.GeoNameID, err)
			}
			if !valid {
				continue
			}
			summary, err := record.Summary()
			if err != nil {
				return fmt.Errorf("decode %s weather for place %d: %w", profile, place.GeoNameID, err)
			}
			next := candidate{featureRank: place.FeatureRank, population: place.Population,
				awfulDays: summary.AwfulDays, longestPoorRun: summary.LongestPoorRun, medianScore: summary.MedianScore,
				comfort: summary.Components.Thermal, dryness: summary.Components.Dryness,
				brightness: summary.Components.Sun, calm: summary.Components.Wind,
				result: Result{
					GeoNameID: place.GeoNameID, Name: place.Name, Admin1: place.Admin1,
					Country: place.Country, CountryCode: place.CountryCode,
					Latitude: place.Latitude, Longitude: place.Longitude,
					Score: summary.Score, NiceDays: summary.NiceDays,
				}}
			if current, exists := best[profile][key]; !exists || better(next, current) {
				best[profile][key] = next
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	result := make(map[string][]Result, len(profiles))
	for _, profile := range profiles {
		ordered := make([]candidate, 0, len(best[profile]))
		for _, item := range best[profile] {
			ordered = append(ordered, item)
		}
		slices.SortFunc(ordered, func(left, right candidate) int {
			if better(left, right) {
				return -1
			}
			if better(right, left) {
				return 1
			}
			return 0
		})
		var selected []Result
		for _, spacing := range spacings {
			if len(selected) == resultCount {
				break
			}
			selected = selectSpaced(ordered, spacing, selected)
		}
		if len(selected) != resultCount {
			return nil, fmt.Errorf("only %d %s destinations have a complete forecast window; need %d", len(selected), profile, resultCount)
		}
		result[profile] = orderSelected(ordered, selected)
	}
	return result, nil
}

func orderSelected(ordered []candidate, selected []Result) []Result {
	wanted := make(map[int64]bool, len(selected))
	for _, item := range selected {
		wanted[item.GeoNameID] = true
	}
	result := make([]Result, 0, len(selected))
	for _, item := range ordered {
		if wanted[item.result.GeoNameID] {
			result = append(result, item.result)
		}
	}
	return result
}

func selectSpaced(ordered []candidate, minimumKM float64, selected []Result) []Result {
	seen := make(map[int64]bool, len(selected))
	for _, item := range selected {
		seen[item.GeoNameID] = true
	}
	for _, item := range ordered {
		if len(selected) == resultCount {
			break
		}
		if seen[item.result.GeoNameID] || !farEnough(item.result, selected, minimumKM) {
			continue
		}
		selected = append(selected, item.result)
		seen[item.result.GeoNameID] = true
	}
	return selected
}

func farEnough(next Result, selected []Result, minimumKM float64) bool {
	for _, current := range selected {
		if greatCircleKM(next.Latitude, next.Longitude, current.Latitude, current.Longitude) < minimumKM {
			return false
		}
	}
	return true
}

func greatCircleKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0088
	toRadians := math.Pi / 180
	latitudeDelta := (lat2 - lat1) * toRadians
	longitudeDelta := (lon2 - lon1) * toRadians
	a := math.Sin(latitudeDelta/2)*math.Sin(latitudeDelta/2) +
		math.Cos(lat1*toRadians)*math.Cos(lat2*toRadians)*math.Sin(longitudeDelta/2)*math.Sin(longitudeDelta/2)
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(a))
}

func better(left, right candidate) bool {
	for _, comparison := range []struct{ left, right int }{
		{left.result.Score, right.result.Score},
		{left.result.NiceDays, right.result.NiceDays},
		{-left.awfulDays, -right.awfulDays},
		{-left.longestPoorRun, -right.longestPoorRun},
		{left.medianScore, right.medianScore},
		{left.comfort, right.comfort},
		{left.brightness, right.brightness},
		{left.dryness, right.dryness},
		{left.calm, right.calm},
		{int(left.featureRank), int(right.featureRank)},
	} {
		if comparison.left != comparison.right {
			return comparison.left > comparison.right
		}
	}
	if left.population != right.population {
		return left.population > right.population
	}
	if left.result.Name != right.result.Name {
		return left.result.Name < right.result.Name
	}
	return left.result.GeoNameID < right.result.GeoNameID
}
