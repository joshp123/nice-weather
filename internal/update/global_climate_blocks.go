package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

const (
	// DefaultGlobalClimateWorkers is the fixed production concurrency. Tests may
	// lower it, but production must not turn climate bootstrap into a tuning
	// surface without new memory evidence.
	DefaultGlobalClimateWorkers = 4

	firstGlobalClimateLatitudeBlock  = 3
	lastGlobalClimateLatitudeBlock   = 109
	firstGlobalClimateLongitudeBlock = 0
	lastGlobalClimateLongitudeBlock  = 112
)

// GlobalClimateBlockSummary is deterministic build evidence. Completed holds
// only land-block checkpoint identities, in canonical north-to-south then
// west-to-east order. Water-only blocks have no checkpoint by design.
type GlobalClimateBlockSummary struct {
	Blocks               int
	SourceCells          int
	IncludedCells        int
	LandCells            int
	UnsupportedLandCells int
	WaterCells           int
	CroppedCells         int
	Reused               int
	Built                int
	WaterSkipped         int
	CheckpointBytes      int64
	TemperatureRepairs   int64
	MaximumInversionC    float64
	Completed            []climatebuild.CheckpointIdentity
}

type globalClimateBlockTask struct {
	index int
	block agera5.BlockCoordinate
}

type globalClimateBlockOutcome struct {
	index                int
	identity             climatebuild.CheckpointIdentity
	sourceCells          int
	includedCells        int
	landCells            int
	unsupportedLandCells int
	waterCells           int
	croppedCells         int
	reused               bool
	built                bool
	waterSkipped         bool
	checkpointBytes      int64
	temperatureRepairs   int
	maximumInversion     float64
	err                  error
}

// RunGlobalClimateBlocks builds or resumes every AgERA5 land block that
// intersects the canonical worldwide climate crop. checkpointRoot must be an
// existing absolute directory so same-directory temporary writes and renames
// remain transactional on an external APFS volume.
func RunGlobalClimateBlocks(
	ctx context.Context,
	checkpointRoot string,
	source climatebuild.Source,
	worldLand pipeline.FloatGrid,
	sourceEvidenceSHA256 [sha256.Size]byte,
	workers int,
) (GlobalClimateBlockSummary, error) {
	if err := validateGlobalClimateBlockInputs(ctx, checkpointRoot, source, worldLand, sourceEvidenceSHA256, workers); err != nil {
		return GlobalClimateBlockSummary{}, err
	}
	tasks := globalClimateBlockTasks()
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan globalClimateBlockTask)
	results := make(chan globalClimateBlockOutcome, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for task := range jobs {
				outcome := runGlobalClimateBlock(workerContext, checkpointRoot, source, worldLand, sourceEvidenceSHA256, task)
				results <- outcome
			}
		}()
	}

	outcomes := make([]globalClimateBlockOutcome, len(tasks))
	completed := make([]bool, len(tasks))
	next, inFlight := 0, 0
	var firstError error
	for (firstError == nil && next < len(tasks)) || inFlight > 0 {
		if firstError != nil {
			outcome := <-results
			inFlight--
			outcomes[outcome.index] = outcome
			completed[outcome.index] = true
			continue
		}
		if err := ctx.Err(); err != nil {
			firstError = err
			cancel()
			continue
		}

		var send chan globalClimateBlockTask
		var task globalClimateBlockTask
		if next < len(tasks) && inFlight < workers {
			send = jobs
			task = tasks[next]
		}
		select {
		case send <- task:
			next++
			inFlight++
			if err := ctx.Err(); err != nil {
				firstError = err
				cancel()
			}
		case outcome := <-results:
			inFlight--
			outcomes[outcome.index] = outcome
			completed[outcome.index] = true
			if outcome.err != nil {
				firstError = outcome.err
				cancel()
			}
		}
	}
	close(jobs)
	group.Wait()
	if firstError != nil {
		return GlobalClimateBlockSummary{}, firstError
	}
	if next != len(tasks) {
		return GlobalClimateBlockSummary{}, fmt.Errorf("global climate build scheduled %d of %d blocks", next, len(tasks))
	}
	for index := range completed {
		if !completed[index] || outcomes[index].err != nil {
			return GlobalClimateBlockSummary{}, fmt.Errorf("global climate block %d did not complete", index)
		}
	}
	return summarizeGlobalClimateBlocks(outcomes)
}

