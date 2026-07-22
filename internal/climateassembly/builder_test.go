package climateassembly

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

func TestNativePointAndEveryMaximumScalarFieldHaveExactParity(t *testing.T) {
	checkpoint := patchCheckpoint(t)
	catalog, releaseRoot := publishFixture(t, "parity", checkpoint)
	point := catalog.PointBlocks[0]
	identity, err := tiledelivery.NewClimateBlockIdentity(catalog.ReleaseID, catalog.Period, checkpoint.Identity.Block)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := tiledelivery.OpenClimateBlock(filepath.Join(releaseRoot, filepath.FromSlash(point.Path)), identity, point.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	record, ok, err := reader.RecordAt(-4.5, 36.7)
	_ = reader.Close()
	if err != nil || !ok {
		t.Fatalf("native Malaga point = %+v, %t, %v", record, ok, err)
	}
	for _, field := range climatetiles.Fields() {
		id, row, column, err := tiledelivery.At(string(field), climatetiles.MaximumZoom, -4.5, 36.7)
		if err != nil {
			t.Fatal(err)
		}
		tile := findScalar(t, catalog, field, id.Zoom, id.X, id.Y)
		encoded, _, err := tiledelivery.ReadScalar(filepath.Join(releaseRoot, filepath.FromSlash(tile.Path)), tile.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		got := encoded[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
		want, err := fieldValue(record, field)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s maximum scalar=%d; native point=%d", field, got, want)
		}
	}
}

func TestWorldwideNativeMappingCoversCitiesDatelinePolesAndCoastNoData(t *testing.T) {
	type location struct {
		name       string
		longitude  float64
		latitude   float64
		sourceLat  int
		sourceLong int
	}
	locations := []location{
		{name: "Malaga", longitude: -4.5, latitude: 36.7, sourceLat: 1267, sourceLong: 1755},
		{name: "Amsterdam", longitude: 4.9, latitude: 52.4, sourceLat: 1424, sourceLong: 1849},
		{name: "west dateline", longitude: -179.9, latitude: 0, sourceLat: 900, sourceLong: 1},
		{name: "east dateline", longitude: 179.9, latitude: 0, sourceLat: 900, sourceLong: 3599},
		{name: "north polar edge", longitude: 10, latitude: 85, sourceLat: 1750, sourceLong: 1900},
		{name: "south polar edge", longitude: 10, latitude: -85, sourceLat: 50, sourceLong: 1900},
	}
	identities := make(map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity)
	checkpoints := make(map[agera5.BlockCoordinate]climatebuild.Checkpoint)
	for index, location := range locations {
		coordinate := agera5.BlockCoordinate{Latitude: location.sourceLat / 16, Longitude: location.sourceLong / 32}
		checkpoint, exists := checkpoints[coordinate]
		if !exists {
			checkpoint = emptyCheckpoint(t, coordinate)
		}
		latitudeOffset := location.sourceLat - coordinate.Latitude*16
		longitudeOffset := location.sourceLong - coordinate.Longitude*32
		checkpoint.Months[0][latitudeOffset*checkpoint.Identity.Width+longitudeOffset] = distribution(uint8(10 + index))
		checkpoints[coordinate] = checkpoint
		identities[coordinate] = checkpoint.Identity
	}
	loader := func(_ context.Context, identity climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
		return checkpoints[identity.Block], nil
	}
	cache := newMonthCache(context.Background(), 0, loader, identities)
	for index, location := range locations {
		record, ok, err := cache.recordAt(location.longitude, location.latitude)
		if err != nil || !ok || record.ComfortGoodPercent != uint8(10+index) {
			t.Fatalf("%s mapped to %+v, %t, %v", location.name, record, ok, err)
		}
	}
	for _, coordinate := range [][2]float64{{-179.956, 0}, {179.956, 0}, {0, 85.051}, {0, -85.051}} {
		if _, ok, err := cache.recordAt(coordinate[0], coordinate[1]); err != nil || ok {
			t.Fatalf("outside exact worldwide crop %v = %t, %v", coordinate, ok, err)
		}
	}
	coast, ok, err := cache.recordAt(-4.4, 36.7)
	if err != nil || ok || coast.Valid {
		t.Fatalf("adjacent no-data coast = %+v, %t, %v", coast, ok, err)
	}
}

func TestEveryClimateFieldMapsItsOnlyCanonicalValue(t *testing.T) {
	record := distribution(11)
	wants := map[climatetiles.Field]uint8{
		climatetiles.FieldComfortGood:     11,
		climatetiles.FieldDryGood:         22,
		climatetiles.FieldBrightGood:      33,
		climatetiles.FieldNiceGood:        70,
		climatetiles.FieldNiceCalmGood:    44,
		climatetiles.FieldCyclingGood:     45,
		climatetiles.FieldCyclingCalmGood: 25,
	}
	for _, field := range climatetiles.Fields() {
		got, err := fieldValue(record, field)
		if err != nil || got != wants[field] {
			t.Fatalf("field %s = %d, %v; want %d", field, got, err, wants[field])
		}
	}
	if _, err := fieldValue(record, climatetiles.Field("unknown")); err == nil {
		t.Fatal("unknown field mapped")
	}
}

func TestAllNoDataCheckpointIsOmittedAndCannotBecomeACompleteMonth(t *testing.T) {
	checkpoint := emptyCheckpoint(t, agera5.BlockCoordinate{Latitude: 79, Longitude: 54})
	stage := testStage(t, "all-water")
	config := Config{
		Stage: stage, Period: "month-01", Month: time.January,
		Checkpoints: []climatebuild.CheckpointIdentity{checkpoint.Identity},
		LoadCheckpoint: func(context.Context, climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
			return checkpoint, nil
		},
	}
	if _, err := BuildMonth(context.Background(), config); err == nil || !strings.Contains(err.Error(), "0 point blocks") {
		t.Fatalf("all-no-data build error = %v", err)
	}
	pointPath := filepath.Join(stage.ReleaseRoot, filepath.FromSlash(climatetiles.PointBlockPath(config.Period, checkpoint.Identity.Block)))
	if _, err := os.Lstat(pointPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("all-no-data point block exists: %v", err)
	}
}

func TestLowerLevelsAverageAvailableChildScalars(t *testing.T) {
	checkpoint := patchCheckpoint(t)
	catalog, releaseRoot := publishFixture(t, "averaging", checkpoint)
	field := climatetiles.FieldComfortGood
	assets := make(map[[3]uint32]climatetiles.ScalarTile)
	for _, scalar := range catalog.ScalarTiles {
		if scalar.Field == field {
			assets[[3]uint32{uint32(scalar.Zoom), scalar.X, scalar.Y}] = scalar
		}
	}
	found := false
	for key, parent := range assets {
		if key[0] != uint32(climatetiles.MaximumZoom-1) {
			continue
		}
		parentBytes := readScalar(t, releaseRoot, parent)
		for row := 0; row < tiledelivery.TileSize && !found; row++ {
			for column := 0; column < tiledelivery.TileSize && !found; column++ {
				values := make([]uint8, 0, 4)
				globalX := (int64(parent.X)*tiledelivery.TileSize + int64(column)) * 2
				globalY := (int64(parent.Y)*tiledelivery.TileSize + int64(row)) * 2
				for offsetY := int64(0); offsetY < 2; offsetY++ {
					for offsetX := int64(0); offsetX < 2; offsetX++ {
						value, ok := scalarGlobalValue(t, releaseRoot, assets, climatetiles.MaximumZoom, globalX+offsetX, globalY+offsetY)
						if ok {
							values = append(values, value)
						}
					}
				}
				if len(values) < 2 || allEqual(values) {
					continue
				}
				total := 0
				for _, value := range values {
					total += int(value)
				}
				want := uint8(math.Round(float64(total) / float64(len(values))))
				got := parentBytes[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
				if got != want {
					t.Fatalf("z3 parent=%d; child mean=%d from %v", got, want, values)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("fixture did not produce a mixed valid lower-level sample")
	}
}

func TestCatalogIsDeterministicAndCarriesCheckpointProvenance(t *testing.T) {
	checkpoint := patchCheckpoint(t)
	first, _ := publishFixture(t, "deterministic", checkpoint)
	second, _ := publishFixture(t, "deterministic", checkpoint)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("identical checkpoint inputs produced different catalogs")
	}
	if first.Provenance.SourceEvidenceSHA256 == "" || first.Provenance.LandMaskSetSHA256 == "" || first.Provenance.DerivationContract != climatebuild.CheckpointDerivationContract {
		t.Fatalf("catalog provenance = %+v", first.Provenance)
	}
}

func TestCancellationAndInvalidStageNeverInstallOutsidePrivateStage(t *testing.T) {
	checkpoint := patchCheckpoint(t)
	stage := testStage(t, "cancelled")
	called := false
	config := Config{
		Stage: stage, Period: "month-01", Month: time.January,
		Checkpoints: []climatebuild.CheckpointIdentity{checkpoint.Identity},
		LoadCheckpoint: func(context.Context, climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
			called = true
			return checkpoint, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildMonth(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build error = %v", err)
	}
	if called {
		t.Fatal("cancelled build called checkpoint loader")
	}
	entries, err := os.ReadDir(stage.ReleaseRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled stage entries = %v, %v", entries, err)
	}

	outside := t.TempDir()
	config.Stage.ReleaseRoot = outside
	if _, err := BuildMonth(context.Background(), config); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside stage error = %v", err)
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil || len(outsideEntries) != 0 {
		t.Fatalf("outside release entries = %v, %v", outsideEntries, err)
	}
}

func TestCheckpointLoaderCacheRetainsOnlyOneMonthAndFixedBlockCount(t *testing.T) {
	identities := make(map[agera5.BlockCoordinate]climatebuild.CheckpointIdentity)
	ordered := make([]climatebuild.CheckpointIdentity, 0, checkpointCacheEntries+1)
	for latitude := 3; latitude <= 109 && len(ordered) <= checkpointCacheEntries; latitude++ {
		for longitude := 0; longitude <= 112 && len(ordered) <= checkpointCacheEntries; longitude++ {
			coordinate := agera5.BlockCoordinate{Latitude: latitude, Longitude: longitude}
			identity := checkpointIdentity(t, coordinate)
			identities[coordinate] = identity
			ordered = append(ordered, identity)
		}
	}
	loads := 0
	loader := func(_ context.Context, identity climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
		loads++
		checkpoint := climatebuild.Checkpoint{Identity: identity}
		for month := range checkpoint.Months {
			checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, identity.Width*identity.Height)
		}
		return checkpoint, nil
	}
	cache := newMonthCache(context.Background(), 0, loader, identities)
	for _, identity := range ordered {
		if _, found, err := cache.get(identity.Block); err != nil || !found {
			t.Fatalf("cache get %+v = %t, %v", identity.Block, found, err)
		}
	}
	if cache.peakEntries != checkpointCacheEntries || len(cache.entries) != checkpointCacheEntries {
		t.Fatalf("cache entries peak/current = %d/%d; want %d", cache.peakEntries, len(cache.entries), checkpointCacheEntries)
	}
	if cache.peakRecords > checkpointCacheEntries*16*32 {
		t.Fatalf("cache retained %d records; bound is %d", cache.peakRecords, checkpointCacheEntries*16*32)
	}
	if _, found, err := cache.get(ordered[0].Block); err != nil || !found {
		t.Fatal(err)
	}
	if loads != len(ordered)+1 {
		t.Fatalf("loader calls=%d; want %d after one bounded-cache reload", loads, len(ordered)+1)
	}
}

func publishFixture(t *testing.T, releaseID string, checkpoint climatebuild.Checkpoint) (climatetiles.Catalog, string) {
	t.Helper()
	artifacts := t.TempDir()
	reference, err := climatetiles.BuildAndInstallMonth(context.Background(), artifacts, releaseID, func(ctx context.Context, stage climatetiles.Stage) (climatetiles.Catalog, error) {
		return BuildMonth(ctx, Config{
			Stage: stage, Period: "month-01", Month: time.January,
			Checkpoints: []climatebuild.CheckpointIdentity{checkpoint.Identity},
			LoadCheckpoint: func(context.Context, climatebuild.CheckpointIdentity) (climatebuild.Checkpoint, error) {
				return checkpoint, nil
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, catalog, found, err := climatetiles.ValidateInstalledMonth(artifacts, releaseID, "month-01")
	if err != nil {
		t.Fatal(err)
	}
	if !found || installed != reference {
		t.Fatalf("installed month = %+v, %t; want %+v", installed, found, reference)
	}
	return catalog, filepath.Join(artifacts, "climate", "releases", releaseID)
}

func testStage(t *testing.T, releaseID string) climatetiles.Stage {
	t.Helper()
	root := t.TempDir()
	releaseRoot := filepath.Join(root, "releases", releaseID)
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return climatetiles.Stage{Root: root, ReleaseRoot: releaseRoot, ReleaseID: releaseID}
}

func patchCheckpoint(t *testing.T) climatebuild.Checkpoint {
	t.Helper()
	checkpoint := emptyCheckpoint(t, agera5.BlockCoordinate{Latitude: 79, Longitude: 54})
	for latitudeOffset := 2; latitudeOffset <= 5; latitudeOffset++ {
		for longitudeOffset := 24; longitudeOffset <= 29; longitudeOffset++ {
			value := uint8(10)
			if (latitudeOffset+longitudeOffset)%2 == 0 {
				value = 90
			}
			checkpoint.Months[0][latitudeOffset*checkpoint.Identity.Width+longitudeOffset] = distribution(value)
		}
	}
	return checkpoint
}

func emptyCheckpoint(t *testing.T, coordinate agera5.BlockCoordinate) climatebuild.Checkpoint {
	t.Helper()
	identity := checkpointIdentity(t, coordinate)
	checkpoint := climatebuild.Checkpoint{Identity: identity}
	for month := range checkpoint.Months {
		checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, identity.Width*identity.Height)
	}
	return checkpoint
}

func checkpointIdentity(t *testing.T, coordinate agera5.BlockCoordinate) climatebuild.CheckpointIdentity {
	t.Helper()
	height, width, err := agera5.BlockShape(coordinate)
	if err != nil {
		t.Fatal(err)
	}
	return climatebuild.CheckpointIdentity{
		Block: coordinate, Width: width, Height: height,
		NaturalEarthMaskSHA256: sha256.Sum256([]byte("mask-" + blockName(coordinate))),
		SourceEvidenceSHA256:   sha256.Sum256([]byte("common-source-evidence")),
	}
}

func blockName(coordinate agera5.BlockCoordinate) string {
	return string(rune(coordinate.Latitude+1)) + ":" + string(rune(coordinate.Longitude+1))
}

func distribution(comfort uint8) delivery.ClimateDistributionRecord {
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: comfort, DryGoodPercent: 22, BrightGoodPercent: 33,
		Nice: delivery.ClimateProfileDistribution{
			MedianScore: 60, CalmGoodPercent: 44, PoorWeekPermille: 120,
			RatingShares: delivery.ClimateRatingShares{Awful: 100, Poor: 100, Mixed: 100, Good: 400, VeryGood: 200, Excellent: 100},
		},
		Cycling: delivery.ClimateProfileDistribution{
			MedianScore: 50, CalmGoodPercent: 25, PoorWeekPermille: 220,
			RatingShares: delivery.ClimateRatingShares{Awful: 150, Poor: 200, Mixed: 200, Good: 300, VeryGood: 100, Excellent: 50},
		},
		Valid: true,
	}
}

func findScalar(t *testing.T, catalog climatetiles.Catalog, field climatetiles.Field, zoom uint8, x, y uint32) climatetiles.ScalarTile {
	t.Helper()
	for _, scalar := range catalog.ScalarTiles {
		if scalar.Field == field && scalar.Zoom == zoom && scalar.X == x && scalar.Y == y {
			return scalar
		}
	}
	t.Fatalf("missing scalar %s %d/%d/%d", field, zoom, x, y)
	return climatetiles.ScalarTile{}
}

func readScalar(t *testing.T, root string, scalar climatetiles.ScalarTile) []byte {
	t.Helper()
	encoded, _, err := tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(scalar.Path)), scalar.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func scalarGlobalValue(t *testing.T, root string, assets map[[3]uint32]climatetiles.ScalarTile, zoom uint8, globalX, globalY int64) (uint8, bool) {
	t.Helper()
	world := int64(tiledelivery.TileSize) * (int64(1) << zoom)
	if globalY < 0 || globalY >= world {
		return 0, false
	}
	globalX = wrapPixel(globalX, world)
	tileX, tileY := uint32(globalX/tiledelivery.TileSize), uint32(globalY/tiledelivery.TileSize)
	asset, ok := assets[[3]uint32{uint32(zoom), tileX, tileY}]
	if !ok {
		return 0, false
	}
	encoded := readScalar(t, root, asset)
	column := int(globalX%tiledelivery.TileSize) + tiledelivery.ScalarGutter
	row := int(globalY%tiledelivery.TileSize) + tiledelivery.ScalarGutter
	value := encoded[row*tiledelivery.ScalarTextureSize+column]
	return value, value != tiledelivery.ScalarNoData
}

func allEqual(values []uint8) bool {
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}
