package forecastruns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastassembly"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
)

type fixtureTier struct {
	ReleaseID string `json:"releaseId"`
	Date      string `json:"date"`
	Tier      string `json:"tier"`
	RunUTC    string `json:"runUtc"`
	Evidence  string `json:"evidence"`
}

type fixtureRange struct {
	ReleaseID string                       `json:"releaseId"`
	RunUTC    string                       `json:"runUtc"`
	Inputs    []forecastrange.InputCatalog `json:"inputs"`
}

func TestPublishBuildsExactCompleteRunAndIsIdempotent(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "run-new", "2026-07-23", "2026-07-22T12:00:00Z", []int{0, 2, 4})

	pointer, err := Publish(context.Background(), artifacts, "run-new")
	if err != nil {
		t.Fatal(err)
	}
	visible, index, err := ValidateCurrent(artifacts)
	if err != nil || !samePointer(pointer, visible) {
		t.Fatalf("visible pointer = %+v, %v; want %+v", visible, err, pointer)
	}
	if pointer.Previous != nil || pointer.Current.Path != "runs/run-new/index.json" || index.FirstDate != "2026-07-23" || index.LastDate != "2026-08-05" || len(index.Days) != 14 {
		t.Fatalf("publication contract = %+v index=%+v", pointer, index)
	}
	if index.TierSelection.Method != SelectionMethod || index.TierSelection.Blend || !strings.HasSuffix(strings.Join(index.TierSelection.Priority, ","), ",global") {
		t.Fatalf("tier policy = %+v", index.TierSelection)
	}
	if index.Range.Period != forecastrange.Period || index.Range.Provider != forecastpoints.ProviderIFS || index.Range.Tier != forecastpoints.TierGlobal || index.Range.RunUTC != "2026-07-22T12:00:00Z" {
		t.Fatalf("range reference = %+v", index.Range)
	}
	for day, entry := range index.Days {
		wantTiers := 1
		if day == 0 || day == 2 || day == 4 {
			wantTiers = 2
		}
		if len(entry.Tiers) != wantTiers || entry.Tiers[len(entry.Tiers)-1].Provider != forecastpoints.ProviderIFS || entry.Tiers[len(entry.Tiers)-1].NativeResolutionKM != 28 {
			t.Fatalf("day %d tiers = %+v", day, entry.Tiers)
		}
		if wantTiers == 2 && (entry.Tiers[0].Provider != forecastpoints.ProviderICONEU || entry.Tiers[0].NativeResolutionKM != 7) {
			t.Fatalf("day %d Europe tier = %+v", day, entry.Tiers[0])
		}
	}
	indexBefore := readFile(t, filepath.Join(artifacts, "forecast", pointer.Current.Path))
	again, err := Publish(context.Background(), artifacts, "run-new")
	if err != nil || !samePointer(pointer, again) {
		t.Fatalf("idempotent publish = %+v, %v; want %+v", again, err, pointer)
	}
	if after := readFile(t, filepath.Join(artifacts, "forecast", pointer.Current.Path)); string(after) != string(indexBefore) {
		t.Fatal("idempotent publication changed immutable index")
	}
}

func TestLoadCurrentMetadataReadsOnlyPointerAndIndexes(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "run-one", "2026-07-23", "2026-07-22T12:00:00Z", nil)
	if _, err := Publish(context.Background(), artifacts, "run-one"); err != nil {
		t.Fatal(err)
	}
	createRun(t, artifacts, "run-two", "2026-07-24", "2026-07-23T12:00:00Z", nil)
	pointer, err := Publish(context.Background(), artifacts, "run-two")
	if err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(artifacts, "forecast", "runs", "run-two", "forecast-days", "2026-07-24", forecastpoints.TierGlobal, "large-unread-payload")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(rangeFile(artifacts, "run-two"), 2<<30); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	visible, current, previous, err := LoadCurrentMetadata(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("metadata load took %s; forecast payload tree was expected to remain unread", elapsed)
	}
	if !samePointer(visible, pointer) || current.IndexID != "run-two" || previous == nil || previous.IndexID != "run-one" {
		t.Fatalf("metadata = %+v current=%+v previous=%+v", visible, current, previous)
	}
}

