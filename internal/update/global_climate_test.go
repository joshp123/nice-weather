package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"nice-weather-finder/internal/climateassembly"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

func TestReadGlobalClimateTokenIsBoundedAndWhitespaceSafe(t *testing.T) {
	secret := "agera5-private-token"
	tests := []struct {
		name   string
		reader io.Reader
		want   string
		err    string
	}{
		{name: "line from stdin", reader: strings.NewReader(" \t" + secret + "\r\n"), want: secret},
		{name: "empty", reader: strings.NewReader(" \r\n\t"), err: "empty"},
		{name: "embedded whitespace", reader: strings.NewReader("first second"), err: "whitespace"},
		{name: "embedded control", reader: strings.NewReader("first\x00second"), err: "control"},
		{name: "bounded", reader: strings.NewReader(strings.Repeat("x", maximumGlobalClimateTokenBytes+1)), err: "exceeds"},
		{name: "reader error", reader: failingTokenReader{err: errors.New("reader exposed " + secret)}, err: "read AgERA5 bearer token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readGlobalClimateToken(test.reader)
			if test.err == "" {
				if err != nil || got != test.want {
					t.Fatalf("token=%q err=%v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("error = %v, want %q", err, test.err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error disclosed token: %v", err)
			}
		})
	}
}

func TestGlobalClimateProviderErrorsRedactToken(t *testing.T) {
	config := globalClimateTestConfig(t, "private-source-token")
	dependencies := globalClimateTestDependencies(t)
	dependencies.fetchMetadata = func(context.Context, string) (agera5.Metadata, error) {
		return agera5.Metadata{}, errors.New("provider echoed private-source-token")
	}
	_, err := runGlobalClimate(context.Background(), config, dependencies)
	if err == nil || !strings.Contains(err.Error(), "[redacted]") || strings.Contains(err.Error(), "private-source-token") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlobalClimateCancellationErrorsCannotDiscloseToken(t *testing.T) {
	for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
		config := globalClimateTestConfig(t, "private-cancellation-token")
		dependencies := globalClimateTestDependencies(t)
		dependencies.runBlocks = func(context.Context, string, climatebuild.Source, pipeline.FloatGrid, [sha256.Size]byte, int) (GlobalClimateBlockSummary, error) {
			return GlobalClimateBlockSummary{}, fmt.Errorf("provider echoed private-cancellation-token: %w", cancellation)
		}
		_, err := runGlobalClimate(context.Background(), config, dependencies)
		if !errors.Is(err, cancellation) || strings.Contains(err.Error(), "private-cancellation-token") {
			t.Fatalf("error = %v, want secret-safe %v", err, cancellation)
		}
	}
}

func TestGlobalClimateSourceEvidenceIsStableAndContractBound(t *testing.T) {
	fields := agera5.RequiredFields()
	firstDate := time.Date(2006, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC)
	plan := globalClimateTestSourcePlan(fields, firstDate, lastDate)
	first, err := digestGlobalClimateSourcePlan(agera5.DefaultRoot, fields, firstDate, lastDate, climatebuild.CheckpointDerivationContract, plan)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]agera5.Field(nil), fields...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	stable, err := digestGlobalClimateSourcePlan(agera5.DefaultRoot, reversed, firstDate, lastDate, climatebuild.CheckpointDerivationContract, plan)
	if err != nil || stable != first {
		t.Fatalf("reordered digest=%x err=%v, want %x", stable, err, first)
	}
	variants := []struct {
		name       string
		root       string
		fields     []agera5.Field
		firstDate  time.Time
		lastDate   time.Time
		derivation string
		plan       []globalClimateSourcePlanEntry
	}{
		{"root", agera5.DefaultRoot + "/changed", fields, firstDate, lastDate, climatebuild.CheckpointDerivationContract, plan},
		{"fields", agera5.DefaultRoot, fields[:len(fields)-1], firstDate, lastDate, climatebuild.CheckpointDerivationContract, globalClimateTestSourcePlan(fields[:len(fields)-1], firstDate, lastDate)},
		{"first year", agera5.DefaultRoot, fields, firstDate.AddDate(1, 0, 0), lastDate, climatebuild.CheckpointDerivationContract, globalClimateTestSourcePlan(fields, firstDate.AddDate(1, 0, 0), lastDate)},
		{"last year", agera5.DefaultRoot, fields, firstDate, lastDate.AddDate(-1, 0, 0), climatebuild.CheckpointDerivationContract, globalClimateTestSourcePlan(fields, firstDate, lastDate.AddDate(-1, 0, 0))},
		{"derivation", agera5.DefaultRoot, fields, firstDate, lastDate, climatebuild.CheckpointDerivationContract + "-changed", plan},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			got, err := digestGlobalClimateSourcePlan(variant.root, variant.fields, variant.firstDate, variant.lastDate, variant.derivation, variant.plan)
			if err != nil {
				t.Fatal(err)
			}
			if got == first {
				t.Fatal("source digest did not change")
			}
		})
	}
	if got, want := globalClimateReleaseID("month-07", time.July, first), globalClimateReleaseID("month-07", time.July, first); got != want {
		t.Fatalf("release ID is not deterministic: %q != %q", got, want)
	}
	if globalClimateReleaseID("month-08", time.August, first) == globalClimateReleaseID("month-07", time.July, first) {
		t.Fatal("release ID did not change with month")
	}
	changedSource := sha256.Sum256([]byte("changed source"))
	if globalClimateReleaseID("month-07", time.July, changedSource) == globalClimateReleaseID("month-07", time.July, first) {
		t.Fatal("release ID did not change with source contract")
	}
}

