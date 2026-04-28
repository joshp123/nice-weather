package grid

type Bounds struct {
	West  float64
	South float64
	East  float64
	North float64
}

var EuropeBounds = Bounds{West: -25, South: 34, East: 45, North: 72}

func (bounds Bounds) Contains(lon, lat float64) bool {
	return lon >= bounds.West && lon <= bounds.East && lat >= bounds.South && lat <= bounds.North
}
