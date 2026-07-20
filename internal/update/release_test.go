package update

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/product"
	"nice-weather-finder/internal/weather"
)

func TestReleaseWriterRejectsAnInvalidProductDomainBeforeBuilding(t *testing.T) {
	root := t.TempDir()
	if _, err := newReleaseWriter(root, "invalid-domain", time.Now(), product.Domain{}); err == nil {
		t.Fatal("release writer accepted an invalid product domain")
	}
	if _, err := os.Stat(filepath.Join(root, ".building")); !os.IsNotExist(err) {
		t.Fatalf("invalid domain created build state: %v", err)
	}
}

func TestReleaseBuildIsOutsideCleanupVisibleReleases(t *testing.T) {
	root := t.TempDir()
	writer, err := newReleaseWriter(root, "live-test", time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".building", "live-test")
	if writer.dir != want {
		t.Fatalf("build dir = %q, want %q", writer.dir, want)
	}
}

func TestCleanupAbandonedBuildsDoesNotTouchPublishedReleases(t *testing.T) {
	root := t.TempDir()
	abandoned := filepath.Join(root, ".building", "partial", "file")
	published := filepath.Join(root, "releases", "live-good", "file")
	for _, path := range []string{abandoned, published} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupAbandonedBuilds(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".building")); !os.IsNotExist(err) {
		t.Fatalf("abandoned build namespace remains: %v", err)
	}
	if _, err := os.Stat(published); err != nil {
		t.Fatalf("published release was touched: %v", err)
	}
}

