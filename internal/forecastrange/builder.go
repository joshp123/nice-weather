package forecastrange

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	productDirectory = "forecast-ranges"
	runLockFilename  = ".install.lock"
	sealedIndex      = "index.json"
)

var beforeCatalogueInstall = func() error { return nil }

type Config struct {
	Root      string
	ReleaseID string
	Inputs    []InputCatalog
}

type Stats struct {
	PointFiles            uint32
	PointBytes            uint64
	ScalarFiles           uint32
	ScalarBytes           uint64
	CatalogBytes          uint64
	OutputFiles           uint32
	OutputBytes           uint64
	DataRecords           uint64
	NoDataRecords         uint64
	ParityRecords         uint64
	MaxInputPointBytes    uint64
	MaxInputPointRecords  uint64
	MaxRangeBlockBytes    uint64
	MaxDecodedBlockBytes  uint64
	MaxScalarBytes        uint64
	EstimatedWorkingBytes uint64
}

type Product struct {
	Root    string
	Catalog Catalog
	Stats   Stats
}

type loadedInput struct {
	reference InputCatalog
	catalog   forecastcatalog.Catalog
	root      string
	points    map[tileKey]forecastcatalog.DisplayPointTile
}

func Build(ctx context.Context, config Config) (result Product, resultErr error) {
	if err := validateBuildConfig(ctx, config); err != nil {
		return Product{}, err
	}
	if err := ensureRealDirectory(config.Root); err != nil {
		return Product{}, err
	}
	lock, err := acquireRunLock(config.Root)
	if err != nil {
		return Product{}, err
	}
	defer releaseRunLock(lock)
	if err := rejectSealed(config.Root); err != nil {
		return Product{}, err
	}
	if err := validateExistingProductParents(config.Root); err != nil {
		return Product{}, err
	}
	inputs, source, dates, err := loadInputs(config)
	if err != nil {
		return Product{}, err
	}
	final := filepath.Join(config.Root, productDirectory, Period, forecastpoints.TierGlobal)
	if _, err := os.Lstat(final); err == nil {
		catalog, encoded, err := validateInstalled(final)
		if err != nil {
			return Product{}, fmt.Errorf("existing forecast range product is invalid: %w", err)
		}
		if catalog.ReleaseID != config.ReleaseID || !slicesEqualInputs(catalog.Inputs, config.Inputs) {
			return Product{}, errors.New("immutable forecast range product already exists with different inputs")
		}
		return Product{Root: final, Catalog: catalog, Stats: statsFromCatalog(catalog, uint64(len(encoded)))}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Product{}, err
	}

	transaction, err := os.MkdirTemp(config.Root, ".forecast-range-")
	if err != nil {
		return Product{}, err
	}
	installed := false
	defer func() {
		if !installed {
			resultErr = errors.Join(resultErr, os.RemoveAll(transaction))
		}
	}()
	writer, err := tiledelivery.NewWriter(transaction, config.ReleaseID)
	if err != nil {
		return Product{}, err
	}
	stagedRoot := filepath.Join(transaction, "releases", config.ReleaseID)
	if err := os.MkdirAll(stagedRoot, 0o755); err != nil {
		return Product{}, err
	}

	catalog := Catalog{
		Version: CatalogVersion, ProductContract: ProductContract, ReleaseID: config.ReleaseID, Period: Period,
		Dates: dates, Source: source,
		Grid:             Grid{Projection: "EPSG:3857-XYZ", Zoom: MaxZoom, TileSize: tiledelivery.TileSize, Rows: worldPixels, Columns: worldPixels},
		Scores:           Scores{Unit: forecastcatalog.ScoreUnit, NoDataValue: forecastcatalog.ScoreNoData, MediaType: tiledelivery.ScalarMediaType, Profiles: []weather.Profile{weather.ProfileNice, weather.ProfileCycling}, Fields: append([]FieldContract(nil), fieldContracts...)},
		PointBlockLayout: PointBlockLayout{Rows: BlockRows, Columns: BlockColumns, HeaderBytes: BlockHeaderBytes, RecordBytes: delivery.RangePointRecordBytes, MediaType: BlockMediaType},
		Inputs:           append([]InputCatalog(nil), config.Inputs...), PointBlocks: make([]PointBlock, 0, blockCount*blockCount), ScalarCoverage: make([]MaxZoomOutcome, 0, 64), ScalarTiles: []ScalarTile{},
	}
	stats := Stats{}
	for y := uint32(0); y < 1<<MaxZoom; y++ {
		for x := uint32(0); x < 1<<MaxZoom; x++ {
			if err := ctx.Err(); err != nil {
				return Product{}, err
			}
			records, inputBytes, err := buildTileRecords(ctx, inputs, x, y)
			if err != nil {
				return Product{}, fmt.Errorf("build forecast range tile %d/%d/%d: %w", MaxZoom, x, y, err)
			}
			stats.MaxInputPointBytes = max(stats.MaxInputPointBytes, inputBytes)
			stats.MaxInputPointRecords = max(stats.MaxInputPointRecords, uint64(delivery.ForecastPointDayCount*tiledelivery.TileSize*tiledelivery.TileSize))
			outcome := MaxZoomOutcome{Zoom: MaxZoom, X: x, Y: y, NoDataRecords: tiledelivery.TileSize * tiledelivery.TileSize}
			for _, record := range records {
				if record.Nice.Valid {
					outcome.DataRecords++
					outcome.NoDataRecords--
				}
			}
			outcome.Omitted = outcome.DataRecords == 0
			catalog.ScalarCoverage = append(catalog.ScalarCoverage, outcome)
			for blockY := uint32(0); blockY < tiledelivery.TileSize/uint32(BlockRows); blockY++ {
				for blockX := uint32(0); blockX < tiledelivery.TileSize/uint32(BlockColumns); blockX++ {
					blockRow := y*(tiledelivery.TileSize/uint32(BlockRows)) + blockY
					blockColumn := x*(tiledelivery.TileSize/uint32(BlockColumns)) + blockX
					blockRecords := tileBlock(records, blockY, blockX)
					identity, _ := NewBlockIdentity(config.ReleaseID, blockRow, blockColumn)
					asset, err := WriteBlock(stagedRoot, identity, blockRecords)
					if err != nil {
						return Product{}, err
					}
					entry, err := blockEntry(stagedRoot, asset)
					if err != nil {
						return Product{}, err
					}
					catalog.PointBlocks = append(catalog.PointBlocks, entry)
					stats.DataRecords += uint64(asset.DataRecords)
					stats.NoDataRecords += uint64(asset.NoDataRecords)
					stats.MaxRangeBlockBytes = max(stats.MaxRangeBlockBytes, uint64(len(blockRecords))*delivery.RangePointRecordBytes)
				}
			}
		}
	}
	sort.Slice(catalog.PointBlocks, func(i, j int) bool {
		if catalog.PointBlocks[i].Row != catalog.PointBlocks[j].Row {
			return catalog.PointBlocks[i].Row < catalog.PointBlocks[j].Row
		}
		return catalog.PointBlocks[i].Column < catalog.PointBlocks[j].Column
	})
	if err := buildScalars(ctx, writer, stagedRoot, &catalog, &stats); err != nil {
		return Product{}, err
	}
	sort.Slice(catalog.ScalarTiles, func(i, j int) bool { return scalarLess(catalog.ScalarTiles[i], catalog.ScalarTiles[j]) })
	catalog.Coverage = calculateCoverage(catalog)
	parity, err := verifyMaxZoomParity(ctx, stagedRoot, catalog)
	if err != nil {
		return Product{}, err
	}
	stats.ParityRecords = parity
	if err := ValidateFiles(stagedRoot, catalog); err != nil {
		return Product{}, err
	}
	if err := verifyInputsUnchanged(config.Root, config.Inputs); err != nil {
		return Product{}, err
	}
	encoded, err := Marshal(catalog)
	if err != nil {
		return Product{}, err
	}
	if err := beforeCatalogueInstall(); err != nil {
		return Product{}, err
	}
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := writeCatalogueLast(filepath.Join(stagedRoot, CatalogFilename), encoded); err != nil {
		return Product{}, err
	}
	if err := syncDirectory(stagedRoot); err != nil {
		return Product{}, err
	}
	if err := validateTree(stagedRoot, catalog); err != nil {
		return Product{}, err
	}
	if err := ensureRealProductParents(config.Root); err != nil {
		return Product{}, err
	}
	if err := rejectSealed(config.Root); err != nil {
		return Product{}, err
	}
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := os.Rename(stagedRoot, final); err != nil {
		return Product{}, err
	}
	installed = true
	if err := syncDirectory(filepath.Dir(final)); err != nil {
		if _, _, validateErr := validateInstalled(final); validateErr != nil {
			rollback := filepath.Join(transaction, ".failed-install")
			if rollbackErr := os.Rename(final, rollback); rollbackErr != nil {
				return Product{}, errors.Join(err, validateErr, fmt.Errorf("rollback failed forecast range install: %w", rollbackErr))
			}
			installed = false
			return Product{}, errors.Join(err, validateErr)
		}
	}
	_ = os.RemoveAll(transaction)
	stats.CatalogBytes = uint64(len(encoded))
	stats.OutputFiles = stats.PointFiles + stats.ScalarFiles + 1
	stats.OutputBytes = stats.PointBytes + stats.ScalarBytes + stats.CatalogBytes
	stats.EstimatedWorkingBytes = stats.MaxInputPointRecords*uint64(unsafe.Sizeof(delivery.DisplayScoreRecord{})) + uint64(tiledelivery.TileSize*tiledelivery.TileSize)*uint64(unsafe.Sizeof(delivery.RangePointRecord{})) + stats.MaxDecodedBlockBytes + stats.MaxScalarBytes
	return Product{Root: final, Catalog: catalog, Stats: stats}, nil
}

