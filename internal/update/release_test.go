package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestReleaseBuildIsOutsideCleanupVisibleReleases(t *testing.T) {
	root := t.TempDir()
	writer, err := newReleaseWriter(root, "live-test", time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
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
	writer, err := newReleaseWriter(root, "live-orphan", time.Now())
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
	writer, err := newReleaseWriter(root, "live-finish-test", generated)
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: pipelineBounds(), Rows: 1, Cols: 1}
	metric := weather.DailyMetrics{
		ComfortableTempC: 24, MinTempC: 20, MaxTempC: 27, RainMM: 0,
		WetTimeFraction: weather.OptionalFloat{Available: true}, CloudCoverPercent: weather.OptionalFloat{Value: 20, Available: true},
		WindSpeedKPH: 8, RelativeHumidityPercent: weather.OptionalFloat{Value: 55, Available: true}, NativeResolutionKM: 7, Valid: true,
	}
	dwdRun := time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)
	aifsRun := time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)
	rangeDays := make([][]weather.DailyMetrics, 0, 14)
	for index := 0; index < 14; index++ {
		date := time.Date(2026, 7, 18+index, 0, 0, 0, 0, time.UTC)
		provider, label, run, kind, resolution, temporal := "ecmwf-aifs", "ECMWF AIFS", aifsRun, "outlook", 28.0, 6
		if index < 4 {
			provider, label, run, kind, resolution, temporal = "dwd-icon-eu", "DWD ICON-EU", dwdRun, "detailed", 7, 1
		}
		source := delivery.Source{ProviderID: provider, ProviderLabel: label, RunAt: run.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(21 * time.Hour).Format(time.RFC3339)}
		id := date.Format("2006-01-02")
		period, quantized, err := writer.writePeriod("forecast", id, id, spec, []weather.DailyMetrics{metric}, source, resolution, temporal)
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
		period, _, err := writer.writePeriod("climate", "month-"+time.Month(month).String(), id, spec, []weather.DailyMetrics{metric}, source, 11, 720)
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
	if !strings.Contains(manifest.Forecast.Days[0].GridURL, manifest.Forecast.Days[0].GridSHA256) {
		t.Fatal("grid URL is mutable")
	}
	for profile, asset := range manifest.Forecast.Window.Profiles {
		if !strings.Contains(asset.GridURL, asset.GridSHA256) || !strings.Contains(asset.Image.URL, asset.Image.SHA256) {
			t.Fatalf("forecast window %s assets are mutable: %+v", profile, asset)
		}
	}
	if !currentUsesRuns(root, dwdRun, aifsRun) || currentUsesRuns(root, dwdRun.Add(-6*time.Hour), aifsRun) {
		t.Fatal("idempotence run matching failed")
	}
}

func TestForecastWindowUsesThePublishedDailyQuantizationAtScoreThresholds(t *testing.T) {
	root := t.TempDir()
	writer, err := newReleaseWriter(root, "threshold-parity", time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: pipelineBounds(), Rows: 1, Cols: 1}
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
	_, publishedMetrics, err := writer.writePeriod("forecast", date.Format("2006-01-02"), "Today", spec, []weather.DailyMetrics{metric}, source, 7, 1)
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
	reader, err := delivery.OpenRangeGrid(filepath.Join(writer.dir, filepath.FromSlash(window.Profiles[delivery.ProfileNice].GridURL)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	record, valid, err := reader.RecordAt(5, 50)
	if err != nil || !valid {
		t.Fatalf("range point valid=%t err=%v", valid, err)
	}
	if int(record.Score) != weather.Score(quantized, weather.ProfileNice).Score || int(record.Score) == raw.Score {
		t.Fatalf("range score %d did not use published daily score %d (raw was %d)", record.Score, weather.Score(quantized, weather.ProfileNice).Score, raw.Score)
	}
}

func formatMonth(month int) string {
	if month < 10 {
		return "0" + string(rune('0'+month))
	}
	return "1" + string(rune('0'+month-10))
}