func TestGlobalClimateSourceEvidenceIgnoresPostReferenceMetadataAppend(t *testing.T) {
	firstMetadata := globalClimateMetadataSnapshot(t, 17_300)
	appendedMetadata := globalClimateMetadataSnapshot(t, 17_301)
	if firstMetadata.TimeLength() == appendedMetadata.TimeLength() {
		t.Fatal("test metadata snapshots do not model an append")
	}
	first, err := globalClimateSourceEvidenceSHA256(firstMetadata)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := globalClimateSourceEvidenceSHA256(appendedMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if appended != first {
		t.Fatalf("post-2025 append changed fixed-window source evidence: %x != %x", appended, first)
	}
}

func TestGlobalClimateDiskPreflightChecksExactDestinationVolumes(t *testing.T) {
	config := GlobalClimateConfig{ArtifactRoot: "/artifacts", CheckpointRoot: "/external/checkpoints"}
	seen := make([]string, 0, 2)
	available := func(path string) (uint64, error) {
		seen = append(seen, path)
		if path == config.CheckpointRoot {
			return minimumCheckpointFreeBytes, nil
		}
		return minimumArtifactFreeBytes, nil
	}
	if err := preflightGlobalClimateDisk(config, available); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{config.CheckpointRoot, config.ArtifactRoot}) {
		t.Fatalf("checked paths = %v", seen)
	}
	for _, test := range []struct {
		name string
		path string
	}{
		{"checkpoint", config.CheckpointRoot},
		{"artifact", config.ArtifactRoot},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := preflightGlobalClimateDisk(config, func(path string) (uint64, error) {
				if path == test.path {
					return 1, nil
				}
				return ^uint64(0), nil
			})
			if err == nil || !strings.Contains(err.Error(), test.name+" volume") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestGlobalClimateWiresMetadataAndSourceRetriesAndExactMask(t *testing.T) {
	config := globalClimateTestConfig(t, "token")
	dependencies := globalClimateTestDependencies(t)
	var metadataAttempts, retryCalls, maskCalls int
	var wrapped bool
	dependencies.retry = func(ctx context.Context, operation func() error) error {
		retryCalls++
		if err := operation(); err != nil {
			return operation()
		}
		return nil
	}
	dependencies.fetchMetadata = func(_ context.Context, token string) (agera5.Metadata, error) {
		metadataAttempts++
		if token != "token" {
			t.Fatalf("token = %q", token)
		}
		if metadataAttempts == 1 {
			return agera5.Metadata{}, errors.New("temporary metadata failure")
		}
		return agera5.Metadata{}, nil
	}
	dependencies.retrySource = func(source climatebuild.Source) climatebuild.Source {
		wrapped = true
		return source
	}
	dependencies.landMask = func(_ context.Context, cache string, spec delivery.GridSpec) (pipeline.FloatGrid, error) {
		maskCalls++
		if cache != config.LandCacheRoot || spec != climatebuild.WorldwideGridSpec() {
			t.Fatalf("cache=%q spec=%+v", cache, spec)
		}
		return pipeline.FloatGrid{Spec: spec}, nil
	}
	dependencies.runBlocks = func(_ context.Context, root string, source climatebuild.Source, mask pipeline.FloatGrid, digest [sha256.Size]byte, workers int) (GlobalClimateBlockSummary, error) {
		if root != config.CheckpointRoot || source == nil || mask.Spec != climatebuild.WorldwideGridSpec() || digest == ([sha256.Size]byte{}) || workers != DefaultGlobalClimateWorkers {
			t.Fatalf("runner root=%q source=%v mask=%+v digest=%x workers=%d", root, source, mask.Spec, digest, workers)
		}
		return GlobalClimateBlockSummary{Blocks: 1, WaterSkipped: 1}, nil
	}
	summary, err := runGlobalClimate(context.Background(), config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if metadataAttempts != 2 || retryCalls != 1 || !wrapped || maskCalls != 1 {
		t.Fatalf("metadata=%d retry=%d wrapped=%v masks=%d", metadataAttempts, retryCalls, wrapped, maskCalls)
	}
	if summary.BlocksSkipped != 1 || summary.SourceResponses != 12 || summary.SourceResponseBytes != 3456 || !summary.MainPointerUnchanged {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestGlobalClimatePublishesCompleteIndexFromAllMatchingInstalledMonths(t *testing.T) {
	config := globalClimateTestConfig(t, "token")
	dependencies := globalClimateTestDependencies(t)
	sourceEvidence := globalClimateTestSourceEvidence(t)
	wantSource := hex.EncodeToString(sourceEvidence[:])
	references := make(map[string]climatetiles.MonthReference, 12)
	catalogs := make(map[string]climatetiles.Catalog, 12)
	for month := time.January; month <= time.December; month++ {
		period := fmt.Sprintf("month-%02d", month)
		releaseID := globalClimateReleaseID(period, month, sourceEvidence)
		digest := sha256.Sum256([]byte(period))
		references[releaseID] = climatetiles.MonthReference{Period: period, ReleaseID: releaseID, CatalogSHA256: hex.EncodeToString(digest[:])}
		catalogs[releaseID] = climatetiles.Catalog{
			ReleaseID: releaseID, Period: period,
			Provenance: climatetiles.Provenance{SourceEvidenceSHA256: wantSource, DerivationContract: climatebuild.CheckpointDerivationContract},
		}
	}
	selectedID := globalClimateReleaseID(config.Period, config.Month, sourceEvidence)
	dependencies.installMonth = func(ctx context.Context, _ string, releaseID string, build climatetiles.Builder) (climatetiles.MonthReference, error) {
		if releaseID != selectedID {
			t.Fatalf("installed release %q; want %q", releaseID, selectedID)
		}
		if _, err := build(ctx, climatetiles.Stage{ReleaseID: releaseID}); err != nil {
			return climatetiles.MonthReference{}, err
		}
		return references[releaseID], nil
	}
	seen := make([]string, 0, 12)
	dependencies.validateMonth = func(_ string, releaseID, period string) (climatetiles.MonthReference, climatetiles.Catalog, bool, error) {
		seen = append(seen, period+"/"+releaseID)
		return references[releaseID], catalogs[releaseID], true, nil
	}
	var published climatetiles.Index
	dependencies.publishIndex = func(_ context.Context, _ string, indexID string, state climatetiles.IndexState, months []climatetiles.MonthReference) (climatetiles.Pointer, error) {
		published = climatetiles.Index{Version: climatetiles.IndexVersion, IndexID: indexID, State: state, Months: append([]climatetiles.MonthReference(nil), months...)}
		digest := sha256.Sum256([]byte(indexID))
		return climatetiles.Pointer{Version: climatetiles.PointerVersion, IndexID: indexID, IndexSHA256: hex.EncodeToString(digest[:])}, nil
	}
	var pointer climatetiles.Pointer
	originalPublish := dependencies.publishIndex
	dependencies.publishIndex = func(ctx context.Context, root, indexID string, state climatetiles.IndexState, months []climatetiles.MonthReference) (climatetiles.Pointer, error) {
		var err error
		pointer, err = originalPublish(ctx, root, indexID, state, months)
		return pointer, err
	}
	dependencies.validateCurrent = func(string) (climatetiles.Pointer, climatetiles.Index, error) {
		return pointer, published, nil
	}

	summary, err := runGlobalClimate(context.Background(), config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.SelectedMonthInstalled || summary.MonthsAvailable != 12 || summary.IndexState != climatetiles.IndexStateComplete || summary.Pointer != pointer || len(published.Months) != 12 {
		t.Fatalf("summary=%+v index=%+v", summary, published)
	}
	if len(seen) != 12 || published.Months[0].Period != "month-01" || published.Months[11].Period != "month-12" {
		t.Fatalf("discovered=%v months=%+v", seen, published.Months)
	}
}

func TestGlobalClimateIndexIdentityBindsFullStateAndMonthSet(t *testing.T) {
	month := climatetiles.MonthReference{Period: "month-07", ReleaseID: "release-07", CatalogSHA256: strings.Repeat("a", 64)}
	first, err := globalClimateIndexID(climatetiles.IndexStatePreview, []climatetiles.MonthReference{month})
	if err != nil {
		t.Fatal(err)
	}
	second, err := globalClimateIndexID(climatetiles.IndexStatePreview, []climatetiles.MonthReference{month})
	if err != nil || second != first || len(first) > 64 {
		t.Fatalf("first=%q second=%q err=%v", first, second, err)
	}
	changed := month
	changed.CatalogSHA256 = strings.Repeat("b", 64)
	other, err := globalClimateIndexID(climatetiles.IndexStatePreview, []climatetiles.MonthReference{changed})
	if err != nil || other == first {
		t.Fatalf("changed identity=%q err=%v", other, err)
	}
	if _, err := globalClimateIndexID(climatetiles.IndexStateComplete, []climatetiles.MonthReference{month}); err == nil {
		t.Fatal("incomplete month set accepted as complete")
	}
}

func TestGlobalClimateRejectsIncompleteDependenciesWithoutPanic(t *testing.T) {
	config := globalClimateTestConfig(t, "token")
	dependencies := globalClimateTestDependencies(t)
	dependencies.now = nil
	if _, err := runGlobalClimate(context.Background(), config, dependencies); err == nil || !strings.Contains(err.Error(), "dependencies are incomplete") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlobalClimateCheckpointLoaderUsesExactIdentityAndPath(t *testing.T) {
	root := t.TempDir()
	checkpoint := globalClimateCheckpointFixture(t, agera5.BlockCoordinate{Latitude: 79, Longitude: 54}, sha256.Sum256([]byte("source")))
	path := globalClimateCheckpointPath(root, checkpoint.Identity.Block)
	if err := climatebuild.WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	loader := globalClimateCheckpointLoader(root)
	loaded, err := loader(context.Background(), checkpoint.Identity)
	if err != nil || !reflect.DeepEqual(loaded, checkpoint) {
		t.Fatalf("loaded=%+v err=%v", loaded.Identity, err)
	}
	wrong := checkpoint.Identity
	wrong.SourceEvidenceSHA256 = sha256.Sum256([]byte("different source"))
	if _, err := loader(context.Background(), wrong); !errors.Is(err, climatebuild.ErrCheckpointMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	missing := checkpoint.Identity
	missing.Block.Longitude++
	height, width, err := agera5.BlockShape(missing.Block)
	if err != nil {
		t.Fatal(err)
	}
	missing.Width, missing.Height = width, height
	if _, err := loader(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("missing error = %v", err)
	}
}

func TestGlobalClimatePublishesIndependentMonthAndRerunsIdempotently(t *testing.T) {
	artifactRoot := t.TempDir()
	checkpointRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(artifactRoot, "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(artifactRoot, "releases", "main-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("releases/main-release", filepath.Join(artifactRoot, "current")); err != nil {
		t.Fatal(err)
	}
	sourceEvidence := globalClimateTestSourceEvidence(t)
	checkpoint := globalClimateCheckpointFixture(t, agera5.BlockCoordinate{Latitude: 79, Longitude: 54}, sourceEvidence)
	for row := 2; row <= 5; row++ {
		for column := 24; column <= 29; column++ {
			checkpoint.Months[int(time.July)-1][row*checkpoint.Identity.Width+column] = globalClimateDistributionRecord(uint8(50 + (row+column)%20))
		}
	}
	checkpointPath := globalClimateCheckpointPath(checkpointRoot, checkpoint.Identity.Block)
	if err := climatebuild.WriteCheckpoint(checkpointPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpointInfo, err := os.Stat(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	dependencies := productionLikeGlobalClimateTestDependencies(t)
	metadataSnapshots := []int{17_300, 17_301}
	var metadataFetches atomic.Int32
	var currentSnapshot atomic.Int32
	seenSnapshots := make([]int, 0, len(metadataSnapshots))
	dependencies.fetchMetadata = func(context.Context, string) (agera5.Metadata, error) {
		index := int(metadataFetches.Add(1)) - 1
		if index >= len(metadataSnapshots) || metadataSnapshots[index] < 17_300 {
			t.Fatalf("unexpected metadata snapshot %d", index)
		}
		currentSnapshot.Store(int32(metadataSnapshots[index]))
		return agera5.Metadata{}, nil
	}
	dependencies.sourceEvidence = func(agera5.Metadata) ([sha256.Size]byte, error) {
		seenSnapshots = append(seenSnapshots, int(currentSnapshot.Load()))
		// The injected seam models two coordinate-validated metadata snapshots.
		// Both expose the same exact 2006-2025 plan; only post-2025 length grew.
		return sourceEvidence, nil
	}
	dependencies.runBlocks = func(context.Context, string, climatebuild.Source, pipeline.FloatGrid, [sha256.Size]byte, int) (GlobalClimateBlockSummary, error) {
		if runs.Add(1) == 1 {
			return GlobalClimateBlockSummary{Blocks: 1, Built: 1, CheckpointBytes: checkpointInfo.Size(), Completed: []climatebuild.CheckpointIdentity{checkpoint.Identity}, TemperatureRepairs: 3, MaximumInversionC: .2, DewPointRepairs: 4, MaximumDewPointCapC: 2.1}, nil
		}
		return GlobalClimateBlockSummary{Blocks: 1, Reused: 1, CheckpointBytes: checkpointInfo.Size(), Completed: []climatebuild.CheckpointIdentity{checkpoint.Identity}, TemperatureRepairs: 3, MaximumInversionC: .2, DewPointRepairs: 4, MaximumDewPointCapC: 2.1}, nil
	}
	config := GlobalClimateConfig{
		ArtifactRoot: artifactRoot, CheckpointRoot: checkpointRoot, LandCacheRoot: filepath.Join(t.TempDir(), "land-cache"),
		Period: "month-07", Month: time.July, TokenReader: strings.NewReader("token\n"),
	}
	first, err := runGlobalClimate(context.Background(), config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	config.TokenReader = strings.NewReader("token\n")
	second, err := runGlobalClimate(context.Background(), config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReleaseID != second.ReleaseID || first.Pointer != second.Pointer || first.BlocksBuilt != 1 || second.BlocksReused != 1 {
		t.Fatalf("first=%+v\nsecond=%+v", first, second)
	}
	if !reflect.DeepEqual(seenSnapshots, metadataSnapshots) {
		t.Fatalf("source-evidence snapshots = %v, want %v", seenSnapshots, metadataSnapshots)
	}
	if first.PointBlocks != 1 || first.ScalarTiles == 0 || first.ArtifactBytes <= 0 || first.CheckpointBytes != checkpointInfo.Size() || first.TemperatureRepairs != 3 || first.MaximumInversionC != .2 || first.DewPointRepairs != 4 || first.MaximumDewPointCapC != 2.1 || !first.SelectedMonthInstalled || first.MonthsAvailable != 1 || first.IndexState != climatetiles.IndexStatePreview || !first.MainPointerUnchanged || !second.MainPointerUnchanged {
		t.Fatalf("first summary = %+v", first)
	}
	pointer, index, err := climatetiles.ValidateCurrentIndex(artifactRoot)
	if err != nil || pointer != first.Pointer || index.State != climatetiles.IndexStatePreview || len(index.Months) != 1 || index.Months[0].ReleaseID != first.ReleaseID || index.Months[0].Period != "month-07" {
		t.Fatalf("pointer=%+v index=%+v err=%v", pointer, index, err)
	}
	target, err := os.Readlink(filepath.Join(artifactRoot, "current"))
	if err != nil || target != "releases/main-release" {
		t.Fatalf("main current=%q err=%v", target, err)
	}
}

func TestGlobalClimateCancellationStopsBeforeBlocksAndDuringAssembly(t *testing.T) {
	t.Run("before work", func(t *testing.T) {
		config := globalClimateTestConfig(t, "token")
		dependencies := globalClimateTestDependencies(t)
		called := false
		dependencies.fetchMetadata = func(context.Context, string) (agera5.Metadata, error) {
			called = true
			return agera5.Metadata{}, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := runGlobalClimate(ctx, config, dependencies); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		if called {
			t.Fatal("metadata fetch ran after cancellation")
		}
	})
	t.Run("during blocks", func(t *testing.T) {
		config := globalClimateTestConfig(t, "token")
		dependencies := globalClimateTestDependencies(t)
		published := false
		dependencies.runBlocks = func(ctx context.Context, _ string, _ climatebuild.Source, _ pipeline.FloatGrid, _ [sha256.Size]byte, _ int) (GlobalClimateBlockSummary, error) {
			return GlobalClimateBlockSummary{}, context.Canceled
		}
		dependencies.installMonth = func(context.Context, string, string, climatetiles.Builder) (climatetiles.MonthReference, error) {
			published = true
			return climatetiles.MonthReference{}, nil
		}
		if _, err := runGlobalClimate(context.Background(), config, dependencies); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		if published {
			t.Fatal("publication ran after block cancellation")
		}
	})
	t.Run("during assembly", func(t *testing.T) {
		config := globalClimateTestConfig(t, "token")
		dependencies := globalClimateTestDependencies(t)
		ctx, cancel := context.WithCancel(context.Background())
		dependencies.buildMonth = func(buildCtx context.Context, _ climateassembly.Config) (climatetiles.Catalog, error) {
			cancel()
			return climatetiles.Catalog{}, buildCtx.Err()
		}
		if _, err := runGlobalClimate(ctx, config, dependencies); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestGlobalClimateFailsIfMainPointerChanges(t *testing.T) {
	config := globalClimateTestConfig(t, "token")
	if err := os.Symlink("original", filepath.Join(config.ArtifactRoot, "current")); err != nil {
		t.Fatal(err)
	}
	dependencies := globalClimateTestDependencies(t)
	install := dependencies.installMonth
	dependencies.installMonth = func(ctx context.Context, root, releaseID string, build climatetiles.Builder) (climatetiles.MonthReference, error) {
		reference, err := install(ctx, root, releaseID, build)
		if err != nil {
			return climatetiles.MonthReference{}, err
		}
		if err := os.Remove(filepath.Join(root, "current")); err != nil {
			return climatetiles.MonthReference{}, err
		}
		if err := os.Symlink("changed", filepath.Join(root, "current")); err != nil {
			return climatetiles.MonthReference{}, err
		}
		return reference, nil
	}
	_, err := runGlobalClimate(context.Background(), config, dependencies)
	if err == nil || !strings.Contains(err.Error(), "main artifacts/current changed") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlobalClimateBoundsRegularMainPointerSnapshot(t *testing.T) {
	config := globalClimateTestConfig(t, "token")
	if err := os.WriteFile(filepath.Join(config.ArtifactRoot, "current"), []byte(strings.Repeat("x", maximumMainPointerBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runGlobalClimate(context.Background(), config, globalClimateTestDependencies(t))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

type failingTokenReader struct{ err error }

func (reader failingTokenReader) Read([]byte) (int, error) { return 0, reader.err }

type globalClimateNoopSource struct{}

func (*globalClimateNoopSource) Shape(agera5.BlockCoordinate) (int, int, error) {
	return 1, 1, nil
}
func (*globalClimateNoopSource) Plan(time.Time, time.Time, agera5.BlockCoordinate) ([]climatebuild.ChunkReference, error) {
	return nil, nil
}
func (*globalClimateNoopSource) Fetch(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error) {
	return nil, errors.New("unexpected source fetch")
}

func globalClimateTestConfig(t *testing.T, token string) GlobalClimateConfig {
	t.Helper()
	return GlobalClimateConfig{
		ArtifactRoot: t.TempDir(), CheckpointRoot: t.TempDir(), LandCacheRoot: filepath.Join(t.TempDir(), "land"),
		Period: "month-07", Month: time.July, TokenReader: strings.NewReader(token),
	}
}

func globalClimateTestDependencies(t *testing.T) globalClimateDependencies {
	t.Helper()
	pointer := climatetiles.Pointer{}
	index := climatetiles.Index{}
	references := make(map[string]climatetiles.MonthReference)
	catalogs := make(map[string]climatetiles.Catalog)
	sourceEvidence := globalClimateTestSourceEvidence(t)
	return globalClimateDependencies{
		now:   func() time.Time { return time.Unix(100, 0) },
		retry: func(_ context.Context, operation func() error) error { return operation() },
		fetchMetadata: func(context.Context, string) (agera5.Metadata, error) {
			return agera5.Metadata{}, nil
		},
		sourceEvidence: func(agera5.Metadata) ([sha256.Size]byte, error) {
			return sourceEvidence, nil
		},
		newSource:     func(agera5.Metadata, string) climatebuild.Source { return &globalClimateNoopSource{} },
		retrySource:   func(source climatebuild.Source) climatebuild.Source { return source },
		sourceMetrics: func() agera5.ClientMetrics { return agera5.ClientMetrics{Responses: 12, FetchedBytes: 3456} },
		landMask: func(_ context.Context, _ string, spec delivery.GridSpec) (pipeline.FloatGrid, error) {
			return pipeline.FloatGrid{Spec: spec}, nil
		},
		availableBytes: func(string) (uint64, error) { return ^uint64(0), nil },
		runBlocks: func(context.Context, string, climatebuild.Source, pipeline.FloatGrid, [sha256.Size]byte, int) (GlobalClimateBlockSummary, error) {
			return GlobalClimateBlockSummary{}, nil
		},
		buildMonth: func(_ context.Context, config climateassembly.Config) (climatetiles.Catalog, error) {
			return climatetiles.Catalog{
				ReleaseID: config.Stage.ReleaseID,
				Period:    config.Period,
				Provenance: climatetiles.Provenance{
					SourceEvidenceSHA256: hex.EncodeToString(sourceEvidence[:]),
					DerivationContract:   climatebuild.CheckpointDerivationContract,
				},
			}, nil
		},
		installMonth: func(ctx context.Context, _ string, releaseID string, build climatetiles.Builder) (climatetiles.MonthReference, error) {
			catalog, err := build(ctx, climatetiles.Stage{ReleaseID: releaseID})
			if err != nil {
				return climatetiles.MonthReference{}, err
			}
			reference := climatetiles.MonthReference{Period: catalog.Period, ReleaseID: releaseID, CatalogSHA256: strings.Repeat("a", 64)}
			references[releaseID] = reference
			catalogs[releaseID] = catalog
			return reference, nil
		},
		validateMonth: func(_ string, releaseID, period string) (climatetiles.MonthReference, climatetiles.Catalog, bool, error) {
			reference, found := references[releaseID]
			if !found {
				return climatetiles.MonthReference{}, climatetiles.Catalog{}, false, nil
			}
			catalog := catalogs[releaseID]
			if reference.Period != period || catalog.Period != period {
				return climatetiles.MonthReference{}, climatetiles.Catalog{}, false, errors.New("period mismatch")
			}
			return reference, catalog, true, nil
		},
		publishIndex: func(_ context.Context, _ string, indexID string, state climatetiles.IndexState, months []climatetiles.MonthReference) (climatetiles.Pointer, error) {
			digest := sha256.Sum256([]byte(indexID))
			pointer = climatetiles.Pointer{Version: climatetiles.PointerVersion, IndexID: indexID, IndexSHA256: hex.EncodeToString(digest[:])}
			index = climatetiles.Index{Version: climatetiles.IndexVersion, IndexID: indexID, State: state, Months: append([]climatetiles.MonthReference(nil), months...)}
			return pointer, nil
		},
		validateCurrent: func(string) (climatetiles.Pointer, climatetiles.Index, error) {
			return pointer, index, nil
		},
		treeStats: func(string) (int, int64, error) { return 4, 9876, nil },
	}
}

func productionLikeGlobalClimateTestDependencies(t *testing.T) globalClimateDependencies {
	t.Helper()
	dependencies := globalClimateTestDependencies(t)
	dependencies.buildMonth = climateassembly.BuildMonth
	dependencies.installMonth = climatetiles.BuildAndInstallMonth
	dependencies.validateMonth = climatetiles.ValidateInstalledMonth
	dependencies.publishIndex = climatetiles.BuildAndPublishIndex
	dependencies.validateCurrent = climatetiles.ValidateCurrentIndex
	dependencies.treeStats = treeStats
	return dependencies
}

func globalClimateCheckpointFixture(t *testing.T, block agera5.BlockCoordinate, sourceEvidence [sha256.Size]byte) climatebuild.Checkpoint {
	t.Helper()
	height, width, err := agera5.BlockShape(block)
	if err != nil {
		t.Fatal(err)
	}
	identity := climatebuild.CheckpointIdentity{
		Block: block, Width: width, Height: height,
		NaturalEarthMaskSHA256: sha256.Sum256([]byte(fmt.Sprintf("mask-%d-%d", block.Latitude, block.Longitude))),
		SourceEvidenceSHA256:   sourceEvidence,
	}
	checkpoint := climatebuild.Checkpoint{Identity: identity}
	for month := range checkpoint.Months {
		checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, width*height)
	}
	return checkpoint
}

func globalClimateTestSourceEvidence(t *testing.T) [sha256.Size]byte {
	t.Helper()
	firstDate := time.Date(climatebuild.CheckpointFirstYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(climatebuild.CheckpointLastYear, time.December, 31, 0, 0, 0, 0, time.UTC)
	fields := agera5.RequiredFields()
	digest, err := digestGlobalClimateSourcePlan(
		agera5.DefaultRoot,
		fields,
		firstDate,
		lastDate,
		climatebuild.CheckpointDerivationContract,
		globalClimateTestSourcePlan(fields, firstDate, lastDate),
	)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// Metadata intentionally has no public fixture constructor. This test-only
// helper sets its two measured state fields so the production planning method,
// rather than an orchestration double, proves append-stable identity.
func globalClimateMetadataSnapshot(t *testing.T, timeLength int) agera5.Metadata {
	t.Helper()
	var metadata agera5.Metadata
	value := reflect.ValueOf(&metadata).Elem()
	setUnexported := func(name string, assign func(reflect.Value)) {
		field := value.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("AgERA5 Metadata no longer has %q", name)
		}
		writable := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		assign(writable)
	}
	setUnexported("timeLength", func(field reflect.Value) { field.SetInt(int64(timeLength)) })
	setUnexported("coordinatesValid", func(field reflect.Value) { field.SetBool(true) })
	return metadata
}

func globalClimateTestSourcePlan(fields []agera5.Field, firstDate, lastDate time.Time) []globalClimateSourcePlanEntry {
	block := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
	middle := time.Date(2015, time.December, 31, 0, 0, 0, 0, time.UTC)
	if middle.Before(firstDate) || !middle.Before(lastDate) {
		middle = firstDate
	}
	result := make([]globalClimateSourcePlanEntry, 0, len(fields)*2)
	for _, field := range fields {
		result = append(result, globalClimateSourcePlanEntry{
			Key: fmt.Sprintf("%s/4.%d.%d", field, block.Latitude, block.Longitude), Field: field,
			FirstDate: firstDate, LastDate: middle, Block: block,
		})
		if middle.Before(lastDate) {
			result = append(result, globalClimateSourcePlanEntry{
				Key: fmt.Sprintf("%s/5.%d.%d", field, block.Latitude, block.Longitude), Field: field,
				FirstDate: middle.AddDate(0, 0, 1), LastDate: lastDate, Block: block,
			})
		}
	}
	return result
}

func globalClimateDistributionRecord(median uint8) delivery.ClimateDistributionRecord {
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: 65, DryGoodPercent: 80, BrightGoodPercent: 55,
		Nice: delivery.ClimateProfileDistribution{
			MedianScore: median, CalmGoodPercent: 75, PoorWeekPermille: 250,
			RatingShares: delivery.ClimateRatingShares{Awful: 100, Poor: 100, Mixed: 200, Good: 250, VeryGood: 200, Excellent: 150},
		},
		Cycling: delivery.ClimateProfileDistribution{
			MedianScore: median, CalmGoodPercent: 60, PoorWeekPermille: 350,
			RatingShares: delivery.ClimateRatingShares{Awful: 150, Poor: 150, Mixed: 200, Good: 225, VeryGood: 175, Excellent: 100},
		},
		Valid: true,
	}
}