func buildTileRecords(ctx context.Context, inputs []loadedInput, x, y uint32) ([]delivery.RangePointRecord, uint64, error) {
	var daily [delivery.ForecastPointDayCount][]delivery.DisplayScoreRecord
	var bytes uint64
	key := tileKey{MaxZoom, x, y}
	for index, input := range inputs {
		point, ok := input.points[key]
		if !ok {
			continue
		}
		path := filepath.Join(input.root, filepath.FromSlash(point.Path))
		if err := rejectSymlinkPath(input.root, point.Path); err != nil {
			return nil, 0, err
		}
		reader, err := tiledelivery.OpenPoint(path)
		if err != nil {
			return nil, 0, err
		}
		meta := reader.Metadata()
		if meta.ReleaseID != input.catalog.ReleaseID || meta.Period != input.catalog.LocalDate || meta.ID.Tier != input.catalog.Tier || meta.ID.Zoom != point.Zoom || meta.ID.X != point.X || meta.ID.Y != point.Y || meta.Bytes != point.Bytes || meta.SHA256 != point.SHA256 || meta.PayloadSHA256 != point.PayloadSHA256 {
			_ = reader.Close()
			return nil, 0, errors.New("daily display point does not match its catalogue")
		}
		daily[index], err = reader.Records()
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			return nil, 0, errors.Join(err, closeErr)
		}
		bytes += point.Bytes
	}
	records := make([]delivery.RangePointRecord, tiledelivery.TileSize*tiledelivery.TileSize)
	for recordIndex := range records {
		if recordIndex%tiledelivery.TileSize == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		var displays [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord
		complete := true
		for day := range displays {
			if daily[day] == nil || !daily[day][recordIndex].Valid {
				complete = false
				break
			}
			displays[day] = daily[day][recordIndex]
		}
		if !complete {
			continue
		}
		record, err := rangeRecord(displays)
		if err != nil {
			return nil, 0, err
		}
		records[recordIndex] = record
	}
	return records, bytes, nil
}

