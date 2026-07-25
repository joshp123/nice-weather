// Package forecastassembly commits one prepared provider tier and local day
// as native point blocks, evaluated scalar tiles and one catalogue. It owns no
// provider download, run selection, scoring policy or multi-tier composition.
package forecastassembly

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	CatalogName           = "catalog.json"
	sealedRunIndex        = "index.json"
	runLockExtension      = ".install.lock"
	transactionPrefix     = ".forecast-product-"
	transactionMarker     = ".nice-weather-product-transaction-v1"
	transactionMarkerBody = "nice-weather-finder forecast product transaction v1\n"
)

var (
	syncForecastDirectory     = syncDirectory
	validateForecastInstalled = validateInstalled
	removeTransaction         = os.RemoveAll
)

// Config identifies one already prepared provider run and one local day.
// Root is an absolute caller-owned assembly directory, not a publication root.
type Config struct {
	Root      string
	ReleaseID string
	Provider  string
	Tier      string
	Date      time.Time
	Series    pipeline.PreparedSeries
	Locate    forecasttiles.LocateTimezone
	Land      forecasttiles.LandMask
}

// Stats is deterministic structural evidence from a complete build.
// EstimatedBulkWorkingBytes covers the dominant simultaneously retained data
// planes. It is not a process-memory claim; integration tests separately
// report observed process peak heap while traversing the exact worldwide grid.
type Stats struct {
	ExpectedPointBlocks       uint32
	AvailablePointBlocks      uint32
	OmittedPointBlocks        uint32
	PointFiles                uint32
	PointBytes                uint64
	DisplayPointFiles         uint32
	DisplayPointBytes         uint64
	ScalarFiles               uint32
	ScalarBytes               uint64
	CatalogBytes              uint64
	OutputFiles               uint32
	OutputBytes               uint64
	ParityRecords             uint64
	MaxMetricCells            uint64
	MaxPointRecords           uint64
	MaxDisplayRecords         uint64
	MaxScalarBytes            uint64
	EstimatedBulkWorkingBytes uint64
}

type Product struct {
	Root    string
	Catalog forecastcatalog.Catalog
	Stats   Stats
}

