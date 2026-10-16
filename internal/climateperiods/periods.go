// Package climateperiods turns the twelve climate months into the periods a
// traveller plans with (December to February, ..., the whole year), and
// records each town's share of nice days for every period so the Worker can
// answer "where is it nice then?".
package climateperiods

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	width                          = 1440
	height                         = 721
	noData                         = 255
	minimumPopulationForPlaceTable = 5000
)

var layers = []string{"nice", "cycling", "comfort", "dry", "clear", "calm-nice", "calm-cycling"}

// Period is a named set of months. Its value is the days-weighted share of
// nice days across those months, so a season reads as "share of its days".
type Period struct {
	Name   string
	Months []int
}

var Periods = []Period{
	{"dec-feb", []int{12, 1, 2}},
	{"mar-may", []int{3, 4, 5}},
	{"jun-aug", []int{6, 7, 8}},
	{"sep-nov", []int{9, 10, 11}},
	{"year", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
}

var daysInMonth = []float64{31, 28.25, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// Build writes climate/<period>/<layer>.png for every period and
// climate/places.json with every town's share of nice days per month and period.
func Build(climateRoot, placesPath string) error {
	monthLayers := map[string][12][]byte{}
	for _, layer := range layers {
		var months [12][]byte
		for month := 1; month <= 12; month++ {
			values, err := readGreyscale(filepath.Join(climateRoot, fmt.Sprintf("month-%02d", month), layer+".png"))
			if err != nil {
				return err
			}
			months[month-1] = values
		}
		monthLayers[layer] = months
	}

	periodLayers := map[string]map[string][]byte{}
	for _, period := range Periods {
		periodLayers[period.Name] = map[string][]byte{}
		for _, layer := range layers {
			combined := daysWeightedShare(monthLayers[layer], period.Months)
			periodLayers[period.Name][layer] = combined
			if err := writeGreyscale(filepath.Join(climateRoot, period.Name, layer+".png"), combined); err != nil {
				return err
			}
		}
	}
	return writePlaceTable(climateRoot, placesPath, monthLayers, periodLayers)
}

func daysWeightedShare(months [12][]byte, selected []int) []byte {
	combined := make([]byte, width*height)
	for cell := range combined {
		total, days := 0.0, 0.0
		for _, month := range selected {
			value := months[month-1][cell]
			if value == noData {
				total = -1
				break
			}
			total += float64(value) * daysInMonth[month-1]
			days += daysInMonth[month-1]
		}
		if total < 0 {
			combined[cell] = noData
		} else {
			combined[cell] = byte(math.Round(total / days))
		}
	}
	return combined
}

type placeTable struct {
	Periods []string `json:"periods"`
	// Each place: name, region, latitude, longitude, population, nice shares, cycling shares.
	Places [][]any `json:"places"`
}

func writePlaceTable(climateRoot, placesPath string, monthLayers map[string][12][]byte, periodLayers map[string]map[string][]byte) error {
	file, err := os.Open(placesPath)
	if err != nil {
		return err
	}
	defer file.Close()
	table := placeTable{}
	for month := 1; month <= 12; month++ {
		table.Periods = append(table.Periods, fmt.Sprintf("month-%02d", month))
	}
	for _, period := range Periods {
		table.Periods = append(table.Periods, period.Name)
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		population, _ := strconv.Atoi(fields[5])
		if population < minimumPopulationForPlaceTable {
			break // places.tsv is sorted by population, largest first
		}
		latitude, _ := strconv.ParseFloat(fields[3], 64)
		longitude, _ := strconv.ParseFloat(fields[4], 64)
		cell, ok := nearestLandCell(monthLayers["nice"][0], latitude, longitude)
		if !ok {
			continue
		}
		valuesFor := func(layer string) []int {
			values := make([]int, 0, len(table.Periods))
			for month := 0; month < 12; month++ {
				values = append(values, int(monthLayers[layer][month][cell]))
			}
			for _, period := range Periods {
				values = append(values, int(periodLayers[period.Name][layer][cell]))
			}
			return values
		}
		table.Places = append(table.Places, []any{fields[0], fields[2], latitude, longitude, population, valuesFor("nice"), valuesFor("cycling")})
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(table)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(climateRoot, "places.json"), body, 0644)
}

// nearestLandCell finds the closest cell with data within two cells, because a
// coastal town's own cell can be sea.
func nearestLandCell(values []byte, latitude, longitude float64) (int, bool) {
	row := int(math.Round((90 - latitude) / 0.25))
	column := int(math.Round((longitude+180)/0.25)) % width
	best, bestDistance := 0, math.Inf(1)
	for dRow := -2; dRow <= 2; dRow++ {
		for dColumn := -2; dColumn <= 2; dColumn++ {
			r, c := row+dRow, (column+dColumn+width)%width
			if r < 0 || r >= height || values[r*width+c] == noData {
				continue
			}
			if distance := math.Hypot(float64(dRow), float64(dColumn)); distance < bestDistance {
				best, bestDistance = r*width+c, distance
			}
		}
	}
	return best, !math.IsInf(bestDistance, 1)
}

func readGreyscale(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	grey, ok := decoded.(*image.Gray)
	if !ok || grey.Rect.Dx() != width || grey.Rect.Dy() != height {
		return nil, fmt.Errorf("%s: want a %dx%d greyscale PNG", path, width, height)
	}
	return grey.Pix, nil
}

func writeGreyscale(path string, values []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, &image.Gray{Pix: values, Stride: width, Rect: image.Rect(0, 0, width, height)})
}