func rangeRecord(displays [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord) (delivery.RangePointRecord, error) {
	nice := make([]weather.Result, delivery.ForecastPointDayCount)
	cycling := make([]weather.Result, delivery.ForecastPointDayCount)
	var niceDays, cyclingDays [delivery.ForecastPointDayCount]uint8
	for index, display := range displays {
		if !display.Valid {
			return delivery.RangePointRecord{}, errors.New("forecast range contains an unavailable stored day")
		}
		nice[index] = storedResult(display.NiceScore, display.Thermal, display.Dryness, display.Sun, display.NiceWind)
		cycling[index] = storedResult(display.CyclingScore, display.Thermal, display.Dryness, display.Sun, display.CyclingWind)
		niceDays[index], cyclingDays[index] = display.NiceScore, display.CyclingScore
	}
	niceSummary, err := weather.SummarizeRange(nice)
	if err != nil {
		return delivery.RangePointRecord{}, err
	}
	cyclingSummary, err := weather.SummarizeRange(cycling)
	if err != nil {
		return delivery.RangePointRecord{}, err
	}
	niceRange, err := delivery.RangeRecordFromSummary(niceSummary)
	if err != nil {
		return delivery.RangePointRecord{}, err
	}
	cyclingRange, err := delivery.RangeRecordFromSummary(cyclingSummary)
	if err != nil {
		return delivery.RangePointRecord{}, err
	}
	return delivery.RangePointRecord{Nice: niceRange, Cycling: cyclingRange, NiceDays: niceDays, CyclingDays: cyclingDays}, nil
}

func storedResult(score, thermal, dryness, sun, wind uint8) weather.Result {
	value := int(score)
	return weather.Result{Available: true, Score: value, Rating: weather.Rating(value), Components: weather.Components{Thermal: int(thermal), Dryness: int(dryness), Sun: int(sun), Wind: int(wind)}}
}

func tileBlock(records []delivery.RangePointRecord, blockY, blockX uint32) []delivery.RangePointRecord {
	result := make([]delivery.RangePointRecord, int(BlockRows)*int(BlockColumns))
	for row := uint32(0); row < uint32(BlockRows); row++ {
		source := int(blockY*uint32(BlockRows)+row)*tiledelivery.TileSize + int(blockX*uint32(BlockColumns))
		copy(result[int(row)*int(BlockColumns):(int(row)+1)*int(BlockColumns)], records[source:source+int(BlockColumns)])
	}
	return result
}

func buildScalars(ctx context.Context, writer *tiledelivery.Writer, root string, catalog *Catalog, stats *Stats) error {
	blocks := make(map[[2]uint32]PointBlock, len(catalog.PointBlocks))
	for _, block := range catalog.PointBlocks {
		blocks[[2]uint32{block.Row, block.Column}] = block
	}
	maxAssets := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
	count := uint32(1) << MaxZoom
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			outcome := catalog.ScalarCoverage[int(y*count+x)]
			if outcome.Omitted {
				continue
			}
			sampler := &blockSampler{root: root, releaseID: catalog.ReleaseID, assets: blocks, cache: make(map[[2]uint32]*Block)}
			for _, field := range Fields() {
				id, err := scalarID(field, MaxZoom, x, y)
				if err != nil {
					return err
				}
				asset, err := writer.WriteScalar(id, Period, func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
					return sampler.sample(sample.GlobalX, sample.GlobalY, field)
				})
				if err != nil {
					return err
				}
				if asset.Omitted {
					return fmt.Errorf("forecast range scalar %s %d/%d/%d lost range data", field, MaxZoom, x, y)
				}
				maxAssets[id] = asset
				entry, err := scalarEntry(root, asset, field)
				if err != nil {
					return err
				}
				catalog.ScalarTiles = append(catalog.ScalarTiles, entry)
				stats.MaxScalarBytes = max(stats.MaxScalarBytes, uint64(tiledelivery.ScalarTileBytes))
			}
			stats.MaxDecodedBlockBytes = max(stats.MaxDecodedBlockBytes, uint64(len(sampler.cache))*BlockBytes)
		}
	}
	level := maxAssets
	for zoom := int(MaxZoom) - 1; zoom >= 0; zoom-- {
		next := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
		count := uint32(1) << uint8(zoom)
		for y := uint32(0); y < count; y++ {
			for x := uint32(0); x < count; x++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				for _, field := range Fields() {
					id, err := scalarID(field, uint8(zoom), x, y)
					if err != nil {
						return err
					}
					parent, err := tiledelivery.NewScalarParentSampler(id, func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) { asset, ok := level[id]; return asset, ok })
					if err != nil {
						return err
					}
					if !parent.HasInteriorData() {
						continue
					}
					asset, err := writer.WriteScalar(id, Period, parent.Sample)
					if err != nil {
						return err
					}
					if asset.Omitted {
						return errors.New("forecast range scalar parent lost child data")
					}
					next[id] = asset
					entry, err := scalarEntry(root, asset, field)
					if err != nil {
						return err
					}
					catalog.ScalarTiles = append(catalog.ScalarTiles, entry)
					stats.MaxScalarBytes = max(stats.MaxScalarBytes, parent.PeakDecodedBytes()+tiledelivery.ScalarTileBytes)
				}
			}
		}
		level = next
	}
	return nil
}