func TestPublishRejectsIncompleteDishonestAndUnsafeRuns(t *testing.T) {
	useFixtureTierLoader(t)
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{name: "missing global baseline", mutate: func(t *testing.T, root string) {
			trashPath(t, filepath.Join(root, "forecast", "runs", "candidate", "forecast-days", "2026-07-26", forecastpoints.TierGlobal))
		}, want: "entries"},
		{name: "Europe after day five", mutate: func(t *testing.T, root string) {
			writeTier(t, root, "candidate", "2026-07-28", forecastpoints.TierEurope, "2026-07-22T00:00:00Z", digest("icon"))
		}, want: "entries"},
		{name: "mixed IFS runs", mutate: func(t *testing.T, root string) {
			overwriteTier(t, root, "candidate", "2026-07-27", forecastpoints.TierGlobal, "2026-07-22T00:00:00Z", digest("ifs"))
			overwriteRange(t, root, "candidate", "2026-07-23", "2026-07-22T12:00:00Z")
		}, want: "mixes IFS runs"},
		{name: "missing range", mutate: func(t *testing.T, root string) {
			trashPath(t, filepath.Join(root, "forecast", "runs", "candidate", "forecast-ranges"))
		}, want: "requires forecast-days and forecast-ranges"},
		{name: "range mixes IFS runs", mutate: func(t *testing.T, root string) {
			overwriteRange(t, root, "candidate", "2026-07-23", "2026-07-22T00:00:00Z")
		}, want: "range reference is invalid"},
		{name: "range omits one day", mutate: func(t *testing.T, root string) {
			path := rangeFile(root, "candidate")
			var fixture fixtureRange
			if err := decodeExactJSON(readFile(t, path), &fixture); err != nil {
				t.Fatal(err)
			}
			fixture.Inputs = fixture.Inputs[:13]
			writeFixtureJSON(t, path, fixture)
		}, want: "indexed global daily catalogues"},
		{name: "extra range period", mutate: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "forecast", "runs", "candidate", "forecast-ranges", "other"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "exactly 1"},
		{name: "extra run entry", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "forecast", "runs", "candidate", "extra"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "unexpected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifacts := publicationFixture(t)
			createRun(t, artifacts, "candidate", "2026-07-23", "2026-07-22T12:00:00Z", []int{0, 1, 2, 3, 4})
			test.mutate(t, artifacts)
			if _, err := Publish(context.Background(), artifacts, "candidate"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Publish error = %v; want %q", err, test.want)
			}
			if _, err := os.Lstat(filepath.Join(artifacts, "forecast", "current.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected run published a pointer: %v", err)
			}
		})
	}

	artifacts := publicationFixture(t)
	createRun(t, artifacts, "candidate", "2026-07-23", "2026-07-22T12:00:00Z", nil)
	if _, err := Publish(context.Background(), artifacts, "../candidate"); err == nil {
		t.Fatal("traversing index ID was accepted")
	}
	symlinkRoot := filepath.Join(t.TempDir(), "artifacts-link")
	if err := os.Symlink(artifacts, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), symlinkRoot, "candidate"); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink artifact root error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(artifacts, "forecast", "runs", "candidate", "forecast-days", "2026-07-23", forecastpoints.TierGlobal, "catalog.json")
	if err := os.Remove(catalog); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, catalog); err != nil {
		t.Fatal(err)
	}
	original := loadInstalledTier
	loadInstalledTier = validateInstalledTier
	_, err := Publish(context.Background(), artifacts, "candidate")
	loadInstalledTier = original
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink catalogue error = %v", err)
	}

	lockArtifacts := publicationFixture(t)
	lockTarget := filepath.Join(t.TempDir(), "outside-lock")
	if err := os.WriteFile(lockTarget, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lockTarget, filepath.Join(lockArtifacts, "forecast", ".publication.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), lockArtifacts, "candidate"); err == nil || !strings.Contains(err.Error(), "publication lock") {
		t.Fatalf("symlink lock error = %v", err)
	}

	runArtifacts := publicationFixture(t)
	runOutside := filepath.Join(t.TempDir(), "outside-run")
	if err := os.Mkdir(runOutside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runOutside, filepath.Join(runArtifacts, "forecast", "runs", "candidate")); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), runArtifacts, "candidate"); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink candidate run error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(runOutside, ".install.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate symlink caused outside write: %v", err)
	}
}

