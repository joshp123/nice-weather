// Package climatebuild joins AgERA5 source chunks into canonical climate
// distributions. It retains one nine-field source time chunk and fixed-size
// per-cell score histograms; source chunks are released before the next chunk.
package climatebuild

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/weather"
)

var (
	ErrPartialFieldChunk = errors.New("AgERA5 time chunk has only some required fields")
	ErrIncompleteSource  = errors.New("AgERA5 block has only some required time chunks")
	ErrPartialCell       = errors.New("AgERA5 cell-day has only some required fields")
	ErrIncompleteCell    = errors.New("AgERA5 cell has incomplete daily coverage")
)

const (
	// Preserve the generic weather contract's existing 5°C tolerance. Clamping
	// only larger AgERA5 crossings fixes source values that would otherwise fail
	// while keeping every previously completed checkpoint byte-equivalent.
	materialComfortTemperatureCrossingC = 5

	// The generic weather contract permits up to 2°C of dew-point tolerance.
	// AgERA5 derives daily fields through variable-specific regressions, so only
	// a crossing beyond that existing materiality boundary is capped.
	materialDewPointCrossingC = 2
)

const (
	fieldDaytimeTemperature = iota
	fieldMinimumTemperature
	fieldMaximumTemperature
	fieldDewPoint
	fieldPrecipitation
	fieldPrecipitationDuration
	fieldSolarRadiation
	fieldCloudCover
	fieldWindSpeed
	fieldCount
)

var requiredFields = [fieldCount]agera5.Field{
	fieldDaytimeTemperature:    agera5.DaytimeMeanTemperature,
	fieldMinimumTemperature:    agera5.MinimumTemperature,
	fieldMaximumTemperature:    agera5.MaximumTemperature,
	fieldDewPoint:              agera5.MeanDewPoint,
	fieldPrecipitation:         agera5.Precipitation,
	fieldPrecipitationDuration: agera5.PrecipitationDuration,
	fieldSolarRadiation:        agera5.SolarRadiation,
	fieldCloudCover:            agera5.CloudCover,
	fieldWindSpeed:             agera5.MeanWindSpeed,
}

// ChunkReference is the minimum source-plan contract used by the processor.
// agera5.ChunkRef satisfies it directly; tests can supply small references.
type ChunkReference interface {
	Field() agera5.Field
	FirstDate() time.Time
	LastDate() time.Time
	Block() agera5.BlockCoordinate
}

// Block is the decoded, fill-aware source view used during one time chunk.
type Block interface {
	Reference() ChunkReference
	TimeCount() int
	LatitudeCount() int
	LongitudeCount() int
	Date(timeOffset int) (time.Time, error)
	Location(latitudeOffset, longitudeOffset int) (float64, float64, error)
	Value(timeOffset, latitudeOffset, longitudeOffset int) (float32, bool, error)
}

// Source plans and fetches one spatial block. Implementations must return
// errors wrapping agera5.ErrChunkNotFound for absent sparse source objects.
type Source interface {
	Shape(agera5.BlockCoordinate) (width, height int, err error)
	Plan(firstDate, lastDate time.Time, coordinate agera5.BlockCoordinate) ([]ChunkReference, error)
	Fetch(context.Context, ChunkReference) (Block, error)
}

// AgERA5Source adapts the authenticated provider client without exposing its
// token in processor errors or result values.
type AgERA5Source struct {
	metadata agera5.Metadata
	fetch    func(context.Context, agera5.ChunkRef) (agera5.Block, error)
}

func NewAgERA5Source(metadata agera5.Metadata, client *agera5.Client, token string) AgERA5Source {
	return AgERA5Source{
		metadata: metadata,
		fetch: func(ctx context.Context, reference agera5.ChunkRef) (agera5.Block, error) {
			if client == nil {
				return agera5.Block{}, errors.New("nil AgERA5 client")
			}
			return client.FetchChunk(ctx, token, reference)
		},
	}
}

