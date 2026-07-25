package forecastassembly

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
)

func TestBuildExactWorldwideIFSProductIsCompleteBoundedAndCatalogueLast(t *testing.T) {
	config, closeFrames := exactIFSConfig(t)
	defer closeFrames()
	abandoned, err := os.MkdirTemp(config.Root, transactionPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := markTransaction(abandoned); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "partial"), []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalSync := syncForecastDirectory
	defer func() { syncForecastDirectory = originalSync }()
	syncForecastDirectory = func(path string) error {
		if filepath.Clean(path) == filepath.Join(config.Root, "forecast-days", config.Date.Format("2006-01-02")) {
			return errors.New("injected parent directory sync ambiguity")
		}
		return originalSync(path)
	}
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var peak atomic.Uint64
	peak.Store(baseline.HeapAlloc)
	stopMemory := make(chan struct{})
	memoryDone := make(chan struct{})
	go func() {
		defer close(memoryDone)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				for current := peak.Load(); sample.HeapAlloc > current && !peak.CompareAndSwap(current, sample.HeapAlloc); current = peak.Load() {
				}
			case <-stopMemory:
				return
			}
		}
	}()
	product, err := BuildDay(context.Background(), config)
	close(stopMemory)
	<-memoryDone
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(abandoned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned forecast product transaction remains after retry: %v", err)
	}
	if product.Root != filepath.Join(config.Root, "forecast-days", "2026-07-20", forecastpoints.TierGlobal) {
		t.Fatalf("product root = %q", product.Root)
	}
	if product.Catalog.Grid.Rows != 720 || product.Catalog.Grid.Columns != 1440 || product.Catalog.Coverage.ExpectedPointBlocks != 276 {
		t.Fatalf("worldwide catalogue geometry = %+v coverage=%+v", product.Catalog.Grid, product.Catalog.Coverage)
	}
	if product.Stats.AvailablePointBlocks == 0 || product.Stats.OmittedPointBlocks == 0 || product.Stats.PointFiles != product.Stats.AvailablePointBlocks || product.Stats.ScalarFiles == 0 {
		t.Fatalf("build did not prove data and all-no-data outcomes: %+v", product.Stats)
	}
	if product.Stats.MaxPointRecords != 64*64 || product.Stats.MaxMetricCells > 40_000 || product.Stats.EstimatedBulkWorkingBytes == 0 || product.Stats.EstimatedBulkWorkingBytes > 512<<20 || peak.Load() > 512<<20 {
		t.Fatalf("working set is not block/tile bounded: %+v", product.Stats)
	}
	if product.Stats.ParityRecords == 0 || product.Stats.DisplayPointFiles == 0 || product.Stats.OutputFiles != product.Stats.PointFiles+product.Stats.DisplayPointFiles+product.Stats.ScalarFiles+1 {
		t.Fatalf("parity/output evidence = %+v", product.Stats)
	}
	encoded, err := os.ReadFile(filepath.Join(product.Root, CatalogName))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := forecastcatalog.Parse(encoded)
	if err != nil || parsed.ReleaseID != product.Catalog.ReleaseID {
		t.Fatalf("installed catalogue: err=%v value=%+v", err, parsed)
	}
	if err := forecastcatalog.ValidateFiles(product.Root, parsed); err != nil {
		t.Fatal(err)
	}
	const islandLongitude, islandLatitude = .02, .02
	displayID, _, _, err := tiledelivery.PointAt(forecastpoints.TierGlobal, 3, islandLongitude, islandLatitude)
	if err != nil {
		t.Fatal(err)
	}
	var coastalDisplayBlock forecastcatalog.DailyPointBlock
	for _, block := range parsed.DailyPointBlocks {
		if block.X == displayID.X && block.Y == displayID.Y {
			coastalDisplayBlock = block
			break
		}
	}
	if coastalDisplayBlock.Path != "" {
		displayReader, openErr := tiledelivery.OpenPoint(filepath.Join(product.Root, filepath.FromSlash(coastalDisplayBlock.Path)))
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, displayValid, displayErr := displayReader.At(islandLongitude, islandLatitude)
		closeDisplayErr := displayReader.Close()
		if displayErr != nil || closeDisplayErr != nil || displayValid {
			t.Fatalf("water-centred coastal display value: valid=%t err=%v close=%v", displayValid, displayErr, closeDisplayErr)
		}
	}
	nativeIdentity, ok, err := forecastpoints.CoordinateIdentity(
		parsed.ReleaseID, parsed.Provider, parsed.Tier, parsed.LocalDate, parsed.Evidence.RunUTC,
		parsed.Evidence.SourceEvidenceSHA256,
		delivery.GridSpec{Bounds: delivery.GridBounds{West: parsed.Grid.West, East: parsed.Grid.East, South: parsed.Grid.South, North: parsed.Grid.North}, Rows: parsed.Grid.Rows, Cols: parsed.Grid.Columns},
		islandLongitude, islandLatitude,
	)
	if err != nil || !ok {
		t.Fatalf("tiny-island native identity: ok=%t err=%v", ok, err)
	}
	var nativeBlock forecastcatalog.PointBlock
	for _, block := range parsed.PointBlocks {
		if block.Row == nativeIdentity.Block.Row && block.Column == nativeIdentity.Block.Column {
			nativeBlock = block
			break
		}
	}
	if nativeBlock.Omitted || nativeBlock.Path == "" {
		t.Fatalf("tiny-island native weather block unavailable: %+v", nativeBlock)
	}
	opened, err := forecastpoints.OpenBlock(filepath.Join(product.Root, filepath.FromSlash(nativeBlock.Path)), nativeIdentity, nativeBlock.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	native, nativeValid, err := opened.RecordAt(islandLongitude, islandLatitude)
	if err != nil || !nativeValid || !native.Weather.Valid {
		t.Fatalf("coastal native evidence: native=%+v valid=%t err=%v", native, nativeValid, err)
	}
	for _, removed := range []string{"forecast-tiles.json"} {
		if _, err := os.Lstat(filepath.Join(product.Root, removed)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("build-only proof output %q remains: %v", removed, err)
		}
	}
	files, bytes, err := treeStats(product.Root)
	if err != nil {
		t.Fatal(err)
	}
	if files != product.Stats.OutputFiles || bytes != product.Stats.OutputBytes || uint64(len(encoded)) != product.Stats.CatalogBytes {
		t.Fatalf("actual files/bytes=%d/%d catalogue=%d; stats=%+v", files, bytes, len(encoded), product.Stats)
	}
	if peak.Load() < baseline.HeapAlloc {
		t.Fatalf("observed peak heap %d is below baseline %d", peak.Load(), baseline.HeapAlloc)
	}
	t.Logf("exact 720x1440 IFS day: %d files, %d bytes, %d omitted point blocks, %d exhaustive parity records, estimated bulk planes <=%d bytes, observed process peak heap=%d bytes (baseline=%d)", files, bytes, product.Stats.OmittedPointBlocks, product.Stats.ParityRecords, product.Stats.EstimatedBulkWorkingBytes, peak.Load(), baseline.HeapAlloc)
}

