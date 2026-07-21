package climatetiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

func TestMonthInstallDoesNotPublishAndPreviewIndexBindsExactRelease(t *testing.T) {
	artifacts := t.TempDir()
	mainPointer := filepath.Join(artifacts, "current")
	if err := os.WriteFile(mainPointer, []byte("main-release-sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	reference := installFixtureMonth(t, artifacts, 1, 61, fixtureProvenance())
	if _, err := os.Lstat(filepath.Join(artifacts, "climate", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("month install changed current pointer: %v", err)
	}

	published, err := BuildAndPublishIndex(context.Background(), artifacts, "typical-2006-2025-preview-01", IndexStatePreview, []MonthReference{reference})
	if err != nil {
		t.Fatal(err)
	}
	pointer, index, err := ValidateCurrentIndex(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if pointer != published || pointer.Version != PointerVersion || index.IndexID != pointer.IndexID || index.State != IndexStatePreview {
		t.Fatalf("current climate index = %+v, %+v; want %+v preview", pointer, index, published)
	}
	if index.ReferencePeriod != (ReferencePeriod{FirstYear: 2006, LastYear: 2025}) || index.Provenance != fixtureProvenance() || len(index.Months) != 1 || index.Months[0] != reference {
		t.Fatalf("preview index contract = %+v", index)
	}
	mainAfter, err := os.ReadFile(mainPointer)
	if err != nil || string(mainAfter) != "main-release-sentinel" {
		t.Fatalf("main artifacts/current changed: %q, %v", mainAfter, err)
	}
}

func TestValidateInstalledMonthFindsOnlyTheExactExpectedRelease(t *testing.T) {
	artifacts := t.TempDir()
	want := installFixtureMonth(t, artifacts, 7, 63, fixtureProvenance())

	got, catalog, found, err := ValidateInstalledMonth(artifacts, want.ReleaseID, want.Period)
	if err != nil || !found || got != want || catalog.ReleaseID != want.ReleaseID || catalog.Period != want.Period {
		t.Fatalf("reference=%+v catalog=%+v found=%v err=%v", got, catalog, found, err)
	}
	if _, _, found, err := ValidateInstalledMonth(artifacts, "climate-08", "month-08"); err != nil || found {
		t.Fatalf("missing release found=%v err=%v", found, err)
	}
	if _, _, _, err := ValidateInstalledMonth(artifacts, want.ReleaseID, "month-08"); err == nil || !strings.Contains(err.Error(), "expected release and period") {
		t.Fatalf("period mismatch error = %v", err)
	}
	if _, _, _, err := ValidateInstalledMonth(artifacts, "../escape", "month-07"); err == nil {
		t.Fatal("unsafe release identity unexpectedly accepted")
	}
}

func TestCompleteIndexRequiresAllMonthsAndCannotDowngradeToPreview(t *testing.T) {
	artifacts := t.TempDir()
	months := installFixtureYear(t, artifacts, fixtureProvenance())
	complete, err := BuildAndPublishIndex(context.Background(), artifacts, "typical-2006-2025", IndexStateComplete, months)
	if err != nil {
		t.Fatal(err)
	}
	pointerPath := filepath.Join(artifacts, "climate", "current.json")
	before, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublishIndex(context.Background(), artifacts, "later-preview", IndexStatePreview, months[:11]); err == nil || !strings.Contains(err.Error(), "complete climate index") {
		t.Fatalf("complete-to-preview error = %v", err)
	}
	after, err := os.ReadFile(pointerPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("downgrade attempt changed pointer: %q, %v", after, err)
	}
	pointer, index, err := ValidateCurrentIndex(artifacts)
	if err != nil || pointer != complete || index.State != IndexStateComplete || len(index.Months) != 12 {
		t.Fatalf("complete index = %+v, %+v, %v", pointer, index, err)
	}
}

func TestPreviewIndexCannotRegressForTheSameSource(t *testing.T) {
	artifacts := t.TempDir()
	first := installFixtureMonth(t, artifacts, 1, 61, fixtureProvenance())
	second := installFixtureMonth(t, artifacts, 2, 62, fixtureProvenance())
	published, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-two", IndexStatePreview, []MonthReference{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublishIndex(context.Background(), artifacts, "stale-preview-one", IndexStatePreview, []MonthReference{first}); err == nil || !strings.Contains(err.Error(), "smaller month set") {
		t.Fatalf("stale preview error = %v", err)
	}
	pointer, index, err := ValidateCurrentIndex(artifacts)
	if err != nil || pointer != published || len(index.Months) != 2 || index.Months[1] != second {
		t.Fatalf("current preview = %+v, %+v, %v", pointer, index, err)
	}
}

func TestIndexRejectsNonCanonicalOrInconsistentMonthSets(t *testing.T) {
	artifacts := t.TempDir()
	first := installFixtureMonth(t, artifacts, 1, 61, fixtureProvenance())
	second := installFixtureMonth(t, artifacts, 2, 62, fixtureProvenance())
	otherProvenance := fixtureProvenance()
	otherProvenance.SourceEvidenceSHA256 = digestString("different source evidence")
	third := installFixtureMonth(t, artifacts, 3, 63, otherProvenance)

	tests := []struct {
		name   string
		state  IndexState
		months []MonthReference
		want   string
	}{
		{"empty preview", IndexStatePreview, nil, "1 to 11"},
		{"short complete", IndexStateComplete, []MonthReference{first}, "exactly 12"},
		{"unknown state", IndexState("draft"), []MonthReference{first}, "state"},
		{"unordered", IndexStatePreview, []MonthReference{second, first}, "canonical order"},
		{"duplicate", IndexStatePreview, []MonthReference{first, first}, "canonical order"},
		{"wrong digest", IndexStatePreview, []MonthReference{{Period: first.Period, ReleaseID: first.ReleaseID, CatalogSHA256: strings.Repeat("0", 64)}}, "exact release"},
		{"wrong period", IndexStatePreview, []MonthReference{{Period: "month-02", ReleaseID: first.ReleaseID, CatalogSHA256: first.CatalogSHA256}}, "exact release"},
		{"mixed provenance", IndexStatePreview, []MonthReference{first, second, third}, "identical geometry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildAndPublishIndex(context.Background(), artifacts, "invalid-"+strings.ReplaceAll(test.name, " ", "-"), test.state, test.months); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildAndPublishIndex error = %v; want %q", err, test.want)
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(artifacts, "climate", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid indexes changed current pointer: %v", err)
	}
}

func TestIndexValidationRejectsCorruptionSymlinksExtraFilesAndReleaseDrift(t *testing.T) {
	t.Run("index checksum corruption", func(t *testing.T) {
		artifacts, pointer := previewFixture(t)
		indexPath := filepath.Join(artifacts, "climate", "indexes", pointer.IndexID+".json")
		payload, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatal(err)
		}
		payload[len(payload)/2] ^= 1
		if err := os.WriteFile(indexPath, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateCurrentIndex(artifacts); err == nil {
			t.Fatal("corrupt index unexpectedly validated")
		}
	})
	t.Run("index symlink", func(t *testing.T) {
		artifacts, pointer := previewFixture(t)
		indexPath := filepath.Join(artifacts, "climate", "indexes", pointer.IndexID+".json")
		target := filepath.Join(t.TempDir(), "index.json")
		payload, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(indexPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, indexPath); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateCurrentIndex(artifacts); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symlink index error = %v", err)
		}
	})
	t.Run("index path is a directory", func(t *testing.T) {
		artifacts, pointer := previewFixture(t)
		filename := filepath.Join(artifacts, "climate", "indexes", pointer.IndexID+".json")
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filename, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filename, "extra"), []byte("extra"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateCurrentIndex(artifacts); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("directory index error = %v", err)
		}
	})
	t.Run("referenced release drift", func(t *testing.T) {
		artifacts, _ := previewFixture(t)
		_, index, err := ValidateCurrentIndex(artifacts)
		if err != nil {
			t.Fatal(err)
		}
		catalogPath := filepath.Join(artifacts, "climate", "releases", index.Months[0].ReleaseID, CatalogFilename)
		file, err := os.OpenFile(catalogPath, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ValidateCurrentIndex(artifacts); err == nil {
			t.Fatal("mutated referenced release unexpectedly validated")
		}
	})
}

func TestMonthAndIndexPublicationAreIdempotentButRejectIdentityCollision(t *testing.T) {
	artifacts := t.TempDir()
	first := installFixtureMonth(t, artifacts, 4, 64, fixtureProvenance())
	again, err := BuildAndInstallMonth(context.Background(), artifacts, first.ReleaseID, fixtureBuilder(t, first.ReleaseID, first.Period, 64, fixtureProvenance()))
	if err != nil || again != first {
		t.Fatalf("idempotent month install = %+v, %v; want %+v", again, err, first)
	}
	if _, err := BuildAndInstallMonth(context.Background(), artifacts, first.ReleaseID, fixtureBuilder(t, first.ReleaseID, first.Period, 65, fixtureProvenance())); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("month identity collision error = %v", err)
	}

	pointer, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-04", IndexStatePreview, []MonthReference{first})
	if err != nil {
		t.Fatal(err)
	}
	againPointer, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-04", IndexStatePreview, []MonthReference{first})
	if err != nil || againPointer != pointer {
		t.Fatalf("idempotent index publication = %+v, %v; want %+v", againPointer, err, pointer)
	}
	if err := os.Remove(filepath.Join(artifacts, "climate", "current.json")); err != nil {
		t.Fatal(err)
	}
	adopted, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-04", IndexStatePreview, []MonthReference{first})
	if err != nil || adopted != pointer {
		t.Fatalf("orphan index adoption = %+v, %v; want %+v", adopted, err, pointer)
	}
	second := installFixtureMonth(t, artifacts, 5, 65, fixtureProvenance())
	if _, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-04", IndexStatePreview, []MonthReference{first, second}); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("index identity collision error = %v", err)
	}
}

func TestFailedMonthBuildAndInvalidCurrentLeavePointerUnchanged(t *testing.T) {
	artifacts, good := previewFixture(t)
	pointerPath := filepath.Join(artifacts, "climate", "current.json")
	before, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndInstallMonth(context.Background(), artifacts, "build-fails", func(context.Context, Stage) (Catalog, error) {
		return Catalog{}, errors.New("synthetic build failure")
	}); err == nil {
		t.Fatal("failed month build unexpectedly installed")
	}
	after, err := os.ReadFile(pointerPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("month failure changed pointer: %q, %v", after, err)
	}
	if err := os.WriteFile(pointerPath, []byte(`{"version":2,"indexId":"bad","indexSha256":"`+strings.Repeat("0", 64)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndPublishIndex(context.Background(), artifacts, "next", IndexStatePreview, goodIndexMonth(t, artifacts, good)); err == nil || !strings.Contains(err.Error(), "invalid current") {
		t.Fatalf("invalid current error = %v", err)
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
			catalog, err := fixtureBuilder(t, "release", "month-06", 60, fixtureProvenance())(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&catalog)
			overwriteCatalog(t, stage.ReleaseRoot, catalog)
			if _, err := ValidateRelease(stage.ReleaseRoot); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateRelease error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestValidateReleaseRejectsCorruptionSymlinksAndExtraEntries(t *testing.T) {
	t.Run("checksum corruption", func(t *testing.T) {
		root, catalog := completeFixtureRelease(t)
		asset := filepath.Join(root, filepath.FromSlash(catalog.ScalarTiles[0].Path))
		data, err := os.ReadFile(asset)
		if err != nil {
			t.Fatal(err)
		}
		data[len(data)/2] ^= 1
		if err := os.WriteFile(asset, data, 0o644); err != nil {
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
}

func TestPublicationRefusesUnsafeCleanupAndArtifactRoots(t *testing.T) {
	artifacts := t.TempDir()
	climateRoot, err := publicationRoot(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(climateRoot, ".build-unsafe-12345")
	if err := os.Symlink(t.TempDir(), unsafe); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndInstallMonth(context.Background(), artifacts, "blocked", fixtureBuilder(t, "blocked", "month-11", 60, fixtureProvenance())); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("unsafe cleanup error = %v", err)
	}

	parent := t.TempDir()
	missing := filepath.Join(parent, "missing", "artifacts")
	if _, err := BuildAndInstallMonth(context.Background(), missing, "blocked", fixtureBuilder(t, "blocked", "month-12", 60, fixtureProvenance())); err == nil || !strings.Contains(err.Error(), "existing real directory") {
		t.Fatalf("missing artifact root error = %v", err)
	}
	realRoot := t.TempDir()
	symlink := filepath.Join(parent, "artifact-link")
	if err := os.Symlink(realRoot, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAndInstallMonth(context.Background(), symlink, "blocked", fixtureBuilder(t, "blocked", "month-12", 60, fixtureProvenance())); err == nil || !strings.Contains(err.Error(), "existing real directory") {
		t.Fatalf("symlink artifact root error = %v", err)
	}
}

func previewFixture(t *testing.T) (string, Pointer) {
	t.Helper()
	artifacts := t.TempDir()
	reference := installFixtureMonth(t, artifacts, 11, 63, fixtureProvenance())
	pointer, err := BuildAndPublishIndex(context.Background(), artifacts, "preview-11", IndexStatePreview, []MonthReference{reference})
	if err != nil {
		t.Fatal(err)
	}
	return artifacts, pointer
}

func goodIndexMonth(t *testing.T, artifacts string, pointer Pointer) []MonthReference {
	t.Helper()
	_, index, err := ValidateCurrentIndex(artifacts)
	if err == nil {
		return index.Months
	}
	indexPath := filepath.Join(artifacts, "climate", "indexes", pointer.IndexID+".json")
	payload, readErr := os.ReadFile(indexPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if decodeErr := json.Unmarshal(payload, &index); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	return index.Months
}

func installFixtureYear(t *testing.T, artifacts string, provenance Provenance) []MonthReference {
	t.Helper()
	months := make([]MonthReference, 0, 12)
	for month := 1; month <= 12; month++ {
		months = append(months, installFixtureMonth(t, artifacts, month, byte(50+month), provenance))
	}
	return months
}

func installFixtureMonth(t *testing.T, artifacts string, month int, score byte, provenance Provenance) MonthReference {
	t.Helper()
	releaseID := "climate-" + fmtMonth(month)
	period := "month-" + fmtMonth(month)
	reference, err := BuildAndInstallMonth(context.Background(), artifacts, releaseID, fixtureBuilder(t, releaseID, period, score, provenance))
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func fmtMonth(month int) string {
	return fmt.Sprintf("%02d", month)
}

func completeFixtureRelease(t *testing.T) (string, Catalog) {
	t.Helper()
	stage := fixtureStage(t, "release")
	catalog, err := fixtureBuilder(t, "release", "month-09", 64, fixtureProvenance())(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(stage.ReleaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(stage.ReleaseRoot); err != nil {
		t.Fatal(err)
	}
	return stage.ReleaseRoot, catalog
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

func fixtureBuilder(t *testing.T, releaseID, period string, score byte, provenance Provenance) Builder {
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
		return NewCatalog(releaseID, period, provenance, []PointBlock{point}, tiles), nil
	}
}

func fixtureProvenance() Provenance {
	return Provenance{
		SourceEvidenceSHA256: digestString("AgERA5 source fixture"),
		LandMaskSetSHA256:    digestString("Natural Earth land-mask-set fixture"),
		DerivationContract:   climatebuild.CheckpointDerivationContract,
	}
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