type blockSampler struct {
	root, releaseID string
	assets          map[[2]uint32]PointBlock
	cache           map[[2]uint32]*Block
}

func (sampler *blockSampler) sample(globalX, globalY int64, field forecasttiles.Field) (uint8, bool, error) {
	if globalY < 0 || globalY >= int64(worldPixels) {
		return 0, false, nil
	}
	globalX %= int64(worldPixels)
	if globalX < 0 {
		globalX += int64(worldPixels)
	}
	key := [2]uint32{uint32(globalY) / uint32(BlockRows), uint32(globalX) / uint32(BlockColumns)}
	asset := sampler.assets[key]
	if asset.Omitted {
		return 0, false, nil
	}
	block, ok := sampler.cache[key]
	if !ok {
		identity, _ := NewBlockIdentity(sampler.releaseID, key[0], key[1])
		var err error
		block, err = OpenBlock(filepath.Join(sampler.root, filepath.FromSlash(asset.Path)), identity, asset.SHA256)
		if err != nil {
			return 0, false, err
		}
		sampler.cache[key] = block
	}
	record := block.records[(uint32(globalY)%uint32(BlockRows))*uint32(BlockColumns)+uint32(globalX)%uint32(BlockColumns)]
	return rangeField(record, field)
}

func rangeField(record delivery.RangePointRecord, field forecasttiles.Field) (uint8, bool, error) {
	if !record.Nice.Valid {
		return 0, false, nil
	}
	switch field {
	case forecasttiles.FieldThermal:
		return record.Nice.Thermal, true, nil
	case forecasttiles.FieldDryness:
		return record.Nice.Dryness, true, nil
	case forecasttiles.FieldSun:
		return record.Nice.Sun, true, nil
	case forecasttiles.FieldNiceScore:
		return record.Nice.Score, true, nil
	case forecasttiles.FieldNiceWind:
		return record.Nice.Wind, true, nil
	case forecasttiles.FieldCyclingScore:
		return record.Cycling.Score, true, nil
	case forecasttiles.FieldCyclingWind:
		return record.Cycling.Wind, true, nil
	default:
		return 0, false, fmt.Errorf("unknown forecast range scalar field %q", field)
	}
}

