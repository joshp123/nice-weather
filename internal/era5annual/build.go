// Package era5annual reads each land source tile once and checkpoints complete
// 2006–2025 monthly judgements before publishing the twelve-month product.
package era5annual

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/era5climate"
	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/providers/era5hourly"
)

type Config struct {
	PlanPath, PlanSHA256                            string
	AuthorityPath, AuthoritySHA256, DisplayMaskPath string
	WorkRoot, ProductRoot                           string
	SourceTiles                                     []string
	Progress                                        io.Writer
}

type Summary struct {
	Tiles, CompletedTiles, Cells, Months int
	Elapsed                              time.Duration
}

type sourceTile struct {
	Row     int   `json:"row"`
	Column  int   `json:"column"`
	Indexes []int `json:"-"`
}

type tileCheckpoint struct {
	SchemaVersion          int
	PlanSHA256, SnapshotID string
	Row, Column            int
	AcquisitionLayout      string
	Indexes                []int
	Results                [12][]era5climate.CellResult
}

type completedSource struct {
	Row             int     `json:"row"`
	Column          int     `json:"column"`
	Path            string  `json:"path"`
	ElapsedSeconds  float64 `json:"elapsedSeconds"`
	DecodedBytes    int64   `json:"decodedBytes"`
	CompressedBytes int64   `json:"compressedBytes"`
	SourceChunks    int     `json:"sourceChunks"`
	CPUSeconds      float64 `json:"cpuSeconds"`
	PeakRSSBytes    int64   `json:"peakRSSBytes"`
}

