package weatherinput

import "math"

type Grid struct {
	West     float64
	East     float64
	South    float64
	North    float64
	StepDeg  float64
	Width    int
	Height   int
	Horizons map[string][]WeatherInput
	Land     []bool
}

func NewGrid(west, east, south, north, stepDeg float64) Grid {
	width := int(math.Round((east-west)/stepDeg)) + 1
	height := int(math.Round((north-south)/stepDeg)) + 1
	return Grid{
		West:     west,
		East:     east,
		South:    south,
		North:    north,
		StepDeg:  stepDeg,
		Width:    width,
		Height:   height,
		Horizons: map[string][]WeatherInput{},
	}
}

func (grid Grid) Coordinates() []Coordinate {
	coords := make([]Coordinate, 0, grid.Width*grid.Height)
	for y := 0; y < grid.Height; y++ {
		lat := grid.South + float64(y)*grid.StepDeg
		for x := 0; x < grid.Width; x++ {
			lon := grid.West + float64(x)*grid.StepDeg
			coords = append(coords, Coordinate{Lon: lon, Lat: lat})
		}
	}
	return coords
}

func (grid Grid) InputAt(horizon string, lon, lat float64) (WeatherInput, bool) {
	values, ok := grid.Horizons[horizon]
	if !ok || lon < grid.West || lon > grid.East || lat < grid.South || lat > grid.North {
		return WeatherInput{}, false
	}
	x := int(math.Round((lon - grid.West) / grid.StepDeg))
	y := int(math.Round((lat - grid.South) / grid.StepDeg))
	if x < 0 || x >= grid.Width || y < 0 || y >= grid.Height {
		return WeatherInput{}, false
	}
	index := y*grid.Width + x
	if index < 0 || index >= len(values) {
		return WeatherInput{}, false
	}
	if len(grid.Land) == len(values) && !grid.Land[index] {
		return WeatherInput{}, false
	}
	return values[index], true
}

func (grid *Grid) ApplyLandMask(touchesLand func(lon, lat, halfStep float64) bool) {
	grid.Land = make([]bool, grid.Width*grid.Height)
	halfStep := grid.StepDeg / 2
	for y := 0; y < grid.Height; y++ {
		lat := grid.South + float64(y)*grid.StepDeg
		for x := 0; x < grid.Width; x++ {
			lon := grid.West + float64(x)*grid.StepDeg
			grid.Land[y*grid.Width+x] = touchesLand(lon, lat, halfStep)
		}
	}
}

type Coordinate struct {
	Lon float64
	Lat float64
}
