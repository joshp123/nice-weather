package landmask

import (
	"encoding/json"
	"fmt"
	"os"
)

type Mask struct {
	polygons []polygon
}

type polygon struct {
	rings [][]point
	box   bounds
}

type point struct {
	lon float64
	lat float64
}

type bounds struct {
	west  float64
	south float64
	east  float64
	north float64
}

type featureCollection struct {
	Type     string    `json:"type"`
	Features []feature `json:"features"`
}

type feature struct {
	Geometry geometry `json:"geometry"`
}

type geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

func LoadGeoJSON(path string) (Mask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Mask{}, err
	}
	var collection featureCollection
	if err := json.Unmarshal(data, &collection); err != nil {
		return Mask{}, err
	}
	var mask Mask
	for _, feature := range collection.Features {
		switch feature.Geometry.Type {
		case "Polygon":
			var coords [][][]float64
			if err := json.Unmarshal(feature.Geometry.Coordinates, &coords); err != nil {
				return Mask{}, err
			}
			mask.polygons = append(mask.polygons, polygonFromCoordinates(coords))
		case "MultiPolygon":
			var coords [][][][]float64
			if err := json.Unmarshal(feature.Geometry.Coordinates, &coords); err != nil {
				return Mask{}, err
			}
			for _, polygonCoords := range coords {
				mask.polygons = append(mask.polygons, polygonFromCoordinates(polygonCoords))
			}
		default:
			return Mask{}, fmt.Errorf("unsupported land geometry %q", feature.Geometry.Type)
		}
	}
	if len(mask.polygons) == 0 {
		return Mask{}, fmt.Errorf("land mask contains no polygons")
	}
	return mask, nil
}

func (mask Mask) Contains(lon, lat float64) bool {
	for _, polygon := range mask.polygons {
		if polygon.contains(lon, lat) {
			return true
		}
	}
	return false
}

func (mask Mask) TouchesCell(lon, lat, halfStep float64) bool {
	for _, polygon := range mask.polygons {
		if polygon.touchesCell(lon, lat, halfStep) {
			return true
		}
	}
	return false
}

func polygonFromCoordinates(coords [][][]float64) polygon {
	rings := make([][]point, 0, len(coords))
	for _, ringCoords := range coords {
		ring := make([]point, 0, len(ringCoords))
		for _, coord := range ringCoords {
			if len(coord) >= 2 {
				ring = append(ring, point{lon: coord[0], lat: coord[1]})
			}
		}
		rings = append(rings, ring)
	}
	if len(rings) == 0 {
		return polygon{}
	}
	return polygon{rings: rings, box: boundsForRing(rings[0])}
}

func (polygon polygon) contains(lon, lat float64) bool {
	if !polygon.box.contains(lon, lat) {
		return false
	}
	if len(polygon.rings) == 0 || !ringContains(polygon.rings[0], lon, lat) {
		return false
	}
	for _, hole := range polygon.rings[1:] {
		if ringContains(hole, lon, lat) {
			return false
		}
	}
	return true
}

func (polygon polygon) touchesCell(lon, lat, halfStep float64) bool {
	if !polygon.box.expand(halfStep).contains(lon, lat) {
		return false
	}
	offsets := [...]struct {
		lon float64
		lat float64
	}{
		{0, 0},
		{-halfStep, -halfStep},
		{0, -halfStep},
		{halfStep, -halfStep},
		{-halfStep, 0},
		{halfStep, 0},
		{-halfStep, halfStep},
		{0, halfStep},
		{halfStep, halfStep},
	}
	for _, offset := range offsets {
		if polygon.contains(lon+offset.lon, lat+offset.lat) {
			return true
		}
	}
	return false
}

func (box bounds) contains(lon, lat float64) bool {
	return lon >= box.west && lon <= box.east && lat >= box.south && lat <= box.north
}

func (box bounds) expand(amount float64) bounds {
	return bounds{
		west:  box.west - amount,
		south: box.south - amount,
		east:  box.east + amount,
		north: box.north + amount,
	}
}

func ringContains(ring []point, lon, lat float64) bool {
	inside := false
	j := len(ring) - 1
	for i := range ring {
		pi := ring[i]
		pj := ring[j]
		if (pi.lat > lat) != (pj.lat > lat) {
			x := (pj.lon-pi.lon)*(lat-pi.lat)/(pj.lat-pi.lat) + pi.lon
			if lon < x {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func boundsForRing(ring []point) bounds {
	if len(ring) == 0 {
		return bounds{}
	}
	box := bounds{west: ring[0].lon, east: ring[0].lon, south: ring[0].lat, north: ring[0].lat}
	for _, point := range ring[1:] {
		if point.lon < box.west {
			box.west = point.lon
		}
		if point.lon > box.east {
			box.east = point.lon
		}
		if point.lat < box.south {
			box.south = point.lat
		}
		if point.lat > box.north {
			box.north = point.lat
		}
	}
	return box
}
