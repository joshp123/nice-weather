package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishReleaseMaintainsAtomicCurrentAndPrevious(t *testing.T) {
	root := t.TempDir()
	first := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, first); err != nil {
		t.Fatal(err)
	}
	assertPublished(t, root, "current", "001")
	if _, ok := PreviousDir(root); ok {
		t.Fatal("first publish unexpectedly created previous")
	}

	second := makeRelease(t, root, "002", 250)
	if err := PublishRelease(root, second); err != nil {
		t.Fatal(err)
	}
	assertPublished(t, root, "current", "002")
	assertPublished(t, root, "previous", "001")

	broken, err := BeginRelease(root, "003")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, manifestFilename), []byte(`{"broken":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishRelease(root, broken); err == nil {
		t.Fatal("invalid release was published")
	}
	assertPublished(t, root, "current", "002")
	assertPublished(t, root, "previous", "001")
}

func TestValidateReleaseChecksManifestPointAssetAndImages(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	if _, err := ValidateRelease(release); err != nil {
		t.Fatal(err)
	}
	manifest, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	point := filepath.Join(release, filepath.FromSlash(manifest.Forecast.Days[0].Point.URL))
	data, err := os.ReadFile(point)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(point, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("release with corrupt point asset was accepted")
	}
	release = makeRelease(t, root, "002", 210)
	manifest, err = ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	display := filepath.Join(release, filepath.FromSlash(manifest.Forecast.Days[5].DisplayPoint.URL))
	data, err = os.ReadFile(display)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(display, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("release with corrupt display-score asset was accepted")
	}
}

func TestReleaseCatalogOpensValidatedGridsForRepeatedLookup(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	catalog, err := OpenReleaseCatalog(release)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	for attempt := 0; attempt < 3; attempt++ {
		record, ok, err := catalog.Lookup("2026-07-18", 5, 50)
		if err != nil || !ok || record.ComfortableTemperatureDeciC != 210 {
			t.Fatalf("lookup = (%+v, %v, %v)", record, ok, err)
		}
	}
	if _, _, err := catalog.Lookup("missing", 5, 50); err == nil {
		t.Fatal("unknown period was accepted")
	}
	for _, profile := range []string{ProfileNice, ProfileCycling} {
		record, ok, err := catalog.LookupRange(profile, 5, 50)
		if err != nil || !ok || record.DayCount != ForecastPointDayCount || record.Score != 70 {
			t.Fatalf("range lookup %s = (%+v, %v, %v)", profile, record, ok, err)
		}
	}
	if _, _, err := catalog.LookupRange("missing", 5, 50); err == nil {
		t.Fatal("unknown range profile was accepted")
	}
}

func TestValidateReleaseRejectsForecastWindowCorruptionAndWrongDayCount(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	manifest, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	pointPath := filepath.Join(release, filepath.FromSlash(manifest.Forecast.Window.Point.URL))
	data, err := os.ReadFile(pointPath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(pointPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("release with corrupt forecast window point asset was accepted")
	}
	release = makeRelease(t, root, "002", 210)
	manifest, err = ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	falseGeometry, err := NewPointGeometry(GridSpec{Bounds: manifest.Region.Bounds, Rows: 1, Cols: 2})
	if err != nil {
		t.Fatal(err)
	}
	falseRecords := make([]RangePointRecord, int(falseGeometry.Rows)*int(falseGeometry.Cols))
	for index := range falseRecords {
		falseRecords[index] = testRangePointRecord(70)
	}
	temporary := filepath.Join(release, "points", ".false-geometry.nwp")
	metadata, err := WriteRangePointAsset(temporary, manifest.ReleaseID, falseGeometry, falseRecords)
	if err != nil {
		t.Fatal(err)
	}
	pointURL := filepath.ToSlash(filepath.Join("points", "false-geometry-"+metadata.SHA256+".nwp"))
	if err := os.Rename(temporary, filepath.Join(release, filepath.FromSlash(pointURL))); err != nil {
		t.Fatal(err)
	}
	manifest.Forecast.Window.Point = PointAssetFromMetadata(pointURL, metadata)
	for profile, asset := range manifest.Forecast.Window.Profiles {
		asset.Image = writeTestPNGSize(t, filepath.Join(release, "images", "false-geometry-"+profile+".png"), int(falseGeometry.Cols), int(falseGeometry.Rows))
		manifest.Forecast.Window.Profiles[profile] = asset
	}
	for lens := range manifest.Forecast.Window.Lenses {
		manifest.Forecast.Window.Lenses[lens] = writeTestPNGSize(t, filepath.Join(release, "images", "false-geometry-range-"+lens+".png"), int(falseGeometry.Cols), int(falseGeometry.Rows))
	}
	if err := WriteManifest(release, manifest); err == nil || !strings.Contains(err.Error(), "geometry must match every outlook display plane") {
		t.Fatalf("self-consistent false-resolution range geometry was accepted: %v", err)
	}
}

func TestValidateReleaseChecksPNGChecksumAndDimensions(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	manifest, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	asset := writeTestPNGSize(t, filepath.Join(release, "images", "wrong-declared-size.png"), 2, 1)
	asset.Width = int(manifest.Forecast.Days[0].Point.Geometry.Cols)
	asset.Height = int(manifest.Forecast.Days[0].Point.Geometry.Rows)
	manifest.Forecast.Days[0].Images["nice"] = asset
	if err := WriteManifest(release, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("wrong PNG dimensions were accepted")
	}

	release = makeRelease(t, root, "002", 210)
	imagePath := filepath.Join(release, "images", "nice.png")
	file, err := os.OpenFile(imagePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("wrong PNG checksum was accepted")
	}

	release = makeRelease(t, root, "003", 210)
	manifest, err = ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	imagePath = filepath.Join(release, filepath.FromSlash(manifest.Forecast.Window.Lenses[LensComfort].URL))
	file, err = os.OpenFile(imagePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("wrong forecast-window lens checksum was accepted")
	}
}

func TestCatalogRequiresSharedGridImageAndRegionGeometry(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	manifest, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Region.Bounds.East++
	if err := WriteManifest(release, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReleaseCatalog(release); err == nil {
		t.Fatal("region/grid bounds mismatch was accepted")
	}

	release = makeRelease(t, root, "002", 210)
	manifest, err = ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(release, "images", "nice.png")
	asset := writeTestPNGSize(t, path, 2, 1)
	manifest.Forecast.Days[0].Images["nice"] = asset
	if err := WriteManifest(release, manifest); err == nil || !strings.Contains(err.Error(), "geometry does not match display authority") {
		t.Fatalf("image/point dimensions mismatch was accepted or misclassified: %v", err)
	}
}

func TestCatalogRejectsReleaseDirectorySymlinkAndNonCanonicalRoot(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	link := filepath.Join(root, "release-link")
	if err := os.Symlink(release, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReleaseCatalog(link); err == nil {
		t.Fatal("release directory symlink was accepted")
	}
	outside := filepath.Join(t.TempDir(), "001")
	if err := os.Rename(release, outside); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReleaseCatalog(outside); err == nil {
		t.Fatal("release outside canonical releases root was accepted")
	}
}

func TestCleanupStaleReleasesRetainsPublishedPair(t *testing.T) {
	root := t.TempDir()
	first := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, first); err != nil {
		t.Fatal(err)
	}
	second := makeRelease(t, root, "002", 220)
	if err := PublishRelease(root, second); err != nil {
		t.Fatal(err)
	}
	_ = makeRelease(t, root, "003", 230)
	if err := os.Symlink("releases/003", filepath.Join(root, ".current-next")); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaleReleases(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001", "002"} {
		if _, err := os.Stat(filepath.Join(root, "releases", name)); err != nil {
			t.Fatalf("retained release %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "releases", "003")); !os.IsNotExist(err) {
		t.Fatal("stale release was retained")
	}
	if _, err := os.Lstat(filepath.Join(root, ".current-next")); !os.IsNotExist(err) {
		t.Fatal("staging pointer was retained")
	}
}

func TestCleanupLeavesAnotherWritersIncompleteRelease(t *testing.T) {
	root := t.TempDir()
	current := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, current); err != nil {
		t.Fatal(err)
	}
	inProgress, err := BeginRelease(root, "building")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inProgress, "download.part"), []byte("still writing"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := makeRelease(t, root, "stale", 220)

	if err := CleanupStaleReleases(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inProgress); err != nil {
		t.Fatalf("in-progress release was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inProgress, "download.part")); err != nil {
		t.Fatalf("in-progress contents were removed: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("complete unreferenced release was retained")
	}
	if _, err := ValidateRelease(current); err != nil {
		t.Fatalf("published release was damaged: %v", err)
	}
}

func TestCleanupAbortsWithoutUsablePointer(t *testing.T) {
	root := t.TempDir()
	stale := makeRelease(t, root, "001", 210)
	if err := CleanupStaleReleases(root); err == nil {
		t.Fatal("cleanup proceeded without a usable publication")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("refused cleanup removed a release")
	}
	if err := os.Symlink("releases/missing", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaleReleases(root); err == nil {
		t.Fatal("cleanup proceeded with only a broken publication")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("refused cleanup removed a release")
	}
}

func TestCleanupKeepsUsablePreviousWhenCurrentIsBroken(t *testing.T) {
	root := t.TempDir()
	first := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, first); err != nil {
		t.Fatal(err)
	}
	second := makeRelease(t, root, "002", 220)
	if err := PublishRelease(root, second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, manifestFilename), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaleReleases(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("usable previous release was removed")
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal("invalid release directory should be left for its owner")
	}
	currentTarget, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	previousTarget, err := os.Readlink(filepath.Join(root, "previous"))
	if err != nil {
		t.Fatal(err)
	}
	if currentTarget != "releases/001" || previousTarget != "releases/001" {
		t.Fatalf("recovered pointers = current %q, previous %q", currentTarget, previousTarget)
	}
	for _, pointer := range []string{"current", "previous"} {
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, pointer))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(resolved); err != nil {
			t.Fatalf("%s is not valid after recovery: %v", pointer, err)
		}
	}

	third := makeRelease(t, root, "003", 230)
	if err := PublishRelease(root, third); err != nil {
		t.Fatal(err)
	}
	previousTarget, err = os.Readlink(filepath.Join(root, "previous"))
	if err != nil {
		t.Fatal(err)
	}
	if previousTarget != "releases/001" {
		t.Fatalf("next publish lost last good release: previous = %q", previousTarget)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, "previous"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(resolved); err != nil {
		t.Fatalf("previous after next publish is invalid: %v", err)
	}
}

func TestPublishReplacesUnsafeExistingPointerWithoutRotatingIt(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("../outside", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	release := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}
	target, _ := os.Readlink(filepath.Join(root, "current"))
	if target != "releases/001" {
		t.Fatalf("current target = %q", target)
	}
	if _, err := os.Lstat(filepath.Join(root, "previous")); !os.IsNotExist(err) {
		t.Fatal("unsafe current was rotated into previous")
	}
}

func TestPublishPreservesLastGoodPreviousWhenCurrentIsCorrupt(t *testing.T) {
	root := t.TempDir()
	first := makeRelease(t, root, "001", 210)
	if err := PublishRelease(root, first); err != nil {
		t.Fatal(err)
	}
	second := makeRelease(t, root, "002", 220)
	if err := PublishRelease(root, second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, manifestFilename), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	third := makeRelease(t, root, "003", 230)
	if err := PublishRelease(root, third); err != nil {
		t.Fatal(err)
	}
	currentTarget, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	previousTarget, err := os.Readlink(filepath.Join(root, "previous"))
	if err != nil {
		t.Fatal(err)
	}
	if currentTarget != "releases/003" || previousTarget != "releases/001" {
		t.Fatalf("publish recovery pointers = current %q, previous %q", currentTarget, previousTarget)
	}
	for _, pointer := range []string{"current", "previous"} {
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, pointer))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRelease(resolved); err != nil {
			t.Fatalf("%s after recovery publish is invalid: %v", pointer, err)
		}
	}
}

func TestValidateReleaseRejectsSymlinkAssets(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	images := filepath.Join(release, "images")
	if err := os.Rename(images, filepath.Join(release, "real-images")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real-images", images); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("release with symlink asset directory was accepted")
	}
}

func TestBeginReleaseRejectsReuseAndUnsafeNames(t *testing.T) {
	root := t.TempDir()
	if _, err := BeginRelease(root, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginRelease(root, "one"); err == nil {
		t.Fatal("existing release was silently reused")
	}
	for _, name := range []string{"", ".", "..", "..hidden", "../escape", "nested/release", "white space"} {
		if _, err := BeginRelease(root, name); err == nil {
			t.Errorf("unsafe release name %q was accepted", name)
		}
	}
}

func TestValidateReleaseBindsManifestToDirectory(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "001", 210)
	manifest, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ReleaseID = "002"
	if err := WriteManifest(release, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(release); err == nil {
		t.Fatal("release whose manifest names a different release directory was accepted")
	}
}

func makeRelease(t *testing.T, root, name string, temperature int16) string {
	t.Helper()
	release, err := BeginRelease(root, name)
	if err != nil {
		t.Fatal(err)
	}
	sourceSpec := GridSpec{Bounds: GridBounds{West: -25, East: 45, South: 30, North: 72}, Rows: 1, Cols: 1}
	dailyGeometry, dailyRecords, err := DailyPointRecords(sourceSpec, []DailyRecord{record(temperature)})
	if err != nil {
		t.Fatal(err)
	}
	dailyTemporary := filepath.Join(release, "points", ".day.nwp")
	dailyMeta, err := WriteDailyPointAsset(dailyTemporary, name, PointKindForecastDay, dailyGeometry, dailyRecords)
	if err != nil {
		t.Fatal(err)
	}
	dailyURL := filepath.ToSlash(filepath.Join("points", "day-"+dailyMeta.SHA256+".nwp"))
	if err := os.Rename(dailyTemporary, filepath.Join(release, filepath.FromSlash(dailyURL))); err != nil {
		t.Fatal(err)
	}
	assets := make(map[string]ImageAsset)
	for _, profile := range []string{"nice", "cycling"} {
		path := filepath.Join(release, "images", profile+".png")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		assets[profile] = writeTestPNGSize(t, path, int(dailyGeometry.Cols), int(dailyGeometry.Rows))
	}
	lenses := make(map[string]ImageAsset)
	for _, lens := range RequiredLensAssets() {
		path := filepath.Join(release, "images", lens+".png")
		lenses[lens] = writeTestPNGSize(t, path, int(dailyGeometry.Cols), int(dailyGeometry.Rows))
	}
	manifest := validManifest(dailyMeta.SHA256)
	manifest.ReleaseID = name
	manifest.Climate = nil
	displayRecords := make([]DisplayScoreRecord, len(dailyRecords))
	for index, record := range dailyRecords {
		if record.Weather.Valid {
			displayRecords[index] = DisplayScoreRecord{Thermal: record.Thermal, Dryness: record.Dryness, Sun: record.Sun, NiceScore: record.NiceScore, NiceWind: record.NiceWind, CyclingScore: record.CyclingScore, CyclingWind: record.CyclingWind, Valid: true}
		}
	}
	displayTemporary := filepath.Join(release, "points", ".display.nwp")
	displayMeta, err := WriteDisplayScoreAsset(displayTemporary, name, dailyGeometry, displayRecords)
	if err != nil {
		t.Fatal(err)
	}
	displayURL := filepath.ToSlash(filepath.Join("points", "display-"+displayMeta.SHA256+".nwp"))
	if err := os.Rename(displayTemporary, filepath.Join(release, filepath.FromSlash(displayURL))); err != nil {
		t.Fatal(err)
	}
	displayAsset := PointAssetFromMetadata(displayURL, displayMeta)
	for dayIndex := range manifest.Forecast.Days {
		manifest.Forecast.Days[dayIndex].Images = assets
		manifest.Forecast.Days[dayIndex].Lenses = lenses
		manifest.Forecast.Days[dayIndex].Point = PointAssetFromMetadata(dailyURL, dailyMeta)
		if manifest.Forecast.Days[dayIndex].Kind == "outlook" {
			manifest.Forecast.Days[dayIndex].DisplayPoint = &displayAsset
		}
		for frameIndex := range manifest.Forecast.Days[dayIndex].WindFrames {
			temporary := filepath.Join(release, "wind", ".frame-"+string(rune('a'+dayIndex))+string(rune('a'+frameIndex))+".nww.gz")
			windMeta, err := WriteWindGridGzip(temporary, sourceSpec, []WindVector{{U: float32(frameIndex + 2), V: -1, Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(release, "wind", "frame-"+string(rune('a'+dayIndex))+string(rune('a'+frameIndex))+"-"+windMeta.SHA256+".nww.gz")
			if err := os.Rename(temporary, path); err != nil {
				t.Fatal(err)
			}
			manifest.Forecast.Days[dayIndex].WindFrames[frameIndex].URL = filepath.ToSlash(strings.TrimPrefix(path, release+string(filepath.Separator)))
			manifest.Forecast.Days[dayIndex].WindFrames[frameIndex].SHA256 = windMeta.SHA256
			manifest.Forecast.Days[dayIndex].WindFrames[frameIndex].ClippedComponents = windMeta.ClippedComponents
		}
	}
	manifest.Forecast.Window.Profiles = make(map[string]ForecastRangeAsset, 2)
	manifest.Forecast.Window.Lenses = make(map[string]ImageAsset, len(RequiredLensAssets()))
	rangeRecord := testRangePointRecord(70)
	rangeRecords := make([]RangePointRecord, int(dailyGeometry.Rows)*int(dailyGeometry.Cols))
	for index := range rangeRecords {
		rangeRecords[index] = rangeRecord
	}
	rangeTemporary := filepath.Join(release, "points", ".range.nwp")
	rangeMeta, err := WriteRangePointAsset(rangeTemporary, name, dailyGeometry, rangeRecords)
	if err != nil {
		t.Fatal(err)
	}
	rangeURL := filepath.ToSlash(filepath.Join("points", "range-"+rangeMeta.SHA256+".nwp"))
	if err := os.Rename(rangeTemporary, filepath.Join(release, filepath.FromSlash(rangeURL))); err != nil {
		t.Fatal(err)
	}
	manifest.Forecast.Window.Point = PointAssetFromMetadata(rangeURL, rangeMeta)
	for _, profile := range []string{ProfileNice, ProfileCycling} {
		imagePath := filepath.Join(release, "images", "range-"+profile+".png")
		manifest.Forecast.Window.Profiles[profile] = ForecastRangeAsset{
			Image: writeTestPNGSize(t, imagePath, int(dailyGeometry.Cols), int(dailyGeometry.Rows)),
		}
	}
	for _, lens := range RequiredLensAssets() {
		imagePath := filepath.Join(release, "images", "range-"+lens+".png")
		manifest.Forecast.Window.Lenses[lens] = writeTestPNGSize(t, imagePath, int(dailyGeometry.Cols), int(dailyGeometry.Rows))
	}
	if err := WriteManifest(release, manifest); err != nil {
		t.Fatal(err)
	}
	return release
}

func testRangePointRecord(score uint8) RangePointRecord {
	record := RangePointRecord{
		Nice:    RangeRecord{Score: score, MedianScore: score, Thermal: score, Dryness: score, Sun: score, Wind: score, NiceDays: ForecastPointDayCount, DayCount: ForecastPointDayCount, Valid: true},
		Cycling: RangeRecord{Score: score, MedianScore: score, Thermal: score, Dryness: score, Sun: score, Wind: score, NiceDays: ForecastPointDayCount, DayCount: ForecastPointDayCount, Valid: true},
	}
	for index := range record.NiceDays {
		record.NiceDays[index], record.CyclingDays[index] = score, score
	}
	return record
}

func writeTestPNG(t *testing.T, path string) ImageAsset {
	return writeTestPNGSize(t, path, 1, 1)
}

func writeTestPNGSize(t *testing.T, path string, width, height int) ImageAsset {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return ImageAsset{
		URL:    filepath.ToSlash(strings.TrimPrefix(path, filepath.Dir(filepath.Dir(path))+string(filepath.Separator))),
		SHA256: hex.EncodeToString(digest[:]), Width: width, Height: height, Projection: ImageProjectionWebMercator,
	}
}

func assertPublished(t *testing.T, root, pointer, want string) {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, pointer))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(target) != want {
		t.Fatalf("%s target = %q; want release %q", pointer, target, want)
	}
	path, ok := CurrentDir(root)
	if pointer == "previous" {
		path, ok = PreviousDir(root)
	}
	if !ok || !strings.HasSuffix(path, pointer) {
		t.Fatalf("%s is not readable through its public pointer", pointer)
	}
}