func verifyMaxZoomParity(ctx context.Context, root string, catalog Catalog) (uint64, error) {
	blocks := make(map[[2]uint32]PointBlock, len(catalog.PointBlocks))
	for _, block := range catalog.PointBlocks {
		blocks[[2]uint32{block.Row, block.Column}] = block
	}
	scalars := make(map[struct {
		field forecasttiles.Field
		x, y  uint32
	}]ScalarTile)
	for _, scalar := range catalog.ScalarTiles {
		if scalar.Zoom == MaxZoom {
			scalars[struct {
				field forecasttiles.Field
				x, y  uint32
			}{scalar.Field, scalar.X, scalar.Y}] = scalar
		}
	}
	var compared uint64
	for _, outcome := range catalog.ScalarCoverage {
		if outcome.Omitted {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		sampler := &blockSampler{root: root, releaseID: catalog.ReleaseID, assets: blocks, cache: make(map[[2]uint32]*Block)}
		for _, field := range Fields() {
			scalar := scalars[struct {
				field forecasttiles.Field
				x, y  uint32
			}{field, outcome.X, outcome.Y}]
			encoded, _, err := tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(scalar.Path)), scalar.SHA256)
			if err != nil {
				return 0, err
			}
			for row := 0; row < tiledelivery.TileSize; row++ {
				for column := 0; column < tiledelivery.TileSize; column++ {
					globalX := int64(outcome.X*tiledelivery.TileSize + uint32(column))
					globalY := int64(outcome.Y*tiledelivery.TileSize + uint32(row))
					want, valid, err := sampler.sample(globalX, globalY, field)
					if err != nil {
						return 0, err
					}
					if !valid {
						want = tiledelivery.ScalarNoData
					}
					got := encoded[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
					if got != want {
						return 0, fmt.Errorf("forecast range point/scalar mismatch for %s at %d/%d record %d: point=%d scalar=%d", field, outcome.X, outcome.Y, row*tiledelivery.TileSize+column, want, got)
					}
					compared++
				}
			}
		}
	}
	return compared, nil
}