func TestBuildCancellationAndExistingProductLeaveNoTransaction(t *testing.T) {
	config, closeFrames := exactIFSConfig(t)
	defer closeFrames()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildDay(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build error = %v", err)
	}
	entries, err := os.ReadDir(config.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".forecast-product-") {
			t.Fatalf("cancelled transaction remains: %s", entry.Name())
		}
	}
	final := filepath.Join(config.Root, "forecast-days", config.Date.Format("2006-01-02"), config.Tier)
	if err := os.MkdirAll(final, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing immutable product error = %v", err)
	}
}

func TestBuildRefusesUnownedMatchingTransactionsWithoutDeletingThem(t *testing.T) {
	for _, test := range []struct {
		name string
		seed func(*testing.T, string) string
	}{
		{name: "wrong marker", seed: func(t *testing.T, root string) string {
			path := filepath.Join(root, transactionPrefix+"foreign")
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, transactionMarker), []byte("wrong\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "symlink", seed: func(t *testing.T, root string) string {
			path := filepath.Join(root, transactionPrefix+"link")
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, closeFrames := exactIFSConfig(t)
			defer closeFrames()
			transaction := test.seed(t, config.Root)
			if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "refuse unowned") {
				t.Fatalf("unowned transaction error = %v", err)
			}
			if _, err := os.Lstat(transaction); err != nil {
				t.Fatalf("unowned transaction was deleted: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(config.Root, "forecast-days")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unowned transaction did not fail before output: %v", err)
			}
		})
	}
}

