package landmask

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

type Mask struct {
	polygons []polygon
}

type polygon struct {
	rings []ring
	box   bounds
}

type ring struct {
	points []point
	box    bounds
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
	rings := make([]ring, 0, len(coords))
	for _, ringCoords := range coords {
		points := make([]point, 0, len(ringCoords))
		for _, coord := range ringCoords {
			if len(coord) >= 2 {
				points = append(points, point{lon: coord[0], lat: coord[1]})
			}
		}
		rings = append(rings, ring{points: points, box: boundsForPoints(points)})
	}
	if len(rings) == 0 {
		return polygon{}
	}
	return polygon{rings: rings, box: rings[0].box}
}

func (polygon polygon) contains(lon, lat float64) bool {
	if !polygon.box.contains(lon, lat) {
		return false
	}
	if len(polygon.rings) == 0 || !ringContains(polygon.rings[0].points, lon, lat) {
		return false
	}
	for _, hole := range polygon.rings[1:] {
		if hole.box.contains(lon, lat) && ringContains(hole.points, lon, lat) {
			return false
		}
	}
	return true
}

func (polygon polygon) touchesCell(lon, lat, halfStep float64) bool {
	cell := bounds{
		west:  lon - halfStep,
		south: lat - halfStep,
		east:  lon + halfStep,
		north: lat + halfStep,
	}
	if !polygon.box.intersects(cell) {
		return false
	}
	offsets := [...]struct {
		lon float64
		lat float64
	}{
		{0, 0},
		{-halfStep, -halfStep},
		{halfStep, -halfStep},
		{-halfStep, halfStep},
		{halfStep, halfStep},
	}
	for _, offset := range offsets {
		if polygon.contains(lon+offset.lon, lat+offset.lat) {
			return true
		}
	}
	for _, ring := range polygon.rings {
		if ringTouchesCell(ring, cell) {
			return true
		}
	}
	return false
}

func (box bounds) contains(lon, lat float64) bool {
	return lon >= box.west && lon <= box.east && lat >= box.south && lat <= box.north
}

func (box bounds) intersects(other bounds) bool {
	return box.west <= other.east && box.east >= other.west &&
		box.south <= other.north && box.north >= other.south
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

func ringTouchesCell(ring ring, cell bounds) bool {
	if len(ring.points) == 0 || !ring.box.intersects(cell) {
		return false
	}
	for _, point := range ring.points {
		if cell.contains(point.lon, point.lat) {
			return true
		}
	}
	previous := ring.points[len(ring.points)-1]
	for _, current := range ring.points {
		if segmentIntersectsCell(previous, current, cell) {
			return true
		}
		previous = current
	}
	return false
}

func segmentIntersectsCell(a, b point, cell bounds) bool {
	if cell.contains(a.lon, a.lat) || cell.contains(b.lon, b.lat) {
		return true
	}
	if math.Max(a.lon, b.lon) < cell.west || math.Min(a.lon, b.lon) > cell.east ||
		math.Max(a.lat, b.lat) < cell.south || math.Min(a.lat, b.lat) > cell.north {
		return false
	}
	edges := [...][2]point{
		{{lon: cell.west, lat: cell.south}, {lon: cell.east, lat: cell.south}},
		{{lon: cell.east, lat: cell.south}, {lon: cell.east, lat: cell.north}},
		{{lon: cell.east, lat: cell.north}, {lon: cell.west, lat: cell.north}},
		{{lon: cell.west, lat: cell.north}, {lon: cell.west, lat: cell.south}},
	}
	for _, edge := range edges {
		if segmentsIntersect(a, b, edge[0], edge[1]) {
			return true
		}
	}
	return false
}

func segmentsIntersect(a, b, c, d point) bool {
	o1 := orientation(a, b, c)
	o2 := orientation(a, b, d)
	o3 := orientation(c, d, a)
	o4 := orientation(c, d, b)
	if o1 == 0 && onSegment(a, c, b) {
		return true
	}
	if o2 == 0 && onSegment(a, d, b) {
		return true
	}
	if o3 == 0 && onSegment(c, a, d) {
		return true
	}
	if o4 == 0 && onSegment(c, b, d) {
		return true
	}
	return (o1 > 0) != (o2 > 0) && (o3 > 0) != (o4 > 0)
}

func orientation(a, b, c point) float64 {
	value := (b.lon-a.lon)*(c.lat-a.lat) - (b.lat-a.lat)*(c.lon-a.lon)
	if math.Abs(value) < 1e-12 {
		return 0
	}
	return value
}

func onSegment(a, b, c point) bool {
	return b.lon >= math.Min(a.lon, c.lon) && b.lon <= math.Max(a.lon, c.lon) &&
		b.lat >= math.Min(a.lat, c.lat) && b.lat <= math.Max(a.lat, c.lat)
}

func boundsForPoints(points []point) bounds {
	if len(points) == 0 {
		return bounds{}
	}
	box := bounds{west: points[0].lon, east: points[0].lon, south: points[0].lat, north: points[0].lat}
	for _, point := range points[1:] {
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