// BuildDay builds inside one private transaction, writes the catalogue after
// every referenced asset validates, and atomically installs the complete
// day/tier directory. Failure and cancellation leave no final product.
func BuildDay(ctx context.Context, config Config) (product Product, resultErr error) {
	maxZoom, err := validateConfig(ctx, config)
	if err != nil {
		return Product{}, err
	}
	if err := os.MkdirAll(config.Root, 0o755); err != nil {
		return Product{}, err
	}
	runLock, err := acquireRunInstallLock(config.Root)
	if err != nil {
		return Product{}, err
	}
	defer releaseRunInstallLock(runLock)
	if err := rejectSealedRun(config.Root); err != nil {
		return Product{}, err
	}
	if err := cleanAbandonedTransactionsLocked(config.Root); err != nil {
		return Product{}, err
	}
	period := config.Date.Format("2006-01-02")
	final := filepath.Join(config.Root, "forecast-days", period, config.Tier)
	if _, err := os.Lstat(final); err == nil {
		return Product{}, fmt.Errorf("immutable forecast product already exists: %s", final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Product{}, err
	}
	transaction, err := os.MkdirTemp(config.Root, transactionPrefix)
	if err != nil {
		return Product{}, err
	}
	if err := markTransaction(transaction); err != nil {
		return Product{}, errors.Join(err, removeTransaction(transaction))
	}
	installed := false
	defer func() {
		if !installed {
			resultErr = errors.Join(resultErr, removeTransaction(transaction))
		}
	}()

	scalarCatalog, err := forecasttiles.BuildDay(ctx, forecasttiles.Config{
		Root: transaction, ReleaseID: config.ReleaseID, Period: period, Tier: config.Tier,
		Date: config.Date, MaxZoom: maxZoom, Series: config.Series, Locate: config.Locate, Land: config.Land,
	})
	if err != nil {
		return Product{}, err
	}
	dayRoot := filepath.Join(transaction, "forecast-days", period, config.Tier)
	points, pointStats, err := writeNativeBlocks(ctx, dayRoot, config, scalarCatalog.Source)
	if err != nil {
		return Product{}, err
	}
	runUTC, err := time.Parse(time.RFC3339, scalarCatalog.Source.RunUTC)
	if err != nil {
		return Product{}, fmt.Errorf("parse prepared source run: %w", err)
	}
	catalog, err := forecastcatalog.New(forecastcatalog.Config{
		ReleaseID: config.ReleaseID, LocalDate: period, Provider: config.Provider, Tier: config.Tier,
		RunUTC: runUTC, SourceEvidenceSHA256: scalarCatalog.Source.EvidenceSHA256,
	}, points, scalarCatalog)
	if err != nil {
		return Product{}, err
	}
	parityRecords, err := verifyMaxZoomParity(ctx, dayRoot, scalarCatalog)
	if err != nil {
		return Product{}, err
	}
	if err := os.Remove(filepath.Join(dayRoot, "forecast-tiles.json")); err != nil {
		return Product{}, err
	}
	if err := forecastcatalog.ValidateFiles(dayRoot, catalog); err != nil {
		return Product{}, err
	}
	encoded, err := forecastcatalog.Marshal(catalog)
	if err != nil {
		return Product{}, err
	}
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := writeCatalogueLast(filepath.Join(dayRoot, CatalogName), encoded); err != nil {
		return Product{}, err
	}
	if err := syncForecastDirectory(dayRoot); err != nil {
		return Product{}, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return Product{}, err
	}
	if err := ctx.Err(); err != nil {
		return Product{}, err
	}
	if err := rejectSealedRun(config.Root); err != nil {
		return Product{}, err
	}
	if err := os.Rename(dayRoot, final); err != nil {
		return Product{}, err
	}
	installed = true
	if err := syncForecastDirectory(filepath.Dir(final)); err != nil {
		if validateErr := validateForecastInstalled(final, catalog, encoded); validateErr != nil {
			rollback := filepath.Join(transaction, ".failed-install")
			if rollbackErr := os.Rename(final, rollback); rollbackErr != nil {
				return Product{}, errors.Join(err, validateErr, fmt.Errorf("rollback failed forecast install: %w", rollbackErr))
			}
			installed = false
			return Product{}, errors.Join(err, validateErr)
		}
	}
	if err := removeTransaction(transaction); err != nil {
		return Product{}, fmt.Errorf("clean installed forecast product transaction: %w", err)
	}

	stats := assembleStats(config.Series, catalog, scalarCatalog, pointStats, uint64(len(encoded)), parityRecords)
	return Product{Root: final, Catalog: catalog, Stats: stats}, nil
}

func acquireRunInstallLock(root string) (*os.File, error) {
	filename := filepath.Join(root, runLockExtension)
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
		return nil, fmt.Errorf("forecast run installation or publication is active: %w", err)
	}
	return lock, nil
}

func releaseRunInstallLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func markTransaction(root string) error {
	if err := os.WriteFile(filepath.Join(root, transactionMarker), []byte(transactionMarkerBody), 0o600); err != nil {
		return fmt.Errorf("mark forecast product transaction: %w", err)
	}
	return nil
}

// CleanAbandonedTransactions removes only marked, current-user day-product
// transactions while holding the shared run lock. An unowned matching entry
// is preserved and rejected because it would poison exact-tree publication.
func CleanAbandonedTransactions(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("forecast run root must be a real directory")
	}
	lock, err := acquireRunInstallLock(root)
	if err != nil {
		return err
	}
	defer releaseRunInstallLock(lock)
	if err := rejectSealedRun(root); err != nil {
		return err
	}
	return cleanAbandonedTransactionsLocked(root)
}

func cleanAbandonedTransactionsLocked(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), transactionPrefix) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		owned, err := ownedTransaction(path)
		if err != nil {
			return fmt.Errorf("inspect abandoned forecast product transaction %q: %w", entry.Name(), err)
		}
		if !owned {
			return fmt.Errorf("refuse unowned forecast product transaction %q", entry.Name())
		}
		if err := removeTransaction(path); err != nil {
			return fmt.Errorf("clean abandoned forecast product transaction %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func ownedTransaction(root string) (bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, nil
	}
	marker := filepath.Join(root, transactionMarker)
	markerInfo, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 || markerInfo.Size() != int64(len(transactionMarkerBody)) {
		return false, err
	}
	payload, err := os.ReadFile(marker)
	return err == nil && string(payload) == transactionMarkerBody, err
}