func TestBuildReportsSuccessfulInstallTransactionCleanupFailure(t *testing.T) {
	config, closeFrames := exactIFSConfig(t)
	defer closeFrames()
	original := removeTransaction
	removeTransaction = func(string) error { return errors.New("injected cleanup failure") }
	t.Cleanup(func() { removeTransaction = original })
	_, err := BuildDay(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "clean installed forecast product transaction") {
		t.Fatalf("successful install cleanup error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Root, "forecast-days", config.Date.Format(time.DateOnly), config.Tier, CatalogName)); err != nil {
		t.Fatalf("completed immutable day product was lost after cleanup failure: %v", err)
	}
}

func TestBuildRejectsRunSealedByPublicationIndex(t *testing.T) {
	config, closeFrames := exactIFSConfig(t)
	defer closeFrames()
	if err := os.WriteFile(filepath.Join(config.Root, sealedRunIndex), []byte("sealed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("sealed run build error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(config.Root, "forecast-days")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed run was modified: %v", err)
	}
}

func TestFailedPostRenameReconciliationRollsBackExactProduct(t *testing.T) {
	config, closeFrames := exactIFSConfig(t)
	defer closeFrames()
	originalSync, originalValidate := syncForecastDirectory, validateForecastInstalled
	defer func() {
		syncForecastDirectory, validateForecastInstalled = originalSync, originalValidate
	}()
	syncForecastDirectory = func(path string) error {
		if filepath.Clean(path) == filepath.Join(config.Root, "forecast-days", config.Date.Format("2006-01-02")) {
			return errors.New("injected parent directory sync ambiguity")
		}
		return originalSync(path)
	}
	validateForecastInstalled = func(string, forecastcatalog.Catalog, []byte) error {
		return errors.New("injected installed product validation failure")
	}
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "validation failure") {
		t.Fatalf("post-rename failure = %v", err)
	}
	final := filepath.Join(config.Root, "forecast-days", config.Date.Format("2006-01-02"), config.Tier)
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed installed product remains: %v", err)
	}
	entries, err := os.ReadDir(config.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".forecast-product-") {
			t.Fatalf("failed transaction remains: %s", entry.Name())
		}
	}
}

func exactIFSConfig(t *testing.T) (Config, func()) {
	t.Helper()
	root := t.TempDir()
	date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	runner := &assemblyFakeRunner{}
	decoder, err := pipeline.NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
	if err != nil {
		t.Fatal(err)
	}
	series := make(pipeline.PreparedSeries)
	for _, field := range []pipeline.Field{pipeline.Temperature, pipeline.DewPoint, pipeline.CloudCover, pipeline.Rain, pipeline.WindU, pipeline.WindV} {
		frame, err := decoder.PrepareFrame(context.Background(), pipeline.DecodeRequest{
			Path: string(field) + ".grib2", SourceID: "ecmwf-ifs-control", Field: field, Run: date,
			NativeResolutionKM: 28, CellDegrees: .25, ScratchDirectory: root,
		})
		if err != nil {
			t.Fatal(err)
		}
		series[field] = []*pipeline.PreparedFrame{frame}
	}
	closeFrames := func() {
		for _, frames := range series {
			for _, frame := range frames {
				_ = frame.Close()
			}
		}
	}
	return Config{
		Root: root, ReleaseID: "ifs-2026072000-20260720", Provider: forecastpoints.ProviderIFS, Tier: forecastpoints.TierGlobal,
		Date: date, Series: series,
		Locate: func(float64, float64) (*time.Location, error) { return time.UTC, nil },
		Land: assemblyLandMask(t, func(bounds delivery.GridBounds) (bool, error) {
			// A stable interior region proves scalar output while the coastal
			// point near 0,0 remains available only through native evidence.
			longitude := (bounds.West + bounds.East) / 2
			latitude := (bounds.South + bounds.North) / 2
			return longitude >= 10 && longitude <= 30 && latitude >= 10 && latitude <= 30, nil
		}),
	}, closeFrames
}