func TestDiscardUncommittedPreservesReleaseMovedAcrossCommitBoundary(t *testing.T) {
	root := t.TempDir()
	writer, err := newReleaseWriter(root, "live-orphan", time.Now(), product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(root, "releases", writer.name)
	if err := os.MkdirAll(filepath.Dir(release), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(writer.dir, release); err != nil {
		t.Fatal(err)
	}
	writer.dir = release
	writer.discardUncommitted()
	if _, err := os.Stat(release); err != nil {
		t.Fatalf("committed orphan was removed: %v", err)
	}
}

func TestLiveWriterFinishesValidatedContentAddressedReleaseAndIdempotenceSeesRuns(t *testing.T) {
	root := t.TempDir()
	generated := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	writer, err := newReleaseWriter(root, "live-finish-test", generated, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	setTestDenseMask(t, writer, product.Europe().DenseForecastScoreSpec())
	metric := weather.DailyMetrics{
		ComfortableTempC: 24, MinTempC: 20, MaxTempC: 27, RainMM: 0,
		WetTimeFraction: weather.OptionalFloat{Available: true}, CloudCoverPercent: weather.OptionalFloat{Value: 20, Available: true},
		WindSpeedKPH: 8, RelativeHumidityPercent: weather.OptionalFloat{Value: 55, Available: true}, NativeResolutionKM: 7, Valid: true,
	}
	dwdRun := time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)
	ifsRun := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	rangeDays := make([][]weather.DailyMetrics, 0, 14)
	for index := 0; index < 14; index++ {
		date := time.Date(2026, 7, 18+index, 0, 0, 0, 0, time.UTC)
		provider, label, run, kind, resolution, temporal := "ecmwf-ifs", "ECMWF IFS", ifsRun, "outlook", 28.0, 6
		if index < 4 {
			provider, label, run, kind, resolution, temporal = "dwd-icon-eu", "DWD ICON-EU", dwdRun, "detailed", 7, 1
		}
		source := delivery.Source{ProviderID: provider, ProviderLabel: label, RunAt: run.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(21 * time.Hour).Format(time.RFC3339)}
		id := date.Format("2006-01-02")
		period, quantized, err := writer.writePeriod("forecast", id, id, spec, []weather.DailyMetrics{metric}, source, resolution, temporal, kind == "outlook")
		if err != nil {
			t.Fatal(err)
		}
		period.WindFrames, err = writer.writeWindFrames(id, testWindSeries(spec, date.Add(6*time.Hour), temporal), source)
		if err != nil {
			t.Fatal(err)
		}
		writer.manifest.Forecast.Days = append(writer.manifest.Forecast.Days, delivery.ForecastPeriod{Period: period, Date: id, Kind: kind})
		rangeDays = append(rangeDays, quantized)
	}
	window, err := writer.writeForecastWindow("next-14-days", "Next 14 days", time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC), spec, rangeDays, 28)
	if err != nil {
		t.Fatal(err)
	}
	writer.manifest.Forecast.Window = window
	for month := 1; month <= 12; month++ {
		id := time.Month(month).String()
		source := delivery.Source{ProviderID: "chelsa", ProviderLabel: "CHELSA 2.1 · CC0", RunAt: "1981-01-01T00:00:00Z", ValidFrom: "1981-01-01T00:00:00Z", ValidTo: "2011-01-01T00:00:00Z"}
		period, _, err := writer.writePeriod("climate", "month-"+time.Month(month).String(), id, spec, []weather.DailyMetrics{metric}, source, 11, 720, false)
		if err != nil {
			t.Fatal(err)
		}
		period.ID = "month-" + formatMonth(month)
		writer.manifest.Climate.Months = append(writer.manifest.Climate.Months, delivery.ClimatePeriod{Period: period, Month: month})
	}
	if err := writer.finish(root); err != nil {
		t.Fatal(err)
	}
	current, _ := delivery.CurrentDir(root)
	resolved, _ := filepath.EvalSymlinks(current)
	manifest, err := delivery.ValidateProductionRelease(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ReleaseID != "live-finish-test" {
		t.Fatal(manifest.ReleaseID)
	}
	if manifest.ProductContract != delivery.ProductContract {
		t.Fatalf("product contract = %q", manifest.ProductContract)
	}
	for _, image := range manifest.Forecast.Days[0].Images {
		if !strings.Contains(image.URL, image.SHA256) {
			t.Fatalf("image URL is mutable: %s", image.URL)
		}
	}
	if !strings.Contains(manifest.Forecast.Days[0].Point.URL, manifest.Forecast.Days[0].Point.SHA256) {
		t.Fatal("point asset URL is mutable")
	}
	for profile, asset := range manifest.Forecast.Window.Profiles {
		if !strings.Contains(manifest.Forecast.Window.Point.URL, manifest.Forecast.Window.Point.SHA256) || !strings.Contains(asset.Image.URL, asset.Image.SHA256) {
			t.Fatalf("forecast window %s assets are mutable: %+v", profile, asset)
		}
	}
	if !currentUsesRuns(root, product.Europe(), dwdRun, ifsRun) || currentUsesRuns(root, product.Europe(), dwdRun.Add(-6*time.Hour), ifsRun) {
		t.Fatal("idempotence run matching failed")
	}
}

func testWindSeries(spec delivery.GridSpec, start time.Time, cadenceHours int) pipeline.Series {
	return testWindSeriesCount(spec, start, cadenceHours, 3)
}

func testWindSeriesCount(spec delivery.GridSpec, start time.Time, cadenceHours, count int) pipeline.Series {
	times := make([]time.Time, count)
	for index := 0; index < count; index++ {
		times[index] = start.Add(time.Duration(index*cadenceHours) * time.Hour)
	}
	return testWindSeriesAt(spec, times)
}

func testWindSeriesAt(spec delivery.GridSpec, times []time.Time) pipeline.Series {
	series := pipeline.Series{pipeline.WindU: nil, pipeline.WindV: nil}
	for index, validAt := range times {
		for _, item := range []struct {
			field pipeline.Field
			value float32
		}{{pipeline.WindU, float32(index + 2)}, {pipeline.WindV, float32(-index - 1)}} {
			series[item.field] = append(series[item.field], pipeline.Frame{
				Field: item.field, Start: validAt, End: validAt, Semantics: pipeline.Instant,
				Units: "km/h", NativeResolutionKM: 7,
				Grid: pipeline.FloatGrid{Spec: spec, Values: []float32{item.value}},
			})
		}
	}
	return series
}

func TestWriteWindFramesPreservesMixedNativeSourceTimes(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	times := []time.Time{
		date.Add(6 * time.Hour), date.Add(7 * time.Hour), date.Add(8 * time.Hour), date.Add(9 * time.Hour),
		date.Add(10 * time.Hour), date.Add(11 * time.Hour), date.Add(12 * time.Hour),
		date.Add(15 * time.Hour), date.Add(18 * time.Hour), date.Add(21 * time.Hour),
	}
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Add(-66 * time.Hour).Format(time.RFC3339), ValidFrom: times[0].Format(time.RFC3339), ValidTo: times[len(times)-1].Format(time.RFC3339)}
	writer, err := newReleaseWriter(t.TempDir(), "mixed-wind-times", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	assets, err := writer.writeWindFrames("2026-07-18", testWindSeriesAt(spec, times), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != len(times) {
		t.Fatalf("wind frame count = %d; want %d", len(assets), len(times))
	}
	for index := range assets {
		if assets[index].ValidAt != times[index].Format(time.RFC3339) {
			t.Fatalf("wind frame %d = %s; want %s", index, assets[index].ValidAt, times[index].Format(time.RFC3339))
		}
	}
}

func TestWriteWindFramesRejectsOutOfOrderSourceCatalogue(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	series := testWindSeries(spec, date.Add(6*time.Hour), 1)
	series[pipeline.WindU][0], series[pipeline.WindU][1] = series[pipeline.WindU][1], series[pipeline.WindU][0]
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(8 * time.Hour).Format(time.RFC3339)}
	writer, err := newReleaseWriter(t.TempDir(), "out-of-order-wind", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.writeWindFrames("2026-07-18", series, source); err == nil || !strings.Contains(err.Error(), "not strictly increasing") {
		t.Fatalf("out-of-order source error = %v", err)
	}
}

func TestWriteWindFramesPublishesExactNativeTimesAcrossForecastSources(t *testing.T) {
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, provider string
		first, last    int
		cadence, count int
	}{
		{name: "first ICON day hourly", provider: "dwd-icon-eu", first: 6, last: 21, cadence: 1, count: 16},
		{name: "late ICON day three-hourly", provider: "dwd-icon-eu", first: 6, last: 21, cadence: 3, count: 6},
		{name: "IFS outlook six-hourly", provider: "ecmwf-ifs", first: 6, last: 18, cadence: 6, count: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer, err := newReleaseWriter(t.TempDir(), strings.ReplaceAll(test.name, " ", "-"), date, product.Europe())
			if err != nil {
				t.Fatal(err)
			}
			source := delivery.Source{ProviderID: test.provider, ProviderLabel: test.provider, RunAt: date.Add(-6 * time.Hour).Format(time.RFC3339), ValidFrom: date.Add(time.Duration(test.first) * time.Hour).Format(time.RFC3339), ValidTo: date.Add(time.Duration(test.last) * time.Hour).Format(time.RFC3339)}
			assets, err := writer.writeWindFrames("2026-07-18", testWindSeriesCount(spec, date.Add(time.Duration(test.first)*time.Hour), test.cadence, test.count), source)
			if err != nil {
				t.Fatal(err)
			}
			if len(assets) != test.count || assets[0].ValidAt != source.ValidFrom || assets[len(assets)-1].ValidAt != source.ValidTo {
				t.Fatalf("wind assets = %d %s..%s; want %d %s..%s", len(assets), assets[0].ValidAt, assets[len(assets)-1].ValidAt, test.count, source.ValidFrom, source.ValidTo)
			}
		})
	}
}

func TestWriteWindFramesRejectsMissingNativeTime(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	series := testWindSeriesCount(spec, date.Add(6*time.Hour), 1, 3)
	series[pipeline.WindV] = append(series[pipeline.WindV][:1], series[pipeline.WindV][2:]...)
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(8 * time.Hour).Format(time.RFC3339)}
	writer, err := newReleaseWriter(t.TempDir(), "missing-wind-time", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.writeWindFrames("2026-07-18", series, source); err == nil || !strings.Contains(err.Error(), "no matching v frame") {
		t.Fatalf("missing native frame error = %v", err)
	}
}

func TestWriteWindFramesRejectsNonCanonicalUnits(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	series := testWindSeries(spec, date.Add(6*time.Hour), 1)
	series[pipeline.WindU][0].Units = "m/s"
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(8 * time.Hour).Format(time.RFC3339)}
	writer, err := newReleaseWriter(t.TempDir(), "wrong-wind-units", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.writeWindFrames("2026-07-18", series, source); err == nil || !strings.Contains(err.Error(), "canonical km/h") {
		t.Fatalf("non-canonical unit error = %v", err)
	}
}

func TestWriteWindFramesConvertsCanonicalKmhToMps(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	series := testWindSeries(spec, date.Add(6*time.Hour), 1)
	series[pipeline.WindU][0].Grid.Values[0] = 10.8
	series[pipeline.WindV][0].Grid.Values[0] = -16.2
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(8 * time.Hour).Format(time.RFC3339)}
	writer, err := newReleaseWriter(t.TempDir(), "wind-unit-conversion", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	assets, err := writer.writeWindFrames("2026-07-18", series, source)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := os.Open(filepath.Join(writer.dir, filepath.FromSlash(assets[0].URL)))
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := gzip.NewReader(compressed)
	if err != nil {
		_ = compressed.Close()
		t.Fatal(err)
	}
	rawPath := filepath.Join(t.TempDir(), "frame.nww")
	raw, err := os.Create(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(raw, decompressed); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := decompressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := delivery.OpenWindGrid(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	vector, err := reader.Vector(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if vector != (delivery.WindVector{U: 3, V: -4.5, Valid: true}) {
		t.Fatalf("published wind vector = %+v; want 3.0,-4.5 m/s", vector)
	}
}

func TestForecastWindowUsesThePublishedDailyQuantizationAtScoreThresholds(t *testing.T) {
	root := t.TempDir()
	writer, err := newReleaseWriter(root, "threshold-parity", time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	setTestDenseMask(t, writer, spec)
	metric := weather.DailyMetrics{
		ComfortableTempC: 19, MinTempC: 16, MaxTempC: 22, CloudCoverPercent: weather.OptionalFloat{Value: 30, Available: true},
		WindSpeedKPH: 9, GustKPH: weather.OptionalFloat{Value: 14, Available: true},
		DewPointC: weather.OptionalFloat{Value: 12, Available: true}, NativeResolutionKM: 7, Valid: true,
	}
	var raw weather.Result
	var quantized weather.DailyMetrics
	found := false
	for thousandths := 15000; thousandths <= 30000; thousandths++ {
		metric.ComfortableTempC = float64(thousandths) / 1000
		metric.MinTempC, metric.MaxTempC = metric.ComfortableTempC-3, metric.ComfortableTempC+3
		record, recordErr := delivery.RecordFromWeather(metric)
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		candidate, decodeErr := record.WeatherMetrics(metric.NativeResolutionKM)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		raw = weather.Score(metric, weather.ProfileNice)
		if scored := weather.Score(candidate, weather.ProfileNice); scored.Score != raw.Score {
			quantized, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("test did not find a score threshold affected by daily-grid quantization")
	}
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	source := delivery.Source{ProviderID: "dwd-icon-eu", ProviderLabel: "DWD ICON-EU", RunAt: date.Add(-6 * time.Hour).Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(21 * time.Hour).Format(time.RFC3339)}
	_, publishedMetrics, err := writer.writePeriod("forecast", date.Format("2006-01-02"), "Today", spec, []weather.DailyMetrics{metric}, source, 7, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if publishedMetrics[0] != quantized {
		t.Fatalf("writer quantized metric = %+v; want %+v", publishedMetrics[0], quantized)
	}
	days := make([][]weather.DailyMetrics, 14)
	for index := range days {
		days[index] = publishedMetrics
	}
	window, err := writer.writeForecastWindow("next-14-days", "Next 14 days", date, date.AddDate(0, 0, 13), spec, days, 28)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := delivery.OpenPointAsset(filepath.Join(writer.dir, filepath.FromSlash(window.Point.URL)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	pointRecord, valid, err := reader.RangeAt(5, 50)
	if err != nil || !valid {
		t.Fatalf("range point valid=%t err=%v", valid, err)
	}
	record := pointRecord.Nice
	if int(record.Score) != weather.Score(quantized, weather.ProfileNice).Score || int(record.Score) == raw.Score {
		t.Fatalf("range score %d did not use published daily score %d (raw was %d)", record.Score, weather.Score(quantized, weather.ProfileNice).Score, raw.Score)
	}
}

func TestForecastWindowImagesMatchThePublishedPointRecord(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	writer, err := newReleaseWriter(root, "window-image-parity", date, product.Europe())
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: product.Europe().Bounds(), Rows: 1, Cols: 1}
	setTestDenseMask(t, writer, spec)
	metric := weather.DailyMetrics{
		ComfortableTempC: 24, MinTempC: 19, MaxTempC: 29, RainMM: 1.2,
		WetTimeFraction:   weather.OptionalFloat{Value: 0.2, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: 35, Available: true},
		WindSpeedKPH:      18, GustKPH: weather.OptionalFloat{Value: 30, Available: true},
		DewPointC: weather.OptionalFloat{Value: 15, Available: true}, NativeResolutionKM: 28, Valid: true,
	}
	days := make([][]weather.DailyMetrics, delivery.ForecastPointDayCount)
	for index := range days {
		days[index] = []weather.DailyMetrics{metric}
	}
	window, err := writer.writeForecastWindow("next-14-days", "Next 14 days", date, date.AddDate(0, 0, 13), spec, days, 28)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := delivery.OpenPointAsset(filepath.Join(writer.dir, filepath.FromSlash(window.Point.URL)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	record, valid, err := reader.RangeAt(10, 50)
	if err != nil || !valid {
		t.Fatalf("published range point valid=%t err=%v", valid, err)
	}
	tests := []struct {
		name    string
		asset   delivery.ImageAsset
		profile weather.Profile
		lens    weather.Lens
	}{
		{"nice overall", window.Profiles[delivery.ProfileNice].Image, weather.ProfileNice, weather.LensOverall},
		{"cycling overall", window.Profiles[delivery.ProfileCycling].Image, weather.ProfileCycling, weather.LensOverall},
		{"comfort", window.Lenses[delivery.LensComfort], weather.ProfileNice, weather.LensComfort},
		{"dry", window.Lenses[delivery.LensDry], weather.ProfileNice, weather.LensDry},
		{"bright", window.Lenses[delivery.LensBright], weather.ProfileNice, weather.LensBright},
		{"nice calm", window.Lenses["wind-"+delivery.ProfileNice], weather.ProfileNice, weather.LensCalm},
		{"cycling calm", window.Lenses["wind-"+delivery.ProfileCycling], weather.ProfileCycling, weather.LensCalm},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var expected bytes.Buffer
			if err := pipeline.RenderPointRangeLensRecords(&expected, reader.Metadata().Geometry, []delivery.RangePointRecord{record}, test.profile, test.lens, writer.manifest.Palette); err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(filepath.Join(writer.dir, filepath.FromSlash(test.asset.URL)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, expected.Bytes()) {
				t.Fatal("published image does not match its point-record lens")
			}
		})
	}
}

func formatMonth(month int) string {
	if month < 10 {
		return "0" + string(rune('0'+month))
	}
	return "1" + string(rune('0'+month-10))
}

func setTestDenseMask(t *testing.T, writer *releaseWriter, spec delivery.GridSpec) {
	t.Helper()
	values := make([]float32, int(spec.Rows)*int(spec.Cols))
	for index := range values {
		values[index] = 1
	}
	if err := writer.setDenseForecastMask(pipeline.FloatGrid{Spec: spec, Values: values}); err != nil {
		t.Fatal(err)
	}
}
