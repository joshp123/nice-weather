package forecasttiles

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
)

func TestBuildWorldwideDayHasDatelineWaterAndExactPointScalarParity(t *testing.T) {
	config, closeFrames := worldwideConfig(t, 3, func(lon, lat float64) (bool, error) {
		return math.Abs(lon) > 170 && math.Abs(lat) < 12, nil
	}, false)
	defer closeFrames()
	catalog, err := BuildDay(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Points) == 0 || len(catalog.Scalars) == 0 {
		t.Fatalf("empty catalog: %+v", catalog)
	}

	for _, longitude := range []float64{-179.9, 179.9} {
		id, row, column, err := tiledelivery.At(config.Tier, config.MaxZoom, longitude, 0)
		if err != nil {
			t.Fatal(err)
		}
		point := findPoint(t, catalog, id)
		reader, err := tiledelivery.OpenPoint(filepath.Join(builtDir(config), filepath.FromSlash(point.Path)))
		if err != nil {
			t.Fatal(err)
		}
		record, valid, err := reader.At(longitude, 0)
		_ = reader.Close()
		if err != nil || !valid {
			t.Fatalf("dateline point %.1f unavailable: valid=%v err=%v", longitude, valid, err)
		}
		for _, field := range fields {
			scalar := findScalar(t, catalog, field, id.Zoom, id.X, id.Y)
			encoded, _, err := tiledelivery.ReadScalar(filepath.Join(builtDir(config), filepath.FromSlash(scalar.Path)), scalar.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			got := encoded[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
			want, _, err := fieldValue(record, field)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s at %.1f scalar=%d point=%d record=%+v", field, longitude, got, want, record)
			}
		}
	}

	waterID, _, _, _ := tiledelivery.At(config.Tier, config.MaxZoom, 0, 50)
	for _, point := range catalog.Points {
		if point.Zoom == waterID.Zoom && point.X == waterID.X && point.Y == waterID.Y {
			t.Fatalf("all-water tile was published: %+v", point)
		}
	}
	for _, scalar := range catalog.Scalars {
		if scalar.Zoom == config.MaxZoom && scalar.X == waterID.X && scalar.Y == waterID.Y {
			t.Fatalf("all-water max-zoom scalar was published: %+v", scalar)
		}
	}
	if _, err := os.Stat(filepath.Join(builtDir(config), "forecast-tiles.json")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDayNoDataOmitsLandAndCancellationCleansStaging(t *testing.T) {
	config, closeFrames := worldwideConfig(t, 1, func(float64, float64) (bool, error) { return true, nil }, true)
	defer closeFrames()
	catalog, err := BuildDay(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Points) != 0 || len(catalog.Scalars) != 0 {
		t.Fatalf("no-data build published assets: %+v", catalog)
	}

	cancelConfig, closeCancelFrames := worldwideConfig(t, 3, nil, false)
	defer closeCancelFrames()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cancelConfig.Land = func(float64, float64) (bool, error) {
		calls++
		if calls == 100 {
			cancel()
		}
		return true, nil
	}
	if _, err := BuildDay(ctx, cancelConfig); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build error=%v", err)
	}
	if _, err := os.Stat(builtDir(cancelConfig)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled release exists: %v", err)
	}
	entries, err := os.ReadDir(cancelConfig.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".forecasttiles-") {
			t.Fatalf("staging remained: %s", entry.Name())
		}
	}
}

func TestBuildDayCatalogIsDeterministicAndBulkWorkingSetIsTileBounded(t *testing.T) {
	var previous []byte
	var z2Stats BuildStats
	for run := 0; run < 2; run++ {
		config, closeFrames := worldwideConfig(t, 2, func(lon, lat float64) (bool, error) { return math.Abs(lon-10) < 3 && math.Abs(lat-5) < 3, nil }, false)
		catalog, err := BuildDay(context.Background(), config)
		closeFrames()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := os.ReadFile(filepath.Join(builtDir(config), "forecast-tiles.json"))
		if err != nil {
			t.Fatal(err)
		}
		if run > 0 && string(encoded) != string(previous) {
			t.Fatal("identical inputs produced different catalogs")
		}
		previous, z2Stats = encoded, catalog.Stats
	}
	config, closeFrames := worldwideConfig(t, 3, func(lon, lat float64) (bool, error) { return math.Abs(lon-10) < 3 && math.Abs(lat-5) < 3, nil }, false)
	catalog, err := BuildDay(context.Background(), config)
	closeFrames()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Stats.MaxDisplayRecords != z2Stats.MaxDisplayRecords || catalog.Stats.MaxMetricCells > z2Stats.MaxMetricCells || catalog.Stats.MaxScalarBytes > 20*tiledelivery.ScalarTileBytes {
		t.Fatalf("bulk working set grew with world tile count: z2=%+v z3=%+v", z2Stats, catalog.Stats)
	}
}

func TestBuildDayComposesTwoDaysAndTiersWithoutOwningReleasePublication(t *testing.T) {
	first, closeFirst := worldwideConfig(t, 0, func(float64, float64) (bool, error) { return true, nil }, false)
	defer closeFirst()
	if _, err := BuildDay(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second, closeSecond := worldwideConfigAt(t, testDate().Add(24*time.Hour), 0, func(float64, float64) (bool, error) { return true, nil }, false)
	defer closeSecond()
	second.Root = first.Root
	second.Period = "2026-07-21"
	second.Tier = "icon"
	if _, err := BuildDay(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{first, second} {
		if _, err := os.Stat(filepath.Join(builtDir(config), "forecast-tiles.json")); err != nil {
			t.Fatalf("composed %s/%s missing: %v", config.Period, config.Tier, err)
		}
	}
	if _, err := os.Stat(filepath.Join(first.Root, "releases", first.ReleaseID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("day builder created a competing release publication: %v", err)
	}
}

func TestBuildRegionalTierPublishesOnlyItsSupportedGeography(t *testing.T) {
	config, closeFrames := preparedConfigAt(t, testDate(), delivery.GridBounds{West: -20, East: 30, South: 30, North: 70}, 3, func(float64, float64) (bool, error) { return true, nil }, false)
	defer closeFrames()
	config.Tier = "icon"
	catalog, err := BuildDay(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	inside, _, _, _ := tiledelivery.At(config.Tier, config.MaxZoom, 5, 50)
	_ = findPoint(t, catalog, inside)
	outside, _, _, _ := tiledelivery.At(config.Tier, config.MaxZoom, -100, 50)
	for _, point := range catalog.Points {
		if point.Zoom == outside.Zoom && point.X == outside.X && point.Y == outside.Y {
			t.Fatalf("regional tier wrapped or escaped its source support: %+v", point)
		}
	}
}

func TestBuildDayRejectsMixedTierResolutionBeforeWriting(t *testing.T) {
	config, closeFrames := worldwideConfig(t, 0, func(float64, float64) (bool, error) { return true, nil }, false)
	defer closeFrames()
	dew := config.Series[pipeline.DewPoint][0]
	metadata, err := dew.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	_ = metadata
	// Prepared frames are opaque by design. Reprepare this one field with a
	// different declared native resolution to prove the one-tier validation.
	_ = dew.Close()
	runner := &tileFakeRunner{allNoData: false}
	decoder, _ := pipeline.NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
	replacement, err := decoder.PrepareFrame(context.Background(), pipeline.DecodeRequest{Path: "dew_point.grib2", SourceID: "test-source", Field: pipeline.DewPoint, Run: testDate(), NativeResolutionKM: 7, CellDegrees: 10, ScratchDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	config.Series[pipeline.DewPoint] = []*pipeline.PreparedFrame{replacement}
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "one source grid and resolution") {
		t.Fatalf("mixed provider-like tier accepted: %v", err)
	}
}

func TestBuildDayRejectsFieldLevelProviderAndRunMixing(t *testing.T) {
	for _, test := range []struct {
		name, source string
		run          time.Time
	}{
		{name: "provider", source: "other-provider", run: testDate()},
		{name: "run", source: "test-source", run: testDate().Add(-12 * time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, closeFrames := worldwideConfig(t, 0, func(float64, float64) (bool, error) { return true, nil }, false)
			defer closeFrames()
			original := config.Series[pipeline.DewPoint][0]
			_ = original.Close()
			runner := &tileFakeRunner{allNoData: false, crops: 1}
			decoder, _ := pipeline.NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
			replacement, err := decoder.PrepareFrame(context.Background(), pipeline.DecodeRequest{Path: "dew_point.grib2", SourceID: test.source, Field: pipeline.DewPoint, Run: test.run, NativeResolutionKM: 28, CellDegrees: 10, ScratchDirectory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			config.Series[pipeline.DewPoint] = []*pipeline.PreparedFrame{replacement}
			if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "cannot mix provider products or runs") {
				t.Fatalf("field-level %s mix accepted: %v", test.name, err)
			}
		})
	}
}

func TestBuildDayRejectsAmbiguousOrMismatchedDayIdentity(t *testing.T) {
	config, closeFrames := worldwideConfig(t, 0, func(float64, float64) (bool, error) { return true, nil }, false)
	defer closeFrames()
	config.Period = "2026-07-21"
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "same UTC calendar day") {
		t.Fatalf("mismatched period/date accepted: %v", err)
	}
	config.Period = "2026-07-20"
	config.Date = time.Date(2026, 7, 20, 0, 0, 0, 0, time.FixedZone("local", 2*60*60))
	if _, err := BuildDay(context.Background(), config); err == nil || !strings.Contains(err.Error(), "same UTC calendar day") {
		t.Fatalf("non-UTC date accepted: %v", err)
	}
}

func BenchmarkBuildWorldwideDayZ3(b *testing.B) {
	for range b.N {
		config, closeFrames := worldwideConfig(b, 3, func(lon, lat float64) (bool, error) { return math.Abs(lon-10) < 2 && math.Abs(lat-5) < 2, nil }, false)
		if _, err := BuildDay(context.Background(), config); err != nil {
			b.Fatal(err)
		}
		closeFrames()
	}
}

func worldwideConfig(tb testing.TB, zoom uint8, land IsLand, allNoData bool) (Config, func()) {
	return worldwideConfigAt(tb, testDate(), zoom, land, allNoData)
}

func worldwideConfigAt(tb testing.TB, date time.Time, zoom uint8, land IsLand, allNoData bool) (Config, func()) {
	return preparedConfigAt(tb, date, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, zoom, land, allNoData)
}

func preparedConfigAt(tb testing.TB, date time.Time, bounds delivery.GridBounds, zoom uint8, land IsLand, allNoData bool) (Config, func()) {
	tb.Helper()
	root := tb.TempDir()
	runner := &tileFakeRunner{allNoData: allNoData}
	decoder, err := pipeline.NewDecoder(runner, bounds)
	if err != nil {
		tb.Fatal(err)
	}
	series := make(pipeline.PreparedSeries)
	for _, field := range []pipeline.Field{pipeline.Temperature, pipeline.DewPoint, pipeline.CloudCover, pipeline.Rain, pipeline.WindU, pipeline.WindV} {
		frame, err := decoder.PrepareFrame(context.Background(), pipeline.DecodeRequest{Path: string(field) + ".grib2", SourceID: "test-source", Field: field, Run: date, NativeResolutionKM: 28, CellDegrees: 10, ScratchDirectory: root})
		if err != nil {
			tb.Fatal(err)
		}
		series[field] = []*pipeline.PreparedFrame{frame}
	}
	if land == nil {
		land = func(float64, float64) (bool, error) { return true, nil }
	}
	return Config{Root: root, ReleaseID: "test-release", Period: date.Format("2006-01-02"), Tier: "ifs", Date: date, MaxZoom: zoom, Series: series, Locate: func(float64, float64) (*time.Location, error) { return time.UTC, nil }, Land: land}, func() {
		for _, frames := range series {
			for _, frame := range frames {
				_ = frame.Close()
			}
		}
	}
}

func testDate() time.Time { return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC) }
func builtDir(config Config) string {
	return filepath.Join(config.Root, "forecast-days", config.Period, config.Tier)
}

func findPoint(t *testing.T, catalog Catalog, id tiledelivery.ID) PointTile {
	t.Helper()
	for _, point := range catalog.Points {
		if point.Zoom == id.Zoom && point.X == id.X && point.Y == id.Y {
			return point
		}
	}
	t.Fatalf("point tile not found: %+v", id)
	return PointTile{}
}
func findScalar(t *testing.T, catalog Catalog, field Field, zoom uint8, x, y uint32) ScalarTile {
	t.Helper()
	for _, scalar := range catalog.Scalars {
		if scalar.Field == field && scalar.Zoom == zoom && scalar.X == x && scalar.Y == y {
			return scalar
		}
	}
	t.Fatalf("scalar tile not found: %s %d/%d/%d", field, zoom, x, y)
	return ScalarTile{}
}

type tileFakeRunner struct {
	mu        sync.Mutex
	allNoData bool
	crops     int
}

func (runner *tileFakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
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
		cols, rows, err := fakeOutputSize(args)
		if err != nil {
			return nil, err
		}
		output := args[len(args)-1]
		// The temporary path does not retain the source field. Use the stable
		// command order: each runner handles preparation serially and records the
		// field value from grib_ls.
		runner.mu.Lock()
		value := runner.nextValue()
		runner.mu.Unlock()
		payload := make([]byte, rows*cols*4)
		for index := 0; index < rows*cols; index++ {
			cell := value
			if runner.allNoData {
				cell = float32(math.NaN())
			}
			binary.LittleEndian.PutUint32(payload[index*4:index*4+4], math.Float32bits(cell))
		}
		if err := os.WriteFile(output, payload, 0o600); err != nil {
			return nil, err
		}
		header := fmt.Sprintf("BYTEORDER I\nLAYOUT BIL\nNROWS %d\nNCOLS %d\nNBANDS 1\nNBITS 32\nNODATA -9999\n", rows, cols)
		return nil, os.WriteFile(strings.TrimSuffix(output, filepath.Ext(output))+".hdr", []byte(header), 0o600)
	default:
		return nil, fmt.Errorf("unexpected command %q", command)
	}
}

func (runner *tileFakeRunner) nextValue() float32 {
	// PrepareFrame calls this fake in the same field order as worldwideConfig.
	index := runner.crops
	runner.crops++
	return []float32{298.15, 288.15, .2, 0, 3, 4}[index%6]
}

func fakeOutputSize(args []string) (int, int, error) {
	for index := range args {
		if args[index] == "-outsize" && index+2 < len(args) {
			cols, err := strconv.Atoi(args[index+1])
			if err != nil {
				return 0, 0, err
			}
			rows, err := strconv.Atoi(args[index+2])
			return cols, rows, err
		}
	}
	return 0, 0, errors.New("missing output size")
}
