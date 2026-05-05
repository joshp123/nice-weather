package timezone

import (
	"time"
	_ "time/tzdata"

	"github.com/ringsaturn/tzf"
)

type Locator struct {
	finder    tzf.F
	locations map[string]*time.Location
}

func NewLocator() (*Locator, error) {
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return nil, err
	}
	return &Locator{finder: finder, locations: map[string]*time.Location{}}, nil
}

func (locator *Locator) Location(lon, lat float64) (string, *time.Location, error) {
	name := locator.finder.GetTimezoneName(lon, lat)
	if name == "" {
		return "UTC", time.UTC, nil
	}
	location, ok := locator.locations[name]
	if !ok {
		var err error
		location, err = time.LoadLocation(name)
		if err != nil {
			return "", nil, err
		}
		locator.locations[name] = location
	}
	return name, location, nil
}