func ValidateFiles(root string, catalog Catalog) error {
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("forecast range root must be absolute")
	}
	if err := Validate(catalog); err != nil {
		return err
	}
	maxCounts := make(map[[2]uint32][2]uint32, len(catalog.ScalarCoverage))
	for _, entry := range catalog.PointBlocks {
		tile := [2]uint32{entry.Column / (tiledelivery.TileSize / uint32(BlockColumns)), entry.Row / (tiledelivery.TileSize / uint32(BlockRows))}
		counts := maxCounts[tile]
		counts[0] += entry.DataRecords
		counts[1] += entry.NoDataRecords
		maxCounts[tile] = counts
		if entry.Omitted {
			continue
		}
		if err := rejectSymlinkPath(root, entry.Path); err != nil {
			return err
		}
		identity, _ := NewBlockIdentity(catalog.ReleaseID, entry.Row, entry.Column)
		block, err := OpenBlock(filepath.Join(root, filepath.FromSlash(entry.Path)), identity, entry.SHA256)
		if err != nil {
			return err
		}
		meta := block.Metadata()
		if meta.Bytes != entry.Bytes || meta.PayloadSHA256 != entry.PayloadSHA256 || meta.MediaType != BlockMediaType || meta.DataRecords != entry.DataRecords || meta.NoDataRecords != entry.NoDataRecords {
			return errors.New("forecast range block metadata does not match its file")
		}
	}
	for _, outcome := range catalog.ScalarCoverage {
		counts := maxCounts[[2]uint32{outcome.X, outcome.Y}]
		if counts[0] != outcome.DataRecords || counts[1] != outcome.NoDataRecords {
			return fmt.Errorf("forecast range max-zoom coverage %d/%d does not match its point blocks", outcome.X, outcome.Y)
		}
	}
	for _, entry := range catalog.ScalarTiles {
		if err := rejectSymlinkPath(root, entry.Path); err != nil {
			return err
		}
		_, stats, err := tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(entry.Path)), entry.SHA256)
		if err != nil {
			return err
		}
		if entry.Bytes != tiledelivery.ScalarTileBytes || entry.DataTexels != stats.DataTexels || entry.NoDataTexels != stats.NoDataTexels {
			return errors.New("forecast range scalar metadata does not match its file")
		}
	}
	return nil
}

func loadInputs(config Config) ([]loadedInput, Source, []string, error) {
	loaded := make([]loadedInput, len(config.Inputs))
	dates := make([]string, len(config.Inputs))
	var run string
	for index, reference := range config.Inputs {
		if !safeRelative(reference.Path) {
			return nil, Source{}, nil, fmt.Errorf("input catalogue path %q is unsafe", reference.Path)
		}
		if err := rejectSymlinkPath(config.Root, reference.Path); err != nil {
			return nil, Source{}, nil, err
		}
		payload, err := readRegularBounded(filepath.Join(config.Root, filepath.FromSlash(reference.Path)), forecastcatalog.MaximumCatalogBytes)
		if err != nil {
			return nil, Source{}, nil, err
		}
		digest := sha256.Sum256(payload)
		if uint64(len(payload)) != reference.Bytes || hex.EncodeToString(digest[:]) != reference.SHA256 {
			return nil, Source{}, nil, fmt.Errorf("input catalogue %d does not match its indexed bytes and SHA-256", index)
		}
		catalog, err := forecastcatalog.Parse(payload)
		if err != nil {
			return nil, Source{}, nil, err
		}
		wantPath := filepath.ToSlash(filepath.Join("forecast-days", reference.Date, forecastpoints.TierGlobal, forecastcatalogFilename))
		if reference.Path != wantPath || catalog.ReleaseID != config.ReleaseID || catalog.LocalDate != reference.Date || catalog.Provider != forecastpoints.ProviderIFS || catalog.Tier != forecastpoints.TierGlobal || catalog.Evidence.Product != forecastcatalog.ProductIFSControl || catalog.Evidence.NativeResolutionKM != 28 || catalog.ScalarMaxZoom != MaxZoom {
			return nil, Source{}, nil, fmt.Errorf("input catalogue %d is not the indexed global IFS daily product", index)
		}
		if index == 0 {
			run = catalog.Evidence.RunUTC
		} else if catalog.Evidence.RunUTC != run {
			return nil, Source{}, nil, errors.New("forecast range input catalogues mix IFS runs")
		}
		points := make(map[tileKey]forecastcatalog.DisplayPointTile, len(catalog.DisplayPoints))
		for _, point := range catalog.DisplayPoints {
			points[tileKey{point.Zoom, point.X, point.Y}] = point
		}
		loaded[index] = loadedInput{reference: reference, catalog: catalog, root: filepath.Dir(filepath.Join(config.Root, filepath.FromSlash(reference.Path))), points: points}
		dates[index] = reference.Date
	}
	first, err := time.Parse(time.DateOnly, dates[0])
	if err != nil {
		return nil, Source{}, nil, errors.New("forecast range input dates are invalid")
	}
	for index, date := range dates {
		if date != first.AddDate(0, 0, index).Format(time.DateOnly) {
			return nil, Source{}, nil, errors.New("forecast range input catalogues are not 14 consecutive dates in order")
		}
	}
	return loaded, Source{Provider: forecastpoints.ProviderIFS, Tier: forecastpoints.TierGlobal, Product: forecastcatalog.ProductIFSControl, RunUTC: run, NativeResolutionKM: 28, StaleAfterHours: StaleAfterHours}, dates, nil
}