func (AgERA5Source) String() string { return "AgERA5 source" }

func (AgERA5Source) Shape(coordinate agera5.BlockCoordinate) (width, height int, err error) {
	latitude, longitude, err := agera5.BlockShape(coordinate)
	return longitude, latitude, err
}

func (source AgERA5Source) Plan(firstDate, lastDate time.Time, coordinate agera5.BlockCoordinate) ([]ChunkReference, error) {
	references, err := source.metadata.PlanChunks(firstDate, lastDate, coordinate)
	if err != nil {
		return nil, err
	}
	result := make([]ChunkReference, len(references))
	for index := range references {
		result[index] = references[index]
	}
	return result, nil
}

func (source AgERA5Source) Fetch(ctx context.Context, reference ChunkReference) (Block, error) {
	if source.fetch == nil {
		return nil, errors.New("uninitialized AgERA5 source")
	}
	providerReference, ok := reference.(agera5.ChunkRef)
	if !ok {
		return nil, errors.New("AgERA5 source received a foreign chunk reference")
	}
	block, err := source.fetch(ctx, providerReference)
	if err != nil {
		return nil, err
	}
	return providerBlock{Block: block}, nil
}

type providerBlock struct{ agera5.Block }

func (block providerBlock) Reference() ChunkReference { return block.Block.Reference() }

// Stats makes sparse-ocean and complete-cell outcomes explicit.
type Stats struct {
	ExpectedDays                        int
	ProcessedDays                       int
	SourceTimeChunks                    int
	ChunksFetched                       int
	ChunksAbsent                        int
	LogicalCells                        int
	LandCells                           int
	WaterCells                          int
	CompleteCells                       int
	UnsupportedLandCells                int
	NoDataCells                         int
	SparseOcean                         bool
	TemperatureRepairs                  int
	MaximumTemperatureRepair            float64
	ComfortTemperatureRepairs           int
	ComfortTemperatureBelowRangeRepairs int
	ComfortTemperatureAboveRangeRepairs int
	MaximumComfortTemperatureRepair     float64
	DewPointRepairs                     int
	MaximumDewPointRepair               float64
	PeakRetainedDecodedBytes            int64
}

// Result contains January through December record planes in row-major order.
// All planes share Width and Height. A no-data cell is the zero invalid record.
type Result struct {
	Width  int
	Height int
	Months [12][]delivery.ClimateDistributionRecord
	Stats  Stats
}

type groupKey struct {
	first time.Time
	last  time.Time
}

type chunkGroup struct {
	key  groupKey
	refs map[agera5.Field]ChunkReference
}

type fieldBlocks [len(requiredFields)]Block

type cellState struct {
	accumulator   *climate.StreamingAccumulator
	availableDays int
	missingDays   int
}

// BuildSourceCanaryBlock scores one unmasked AgERA5 source block. It exists
// only for bounded source audits; publication must use BuildLandBlock.
func BuildSourceCanaryBlock(ctx context.Context, source Source, coordinate agera5.BlockCoordinate, window climate.ReferenceWindow) (Result, error) {
	return buildBlock(ctx, source, coordinate, window, nil)
}

// BuildLandBlock scores only cells marked true by the Natural Earth centre
// mask. The mask is row-major in the exact source-block geometry.
func BuildLandBlock(ctx context.Context, source Source, coordinate agera5.BlockCoordinate, window climate.ReferenceWindow, land []bool) (Result, error) {
	if land == nil {
		return Result{}, errors.New("climate land mask is required")
	}
	return buildBlock(ctx, source, coordinate, window, land)
}

