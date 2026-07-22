package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastassembly"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/tiledelivery"
)

func TestForecastProductionPublishesWholeCandidatePointerLastAndIsIdempotent(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	summary, err := runForecastProduction(context.Background(), fixture.config, fixture.now, fixture.dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if summary.GlobalDays != productDays || summary.EuropeDays != 4 || summary.BuiltProducts != 19 || summary.ReusedProducts != 0 {
		t.Fatalf("forecast production summary = %+v", summary)
	}
	if fixture.ifsStageCalls != 1 || fixture.iconStageCalls != 1 || fixture.publishCalls != 1 {
		t.Fatalf("boundary calls: IFS stage=%d ICON stage=%d publish=%d", fixture.ifsStageCalls, fixture.iconStageCalls, fixture.publishCalls)
	}
	if got := fixture.events[len(fixture.events)-1]; got != "publish" {
		t.Fatalf("last transaction event = %q, want pointer publication", got)
	}
	if _, err := os.Stat(filepath.Join(fixture.config.ArtifactRoot, "forecast", "current.json")); err != nil {
		t.Fatalf("forecast pointer was not published: %v", err)
	}
	assertNoForecastSourceScratch(t, fixture.config.StagingRoot)
	if _, err := os.Stat(filepath.Join(fixture.config.ArtifactRoot, "forecast", ".candidate-"+summary.IndexID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private candidate was not atomically installed: %v", err)
	}
	for day := 0; day < 5; day++ {
		date := fixture.now.AddDate(0, 0, 1+day).Format(time.DateOnly)
		_, statErr := os.Stat(filepath.Join(summary.CandidateRoot, "forecast-days", date, forecastpoints.TierEurope))
		if day < summary.EuropeDays && statErr != nil {
			t.Fatalf("eligible ICON-EU day %s was omitted: %v", date, statErr)
		}
		if day >= summary.EuropeDays && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("horizon-ineligible ICON-EU day %s was included: %v", date, statErr)
		}
	}

	stageCalls := fixture.ifsStageCalls + fixture.iconStageCalls
	again, err := runForecastProduction(context.Background(), fixture.config, fixture.now, fixture.dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if again.IndexID != summary.IndexID || again.BuiltProducts != 0 || again.ReusedProducts != 19 || fixture.ifsStageCalls+fixture.iconStageCalls != stageCalls || fixture.publishCalls != 2 {
		t.Fatalf("idempotent run = %+v; stage calls=%d publish calls=%d", again, fixture.ifsStageCalls+fixture.iconStageCalls, fixture.publishCalls)
	}
}

func TestForecastProductionFailsClosedWhenICONDiscoveryOrRequiredStagingFails(t *testing.T) {
	t.Run("IFS baseline", func(t *testing.T) {
		fixture := newForecastProductionFixture(t)
		dependencies := fixture.dependencies()
		dependencies.stageIFS = func(context.Context, ifs.Run, []int, string) ([]stagedFrame, error) {
			fixture.ifsStageCalls++
			return nil, errors.New("IFS range failed")
		}
		_, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
		if err == nil || !strings.Contains(err.Error(), "stage complete worldwide IFS") {
			t.Fatalf("IFS stage error = %v", err)
		}
		if fixture.ifsStageCalls != 1 || fixture.iconStageCalls != 0 || fixture.publishCalls != 0 {
			t.Fatalf("IFS failure boundary calls: IFS=%d ICON=%d publish=%d", fixture.ifsStageCalls, fixture.iconStageCalls, fixture.publishCalls)
		}
		assertNoForecastPointer(t, fixture.config.ArtifactRoot)
		assertNoForecastSourceScratch(t, fixture.config.StagingRoot)
	})

	t.Run("discovery", func(t *testing.T) {
		fixture := newForecastProductionFixture(t)
		dependencies := fixture.dependencies()
		dependencies.discoverDWD = func(context.Context, time.Time) (dwd.Run, error) {
			return dwd.Run{}, errors.New("temporary DWD failure")
		}
		_, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
		if err == nil || !strings.Contains(err.Error(), "discover complete ICON-EU") {
			t.Fatalf("ICON discovery error = %v", err)
		}
		if fixture.ifsStageCalls != 0 || fixture.iconStageCalls != 0 || fixture.publishCalls != 0 {
			t.Fatalf("failed discovery crossed a mutation boundary: %+v", fixture)
		}
		assertNoForecastPointer(t, fixture.config.ArtifactRoot)
	})

	t.Run("required fields", func(t *testing.T) {
		fixture := newForecastProductionFixture(t)
		dependencies := fixture.dependencies()
		dependencies.stageDWD = func(context.Context, dwd.Run, []int, string) ([]stagedFrame, error) {
			fixture.iconStageCalls++
			return nil, errors.New("required temperature unavailable")
		}
		_, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
		if err == nil || !strings.Contains(err.Error(), "stage selected ICON-EU refinement") {
			t.Fatalf("required ICON staging error = %v", err)
		}
		if fixture.ifsStageCalls != 1 || fixture.iconStageCalls != 1 || fixture.publishCalls != 0 {
			t.Fatalf("required failure boundary calls: IFS=%d ICON=%d publish=%d", fixture.ifsStageCalls, fixture.iconStageCalls, fixture.publishCalls)
		}
		assertNoForecastPointer(t, fixture.config.ArtifactRoot)
		assertNoForecastSourceScratch(t, fixture.config.StagingRoot)
	})
}

func TestForecastProductionFailsClosedBeforeInstallWhenRangeBuildFails(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	dependencies := fixture.dependencies()
	dependencies.buildRange = func(context.Context, forecastrange.Config) (forecastrange.Product, error) {
		return forecastrange.Product{}, errors.New("range inputs disagree")
	}
	summary, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
	if err == nil || !strings.Contains(err.Error(), "build worldwide 14-day forecast range") || !strings.Contains(err.Error(), "private candidate retained for retry") {
		t.Fatalf("range failure = %v", err)
	}
	if summary.BuiltProducts != summary.GlobalDays+summary.EuropeDays || fixture.publishCalls != 0 {
		t.Fatalf("range failure summary = %+v, publish calls=%d", summary, fixture.publishCalls)
	}
	assertNoForecastPointer(t, fixture.config.ArtifactRoot)
	if _, statErr := os.Stat(filepath.Join(fixture.config.ArtifactRoot, "forecast", "runs", summary.IndexID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed range installed a public candidate: %v", statErr)
	}
}

func TestForecastProductionRefusesUnsafePrivateCandidateBeforeStaging(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	dependencies := fixture.dependencies()
	dependencies.cleanCandidate = func(string) error { return errors.New("refuse unowned forecast range transaction") }
	_, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
	if err == nil || !strings.Contains(err.Error(), "clean private forecast candidate before retry") {
		t.Fatalf("private candidate cleanup error = %v", err)
	}
	if fixture.ifsStageCalls != 0 || fixture.iconStageCalls != 0 || fixture.publishCalls != 0 {
		t.Fatalf("unsafe private candidate crossed staging/publication: %+v", fixture)
	}
	assertNoForecastPointer(t, fixture.config.ArtifactRoot)
}

func TestForecastProductionCleansPrivateCandidateBeforeResume(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	first, last := fixture.now.AddDate(0, 0, 1), fixture.now.AddDate(0, 0, productDays)
	indexID := forecastProductionID(fixture.ifsRun.Time, fixture.iconRun.StartedAt, 4, first, last)
	candidate := filepath.Join(fixture.config.ArtifactRoot, "forecast", ".candidate-"+indexID)
	debris := filepath.Join(candidate, ".forecast-range-interrupted")
	if err := os.MkdirAll(debris, 0o755); err != nil {
		t.Fatal(err)
	}
	dependencies := fixture.dependencies()
	dependencies.cleanCandidate = func(root string) error {
		if root != candidate {
			return fmt.Errorf("cleaned candidate %s; want %s", root, candidate)
		}
		return os.RemoveAll(debris)
	}
	summary, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if summary.IndexID != indexID {
		t.Fatalf("resumed index = %s; want %s", summary.IndexID, indexID)
	}
}

func TestForecastProductionCancellationRetainsOnlyPrivateCompletedProductsAndResumes(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	dependencies := fixture.dependencies()
	builds := 0
	completeBuild := dependencies.build
	dependencies.build = func(ctx context.Context, config forecastassembly.Config) (forecastassembly.Product, error) {
		builds++
		if builds == 3 {
			return forecastassembly.Product{}, context.Canceled
		}
		return completeBuild(ctx, config)
	}
	partial, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "private candidate retained for retry") {
		t.Fatalf("cancelled forecast transaction error = %v", err)
	}
	if partial.BuiltProducts != 2 || fixture.publishCalls != 0 {
		t.Fatalf("cancelled summary = %+v, publish calls=%d", partial, fixture.publishCalls)
	}
	assertNoForecastPointer(t, fixture.config.ArtifactRoot)
	assertNoForecastSourceScratch(t, fixture.config.StagingRoot)
	if exists, statErr := realDirectoryExists(partial.CandidateRoot); statErr != nil || !exists {
		t.Fatalf("private retry candidate = %v, %v", exists, statErr)
	}

	resumed, err := runForecastProduction(context.Background(), fixture.config, fixture.now, fixture.dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ReusedProducts != 2 || resumed.BuiltProducts != 17 || fixture.publishCalls != 1 {
		t.Fatalf("resumed summary = %+v, publish calls=%d", resumed, fixture.publishCalls)
	}
	assertNoForecastSourceScratch(t, fixture.config.StagingRoot)
}

func TestForecastProductionDiskPreflightChecksEachDistinctFilesystemOnce(t *testing.T) {
	calls := 0
	free := func(path string) (int64, uint64, error) {
		calls++
		if strings.Contains(path, "staging") {
			return forecastProductionMinimumFreeBytes - 1, 2, nil
		}
		return forecastProductionMinimumFreeBytes, 1, nil
	}
	err := preflightForecastProductionDisk("/artifact", "/staging", free)
	if err == nil || !strings.Contains(err.Error(), "needs at least") || calls != 2 {
		t.Fatalf("disk preflight = %v, calls=%d", err, calls)
	}
	calls = 0
	if err := preflightForecastProductionDisk("/artifact", "/staging", func(string) (int64, uint64, error) {
		calls++
		return forecastProductionMinimumFreeBytes, 1, nil
	}); err != nil || calls != 2 {
		t.Fatalf("shared-filesystem preflight = %v, calls=%d", err, calls)
	}
}

func TestForecastProductionCleansOnlyAbandonedSourceTransactions(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "forecast-source-stale")
	unowned := filepath.Join(root, "forecast-source-unowned")
	unrelated := filepath.Join(root, "keep-me")
	for _, path := range []string{stale, unowned, unrelated} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := markForecastSourceTransaction(stale); err != nil {
		t.Fatal(err)
	}
	if err := cleanForecastSourceTransactions(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned source transaction remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("source cleanup touched unrelated staging: %v", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("source cleanup touched unowned matching directory: %v", err)
	}
}

func TestForecastProductionCleanupRejectsSymlinkedOwnership(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "owned-elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := markForecastSourceTransaction(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "forecast-source-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := cleanForecastSourceTransactions(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("source cleanup touched symlinked transaction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, forecastSourceMarker)); err != nil {
		t.Fatalf("source cleanup touched symlink target: %v", err)
	}
}

func TestForecastProductionCleansAbandonedSourceBeforeDiskPreflight(t *testing.T) {
	fixture := newForecastProductionFixture(t)
	stale := filepath.Join(fixture.config.StagingRoot, "forecast-source-stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := markForecastSourceTransaction(stale); err != nil {
		t.Fatal(err)
	}
	dependencies := fixture.dependencies()
	cleanAtPreflight := false
	dependencies.freeBytes = func(path string) (int64, uint64, error) {
		if path == fixture.config.StagingRoot {
			_, err := os.Stat(stale)
			cleanAtPreflight = errors.Is(err, os.ErrNotExist)
			if !cleanAtPreflight {
				return forecastProductionMinimumFreeBytes - 1, 1, nil
			}
		}
		return forecastProductionMinimumFreeBytes, 1, nil
	}
	dependencies.discoverIFS = func(context.Context) (ifs.Run, error) {
		return ifs.Run{}, errors.New("stop after preflight")
	}
	_, err := runForecastProduction(context.Background(), fixture.config, fixture.now, dependencies)
	if err == nil || !strings.Contains(err.Error(), "discover complete worldwide IFS") || !cleanAtPreflight {
		t.Fatalf("cleanup/preflight order = clean:%v error:%v", cleanAtPreflight, err)
	}
}

func TestProductionLandPredicateAcceptsICONMaximumZoom(t *testing.T) {
	const zoom = uint8(5)
	width := int(tiledelivery.TileSize) << zoom
	pixels := make([]byte, width*width)
	worldEdge := [2]int{width - 1, width - 1}
	europe := [2]int{4207, 2810}
	for _, cell := range [][2]int{worldEdge, europe} {
		pixels[cell[1]*width+cell[0]] = 1
	}
	isLand, err := webMercatorTouchedLandPredicate(pixels, zoom)
	if err != nil {
		t.Fatal(err)
	}
	for name, cell := range map[string][2]int{"world edge": worldEdge, "Europe": europe} {
		got, lookupErr := isLand(webMercatorTestTexelBounds(width, cell[0], cell[1]))
		if lookupErr != nil || !got {
			t.Fatalf("ICON z5 %s land predicate = %v, %v; want land", name, got, lookupErr)
		}
	}
	if _, err := webMercatorTouchedLandPredicate(nil, 6); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("z6 land predicate boundary = %v; want bounded rejection", err)
	}
}

func TestValidateICONStagedRequiresWholeOptionalGroups(t *testing.T) {
	run := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	steps := []int{0, 1}
	complete := fakeICONStaged(run, steps)
	if err := validateICONStaged(run, steps, complete); err != nil {
		t.Fatal(err)
	}
	partialGust := append(append([]stagedFrame(nil), complete...), stagedFrame{field: pipeline.Gust, step: 0, run: run, resolution: 7, spacing: .0625})
	if err := validateICONStaged(run, steps, partialGust); err == nil || !strings.Contains(err.Error(), "gust series is partial") {
		t.Fatalf("partial gust validation = %v", err)
	}
	partialSolar := append(append([]stagedFrame(nil), complete...), stagedFrame{field: pipeline.SolarDirect, step: 0, run: run, resolution: 7, spacing: .0625})
	if err := validateICONStaged(run, steps, partialSolar); err == nil || !strings.Contains(err.Error(), "solar pair is partial") {
		t.Fatalf("partial solar validation = %v", err)
	}
}

type forecastProductionFixture struct {
	t              *testing.T
	now            time.Time
	ifsRun         ifs.Run
	iconRun        dwd.Run
	config         ForecastProductionConfig
	events         []string
	ifsStageCalls  int
	iconStageCalls int
	publishCalls   int
	preparedOpen   bool
	preparedMarker string
}

func newForecastProductionFixture(t *testing.T) *forecastProductionFixture {
	root := t.TempDir()
	now := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	return &forecastProductionFixture{
		t: t, now: now,
		ifsRun:  ifs.Run{Time: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)},
		iconRun: dwd.Run{StartedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)},
		config: ForecastProductionConfig{
			ArtifactRoot: filepath.Join(root, "artifacts"), StagingRoot: filepath.Join(root, "staging"), CacheRoot: filepath.Join(root, "cache"),
		},
	}
}

func (fixture *forecastProductionFixture) dependencies() forecastProductionDependencies {
	return forecastProductionDependencies{
		discoverIFS: func(context.Context) (ifs.Run, error) { return fixture.ifsRun, nil },
		discoverDWD: func(context.Context, time.Time) (dwd.Run, error) { return fixture.iconRun, nil },
		stageIFS: func(_ context.Context, run ifs.Run, steps []int, root string) ([]stagedFrame, error) {
			fixture.ifsStageCalls++
			fixture.events = append(fixture.events, "stage-ifs")
			if err := writeFakeStage(root, "ifs"); err != nil {
				return nil, err
			}
			return fakeIFSStaged(run.Time, steps), nil
		},
		stageDWD: func(_ context.Context, run dwd.Run, steps []int, root string) ([]stagedFrame, error) {
			fixture.iconStageCalls++
			fixture.events = append(fixture.events, "stage-icon")
			if err := writeFakeStage(root, "icon"); err != nil {
				return nil, err
			}
			return fakeICONStaged(run.StartedAt, steps), nil
		},
		newDecoder: func(delivery.GridBounds) (*pipeline.Decoder, error) { return nil, nil },
		land: func(context.Context, *pipeline.Decoder, string, string, uint8) (forecasttiles.LandMask, error) {
			return updateTestLandMask(fixture.t, func(delivery.GridBounds) (bool, error) { return true, nil }), nil
		},
		locate: func() (forecasttiles.LocateTimezone, error) {
			return func(float64, float64) (*time.Location, error) { return time.UTC, nil }, nil
		},
		prepare: func(_ context.Context, _ *pipeline.Decoder, selected []stagedFrame, scratch, sourceID, _ string) (pipeline.PreparedSeries, func() error, error) {
			if fixture.preparedOpen || len(selected) == 0 || sourceID == "" {
				return nil, nil, errors.New("prepared-day boundary overlapped or was incomplete")
			}
			fixture.preparedOpen = true
			fixture.preparedMarker = filepath.Join(scratch, "prepared.marker")
			if err := os.WriteFile(fixture.preparedMarker, []byte(sourceID), 0o600); err != nil {
				return nil, nil, err
			}
			return pipeline.PreparedSeries{pipeline.Temperature: nil}, func() error {
				fixture.preparedOpen = false
				return os.Remove(fixture.preparedMarker)
			}, nil
		},
		build: func(_ context.Context, config forecastassembly.Config) (forecastassembly.Product, error) {
			if !fixture.preparedOpen {
				return forecastassembly.Product{}, errors.New("build ran without one prepared day")
			}
			root := filepath.Join(config.Root, "forecast-days", config.Date.Format(time.DateOnly), config.Tier)
			if err := os.MkdirAll(root, 0o755); err != nil {
				return forecastassembly.Product{}, err
			}
			if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte("fake\n"), 0o644); err != nil {
				return forecastassembly.Product{}, err
			}
			fixture.events = append(fixture.events, "build-"+config.Date.Format(time.DateOnly)+"-"+config.Tier)
			return forecastassembly.Product{Root: root, Stats: forecastassembly.Stats{OutputFiles: 1, OutputBytes: 5}}, nil
		},
		buildRange: func(_ context.Context, config forecastrange.Config) (forecastrange.Product, error) {
			if len(config.Inputs) != productDays {
				return forecastrange.Product{}, errors.New("range build did not receive all global days")
			}
			for _, input := range config.Inputs {
				if !strings.HasSuffix(input.Path, "/"+forecastpoints.TierGlobal+"/catalog.json") || strings.Contains(input.Path, forecastpoints.TierEurope) {
					return forecastrange.Product{}, fmt.Errorf("range input used a non-global tier: %s", input.Path)
				}
			}
			root := filepath.Join(config.Root, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal)
			if exists, err := realDirectoryExists(root); err != nil {
				return forecastrange.Product{}, err
			} else if exists {
				return forecastrange.Product{Root: root, Stats: forecastrange.Stats{OutputFiles: 1, OutputBytes: 6}}, nil
			}
			for _, input := range config.Inputs {
				if _, err := os.Stat(filepath.Join(config.Root, filepath.FromSlash(input.Path))); err != nil {
					return forecastrange.Product{}, fmt.Errorf("range input %s is missing: %w", input.Date, err)
				}
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return forecastrange.Product{}, err
			}
			if err := os.WriteFile(filepath.Join(root, forecastrange.CatalogFilename), []byte("range\n"), 0o644); err != nil {
				return forecastrange.Product{}, err
			}
			fixture.events = append(fixture.events, "build-range")
			return forecastrange.Product{Root: root, Stats: forecastrange.Stats{OutputFiles: 1, OutputBytes: 6}}, nil
		},
		cleanCandidate: func(string) error { return nil },
		publish: func(_ context.Context, artifactRoot, indexID string) (forecastruns.Pointer, error) {
			fixture.publishCalls++
			candidate := filepath.Join(artifactRoot, "forecast", "runs", indexID)
			entries, err := os.ReadDir(candidate)
			if err != nil {
				return forecastruns.Pointer{}, err
			}
			for _, entry := range entries {
				if entry.Name() != "forecast-days" && entry.Name() != "forecast-ranges" && entry.Name() != ".install.lock" {
					return forecastruns.Pointer{}, fmt.Errorf("publish saw unexpected candidate entry %s", entry.Name())
				}
			}
			for day := 0; day < productDays; day++ {
				date := fixture.now.AddDate(0, 0, 1+day).Format(time.DateOnly)
				if _, err := os.Stat(filepath.Join(candidate, "forecast-days", date, forecastpoints.TierGlobal)); err != nil {
					return forecastruns.Pointer{}, fmt.Errorf("publish saw incomplete global day %s: %w", date, err)
				}
			}
			if _, err := os.Stat(filepath.Join(candidate, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal, forecastrange.CatalogFilename)); err != nil {
				return forecastruns.Pointer{}, fmt.Errorf("publish saw incomplete forecast range: %w", err)
			}
			if _, err := os.Stat(filepath.Join(artifactRoot, "forecast", ".candidate-"+indexID)); !errors.Is(err, os.ErrNotExist) {
				return forecastruns.Pointer{}, errors.New("publish ran before atomic candidate install")
			}
			fixture.events = append(fixture.events, "publish")
			if err := os.WriteFile(filepath.Join(artifactRoot, "forecast", "current.json"), []byte(indexID+"\n"), 0o644); err != nil {
				return forecastruns.Pointer{}, err
			}
			return forecastruns.Pointer{Current: forecastruns.IndexReference{IndexID: indexID}}, nil
		},
		freeBytes: func(string) (int64, uint64, error) {
			return forecastProductionMinimumFreeBytes, 1, nil
		},
	}
}

