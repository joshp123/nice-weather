// Package outlook makes daily, ready-to-post maps from the published default score grid.
package outlook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

const gridWidth, gridHeight = 1440, 721
const minimumPopulation = 10000
const minimumSeparationKM = 250
const nearbyNameRadiusKM = 50
const nearbyNameScoreDifference = 5

type Pointer struct {
	Run       string `json:"run"`
	FirstDate string `json:"firstDate"`
	Days      int    `json:"days"`
}

type Place struct {
	Name       string  `json:"name"`
	Region     string  `json:"region"`
	Slug       string  `json:"slug"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Score      int     `json:"score"`
	PointIndex int     `json:"pointIndex"`
	Population int     `json:"population"`
}

type Outlook struct {
	MinimumPopulation int     `json:"minimumPopulation"`
	Region            string  `json:"region"`
	Title             string  `json:"title"`
	Date              string  `json:"date"`
	Sentence          string  `json:"sentence"`
	Run               string  `json:"run"`
	Day               int     `json:"day"`
	Places            []Place `json:"places"`
	Image             string  `json:"image"`
	FullImage         string  `json:"fullImage"`
}

type Region struct{ ID, Title, SentenceName string }

var Regions = []Region{{"world", "World", "the world"}, {"europe", "Europe", "Europe"}, {"usa", "USA", "the USA"}}

// Generate reads source/data-style files (or a local artifacts directory), and writes
// only outlook files under out/v6/forecast/<run>/outlook. Tomorrow is a UTC date;
// the forecast records themselves describe that calendar day in each place's local time.
func Generate(ctx context.Context, source, placesSource, out string, now time.Time) ([]Outlook, error) {
	read := func(name string) ([]byte, error) { return readSource(ctx, source, name) }
	body, err := read("v6/forecast/current.json")
	if err != nil {
		return nil, err
	}
	var pointer Pointer
	if err = json.Unmarshal(body, &pointer); err != nil {
		return nil, err
	}
	firstDate, err := time.Parse("2006-01-02", pointer.FirstDate)
	if err != nil {
		return nil, err
	}
	if pointer.Run == "" || filepath.Base(pointer.Run) != pointer.Run || pointer.Run == "." {
		return nil, fmt.Errorf("invalid forecast run")
	}
	tomorrow := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
	day := int(tomorrow.Sub(firstDate) / (24 * time.Hour))
	if day < 0 || day >= pointer.Days {
		return nil, fmt.Errorf("tomorrow %s is outside forecast starting %s", tomorrow.Format("2006-01-02"), pointer.FirstDate)
	}
	scoreBytes, err := read(fmt.Sprintf("v6/forecast/%s/scores-day-%02d.png", pointer.Run, day))
	if err != nil {
		return nil, err
	}
	decoded, err := png.Decode(bytes.NewReader(scoreBytes))
	if err != nil {
		return nil, err
	}
	scores, ok := decoded.(*image.Gray)
	if !ok || scores.Bounds() != image.Rect(0, 0, gridWidth, gridHeight) {
		return nil, fmt.Errorf("score image must be a %d × %d 8-bit greyscale grid", gridWidth, gridHeight)
	}
	for _, score := range scores.Pix {
		if score > 100 && score != 255 {
			return nil, fmt.Errorf("invalid score byte %d", score)
		}
	}
	placeBytes, err := readSource(ctx, placesSource, "places.tsv")
	if err != nil {
		return nil, err
	}
	places, err := readPlaces(placeBytes, scores)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(out, "v6", "forecast", pointer.Run, "outlook")
	if err = os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	posters, err := newPosterRenderer()
	if err != nil {
		return nil, err
	}
	var outlooks []Outlook
	for _, region := range Regions {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		chosen := rankPlaces(places, region.ID)
		if len(chosen) != 3 {
			return nil, fmt.Errorf("%s needs three separated places with scores", region.ID)
		}
		base := fmt.Sprintf("/data/v6/forecast/%s/outlook/%s", pointer.Run, region.ID)
		outlook := Outlook{MinimumPopulation: populationThreshold(region.ID), Region: region.ID, Title: region.Title, Date: tomorrow.Format("2006-01-02"), Run: pointer.Run, Day: day, Places: chosen, Image: base + ".png", FullImage: base + "-full.png"}
		outlook.Sentence = fmt.Sprintf("The nicest weather in %s tomorrow, %s, will be in and around %s, %s and %s.", region.SentenceName, tomorrow.Format("Monday 2 January"), chosen[0].Name, chosen[1].Name, chosen[2].Name)
		if err = posters.write(directory, outlook, scores); err != nil {
			return nil, err
		}
		body, err = json.MarshalIndent(outlook, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(directory, region.ID+".json"), append(body, '\n'), 0644); err != nil {
			return nil, err
		}
		outlooks = append(outlooks, outlook)
	}
	return outlooks, nil
}

func readSource(ctx context.Context, source, name string) ([]byte, error) {
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		return os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", name, response.StatusCode)
	}
	return io.ReadAll(response.Body)
}

func readPlaces(body []byte, scores *image.Gray) ([]Place, error) {
	var places []Place
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 6 {
			return nil, fmt.Errorf("invalid places.tsv row")
		}
		population, err := strconv.Atoi(fields[5])
		if err != nil {
			return nil, err
		}
		country := strings.TrimSpace(fields[2][strings.LastIndex(fields[2], ",")+1:])
		if population < minimumPopulation || country == "Palestine" || country == "Israel" || len(fields) > 9 && (fields[9] == "PS" || fields[9] == "IL") {
			continue
		}
		latitude, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			return nil, err
		}
		longitude, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(latitude) || math.IsNaN(longitude) || math.IsInf(latitude, 0) || math.IsInf(longitude, 0) || math.Abs(latitude) > 90 || math.Abs(longitude) > 180 {
			return nil, fmt.Errorf("invalid place coordinates")
		}
		// places.tsv's plain English names have already had accents removed.
		slug := kebab(country) + "/" + kebab(fields[1])
		if seen[slug] {
			continue
		}
		seen[slug] = true
		index := nearestLandPoint(latitude, longitude, scores)
		if index < 0 {
			continue
		}
		places = append(places, Place{fields[0], fields[2], slug, latitude, longitude, int(scores.Pix[index]), index, population})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return places, nil
}

func kebab(text string) string {
	var result strings.Builder
	separator := false
	for _, letter := range strings.ToLower(norm.NFD.String(text)) {
		if letter >= '\u0300' && letter <= '\u036f' {
			continue
		}
		if letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' {
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			separator = false
			result.WriteRune(letter)
		} else {
			separator = true
		}
	}
	return result.String()
}

// FileNames is the complete per-run publication set, relative to the run directory.
func FileNames() []string {
	var names []string
	for _, region := range Regions {
		for _, suffix := range []string{".png", "-full.png", ".json"} {
			names = append(names, "outlook/"+region.ID+suffix)
		}
	}
	return names
}

// Same row-major search, ±2 points, squared degree distance, seam wrap and ties
// as nearestFineIndex in the Worker. Using the score mask also uses that month's land mask.
func nearestLandPoint(latitude, longitude float64, scores *image.Gray) int {
	if latitude < -60 || latitude > 90 {
		return -1
	}
	x, y := math.Mod(math.Mod((longitude+180)*4, gridWidth)+gridWidth, gridWidth), (90-latitude)*4
	column, row := int(math.Floor(x+.5)), int(math.Floor(y+.5))
	nearest, distance := -1, math.Inf(1)
	for dr := -2; dr <= 2; dr++ {
		r := row + dr
		if r < 0 || r > 600 {
			continue
		}
		for dc := -2; dc <= 2; dc++ {
			c := column + dc
			index := r*gridWidth + ((c%gridWidth)+gridWidth)%gridWidth
			if scores.Pix[index] == 255 {
				continue
			}
			squared := math.Pow(float64(r)-y, 2) + math.Pow(float64(c)-x, 2)
			if squared < distance {
				nearest, distance = index, squared
			}
		}
	}
	return nearest
}

var europeanCountries = strings.Split("Albania|Andorra|Austria|Belarus|Belgium|Bosnia and Herzegovina|Bulgaria|Croatia|Cyprus|Czechia|Denmark|Estonia|Faroe Islands|Finland|France|Germany|Gibraltar|Greece|Guernsey|Hungary|Iceland|Ireland|Isle of Man|Italy|Jersey|Kosovo|Latvia|Liechtenstein|Lithuania|Luxembourg|Malta|Moldova|Monaco|Montenegro|Netherlands|North Macedonia|Norway|Poland|Portugal|Romania|Russia|San Marino|Serbia|Slovakia|Slovenia|Spain|Sweden|Switzerland|Turkey|Ukraine|United Kingdom|Vatican", "|")

func inRegion(place Place, region string) bool {
	country := strings.TrimSpace(place.Region[strings.LastIndex(place.Region, ",")+1:])
	switch region {
	case "world":
		return true
	case "usa":
		return country == "United States" && (place.Latitude >= 24 && place.Latitude <= 72 && place.Longitude >= -180 && place.Longitude <= -66 || place.Latitude >= 18 && place.Latitude <= 23 && place.Longitude >= -161 && place.Longitude <= -154)
	case "europe":
		if place.Latitude < 27 || place.Latitude > 72 || place.Longitude < -32 || place.Longitude > 40 {
			return false
		}
		if country == "Turkey" {
			return inEuropeanTurkey(place)
		}
		if country == "Spain" && place.Latitude < 36 {
			return place.Longitude >= -18.5 && place.Longitude <= -13 && place.Latitude <= 30
		}
		for _, name := range europeanCountries {
			if country == name {
				return true
			}
		}
	}
	return false
}

func populationThreshold(region string) int {
	if region == "world" {
		return 300000
	}
	return minimumPopulation
}

// The eastern edge follows the Bosporus, Marmara and Dardanelles through water.
// The remaining edges enclose Thrace; country membership is checked first.
var europeanTurkeyBoundary = [][2]float64{
	{25, 42.5}, {29.3, 42.5}, {29.12, 41.23}, {29.075, 41.12},
	{29.035, 41.045}, {29.005, 41.025}, {28.985, 41}, {28.98, 40.98},
	{28.6, 40.8}, {27.5, 40.4}, {26.75, 40.35}, {26.4, 40.2},
	{26.17, 40.03}, {25.8, 39.8}, {25, 39.8},
}

func inEuropeanTurkey(place Place) bool {
	inside := false
	previous := europeanTurkeyBoundary[len(europeanTurkeyBoundary)-1]
	for _, current := range europeanTurkeyBoundary {
		if (current[1] > place.Latitude) != (previous[1] > place.Latitude) &&
			place.Longitude < (previous[0]-current[0])*(place.Latitude-current[1])/(previous[1]-current[1])+current[0] {
			inside = !inside
		}
		previous = current
	}
	return inside
}

func rankPlaces(places []Place, region string) []Place {
	candidates := make([]Place, 0)
	for _, place := range places {
		if place.Population >= populationThreshold(region) && inRegion(place, region) {
			candidates = append(candidates, place)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		a, b := candidates[left], candidates[right]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Population != b.Population {
			return a.Population > b.Population
		}
		return a.Slug < b.Slug
	})
	var chosen, winningSpots []Place
	for _, candidate := range candidates {
		namedPlace := largestNearbyPlace(candidate, candidates)
		separate := true
		for index, place := range chosen {
			if distanceKM(candidate, winningSpots[index]) < minimumSeparationKM || distanceKM(namedPlace, place) < minimumSeparationKM {
				separate = false
				break
			}
		}
		if separate {
			chosen = append(chosen, namedPlace)
			winningSpots = append(winningSpots, candidate)
		}
		if len(chosen) == 3 {
			break
		}
	}
	return chosen
}

func largestNearbyPlace(winner Place, candidates []Place) Place {
	name := winner
	for _, candidate := range candidates {
		if math.Abs(float64(candidate.Score-winner.Score)) > nearbyNameScoreDifference || distanceKM(winner, candidate) > nearbyNameRadiusKM {
			continue
		}
		if candidate.Population > name.Population || candidate.Population == name.Population && candidate.Slug < name.Slug {
			name = candidate
		}
	}
	return name
}

func distanceKM(a, b Place) float64 {
	radians := math.Pi / 180
	squared := math.Pow(math.Sin((b.Latitude-a.Latitude)*radians/2), 2) + math.Cos(a.Latitude*radians)*math.Cos(b.Latitude*radians)*math.Pow(math.Sin((b.Longitude-a.Longitude)*radians/2), 2)
	return 6371 * 2 * math.Asin(math.Sqrt(min(1, squared)))
}
