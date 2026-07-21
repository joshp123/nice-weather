package climatetiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

func TestBuildAndPublishInstallsIndependentImmutableClimateRelease(t *testing.T) {
	artifacts := t.TempDir()
	mainPointer := filepath.Join(artifacts, "current")
	if err := os.WriteFile(mainPointer, []byte("main-release-sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := BuildAndPublish(context.Background(), artifacts, "climate-2026-01", fixtureBuilder(t, "climate-2026-01", "month-01", 61))
	if err != nil {
		t.Fatal(err)
	}
	pointer, catalog, err := ValidateCurrent(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if pointer != first || catalog.ReleaseID != first.ReleaseID || catalog.Period != "month-01" {
		t.Fatalf("current climate publication = %+v, %+v; want %+v", pointer, catalog, first)
	}
	if catalog.Geometry != NewCatalog(catalog.ReleaseID, catalog.Period, catalog.Provenance, catalog.PointBlocks, catalog.ScalarTiles).Geometry || catalog.Source.Dataset != SourceDataset || catalog.Source.ReferencePeriod != (ReferencePeriod{FirstYear: 2006, LastYear: 2025}) {
		t.Fatalf("catalog is not explicit about worldwide geometry and source: %+v %+v", catalog.Geometry, catalog.Source)
	}
	if catalog.Probability.GoodMinimumScore != 55 || catalog.Probability.NoDataValue != 255 || len(catalog.Probability.Fields) != 7 {
		t.Fatalf("catalog probability semantics = %+v", catalog.Probability)
	}
	firstCatalogPath := filepath.Join(artifacts, "climate", "releases", first.ReleaseID, CatalogFilename)
	firstCatalogBefore, err := os.ReadFile(firstCatalogPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := BuildAndPublish(context.Background(), artifacts, "climate-2026-02", fixtureBuilder(t, "climate-2026-02", "month-02", 72))
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("second climate publication did not change the pointer")
	}
	firstCatalogAfter, err := os.ReadFile(firstCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstCatalogAfter) != string(firstCatalogBefore) {
		t.Fatal("previous immutable climate release changed")
	}
	mainAfter, err := os.ReadFile(mainPointer)
	if err != nil || string(mainAfter) != "main-release-sentinel" {
		t.Fatalf("main artifacts/current changed: %q, %v", mainAfter, err)
	}
	entries, err := os.ReadDir(filepath.Join(artifacts, "climate"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".build-") || strings.HasPrefix(entry.Name(), ".current-") {
			t.Fatalf("publication left private staging entry %q", entry.Name())
		}
	}
}

func TestFailedBuildAndValidationLeaveBothPointersUnchanged(t *testing.T) {
	artifacts := t.TempDir()
	mainPointer := filepath.Join(artifacts, "current")
	if err := os.WriteFile(mainPointer, []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	good, err := BuildAndPublish(context.Background(), artifacts, "good", fixtureBuilder(t, "good", "month-03", 55))
	if err != nil {
		t.Fatal(err)
	}
	climatePointer := filepath.Join(artifacts, "climate", "current.json")
	before, err := os.ReadFile(climatePointer)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := BuildAndPublish(context.Background(), artifacts, "build-fails", func(context.Context, Stage) (Catalog, error) {
		return Catalog{}, errors.New("synthetic build failure")
	}); err == nil {
		t.Fatal("failed build unexpectedly published")
	}
	if _, err := BuildAndPublish(context.Background(), artifacts, "invalid", func(ctx context.Context, stage Stage) (Catalog, error) {
		catalog, err := fixtureBuilder(t, "invalid", "month-04", 55)(ctx, stage)
		catalog.ScalarTiles = catalog.ScalarTiles[:len(catalog.ScalarTiles)-1]
		return catalog, err
	}); err == nil {
		t.Fatal("invalid release unexpectedly published")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	if _, err := BuildAndPublish(cancelled, artifacts, "cancelled", func(ctx context.Context, stage Stage) (Catalog, error) {
		catalog, err := fixtureBuilder(t, "cancelled", "month-05", 55)(ctx, stage)
		cancel()
		return catalog, err
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication error = %v", err)
	}

	after, err := os.ReadFile(climatePointer)
	if err != nil || string(after) != string(before) {
		t.Fatalf("climate/current changed after failure: %q, %v", after, err)
	}
	mainAfter, err := os.ReadFile(mainPointer)
	if err != nil || string(mainAfter) != "main" {
		t.Fatalf("main current changed after failure: %q, %v", mainAfter, err)
	}
	pointer, _, err := ValidateCurrent(artifacts)
	if err != nil || pointer != good {
		t.Fatalf("current climate release = %+v, %v; want %+v", pointer, err, good)
	}
	for _, releaseID := range []string{"build-fails", "invalid", "cancelled"} {
		if _, err := os.Lstat(filepath.Join(artifacts, "climate", "releases", releaseID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed release %q was installed: %v", releaseID, err)
		}
	}
}

func TestValidateReleaseRejectsUnsafeIncompleteOrDishonestCatalog(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*Catalog)
		want   string
	}{
		{"unsafe point path", func(c *Catalog) { c.PointBlocks[0].Path = "../escape.nwc" }, "point block"},
		{"wrong source", func(c *Catalog) { c.Source.Kind = "native-0.1-degree-atmosphere" }, "geometry, source or probability"},
		{"wrong probability", func(c *Catalog) { c.Probability.Denominator = "monthly-mean-day" }, "geometry, source or probability"},
		{"wrong provenance", func(c *Catalog) { c.Provenance.LandMaskSetSHA256 = strings.Repeat("0", 64) }, "checkpoint provenance"},
		{"missing field tile", func(c *Catalog) { c.ScalarTiles = c.ScalarTiles[:len(c.ScalarTiles)-1] }, "complete field/zoom coverage"},
		{"missing zoom", func(c *Catalog) {
			filtered := c.ScalarTiles[:0]
			for _, tile := range c.ScalarTiles {
				if tile.Zoom != MaximumZoom {
					filtered = append(filtered, tile)
				}
			}
			c.ScalarTiles = filtered
		}, "no zoom 4 tiles"},
		{"wrong size", func(c *Catalog) { c.ScalarTiles[0].Bytes++ }, "metadata is invalid"},
		{"wrong media type", func(c *Catalog) { c.PointBlocks[0].MediaType = "application/octet-stream" }, "metadata is invalid"},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			stage := fixtureStage(t, "release")
			root := stage.ReleaseRoot
			catalog, err := fixtureBuilder(t, "release", "month-06", 60)(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&catalog)
			overwriteCatalog(t, root, catalog)
			if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateRelease error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestValidateReleaseRejectsCorruptionSymlinksAndExtraEntries(t *testing.T) {
	t.Run("checksum corruption", func(t *testing.T) {
		root, catalog := completeFixtureRelease(t)
		path := filepath.Join(root, filepath.FromSlash(catalog.ScalarTiles[0].Path))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)/2] ^= 0x01
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("corrupt scalar error = %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root, catalog := completeFixtureRelease(t)
		asset := filepath.Join(root, filepath.FromSlash(catalog.ScalarTiles[0].Path))
		target := filepath.Join(t.TempDir(), "outside.nws")
		data, err := os.ReadFile(asset)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(asset); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, asset); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink error = %v", err)
		}
	})
	t.Run("extra file", func(t *testing.T) {
		root, _ := completeFixtureRelease(t)
		if err := os.WriteFile(filepath.Join(root, "unlisted"), []byte("extra"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "missing or extra") {
			t.Fatalf("extra file error = %v", err)
		}
	})
	t.Run("extra directory", func(t *testing.T) {
		root, _ := completeFixtureRelease(t)
		if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "missing or extra") {
			t.Fatalf("extra directory error = %v", err)
		}
	})
}

func TestPublicationRejectsExistingReleaseAndInvalidCurrent(t *testing.T) {
	artifacts := t.TempDir()
	if _, err := BuildAndPublish(context.Background(), artifacts, "same", fixtureBuilder(t, "same", "month-07", 50)); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublish(context.Background(), artifacts, "same", fixtureBuilder(t, "same", "month-07", 51)); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("existing release error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "climate", "current.json"), []byte(`{"releaseId":"same","catalogSha256":"`+strings.Repeat("0", 64)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublish(context.Background(), artifacts, "next", fixtureBuilder(t, "next", "month-08", 51)); err == nil || !strings.Contains(err.Error(), "invalid current") {
		t.Fatalf("invalid current error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(artifacts, "climate", "releases", "next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release installed over invalid current: %v", err)
	}
}

func TestPublicationAdoptsIdenticalOrphanAndCleansSafePrivateEntries(t *testing.T) {
	artifacts := t.TempDir()
	climateRoot, err := publicationRoot(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	orphanStage := fixtureStage(t, "orphan")
	catalog, err := fixtureBuilder(t, "orphan", "month-10", 63)(context.Background(), orphanStage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(orphanStage.ReleaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	orphanTarget := filepath.Join(climateRoot, "releases", "orphan")
	if err := os.Rename(orphanStage.ReleaseRoot, orphanTarget); err != nil {
		t.Fatal(err)
	}
	staleBuild := filepath.Join(climateRoot, ".build-stale-12345")
	if err := os.Mkdir(staleBuild, 0o755); err != nil {
		t.Fatal(err)
	}
	stalePointer := filepath.Join(climateRoot, ".current-0123456789abcdef01234567.json")
	if err := os.WriteFile(stalePointer, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	pointer, err := BuildAndPublish(context.Background(), artifacts, "orphan", fixtureBuilder(t, "orphan", "month-10", 63))
	if err != nil {
		t.Fatal(err)
	}
	if pointer.ReleaseID != "orphan" {
		t.Fatalf("adopted pointer = %+v", pointer)
	}
	for _, stale := range []string{staleBuild, stalePointer} {
		if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale private entry remains at %s: %v", stale, err)
		}
	}
	if _, _, err := ValidateCurrent(artifacts); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationRefusesUnsafePrivateStageCleanup(t *testing.T) {
	artifacts := t.TempDir()
	climateRoot, err := publicationRoot(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	unsafe := filepath.Join(climateRoot, ".build-unsafe-12345")
	if err := os.Symlink(outside, unsafe); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublish(context.Background(), artifacts, "blocked", fixtureBuilder(t, "blocked", "month-11", 60)); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("unsafe cleanup error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(climateRoot, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe cleanup changed current pointer: %v", err)
	}
}

func TestPublicationRequiresExistingRealArtifactRoot(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "missing", "artifacts")
	if _, err := BuildAndPublish(context.Background(), missing, "blocked", fixtureBuilder(t, "blocked", "month-12", 60)); err == nil || !strings.Contains(err.Error(), "existing real directory") {
		t.Fatalf("missing artifact root error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication created missing artifact parents: %v", err)
	}
	realRoot := t.TempDir()
	symlink := filepath.Join(parent, "artifact-link")
	if err := os.Symlink(realRoot, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublish(context.Background(), symlink, "blocked", fixtureBuilder(t, "blocked", "month-12", 60)); err == nil || !strings.Contains(err.Error(), "existing real directory") {
		t.Fatalf("symlink artifact root error = %v", err)
	}
}

func completeFixtureRelease(t *testing.T) (string, Catalog) {
	t.Helper()
	stage := fixtureStage(t, "release")
	root := stage.ReleaseRoot
	catalog, err := fixtureBuilder(t, "release", "month-09", 64)(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(root, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(root); err != nil {
		t.Fatal(err)
	}
	return root, catalog
}

func fixtureStage(t *testing.T, releaseID string) Stage {
	t.Helper()
	root := t.TempDir()
	releaseRoot := filepath.Join(root, "releases", releaseID)
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return Stage{Root: root, ReleaseRoot: releaseRoot, ReleaseID: releaseID}
}

func fixtureBuilder(t *testing.T, releaseID, period string, score byte) Builder {
	t.Helper()
	return func(ctx context.Context, stage Stage) (Catalog, error) {
		if err := ctx.Err(); err != nil {
			return Catalog{}, err
		}
		if stage.ReleaseID != releaseID {
			return Catalog{}, errors.New("fixture stage release mismatch")
		}
		blockCoordinate := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
		identity, err := tiledelivery.NewClimateBlockIdentity(releaseID, period, blockCoordinate)
		if err != nil {
			return Catalog{}, err
		}
		records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
		for index := range records {
			records[index] = validClimateRecord()
		}
		pointPath := PointBlockPath(period, blockCoordinate)
		pointAsset, err := tiledelivery.WriteClimateBlock(filepath.Join(stage.ReleaseRoot, filepath.FromSlash(pointPath)), releaseID, period, blockCoordinate, records)
		if err != nil {
			return Catalog{}, err
		}
		point, err := PointBlockFromAsset(stage.ReleaseRoot, pointAsset)
		if err != nil {
			return Catalog{}, err
		}
		points := []PointBlock{point}

		tiles := make([]ScalarTile, 0, len(Fields())*(MaximumZoom+1))
		writer, err := tiledelivery.NewWriter(stage.Root, releaseID)
		if err != nil {
			return Catalog{}, err
		}
		for _, field := range Fields() {
			for zoom := uint8(0); zoom <= MaximumZoom; zoom++ {
				id, err := tiledelivery.NewID(string(field), zoom, 0, 0)
				if err != nil {
					return Catalog{}, err
				}
				asset, err := writer.WriteScalar(id, period, func(tiledelivery.ScalarSample) (uint8, bool, error) {
					return score, true, nil
				})
				if err != nil {
					return Catalog{}, err
				}
				tile, err := ScalarTileFromAsset(stage.ReleaseRoot, field, asset)
				if err != nil {
					return Catalog{}, err
				}
				tiles = append(tiles, tile)
			}
		}
		return NewCatalog(releaseID, period, fixtureProvenance(), points, tiles), nil
	}
}

func fixtureProvenance() Provenance {
	source := sha256.Sum256([]byte("AgERA5 source fixture"))
	land := sha256.Sum256([]byte("Natural Earth land-mask-set fixture"))
	return Provenance{
		SourceEvidenceSHA256: hex.EncodeToString(source[:]),
		LandMaskSetSHA256:    hex.EncodeToString(land[:]),
		DerivationContract:   climatebuild.CheckpointDerivationContract,
	}
}

func validClimateRecord() delivery.ClimateDistributionRecord {
	profile := delivery.ClimateProfileDistribution{
		MedianScore: 65, CalmGoodPercent: 75, PoorWeekPermille: 125,
		RatingShares: delivery.ClimateRatingShares{Awful: 50, Poor: 100, Mixed: 200, Good: 300, VeryGood: 250, Excellent: 100},
	}
	cycling := profile
	cycling.MedianScore = 58
	cycling.CalmGoodPercent = 60
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: 70, DryGoodPercent: 80, BrightGoodPercent: 65,
		Nice: profile, Cycling: cycling, Valid: true,
	}
}

func overwriteCatalog(t *testing.T, root string, catalog Catalog) {
	t.Helper()
	payload, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CatalogFilename), payload, 0o644); err != nil {
		t.Fatal(err)
	}
}
