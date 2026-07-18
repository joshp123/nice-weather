package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

type releaseWriter struct {
	dir      string
	name     string
	root     string
	manifest delivery.Manifest
}

// cleanupAbandonedBuilds removes only the private pre-commit namespace. It is
// called while the update lock is held; published releases are never touched.
func cleanupAbandonedBuilds(root string) error {
	if err := os.RemoveAll(filepath.Join(root, ".building")); err != nil {
		return fmt.Errorf("clean abandoned release builds: %w", err)
	}
	return nil
}

func newReleaseWriter(root, name string, generated time.Time) (*releaseWriter, error) {
	buildingRoot := filepath.Join(root, ".building")
	if err := os.MkdirAll(buildingRoot, 0o755); err != nil {
		return nil, err
	}
	dir := filepath.Join(buildingRoot, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, err
	}
	return &releaseWriter{dir: dir, name: name, root: root, manifest: delivery.Manifest{
		Version: delivery.ManifestVersion, ReleaseID: name, ProductContract: delivery.ProductContract, DataStatus: delivery.DataStatusLive, GeneratedAt: generated.UTC().Format(time.RFC3339),
		Region:   delivery.Region{ID: "europe", Label: "Europe", Bounds: pipeline.EuropeGridBounds()},
		Profiles: []delivery.Profile{{ID: delivery.ProfileNice, Label: "Nice"}, {ID: delivery.ProfileCycling, Label: "Cycling"}},
		Legend:   delivery.CanonicalLegend(),
		Forecast: delivery.ForecastCatalog{UpdatedAt: generated.UTC().Format(time.RFC3339), StaleAfterHours: 18},
		Climate:  &delivery.ClimateCatalog{Label: "Typical month", ReferencePeriod: "1981–2010"},
	}}, nil
}

func (writer *releaseWriter) discardUncommitted() {
	if writer == nil {
		return
	}
	buildingRoot := filepath.Join(writer.root, ".building")
	if filepath.Dir(writer.dir) == buildingRoot {
		_ = os.RemoveAll(writer.dir)
	}
}

func (writer *releaseWriter) writePeriod(group, id, label string, spec delivery.GridSpec, metrics []weather.DailyMetrics, source delivery.Source, resolution float64, temporalHours int) (delivery.Period, error) {
	records, err := pipeline.DeliveryRecords(metrics)
	if err != nil {
		return delivery.Period{}, err
	}
	gridDirectory := filepath.Join(writer.dir, "grids", group)
	gridTemporary := filepath.Join(gridDirectory, "."+id+".building.nwg")
	meta, err := delivery.WriteGrid(gridTemporary, spec, records)
	if err != nil {
		return delivery.Period{}, fmt.Errorf("write %s grid: %w", id, err)
	}
	gridURL := filepath.ToSlash(filepath.Join("grids", group, id+"-"+meta.SHA256+".nwg"))
	if err := os.Rename(gridTemporary, filepath.Join(writer.dir, filepath.FromSlash(gridURL))); err != nil {
		return delivery.Period{}, fmt.Errorf("name %s grid by content: %w", id, err)
	}
	images := make(map[string]delivery.ImageAsset, 2)
	imageWidth, imageHeight, err := delivery.WebMercatorImageDimensions(spec)
	if err != nil {
		return delivery.Period{}, err
	}
	for _, item := range []struct {
		id      string
		profile weather.Profile
	}{{delivery.ProfileNice, weather.ProfileNice}, {delivery.ProfileCycling, weather.ProfileCycling}} {
		directory := filepath.Join(writer.dir, "images", group)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return delivery.Period{}, err
		}
		file, err := os.CreateTemp(directory, ".overlay-*.tmp")
		if err != nil {
			return delivery.Period{}, err
		}
		temporary := file.Name()
		if err := pipeline.RenderWebMercatorOverlay(file, spec, metrics, item.profile, writer.manifest.Legend); err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return delivery.Period{}, fmt.Errorf("render %s %s overlay: %w", id, item.id, err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(temporary)
			return delivery.Period{}, err
		}
		data, err := os.ReadFile(temporary)
		if err != nil {
			return delivery.Period{}, err
		}
		digest := sha256.Sum256(data)
		checksum := hex.EncodeToString(digest[:])
		url := filepath.ToSlash(filepath.Join("images", group, id+"-"+item.id+"-"+checksum+".png"))
		if err := os.Rename(temporary, filepath.Join(writer.dir, filepath.FromSlash(url))); err != nil {
			_ = os.Remove(temporary)
			return delivery.Period{}, err
		}
		images[item.id] = delivery.ImageAsset{
			URL: url, SHA256: checksum, Width: imageWidth, Height: imageHeight,
			Projection: delivery.ImageProjectionWebMercator,
		}
	}
	return delivery.Period{
		ID: id, Label: label, Source: source, NativeResolutionKM: resolution,
		TemporalResolutionHours: temporalHours, Images: images, GridURL: gridURL, GridSHA256: meta.SHA256,
	}, nil
}

func (writer *releaseWriter) finish(root string) error {
	if err := delivery.WriteManifest(writer.dir, writer.manifest); err != nil {
		return err
	}
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		return err
	}
	final := filepath.Join(releases, writer.name)
	if err := os.Rename(writer.dir, final); err != nil {
		return fmt.Errorf("commit built release: %w", err)
	}
	writer.dir = final
	if err := delivery.PublishProductionRelease(root, writer.dir); err != nil {
		return err
	}
	if err := delivery.CleanupStaleReleases(root); err != nil {
		fmt.Printf("warning: published release; stale-release cleanup failed: %v\n", err)
	}
	return nil
}