func assemblyLandMask(t *testing.T, displays forecasttiles.DisplayLand) forecasttiles.LandMask {
	t.Helper()
	mask, err := forecasttiles.NewLandMask(displays, forecasttiles.LandIdentity{
		Authority: forecasttiles.LandAuthorityNaturalEarth, Dataset: forecasttiles.LandDatasetNaturalEarth10M, Version: forecasttiles.LandVersionNaturalEarth511,
		SourceArchiveSHA256: strings.Repeat("a", 64), MaskSHA256: strings.Repeat("b", 64), DerivationContract: forecasttiles.LandDerivationWebMercatorCentreV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mask
}

type assemblyFakeRunner struct {
	mu    sync.Mutex
	crops int
}

func (runner *assemblyFakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	switch command {
	case "grib_ls":
		field := strings.TrimSuffix(filepath.Base(args[len(args)-1]), ".grib2")
		identity, ok := map[string][2]string{
			"temperature": {"2t", "K"}, "dew_point": {"2d", "K"}, "cloud_cover": {"tcc", "0-1"},
			"rain": {"tp", "mm"}, "wind_u": {"10u", "m/s"}, "wind_v": {"10v", "m/s"},
		}[field]
		if !ok {
			return nil, fmt.Errorf("unknown fake field %q", field)
		}
		return []byte(fmt.Sprintf(`{"messages":[{"shortName":{"value":%q},"stepType":{"value":"avg"},"startStep":{"value":"10"},"endStep":{"value":"20"},"units":{"value":%q},"packingError":{"value":"0.001"},"binaryScaleFactor":{"value":"-10"},"decimalScaleFactor":{"value":"0"}}]}`, identity[0], identity[1])), nil
	case "grib_to_netcdf":
		return nil, nil
	case "gdal_translate":
		columns, rows, err := outputSize(args)
		if err != nil {
			return nil, err
		}
		runner.mu.Lock()
		value := []float32{298.15, 288.15, .2, 0, 3, 4}[runner.crops%6]
		runner.crops++
		runner.mu.Unlock()
		payload := make([]byte, rows*columns*4)
		bits := math.Float32bits(value)
		for index := 0; index < rows*columns; index++ {
			cellBits := bits
			if index/columns < 64 && index%columns < 64 {
				cellBits = math.Float32bits(float32(math.NaN()))
			}
			binary.LittleEndian.PutUint32(payload[index*4:index*4+4], cellBits)
		}
		output := args[len(args)-1]
		if err := os.WriteFile(output, payload, 0o600); err != nil {
			return nil, err
		}
		header := fmt.Sprintf("BYTEORDER I\nLAYOUT BIL\nNROWS %d\nNCOLS %d\nNBANDS 1\nNBITS 32\nNODATA -9999\n", rows, columns)
		return nil, os.WriteFile(strings.TrimSuffix(output, filepath.Ext(output))+".hdr", []byte(header), 0o600)
	default:
		return nil, fmt.Errorf("unexpected command %q", command)
	}
}

func outputSize(args []string) (columns, rows int, err error) {
	for index := range args {
		if args[index] == "-outsize" && index+2 < len(args) {
			columns, err = strconv.Atoi(args[index+1])
			if err != nil {
				return 0, 0, err
			}
			rows, err = strconv.Atoi(args[index+2])
			return columns, rows, err
		}
	}
	return 0, 0, errors.New("missing output size")
}

func treeStats(root string) (uint32, uint64, error) {
	var files uint32
	var bytes uint64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files++
			bytes += uint64(info.Size())
		}
		return nil
	})
	return files, bytes, err
}
