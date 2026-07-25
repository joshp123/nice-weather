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
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

func TestScoreRecordUsesExactQuantizedDailyPointEvidence(t *testing.T) {
	metrics := weather.DailyMetrics{
		ComfortableTempC: 29.96, MinTempC: 27.04, MaxTempC: 31.04, RainMM: .04,
		WetTimeFraction:   weather.OptionalFloat{Value: .0004, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: 18.49, Available: true},
		WindSpeedKPH:      9.96, DewPointC: weather.OptionalFloat{Value: 23.96, Available: true},
		RelativeHumidityPercent: weather.OptionalFloat{Value: 70.49, Available: true},
		NativeResolutionKM:      28, Valid: true,
	}
	weatherRecord, err := delivery.RecordFromWeather(metrics)
	if err != nil {
		t.Fatal(err)
	}
	point, err := delivery.DailyPointRecordFromWeather(weatherRecord)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scoreRecord(metrics)
	if err != nil {
		t.Fatal(err)
	}
	want := point
	if got != want {
		t.Fatalf("scalar source = %+v; quantized daily point = %+v", got, want)
	}
}

func TestInterpolateRecordScoresOneInterpolatedPhysicalDay(t *testing.T) {
	day := func(temperature float64) weather.DailyMetrics {
		return weather.DailyMetrics{
			ComfortableTempC: temperature, MinTempC: temperature - 2, MaxTempC: temperature + 2,
			RainMM: 0, WindSpeedKPH: 8, CloudCoverPercent: weather.OptionalFloat{Value: 10, Available: true},
			DewPointC: weather.OptionalFloat{Value: 12, Available: true}, NativeResolutionKM: 28, Valid: true,
		}
	}
	window := pipeline.DailyMetricsWindow{
		Spec:    delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2},
		Metrics: []weather.DailyMetrics{day(12), day(36), day(36), day(12)},
	}
	got, ok, err := interpolateRecord(1, 1, window)
	if err != nil || !ok {
		t.Fatalf("interpolated record unavailable: %t, %v", ok, err)
	}
	want, err := scoreRecord(day(24))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("physical-first record = %+v; want canonical 24C record %+v", got, want)
	}
	cold, _ := scoreRecord(day(12))
	hot, _ := scoreRecord(day(36))
	oldInterpolatedScore := uint8(math.Round((float64(cold.NiceScore) + float64(hot.NiceScore)) / 2))
	if got.NiceScore == oldInterpolatedScore {
		t.Fatalf("fixture is not nonlinear: canonical=%d old score interpolation=%d", got.NiceScore, oldInterpolatedScore)
	}
}

func TestInterpolateRecordUsesIndependentMaskedOptionalFields(t *testing.T) {
	day := func() weather.DailyMetrics {
		return weather.DailyMetrics{
			ComfortableTempC: 24, MinTempC: 20, MaxTempC: 28, RainMM: 0, WindSpeedKPH: 8,
			CloudCoverPercent: weather.OptionalFloat{Value: 20, Available: true},
			DewPointC:         weather.OptionalFloat{Value: 12, Available: true}, NativeResolutionKM: 7, Valid: true,
		}
	}
	window := func(metrics []weather.DailyMetrics) pipeline.DailyMetricsWindow {
		return pipeline.DailyMetricsWindow{Spec: delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2}, Metrics: metrics}
	}

	metrics := []weather.DailyMetrics{day(), day(), day(), day()}
	metrics[0].SolarEnergyMJm2 = weather.OptionalFloat{Value: 10, Available: true}
	metrics[2].SolarEnergyMJm2 = weather.OptionalFloat{Value: 30, Available: true}
	metrics[3].SolarEnergyMJm2 = weather.OptionalFloat{Value: 50, Available: true}
	metrics[0].GustKPH = weather.OptionalFloat{Value: 12, Available: true}
	metrics[1].GustKPH = weather.OptionalFloat{Value: 16, Available: true}
	record, ok, err := interpolateRecord(1, 1, window(metrics))
	if err != nil || !ok {
		t.Fatalf("masked optional interpolation unavailable: %t, %v", ok, err)
	}
	if !record.Weather.HasSolar || record.Weather.SolarCentiMJm2 != 3000 || !record.Weather.HasGust || record.Weather.GustDeciKMH != 140 {
		t.Fatalf("independent optional fields = %+v; want solar 30 and gust 14", record.Weather)
	}

	// Missing contributors with zero bilinear weight cannot suppress exact
	// evidence at a source-cell centre.
	exact, ok, err := interpolateRecord(.5, 1.5, window(metrics))
	if err != nil || !ok || !exact.Weather.HasSolar || exact.Weather.SolarCentiMJm2 != 1000 {
		t.Fatalf("zero-weight missing contributor changed exact solar: %+v, %t, %v", exact.Weather, ok, err)
	}

	for index := range metrics {
		metrics[index].SolarEnergyMJm2 = weather.OptionalFloat{}
	}
	fallback, ok, err := interpolateRecord(1, 1, window(metrics))
	if err != nil || !ok || fallback.Weather.HasSolar || !fallback.Weather.HasCloud {
		t.Fatalf("all-missing solar did not retain cloud fallback: %+v, %t, %v", fallback.Weather, ok, err)
	}

	metrics[1].Valid = false
	if _, ok, err := interpolateRecord(1, 1, window(metrics)); err != nil || ok {
		t.Fatalf("required-neighbour gap did not fail closed: %t, %v", ok, err)
	}
}