func rejectSealedRun(root string) error {
	_, err := os.Lstat(filepath.Join(root, sealedRunIndex))
	if err == nil {
		return errors.New("forecast run is sealed by its immutable index")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type nativeStats struct {
	maxMetricCells  uint64
	maxPointRecords uint64
}

func writeNativeBlocks(ctx context.Context, root string, config Config, source forecasttiles.SourceIdentity) ([]forecastpoints.Asset, nativeStats, error) {
	rows, columns, err := forecastpoints.BlockGrid(config.Provider, config.Tier, source.Grid)
	if err != nil {
		return nil, nativeStats{}, err
	}
	assets := make([]forecastpoints.Asset, 0, rows*columns)
	stats := nativeStats{}
	for blockRow := uint32(0); blockRow < rows; blockRow++ {
		for blockColumn := uint32(0); blockColumn < columns; blockColumn++ {
			if err := ctx.Err(); err != nil {
				return nil, nativeStats{}, err
			}
			identity, err := forecastpoints.NewIdentity(
				config.ReleaseID, config.Provider, config.Tier, config.Date.Format("2006-01-02"),
				source.RunUTC, source.EvidenceSHA256, source.Grid,
				forecastpoints.Coordinate{Row: blockRow, Column: blockColumn},
			)
			if err != nil {
				return nil, nativeStats{}, err
			}
			row, column, height, width, err := forecastpoints.CellWindow(identity)
			if err != nil {
				return nil, nativeStats{}, err
			}
			window := pipeline.GridWindow{Row: row, Column: column, Rows: height, Cols: width}
			aggregated, err := pipeline.AggregatePreparedDayWindow(ctx, config.Date, window, config.Locate, config.Series)
			if err != nil {
				return nil, nativeStats{}, fmt.Errorf("aggregate native forecast block %d/%d: %w", blockRow, blockColumn, err)
			}
			stats.maxMetricCells = max(stats.maxMetricCells, uint64(len(aggregated.Metrics)))
			records, err := nativePointRecords(ctx, aggregated)
			if err != nil {
				return nil, nativeStats{}, fmt.Errorf("evaluate native forecast block %d/%d: %w", blockRow, blockColumn, err)
			}
			stats.maxPointRecords = max(stats.maxPointRecords, uint64(len(records)))
			asset, err := forecastpoints.WriteBlock(root, identity, records)
			if err != nil {
				return nil, nativeStats{}, fmt.Errorf("write native forecast block %d/%d: %w", blockRow, blockColumn, err)
			}
			assets = append(assets, asset)
		}
	}
	return assets, stats, nil
}

func nativePointRecords(ctx context.Context, window pipeline.DailyMetricsWindow) ([]delivery.DailyPointRecord, error) {
	if window.Core.Rows == 0 || window.Core.Cols == 0 || window.Spec.Rows == 0 || window.Spec.Cols == 0 || len(window.Metrics) != int(window.Spec.Rows)*int(window.Spec.Cols) {
		return nil, errors.New("native forecast block has invalid aggregated geometry")
	}
	coreRow := int64(window.Core.Row) - window.OriginRow
	coreColumn := int64(window.Core.Column) - window.OriginColumn
	if coreRow < 0 || coreColumn < 0 || coreRow+int64(window.Core.Rows) > int64(window.Spec.Rows) || coreColumn+int64(window.Core.Cols) > int64(window.Spec.Cols) {
		return nil, errors.New("native forecast block core is outside its prepared window")
	}
	records := make([]delivery.DailyPointRecord, int(window.Core.Rows)*int(window.Core.Cols))
	for row := uint32(0); row < window.Core.Rows; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for column := uint32(0); column < window.Core.Cols; column++ {
			metricIndex := int(coreRow+int64(row))*int(window.Spec.Cols) + int(coreColumn+int64(column))
			metric := window.Metrics[metricIndex]
			if !metric.Valid {
				continue
			}
			weatherRecord, err := delivery.RecordFromWeather(metric)
			if err != nil {
				return nil, err
			}
			point, err := delivery.DailyPointRecordFromWeather(weatherRecord)
			if err != nil {
				return nil, err
			}
			records[int(row)*int(window.Core.Cols)+int(column)] = point
		}
	}
	return records, nil
}

func verifyMaxZoomParity(ctx context.Context, root string, catalog forecasttiles.Catalog) (uint64, error) {
	scalars := make(map[scalarKey]forecasttiles.ScalarTile)
	for _, scalar := range catalog.Scalars {
		if scalar.Zoom == catalog.MaxZoom {
			scalars[scalarKey{field: scalar.Field, x: scalar.X, y: scalar.Y}] = scalar
		}
	}
	var compared uint64
	for _, point := range catalog.Points {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		path := filepath.Join(root, filepath.FromSlash(point.Path))
		reader, err := tiledelivery.OpenPoint(path)
		if err != nil {
			return 0, err
		}
		metadata := reader.Metadata()
		if metadata.SHA256 != point.SHA256 || metadata.PayloadSHA256 != point.PayloadSHA256 || metadata.Bytes != point.Bytes || metadata.ID.Zoom != catalog.MaxZoom || metadata.ID.X != point.X || metadata.ID.Y != point.Y {
			_ = reader.Close()
			return 0, fmt.Errorf("max-zoom point tile %d/%d metadata does not match its catalogue", point.X, point.Y)
		}
		records, readErr := reader.Records()
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return 0, errors.Join(readErr, closeErr)
		}
		blocksPerTile := uint32(tiledelivery.TileSize / tiledelivery.PointBlockSize)
		tileX, tileY := point.X/blocksPerTile, point.Y/blocksPerTile
		blockX, blockY := int(point.X%blocksPerTile), int(point.Y%blocksPerTile)
		for _, field := range forecastFields() {
			scalar, ok := scalars[scalarKey{field: field, x: tileX, y: tileY}]
			if !ok {
				return 0, fmt.Errorf("max-zoom point tile %d/%d has no %s scalar", point.X, point.Y, field)
			}
			var encoded []byte
			var err error
			if catalog.Tier == "europe-detail" {
				encoded, _, err = tiledelivery.ReadForecastScalar(filepath.Join(root, filepath.FromSlash(scalar.Path)), scalar.SHA256)
			} else {
				encoded, _, err = tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(scalar.Path)), scalar.SHA256)
			}
			if err != nil {
				return 0, err
			}
			for index, record := range records {
				row, column := index/tiledelivery.PointBlockSize, index%tiledelivery.PointBlockSize
				row += blockY * tiledelivery.PointBlockSize
				column += blockX * tiledelivery.PointBlockSize
				got := encoded[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
				want, valid, err := displayField(record, field)
				if err != nil {
					return 0, err
				}
				if !valid {
					if catalog.Tier == "europe-detail" && got == tiledelivery.ForecastScalarFallback {
						compared++
						continue
					}
					want = tiledelivery.ScalarNoData
				}
				if got != want {
					return 0, fmt.Errorf("point/scalar mismatch for %s at %d/%d record %d: point=%d scalar=%d", field, point.X, point.Y, index, want, got)
				}
				compared++
			}
		}
	}
	return compared, nil
}

