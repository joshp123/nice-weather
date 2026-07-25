package era5climateproduct

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/tiledelivery"
)

func TestScalarParentsPreserveMaximumZoomIsland(t *testing.T) {
	root := t.TempDir()
	cell := era5hourly.Cell{Latitude: -0.25, Longitude: 0.25}
	builder := newTestBuilder(t, root, "island", []era5hourly.Cell{cell})
	if err := builder.Add(testResult(0, cell, 91)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "island")
	catalog := readCatalog(t, releaseRoot)
	parent := findScalar(t, catalog.ScalarTiles, FieldNiceGood, 0, 0, 0)
	encoded, stats, err := tiledelivery.ReadScalar(
		filepath.Join(releaseRoot, filepath.FromSlash(parent.Path)), parent.SHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.DataTexels == 0 {
		t.Fatal("derived parent dropped a maximum-zoom island")
	}
	found := false
	for _, value := range encoded {
		if value == 91 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("derived parent lost the island's evaluated value")
	}
}

func TestParseCatalogAndValidateReleaseFailClosed(t *testing.T) {
	root := t.TempDir()
	cell := era5hourly.Cell{Latitude: -0.25, Longitude: 0.25}
	builder := newTestBuilder(t, root, "strict", []era5hourly.Cell{cell})
	plan, mask := builder.plan, builder.displayMask
	if err := builder.Add(testResult(0, cell, 66)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "strict")
	if _, err := ValidateRelease(releaseRoot, plan, mask); err != nil {
		t.Fatal(err)
	}

	catalogBytes, err := os.ReadFile(filepath.Join(releaseRoot, CatalogFilename))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(catalogBytes, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	hostile, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(hostile); err == nil {
		t.Fatal("accepted an unknown catalog field")
	}
	if _, err := ParseCatalog(append(catalogBytes, []byte(`{}`)...)); err == nil {
		t.Fatal("accepted trailing catalog JSON")
	}
	for _, field := range []string{"localDay", "landAuthority"} {
		var changed map[string]any
		if err := json.Unmarshal(catalogBytes, &changed); err != nil {
			t.Fatal(err)
		}
		changed["source"].(map[string]any)[field] = "caller asserted"
		encoded, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseCatalog(encoded); err == nil {
			t.Fatalf("accepted non-canonical source %s", field)
		}
	}

	unexpected := filepath.Join(releaseRoot, "unexpected.bin")
	if err := os.WriteFile(unexpected, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(releaseRoot, plan, mask); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("unexpected-file validation error = %v", err)
	}
}

func TestPointHeaderRangeAndRecordReader(t *testing.T) {
	root := t.TempDir()
	cells := []era5hourly.Cell{{Latitude: 0, Longitude: 0}, {Latitude: 0, Longitude: 40}}
	builder := newTestBuilder(t, root, "point-reader", cells)
	if err := builder.Add(testResult(0, cells[0], 72)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(testResult(1, cells[1], 64)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "point-reader")
	catalog := readCatalog(t, releaseRoot)
	asset := catalog.PointBlocks[0]
	body, err := os.ReadFile(filepath.Join(releaseRoot, filepath.FromSlash(asset.Path)))
	if err != nil {
		t.Fatal(err)
	}
	header, err := ParsePointHeader(body[:pointHeaderBytes])
	if err != nil {
		t.Fatal(err)
	}
	row, column, _, err := gridPosition(cells[0].Latitude, cells[0].Longitude)
	if err != nil {
		t.Fatal(err)
	}
	recordIndex := (row%PointBlockRows)*PointBlockCols + column%PointBlockCols
	offset, length, err := PointRecordByteRange(header, recordIndex)
	if err != nil || offset < pointHeaderBytes || length != delivery.ClimatePointRecordBytes {
		t.Fatalf("record range = %d,%d,%v", offset, length, err)
	}
	record, valid, err := ReadPointRecord(
		filepath.Join(releaseRoot, filepath.FromSlash(asset.Path)), asset.Descriptor(), recordIndex,
	)
	if err != nil || !valid || record.Distribution.Nice.GoodOrBetterPercent() != 72 {
		t.Fatalf("point record = %+v, valid=%t, err=%v", record, valid, err)
	}
	if _, _, err := PointRecordByteRange(header, PointBlockRows*PointBlockCols); err == nil {
		t.Fatal("accepted an out-of-range point record")
	}
	forged := header
	forged.Rows = 1
	if _, _, err := PointRecordByteRange(forged, 0); err == nil {
		t.Fatal("accepted a forged point header")
	}
	hostile := append([]byte(nil), body[:pointHeaderBytes]...)
	hostile[18] = 1
	if _, err := ParsePointHeader(hostile); err == nil {
		t.Fatal("accepted nonzero reserved point-header data")
	}
	wrongAsset := catalog.PointBlocks[1]
	wrongDescriptor := asset.Descriptor()
	wrongDescriptor.SHA256 = wrongAsset.SHA256
	wrongDescriptor.PayloadSHA256 = wrongAsset.PayloadSHA256
	if _, _, err := ReadPointRecord(
		filepath.Join(releaseRoot, filepath.FromSlash(wrongAsset.Path)), wrongDescriptor, 0,
	); err == nil {
		t.Fatal("accepted a valid point asset from the wrong block")
	}
}

func TestValidateReleaseRejectsChecksumMatchedMetadataWithCorruptAsset(t *testing.T) {
	root := t.TempDir()
	cell := era5hourly.Cell{Latitude: -0.25, Longitude: 0.25}
	builder := newTestBuilder(t, root, "corrupt", []era5hourly.Cell{cell})
	plan, mask := builder.plan, builder.displayMask
	if err := builder.Add(testResult(0, cell, 68)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "corrupt")
	catalog := readCatalog(t, releaseRoot)
	tile := findScalar(t, catalog.ScalarTiles, FieldNiceGood, 1, 1, 1)
	path := filepath.Join(releaseRoot, filepath.FromSlash(tile.Path))
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] ^= 1
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(releaseRoot, plan, mask); err == nil {
		t.Fatal("accepted a corrupt scalar asset")
	}
}

func TestValidateReleaseRequiresExactPlanOccupancy(t *testing.T) {
	root := t.TempDir()
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0}
	builder := newTestBuilder(t, root, "occupancy", []era5hourly.Cell{cell})
	plan, mask := builder.plan, builder.displayMask
	if err := builder.Add(testResult(0, cell, 68)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "occupancy")
	catalog := readCatalog(t, releaseRoot)
	block := &catalog.PointBlocks[0]
	path := filepath.Join(releaseRoot, filepath.FromSlash(block.Path))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	row, column, _, _ := gridPosition(cell.Latitude, cell.Longitude)
	from := (row%PointBlockRows)*PointBlockCols + column%PointBlockCols
	to := from + 1
	fromStart := pointHeaderBytes + from*delivery.ClimatePointRecordBytes
	toStart := pointHeaderBytes + to*delivery.ClimatePointRecordBytes
	copy(body[toStart:toStart+delivery.ClimatePointRecordBytes], body[fromStart:fromStart+delivery.ClimatePointRecordBytes])
	clear(body[fromStart : fromStart+delivery.ClimatePointRecordBytes])
	payloadDigest := sha256.Sum256(body[pointHeaderBytes:])
	copy(body[32:64], payloadDigest[:])
	bodyDigest := sha256.Sum256(body)
	block.PayloadSHA256 = hex.EncodeToString(payloadDigest[:])
	block.SHA256 = hex.EncodeToString(bodyDigest[:])
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteCatalog(t, releaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(releaseRoot, plan, mask); err == nil ||
		!strings.Contains(err.Error(), "outside the source plan") {
		t.Fatalf("plan occupancy error = %v", err)
	}
}

func rewriteCatalog(t *testing.T, releaseRoot string, catalog Catalog) error {
	t.Helper()
	encoded, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(releaseRoot, CatalogFilename), append(encoded, '\n'), 0o644)
}
