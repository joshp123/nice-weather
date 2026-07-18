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
	for index := 0; index < 14; index++ {
		date := time.Date(2026, 7, 18+index, 0, 0, 0, 0, time.UTC)
		provider, label, run, kind, resolution, temporal := "ecmwf-aifs", "ECMWF AIFS", aifsRun, "outlook", 28.0, 6
		if index < 4 {
			provider, label, run, kind, resolution, temporal = "dwd-icon-eu", "DWD ICON-EU", dwdRun, "detailed", 7, 1
		}
		source := delivery.Source{ProviderID: provider, ProviderLabel: label, RunAt: run.Format(time.RFC3339), ValidFrom: date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(21 * time.Hour).Format(time.RFC3339)}
		id := date.Format("2006-01-02")
		period, err := writer.writePeriod("forecast", id, id, spec, []weather.DailyMetrics{metric}, source, resolution, temporal)
		if err != nil {
			t.Fatal(err)
		}
		writer.manifest.Forecast.Days = append(writer.manifest.Forecast.Days, delivery.ForecastPeriod{Period: period, Date: id, Kind: kind})
	}
	for month := 1; month <= 12; month++ {
		id := time.Month(month).String()
		source := delivery.Source{ProviderID: "chelsa", ProviderLabel: "CHELSA 2.1 · CC0", RunAt: "1981-01-01T00:00:00Z", ValidFrom: "1981-01-01T00:00:00Z", ValidTo: "2011-01-01T00:00:00Z"}
		period, err := writer.writePeriod("climate", "month-"+time.Month(month).String(), id, spec, []weather.DailyMetrics{metric}, source, 11, 720)
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
	if !currentUsesRuns(root, dwdRun, aifsRun) || currentUsesRuns(root, dwdRun.Add(-6*time.Hour), aifsRun) {
		t.Fatal("idempotence run matching failed")
	}
}

func formatMonth(month int) string {
	if month < 10 {
		return "0" + string(rune('0'+month))
	}
	return "1" + string(rune('0'+month-10))
}
