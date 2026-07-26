// Package era5climateproduct turns one complete ERA5 climate reduction into
// an immutable, source-honest month release. It owns delivery geometry only;
// scoring remains in the upstream reducer.
package era5climateproduct

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/era5climate"
	"nice-weather-finder/internal/providers/era5hourly"
)

const (
	Rows              = era5hourly.GridRows
	Columns           = era5hourly.GridColumns
	GridDegrees       = 0.25
	PointBlockRows    = 16
	PointBlockCols    = 32
	MaximumZoom       = 4
	CatalogFilename   = "catalog.json"
	PointMediaType    = "application/vnd.nice-weather.era5-climate-point-block"
	OverrideMediaType = "application/vnd.nice-weather.era5-climate-dateline-block"

	pointHeaderBytes = 64
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Field identifies one already evaluated probability plane.
type Field string

const (
	FieldComfortGood     Field = "comfort_good"
	FieldDryGood         Field = "dry_good"
	FieldClearSkyGood    Field = "clear_sky_good"
	FieldNiceGood        Field = "nice_good"
	FieldNiceCalmGood    Field = "nice_calm_good"
	FieldCyclingGood     Field = "cycling_good"
	FieldCyclingCalmGood Field = "cycling_calm_good"
)

var fields = []Field{
	FieldComfortGood,
	FieldDryGood,
	FieldClearSkyGood,
	FieldNiceGood,
	FieldNiceCalmGood,
	FieldCyclingGood,
	FieldCyclingCalmGood,
}

// Fields returns the canonical ERA5 climate scalar fields in publication order.
func Fields() []Field {
	return append([]Field(nil), fields...)
}

// Config binds one immutable month release to its exact source plan.
type Config struct {
	StagingRoot string
	ReleaseID   string
	MaximumZoom uint8
}

type expectedCell struct {
	latitude  float64
	longitude float64
	row       int
	column    int
	override  bool
	days      int
}

// Builder implements era5climate.Sink. Add only changes memory. Commit writes
// a complete release into StagingRoot and installs its directory atomically.
type Builder struct {
	config          Config
	expected        []expectedCell
	seen            []bool
	added           int
	mainRecords     []byte
	overrideRecords []byte
	mainScalars     [][]byte
	overrideScalars [][]byte
	overridePresent []bool
	displayMask     DisplayMask
	plan            era5hourly.CellPlan
	period          string
	planSHA256      string
	state           builderState
	beforeInstall   func() error
	syncDirectory   func(string) error
}

type builderState uint8

const (
	stateOpen builderState = iota
	stateCommitted
	stateAborted
)

// New binds the product to the opaque checksum-trusted plan and its canonical
// centre-on-land display mask.
func New(config Config, plan era5hourly.CellPlan, displayMask DisplayMask) (*Builder, error) {
	if plan.Year() < 2006 || plan.Year() > 2025 || plan.Month() < 1 || plan.Month() > 12 ||
		plan.CellCount() == 0 || !validSHA256(plan.SHA256()) || !displayMask.matchesPlan(plan) {
		return nil, errors.New("ERA5 climate product requires a validated source plan and display mask")
	}
	expectedDays := make([]int, plan.CellCount())
	for index := range expectedDays {
		count, ok := plan.ExpectedDayCount(index)
		if !ok {
			return nil, errors.New("ERA5 climate product source plan lacks exact valid-day counts")
		}
		expectedDays[index] = count
	}
	return newBuilder(config, plan, plan.Month(), plan.SHA256(), plan.Cells(), expectedDays, displayMask)
}

func newBuilder(config Config, plan era5hourly.CellPlan, month int, planSHA256 string, cells []era5hourly.Cell, expectedDays []int, displayMask DisplayMask) (*Builder, error) {
	if config.MaximumZoom == 0 {
		config.MaximumZoom = MaximumZoom
	}
	if config.StagingRoot == "" || !validIdentifier(config.ReleaseID) ||
		config.MaximumZoom > MaximumZoom || month < 1 || month > 12 ||
		!validSHA256(planSHA256) || len(cells) == 0 || len(cells) > Rows*Columns+Rows ||
		len(expectedDays) != len(cells) {
		return nil, errors.New("ERA5 climate product requires a staging root, release, month, plan checksum and supported zoom")
	}

	builder := &Builder{
		config:          config,
		syncDirectory:   syncDirectory,
		period:          fmt.Sprintf("month-%02d", month),
		planSHA256:      planSHA256,
		plan:            plan,
		displayMask:     displayMask,
		expected:        make([]expectedCell, len(cells)),
		seen:            make([]bool, len(cells)),
		mainRecords:     make([]byte, Rows*Columns*delivery.ClimatePointRecordBytes),
		overrideRecords: make([]byte, Rows*delivery.ClimatePointRecordBytes),
		mainScalars:     make([][]byte, len(fields)),
		overrideScalars: make([][]byte, len(fields)),
		overridePresent: make([]bool, Rows),
	}
	for index := range fields {
		builder.mainScalars[index] = make([]byte, Rows*Columns)
		builder.overrideScalars[index] = make([]byte, Rows)
	}

	mainSeen := make(map[int]struct{}, len(cells))
	overrideSeen := make(map[int]struct{})
	for index, cell := range cells {
		if expectedDays[index] < 1 || expectedDays[index] > math.MaxUint16 {
			return nil, fmt.Errorf("ERA5 climate plan cell %d has invalid expected day count %d", index, expectedDays[index])
		}
		row, column, override, err := gridPosition(cell.Latitude, cell.Longitude)
		if err != nil {
			return nil, fmt.Errorf("ERA5 climate plan cell %d: %w", index, err)
		}
		if override {
			if _, duplicate := overrideSeen[row]; duplicate {
				return nil, fmt.Errorf("ERA5 climate plan has duplicate +180 cell in row %d", row)
			}
			overrideSeen[row] = struct{}{}
			builder.overridePresent[row] = true
		} else {
			offset := row*Columns + column
			if _, duplicate := mainSeen[offset]; duplicate {
				return nil, fmt.Errorf("ERA5 climate plan has duplicate main cell %d,%d", row, column)
			}
			mainSeen[offset] = struct{}{}
		}
		builder.expected[index] = expectedCell{
			latitude: cell.Latitude, longitude: cell.Longitude,
			row: row, column: column, override: override, days: expectedDays[index],
		}
	}
	return builder, nil
}

// Add encodes one reducer result into its exact grid cell.
func (builder *Builder) Add(result era5climate.CellResult) error {
	if builder == nil || builder.state != stateOpen {
		return errors.New("ERA5 climate product is not open")
	}
	if result.CellIndex < 0 || result.CellIndex >= len(builder.expected) {
		return fmt.Errorf("ERA5 climate result cell %d is outside the source plan", result.CellIndex)
	}
	if builder.seen[result.CellIndex] {
		return fmt.Errorf("ERA5 climate result cell %d was added twice", result.CellIndex)
	}
	expected := builder.expected[result.CellIndex]
	if math.Float64bits(result.Latitude) != math.Float64bits(expected.latitude) ||
		math.Float64bits(result.Longitude) != math.Float64bits(expected.longitude) {
		return fmt.Errorf("ERA5 climate result cell %d coordinate does not match the source plan", result.CellIndex)
	}
	if result.Days != expected.days {
		return fmt.Errorf("ERA5 climate result cell %d has %d valid days; want %d for its timezone and month", result.CellIndex, result.Days, expected.days)
	}

	record := delivery.ClimatePointRecord{
		Distribution: result.Distribution,
		Physical: delivery.ClimatePhysicalSummary{
			DaytimeTemperatureP10C:     tenth(result.Physical.DaytimeTemperatureP10C),
			DaytimeTemperatureP50C:     tenth(result.Physical.DaytimeTemperatureP50C),
			DaytimeTemperatureP90C:     tenth(result.Physical.DaytimeTemperatureP90C),
			DaytimeMinimumP50C:         tenth(result.Physical.DaytimeMinimumP50C),
			DaytimeMaximumP50C:         tenth(result.Physical.DaytimeMaximumP50C),
			DewPointP50C:               tenth(result.Physical.DewPointP50C),
			DewPointP90C:               tenth(result.Physical.DewPointP90C),
			RelativeHumidityP50Percent: wholePercent(result.Physical.RelativeHumidityP50Percent),
			MuggyDayPercent:            float64(result.Physical.MuggyDayPercent),
			RainFreeDayPercent:         float64(result.Physical.RainFreeDayPercent),
			RainP90MM:                  tenth(result.Physical.RainP90MM),
			CloudCoverP50Percent:       wholePercent(result.Physical.CloudCoverP50Percent),
			ClearSkyDayPercent:         float64(result.Physical.ClearSkyDayPercent),
			WindP50KPH:                 tenth(result.Physical.WindP50KPH),
			WindP90KPH:                 tenth(result.Physical.WindP90KPH),
		},
		Days: uint16(result.Days),
	}
	var encoded [delivery.ClimatePointRecordBytes]byte
	if err := delivery.EncodeClimatePointRecord(encoded[:], record); err != nil {
		return fmt.Errorf("encode ERA5 climate result cell %d: %w", result.CellIndex, err)
	}
	values := scalarValues(result.Distribution)
	if expected.override {
		copy(builder.overrideRecords[expected.row*delivery.ClimatePointRecordBytes:], encoded[:])
		for field := range fields {
			builder.overrideScalars[field][expected.row] = values[field]
		}
	} else {
		offset := expected.row*Columns + expected.column
		copy(builder.mainRecords[offset*delivery.ClimatePointRecordBytes:], encoded[:])
		for field := range fields {
			builder.mainScalars[field][offset] = values[field]
		}
	}
	builder.seen[result.CellIndex] = true
	builder.added++
	return nil
}

// Commit writes all assets below a temporary directory, then installs only
// the complete release directory.
func (builder *Builder) Commit() error {
	if builder == nil || builder.state != stateOpen {
		return errors.New("ERA5 climate product is not open")
	}
	if builder.added != len(builder.expected) {
		return fmt.Errorf("ERA5 climate product has %d of %d expected cells", builder.added, len(builder.expected))
	}
	for index, seen := range builder.seen {
		if !seen {
			return fmt.Errorf("ERA5 climate product is missing source-plan cell %d", index)
		}
	}
	if err := os.MkdirAll(builder.config.StagingRoot, 0o755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(builder.config.StagingRoot, ".era5-climate-product-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)

	releaseRoot := filepath.Join(temporary, "releases", builder.config.ReleaseID)
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		return err
	}
	if err := writeBytes(filepath.Join(releaseRoot, "display-mask.bin"), builder.displayMask.values); err != nil {
		return err
	}
	pointBlocks, overrideBlocks, err := builder.writePointBlocks(releaseRoot)
	if err != nil {
		return err
	}
	scalarTiles, err := builder.writeScalarTiles(temporary)
	if err != nil {
		return err
	}
	catalog := builder.catalog(pointBlocks, overrideBlocks, scalarTiles)
	if err := writeJSON(filepath.Join(releaseRoot, CatalogFilename), catalog); err != nil {
		return err
	}
	if _, err := ValidateRelease(releaseRoot, builder.plan, builder.displayMask); err != nil {
		return fmt.Errorf("validate staged ERA5 climate release: %w", err)
	}
	if err := syncTree(releaseRoot); err != nil {
		return fmt.Errorf("sync staged ERA5 climate release: %w", err)
	}
	if builder.beforeInstall != nil {
		if err := builder.beforeInstall(); err != nil {
			return err
		}
	}

	finalParent := filepath.Join(builder.config.StagingRoot, "releases")
	if err := os.MkdirAll(finalParent, 0o755); err != nil {
		return err
	}
	if err := builder.syncDirectory(builder.config.StagingRoot); err != nil {
		return err
	}
	finalRoot := filepath.Join(finalParent, builder.config.ReleaseID)
	if _, err := os.Stat(finalRoot); err == nil {
		return fmt.Errorf("immutable ERA5 climate release already exists: %s", finalRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(releaseRoot, finalRoot); err != nil {
		return err
	}
	if err := builder.syncDirectory(finalParent); err != nil {
		return builder.rollbackInstalledRelease(finalRoot, releaseRoot, finalParent, err)
	}
	builder.state = stateCommitted
	builder.releaseMemory()
	return nil
}

func (builder *Builder) rollbackInstalledRelease(finalRoot, stagedRoot, finalParent string, syncErr error) error {
	recoveryErr := os.Rename(finalRoot, stagedRoot)
	if recoveryErr != nil {
		recoveryErr = errors.Join(recoveryErr, os.RemoveAll(finalRoot))
	}
	resyncErr := builder.syncDirectory(finalParent)
	_, visibleErr := os.Lstat(finalRoot)
	if visibleErr == nil {
		visibleErr = errors.New("failed ERA5 climate release remains visible after rollback")
	} else if errors.Is(visibleErr, os.ErrNotExist) {
		visibleErr = nil
	}
	return errors.Join(
		fmt.Errorf("sync installed ERA5 climate release parent: %w", syncErr),
		recoveryErr, resyncErr, visibleErr,
	)
}

// Abort discards all staged memory. Add never creates files.
func (builder *Builder) Abort() error {
	if builder == nil || builder.state == stateAborted {
		return nil
	}
	if builder.state == stateCommitted {
		return errors.New("cannot abort a committed ERA5 climate product")
	}
	builder.state = stateAborted
	builder.releaseMemory()
	return nil
}

func (builder *Builder) releaseMemory() {
	builder.expected = nil
	builder.seen = nil
	builder.mainRecords = nil
	builder.overrideRecords = nil
	builder.mainScalars = nil
	builder.overrideScalars = nil
	builder.overridePresent = nil
	builder.displayMask = DisplayMask{}
	builder.plan = era5hourly.CellPlan{}
}

func scalarValues(record delivery.ClimateDistributionRecord) [7]byte {
	return [7]byte{
		record.ComfortGoodPercent,
		record.DryGoodPercent,
		record.BrightGoodPercent,
		record.Nice.GoodOrBetterPercent(),
		record.Nice.CalmGoodPercent,
		record.Cycling.GoodOrBetterPercent(),
		record.Cycling.CalmGoodPercent,
	}
}

func tenth(value float64) float64        { return math.Round(value*10) / 10 }
func wholePercent(value float64) float64 { return math.Round(value) }

func gridPosition(latitude, longitude float64) (row, column int, override bool, err error) {
	if !finite(latitude) || !finite(longitude) {
		return 0, 0, false, errors.New("coordinate is not finite")
	}
	rowValue := (90 - latitude) / GridDegrees
	if math.Abs(rowValue-math.Round(rowValue)) > 1e-9 || rowValue < 0 || rowValue >= Rows {
		return 0, 0, false, fmt.Errorf("latitude %.9g is outside the ERA5 0.25° grid", latitude)
	}
	row = int(math.Round(rowValue))
	if longitude == 180 {
		return row, 0, true, nil
	}
	columnValue := (longitude + 180) / GridDegrees
	if math.Abs(columnValue-math.Round(columnValue)) > 1e-9 || columnValue < 0 || columnValue >= Columns {
		return 0, 0, false, fmt.Errorf("longitude %.9g is outside the canonical ERA5 grid", longitude)
	}
	return row, int(math.Round(columnValue)), false, nil
}

func validIdentifier(value string) bool {
	return len(value) <= 64 && identifierPattern.MatchString(value)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeBytes(path string, encoded []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to sync symlink %s", path)
		}
		if info.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to sync non-regular file %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		return errors.Join(syncErr, closeErr)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDirectory(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func relativeReleasePath(releaseRoot, assetPath string) (string, error) {
	relative, err := filepath.Rel(releaseRoot, assetPath)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("ERA5 climate asset is outside its release")
	}
	return filepath.ToSlash(relative), nil
}

func fieldRank(field Field) int {
	for index, candidate := range fields {
		if candidate == field {
			return index
		}
	}
	return len(fields)
}

func sortedFields() []Field { return append([]Field(nil), fields...) }

func putUint16(dst []byte, value uint16) { binary.LittleEndian.PutUint16(dst, value) }