func TestInterpolateRecordKeepsBoundedEvidenceInsideItsPhysicalRange(t *testing.T) {
	for _, endpoint := range []float64{0, 1} {
		endpoint := endpoint
		t.Run(fmt.Sprintf("endpoint_%g", endpoint), func(t *testing.T) {
			day := weather.DailyMetrics{
				ComfortableTempC: 24, MinTempC: 20, MaxTempC: 28, RainMM: 0, WindSpeedKPH: 8,
				WetTimeFraction:         weather.OptionalFloat{Value: endpoint, Available: true},
				CloudCoverPercent:       weather.OptionalFloat{Value: endpoint * 100, Available: true},
				DewPointC:               weather.OptionalFloat{Value: 24, Available: true},
				RelativeHumidityPercent: weather.OptionalFloat{Value: endpoint * 100, Available: true},
				NativeResolutionKM:      28, Valid: true,
			}
			window := pipeline.DailyMetricsWindow{
				Spec: delivery.GridSpec{
					Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2,
				},
				Metrics: []weather.DailyMetrics{day, day, day, day},
			}
			for _, fraction := range []float64{.00035, .5, .99965} {
				record, ok, err := interpolateRecord(.5+fraction, 1.5-fraction, window)
				if err != nil || !ok {
					t.Fatalf("bounded interpolation failed at %.8f: available=%t error=%v", fraction, ok, err)
				}
				wantWet := uint16(endpoint * 1000)
				wantPercent := uint8(endpoint * 100)
				if record.Weather.WetTimePermille != wantWet || record.Weather.CloudPercent != wantPercent || record.Weather.HumidityPercent != wantPercent {
					t.Fatalf("bounded fields at %.8f = wet %d cloud %d humidity %d; want %d, %d, %d", fraction,
						record.Weather.WetTimePermille, record.Weather.CloudPercent, record.Weather.HumidityPercent,
						wantWet, wantPercent, wantPercent)
				}
			}
		})
	}
}