func buildBlock(ctx context.Context, source Source, coordinate agera5.BlockCoordinate, window climate.ReferenceWindow, land []bool) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("nil climate build context")
	}
	if source == nil {
		return Result{}, errors.New("nil climate source")
	}
	if _, err := climate.NewStreamingAccumulator(window); err != nil {
		return Result{}, err
	}
	firstDate := time.Date(window.FirstYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(window.LastYear, time.December, 31, 0, 0, 0, 0, time.UTC)
	expectedDays := int(lastDate.Sub(firstDate)/(24*time.Hour)) + 1
	width, height, err := source.Shape(coordinate)
	if err != nil {
		return Result{}, fmt.Errorf("shape AgERA5 block: %w", err)
	}
	if width <= 0 || width > 32 || height <= 0 || height > 16 {
		return Result{}, fmt.Errorf("AgERA5 source returned invalid block shape %dx%d", width, height)
	}
	if land != nil && len(land) != width*height {
		return Result{}, fmt.Errorf("climate land mask has %d cells; want %d", len(land), width*height)
	}

	references, err := source.Plan(firstDate, lastDate, coordinate)
	if err != nil {
		return Result{}, fmt.Errorf("plan AgERA5 block: %w", err)
	}
	groups, err := groupReferences(references, coordinate, firstDate, lastDate)
	if err != nil {
		return Result{}, err
	}
	result := Result{Width: width, Height: height, Stats: Stats{ExpectedDays: expectedDays, SourceTimeChunks: len(groups), LogicalCells: width * height}}
	if land == nil {
		result.Stats.LandCells = width * height
	} else {
		for _, isLand := range land {
			if isLand {
				result.Stats.LandCells++
			} else {
				result.Stats.WaterCells++
			}
		}
	}
	for month := range result.Months {
		result.Months[month] = make([]delivery.ClimateDistributionRecord, width*height)
	}
	var cells []cellState
	var latitudes, longitudes []float64
	sawPresent, sawAbsent := false, false

	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		blocks, absent, fetched, err := fetchGroup(ctx, source, group)
		result.Stats.ChunksFetched += fetched
		if err != nil {
			return Result{}, err
		}
		if absent {
			sawAbsent = true
			result.Stats.ChunksAbsent += len(requiredFields)
			if sawPresent {
				return Result{}, fmt.Errorf("%w: time chunk beginning %s is absent after present data", ErrIncompleteSource, group.key.first.Format(time.DateOnly))
			}
			continue
		}
		decodedBytes := decodedBlockBytes(blocks)
		if decodedBytes > result.Stats.PeakRetainedDecodedBytes {
			result.Stats.PeakRetainedDecodedBytes = decodedBytes
		}
		sawPresent = true
		if sawAbsent {
			return Result{}, fmt.Errorf("%w: time chunk beginning %s is present after absent data", ErrIncompleteSource, group.key.first.Format(time.DateOnly))
		}

		dates, blockWidth, blockHeight, groupLatitudes, groupLongitudes, err := validateBlocks(blocks, group)
		if err != nil {
			return Result{}, err
		}
		if blockWidth != result.Width || blockHeight != result.Height {
			return Result{}, fmt.Errorf("AgERA5 block dimensions %dx%d disagree with source shape %dx%d", blockWidth, blockHeight, result.Width, result.Height)
		}
		if cells == nil {
			cells = make([]cellState, result.Width*result.Height)
			for index := range cells {
				if land != nil && !land[index] {
					continue
				}
				cells[index].accumulator, err = climate.NewStreamingAccumulator(window)
				if err != nil {
					return Result{}, err
				}
			}
			latitudes, longitudes = groupLatitudes, groupLongitudes
		} else if !sameCoordinates(latitudes, longitudes, groupLatitudes, groupLongitudes) {
			return Result{}, errors.New("AgERA5 block geometry changed between time chunks")
		}

		for timeOffset, date := range dates {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			for cellIndex := range cells {
				if cells[cellIndex].accumulator == nil {
					continue
				}
				latitudeOffset, longitudeOffset := cellIndex/result.Width, cellIndex%result.Width
				values, available, err := readCellDay(blocks, timeOffset, latitudeOffset, longitudeOffset)
				if err != nil {
					return Result{}, err
				}
				if available == 0 {
					cells[cellIndex].missingDays++
					continue
				}
				if available != len(requiredFields) {
					return Result{}, fmt.Errorf("%w at %s cell %d,%d: have %d of %d fields", ErrPartialCell, date.Format(time.DateOnly), latitudeOffset, longitudeOffset, available, len(requiredFields))
				}
				cells[cellIndex].availableDays++
				if cells[cellIndex].missingDays != 0 {
					continue
				}
				metrics, repairs := normalize(values)
				if repairs.temperature > 0 {
					result.Stats.TemperatureRepairs++
					if repairs.temperature > result.Stats.MaximumTemperatureRepair {
						result.Stats.MaximumTemperatureRepair = repairs.temperature
					}
				}
				if repairs.comfortTemperature > 0 {
					result.Stats.ComfortTemperatureRepairs++
					if repairs.comfortTemperatureDirection < 0 {
						result.Stats.ComfortTemperatureBelowRangeRepairs++
					} else {
						result.Stats.ComfortTemperatureAboveRangeRepairs++
					}
					if repairs.comfortTemperature > result.Stats.MaximumComfortTemperatureRepair {
						result.Stats.MaximumComfortTemperatureRepair = repairs.comfortTemperature
					}
				}
				if repairs.dewPoint > 0 {
					result.Stats.DewPointRepairs++
					if repairs.dewPoint > result.Stats.MaximumDewPointRepair {
						result.Stats.MaximumDewPointRepair = repairs.dewPoint
					}
				}
				if err := cells[cellIndex].accumulator.AddMetrics(date, latitudes[cellIndex], longitudes[cellIndex], metrics); err != nil {
					return Result{}, fmt.Errorf("accumulate AgERA5 %s cell %d,%d: %w", date.Format(time.DateOnly), latitudeOffset, longitudeOffset, err)
				}
			}
		}
		result.Stats.ProcessedDays += len(dates)
		clear(blocks[:])
		// No decoded source time chunk remains referenced while the next one
		// is fetched.
	}

	if !sawPresent {
		if land != nil && result.Stats.LandCells > 0 {
			return Result{}, fmt.Errorf("%w: land block has no source evidence", ErrIncompleteCell)
		}
		result.Stats.SparseOcean = true
		result.Stats.NoDataCells = result.Stats.LogicalCells
		return result, nil
	}
	if result.Stats.ProcessedDays != expectedDays {
		return Result{}, fmt.Errorf("%w: processed %d of %d dates", ErrIncompleteSource, result.Stats.ProcessedDays, expectedDays)
	}
	for cellIndex := range cells {
		if cells[cellIndex].accumulator == nil {
			result.Stats.NoDataCells++
			continue
		}
		switch cells[cellIndex].availableDays {
		case 0:
			if land != nil {
				// AgERA5 has a stricter source-support mask than Natural Earth,
				// notably over permanent ice. A cell with no value in any required
				// field on any day is structurally unsupported, not incomplete.
				// It remains invalid and transparent. Any partial field or day
				// coverage still fails in the hot path above or the default case.
				result.Stats.UnsupportedLandCells++
			}
			result.Stats.NoDataCells++
			continue
		case expectedDays:
		default:
			return Result{}, fmt.Errorf("%w at cell %d,%d: have %d of %d days", ErrIncompleteCell, cellIndex/result.Width, cellIndex%result.Width, cells[cellIndex].availableDays, expectedDays)
		}
		distributions, err := cells[cellIndex].accumulator.Finalize()
		if err != nil {
			return Result{}, fmt.Errorf("finalize climate cell %d,%d: %w", cellIndex/result.Width, cellIndex%result.Width, err)
		}
		for month := range distributions {
			record, err := distributions[month].DeliveryRecord()
			if err != nil {
				return Result{}, fmt.Errorf("deliver climate cell %d,%d month %d: %w", cellIndex/result.Width, cellIndex%result.Width, month+1, err)
			}
			result.Months[month][cellIndex] = record
		}
		result.Stats.CompleteCells++
	}
	if land != nil && (result.Stats.CompleteCells+result.Stats.UnsupportedLandCells != result.Stats.LandCells || result.Stats.NoDataCells != result.Stats.WaterCells+result.Stats.UnsupportedLandCells) {
		return Result{}, fmt.Errorf("%w: complete land cells %d, unsupported land cells %d, Natural Earth land cells %d, and no-data cells %d/%d", ErrIncompleteCell, result.Stats.CompleteCells, result.Stats.UnsupportedLandCells, result.Stats.LandCells, result.Stats.NoDataCells, result.Stats.WaterCells+result.Stats.UnsupportedLandCells)
	}
	return result, nil
}