func TestFailedCandidateKeepsCurrentAndCanAdoptOrphanIndexOnRetry(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "run-one", "2026-07-23", "2026-07-22T12:00:00Z", []int{0})
	current, err := Publish(context.Background(), artifacts, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	createRun(t, artifacts, "run-two", "2026-07-24", "2026-07-23T12:00:00Z", []int{0})
	target := tierFile(artifacts, "run-two", "2026-07-24", forecastpoints.TierGlobal)
	original := readFile(t, target)
	originalGate := publicationGate
	publicationGate = func() {
		if err := os.WriteFile(target, append(original, ' '), 0o644); err != nil {
			panic(err)
		}
	}
	_, err = Publish(context.Background(), artifacts, "run-two")
	publicationGate = originalGate
	if err == nil || !strings.Contains(err.Error(), "changed before pointer") {
		t.Fatalf("install-race error = %v", err)
	}
	visible, _, err := ValidateCurrent(artifacts)
	if err != nil || !samePointer(visible, current) {
		t.Fatalf("failed candidate changed current: %+v, %v; want %+v", visible, err, current)
	}
	if _, err := os.Lstat(filepath.Join(artifacts, "forecast", "runs", "run-two", IndexFilename)); err != nil {
		t.Fatalf("safe orphan index missing: %v", err)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	adopted, err := Publish(context.Background(), artifacts, "run-two")
	if err != nil || adopted.Previous == nil || *adopted.Previous != current.Current {
		t.Fatalf("orphan adoption = %+v, %v", adopted, err)
	}
}

func TestPublishedRunRejectsLateOptionalTierInstallation(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "sealed-run", "2026-07-23", "2026-07-22T12:00:00Z", nil)
	pointer, err := Publish(context.Background(), artifacts, "sealed-run")
	if err != nil {
		t.Fatal(err)
	}
	runRoot := filepath.Join(artifacts, "forecast", "runs", "sealed-run")
	_, err = forecastassembly.BuildDay(context.Background(), forecastassembly.Config{
		Root: runRoot, ReleaseID: "sealed-run", Provider: forecastpoints.ProviderICONEU, Tier: forecastpoints.TierEurope,
		Date:   time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		Series: pipeline.PreparedSeries{pipeline.Temperature: []*pipeline.PreparedFrame{nil}},
		Locate: func(float64, float64) (*time.Location, error) { return time.UTC, nil },
		Land:   runTestLandMask(t),
	})
	if err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("late tier installation error = %v", err)
	}
	visible, index, err := ValidateCurrent(artifacts)
	if err != nil || !samePointer(visible, pointer) || len(index.Days[0].Tiers) != 1 {
		t.Fatalf("sealed publication changed: %+v %+v, %v", visible, index, err)
	}
	_, err = forecastrange.Build(context.Background(), forecastrange.Config{Root: runRoot, ReleaseID: "sealed-run", Inputs: rangeInputs(index)})
	if err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("late range installation error = %v", err)
	}
}

