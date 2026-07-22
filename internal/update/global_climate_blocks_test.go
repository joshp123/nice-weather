package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

type globalClimateTestReference struct {
	field       agera5.Field
	first, last time.Time
	block       agera5.BlockCoordinate
}

func (reference globalClimateTestReference) Field() agera5.Field           { return reference.field }
func (reference globalClimateTestReference) FirstDate() time.Time          { return reference.first }
func (reference globalClimateTestReference) LastDate() time.Time           { return reference.last }
func (reference globalClimateTestReference) Block() agera5.BlockCoordinate { return reference.block }

type globalClimateTestSource struct {
	mu                   sync.Mutex
	entries              int
	fetches              int
	active               int
	maximum              int
	blocked              map[agera5.BlockCoordinate]bool
	entered              chan struct{}
	release              chan struct{}
	fetchErr             error
	repairBlock          agera5.BlockCoordinate
	unsupported          bool
	unsupportedBlock     agera5.BlockCoordinate
	unsupportedLatitude  int
	unsupportedLongitude int
}

func (source *globalClimateTestSource) Shape(block agera5.BlockCoordinate) (int, int, error) {
	source.mu.Lock()
	source.entries++
	source.mu.Unlock()
	height, width, err := agera5.BlockShape(block)
	return width, height, err
}

func (source *globalClimateTestSource) Plan(first, last time.Time, block agera5.BlockCoordinate) ([]climatebuild.ChunkReference, error) {
	if first.Format(time.DateOnly) != "2006-01-01" || last.Format(time.DateOnly) != "2025-12-31" {
		return nil, errors.New("unexpected reference window")
	}
	fields := agera5.RequiredFields()
	references := make([]climatebuild.ChunkReference, len(fields))
	for index, field := range fields {
		references[index] = globalClimateTestReference{field: field, first: first, last: last, block: block}
	}
	return references, nil
}

func (source *globalClimateTestSource) Fetch(ctx context.Context, raw climatebuild.ChunkReference) (climatebuild.Block, error) {
	reference := raw.(globalClimateTestReference)
	source.mu.Lock()
	source.fetches++
	shouldBlock := source.release != nil && !source.blocked[reference.block]
	if shouldBlock {
		if source.blocked == nil {
			source.blocked = make(map[agera5.BlockCoordinate]bool)
		}
		source.blocked[reference.block] = true
		source.active++
		if source.active > source.maximum {
			source.maximum = source.active
		}
	}
	source.mu.Unlock()
	if shouldBlock {
		if source.entered != nil {
			source.entered <- struct{}{}
		}
		select {
		case <-source.release:
		case <-ctx.Done():
			source.mu.Lock()
			source.active--
			source.mu.Unlock()
			return nil, ctx.Err()
		}
		source.mu.Lock()
		source.active--
		source.mu.Unlock()
	}
	if source.fetchErr != nil {
		return nil, source.fetchErr
	}
	height, width, err := agera5.BlockShape(reference.block)
	if err != nil {
		return nil, err
	}
	return globalClimateTestBlock{
		reference: reference, width: width, height: height, repair: reference.block == source.repairBlock,
		unsupported:         source.unsupported && reference.block == source.unsupportedBlock,
		unsupportedLatitude: source.unsupportedLatitude, unsupportedLongitude: source.unsupportedLongitude,
	}, nil
}

func (source *globalClimateTestSource) counts() (fetches, maximum int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.fetches, source.maximum
}

func (source *globalClimateTestSource) taskEntries() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.entries
}

type globalClimateTestBlock struct {
	reference                                 globalClimateTestReference
	width, height                             int
	repair                                    bool
	unsupported                               bool
	unsupportedLatitude, unsupportedLongitude int
}

func (block globalClimateTestBlock) Reference() climatebuild.ChunkReference { return block.reference }
func (block globalClimateTestBlock) TimeCount() int                         { return 20*365 + 5 }
func (block globalClimateTestBlock) LatitudeCount() int                     { return block.height }
func (block globalClimateTestBlock) LongitudeCount() int                    { return block.width }

