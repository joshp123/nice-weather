package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/artifact"
	"nice-weather-finder/internal/tiles"
)

func TestCurrentForecastReusableRequiresRunAndTileContract(t *testing.T) {
	root := t.TempDir()
	release, err := artifact.NewReleaseDir(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"version":1,"grid":{"tileMaxZoom":%d,"tileRenderer":%d},"sources":{"forecast":{"run":"2026050506"}}}`, tiles.MaxZoom, tiles.RendererVersion)
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifact.PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}

	if !currentForecastReusable(root, "2026050506") {
		t.Fatal("expected current forecast to be reusable")
	}
	if currentForecastReusable(root, "2026050512") {
		t.Fatal("expected different run to require rebuild")
	}
}

func TestCurrentForecastReusableRejectsOldTileContract(t *testing.T) {
	root := t.TempDir()
	release, err := artifact.NewReleaseDir(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), []byte(`{"version":1,"grid":{"tileMaxZoom":6},"sources":{"forecast":{"run":"2026050506"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifact.PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}

	if currentForecastReusable(root, "2026050506") {
		t.Fatal("expected old tile contract to require rebuild")
	}
}

func TestCurrentForecastReusableRejectsOldRenderer(t *testing.T) {
	root := t.TempDir()
	release, err := artifact.NewReleaseDir(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"version":1,"grid":{"tileMaxZoom":%d,"tileRenderer":1},"sources":{"forecast":{"run":"2026050506"}}}`, tiles.MaxZoom)
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifact.PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}

	if currentForecastReusable(root, "2026050506") {
		t.Fatal("expected old renderer to require rebuild")
	}
}