func runTestLandMask(t *testing.T) forecasttiles.LandMask {
	t.Helper()
	mask, err := forecasttiles.NewLandMask(func(delivery.GridBounds) (bool, error) { return true, nil }, forecasttiles.LandIdentity{
		Authority: forecasttiles.LandAuthorityNaturalEarth, Dataset: forecasttiles.LandDatasetNaturalEarth10M, Version: forecasttiles.LandVersionNaturalEarth511,
		SourceArchiveSHA256: strings.Repeat("a", 64), MaskSHA256: strings.Repeat("b", 64), DerivationContract: forecasttiles.LandDerivationWebMercatorTouchV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mask
}

func TestStaleRunRequiresExplicitDeterministicRollback(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "run-one", "2026-07-23", "2026-07-22T12:00:00Z", []int{0, 1})
	first, err := Publish(context.Background(), artifacts, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	createRun(t, artifacts, "run-two", "2026-07-24", "2026-07-23T12:00:00Z", nil)
	second, err := Publish(context.Background(), artifacts, "run-two")
	if err != nil || second.Previous == nil || *second.Previous != first.Current {
		t.Fatalf("second publication = %+v, %v", second, err)
	}
	createRun(t, artifacts, "stale-run", "2026-07-25", "2026-07-22T18:00:00Z", nil)
	if _, err := Publish(context.Background(), artifacts, "stale-run"); err == nil || !strings.Contains(err.Error(), "stale forecast run") {
		t.Fatalf("stale publication error = %v", err)
	}
	rolledBack, err := Rollback(context.Background(), artifacts)
	if err != nil || rolledBack.Current != first.Current || rolledBack.Previous == nil || *rolledBack.Previous != second.Current {
		t.Fatalf("rollback = %+v, %v", rolledBack, err)
	}
	toggled, err := Rollback(context.Background(), artifacts)
	if err != nil || toggled.Current != second.Current || toggled.Previous == nil || *toggled.Previous != first.Current {
		t.Fatalf("second rollback = %+v, %v", toggled, err)
	}
}

func TestNewerProviderRunCannotMoveForecastHorizonBackwards(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "current-run", "2026-07-24", "2026-07-22T12:00:00Z", nil)
	current, err := Publish(context.Background(), artifacts, "current-run")
	if err != nil {
		t.Fatal(err)
	}
	createRun(t, artifacts, "backwards-run", "2026-07-23", "2026-07-23T12:00:00Z", nil)
	if _, err := Publish(context.Background(), artifacts, "backwards-run"); err == nil || !strings.Contains(err.Error(), "horizon moves backwards") {
		t.Fatalf("backwards horizon error = %v", err)
	}
	visible, _, err := ValidateCurrent(artifacts)
	if err != nil || !samePointer(visible, current) {
		t.Fatalf("backwards horizon changed current: %+v, %v", visible, err)
	}
}

func TestPublicationRejectsRootReplacementAfterValidation(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "candidate", "2026-07-23", "2026-07-22T12:00:00Z", nil)
	forecastRoot := filepath.Join(artifacts, "forecast")
	moved := filepath.Join(artifacts, "forecast-moved")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(outside, "runs", "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	originalGate := publicationGate
	publicationGate = func() {
		if err := os.Rename(forecastRoot, moved); err != nil {
			panic(err)
		}
		if err := os.Symlink(outside, forecastRoot); err != nil {
			panic(err)
		}
	}
	_, err := Publish(context.Background(), artifacts, "candidate")
	publicationGate = originalGate
	if err == nil || !strings.Contains(err.Error(), "root changed") {
		t.Fatalf("root replacement error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside pointer created: %v", err)
	}
}

func TestConcurrentPublicationHasOneLockOwnerAndNoPartialPointer(t *testing.T) {
	useFixtureTierLoader(t)
	artifacts := publicationFixture(t)
	createRun(t, artifacts, "race-one", "2026-07-23", "2026-07-22T12:00:00Z", nil)
	createRun(t, artifacts, "race-two", "2026-07-24", "2026-07-23T12:00:00Z", nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	originalGate := publicationGate
	var once sync.Once
	publicationGate = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	defer func() { publicationGate = originalGate }()
	result := make(chan error, 1)
	go func() {
		_, err := Publish(context.Background(), artifacts, "race-one")
		result <- err
	}()
	<-entered
	if _, err := Publish(context.Background(), artifacts, "race-two"); err == nil || !strings.Contains(err.Error(), "another forecast publication") {
		t.Fatalf("concurrent publication error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(artifacts, "forecast", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pointer visible before owner commit: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	pointer, index, err := ValidateCurrent(artifacts)
	if err != nil || pointer.Current.IndexID != "race-one" || index.IndexID != "race-one" {
		t.Fatalf("race winner = %+v %+v, %v", pointer, index, err)
	}
}

func publicationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "forecast", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func createRun(t *testing.T, artifacts, id, startDate, ifsRun string, europeDays []int) {
	t.Helper()
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		t.Fatal(err)
	}
	europe := make(map[int]bool, len(europeDays))
	for _, day := range europeDays {
		europe[day] = true
	}
	for day := 0; day < 14; day++ {
		date := start.AddDate(0, 0, day).Format("2006-01-02")
		writeTier(t, artifacts, id, date, forecastpoints.TierGlobal, ifsRun, digest("ifs-evidence-"+ifsRun+"-"+date))
		if europe[day] {
			writeTier(t, artifacts, id, date, forecastpoints.TierEurope, "2026-07-22T00:00:00Z", digest("icon-evidence-"+date))
		}
	}
	overwriteRange(t, artifacts, id, startDate, ifsRun)
}

func overwriteRange(t *testing.T, artifacts, id, startDate, run string) {
	t.Helper()
	start, err := time.Parse(time.DateOnly, startDate)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]forecastrange.InputCatalog, 14)
	for day := range inputs {
		date := start.AddDate(0, 0, day).Format(time.DateOnly)
		payload := readFile(t, tierFile(artifacts, id, date, forecastpoints.TierGlobal))
		inputs[day] = forecastrange.InputCatalog{
			Date: date, Path: filepath.ToSlash(filepath.Join("forecast-days", date, forecastpoints.TierGlobal, "catalog.json")),
			Bytes: uint64(len(payload)), SHA256: digest(string(payload)),
		}
	}
	writeFixtureJSON(t, rangeFile(artifacts, id), fixtureRange{ReleaseID: id, RunUTC: run, Inputs: inputs})
}

func rangeFile(artifacts, id string) string {
	return filepath.Join(artifacts, "forecast", "runs", id, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal, forecastrange.CatalogFilename)
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTier(t *testing.T, artifacts, id, date, tier, run, evidence string) {
	t.Helper()
	root := filepath.Dir(tierFile(artifacts, id, date, tier))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	overwriteTier(t, artifacts, id, date, tier, run, evidence)
}

func overwriteTier(t *testing.T, artifacts, id, date, tier, run, evidence string) {
	t.Helper()
	payload, err := json.Marshal(fixtureTier{ReleaseID: id, Date: date, Tier: tier, RunUTC: run, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(tierFile(artifacts, id, date, tier), payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func tierFile(artifacts, id, date, tier string) string {
	return filepath.Join(artifacts, "forecast", "runs", id, "forecast-days", date, tier, "catalog.json")
}

func useFixtureTierLoader(t *testing.T) {
	t.Helper()
	original, originalRange := loadInstalledTier, loadInstalledRange
	loadInstalledTier = func(root string) (forecastcatalog.Catalog, string, uint64, error) {
		payload, err := readRegularBounded(filepath.Join(root, "catalog.json"), forecastcatalog.MaximumCatalogBytes)
		if err != nil {
			return forecastcatalog.Catalog{}, "", 0, err
		}
		var fixture fixtureTier
		if err := decodeExactJSON(payload, &fixture); err != nil {
			return forecastcatalog.Catalog{}, "", 0, err
		}
		provider, product, resolution := forecastpoints.ProviderIFS, forecastcatalog.ProductIFSControl, float64(28)
		if fixture.Tier == forecastpoints.TierEurope {
			provider, product, resolution = forecastpoints.ProviderICONEU, forecastcatalog.ProductICONEU, 7
		}
		sum := sha256.Sum256(payload)
		return forecastcatalog.Catalog{
			ReleaseID: fixture.ReleaseID, LocalDate: fixture.Date, Provider: provider, Tier: fixture.Tier,
			Evidence: forecastcatalog.Evidence{Product: product, RunUTC: fixture.RunUTC, NativeResolutionKM: resolution, SourceEvidenceSHA256: fixture.Evidence},
		}, hex.EncodeToString(sum[:]), uint64(len(payload)), nil
	}
	loadInstalledRange = func(root string) (forecastrange.Catalog, []byte, error) {
		entries, err := readRealDirectory(root)
		if err != nil {
			return forecastrange.Catalog{}, nil, err
		}
		if err := exactEntries(entries, map[string]bool{forecastrange.CatalogFilename: false}, "forecast range product"); err != nil {
			return forecastrange.Catalog{}, nil, err
		}
		payload, err := readRegularBounded(filepath.Join(root, forecastrange.CatalogFilename), forecastrange.MaximumCatalogBytes)
		if err != nil {
			return forecastrange.Catalog{}, nil, err
		}
		var fixture fixtureRange
		if err := decodeExactJSON(payload, &fixture); err != nil {
			return forecastrange.Catalog{}, nil, err
		}
		dates := make([]string, len(fixture.Inputs))
		for index, input := range fixture.Inputs {
			dates[index] = input.Date
		}
		return forecastrange.Catalog{
			ReleaseID: fixture.ReleaseID, Period: forecastrange.Period, Dates: dates, Inputs: fixture.Inputs,
			Source: forecastrange.Source{Provider: forecastpoints.ProviderIFS, Tier: forecastpoints.TierGlobal, Product: forecastcatalog.ProductIFSControl, RunUTC: fixture.RunUTC, NativeResolutionKM: 28, StaleAfterHours: forecastrange.StaleAfterHours},
		}, payload, nil
	}
	t.Cleanup(func() { loadInstalledTier, loadInstalledRange = original, originalRange })
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func trashPath(t *testing.T, path string) {
	t.Helper()
	trash := filepath.Join(t.TempDir(), filepath.Base(path))
	if err := os.Rename(path, trash); err != nil {
		t.Fatal(err)
	}
}
