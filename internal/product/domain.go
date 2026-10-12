// Package product defines the European refinement footprint.
package product

import "nice-weather-finder/internal/delivery"

type Domain struct{ bounds delivery.GridBounds }

func Europe() Domain {
	return Domain{bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}}
}

func (domain Domain) Bounds() delivery.GridBounds { return domain.bounds }