func decodedBlockBytes(blocks fieldBlocks) int64 {
	var total int64
	for _, block := range blocks {
		if sized, ok := block.(interface{ DecodedBytes() int }); ok {
			total += int64(sized.DecodedBytes())
			continue
		}
		// Test and alternate sources may expose logical blocks without a
		// physical backing allocation. Four bytes per value is the minimum
		// retained float32 evidence for that contract.
		total += int64(block.TimeCount() * block.LatitudeCount() * block.LongitudeCount() * 4)
	}
	return total
}

func groupReferences(references []ChunkReference, coordinate agera5.BlockCoordinate, firstDate, lastDate time.Time) ([]chunkGroup, error) {
	if len(references) == 0 {
		return nil, errors.New("AgERA5 plan contains no chunks")
	}
	required := make(map[agera5.Field]bool, len(requiredFields))
	for _, field := range requiredFields {
		required[field] = true
	}
	groupMap := make(map[groupKey]map[agera5.Field]ChunkReference)
	for _, reference := range references {
		if reference == nil {
			return nil, errors.New("AgERA5 plan contains a nil reference")
		}
		if reference.Block() != coordinate || !required[reference.Field()] || !utcDate(reference.FirstDate()) || !utcDate(reference.LastDate()) || reference.LastDate().Before(reference.FirstDate()) {
			return nil, errors.New("AgERA5 plan contains an invalid reference")
		}
		key := groupKey{first: reference.FirstDate(), last: reference.LastDate()}
		if groupMap[key] == nil {
			groupMap[key] = make(map[agera5.Field]ChunkReference)
		}
		if _, duplicate := groupMap[key][reference.Field()]; duplicate {
			return nil, fmt.Errorf("AgERA5 plan repeats %s for time chunk beginning %s", reference.Field(), key.first.Format(time.DateOnly))
		}
		groupMap[key][reference.Field()] = reference
	}
	groups := make([]chunkGroup, 0, len(groupMap))
	for key, refs := range groupMap {
		if len(refs) != len(required) {
			return nil, fmt.Errorf("AgERA5 plan for time chunk beginning %s has %d of %d fields", key.first.Format(time.DateOnly), len(refs), len(required))
		}
		groups = append(groups, chunkGroup{key: key, refs: refs})
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].key.first.Before(groups[right].key.first) })
	expected := firstDate
	for _, group := range groups {
		if !group.key.first.Equal(expected) {
			return nil, fmt.Errorf("AgERA5 plan is not contiguous: got %s, expected %s", group.key.first.Format(time.DateOnly), expected.Format(time.DateOnly))
		}
		expected = group.key.last.AddDate(0, 0, 1)
	}
	if !expected.Equal(lastDate.AddDate(0, 0, 1)) {
		return nil, fmt.Errorf("AgERA5 plan ends at %s, expected %s", expected.AddDate(0, 0, -1).Format(time.DateOnly), lastDate.Format(time.DateOnly))
	}
	return groups, nil
}

