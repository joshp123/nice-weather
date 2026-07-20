package product

import (
	"testing"

	"nice-weather-finder/internal/delivery"
)

func TestDomainAcceptsNonEuropeanProductRectangle(t *testing.T) {
	bounds := delivery.GridBounds{West: 150, East: 152, South: -35, North: -33}
	domain, err := NewDomain(
		delivery.Region{ID: "southeast-australia", Label: "South-east Australia", Bounds: bounds},
		delivery.GridSpec{Bounds: bounds, Rows: 32, Cols: 32},
	)
	if err != nil {
		t.Fatal(err)
	}
	if domain.Bounds() != bounds || domain.Region().ID != "southeast-australia" || domain.DenseForecastScoreSpec().Bounds != bounds {
		t.Fatalf("domain geography drifted: region=%+v display=%+v", domain.Region(), domain.DenseForecastScoreSpec())
	}
}

func TestEuropePreservesPublishedRegionAndDenseGeometry(t *testing.T) {
	domain := Europe()
	wantBounds := delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}
	if domain.Region() != (delivery.Region{ID: "europe", Label: "Europe", Bounds: wantBounds}) {
		t.Fatalf("Europe region = %+v", domain.Region())
	}
	spec := domain.DenseForecastScoreSpec()
	geometry, err := delivery.NewPointGeometry(spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Bounds != wantBounds || spec.Rows != 680 || spec.Cols != 1104 || geometry.Rows != 1179 || geometry.Cols != 1104 {
		t.Fatalf("Europe geometry changed: spec=%+v display=%dx%d", spec, geometry.Cols, geometry.Rows)
	}
}

func TestDomainRejectsImplicitOrMismatchedGeography(t *testing.T) {
	europe := Europe()
	if err := europe.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Domain{}).Validate(); err == nil {
		t.Fatal("zero product domain was accepted")
	}
	_, err := NewDomain(
		delivery.Region{ID: "test", Label: "Test", Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}},
		delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 3, South: 0, North: 2}, Rows: 2, Cols: 3},
	)
	if err == nil {
		t.Fatal("display grid outside the product region was accepted")
	}
}
