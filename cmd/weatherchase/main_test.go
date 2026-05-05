package main

import (
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/artifact"
)

func TestCurrentForecastRunReadsPublishedRelease(t *testing.T) {
	root := t.TempDir()
	release, err := artifact.NewReleaseDir(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), []byte(`{"version":1,"sources":{"forecast":{"run":"2026050506"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifact.PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}

	got, ok := currentForecastRun(root)
	if !ok || got != "2026050506" {
		t.Fatalf("currentForecastRun = %q, %v; want 2026050506, true", got, ok)
	}
}