func validateGlobalClimateBlockInputs(
	ctx context.Context,
	checkpointRoot string,
	source climatebuild.Source,
	worldLand pipeline.FloatGrid,
	sourceEvidenceSHA256 [sha256.Size]byte,
	workers int,
) error {
	if ctx == nil {
		return errors.New("nil global climate context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil {
		return errors.New("nil global climate source")
	}
	if checkpointRoot == "" || !filepath.IsAbs(checkpointRoot) || filepath.Clean(checkpointRoot) != checkpointRoot || checkpointRoot == string(filepath.Separator) {
		return errors.New("global climate checkpoint root must be a clean absolute directory")
	}
	info, err := os.Stat(checkpointRoot)
	if err != nil {
		return fmt.Errorf("stat global climate checkpoint root: %w", err)
	}
	if !info.IsDir() {
		return errors.New("global climate checkpoint root is not a directory")
	}
	if workers < 1 || workers > DefaultGlobalClimateWorkers {
		return fmt.Errorf("global climate workers must be between 1 and %d", DefaultGlobalClimateWorkers)
	}
	if sourceEvidenceSHA256 == ([sha256.Size]byte{}) {
		return errors.New("global climate source evidence SHA-256 is required")
	}
	if err := worldLand.Validate(); err != nil {
		return fmt.Errorf("worldwide climate land mask: %w", err)
	}
	if worldLand.Spec != climatebuild.WorldwideGridSpec() {
		return errors.New("land mask does not use the canonical worldwide climate grid")
	}
	for index, value := range worldLand.Values {
		if value != 0 && value != 1 {
			return fmt.Errorf("worldwide climate land mask cell %d has value %g; want 0 or 1", index, value)
		}
	}
	return nil
}

func globalClimateBlockTasks() []globalClimateBlockTask {
	count := (lastGlobalClimateLatitudeBlock - firstGlobalClimateLatitudeBlock + 1) *
		(lastGlobalClimateLongitudeBlock - firstGlobalClimateLongitudeBlock + 1)
	tasks := make([]globalClimateBlockTask, 0, count)
	for latitude := lastGlobalClimateLatitudeBlock; latitude >= firstGlobalClimateLatitudeBlock; latitude-- {
		for longitude := firstGlobalClimateLongitudeBlock; longitude <= lastGlobalClimateLongitudeBlock; longitude++ {
			tasks = append(tasks, globalClimateBlockTask{
				index: len(tasks),
				block: agera5.BlockCoordinate{Latitude: latitude, Longitude: longitude},
			})
		}
	}
	return tasks
}

func runGlobalClimateBlock(
	ctx context.Context,
	checkpointRoot string,
	source climatebuild.Source,
	worldLand pipeline.FloatGrid,
	sourceEvidenceSHA256 [sha256.Size]byte,
	task globalClimateBlockTask,
) globalClimateBlockOutcome {
	outcome := globalClimateBlockOutcome{index: task.index}
	if err := ctx.Err(); err != nil {
		outcome.err = err
		return outcome
	}
	land, err := climatebuild.BlockLandMask(worldLand, task.block)
	if err != nil {
		outcome.err = fmt.Errorf("derive global climate block %+v land mask: %w", task.block, err)
		return outcome
	}
	height, width, err := agera5.BlockShape(task.block)
	if err != nil {
		outcome.err = err
		return outcome
	}
	outcome.sourceCells = width * height
	maskDigest := sha256.New()
	for latitudeOffset := range height {
		for longitudeOffset := range width {
			index := latitudeOffset*width + longitudeOffset
			if land[index] {
				outcome.landCells++
				_, _ = maskDigest.Write([]byte{1})
			} else {
				_, _ = maskDigest.Write([]byte{0})
			}
			sourceLatitude, sourceLongitude, locateErr := climatebuild.Locate(task.block, latitudeOffset, longitudeOffset)
			if locateErr != nil {
				outcome.err = locateErr
				return outcome
			}
			if _, included := climatebuild.WorldIndex(sourceLatitude, sourceLongitude); included {
				outcome.includedCells++
			}
		}
	}
	outcome.croppedCells = outcome.sourceCells - outcome.includedCells
	outcome.waterCells = outcome.includedCells - outcome.landCells
	path := globalClimateCheckpointPath(checkpointRoot, task.block)
	if outcome.landCells == 0 {
		if _, err := os.Lstat(path); err == nil {
			outcome.err = fmt.Errorf("water-only global climate block %+v has an existing final checkpoint", task.block)
			return outcome
		} else if !os.IsNotExist(err) {
			outcome.err = fmt.Errorf("inspect water-only global climate block %+v checkpoint: %w", task.block, err)
			return outcome
		}
		outcome.waterSkipped = true
		return outcome
	}
	copy(outcome.identity.NaturalEarthMaskSHA256[:], maskDigest.Sum(nil))
	outcome.identity.Block = task.block
	outcome.identity.Width = width
	outcome.identity.Height = height
	outcome.identity.SourceEvidenceSHA256 = sourceEvidenceSHA256

	checkpoint, found, err := climatebuild.LoadCheckpoint(path, outcome.identity)
	if err != nil {
		outcome.err = fmt.Errorf("load global climate block %+v checkpoint: %w", task.block, err)
		return outcome
	}
	if found {
		_, outcome.unsupportedLandCells, err = globalClimateCheckpointSupport(checkpoint, land)
		if err != nil {
			outcome.err = fmt.Errorf("validate global climate block %+v checkpoint support: %w", task.block, err)
			return outcome
		}
		outcome.reused = true
		outcome.temperatureRepairs = checkpoint.TemperatureRepairs
		outcome.maximumInversion = checkpoint.MaximumTemperatureRepair
		outcome.checkpointBytes, outcome.err = globalClimateCheckpointBytes(path)
		return outcome
	}

	result, err := climatebuild.BuildLandBlock(ctx, source, task.block, climate.DefaultReferenceWindow(), land)
	if err != nil {
		outcome.err = fmt.Errorf("build global climate block %+v: %w", task.block, err)
		return outcome
	}
	if result.Width != width || result.Height != height || result.Stats.LogicalCells != outcome.sourceCells ||
		result.Stats.LandCells != outcome.landCells || result.Stats.WaterCells != outcome.sourceCells-outcome.landCells ||
		result.Stats.CompleteCells+result.Stats.UnsupportedLandCells != outcome.landCells ||
		result.Stats.NoDataCells != outcome.sourceCells-outcome.landCells+result.Stats.UnsupportedLandCells || result.Stats.SparseOcean {
		outcome.err = fmt.Errorf("global climate block %+v returned incomplete land evidence", task.block)
		return outcome
	}
	outcome.unsupportedLandCells = result.Stats.UnsupportedLandCells
	if err := ctx.Err(); err != nil {
		outcome.err = err
		return outcome
	}
	checkpoint = climatebuild.Checkpoint{
		Identity:                 outcome.identity,
		Months:                   result.Months,
		TemperatureRepairs:       result.Stats.TemperatureRepairs,
		MaximumTemperatureRepair: result.Stats.MaximumTemperatureRepair,
	}
	if err := climatebuild.WriteCheckpoint(path, checkpoint); err != nil {
		outcome.err = fmt.Errorf("write global climate block %+v checkpoint: %w", task.block, err)
		return outcome
	}
	outcome.built = true
	outcome.temperatureRepairs = checkpoint.TemperatureRepairs
	outcome.maximumInversion = checkpoint.MaximumTemperatureRepair
	outcome.checkpointBytes, outcome.err = globalClimateCheckpointBytes(path)
	return outcome
}

func globalClimateCheckpointPath(root string, block agera5.BlockCoordinate) string {
	return filepath.Join(root, fmt.Sprintf("block-%03d-%03d.nwc", block.Latitude, block.Longitude))
}

func globalClimateCheckpointBytes(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat global climate checkpoint: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return 0, errors.New("global climate checkpoint is not a non-empty regular file")
	}
	return info.Size(), nil
}