func (block globalClimateTestBlock) Date(offset int) (time.Time, error) {
	if offset < 0 || offset >= block.TimeCount() {
		return time.Time{}, errors.New("date offset outside test block")
	}
	return block.reference.first.AddDate(0, 0, offset), nil
}

func (block globalClimateTestBlock) Location(latitudeOffset, longitudeOffset int) (float64, float64, error) {
	if latitudeOffset < 0 || latitudeOffset >= block.height || longitudeOffset < 0 || longitudeOffset >= block.width {
		return 0, 0, errors.New("location outside test block")
	}
	latitudeIndex := block.reference.block.Latitude*16 + latitudeOffset
	longitudeIndex := block.reference.block.Longitude*32 + longitudeOffset
	return -90 + float64(latitudeIndex)/10, -180 + float64(longitudeIndex)/10, nil
}

func (block globalClimateTestBlock) Value(timeOffset, latitudeOffset, longitudeOffset int) (float32, bool, error) {
	if timeOffset < 0 || timeOffset >= block.TimeCount() || latitudeOffset < 0 || latitudeOffset >= block.height || longitudeOffset < 0 || longitudeOffset >= block.width {
		return 0, false, errors.New("value outside test block")
	}
	if block.unsupported && latitudeOffset == block.unsupportedLatitude && longitudeOffset == block.unsupportedLongitude {
		return 0, false, nil
	}
	switch block.reference.field {
	case agera5.DaytimeMeanTemperature:
		if block.repair && timeOffset == 2 {
			return 284, true, nil
		}
		return 295, true, nil
	case agera5.MinimumTemperature:
		if block.repair && timeOffset == 0 {
			return 301, true, nil
		}
		return 290, true, nil
	case agera5.MaximumTemperature:
		return 300, true, nil
	case agera5.MeanDewPoint:
		if block.repair && timeOffset == 1 {
			return 302.5, true, nil
		}
		return 285, true, nil
	case agera5.Precipitation:
		return 0, true, nil
	case agera5.PrecipitationDuration:
		return 0, true, nil
	case agera5.SolarRadiation:
		return 20_000_000, true, nil
	case agera5.CloudCover:
		return 0.2, true, nil
	case agera5.MeanWindSpeed:
		return 2, true, nil
	default:
		return 0, false, errors.New("unknown test field")
	}
}

func TestGlobalClimateBlockEnumerationCoversExactWorldwideCrop(t *testing.T) {
	tasks := globalClimateBlockTasks()
	if len(tasks) != 107*113 {
		t.Fatalf("blocks = %d, want %d", len(tasks), 107*113)
	}
	if got, want := tasks[0].block, (agera5.BlockCoordinate{Latitude: 109, Longitude: 0}); got != want {
		t.Fatalf("first block = %+v, want %+v", got, want)
	}
	if got, want := tasks[112].block, (agera5.BlockCoordinate{Latitude: 109, Longitude: 112}); got != want {
		t.Fatalf("first row last block = %+v, want %+v", got, want)
	}
	if got, want := tasks[113].block, (agera5.BlockCoordinate{Latitude: 108, Longitude: 0}); got != want {
		t.Fatalf("second row first block = %+v, want %+v", got, want)
	}
	if got, want := tasks[len(tasks)-1].block, (agera5.BlockCoordinate{Latitude: 3, Longitude: 112}); got != want {
		t.Fatalf("last block = %+v, want %+v", got, want)
	}
	for index, task := range tasks {
		if task.index != index {
			t.Fatalf("task %d has index %d", index, task.index)
		}
	}
}