type scalarKey struct {
	field forecasttiles.Field
	x, y  uint32
}

func forecastFields() []forecasttiles.Field {
	return []forecasttiles.Field{
		forecasttiles.FieldThermal, forecasttiles.FieldDryness, forecasttiles.FieldSun,
		forecasttiles.FieldNiceScore, forecasttiles.FieldNiceWind,
		forecasttiles.FieldCyclingScore, forecasttiles.FieldCyclingWind,
	}
}

func displayField(record delivery.DailyPointRecord, field forecasttiles.Field) (uint8, bool, error) {
	if !record.Weather.Valid {
		return 0, false, nil
	}
	switch field {
	case forecasttiles.FieldThermal:
		return record.Thermal, true, nil
	case forecasttiles.FieldDryness:
		return record.Dryness, true, nil
	case forecasttiles.FieldSun:
		return record.Sun, true, nil
	case forecasttiles.FieldNiceScore:
		return record.NiceScore, true, nil
	case forecasttiles.FieldNiceWind:
		return record.NiceWind, true, nil
	case forecasttiles.FieldCyclingScore:
		return record.CyclingScore, true, nil
	case forecasttiles.FieldCyclingWind:
		return record.CyclingWind, true, nil
	default:
		return 0, false, fmt.Errorf("unknown forecast scalar field %q", field)
	}
}