// Build resumes validated tile checkpoints. Only a complete world can reach
// the existing monthly builder and validated publication path.
func Build(ctx context.Context, config Config) (summary Summary, returnErr error) {
	started := time.Now()
	defer func() { summary.Elapsed = time.Since(started) }()
	if config.Progress == nil {
		config.Progress = io.Discard
	}
	for _, path := range []string{config.PlanPath, config.AuthorityPath, config.DisplayMaskPath, config.WorkRoot, config.ProductRoot} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return summary, errors.New("ERA5 annual paths must be clean absolute paths")
		}
	}
	plan, err := era5hourly.LoadCellPlan(config.PlanPath, config.PlanSHA256)
	if err != nil {
		return summary, err
	}
	authority, err := era5climateproduct.LoadPublicationAuthority(config.AuthorityPath, config.AuthoritySHA256, plan)
	if err != nil {
		return summary, err
	}
	if _, err := era5climateproduct.LoadDisplayMaskWithAuthority(config.DisplayMaskPath, plan, authority); err != nil {
		return summary, err
	}
	cells := plan.Cells()
	tiles := worldTiles(cells)
	summary.Cells = len(cells)
	summary.Tiles = len(tiles)
	if len(config.SourceTiles) > 0 {
		wanted := map[[2]int]bool{}
		for _, value := range config.SourceTiles {
			row, column, err := parseTile(value)
			if err != nil {
				return summary, err
			}
			wanted[[2]int{row, column}] = true
		}
		selected := []sourceTile{}
		for _, tile := range tiles {
			if wanted[[2]int{tile.Row, tile.Column}] {
				selected = append(selected, tile)
			}
		}
		if len(selected) != len(wanted) {
			return summary, errors.New("selected ERA5 source tile has no plan cells")
		}
		tiles = selected
	}
	checkpointRoot := filepath.Join(config.WorkRoot, "checkpoints", plan.SHA256())
	rawRoot := filepath.Join(config.WorkRoot, "raw")
	planRoot := filepath.Join(config.WorkRoot, "plans")
	for _, path := range []string{checkpointRoot, rawRoot, planRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return summary, err
		}
	}
	var monthlyPlans [12]era5hourly.CellPlan
	for month := 1; month <= 12; month++ {
		derived, err := era5hourly.DeriveMonthPlan(plan, config.PlanPath, config.PlanSHA256, filepath.Join(planRoot, fmt.Sprintf("world-%d-%02d.json.gz", plan.Year(), month)), time.Month(month))
		if err != nil {
			return summary, err
		}
		monthlyPlans[month-1] = derived
	}
	fmt.Fprintf(config.Progress, "ERA5 annual: %d land tiles, %d cells, 2006–2025, twelve exact civil calendars\n", summary.Tiles, summary.Cells)
	pending := []sourceTile{}
	for _, tile := range tiles {
		checkpointPath := filepath.Join(checkpointRoot, tileName(tile)+".gob")
		if _, err := readCheckpoint(checkpointPath, plan, tile, monthlyPlans); err == nil {
			summary.CompletedTiles++
			continue
		} else if !os.IsNotExist(err) {
			return summary, err
		}
		rawPath := filepath.Join(rawRoot, tileName(tile)+".raw")
		if _, err := os.Stat(rawPath); err == nil {
			if err := processTile(ctx, config, plan, monthlyPlans, tile, rawPath, checkpointPath); err != nil {
				return summary, err
			}
			summary.CompletedTiles++
		} else if !os.IsNotExist(err) {
			return summary, err
		} else {
			pending = append(pending, tile)
		}
	}
	if len(pending) > 0 {
		taskPath := filepath.Join(config.WorkRoot, "pending-tiles.json")
		task, err := json.Marshal(pending)
		if err != nil {
			return summary, err
		}
		if err := os.WriteFile(taskPath, task, 0o600); err != nil {
			return summary, err
		}
		command := exec.CommandContext(ctx, "uv", "run", "--quiet", "--script", "scripts/era5_annual_tiles.py", "--tiles", taskPath, "--out", rawRoot)
		command.Env = append(os.Environ(), "UV_CACHE_DIR="+filepath.Join(config.WorkRoot, "uv-cache"), "PYTHONUNBUFFERED=1")
		command.Stderr = config.Progress
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
		command.WaitDelay = 5 * time.Second
		output, err := command.StdoutPipe()
		if err != nil {
			return summary, err
		}
		input, err := command.StdinPipe()
		if err != nil {
			return summary, err
		}
		if err := command.Start(); err != nil {
			return summary, err
		}
		done := false
		defer func() {
			if !done {
				syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				command.Wait()
			}
		}()
		scanner := bufio.NewScanner(output)
		for _, tile := range pending {
			if !scanner.Scan() {
				if err := command.Wait(); err != nil {
					return summary, fmt.Errorf("ERA5 temporal producer: %w", err)
				}
				return summary, errors.New("ERA5 temporal producer ended before all tiles")
			}
			var completed completedSource
			if err := json.Unmarshal(scanner.Bytes(), &completed); err != nil {
				return summary, err
			}
			expectedPath := filepath.Join(rawRoot, tileName(tile)+".raw")
			if completed.Row != tile.Row || completed.Column != tile.Column || completed.Path != expectedPath {
				return summary, errors.New("ERA5 temporal producer returned a different tile")
			}
			fmt.Fprintf(config.Progress, "ERA5 downloaded %s: %d compressed bytes in %d chunks; %d decoded bytes, %.1fs; CPU %.1fs; peak RSS %d bytes\n", tileName(tile), completed.CompressedBytes, completed.SourceChunks, completed.DecodedBytes, completed.ElapsedSeconds, completed.CPUSeconds, completed.PeakRSSBytes)
			if err := processTile(ctx, config, plan, monthlyPlans, tile, completed.Path, filepath.Join(checkpointRoot, tileName(tile)+".gob")); err != nil {
				return summary, err
			}
			summary.CompletedTiles++
			fmt.Fprintf(config.Progress, "ERA5 annual checkpoint: %d/%d source tiles complete, elapsed=%s\n", summary.CompletedTiles, summary.Tiles, time.Since(started).Round(time.Second))
			if _, err := io.WriteString(input, "ok\n"); err != nil {
				return summary, err
			}
		}
		input.Close()
		if err := command.Wait(); err != nil {
			return summary, err
		}
		done = true
	}
	if len(config.SourceTiles) > 0 {
		return summary, nil
	}
	maskBytes, err := os.ReadFile(config.DisplayMaskPath)
	if err != nil {
		return summary, err
	}
	for monthIndex, monthPlan := range monthlyPlans {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		encoded, err := era5climateproduct.EncodePublicationAuthority(monthPlan, maskBytes)
		if err != nil {
			return summary, err
		}
		authorityPath := filepath.Join(planRoot, fmt.Sprintf("month-%02d-authority.json", monthIndex+1))
		if err := os.WriteFile(authorityPath, encoded.JSON, 0o600); err != nil {
			return summary, err
		}
		monthlyAuthority, err := era5climateproduct.LoadPublicationAuthority(authorityPath, encoded.SHA256, monthPlan)
		if err != nil {
			return summary, err
		}
		mask, err := era5climateproduct.LoadDisplayMaskWithAuthority(config.DisplayMaskPath, monthPlan, monthlyAuthority)
		if err != nil {
			return summary, err
		}
		releaseID := fmt.Sprintf("era5-2006-2025-month-%02d-%s", monthIndex+1, monthPlan.LogicalSHA256()[:12])
		releaseRoot := filepath.Join(config.ProductRoot, "releases", releaseID)
		catalog, existingErr := era5climateproduct.ValidateRelease(releaseRoot, monthPlan, mask)
		if existingErr != nil {
			if _, err := os.Stat(releaseRoot); err == nil {
				return summary, existingErr
			} else if !os.IsNotExist(err) {
				return summary, err
			}
			builder, err := era5climateproduct.New(era5climateproduct.Config{StagingRoot: config.ProductRoot, ReleaseID: releaseID}, monthPlan, mask)
			if err != nil {
				return summary, err
			}
			for _, tile := range tiles {
				checkpoint, err := readCheckpoint(filepath.Join(checkpointRoot, tileName(tile)+".gob"), plan, tile, monthlyPlans)
				if err != nil {
					builder.Abort()
					return summary, err
				}
				for _, result := range checkpoint.Results[monthIndex] {
					if err := builder.Add(result); err != nil {
						builder.Abort()
						return summary, err
					}
				}
			}
			if err := builder.Commit(); err != nil {
				return summary, err
			}
			catalog, err = era5climateproduct.ValidateRelease(releaseRoot, monthPlan, mask)
			if err != nil {
				return summary, err
			}
		}
		publication, err := era5climateproduct.PublishValidatedMonth(ctx, config.ProductRoot, catalog)
		if err != nil {
			return summary, err
		}
		summary.Months = len(publication.Index.Months)
		fmt.Fprintf(config.Progress, "ERA5 validated publication: month-%02d; %d/12 months\n", monthIndex+1, summary.Months)
	}
	return summary, nil
}