func TestGlobalClimateBlocksSkipWaterBeforeSourceFetch(t *testing.T) {
	source := &globalClimateTestSource{}
	summary, err := RunGlobalClimateBlocks(context.Background(), t.TempDir(), source, globalClimateMask(t), testSourceDigest(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if fetches, _ := source.counts(); fetches != 0 {
		t.Fatalf("source fetches = %d, want 0", fetches)
	}
	if summary.Blocks != 107*113 || summary.WaterSkipped != summary.Blocks || summary.Built != 0 || summary.Reused != 0 || len(summary.Completed) != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	wantCells := int(climatebuild.WorldwideGridSpec().Rows) * int(climatebuild.WorldwideGridSpec().Cols)
	if summary.IncludedCells != wantCells || summary.WaterCells != wantCells || summary.LandCells != 0 {
		t.Fatalf("cell summary = %+v, want %d included water cells", summary, wantCells)
	}
}

func TestGlobalClimateBlocksBuildOneLandCheckpointAndResumeWithoutFetch(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	block := agera5.BlockCoordinate{Latitude: 109, Longitude: 0}
	setGlobalClimateLand(t, &mask, 1750, 1)
	source := &globalClimateTestSource{repairBlock: block}

	built, err := RunGlobalClimateBlocks(context.Background(), root, source, mask, testSourceDigest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if built.Built != 1 || built.Reused != 0 || built.WaterSkipped != built.Blocks-1 || len(built.Completed) != 1 || built.Completed[0].Block != block {
		t.Fatalf("built summary = %+v", built)
	}
	if built.TemperatureRepairs != 1 || math.Abs(built.MaximumInversionC-1) > 1e-9 || built.ComfortTemperatureRepairs != 1 || math.Abs(built.MaximumComfortTemperatureAdjustmentC-6) > 1e-9 || built.DewPointRepairs != 1 || math.Abs(built.MaximumDewPointCapC-2.5) > 1e-9 || built.CheckpointBytes <= 0 {
		t.Fatalf("build evidence = %+v", built)
	}
	checkpoint, found, err := climatebuild.LoadCheckpoint(globalClimateCheckpointPath(root, block), built.Completed[0])
	if err != nil || !found {
		t.Fatalf("load checkpoint found=%t error=%v", found, err)
	}
	if checkpoint.TemperatureRepairs != 1 || checkpoint.ComfortTemperatureRepairs != 1 || checkpoint.MaximumComfortTemperatureRepair != 6 || checkpoint.DewPointRepairs != 1 || checkpoint.MaximumDewPointRepair != 2.5 || len(checkpoint.Months[0]) != 16*32 {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}

	resumeSource := &globalClimateTestSource{fetchErr: errors.New("resume fetched source")}
	resumed, err := RunGlobalClimateBlocks(context.Background(), root, resumeSource, mask, testSourceDigest(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if fetches, _ := resumeSource.counts(); fetches != 0 {
		t.Fatalf("resume source fetches = %d, want 0", fetches)
	}
	if resumed.Reused != 1 || resumed.Built != 0 || resumed.CheckpointBytes != built.CheckpointBytes || !reflect.DeepEqual(resumed.Completed, built.Completed) {
		t.Fatalf("resumed summary = %+v; built = %+v", resumed, built)
	}
}

func TestGlobalClimateBlocksReuseExistingLegacyV1Checkpoint(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	block := agera5.BlockCoordinate{Latitude: 109, Longitude: 0}
	setGlobalClimateLand(t, &mask, 1750, 1)

	built, err := RunGlobalClimateBlocks(context.Background(), root, &globalClimateTestSource{}, mask, testSourceDigest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	path := globalClimateCheckpointPath(root, block)
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for offset, value := range encoded[216:232] {
		if value != 0 {
			t.Fatalf("legacy v1 reserved byte %d is nonzero", 216+offset)
		}
	}

	resumeSource := &globalClimateTestSource{fetchErr: errors.New("legacy v1 resume fetched source")}
	resumed, err := RunGlobalClimateBlocks(context.Background(), root, resumeSource, mask, testSourceDigest(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if fetches, _ := resumeSource.counts(); fetches != 0 {
		t.Fatalf("legacy v1 resume fetched %d source chunks", fetches)
	}
	if built.Built != 1 || resumed.Reused != 1 || resumed.Built != 0 || resumed.DewPointRepairs != 0 || !reflect.DeepEqual(resumed.Completed, built.Completed) {
		t.Fatalf("legacy v1 reuse failed: built=%+v resumed=%+v", built, resumed)
	}
}

func TestGlobalClimateBlocksCountStructurallyUnsupportedLandAcrossBuildAndResume(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	block := agera5.BlockCoordinate{Latitude: 109, Longitude: 0}
	setGlobalClimateLand(t, &mask, 1749, 1)
	setGlobalClimateLand(t, &mask, 1750, 1)
	source := &globalClimateTestSource{
		unsupported: true, unsupportedBlock: block,
		unsupportedLatitude: 5, unsupportedLongitude: 1,
	}

	built, err := RunGlobalClimateBlocks(context.Background(), root, source, mask, testSourceDigest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if built.LandCells != 2 || built.UnsupportedLandCells != 1 || built.Built != 1 {
		t.Fatalf("built support summary = %+v", built)
	}
	checkpoint, found, err := climatebuild.LoadCheckpoint(globalClimateCheckpointPath(root, block), built.Completed[0])
	if err != nil || !found {
		t.Fatalf("load checkpoint found=%t error=%v", found, err)
	}
	if checkpoint.Months[0][5*32+1].Valid || !checkpoint.Months[0][6*32+1].Valid {
		t.Fatalf("checkpoint support cells are not deterministic")
	}

	resumed, err := RunGlobalClimateBlocks(context.Background(), root, &globalClimateTestSource{fetchErr: errors.New("resume fetched source")}, mask, testSourceDigest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.UnsupportedLandCells != 1 || resumed.Reused != 1 || resumed.Built != 0 {
		t.Fatalf("resumed support summary = %+v", resumed)
	}
}

func TestGlobalClimateCheckpointSupportRejectsPartialMonthsAndWaterEvidence(t *testing.T) {
	checkpoint := climatebuild.Checkpoint{Identity: climatebuild.CheckpointIdentity{Width: 2, Height: 1}}
	for month := range checkpoint.Months {
		checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, 2)
		checkpoint.Months[month][0].Valid = true
	}
	if complete, unsupported, err := globalClimateCheckpointSupport(checkpoint, []bool{true, true}); err != nil || complete != 1 || unsupported != 1 {
		t.Fatalf("support = complete %d unsupported %d error %v", complete, unsupported, err)
	}
	checkpoint.Months[0][1].Valid = true
	if _, _, err := globalClimateCheckpointSupport(checkpoint, []bool{true, true}); err == nil || !strings.Contains(err.Error(), "1 of 12 valid months") {
		t.Fatalf("partial-month error = %v", err)
	}
	if _, _, err := globalClimateCheckpointSupport(checkpoint, []bool{false, true}); err == nil || !strings.Contains(err.Error(), "water cell 0") {
		t.Fatalf("water evidence error = %v", err)
	}
}

func TestGlobalClimateBlocksRejectCheckpointSourceMaskAndPayloadMismatch(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	block := agera5.BlockCoordinate{Latitude: 109, Longitude: 0}
	setGlobalClimateLand(t, &mask, 1750, 1)
	if _, err := RunGlobalClimateBlocks(context.Background(), root, &globalClimateTestSource{}, mask, testSourceDigest(), 1); err != nil {
		t.Fatal(err)
	}

	t.Run("source", func(t *testing.T) {
		source := &globalClimateTestSource{fetchErr: errors.New("must not fetch")}
		_, err := RunGlobalClimateBlocks(context.Background(), root, source, mask, sha256.Sum256([]byte("different source contract")), 1)
		if !errors.Is(err, climatebuild.ErrCheckpointMismatch) {
			t.Fatalf("error = %v", err)
		}
		if fetches, _ := source.counts(); fetches != 0 {
			t.Fatalf("fetches = %d", fetches)
		}
	})

	t.Run("mask", func(t *testing.T) {
		changed := globalClimateMask(t)
		setGlobalClimateLand(t, &changed, 1750, 2)
		source := &globalClimateTestSource{fetchErr: errors.New("must not fetch")}
		_, err := RunGlobalClimateBlocks(context.Background(), root, source, changed, testSourceDigest(), 1)
		if !errors.Is(err, climatebuild.ErrCheckpointMismatch) {
			t.Fatalf("error = %v", err)
		}
		if fetches, _ := source.counts(); fetches != 0 {
			t.Fatalf("fetches = %d", fetches)
		}
	})

	t.Run("land becomes water", func(t *testing.T) {
		source := &globalClimateTestSource{fetchErr: errors.New("must not fetch")}
		_, err := RunGlobalClimateBlocks(context.Background(), root, source, globalClimateMask(t), testSourceDigest(), 1)
		if err == nil || !strings.Contains(err.Error(), "water-only") || !strings.Contains(err.Error(), "existing final checkpoint") {
			t.Fatalf("error = %v", err)
		}
		if fetches, _ := source.counts(); fetches != 0 {
			t.Fatalf("fetches = %d", fetches)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		path := globalClimateCheckpointPath(root, block)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload[:len(payload)-1], 0o600); err != nil {
			t.Fatal(err)
		}
		source := &globalClimateTestSource{fetchErr: errors.New("must not fetch")}
		_, err = RunGlobalClimateBlocks(context.Background(), root, source, mask, testSourceDigest(), 1)
		if err == nil || !strings.Contains(err.Error(), "header declares") {
			t.Fatalf("error = %v", err)
		}
		if fetches, _ := source.counts(); fetches != 0 {
			t.Fatalf("fetches = %d", fetches)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || info.Size() != int64(len(payload)-1) {
			t.Fatalf("invalid checkpoint was replaced: size=%d error=%v", info.Size(), statErr)
		}
	})
}

func TestGlobalClimateBlocksCancellationStopsSchedulingAndLeavesNoFinals(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	for longitude := 0; longitude < 8; longitude++ {
		setGlobalClimateLand(t, &mask, 1750, max(1, longitude*32))
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	source := &globalClimateTestSource{entered: entered, release: release}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RunGlobalClimateBlocks(ctx, root, source, mask, testSourceDigest(), 2)
		done <- err
	}()
	<-entered
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if fetches, maximum := source.counts(); fetches != 2 || maximum != 2 {
		t.Fatalf("fetches=%d maximum=%d, want 2 and 2", fetches, maximum)
	}
	if entries := source.taskEntries(); entries != 2 {
		t.Fatalf("entered tasks = %d, want 2; cancellation scheduled new work", entries)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.nwc"))
	if err != nil || len(files) != 0 {
		t.Fatalf("final checkpoints = %v error=%v", files, err)
	}
}

func TestGlobalClimateBlocksFirstFailureDoesNotScheduleRemainingBlocksOrDeadlock(t *testing.T) {
	root := t.TempDir()
	mask := globalClimateMask(t)
	for longitude := 0; longitude < 8; longitude++ {
		setGlobalClimateLand(t, &mask, 1750, max(1, longitude*32))
	}
	source := &globalClimateTestSource{fetchErr: errors.New("source failed")}
	done := make(chan error, 1)
	go func() {
		_, err := RunGlobalClimateBlocks(context.Background(), root, source, mask, testSourceDigest(), 1)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "source failed") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner deadlocked after first block failure")
	}
	if fetches, maximum := source.counts(); fetches != 1 || maximum != 0 {
		t.Fatalf("fetches=%d maximum=%d, want 1 and 0", fetches, maximum)
	}
}

func TestGlobalClimateBlocksHonourWorkerBoundAndSummariesAreDeterministic(t *testing.T) {
	mask := globalClimateMask(t)
	for longitude := 0; longitude < 4; longitude++ {
		setGlobalClimateLand(t, &mask, 1750, max(1, longitude*32))
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	concurrentSource := &globalClimateTestSource{entered: entered, release: release}
	concurrentDone := make(chan struct {
		summary GlobalClimateBlockSummary
		err     error
	}, 1)
	go func() {
		summary, err := RunGlobalClimateBlocks(context.Background(), t.TempDir(), concurrentSource, mask, testSourceDigest(), 4)
		concurrentDone <- struct {
			summary GlobalClimateBlockSummary
			err     error
		}{summary, err}
	}()
	for range 4 {
		<-entered
	}
	if _, maximum := concurrentSource.counts(); maximum != 4 {
		t.Fatalf("maximum active blocks = %d, want 4", maximum)
	}
	close(release)
	concurrent := <-concurrentDone
	if concurrent.err != nil {
		t.Fatal(concurrent.err)
	}

	serial, err := RunGlobalClimateBlocks(context.Background(), t.TempDir(), &globalClimateTestSource{}, mask, testSourceDigest(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(concurrent.summary, serial) {
		t.Fatalf("concurrent summary differs\nconcurrent: %+v\nserial: %+v", concurrent.summary, serial)
	}
}

func TestGlobalClimateBlocksValidateInputsAndIgnoreInterruptedTemporaryFile(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, ".climate-checkpoint-interrupted.tmp")
	if err := os.WriteFile(temporary, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGlobalClimateBlocks(context.Background(), root, &globalClimateTestSource{}, globalClimateMask(t), testSourceDigest(), 1); err != nil {
		t.Fatal(err)
	}
	if payload, err := os.ReadFile(temporary); err != nil || string(payload) != "interrupted" {
		t.Fatalf("temporary payload=%q error=%v", payload, err)
	}

	tests := []struct {
		name    string
		root    string
		mask    pipeline.FloatGrid
		digest  [sha256.Size]byte
		workers int
	}{
		{"relative root", "relative", globalClimateMask(t), testSourceDigest(), 1},
		{"zero digest", root, globalClimateMask(t), [sha256.Size]byte{}, 1},
		{"zero workers", root, globalClimateMask(t), testSourceDigest(), 0},
		{"too many workers", root, globalClimateMask(t), testSourceDigest(), 5},
		{"wrong geometry", root, pipeline.FloatGrid{Spec: climatebuild.WorldwideGridSpec(), Values: []float32{0}}, testSourceDigest(), 1},
	}
	invalidValue := globalClimateMask(t)
	invalidValue.Values[1] = 2
	tests = append(tests, struct {
		name    string
		root    string
		mask    pipeline.FloatGrid
		digest  [sha256.Size]byte
		workers int
	}{"non-binary mask", root, invalidValue, testSourceDigest(), 1})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RunGlobalClimateBlocks(context.Background(), test.root, &globalClimateTestSource{}, test.mask, test.digest, test.workers); err == nil {
				t.Fatal("expected input error")
			}
		})
	}
}

func globalClimateMask(t *testing.T) pipeline.FloatGrid {
	t.Helper()
	spec := climatebuild.WorldwideGridSpec()
	return pipeline.FloatGrid{Spec: spec, Values: make([]float32, int(spec.Rows)*int(spec.Cols))}
}

func setGlobalClimateLand(t *testing.T, mask *pipeline.FloatGrid, sourceLatitude, sourceLongitude int) {
	t.Helper()
	index, included := climatebuild.WorldIndex(sourceLatitude, sourceLongitude)
	if !included {
		t.Fatalf("source cell %d,%d is outside worldwide crop", sourceLatitude, sourceLongitude)
	}
	mask.Values[index] = 1
}

func testSourceDigest() [sha256.Size]byte {
	return sha256.Sum256([]byte("validated AgERA5 source contract fixture"))
}