func fetchGroup(ctx context.Context, source Source, group chunkGroup) (fieldBlocks, bool, int, error) {
	var blocks fieldBlocks
	missing := 0
	fetched := 0
	for index, field := range requiredFields {
		if err := ctx.Err(); err != nil {
			return fieldBlocks{}, false, fetched, err
		}
		block, err := source.Fetch(ctx, group.refs[field])
		if errors.Is(err, agera5.ErrChunkNotFound) {
			missing++
			continue
		}
		if err != nil {
			return fieldBlocks{}, false, fetched, fmt.Errorf("fetch AgERA5 %s time chunk beginning %s: %w", field, group.key.first.Format(time.DateOnly), err)
		}
		if block == nil {
			return fieldBlocks{}, false, fetched, fmt.Errorf("fetch AgERA5 %s returned a nil block", field)
		}
		blocks[index] = block
		fetched++
	}
	if missing == len(group.refs) {
		return fieldBlocks{}, true, fetched, nil
	}
	if missing != 0 {
		return fieldBlocks{}, false, fetched, fmt.Errorf("%w beginning %s: %d fields absent and %d present", ErrPartialFieldChunk, group.key.first.Format(time.DateOnly), missing, len(group.refs)-missing)
	}
	return blocks, false, fetched, nil
}

