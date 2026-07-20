package main

import (
	"os"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/tiledelivery"
)

func TestCoveringIDsStayInsideTheBoundedEuropePyramid(t *testing.T) {
	bounds := delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}
	ids, err := coveringIDs(bounds, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 56 {
		t.Fatalf("zoom 5 Europe coverage has %d tiles; want 56", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		key := tileKey(id)
		if seen[key] {
			t.Fatalf("duplicate tile %s", key)
		}
		seen[key] = true
		tile, err := id.Bounds()
		if err != nil {
			t.Fatal(err)
		}
		if tile.East <= bounds.West || tile.West >= bounds.East || tile.North <= bounds.South || tile.South >= bounds.North {
			t.Fatalf("tile %s does not intersect the product region: %+v", key, tile)
		}
	}
}

func TestScalarCanaryReusesOnlyAnIdenticalImmutableTile(t *testing.T) {
	root := t.TempDir()
	writer, err := tiledelivery.NewWriter(root, "test-release")
	if err != nil {
		t.Fatal(err)
	}
	id, err := tiledelivery.NewID("europe", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sampler := func(tiledelivery.ScalarSample) (uint8, bool, error) { return 73, true, nil }
	first, err := writeOrReuseScalar(writer, root, "test-release", id, "2026-07-20", sampler)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeOrReuseScalar(writer, root, "test-release", id, "2026-07-20", sampler)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || first.Path != second.Path {
		t.Fatalf("reused tile changed identity: first=%+v second=%+v", first, second)
	}
	data, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 72
	if err := os.WriteFile(first.Path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeOrReuseScalar(writer, root, "test-release", id, "2026-07-20", sampler); err == nil {
		t.Fatal("reused a corrupted immutable scalar tile")
	}
}

func TestSelectPeriodUsesFirstDayOrExactRequestedDay(t *testing.T) {
	manifest := delivery.Manifest{Forecast: delivery.ForecastCatalog{Days: []delivery.ForecastPeriod{
		{Period: delivery.Period{ID: "2026-07-20", Label: "Mon 20 Jul"}},
		{Period: delivery.Period{ID: "2026-07-21", Label: "Tue 21 Jul"}},
	}}}
	first, err := selectPeriod(manifest, "")
	if err != nil || first.ID != "2026-07-20" {
		t.Fatalf("default period = %+v, %v", first, err)
	}
	requested, err := selectPeriod(manifest, "2026-07-21")
	if err != nil || requested.ID != "2026-07-21" {
		t.Fatalf("requested period = %+v, %v", requested, err)
	}
	if _, err := selectPeriod(manifest, "2026-07-22"); err == nil {
		t.Fatal("accepted a day outside the release")
	}
}

func TestScalarNoDataCompressesWithoutChangingDecodedContract(t *testing.T) {
	encoded := make([]byte, 66_564)
	for index := range encoded {
		encoded[index] = 255
	}
	compressed, err := compressedSize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if compressed >= uint64(len(encoded))/100 {
		t.Fatalf("no-data tile compressed to %d bytes from %d", compressed, len(encoded))
	}
}
