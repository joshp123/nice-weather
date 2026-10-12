package era5climateproduct

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/era5climate"
	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/tiledelivery"
)

const testPlanSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestBuilderPublishesPointAndScalarParityWithHonestDateline(t *testing.T) {
	root := t.TempDir()
	cells := []era5hourly.Cell{
		{Latitude: 0, Longitude: -180},
		{Latitude: -0.25, Longitude: 0.25},
		{Latitude: 0, Longitude: 180},
	}
	builder := newTestBuilder(t, root, "parity", cells)
	for index, score := range []uint8{21, 63, 84} {
		if err := builder.Add(testResult(index, cells[index], score)); err != nil {
			t.Fatalf("add cell %d: %v", index, err)
		}
	}

	mainScore, valid, err := builder.scalarSampler(fieldRank(FieldNiceGood))(tiledelivery.ScalarSample{
		Longitude: -179.9, Latitude: 0,
	})
	if err != nil || !valid || mainScore != 21 {
		t.Fatalf("-180 scalar = %d, %t, %v; want 21, true, nil", mainScore, valid, err)
	}
	overrideScore, valid, err := builder.scalarSampler(fieldRank(FieldNiceGood))(tiledelivery.ScalarSample{
		Longitude: 179.9, Latitude: 0,
	})
	if err != nil || !valid || overrideScore != 84 {
		t.Fatalf("+180 scalar = %d, %t, %v; want 84, true, nil", overrideScore, valid, err)
	}
	if _, valid, err := builder.scalarSampler(fieldRank(FieldNiceGood))(tiledelivery.ScalarSample{
		Longitude: 10, Latitude: 10,
	}); err != nil || valid {
		t.Fatalf("water/no-data scalar valid=%t, err=%v", valid, err)
	}

	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(root, "releases", "parity")
	catalog := readCatalog(t, releaseRoot)
	if catalog.Source.Dataset != "ERA5" || catalog.Geometry.Rows != Rows ||
		catalog.Geometry.Columns != Columns || len(catalog.DatelineOverrides) != 1 {
		t.Fatalf("source-honest catalog mismatch: %+v", catalog)
	}
	if len(catalog.PointBlocks) != 2 {
		t.Fatalf("point block count = %d; want 2", len(catalog.PointBlocks))
	}
	if len(catalog.ScalarTiles) == 0 {
		t.Fatal("catalog has no evaluated scalar tiles")
	}

	mainRecord := readMainRecord(t, releaseRoot, catalog.PointBlocks, cells[1])
	if got := mainRecord.Distribution.Nice.GoodOrBetterPercent(); got != 63 {
		t.Fatalf("main point/scalar value = %d; want 63", got)
	}
	scalarAsset := findScalar(t, catalog.ScalarTiles, FieldNiceGood, 1, 1, 1)
	scalar, _, err := tiledelivery.ReadScalar(
		filepath.Join(releaseRoot, filepath.FromSlash(scalarAsset.Path)),
		scalarAsset.SHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar[tiledelivery.ScalarTextureSize+1]; got != 63 {
		t.Fatalf("published scalar texel = %d; point record = 63", got)
	}
	overrideRow, _, _, err := gridPosition(0, 180)
	if err != nil {
		t.Fatal(err)
	}
	overrideRecord := readOverrideRecord(t, releaseRoot, catalog.DatelineOverrides[0], overrideRow)
	if got := overrideRecord.Distribution.Nice.GoodOrBetterPercent(); got != 84 {
		t.Fatalf("dateline point/scalar value = %d; want 84", got)
	}
}

func TestBuilderRequiresEveryPlanCellExactlyOnce(t *testing.T) {
	root := t.TempDir()
	cells := []era5hourly.Cell{
		{Latitude: 1, Longitude: 1},
		{Latitude: 1, Longitude: 1.25},
	}
	builder := newTestBuilder(t, root, "complete", cells)
	if err := builder.Add(testResult(0, cells[0], 60)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(testResult(0, cells[0], 60)); err == nil {
		t.Fatal("accepted a duplicate source-plan cell")
	}
	if err := builder.Commit(); err == nil {
		t.Fatal("committed an incomplete source plan")
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "complete")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete release became visible: %v", err)
	}
}

func TestBuilderRejectsAmbiguousPlanCoordinatesAndAbortWritesNothing(t *testing.T) {
	root := t.TempDir()
	cells := []era5hourly.Cell{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 0},
	}
	if _, err := newBuilder(
		testConfig(root, "duplicate"), era5hourly.CellPlan{}, 11, testPlanSHA,
		cells, []int{600, 600}, DisplayMask{},
	); err == nil {
		t.Fatal("accepted duplicate plan coordinates")
	}
	builder := newTestBuilder(t, root, "abort", []era5hourly.Cell{{Latitude: 0, Longitude: 0}})
	if err := builder.Abort(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Abort wrote files: %v", entries)
	}
}