func validateBlocks(blocks fieldBlocks, group chunkGroup) ([]time.Time, int, int, []float64, []float64, error) {
	first := blocks[0]
	width, height, count := first.LongitudeCount(), first.LatitudeCount(), first.TimeCount()
	if width <= 0 || width > 32 || height <= 0 || height > 16 || count <= 0 {
		return nil, 0, 0, nil, nil, errors.New("AgERA5 block has invalid logical dimensions")
	}
	dates := make([]time.Time, count)
	for offset := range dates {
		date, err := first.Date(offset)
		if err != nil {
			return nil, 0, 0, nil, nil, fmt.Errorf("read AgERA5 source date %d: %w", offset, err)
		}
		if !utcDate(date) {
			return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 source date %d is not a UTC calendar date", offset)
		}
		if offset > 0 && !date.Equal(dates[offset-1].AddDate(0, 0, 1)) {
			return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 source date %d is not consecutive", offset)
		}
		dates[offset] = date
	}
	if !dates[0].Equal(group.key.first) || !dates[len(dates)-1].Equal(group.key.last) {
		return nil, 0, 0, nil, nil, errors.New("AgERA5 block dates disagree with the source plan")
	}
	latitudes := make([]float64, width*height)
	longitudes := make([]float64, width*height)
	for fieldIndex, field := range requiredFields {
		block := blocks[fieldIndex]
		identity := block.Reference()
		planned := group.refs[field]
		if identity == nil || identity.Field() != field || identity.Block() != planned.Block() || !identity.FirstDate().Equal(planned.FirstDate()) || !identity.LastDate().Equal(planned.LastDate()) {
			return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 %s returned a block for a different source reference", field)
		}
		if block.TimeCount() != count || block.LatitudeCount() != height || block.LongitudeCount() != width {
			return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 %s block dimensions disagree", field)
		}
		for offset, want := range dates {
			got, err := block.Date(offset)
			if err != nil || !got.Equal(want) {
				return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 %s source date %d disagrees", field, offset)
			}
		}
		for latitudeOffset := 0; latitudeOffset < height; latitudeOffset++ {
			for longitudeOffset := 0; longitudeOffset < width; longitudeOffset++ {
				index := latitudeOffset*width + longitudeOffset
				latitude, longitude, err := block.Location(latitudeOffset, longitudeOffset)
				if err != nil {
					return nil, 0, 0, nil, nil, fmt.Errorf("read AgERA5 %s source location %d,%d: %w", field, latitudeOffset, longitudeOffset, err)
				}
				if !finite(latitude, longitude) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
					return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 %s source location %d,%d is invalid", field, latitudeOffset, longitudeOffset)
				}
				if field == requiredFields[0] {
					latitudes[index], longitudes[index] = latitude, longitude
				} else if math.Abs(latitude-latitudes[index]) > 1e-9 || math.Abs(longitude-longitudes[index]) > 1e-9 {
					return nil, 0, 0, nil, nil, fmt.Errorf("AgERA5 %s source location %d,%d disagrees", field, latitudeOffset, longitudeOffset)
				}
			}
		}
	}
	return dates, width, height, latitudes, longitudes, nil
}