func fakeIFSStaged(run time.Time, steps []int) []stagedFrame {
	frames := make([]stagedFrame, 0, len(steps)*len(ifs.RequiredSurfaceFields()))
	for _, parameter := range ifs.RequiredSurfaceFields() {
		field, _ := ifsPipelineField(parameter)
		for _, step := range steps {
			frames = append(frames, stagedFrame{field: field, step: step, run: run, resolution: 28, spacing: .25})
		}
	}
	return frames
}

func fakeICONStaged(run time.Time, steps []int) []stagedFrame {
	frames := make([]stagedFrame, 0, len(steps)*len(dwd.RequiredFields()))
	for _, source := range dwd.RequiredFields() {
		field, _ := dwdPipelineField(source.Name)
		for _, step := range steps {
			frames = append(frames, stagedFrame{field: field, step: step, run: run, resolution: 7, spacing: .0625})
		}
	}
	return frames
}

func writeFakeStage(root, name string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, name+".grib2"), []byte(name), 0o600)
}

func assertNoForecastPointer(t *testing.T, artifactRoot string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(artifactRoot, "forecast", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed forecast transaction changed current pointer: %v", err)
	}
}

func assertNoForecastSourceScratch(t *testing.T, stagingRoot string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(stagingRoot, "forecast-source-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("forecast source scratch remains: %v, %v", matches, err)
	}
}