func verifyInputsUnchanged(root string, inputs []InputCatalog) error {
	for _, input := range inputs {
		payload, err := readRegularBounded(filepath.Join(root, filepath.FromSlash(input.Path)), forecastcatalog.MaximumCatalogBytes)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if uint64(len(payload)) != input.Bytes || hex.EncodeToString(digest[:]) != input.SHA256 {
			return errors.New("forecast range input catalogue changed during build")
		}
	}
	return nil
}

func blockEntry(root string, asset BlockAsset) (PointBlock, error) {
	entry := PointBlock{Row: asset.Identity.Row, Column: asset.Identity.Column, Omitted: asset.Omitted, DataRecords: asset.DataRecords, NoDataRecords: asset.NoDataRecords}
	if asset.Omitted {
		return entry, nil
	}
	relative, err := relativePath(root, asset.Path)
	if err != nil || relative != BlockPath(entry.Row, entry.Column) {
		return PointBlock{}, errors.New("forecast range block path is invalid")
	}
	entry.Path, entry.Bytes, entry.SHA256, entry.PayloadSHA256 = relative, asset.Bytes, asset.SHA256, asset.PayloadSHA256
	return entry, nil
}

func scalarEntry(root string, asset tiledelivery.ScalarAsset, field forecasttiles.Field) (ScalarTile, error) {
	relative, err := relativePath(root, asset.Path)
	if err != nil {
		return ScalarTile{}, err
	}
	want, _ := scalarPath(field, asset.ID.Zoom, asset.ID.X, asset.ID.Y)
	if relative != want {
		return ScalarTile{}, errors.New("forecast range scalar path is invalid")
	}
	return ScalarTile{Field: field, Zoom: asset.ID.Zoom, X: asset.ID.X, Y: asset.ID.Y, Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, DataTexels: asset.DataTexels, NoDataTexels: asset.NoDataTexels}, nil
}

func scalarID(field forecasttiles.Field, zoom uint8, x, y uint32) (tiledelivery.ID, error) {
	suffix := map[forecasttiles.Field]string{forecasttiles.FieldThermal: ".th", forecasttiles.FieldDryness: ".dr", forecasttiles.FieldSun: ".su", forecasttiles.FieldNiceScore: ".ns", forecasttiles.FieldNiceWind: ".nw", forecasttiles.FieldCyclingScore: ".cs", forecasttiles.FieldCyclingWind: ".cw"}[field]
	if suffix == "" {
		return tiledelivery.ID{}, errors.New("unknown forecast range scalar field")
	}
	return tiledelivery.NewID(forecastpoints.TierGlobal+suffix, zoom, int64(x), int64(y))
}

func validateInstalled(root string) (Catalog, []byte, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Catalog{}, nil, errors.New("forecast range product root must be a real directory")
	}
	payload, err := readRegularBounded(filepath.Join(root, CatalogFilename), MaximumCatalogBytes)
	if err != nil {
		return Catalog{}, nil, err
	}
	catalog, err := Parse(payload)
	if err != nil {
		return Catalog{}, nil, err
	}
	if err := validateTree(root, catalog); err != nil {
		return Catalog{}, nil, err
	}
	if err := ValidateFiles(root, catalog); err != nil {
		return Catalog{}, nil, err
	}
	after, err := readRegularBounded(filepath.Join(root, CatalogFilename), MaximumCatalogBytes)
	if err != nil {
		return Catalog{}, nil, err
	}
	if string(payload) != string(after) {
		return Catalog{}, nil, errors.New("forecast range catalogue changed during validation")
	}
	return catalog, payload, nil
}

