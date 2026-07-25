package era5climateproduct

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/tiledelivery"
)

func TestLoadDisplayMaskRejectsSizeChecksumAndValuesBeforeUse(t *testing.T) {
	root := t.TempDir()
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, canonical := testPlanAndMask(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell})
	short := filepath.Join(root, "short.bin")
	if err := os.WriteFile(short, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDisplayMask(short, plan); err == nil {
		t.Fatal("accepted a short display mask")
	}

	for name, values := range map[string][]byte{
		"all-water": make([]byte, Rows*Columns),
		"all-land":  bytes.Repeat([]byte{1}, Rows*Columns),
		"invalid-value": func() []byte {
			values := append([]byte(nil), canonical.values...)
			values[100] = 2
			return values
		}(),
	} {
		path := filepath.Join(root, name+".bin")
		if err := os.WriteFile(path, values, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDisplayMask(path, plan); err == nil {
			t.Fatalf("accepted forged %s display mask", name)
		}
	}
}

func TestNewRejectsUnvalidatedPlanBeforeProductAllocation(t *testing.T) {
	if _, err := New(testConfig(t.TempDir(), "unvalidated"), era5hourly.CellPlan{}, DisplayMask{}); err == nil {
		t.Fatal("accepted a zero-value, unvalidated cell plan")
	}
}

func TestDisplayMaskKeepsCoastalPointEvidenceButSuppressesWaterCentreScalar(t *testing.T) {
	root := t.TempDir()
	centre := era5hourly.Cell{Latitude: -0.25, Longitude: 0.25, TimezoneID: "UTC"}
	coast := era5hourly.Cell{Latitude: -0.25, Longitude: 0.5, TimezoneID: "UTC"}
	plan, mask := testPlanAndMask(t, 11, []era5hourly.Cell{centre, coast}, []era5hourly.Cell{centre})
	builder, err := New(testConfig(root, "coastal-support"), plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(testResult(0, centre, 55)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(testResult(1, coast, 77)); err != nil {
		t.Fatal(err)
	}
	score, valid, err := builder.scalarSampler(fieldRank(FieldNiceGood))(tiledelivery.ScalarSample{
		Longitude: 0.5, Latitude: -0.25,
	})
	if err != nil || valid || score != 0 {
		t.Fatalf("water-centre scalar = %d, valid=%t, err=%v", score, valid, err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	catalog := readCatalog(t, filepath.Join(root, "releases", "coastal-support"))
	if len(catalog.PointBlocks) != 1 {
		t.Fatalf("coastal point block count = %d; want 1", len(catalog.PointBlocks))
	}
	if len(catalog.ScalarTiles) == 0 {
		t.Fatal("centre-on-land cell produced no scalar tiles")
	}
	if catalog.Source.DisplayMaskSHA256 != mask.sha256 ||
		catalog.Source.DisplayMaskAuthority != DisplayMaskAuthority ||
		catalog.Source.LandSourceSHA256 != mask.landSourceSHA256 ||
		catalog.Source.DisplayMaskDerivation != mask.derivation {
		t.Fatal("catalog lost display-mask provenance")
	}
}

func TestBuilderRequiresExactHistoricalValidDayCountPerTimezone(t *testing.T) {
	root := t.TempDir()
	cells := []era5hourly.Cell{
		{Latitude: 0, Longitude: 0, TimezoneID: "UTC"},
		{Latitude: 0, Longitude: 0.25, TimezoneID: "Pacific/Apia"},
	}
	plan, mask := testPlanAndMask(t, 12, cells, cells)
	builder, err := New(testConfig(root, "valid-days"), plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	tooShort := testResult(0, cells[0], 60)
	tooShort.Days = 1
	if err := builder.Add(tooShort); err == nil {
		t.Fatal("accepted a one-day climate result")
	}
	utc := testResult(0, cells[0], 60)
	utc.Days = 620
	if err := builder.Add(utc); err != nil {
		t.Fatal(err)
	}
	apia := testResult(1, cells[1], 61)
	apia.Days = 619
	if err := builder.Add(apia); err != nil {
		t.Fatal(err)
	}
}