type rawValues [len(requiredFields)]float64

func readCellDay(blocks fieldBlocks, timeOffset, latitudeOffset, longitudeOffset int) (rawValues, int, error) {
	var values rawValues
	available := 0
	for index, field := range requiredFields {
		value, present, err := blocks[index].Value(timeOffset, latitudeOffset, longitudeOffset)
		if err != nil {
			return rawValues{}, 0, fmt.Errorf("read AgERA5 %s value: %w", field, err)
		}
		if present {
			values[index] = float64(value)
			available++
		}
	}
	return values, available, nil
}

type repairMagnitudes struct {
	temperature                 float64
	comfortTemperature          float64
	comfortTemperatureDirection int8
	dewPoint                    float64
}

func normalize(values rawValues) (weather.DailyMetrics, repairMagnitudes) {
	minimum, maximum := values[fieldMinimumTemperature], values[fieldMaximumTemperature]
	repairs := repairMagnitudes{}
	if minimum > maximum {
		repairs.temperature = minimum - maximum
		minimum, maximum = maximum, minimum
	}
	// AgERA5 derives its daytime mean and 24-hour extrema through separate
	// grid- and variable-specific regressions. Clamp only finite, otherwise
	// valid source temperatures that exceed the generic contract's existing
	// 5°C tolerance. Generic weather validation remains unchanged.
	comfort := values[fieldDaytimeTemperature]
	comfortC, minimumC, maximumC := comfort-273.15, minimum-273.15, maximum-273.15
	if finite(comfort, minimum, maximum) && comfortC >= -100 && comfortC <= 70 && minimumC >= -100 && minimumC <= 70 && maximumC >= -100 && maximumC <= 70 {
		switch {
		case comfort < minimum-materialComfortTemperatureCrossingC:
			repairs.comfortTemperature = minimum - comfort
			repairs.comfortTemperatureDirection = -1
			comfort = minimum
		case comfort > maximum+materialComfortTemperatureCrossingC:
			repairs.comfortTemperature = comfort - maximum
			repairs.comfortTemperatureDirection = 1
			comfort = maximum
		}
	}
	dewPoint := values[fieldDewPoint]
	dewPointC := dewPoint - 273.15
	if finite(dewPoint) && dewPointC >= -100 && dewPointC <= 70 && dewPoint > maximum+materialDewPointCrossingC {
		repairs.dewPoint = dewPoint - maximum
		dewPoint = maximum
	}
	return weather.DailyMetrics{
		ComfortableTempC: comfort - 273.15,
		MinTempC:         minimum - 273.15,
		MaxTempC:         maximum - 273.15,
		RainMM:           values[fieldPrecipitation],
		WetTimeFraction:  weather.OptionalFloat{Value: values[fieldPrecipitationDuration], Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: values[fieldSolarRadiation] / 1_000_000, Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: values[fieldCloudCover] * 100, Available: true,
		},
		WindSpeedKPH: values[fieldWindSpeed] * 3.6,
		DewPointC: weather.OptionalFloat{
			Value: dewPoint - 273.15, Available: true,
		},
		NativeResolutionKM: 30,
		Valid:              true,
	}, repairs
}

func sameCoordinates(leftLatitudes, leftLongitudes, rightLatitudes, rightLongitudes []float64) bool {
	if len(leftLatitudes) != len(rightLatitudes) || len(leftLongitudes) != len(rightLongitudes) {
		return false
	}
	for index := range leftLatitudes {
		if math.Abs(leftLatitudes[index]-rightLatitudes[index]) > 1e-9 || math.Abs(leftLongitudes[index]-rightLongitudes[index]) > 1e-9 {
			return false
		}
	}
	return true
}

func utcDate(date time.Time) bool {
	return date.Location() == time.UTC && date.Hour() == 0 && date.Minute() == 0 && date.Second() == 0 && date.Nanosecond() == 0
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