func globalClimateCheckpointSupport(checkpoint climatebuild.Checkpoint, land []bool) (completeLand, unsupportedLand int, err error) {
	cells := checkpoint.Identity.Width * checkpoint.Identity.Height
	if len(land) != cells {
		return 0, 0, fmt.Errorf("land mask has %d cells; want %d", len(land), cells)
	}
	for month := range checkpoint.Months {
		if len(checkpoint.Months[month]) != cells {
			return 0, 0, fmt.Errorf("checkpoint month %02d has %d cells; want %d", month+1, len(checkpoint.Months[month]), cells)
		}
	}
	for cell, isLand := range land {
		validMonths := 0
		for month := range checkpoint.Months {
			if checkpoint.Months[month][cell].Valid {
				validMonths++
			}
		}
		switch {
		case !isLand && validMonths != 0:
			return 0, 0, fmt.Errorf("water cell %d has %d valid months", cell, validMonths)
		case isLand && validMonths == len(checkpoint.Months):
			completeLand++
		case isLand && validMonths == 0:
			unsupportedLand++
		case isLand:
			return 0, 0, fmt.Errorf("land cell %d has %d of %d valid months", cell, validMonths, len(checkpoint.Months))
		}
	}
	return completeLand, unsupportedLand, nil
}

func summarizeGlobalClimateBlocks(outcomes []globalClimateBlockOutcome) (GlobalClimateBlockSummary, error) {
	summary := GlobalClimateBlockSummary{Blocks: len(outcomes)}
	for index, outcome := range outcomes {
		if outcome.index != index || outcome.err != nil {
			return GlobalClimateBlockSummary{}, fmt.Errorf("global climate outcome %d is invalid", index)
		}
		summary.SourceCells += outcome.sourceCells
		summary.IncludedCells += outcome.includedCells
		summary.LandCells += outcome.landCells
		summary.UnsupportedLandCells += outcome.unsupportedLandCells
		summary.WaterCells += outcome.waterCells
		summary.CroppedCells += outcome.croppedCells
		summary.CheckpointBytes += outcome.checkpointBytes
		summary.TemperatureRepairs += int64(outcome.temperatureRepairs)
		summary.MaximumInversionC = math.Max(summary.MaximumInversionC, outcome.maximumInversion)
		switch {
		case outcome.waterSkipped && !outcome.reused && !outcome.built && outcome.landCells == 0:
			summary.WaterSkipped++
		case outcome.reused != outcome.built && outcome.landCells > 0 && outcome.identity != (climatebuild.CheckpointIdentity{}):
			if outcome.reused {
				summary.Reused++
			} else {
				summary.Built++
			}
			summary.Completed = append(summary.Completed, outcome.identity)
		default:
			return GlobalClimateBlockSummary{}, fmt.Errorf("global climate block %+v has an incomplete outcome", outcome.identity.Block)
		}
	}
	if summary.IncludedCells != int(climatebuild.WorldwideGridSpec().Rows)*int(climatebuild.WorldwideGridSpec().Cols) ||
		summary.IncludedCells != summary.LandCells+summary.WaterCells || summary.SourceCells != summary.IncludedCells+summary.CroppedCells ||
		summary.UnsupportedLandCells < 0 || summary.UnsupportedLandCells > summary.LandCells ||
		summary.Blocks != summary.Reused+summary.Built+summary.WaterSkipped || len(summary.Completed) != summary.Reused+summary.Built {
		return GlobalClimateBlockSummary{}, errors.New("global climate block summary is incomplete")
	}
	return summary, nil
}
