package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestClimateCheckpointValidatesAndCopiesCanonicalAssets(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	writer, err := newReleaseWriter(root, "live-cache-source", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	spec := delivery.GridSpec{Bounds: pipelineBounds(), Rows: 425, Cols: 690}
	metrics := make([]weather.DailyMetrics, int(spec.Rows*spec.Cols))
	source := delivery.Source{ProviderID: "chelsa", ProviderLabel: "CHELSA 2.1 · CC0", RunAt: "1981-01-01T00:00:00Z", ValidFrom: "1981-01-01T00:00:00Z", ValidTo: "2011-01-01T00:00:00Z"}
	period, _, err := writer.writePeriod("climate", "month-01", "January", spec, metrics, source, 11, 720, false)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := delivery.ClimatePeriod{Period: period, Month: 1}
	if err := saveClimateCheckpoint(cache, writer.dir, checkpoint); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "copy")
	loaded, ok, err := loadClimateCheckpoint(cache, destination, 1)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if loaded.Point.URL != period.Point.URL || loaded.Images[delivery.ProfileNice].URL != period.Images[delivery.ProfileNice].URL {
		t.Fatal("checkpoint asset identity drifted")
	}

	data, err := json.Marshal(climateCheckpoint{
		Contract: "nice-weather-v12|chelsa-2.1-1981-2010-0p1|validated-finite|overlap-renderer-v2",
		Period:   checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(cache, "month-01.json")
	if err := os.WriteFile(checkpointPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := loadClimateCheckpoint(cache, filepath.Join(root, "obsolete-copy"), 1); err != nil || ok {
		t.Fatalf("prior scoring checkpoint: ok=%v err=%v", ok, err)
	}
	for _, relative := range periodAssets(period) {
		if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("obsolete scoring asset %q was retained: %v", relative, err)
		}
	}
	if _, err := os.Stat(checkpointPath + ".invalid"); err != nil {
		t.Fatalf("obsolete checkpoint was not quarantined: %v", err)
	}
}

func TestClimateCheckpointRejectsPrePaletteContract(t *testing.T) {
	cache := t.TempDir()
	data, err := json.Marshal(climateCheckpoint{
		Contract: "nice-weather-v8|chelsa-2.1-1981-2010-0p1|validated-finite|overlap-renderer-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(cache, "month-01.json")
	if err := os.WriteFile(checkpointPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := loadClimateCheckpoint(cache, t.TempDir(), 1); err != nil || ok {
		t.Fatalf("obsolete v8 checkpoint: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(checkpointPath + ".invalid"); err != nil {
		t.Fatalf("obsolete v8 checkpoint was not quarantined: %v", err)
	}
}

func TestClimateCheckpointQuarantinesUnsafePath(t *testing.T) {
	cache := t.TempDir()
	checkpoint := climateCheckpoint{Contract: climateScoreContract, Period: delivery.ClimatePeriod{Period: delivery.Period{ID: "month-01", Label: "January", Point: delivery.PointAsset{URL: "../../escape"}}, Month: 1}}
	data, _ := json.Marshal(checkpoint)
	path := filepath.Join(cache, "month-01.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := loadClimateCheckpoint(cache, t.TempDir(), 1)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if matches, _ := filepath.Glob(path + ".invalid*"); len(matches) != 1 {
		t.Fatalf("quarantine files = %v", matches)
	}
}

func TestClimateCheckpointRejectsTrailingJSONAndBoundsQuarantine(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "month-01.json")
	for attempt := 0; attempt < 2; attempt++ {
		checkpoint := climateCheckpoint{Contract: climateScoreContract}
		data, err := json.Marshal(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, []byte(`{"ignored":true}`)...)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := loadClimateCheckpoint(cache, t.TempDir(), 1); err != nil || ok {
			t.Fatalf("attempt %d: ok=%v err=%v", attempt, ok, err)
		}
	}
	if matches, _ := filepath.Glob(path + ".invalid*"); len(matches) != 1 {
		t.Fatalf("quarantine files = %v, want one bounded diagnostic", matches)
	}
}

func TestRemoveClimateCheckpointAssetsReportsCleanupFailure(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "points", "climate", "blocked.nwp")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	period := delivery.Period{Point: delivery.PointAsset{URL: "points/climate/blocked.nwp"}}
	if err := removeClimateCheckpointAssets(cache, period); err == nil {
		t.Fatal("non-empty obsolete asset directory cleanup failure was ignored")
	}
}