func newTestBuilder(t *testing.T, root, release string, cells []era5hourly.Cell) *Builder {
	t.Helper()
	plan, mask := testPlanAndMask(t, 11, cells, cells)
	builder, err := New(testConfig(root, release), plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func testConfig(root, release string) Config {
	return Config{
		StagingRoot: root, ReleaseID: release, MaximumZoom: 1,
	}
}

func testPlanAndMask(
	t *testing.T,
	month int,
	cells []era5hourly.Cell,
	centreCells []era5hourly.Cell,
) (era5hourly.CellPlan, DisplayMask) {
	t.Helper()
	plan, displayValues := testPlanAndMaskData(t, month, cells, centreCells)
	maskPath := filepath.Join(t.TempDir(), "display-mask.bin")
	if err := os.WriteFile(maskPath, displayValues, 0o600); err != nil {
		t.Fatal(err)
	}
	authorityPath, authoritySHA := writeTestPublicationAuthority(t, testPublicationAuthority(plan, displayValues))
	authority, err := LoadPublicationAuthority(authorityPath, authoritySHA, plan)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := LoadDisplayMaskWithAuthority(maskPath, plan, authority)
	if err != nil {
		t.Fatal(err)
	}
	return plan, mask
}

func testPlanAndMaskData(
	t *testing.T,
	month int,
	cells []era5hourly.Cell,
	centreCells []era5hourly.Cell,
) (era5hourly.CellPlan, []byte) {
	t.Helper()
	landMask := make([]byte, Rows*Columns)
	displayValues := make([]byte, Rows*Columns)
	timezoneIndexes := make([]byte, Rows*Columns*2)
	zones := []string{"UTC"}
	zoneIndex := map[string]int{"UTC": 0}
	mainTimezones := make(map[int]string)
	positiveTimezones := make(map[int]string)
	for _, cell := range cells {
		row, column, override, err := gridPosition(cell.Latitude, cell.Longitude)
		if err != nil {
			t.Fatal(err)
		}
		zone := cell.TimezoneID
		if zone == "" {
			zone = "UTC"
		}
		if _, exists := zoneIndex[zone]; !exists {
			zoneIndex[zone] = len(zones)
			zones = append(zones, zone)
		}
		if override {
			positiveTimezones[row] = zone
			continue
		}
		offset := row*Columns + column
		landMask[offset] = 1
		mainTimezones[offset] = zone
		binary.LittleEndian.PutUint16(timezoneIndexes[offset*2:], uint16(zoneIndex[zone]))
	}
	for _, cell := range centreCells {
		row, column, override, err := gridPosition(cell.Latitude, cell.Longitude)
		if err != nil {
			t.Fatal(err)
		}
		if override {
			column = 0
		}
		displayValues[row*Columns+column] = 1
	}
	landCells, centreLandCells := 0, 0
	westEdge, eastEdge := 0, 0
	for row := 0; row < Rows; row++ {
		for column := 0; column < Columns; column++ {
			if landMask[row*Columns+column] == 0 {
				continue
			}
			landCells++
			if column == 0 {
				westEdge++
			}
			if column == Columns-1 {
				eastEdge++
			}
		}
	}
	for _, value := range displayValues {
		centreLandCells += int(value)
	}
	landSHA := sha256.Sum256(landMask)
	timezoneSHA := sha256.Sum256(timezoneIndexes)
	positiveRows := make([]int, 0, len(positiveTimezones))
	for row := range positiveTimezones {
		positiveRows = append(positiveRows, row)
	}
	sort.Ints(positiveRows)
	audits := make([]map[string]any, 0, len(positiveRows))
	for _, row := range positiveRows {
		mainZone, exists := mainTimezones[row*Columns]
		if !exists {
			t.Fatalf("+180 test cell row %d lacks its -180 main cell", row)
		}
		audits = append(audits, map[string]any{
			"row": row, "latitude": 90 - float64(row)*GridDegrees,
			"westSupported": true, "eastSupported": true,
			"mainTimezone": mainZone, "positiveTimezone": positiveTimezones[row],
		})
	}
	encoded := map[string]any{
		"schemaVersion": 1, "year": 2025, "month": month,
		"rows": Rows, "columns": Columns, "gridDegrees": GridDegrees,
		"firstLatitude": 90, "firstLongitude": -180,
		"cellRegistration":      "exact-era5-grid-point-centres",
		"landAuthority":         "Natural Earth 5.1.1 1:10m polygon intersects 0.25 degree ERA5 source-cell footprint",
		"landFeatureFilter":     "featurecla IS NULL OR featurecla = 'Land'",
		"landFeatureClassAudit": "ERA5 climate product test fixture",
		"landSourceSha256":      strings.Repeat("1", 64),
		"timezoneAuthority":     "github.com/ringsaturn/tzf-dist@v0.0.2026-b-fix1",
		"landCells":             landCells, "waterCells": Rows*Columns - landCells,
		"centreLandCells": centreLandCells, "coastSupportCells": landCells - centreLandCells,
		"positiveDatelineSupportRows": len(positiveRows),
		"negativeDatelineSupportRows": len(positiveRows),
		"positiveDatelineBothRows":    len(positiveRows),
		"westEdgeLandCells":           westEdge, "eastEdgeLandCells": eastEdge,
		"landMaskSha256":      hex.EncodeToString(landSHA[:]),
		"timezoneIndexSha256": hex.EncodeToString(timezoneSHA[:]),
		"landMaskBase64":      base64.StdEncoding.EncodeToString(landMask),
		"timezoneIndexBase64": base64.StdEncoding.EncodeToString(timezoneIndexes),
		"timezones":           zones, "positiveDatelineTimezoneAudits": audits,
	}
	jsonBytes, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(jsonBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json.gz")
	if err := os.WriteFile(planPath, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	planDigest := sha256.Sum256(compressed.Bytes())
	plan, err := era5hourly.LoadCellPlan(planPath, hex.EncodeToString(planDigest[:]))
	if err != nil {
		t.Fatal(err)
	}
	return plan, displayValues
}

func testResult(index int, cell era5hourly.Cell, goodPercent uint8) era5climate.CellResult {
	goodPermille := uint16(goodPercent) * 10
	profile := delivery.ClimateProfileDistribution{
		MedianScore: goodPercent, CalmGoodPercent: goodPercent, PoorWeekPermille: 100,
		RatingShares: delivery.ClimateRatingShares{
			Awful: 1000 - goodPermille,
			Good:  goodPermille,
		},
	}
	return era5climate.CellResult{
		CellIndex: index, Latitude: cell.Latitude, Longitude: cell.Longitude, Days: 600,
		Distribution: delivery.ClimateDistributionRecord{
			ComfortGoodPercent: goodPercent, DryGoodPercent: goodPercent,
			BrightGoodPercent: goodPercent, Nice: profile, Cycling: profile, Valid: true,
		},
		Physical: era5climate.PhysicalSummary{
			DaytimeTemperatureP10C: 12.04, DaytimeTemperatureP50C: 20.06,
			DaytimeTemperatureP90C: 27.04, DaytimeMinimumP50C: 10.04,
			DaytimeMaximumP50C: 28.04, DewPointP50C: 15.04, DewPointP90C: 19.04,
			RelativeHumidityP50Percent: 62.4, MuggyDayPercent: 20,
			RainFreeDayPercent: 80, RainP90MM: 5.04, CloudCoverP50Percent: 38.4,
			ClearSkyDayPercent: 65, WindP50KPH: 9.04, WindP90KPH: 22.04,
		},
	}
}

func readCatalog(t *testing.T, releaseRoot string) Catalog {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(releaseRoot, CatalogFilename))
	if err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if err := json.Unmarshal(encoded, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func readMainRecord(t *testing.T, releaseRoot string, assets []PointBlock, cell era5hourly.Cell) delivery.ClimatePointRecord {
	t.Helper()
	row, column, _, err := gridPosition(cell.Latitude, cell.Longitude)
	if err != nil {
		t.Fatal(err)
	}
	latitudeBlock, longitudeBlock := row/PointBlockRows, column/PointBlockCols
	for _, asset := range assets {
		if asset.LatitudeBlock != latitudeBlock || asset.LongitudeBlock != longitudeBlock {
			continue
		}
		body, err := os.ReadFile(filepath.Join(releaseRoot, filepath.FromSlash(asset.Path)))
		if err != nil {
			t.Fatal(err)
		}
		recordIndex := (row%PointBlockRows)*PointBlockCols + column%PointBlockCols
		start := pointHeaderBytes + recordIndex*delivery.ClimatePointRecordBytes
		record, err := delivery.DecodeClimatePointRecord(body[start : start+delivery.ClimatePointRecordBytes])
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	t.Fatal("main point block not found")
	return delivery.ClimatePointRecord{}
}

func readOverrideRecord(t *testing.T, releaseRoot string, asset DatelineBlock, row int) delivery.ClimatePointRecord {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(releaseRoot, filepath.FromSlash(asset.Path)))
	if err != nil {
		t.Fatal(err)
	}
	start := pointHeaderBytes + (row%PointBlockRows)*delivery.ClimatePointRecordBytes
	record, err := delivery.DecodeClimatePointRecord(body[start : start+delivery.ClimatePointRecordBytes])
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func findScalar(t *testing.T, assets []ScalarTile, field Field, zoom uint8, x, y uint32) ScalarTile {
	t.Helper()
	for _, asset := range assets {
		if asset.Field == field && asset.Zoom == zoom && asset.X == x && asset.Y == y {
			return asset
		}
	}
	t.Fatalf("scalar %s %d/%d/%d not found", field, zoom, x, y)
	return ScalarTile{}
}

func TestPointAssetPayloadChecksumAndNoDataRemainCanonical(t *testing.T) {
	root := t.TempDir()
	payload := make([]byte, PointBlockRows*delivery.ClimatePointRecordBytes)
	path := filepath.Join(root, "block.nwd")
	asset, err := writePointAsset(path, overridePointMagic, 0, 0, PointBlockRows, 1, payload)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body[pointHeaderBytes:], payload) || asset.bytes != uint64(len(body)) {
		t.Fatal("point asset changed its no-data payload")
	}
}