func TestLandMaskKeepsPredicateAndExactIdentityTogether(t *testing.T) {
	identity := LandIdentity{
		Authority: LandAuthorityNaturalEarth, Dataset: LandDatasetNaturalEarth10M, Version: LandVersionNaturalEarth511,
		SourceArchiveSHA256: strings.Repeat("a", 64), MaskSHA256: strings.Repeat("b", 64), DerivationContract: LandDerivationWebMercatorCentreV1,
	}
	mask, err := NewLandMask(func(bounds delivery.GridBounds) (bool, error) { return bounds.West == 1, nil }, identity)
	if err != nil || mask.Identity() != identity {
		t.Fatalf("land mask identity = %+v, %v", mask.Identity(), err)
	}
	if got, err := mask.Displays(delivery.GridBounds{West: 1, East: 2, South: 3, North: 4}); err != nil || !got {
		t.Fatalf("land predicate = %v, %v", got, err)
	}
	bad := []LandIdentity{
		{},
		func() LandIdentity { value := identity; value.Authority = "other"; return value }(),
		func() LandIdentity { value := identity; value.Dataset = "ne_50m_land"; return value }(),
		func() LandIdentity { value := identity; value.Version = "5.1.0"; return value }(),
		func() LandIdentity {
			value := identity
			value.SourceArchiveSHA256 = strings.Repeat("A", 64)
			return value
		}(),
		func() LandIdentity { value := identity; value.MaskSHA256 = "short"; return value }(),
		func() LandIdentity { value := identity; value.DerivationContract = "centre-land-v1"; return value }(),
	}
	for index, candidate := range bad {
		if _, err := NewLandMask(func(delivery.GridBounds) (bool, error) { return true, nil }, candidate); err == nil {
			t.Fatalf("invalid land identity %d accepted: %+v", index, candidate)
		}
	}
	if _, err := NewLandMask(nil, identity); err == nil {
		t.Fatal("nil land predicate accepted")
	}
}

func TestSourceEvidenceBindsPreparedFloat32Contents(t *testing.T) {
	first, closeFirst := preparedConfigWithOffset(t, 0)
	defer closeFirst()
	second, closeSecond := preparedConfigWithOffset(t, .5)
	defer closeSecond()
	firstSource, err := sourceSpec(first.Series)
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := sourceSpec(second.Series)
	if err != nil {
		t.Fatal(err)
	}
	if firstSource.EvidenceSHA256 == secondSource.EvidenceSHA256 {
		t.Fatal("different accepted Float32 contents produced the same source evidence")
	}
}

