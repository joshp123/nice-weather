package forecastrange

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	fixtureTileX = uint32(4)
	fixtureTileY = uint32(2)
	fixtureRow   = 100
	fixtureCol   = 101
)

func TestBuildExactWorldRangeProductPreservesStoredFingerprintsAndScalarParity(t *testing.T) {
	runRoot, inputs, displays := rangeFixture(t, -1)
	product, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	if product.Root != filepath.Join(runRoot, productDirectory, Period, forecastpoints.TierGlobal) || len(product.Catalog.PointBlocks) != 32*32 || len(product.Catalog.ScalarCoverage) != 8*8 {
		t.Fatalf("range product shape = root %q blocks %d coverage %d", product.Root, len(product.Catalog.PointBlocks), len(product.Catalog.ScalarCoverage))
	}
	if product.Stats.MaxInputPointBytes != delivery.ForecastPointDayCount*tiledelivery.PointTileBytes || product.Stats.MaxInputPointRecords != delivery.ForecastPointDayCount*tiledelivery.TileSize*tiledelivery.TileSize || product.Stats.MaxRangeBlockBytes > uint64(BlockRows)*uint64(BlockColumns)*delivery.RangePointRecordBytes || product.Stats.CatalogBytes > MaximumCatalogBytes {
		t.Fatalf("range bounds = %+v", product.Stats)
	}
	if product.Stats.OutputFiles != product.Stats.PointFiles+product.Stats.ScalarFiles+1 || product.Stats.OutputBytes != product.Stats.PointBytes+product.Stats.ScalarBytes+product.Stats.CatalogBytes || product.Stats.OutputBytes > 64<<20 {
		t.Fatalf("range storage totals = %+v", product.Stats)
	}
	if product.Stats.EstimatedWorkingBytes == 0 || product.Stats.EstimatedWorkingBytes > 32<<20 {
		t.Fatalf("range bounded working set = %+v", product.Stats)
	}

	globalRow := fixtureTileY*tiledelivery.TileSize + fixtureRow
	globalColumn := fixtureTileX*tiledelivery.TileSize + fixtureCol
	blockRow, blockColumn := globalRow/uint32(BlockRows), globalColumn/uint32(BlockColumns)
	entry := product.Catalog.PointBlocks[blockRow*blockCount+blockColumn]
	identity, _ := NewBlockIdentity("range-run", blockRow, blockColumn)
	block, err := OpenBlock(filepath.Join(product.Root, filepath.FromSlash(entry.Path)), identity, entry.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	record := block.records[(globalRow%uint32(BlockRows))*uint32(BlockColumns)+globalColumn%uint32(BlockColumns)]
	want, err := rangeRecord(displays)
	if err != nil {
		t.Fatal(err)
	}
	if record != want || record.NiceDays != want.NiceDays || record.CyclingDays != want.CyclingDays {
		t.Fatalf("stored range record = %+v; want %+v", record, want)
	}
	if record.NiceDays == record.CyclingDays {
		t.Fatal("Nice and Cycling 14-score fingerprints unexpectedly match")
	}
	for _, field := range Fields() {
		scalar := findScalar(t, product.Catalog, field, MaxZoom, fixtureTileX, fixtureTileY)
		encoded, _, err := tiledelivery.ReadScalar(filepath.Join(product.Root, filepath.FromSlash(scalar.Path)), scalar.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		got := encoded[(fixtureRow+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+fixtureCol+tiledelivery.ScalarGutter]
		wantScore, valid, err := rangeField(record, field)
		if err != nil || !valid || got != wantScore {
			t.Fatalf("scalar %s = %d, %t, %v; want %d", field, got, valid, err, wantScore)
		}
	}

	again, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs})
	if err != nil || !reflect.DeepEqual(again.Catalog, product.Catalog) {
		t.Fatalf("idempotent build = %+v, %v", again, err)
	}
}

func TestBuildTileRequiresEveryStoredDay(t *testing.T) {
	runRoot, inputs, _ := rangeFixture(t, 7)
	loaded, _, _, err := loadInputs(Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := buildTileRecords(context.Background(), loaded, fixtureTileX, fixtureTileY)
	if err != nil {
		t.Fatal(err)
	}
	index := fixtureRow*tiledelivery.TileSize + fixtureCol
	if records[index] != (delivery.RangePointRecord{}) {
		t.Fatalf("record with one missing day = %+v; want no-data", records[index])
	}
}

func TestInputValidationRejectsMixedRunDateOrderAndDishonestProvider(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, []InputCatalog)
		want   string
	}{
		{name: "catalog order", mutate: func(_ *testing.T, _ string, inputs []InputCatalog) { inputs[0], inputs[1] = inputs[1], inputs[0] }, want: "not 14 consecutive"},
		{name: "mixed run", mutate: func(t *testing.T, root string, inputs []InputCatalog) {
			mutateInputCatalog(t, root, &inputs[5], func(c *forecastcatalog.Catalog) { c.Evidence.RunUTC = "2026-07-22T18:00:00Z" })
		}, want: "mix IFS runs"},
		{name: "mixed resolution", mutate: func(t *testing.T, root string, inputs []InputCatalog) {
			mutateInputCatalog(t, root, &inputs[6], func(c *forecastcatalog.Catalog) { c.Evidence.NativeResolutionKM = 7 })
		}, want: "grid, resolution"},
		{name: "ICON provider", mutate: func(t *testing.T, root string, inputs []InputCatalog) {
			mutateInputCatalog(t, root, &inputs[4], func(c *forecastcatalog.Catalog) { c.Provider = forecastpoints.ProviderICONEU })
		}, want: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runRoot, inputs, _ := rangeFixture(t, -1)
			test.mutate(t, runRoot, inputs)
			_, _, _, err := loadInputs(Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadInputs error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestBuildUsesSharedRunLockRejectsSealedRunAndRefusesSymlinkInput(t *testing.T) {
	runRoot, inputs, _ := rangeFixture(t, -1)
	lock, err := acquireRunLock(runRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs}); err == nil || !strings.Contains(err.Error(), "lock forecast run installation") {
		t.Fatalf("shared lock Build error = %v", err)
	}
	releaseRunLock(lock)
	if err := os.WriteFile(filepath.Join(runRoot, sealedIndex), []byte("sealed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs}); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("sealed Build error = %v", err)
	}
	runRoot, inputs, _ = rangeFixture(t, -1)
	inputPath := filepath.Join(runRoot, filepath.FromSlash(inputs[0].Path))
	realPath := filepath.Join(runRoot, "catalog-outside")
	if err := os.Rename(inputPath, realPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, inputPath); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadInputs(Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink input error = %v", err)
	}
	runRoot, inputs, _ = rangeFixture(t, -1)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(runRoot, productDirectory)); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs}); err == nil || !strings.Contains(err.Error(), "install parent") {
		t.Fatalf("symlink output parent error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, Period)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink output parent caused an outside write: %v", err)
	}
}

func TestBuildFailureBeforeCatalogueLeavesNoProductOrPartialTree(t *testing.T) {
	runRoot, inputs, _ := rangeFixture(t, -1)
	original := beforeCatalogueInstall
	beforeCatalogueInstall = func() error { return errors.New("injected catalogue failure") }
	t.Cleanup(func() { beforeCatalogueInstall = original })
	_, err := Build(context.Background(), Config{Root: runRoot, ReleaseID: "range-run", Inputs: inputs})
	if err == nil || !strings.Contains(err.Error(), "injected catalogue failure") {
		t.Fatalf("Build error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(runRoot, productDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed build left product tree: %v", err)
	}
	entries, err := os.ReadDir(runRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".forecast-range-") {
			t.Fatalf("failed build left transaction %q", entry.Name())
		}
	}
}

