package update

import (
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/product"
)

func testProductDomain() product.Domain { return product.Europe() }

func testDecoder(t *testing.T) *pipeline.Decoder {
	t.Helper()
	decoder, err := pipeline.NewDecoder(nil, testProductDomain().Bounds())
	if err != nil {
		t.Fatal(err)
	}
	return decoder
}

func TestProductDomainCarriesNonEuropeanBoundsAndTimezone(t *testing.T) {
	bounds := delivery.GridBounds{West: 150, East: 152, South: -35, North: -33}
	domain, err := product.NewDomain(
		delivery.Region{ID: "southeast-australia", Label: "South-east Australia", Bounds: bounds},
		delivery.GridSpec{Bounds: bounds, Rows: 2, Cols: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := pipeline.NewDecoder(nil, domain.Bounds())
	if err != nil {
		t.Fatal(err)
	}
	if decoder == nil {
		t.Fatal("explicit non-European decoder was not constructed")
	}
	sydney, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatal(err)
	}
	locations, err := pipeline.CellLocations(domain.DenseForecastScoreSpec(), func(lon, lat float64) (*time.Location, error) {
		if lon < bounds.West || lon > bounds.East || lat < bounds.South || lat > bounds.North {
			t.Fatalf("locator received coordinate outside product domain: %.3f,%.3f", lon, lat)
		}
		return sydney, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, location := range locations {
		if location.String() != "Australia/Sydney" {
			t.Fatalf("location %d = %q", index, location)
		}
	}
}
