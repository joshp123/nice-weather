// Package product defines the geographic contract shared by product assembly.
package product

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"nice-weather-finder/internal/delivery"
)

var domainIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Domain is the validated geographic boundary for one product build. Provider
// footprints remain provider concerns; the domain only states what the product
// intends to publish and the evaluated display geometry used inside it.
type Domain struct {
	region             delivery.Region
	denseForecastScore delivery.GridSpec
}

// NewDomain validates a geographic product boundary and its forecast display
// grid. The display grid must cover exactly the declared region.
func NewDomain(region delivery.Region, denseForecastScore delivery.GridSpec) (Domain, error) {
	if !domainIDPattern.MatchString(region.ID) || strings.TrimSpace(region.Label) == "" {
		return Domain{}, errors.New("product domain region is incomplete or invalid")
	}
	if err := delivery.ValidateGridSpec(denseForecastScore); err != nil {
		return Domain{}, fmt.Errorf("product domain display grid: %w", err)
	}
	if denseForecastScore.Bounds != region.Bounds {
		return Domain{}, errors.New("product domain display grid must match region bounds")
	}
	if _, err := delivery.NewPointGeometry(denseForecastScore); err != nil {
		return Domain{}, fmt.Errorf("product domain display projection: %w", err)
	}
	return Domain{region: region, denseForecastScore: denseForecastScore}, nil
}

// Europe is the current production domain. Global expansion must introduce a
// new explicit domain rather than changing a decoder fallback.
func Europe() Domain {
	bounds := delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}
	domain, err := NewDomain(
		delivery.Region{ID: "europe", Label: "Europe", Bounds: bounds},
		delivery.GridSpec{Bounds: bounds, Rows: delivery.DenseForecastScoreRows, Cols: delivery.DenseForecastScoreCols},
	)
	if err != nil {
		panic(err)
	}
	return domain
}

// Validate rejects a zero or corrupted value at the composition boundary.
func (domain Domain) Validate() error {
	_, err := NewDomain(domain.region, domain.denseForecastScore)
	return err
}

func (domain Domain) Region() delivery.Region { return domain.region }

func (domain Domain) Bounds() delivery.GridBounds { return domain.region.Bounds }

func (domain Domain) DenseForecastScoreSpec() delivery.GridSpec { return domain.denseForecastScore }