func validateConfig(ctx context.Context, config Config) (uint8, error) {
	if ctx == nil {
		return 0, errors.New("nil forecast assembly context")
	}
	if config.Root == "" || !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root || config.ReleaseID == "" || config.Date.IsZero() || len(config.Series) == 0 || config.Locate == nil || config.Land.Identity() == (forecasttiles.LandIdentity{}) {
		return 0, errors.New("forecast assembly config is incomplete")
	}
	if config.Date.Location() != time.UTC || config.Date.Hour() != 0 || config.Date.Minute() != 0 || config.Date.Second() != 0 || config.Date.Nanosecond() != 0 {
		return 0, errors.New("forecast assembly date must be one UTC calendar day")
	}
	switch {
	case config.Provider == forecastpoints.ProviderIFS && config.Tier == forecastpoints.TierGlobal:
		return 3, nil
	case config.Provider == forecastpoints.ProviderICONEU && config.Tier == forecastpoints.TierEurope:
		return 5, nil
	default:
		return 0, errors.New("forecast assembly provider and tier are unsupported")
	}
}

func assembleStats(series pipeline.PreparedSeries, catalog forecastcatalog.Catalog, scalars forecasttiles.Catalog, native nativeStats, catalogBytes, parityRecords uint64) Stats {
	stats := Stats{
		ExpectedPointBlocks: catalog.Coverage.ExpectedPointBlocks, AvailablePointBlocks: catalog.Coverage.AvailablePointBlocks,
		OmittedPointBlocks: catalog.Coverage.OmittedPointBlocks, PointFiles: catalog.Coverage.AvailablePointBlocks,
		DisplayPointFiles: uint32(len(catalog.DailyPointBlocks)), ScalarFiles: uint32(len(catalog.ScalarTiles)), CatalogBytes: catalogBytes, ParityRecords: parityRecords,
		MaxMetricCells: max(native.maxMetricCells, scalars.Stats.MaxMetricCells), MaxPointRecords: native.maxPointRecords,
		MaxDisplayRecords: scalars.Stats.MaxDisplayRecords, MaxScalarBytes: scalars.Stats.MaxScalarBytes,
	}
	for _, block := range catalog.PointBlocks {
		stats.PointBytes += block.Bytes
	}
	for _, scalar := range catalog.ScalarTiles {
		stats.ScalarBytes += scalar.Bytes
	}
	for _, point := range catalog.DailyPointBlocks {
		stats.DisplayPointBytes += point.Bytes
	}
	stats.OutputFiles = stats.PointFiles + stats.DisplayPointFiles + stats.ScalarFiles + 1
	stats.OutputBytes = stats.PointBytes + stats.DisplayPointBytes + stats.ScalarBytes + stats.CatalogBytes
	frameCount := uint64(0)
	for _, frames := range series {
		frameCount += uint64(len(frames))
	}
	metricBytes := uint64(unsafe.Sizeof(weather.DailyMetrics{}))
	displayBytes := uint64(unsafe.Sizeof(delivery.DailyPointRecord{}))
	pointBytes := uint64(unsafe.Sizeof(delivery.DailyPointRecord{}))
	// Each aggregation can retain the source Float32 windows, cumulative
	// normalization scratch, local-time windows, accumulators and output
	// metrics. 256 bytes per cell covers those fixed structures; doubling all
	// source-frame Float32 cells covers cumulative product copies.
	aggregationBound := func(cells uint64) uint64 {
		return cells * (256 + metricBytes + 2*frameCount*4)
	}
	tilePeak := aggregationBound(scalars.Stats.MaxMetricCells) + scalars.Stats.MaxDisplayRecords*displayBytes + scalars.Stats.MaxScalarBytes
	pointPeak := aggregationBound(native.maxMetricCells) + native.maxPointRecords*pointBytes
	catalogPeak := uint64(len(catalog.PointBlocks))*uint64(unsafe.Sizeof(forecastcatalog.PointBlock{})) + uint64(len(catalog.ScalarTiles))*uint64(unsafe.Sizeof(forecastcatalog.ScalarTile{})) + catalogBytes
	stats.EstimatedBulkWorkingBytes = max(tilePeak, pointPeak) + catalogPeak
	return stats
}

func validateInstalled(root string, catalog forecastcatalog.Catalog, encoded []byte) error {
	actual, err := os.ReadFile(filepath.Join(root, CatalogName))
	if err != nil {
		return err
	}
	if string(actual) != string(encoded) {
		return errors.New("installed forecast catalogue changed after rename")
	}
	return forecastcatalog.ValidateFiles(root, catalog)
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