func preparedConfigWithOffset(t *testing.T, offset float32) (Config, func()) {
	t.Helper()
	root := t.TempDir()
	runner := &tileFakeRunner{valueOffset: offset}
	decoder, err := pipeline.NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
	if err != nil {
		t.Fatal(err)
	}
	series := make(pipeline.PreparedSeries)
	for _, field := range []pipeline.Field{pipeline.Temperature, pipeline.DewPoint, pipeline.CloudCover, pipeline.Rain, pipeline.WindU, pipeline.WindV} {
		frame, err := decoder.PrepareFrame(context.Background(), pipeline.DecodeRequest{Path: string(field) + ".grib2", SourceID: "test-source", Field: field, Run: testDate(), NativeResolutionKM: 28, CellDegrees: 10, ScratchDirectory: root})
		if err != nil {
			t.Fatal(err)
		}
		series[field] = []*pipeline.PreparedFrame{frame}
	}
	return Config{Series: series}, func() {
		for _, frames := range series {
			for _, frame := range frames {
				_ = frame.Close()
			}
		}
	}
}

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
	if catalog.Source.SourceID != "test-source" || catalog.Source.RunUTC != testDate().Format(time.RFC3339) || catalog.Source.NativeResolutionKM != 28 || catalog.Source.Grid.Rows != 18 || catalog.Source.Grid.Cols != 36 || len(catalog.Source.EvidenceSHA256) != 64 {
		t.Fatalf("source identity = %+v", catalog.Source)
	}
	if len(catalog.MaxZoomOutcomes) != 64 {
		t.Fatalf("max-zoom outcomes = %d; want 64", len(catalog.MaxZoomOutcomes))
	}
	produced := make(map[[2]uint32]struct{})
	for _, outcome := range catalog.MaxZoomOutcomes {
		if outcome.Zoom != config.MaxZoom || outcome.DataRecords+outcome.FallbackRecords+outcome.NoDataRecords != tiledelivery.TileSize*tiledelivery.TileSize || outcome.Omitted != (outcome.DataRecords+outcome.FallbackRecords == 0) {
			t.Fatalf("invalid max-zoom outcome: %+v", outcome)
		}
		if !outcome.Omitted {
			produced[[2]uint32{outcome.X, outcome.Y}] = struct{}{}
		}
	}
	blocksPerTile := uint32(tiledelivery.TileSize / tiledelivery.PointBlockSize)
	for _, point := range catalog.Points {
		if _, ok := produced[[2]uint32{point.X / blocksPerTile, point.Y / blocksPerTile}]; !ok {
			t.Fatalf("point has no produced outcome: %+v", point)
		}
	}

	for _, longitude := range []float64{-179.9, 179.9} {
		id, row, column, err := tiledelivery.At(config.Tier, config.MaxZoom, longitude, 0)
		if err != nil {
			t.Fatal(err)
		}
		pointID, _, _, err := tiledelivery.PointAt(config.Tier, config.MaxZoom, longitude, 0)
		if err != nil {
			t.Fatal(err)
		}
		point := findPoint(t, catalog, pointID)
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
		if point.Zoom == waterID.Zoom && point.X/blocksPerTile == waterID.X && point.Y/blocksPerTile == waterID.Y {
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

func TestDisplayMaskDoesNotColourCoastalSupportOutsideLandCentre(t *testing.T) {
	const longitude, latitude = 18.9553, 69.6492
	id, row, column, err := tiledelivery.At(forecastpoints.TierEurope, 5, longitude, latitude)
	if err != nil {
		t.Fatal(err)
	}
	metric := weather.DailyMetrics{
		ComfortableTempC: 24, MinTempC: 19, MaxTempC: 27, RainMM: 0, WindSpeedKPH: 6,
		CloudCoverPercent: weather.OptionalFloat{Value: 15, Available: true},
		DewPointC:         weather.OptionalFloat{Value: 12, Available: true}, NativeResolutionKM: 7, Valid: true,
	}
	metrics := make([]weather.DailyMetrics, 16)
	for index := range metrics {
		metrics[index] = metric
	}
	window := pipeline.DailyMetricsWindow{
		Spec: delivery.GridSpec{
			Bounds: delivery.GridBounds{West: 18.8125, East: 19.0625, South: 69.5, North: 69.75},
			Rows:   4, Cols: 4,
		},
		Metrics: metrics,
	}
	land := func(delivery.GridBounds) (bool, error) { return false, nil }
	texture, points, err := evaluateMaxTile(context.Background(), id, window, window.Spec, land, true)
	if err != nil {
		t.Fatal(err)
	}
	point := points[row*tiledelivery.TileSize+column]
	if point.Weather.Valid {
		t.Fatal("coastal source support coloured a water-centred display texel")
	}
	scalar := texture[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
	if scalar.Weather.Valid {
		t.Fatalf("water-centred scalar is valid: %+v", scalar)
	}
	for rowOffset := -1; rowOffset <= 1; rowOffset++ {
		for columnOffset := -1; columnOffset <= 1; columnOffset++ {
			if rowOffset == 0 && columnOffset == 0 {
				continue
			}
			neighbour := (row+rowOffset)*tiledelivery.TileSize + column + columnOffset
			if points[neighbour].Weather.Valid {
				t.Fatalf("surrounding water texel %d/%d inherited blocky source-cell support", rowOffset, columnOffset)
			}
		}
	}
}

func TestDisplayLandWithoutDetailUsesFallbackWithoutChangingPointNoData(t *testing.T) {
	id, row, column, err := tiledelivery.At(forecastpoints.TierEurope, 5, 18.9553, 69.6492)
	if err != nil {
		t.Fatal(err)
	}
	window := pipeline.DailyMetricsWindow{
		Spec:    delivery.GridSpec{Bounds: delivery.GridBounds{West: 18.8125, East: 19.0625, South: 69.5, North: 69.75}, Rows: 4, Cols: 4},
		Metrics: make([]weather.DailyMetrics, 16),
	}
	texture, points, err := evaluateMaxTile(context.Background(), id, window, window.Spec, func(delivery.GridBounds) (bool, error) { return true, nil }, true)
	if err != nil {
		t.Fatal(err)
	}
	if points[row*tiledelivery.TileSize+column].Weather.Valid {
		t.Fatal("fallback marker changed point no-data into weather evidence")
	}
	for _, field := range fields {
		value, valid, err := fieldValue(texture[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter], field)
		if err != nil || !valid || value != tiledelivery.ForecastScalarFallback {
			t.Fatalf("%s fallback marker = %d valid=%t err=%v", field, value, valid, err)
		}
	}
}

func TestDetailFallbackStopsAtExactICONEUSourceBoundary(t *testing.T) {
	source := delivery.GridSpec{
		Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   680, Cols: 1104,
	}
	empty := pipeline.DailyMetricsWindow{
		Spec: delivery.GridSpec{
			Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90},
			Rows:   2, Cols: 2,
		},
		Metrics: make([]weather.DailyMetrics, 4),
	}
	land := func(delivery.GridBounds) (bool, error) { return true, nil }

	for y := int64(6); y <= 13; y++ {
		outside, err := tiledelivery.NewID(forecastpoints.TierEurope, 5, 20, y)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := tileSourceWindow(source, outside); !ok {
			t.Fatalf("real ICON gutter coordinate 5/20/%d was not visited", y)
		}
		texture, _, err := evaluateMaxTile(context.Background(), outside, empty, source, land, true)
		if err != nil {
			t.Fatal(err)
		}
		outcome := maxZoomOutcome(outside, texture)
		if !outcome.Omitted || outcome.DataRecords != 0 || outcome.FallbackRecords != 0 || outcome.NoDataRecords != tiledelivery.TileSize*tiledelivery.TileSize {
			t.Fatalf("out-of-domain detail outcome 5/20/%d = %+v", y, outcome)
		}
	}

	inside, err := tiledelivery.NewID(forecastpoints.TierEurope, 5, 19, 9)
	if err != nil {
		t.Fatal(err)
	}
	texture, _, err := evaluateMaxTile(context.Background(), inside, empty, source, land, true)
	if err != nil {
		t.Fatal(err)
	}
	insideOutcome := maxZoomOutcome(inside, texture)
	if insideOutcome.Omitted || insideOutcome.FallbackRecords != tiledelivery.TileSize*tiledelivery.TileSize {
		t.Fatalf("in-domain missing detail did not retain fallback: %+v", insideOutcome)
	}

	coverage := make(map[[3]uint32]struct{})
	for y := int64(0); y < 32; y++ {
		for x := int64(0); x < 32; x++ {
			id, err := tiledelivery.NewID(forecastpoints.TierEurope, 5, x, y)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := tileSourceWindow(source, id); !ok {
				continue
			}
			bounds, err := id.Bounds()
			if err != nil {
				t.Fatal(err)
			}
			if bounds.East <= source.Bounds.West || bounds.West >= source.Bounds.East || bounds.North <= source.Bounds.South || bounds.South >= source.Bounds.North {
				continue
			}
			coordinate := [3]uint32{5, uint32(x), uint32(y)}
			for {
				coverage[coordinate] = struct{}{}
				if coordinate[0] == 0 {
					break
				}
				coordinate = [3]uint32{coordinate[0] - 1, coordinate[1] / 2, coordinate[2] / 2}
			}
		}
	}
	if scalarCount := len(coverage) * len(fields); scalarCount != 595 {
		t.Fatalf("exact ICON detail scalar ceiling = %d; want 595", scalarCount)
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
	cancelConfig.Land = testLandMask(t, func(delivery.GridBounds) (bool, error) {
		calls++
		if calls == 100 {
			cancel()
		}
		return true, nil
	})
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

func TestDetailBuildPublishesAllFallbackLandWithoutPointEvidence(t *testing.T) {
	config, closeFrames := worldwideConfig(t, 0, func(float64, float64) (bool, error) { return true, nil }, true)
	defer closeFrames()
	config.Tier = "europe-detail"
	catalog, err := BuildDay(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Points) != 0 || len(catalog.Scalars) != len(fields) {
		t.Fatalf("fallback build points=%d scalars=%d; want 0 and %d", len(catalog.Points), len(catalog.Scalars), len(fields))
	}
	if len(catalog.MaxZoomOutcomes) != 1 || catalog.MaxZoomOutcomes[0].FallbackRecords != tiledelivery.TileSize*tiledelivery.TileSize || catalog.MaxZoomOutcomes[0].Omitted {
		t.Fatalf("fallback outcome = %+v", catalog.MaxZoomOutcomes)
	}
	for _, scalar := range catalog.Scalars {
		encoded, _, err := tiledelivery.ReadForecastScalar(filepath.Join(builtDir(config), filepath.FromSlash(scalar.Path)), scalar.SHA256)
		if err != nil || encoded[tiledelivery.ScalarGutter*tiledelivery.ScalarTextureSize+tiledelivery.ScalarGutter] != tiledelivery.ForecastScalarFallback {
			t.Fatalf("fallback scalar %s read=%v err=%v", scalar.Field, len(encoded), err)
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
	if catalog.Stats.MaxDisplayRecords != z2Stats.MaxDisplayRecords || catalog.Stats.MaxMetricCells > z2Stats.MaxMetricCells || catalog.Stats.MaxScalarBytes > 32*tiledelivery.ScalarTileBytes {
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
	inside, _, _, _ := tiledelivery.PointAt(config.Tier, config.MaxZoom, 5, 50)
	_ = findPoint(t, catalog, inside)
	outside, _, _, _ := tiledelivery.At(config.Tier, config.MaxZoom, -100, 50)
	blocksPerTile := uint32(tiledelivery.TileSize / tiledelivery.PointBlockSize)
	for _, point := range catalog.Points {
		if point.Zoom == outside.Zoom && point.X/blocksPerTile == outside.X && point.Y/blocksPerTile == outside.Y {
			t.Fatalf("regional tier wrapped or escaped its source support: %+v", point)
		}
	}
	source, err := sourceSpec(config.Series)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[[2]uint32]struct{})
	count := uint32(1) << config.MaxZoom
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			id, _ := tiledelivery.NewID(config.Tier, config.MaxZoom, int64(x), int64(y))
			if _, ok := tileSourceWindow(source.Grid, id); ok {
				expected[[2]uint32{x, y}] = struct{}{}
			}
		}
	}
	if len(catalog.MaxZoomOutcomes) != len(expected) {
		t.Fatalf("regional outcomes=%d want=%d", len(catalog.MaxZoomOutcomes), len(expected))
	}
	for _, outcome := range catalog.MaxZoomOutcomes {
		if _, ok := expected[[2]uint32{outcome.X, outcome.Y}]; !ok {
			t.Fatalf("unexpected regional outcome: %+v", outcome)
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

func worldwideConfig(tb testing.TB, zoom uint8, land func(float64, float64) (bool, error), allNoData bool) (Config, func()) {
	return worldwideConfigAt(tb, testDate(), zoom, land, allNoData)
}

func worldwideConfigAt(tb testing.TB, date time.Time, zoom uint8, land func(float64, float64) (bool, error), allNoData bool) (Config, func()) {
	return preparedConfigAt(tb, date, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, zoom, land, allNoData)
}

func preparedConfigAt(tb testing.TB, date time.Time, bounds delivery.GridBounds, zoom uint8, land func(float64, float64) (bool, error), allNoData bool) (Config, func()) {
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
	touchesLand := func(bounds delivery.GridBounds) (bool, error) {
		return land((bounds.West+bounds.East)/2, (bounds.North+bounds.South)/2)
	}
	return Config{Root: root, ReleaseID: "test-release", Period: date.Format("2006-01-02"), Tier: "ifs", Date: date, MaxZoom: zoom, Series: series, Locate: func(float64, float64) (*time.Location, error) { return time.UTC, nil }, Land: testLandMask(tb, touchesLand)}, func() {
		for _, frames := range series {
			for _, frame := range frames {
				_ = frame.Close()
			}
		}
	}
}

func testLandMask(tb testing.TB, displays DisplayLand) LandMask {
	tb.Helper()
	mask, err := NewLandMask(displays, LandIdentity{
		Authority: LandAuthorityNaturalEarth, Dataset: LandDatasetNaturalEarth10M, Version: LandVersionNaturalEarth511,
		SourceArchiveSHA256: strings.Repeat("a", 64), MaskSHA256: strings.Repeat("b", 64), DerivationContract: LandDerivationWebMercatorCentreV1,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return mask
}

func testDate() time.Time { return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC) }
func builtDir(config Config) string {
	return filepath.Join(config.Root, "forecast-days", config.Period, config.Tier)
}

func findPoint(t *testing.T, catalog Catalog, id tiledelivery.PointID) PointTile {
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
	mu          sync.Mutex
	allNoData   bool
	crops       int
	valueOffset float32
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
	return []float32{298.15, 288.15, .2, 0, 3, 4}[index%6] + runner.valueOffset
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