func parseTile(value string) (int, int, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("source tile must be row,column")
	}
	row, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	column, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return row, column, nil
}
func tileName(tile sourceTile) string { return fmt.Sprintf("r%03d-c%04d", tile.Row, tile.Column) }
func worldTiles(cells []era5hourly.Cell) []sourceTile {
	grouped := map[int]*sourceTile{}
	for index, cell := range cells {
		row, column := cell.SourceIndex/era5hourly.GridColumns, cell.SourceIndex%era5hourly.GridColumns
		row = row / 12 * 12
		column = column / 12 * 12
		key := row*era5hourly.GridColumns + column
		tile := grouped[key]
		if tile == nil {
			tile = &sourceTile{Row: row, Column: column}
			grouped[key] = tile
		}
		tile.Indexes = append(tile.Indexes, index)
	}
	result := make([]sourceTile, 0, len(grouped))
	for _, tile := range grouped {
		result = append(result, *tile)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result
}

type observationSink struct {
	observations [][]climate.DailyObservation
}

func (sink *observationSink) BeginMonth(context.Context, int, time.Month, []era5hourly.Cell) (era5hourly.MonthTransaction, error) {
	return sink, nil
}
func (sink *observationSink) Add(day era5hourly.DailyMetrics) error {
	sink.observations[day.CellIndex] = append(sink.observations[day.CellIndex], climate.DailyObservation{Date: day.Date, Latitude: day.Latitude, Longitude: day.Longitude, Metrics: day.Metrics})
	return nil
}
func (*observationSink) Commit() error { return nil }
func (*observationSink) Abort() error  { return nil }

func processTile(ctx context.Context, config Config, plan era5hourly.CellPlan, monthlyPlans [12]era5hourly.CellPlan, tile sourceTile, rawPath, checkpointPath string) error {
	started := time.Now()
	source, err := era5hourly.OpenAnnualTile(rawPath, tile.Row, tile.Column)
	if err != nil {
		return err
	}
	defer source.Close()
	allCells := plan.Cells()
	cells := make([]era5hourly.Cell, len(tile.Indexes))
	for index, globalIndex := range tile.Indexes {
		cells[index] = allCells[globalIndex]
	}
	checkpoint := tileCheckpoint{SchemaVersion: 1, PlanSHA256: plan.SHA256(), SnapshotID: era5hourly.SnapshotID, AcquisitionLayout: "single/temporal", Row: tile.Row, Column: tile.Column, Indexes: tile.Indexes}
	for month := time.January; month <= time.December; month++ {
		sink := &observationSink{observations: make([][]climate.DailyObservation, len(cells))}
		for year := 2006; year <= 2025; year++ {
			frames, err := source.MonthSource(year, month, cells)
			if err != nil {
				return err
			}
			stats, err := era5hourly.AggregateMonth(ctx, frames, year, month, cells, sink)
			if err != nil {
				return fmt.Errorf("annual %s %d-%02d: %w", tileName(tile), year, month, err)
			}
			if stats.SolarZeroClampedCellHours > 0 {
				fmt.Fprintf(config.Progress, "ERA5 solar normalized %s %d-%02d: %d consumed cell-hours, minimum=%.9g J/m², bounded [-2,0) to zero; raw source unchanged\n", tileName(tile), year, month, stats.SolarZeroClampedCellHours, stats.MinimumClampedSolarJM2)
			}
		}
		checkpoint.Results[int(month)-1] = make([]era5climate.CellResult, len(cells))
		for index, observations := range sink.observations {
			expectedCount, _ := monthlyPlans[int(month)-1].ExpectedDayCount(tile.Indexes[index])
			if len(observations) != expectedCount {
				return fmt.Errorf("annual month %d cell %d has %d days; expected %d", month, tile.Indexes[index], len(observations), expectedCount)
			}
			expected := make([]time.Time, len(observations))
			for index, day := range observations {
				expected[index] = day.Date
			}
			result, err := era5climate.ReduceCell(tile.Indexes[index], cells[index], observations, expected)
			if err != nil {
				return err
			}
			checkpoint.Results[int(month)-1][index] = result
		}
		fmt.Fprintf(config.Progress, "ERA5 reduced %s month-%02d: %d complete cell histories\n", tileName(tile), month, len(cells))
	}
	if err := writeCheckpoint(checkpointPath, checkpoint); err != nil {
		return err
	}
	if _, err := readCheckpoint(checkpointPath, plan, tile, monthlyPlans); err != nil {
		return err
	}
	fmt.Fprintf(config.Progress, "ERA5 checkpoint %s: twelve months, %d cells; reduction=%s\n", tileName(tile), len(cells), time.Since(started).Round(time.Millisecond))
	if err := os.Remove(rawPath); err != nil {
		return err
	}
	return nil
}

func writeCheckpoint(path string, checkpoint tileCheckpoint) error {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(checkpoint); err != nil {
		return err
	}
	digest := sha256.Sum256(buffer.Bytes())
	buffer.Write(digest[:])
	partial := path + ".partial"
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(buffer.Bytes()); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(partial, path)
}
func readCheckpoint(path string, plan era5hourly.CellPlan, tile sourceTile, monthlyPlans [12]era5hourly.CellPlan) (tileCheckpoint, error) {
	var result tileCheckpoint
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if len(data) < sha256.Size {
		return result, errors.New("ERA5 tile checkpoint is incomplete")
	}
	body := data[:len(data)-sha256.Size]
	digest := sha256.Sum256(body)
	if !bytes.Equal(digest[:], data[len(body):]) {
		return result, errors.New("ERA5 tile checkpoint checksum mismatch")
	}
	if err := gob.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return result, err
	}
	if result.SchemaVersion != 1 || result.PlanSHA256 != plan.SHA256() || result.SnapshotID != era5hourly.SnapshotID || result.AcquisitionLayout != "single/temporal" || result.Row != tile.Row || result.Column != tile.Column || !reflect.DeepEqual(result.Indexes, tile.Indexes) {
		return result, errors.New("ERA5 tile checkpoint identity changed")
	}
	for month, results := range result.Results {
		if len(results) != len(tile.Indexes) {
			return result, errors.New("ERA5 tile checkpoint cell histories are incomplete")
		}
		for index, cell := range results {
			days, _ := monthlyPlans[month].ExpectedDayCount(tile.Indexes[index])
			if cell.CellIndex != tile.Indexes[index] || cell.Days != days {
				return result, errors.New("ERA5 tile checkpoint civil calendar differs")
			}
		}
	}
	return result, nil
}