func TestRangeBlockWrapsXAndNeverWrapsY(t *testing.T) {
	root := t.TempDir()
	identity, _ := NewBlockIdentity("range-run", 16, 0)
	record := constantRangeRecord(t, 70)
	records := make([]delivery.RangePointRecord, int(BlockRows)*int(BlockColumns))
	for index := range records {
		records[index] = record
	}
	asset, err := WriteBlock(root, identity, records)
	if err != nil {
		t.Fatal(err)
	}
	block, err := OpenBlock(asset.Path, identity, asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	west, westOK, err := block.RecordAt(-180, -0.01)
	if err != nil || !westOK || west != record {
		t.Fatalf("west edge = %+v, %t, %v", west, westOK, err)
	}
	east, eastOK, err := block.RecordAt(180, -0.01)
	if err != nil || !eastOK || east != record || east != west {
		t.Fatalf("wrapped east edge = %+v, %t, %v", east, eastOK, err)
	}
	if _, _, err := block.RecordAt(0, 90); err == nil {
		t.Fatal("latitude outside Web Mercator was wrapped or accepted")
	}
}

func rangeFixture(t *testing.T, missingDay int) (string, []InputCatalog, [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord) {
	t.Helper()
	runRoot := filepath.Join(t.TempDir(), "range-run")
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := make([]InputCatalog, delivery.ForecastPointDayCount)
	var selected [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord
	first := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	for day := 0; day < delivery.ForecastPointDayCount; day++ {
		date := first.AddDate(0, 0, day).Format(time.DateOnly)
		display := delivery.DisplayScoreRecord{Thermal: uint8(61 + day%5), Dryness: uint8(70 + day%4), Sun: uint8(55 + day%7), NiceScore: uint8(30 + day*4), NiceWind: uint8(80 - day), CyclingScore: uint8(22 + day*3), CyclingWind: uint8(60 - day), Valid: true}
		selected[day] = display
		include := day != missingDay
		inputs[day] = writeDailyCatalogFixture(t, runRoot, "range-run", date, display, include)
	}
	return runRoot, inputs, selected
}

func writeDailyCatalogFixture(t *testing.T, runRoot, releaseID, date string, selected delivery.DisplayScoreRecord, includeSelected bool) InputCatalog {
	t.Helper()
	dayRoot := filepath.Join(runRoot, "forecast-days", date, forecastpoints.TierGlobal)
	if err := os.MkdirAll(dayRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	points := []forecasttiles.PointTile{}
	{
		stage := t.TempDir()
		writer, err := tiledelivery.NewWriter(stage, releaseID)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := tiledelivery.NewID(forecastpoints.TierGlobal, MaxZoom, int64(fixtureTileX), int64(fixtureTileY))
		records := make([]delivery.DisplayScoreRecord, tiledelivery.TileSize*tiledelivery.TileSize)
		records[0] = delivery.DisplayScoreRecord{Thermal: 60, Dryness: 60, Sun: 60, NiceScore: 60, NiceWind: 60, CyclingScore: 60, CyclingWind: 60, Valid: true}
		if includeSelected {
			records[fixtureRow*tiledelivery.TileSize+fixtureCol] = selected
		}
		asset, err := writer.Write(id, date, records)
		if err != nil {
			t.Fatal(err)
		}
		relative := filepath.ToSlash(filepath.Join("points", date, forecastpoints.TierGlobal, "3", "4", "2.nwt"))
		destination := filepath.Join(dayRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(asset.Path, destination); err != nil {
			t.Fatal(err)
		}
		points = append(points, forecasttiles.PointTile{Zoom: MaxZoom, X: fixtureTileX, Y: fixtureTileY, Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, PayloadSHA256: asset.PayloadSHA256})
	}

	grid := delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 720, Cols: 1440}
	runUTC := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	evidence := fixtureDigest("evidence")
	blockRows, blockColumns, _ := forecastpoints.BlockGrid(forecastpoints.ProviderIFS, forecastpoints.TierGlobal, grid)
	blocks := make([]forecastpoints.Asset, 0, blockRows*blockColumns)
	for row := uint32(0); row < blockRows; row++ {
		for column := uint32(0); column < blockColumns; column++ {
			identity, _ := forecastpoints.NewIdentity(releaseID, forecastpoints.ProviderIFS, forecastpoints.TierGlobal, date, runUTC.Format(time.RFC3339), evidence, grid, forecastpoints.Coordinate{Row: row, Column: column})
			recordCount := uint32(identity.Rows) * uint32(identity.Columns)
			asset := forecastpoints.Asset{Identity: identity, MediaType: forecastpoints.MediaType, Omitted: true, NoDataRecords: recordCount}
			if row == 0 && column == 0 {
				path, _ := forecastpoints.RelativePath(identity)
				whole, _ := forecastpoints.WholeRange(identity)
				asset = forecastpoints.Asset{Identity: identity, Path: filepath.Join(dayRoot, filepath.FromSlash(path)), Bytes: uint64(whole.Length), SHA256: fixtureDigest("native-whole"), PayloadSHA256: fixtureDigest("native-payload"), MediaType: forecastpoints.MediaType, DataRecords: 1, NoDataRecords: recordCount - 1}
			}
			blocks = append(blocks, asset)
		}
	}
	outcomes := make([]forecasttiles.MaxZoomOutcome, 0, 64)
	for y := uint32(0); y < 8; y++ {
		for x := uint32(0); x < 8; x++ {
			outcome := forecasttiles.MaxZoomOutcome{Zoom: MaxZoom, X: x, Y: y, Omitted: true, NoDataRecords: tiledelivery.TileSize * tiledelivery.TileSize}
			if x == fixtureTileX && y == fixtureTileY {
				dataRecords := uint32(1)
				if includeSelected {
					dataRecords++
				}
				outcome.Omitted, outcome.DataRecords, outcome.NoDataRecords = false, dataRecords, tiledelivery.TileSize*tiledelivery.TileSize-dataRecords
			}
			outcomes = append(outcomes, outcome)
		}
	}
	scalars := []forecasttiles.ScalarTile{}
	{
		for _, field := range Fields() {
			x, y := fixtureTileX, fixtureTileY
			for zoom := MaxZoom; ; zoom-- {
				path, _ := fixtureDailyScalarPath(date, field, zoom, x, y)
				scalars = append(scalars, forecasttiles.ScalarTile{Field: field, Zoom: zoom, X: x, Y: y, Path: path, Bytes: tiledelivery.ScalarTileBytes, SHA256: fixtureDigest(string(field)), DataTexels: 1, NoDataTexels: tiledelivery.ScalarTileBytes - 1})
				if zoom == 0 {
					break
				}
				x, y = x/2, y/2
			}
		}
	}
	scalarCatalog := forecasttiles.Catalog{Version: forecastcatalog.CatalogVersion, ReleaseID: releaseID, Period: date, Tier: forecastpoints.TierGlobal, Date: date, MaxZoom: MaxZoom, Source: forecasttiles.SourceIdentity{SourceID: "ecmwf-ifs-control", RunUTC: runUTC.Format(time.RFC3339), Grid: grid, NativeResolutionKM: 28, EvidenceSHA256: evidence}, MaxZoomOutcomes: outcomes, Points: points, Scalars: scalars}
	catalog, err := forecastcatalog.New(forecastcatalog.Config{ReleaseID: releaseID, LocalDate: date, Provider: forecastpoints.ProviderIFS, Tier: forecastpoints.TierGlobal, RunUTC: runUTC, SourceEvidenceSHA256: evidence}, blocks, scalarCatalog)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := forecastcatalog.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dayRoot, forecastcatalogFilename)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return InputCatalog{Date: date, Path: filepath.ToSlash(filepath.Join("forecast-days", date, forecastpoints.TierGlobal, forecastcatalogFilename)), Bytes: uint64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
}

func fixtureDailyScalarPath(date string, field forecasttiles.Field, zoom uint8, x, y uint32) (string, error) {
	suffix := map[forecasttiles.Field]string{forecasttiles.FieldThermal: ".th", forecasttiles.FieldDryness: ".dr", forecasttiles.FieldSun: ".su", forecasttiles.FieldNiceScore: ".ns", forecasttiles.FieldNiceWind: ".nw", forecasttiles.FieldCyclingScore: ".cs", forecasttiles.FieldCyclingWind: ".cw"}[field]
	return filepath.ToSlash(filepath.Join("scores", date, forecastpoints.TierGlobal+suffix, string(rune('0'+zoom)), string(rune('0'+x)), string(rune('0'+y))+".nws")), nil
}

func mutateInputCatalog(t *testing.T, root string, input *InputCatalog, mutate func(*forecastcatalog.Catalog)) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(input.Path))
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog forecastcatalog.Catalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		t.Fatal(err)
	}
	mutate(&catalog)
	payload, err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	input.Bytes, input.SHA256 = uint64(len(payload)), hex.EncodeToString(digest[:])
}

func constantRangeRecord(t *testing.T, score uint8) delivery.RangePointRecord {
	t.Helper()
	display := delivery.DisplayScoreRecord{Thermal: score, Dryness: score, Sun: score, NiceScore: score, NiceWind: score, CyclingScore: score, CyclingWind: score, Valid: true}
	var days [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord
	for index := range days {
		days[index] = display
	}
	record, err := rangeRecord(days)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func findScalar(t *testing.T, catalog Catalog, field forecasttiles.Field, zoom uint8, x, y uint32) ScalarTile {
	t.Helper()
	for _, scalar := range catalog.ScalarTiles {
		if scalar.Field == field && scalar.Zoom == zoom && scalar.X == x && scalar.Y == y {
			return scalar
		}
	}
	t.Fatalf("missing scalar %s %d/%d/%d", field, zoom, x, y)
	return ScalarTile{}
}

func fixtureDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestStoredRangeRecordUsesSummarizeRangeWithoutRescoring(t *testing.T) {
	var displays [delivery.ForecastPointDayCount]delivery.DisplayScoreRecord
	for index := range displays {
		displays[index] = delivery.DisplayScoreRecord{Thermal: uint8(50 + index), Dryness: 80, Sun: 70, NiceScore: uint8(20 + index*5), NiceWind: 90, CyclingScore: uint8(10 + index*4), CyclingWind: 40, Valid: true}
	}
	record, err := rangeRecord(displays)
	if err != nil {
		t.Fatal(err)
	}
	nice := make([]weather.Result, len(displays))
	for index, display := range displays {
		nice[index] = storedResult(display.NiceScore, display.Thermal, display.Dryness, display.Sun, display.NiceWind)
	}
	summary, err := weather.SummarizeRange(nice)
	if err != nil {
		t.Fatal(err)
	}
	want, err := delivery.RangeRecordFromSummary(summary)
	if err != nil {
		t.Fatal(err)
	}
	if record.Nice != want {
		t.Fatalf("Nice range = %+v; want %+v", record.Nice, want)
	}
}

func TestWorstCaseWorldCatalogueFitsStaticDeliveryLimit(t *testing.T) {
	first := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	catalog := Catalog{
		Version: CatalogVersion, ProductContract: ProductContract, ReleaseID: strings.Repeat("r", 64), Period: Period,
		Source:           Source{Provider: forecastpoints.ProviderIFS, Tier: forecastpoints.TierGlobal, Product: forecastcatalog.ProductIFSControl, RunUTC: "2026-07-22T12:00:00Z", NativeResolutionKM: 28, StaleAfterHours: StaleAfterHours},
		Grid:             Grid{Projection: "EPSG:3857-XYZ", Zoom: MaxZoom, TileSize: tiledelivery.TileSize, Rows: worldPixels, Columns: worldPixels},
		Scores:           Scores{Unit: forecastcatalog.ScoreUnit, NoDataValue: forecastcatalog.ScoreNoData, MediaType: tiledelivery.ScalarMediaType, Profiles: []weather.Profile{weather.ProfileNice, weather.ProfileCycling}, Fields: append([]FieldContract(nil), fieldContracts...)},
		PointBlockLayout: PointBlockLayout{Rows: BlockRows, Columns: BlockColumns, HeaderBytes: BlockHeaderBytes, RecordBytes: delivery.RangePointRecordBytes, MediaType: BlockMediaType},
	}
	for day := 0; day < delivery.ForecastPointDayCount; day++ {
		date := first.AddDate(0, 0, day).Format(time.DateOnly)
		catalog.Dates = append(catalog.Dates, date)
		catalog.Inputs = append(catalog.Inputs, InputCatalog{Date: date, Path: filepath.ToSlash(filepath.Join("forecast-days", date, forecastpoints.TierGlobal, forecastcatalogFilename)), Bytes: forecastcatalog.MaximumCatalogBytes, SHA256: fixtureDigest("input")})
	}
	for row := uint32(0); row < blockCount; row++ {
		for column := uint32(0); column < blockCount; column++ {
			catalog.PointBlocks = append(catalog.PointBlocks, PointBlock{Row: row, Column: column, Path: BlockPath(row, column), Bytes: BlockBytes, SHA256: fixtureDigest("block"), PayloadSHA256: fixtureDigest("payload"), DataRecords: uint32(BlockRows) * uint32(BlockColumns)})
		}
	}
	for y := uint32(0); y < 1<<MaxZoom; y++ {
		for x := uint32(0); x < 1<<MaxZoom; x++ {
			catalog.ScalarCoverage = append(catalog.ScalarCoverage, MaxZoomOutcome{Zoom: MaxZoom, X: x, Y: y, DataRecords: tiledelivery.TileSize * tiledelivery.TileSize})
		}
	}
	for _, field := range Fields() {
		for zoom := uint8(0); zoom <= MaxZoom; zoom++ {
			for y := uint32(0); y < 1<<zoom; y++ {
				for x := uint32(0); x < 1<<zoom; x++ {
					path, _ := scalarPath(field, zoom, x, y)
					catalog.ScalarTiles = append(catalog.ScalarTiles, ScalarTile{Field: field, Zoom: zoom, X: x, Y: y, Path: path, Bytes: tiledelivery.ScalarTileBytes, SHA256: fixtureDigest("scalar"), DataTexels: tiledelivery.ScalarTileBytes})
				}
			}
		}
	}
	sort.Slice(catalog.ScalarTiles, func(i, j int) bool { return scalarLess(catalog.ScalarTiles[i], catalog.ScalarTiles[j]) })
	catalog.Coverage = calculateCoverage(catalog)
	encoded, err := Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaximumCatalogBytes {
		t.Fatalf("worst-case catalogue = %d bytes; maximum %d", len(encoded), MaximumCatalogBytes)
	}
	t.Logf("worst-case world catalogue: %d bytes", len(encoded))
}