func validateTree(root string, catalog Catalog) error {
	expected := map[string]bool{".": true, CatalogFilename: false}
	add := func(path string) error {
		if !safeRelative(path) {
			return fmt.Errorf("forecast range catalogue path %q is unsafe", path)
		}
		native := filepath.FromSlash(path)
		if _, duplicate := expected[native]; duplicate {
			return fmt.Errorf("forecast range asset path %q is duplicated", path)
		}
		expected[native] = false
		for parent := filepath.Dir(native); parent != "."; parent = filepath.Dir(parent) {
			expected[parent] = true
		}
		return nil
	}
	for _, block := range catalog.PointBlocks {
		if !block.Omitted {
			if err := add(block.Path); err != nil {
				return err
			}
		}
	}
	for _, scalar := range catalog.ScalarTiles {
		if err := add(scalar.Path); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(expected))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		wantDirectory, ok := expected[relative]
		if !ok {
			return fmt.Errorf("forecast range product has unreferenced entry %q", filepath.ToSlash(relative))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
			return fmt.Errorf("forecast range entry %q has unsafe type", filepath.ToSlash(relative))
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("forecast range product is missing referenced entries")
	}
	return nil
}

func validateBuildConfig(ctx context.Context, config Config) error {
	if ctx == nil || config.Root == "" || !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root || !validIdentifier(config.ReleaseID) || len(config.Inputs) != delivery.ForecastPointDayCount {
		return errors.New("forecast range build config is incomplete")
	}
	for _, input := range config.Inputs {
		if input.Bytes == 0 || input.Bytes > forecastcatalog.MaximumCatalogBytes || !validSHA256(input.SHA256) || !safeRelative(input.Path) {
			return errors.New("forecast range build input is invalid")
		}
	}
	return ctx.Err()
}

func acquireRunLock(root string) (*os.File, error) {
	filename := filepath.Join(root, runLockFilename)
	fd, err := syscall.Open(filename, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open forecast run install lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), filename)
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, errors.New("forecast run install lock changed while opening")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock forecast run installation: %w", err)
	}
	return lock, nil
}

func releaseRunLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}
func rejectSealed(root string) error {
	info, err := os.Lstat(filepath.Join(root, sealedIndex))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info != nil {
		return errors.New("forecast run is sealed by its immutable index")
	}
	return nil
}
func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("forecast run root must be a real directory")
	}
	return nil
}

func validateExistingProductParents(root string) error {
	current := root
	for _, component := range []string{productDirectory, Period} {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("forecast range install parent %q must be a real directory", component)
		}
	}
	return nil
}

func ensureRealProductParents(root string) error {
	current := root
	for _, component := range []string{productDirectory, Period} {
		current = filepath.Join(current, component)
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("forecast range install parent %q must be a real directory", component)
		}
	}
	return nil
}

func rejectSymlinkPath(root, relative string) error {
	if !safeRelative(relative) {
		return errors.New("forecast range path is unsafe")
	}
	current := root
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("forecast range path %q contains a symlink", relative)
		}
	}
	return nil
}

func readRegularBounded(path string, maximum int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("forecast range file is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(payload) == 0 || len(payload) > maximum {
		return nil, errors.New("forecast range file could not be read safely")
	}
	return payload, nil
}

func relativePath(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("forecast range asset is outside its product root")
	}
	return filepath.ToSlash(relative), nil
}
func writeCatalogueLast(path string, encoded []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	open := true
	defer func() {
		if open {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	open = false
	return nil
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
func slicesEqualInputs(left, right []InputCatalog) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func statsFromCatalog(catalog Catalog, catalogBytes uint64) Stats {
	stats := Stats{CatalogBytes: catalogBytes}
	for _, block := range catalog.PointBlocks {
		stats.DataRecords += uint64(block.DataRecords)
		stats.NoDataRecords += uint64(block.NoDataRecords)
		if !block.Omitted {
			stats.PointFiles++
			stats.PointBytes += block.Bytes
		}
	}
	for _, scalar := range catalog.ScalarTiles {
		stats.ScalarFiles++
		stats.ScalarBytes += scalar.Bytes
	}
	stats.OutputFiles = stats.PointFiles + stats.ScalarFiles + 1
	stats.OutputBytes = stats.PointBytes + stats.ScalarBytes + stats.CatalogBytes
	return stats
}
