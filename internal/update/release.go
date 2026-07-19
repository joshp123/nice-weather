package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

type releaseWriter struct {
	dir                 string
	name                string
	root                string
	manifest            delivery.Manifest
	denseForecastMask   *pipeline.FloatGrid
	denseScoreFiles     int
	denseScoreBytes     uint64
	denseScoreBuildTime time.Duration
}

func (writer *releaseWriter) setDenseForecastMask(mask pipeline.FloatGrid) error {
	if mask.Spec.Bounds != writer.manifest.Region.Bounds || mask.Spec.Rows == 0 || mask.Spec.Cols == 0 || len(mask.Values) != int(mask.Spec.Rows)*int(mask.Spec.Cols) {
		return fmt.Errorf("dense forecast mask does not match the release region")
	}
	writer.denseForecastMask = &mask
	return nil
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
		Palette:  delivery.CanonicalPalette(),
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

func (writer *releaseWriter) writePeriod(group, id, label string, spec delivery.GridSpec, metrics []weather.DailyMetrics, source delivery.Source, resolution float64, temporalHours int, denseDisplay bool) (delivery.Period, []weather.DailyMetrics, error) {
	if denseDisplay && group != "forecast" {
		return delivery.Period{}, nil, fmt.Errorf("dense display scores are only valid for forecast periods")
	}
	denseStarted := time.Time{}
	records, err := pipeline.DeliveryRecords(metrics)
	if err != nil {
		return delivery.Period{}, nil, err
	}
	quantized, err := pipeline.QuantizedMetrics(records, metrics)
	if err != nil {
		return delivery.Period{}, nil, err
	}
	pointGeometry, pointRecords, err := delivery.DailyPointRecords(spec, records)
	if err != nil {
		return delivery.Period{}, nil, fmt.Errorf("build %s point plane: %w", id, err)
	}
	pointDirectory := filepath.Join(writer.dir, "points", group)
	pointTemporary := filepath.Join(pointDirectory, "."+id+".building.nwp")
	pointKind := delivery.PointKindForecastDay
	if group == "climate" {
		pointKind = delivery.PointKindLegacyClimateMonth
	}
	pointMeta, err := delivery.WriteDailyPointAsset(pointTemporary, writer.name, pointKind, pointGeometry, pointRecords)
	if err != nil {
		return delivery.Period{}, nil, fmt.Errorf("write %s point asset: %w", id, err)
	}
	pointURL := filepath.ToSlash(filepath.Join("points", group, id+"-"+pointMeta.SHA256+".nwp"))
	if err := os.Rename(pointTemporary, filepath.Join(writer.dir, filepath.FromSlash(pointURL))); err != nil {
		return delivery.Period{}, nil, fmt.Errorf("name %s point asset by content: %w", id, err)
	}
	pointAsset := delivery.PointAssetFromMetadata(pointURL, pointMeta)
	displayGeometry := pointGeometry
	displayRecords := []delivery.DisplayScoreRecord(nil)
	if denseDisplay {
		denseStarted = time.Now()
		if writer.denseForecastMask == nil {
			return delivery.Period{}, nil, fmt.Errorf("dense forecast mask is required for outlook %s", id)
		}
		displayGeometry, displayRecords, err = pipeline.DenseDailyDisplayScores(pointGeometry, pointRecords, writer.denseForecastMask.Spec, *writer.denseForecastMask)
		if err != nil {
			return delivery.Period{}, nil, fmt.Errorf("build %s dense display scores: %w", id, err)
		}
	}
	var displayPoint *delivery.PointAsset
	if displayRecords != nil {
		displayDirectory := filepath.Join(writer.dir, "points", group, "display")
		displayTemporary := filepath.Join(displayDirectory, "."+id+".building.nwp")
		displayMeta, writeErr := delivery.WriteDisplayScoreAsset(displayTemporary, writer.name, displayGeometry, displayRecords)
		if writeErr != nil {
			return delivery.Period{}, nil, fmt.Errorf("write %s display-score asset: %w", id, writeErr)
		}
		displayURL := filepath.ToSlash(filepath.Join("points", group, "display", id+"-"+displayMeta.SHA256+".nwp"))
		if err := os.Rename(displayTemporary, filepath.Join(writer.dir, filepath.FromSlash(displayURL))); err != nil {
			return delivery.Period{}, nil, fmt.Errorf("name %s display-score asset by content: %w", id, err)
		}
		asset := delivery.PointAssetFromMetadata(displayURL, displayMeta)
		displayPoint = &asset
	}
	images := make(map[string]delivery.ImageAsset, 2)
	lenses := make(map[string]delivery.ImageAsset, len(delivery.RequiredLensAssets()))
	imageWidth, imageHeight := int(pointGeometry.Cols), int(pointGeometry.Rows)
	for _, item := range []struct {
		id      string
		profile weather.Profile
	}{{delivery.ProfileNice, weather.ProfileNice}, {delivery.ProfileCycling, weather.ProfileCycling}} {
		var asset delivery.ImageAsset
		if displayRecords != nil {
			asset, err = writer.renderDisplayOverlay(group, id, item.id, displayGeometry, displayRecords, item.profile, weather.LensOverall)
		} else {
			asset, err = writer.renderOverlay(group, id, item.id, pointGeometry, pointRecords, item.profile, weather.LensOverall, imageWidth, imageHeight)
		}
		if err != nil {
			return delivery.Period{}, nil, err
		}
		images[item.id] = asset
	}
	for _, item := range []struct {
		id      string
		profile weather.Profile
		lens    weather.Lens
	}{
		{delivery.LensComfort, weather.ProfileNice, weather.LensComfort},
		{delivery.LensDry, weather.ProfileNice, weather.LensDry},
		{delivery.LensBright, weather.ProfileNice, weather.LensBright},
		{"wind-" + delivery.ProfileNice, weather.ProfileNice, weather.LensCalm},
		{"wind-" + delivery.ProfileCycling, weather.ProfileCycling, weather.LensCalm},
	} {
		var asset delivery.ImageAsset
		if displayRecords != nil {
			asset, err = writer.renderDisplayOverlay(group, id, item.id, displayGeometry, displayRecords, item.profile, item.lens)
		} else {
			asset, err = writer.renderOverlay(group, id, item.id, pointGeometry, pointRecords, item.profile, item.lens, imageWidth, imageHeight)
		}
		if err != nil {
			return delivery.Period{}, nil, err
		}
		lenses[item.id] = asset
	}
	if displayPoint != nil {
		if err := writer.recordDenseScoreAssets(denseStarted, *displayPoint, images, lenses); err != nil {
			return delivery.Period{}, nil, err
		}
	}
	return delivery.Period{
		ID: id, Label: label, Source: source, NativeResolutionKM: resolution,
		TemporalResolutionHours: temporalHours, Images: images, Lenses: lenses, Point: pointAsset, DisplayPoint: displayPoint,
	}, quantized, nil
}

func (writer *releaseWriter) renderDisplayOverlay(group, periodID, assetID string, geometry delivery.PointGeometry, records []delivery.DisplayScoreRecord, profile weather.Profile, lens weather.Lens) (delivery.ImageAsset, error) {
	return writer.renderScorePNG(group, periodID, assetID, geometry, func(file *os.File) error {
		return pipeline.RenderDisplayScoreRecords(file, geometry, records, profile, lens, writer.manifest.Palette)
	})
}

func (writer *releaseWriter) renderScorePNG(group, periodID, assetID string, geometry delivery.PointGeometry, render func(*os.File) error) (delivery.ImageAsset, error) {
	directory := filepath.Join(writer.dir, "images", group)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return delivery.ImageAsset{}, err
	}
	file, err := os.CreateTemp(directory, ".overlay-*.tmp")
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	temporary, committed := file.Name(), false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := render(file); err != nil {
		return delivery.ImageAsset{}, fmt.Errorf("render %s %s overlay: %w", periodID, assetID, err)
	}
	if err := file.Close(); err != nil {
		return delivery.ImageAsset{}, err
	}
	data, err := os.ReadFile(temporary)
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	digest := sha256.Sum256(data)
	checksum := hex.EncodeToString(digest[:])
	url := filepath.ToSlash(filepath.Join("images", group, periodID+"-"+assetID+"-"+checksum+".png"))
	if err := os.Rename(temporary, filepath.Join(writer.dir, filepath.FromSlash(url))); err != nil {
		return delivery.ImageAsset{}, err
	}
	committed = true
	return delivery.ImageAsset{URL: url, SHA256: checksum, Width: int(geometry.Cols), Height: int(geometry.Rows), Projection: delivery.ImageProjectionWebMercator}, nil
}

func (writer *releaseWriter) writeWindFrames(periodID string, series pipeline.Series, source delivery.Source) ([]delivery.WindFrameAsset, error) {
	validFrom, err := time.Parse(time.RFC3339, source.ValidFrom)
	if err != nil {
		return nil, fmt.Errorf("parse wind validity start: %w", err)
	}
	validTo, err := time.Parse(time.RFC3339, source.ValidTo)
	if err != nil {
		return nil, fmt.Errorf("parse wind validity end: %w", err)
	}
	uFrames, vFrames := series[pipeline.WindU], series[pipeline.WindV]
	if len(uFrames) == 0 || len(vFrames) == 0 {
		return nil, fmt.Errorf("complete u/v wind series is required")
	}
	for _, component := range []struct {
		label  string
		frames []pipeline.Frame
	}{{"u", uFrames}, {"v", vFrames}} {
		for index := 1; index < len(component.frames); index++ {
			if !component.frames[index].End.After(component.frames[index-1].End) {
				return nil, fmt.Errorf("%s wind source timestamps are not strictly increasing at %s", component.label, component.frames[index].End.Format(time.RFC3339))
			}
		}
	}
	vByTime := make(map[time.Time]pipeline.Frame, len(vFrames))
	for _, frame := range vFrames {
		if _, duplicate := vByTime[frame.End]; duplicate {
			return nil, fmt.Errorf("duplicate v wind frame at %s", frame.End.Format(time.RFC3339))
		}
		vByTime[frame.End] = frame
	}
	assets := make([]delivery.WindFrameAsset, 0)
	for _, uFrame := range uFrames {
		validAt := uFrame.End
		if validAt.Before(validFrom) || validAt.After(validTo) {
			continue
		}
		vFrame, ok := vByTime[validAt]
		if !ok {
			return nil, fmt.Errorf("u wind frame at %s has no matching v frame", validAt.Format(time.RFC3339))
		}
		if uFrame.Semantics != pipeline.Instant || vFrame.Semantics != pipeline.Instant || uFrame.Start != validAt || vFrame.Start != validAt {
			return nil, fmt.Errorf("wind frame at %s is not instantaneous", validAt.Format(time.RFC3339))
		}
		if uFrame.Units != "km/h" || vFrame.Units != "km/h" {
			return nil, fmt.Errorf("wind frame at %s must use canonical km/h components", validAt.Format(time.RFC3339))
		}
		if uFrame.Grid.Spec != vFrame.Grid.Spec || len(uFrame.Grid.Values) != len(vFrame.Grid.Values) {
			return nil, fmt.Errorf("wind frame at %s has mismatched u/v geometry", validAt.Format(time.RFC3339))
		}
		vectors := make([]delivery.WindVector, len(uFrame.Grid.Values))
		for index := range vectors {
			u, v := uFrame.Grid.Values[index], vFrame.Grid.Values[index]
			uMissing, vMissing := math.IsNaN(float64(u)), math.IsNaN(float64(v))
			if uMissing != vMissing {
				return nil, fmt.Errorf("wind frame at %s cell %d has partial u/v support", validAt.Format(time.RFC3339), index)
			}
			if !uMissing {
				vectors[index] = delivery.WindVector{U: u / 3.6, V: v / 3.6, Valid: true}
			}
		}
		directory := filepath.Join(writer.dir, "wind", periodID)
		temporary := filepath.Join(directory, fmt.Sprintf(".%s.building.nww.gz", validAt.Format("1504")))
		metadata, err := delivery.WriteWindGridGzip(temporary, uFrame.Grid.Spec, vectors)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", validAt.Format(time.RFC3339), err)
		}
		url := filepath.ToSlash(filepath.Join("wind", periodID, fmt.Sprintf("%s-%s.nww.gz", validAt.Format("1504"), metadata.SHA256)))
		if err := os.Rename(temporary, filepath.Join(writer.dir, filepath.FromSlash(url))); err != nil {
			return nil, err
		}
		assets = append(assets, delivery.WindFrameAsset{ValidAt: validAt.Format(time.RFC3339), URL: url, SHA256: metadata.SHA256, ClippedComponents: metadata.ClippedComponents})
	}
	if len(assets) < 2 {
		return nil, fmt.Errorf("period has %d in-window wind frames; want at least 2", len(assets))
	}
	for index := 1; index < len(assets); index++ {
		previous, _ := time.Parse(time.RFC3339, assets[index-1].ValidAt)
		current, _ := time.Parse(time.RFC3339, assets[index].ValidAt)
		if !current.After(previous) {
			return nil, fmt.Errorf("wind source timestamps are not strictly increasing at %s", assets[index].ValidAt)
		}
	}
	return assets, nil
}

func (writer *releaseWriter) renderOverlay(group, periodID, assetID string, geometry delivery.PointGeometry, records []delivery.DailyPointRecord, profile weather.Profile, lens weather.Lens, width, height int) (delivery.ImageAsset, error) {
	directory := filepath.Join(writer.dir, "images", group)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return delivery.ImageAsset{}, err
	}
	file, err := os.CreateTemp(directory, ".overlay-*.tmp")
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	temporary := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := pipeline.RenderPointDailyRecords(file, geometry, records, profile, lens, writer.manifest.Palette); err != nil {
		return delivery.ImageAsset{}, fmt.Errorf("render %s %s overlay: %w", periodID, assetID, err)
	}
	if err := file.Close(); err != nil {
		return delivery.ImageAsset{}, err
	}
	data, err := os.ReadFile(temporary)
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	digest := sha256.Sum256(data)
	checksum := hex.EncodeToString(digest[:])
	url := filepath.ToSlash(filepath.Join("images", group, periodID+"-"+assetID+"-"+checksum+".png"))
	if err := os.Rename(temporary, filepath.Join(writer.dir, filepath.FromSlash(url))); err != nil {
		return delivery.ImageAsset{}, err
	}
	committed = true
	return delivery.ImageAsset{URL: url, SHA256: checksum, Width: width, Height: height, Projection: delivery.ImageProjectionWebMercator}, nil
}

func (writer *releaseWriter) writeForecastWindow(id, label string, start, end time.Time, spec delivery.GridSpec, days [][]weather.DailyMetrics, resolution float64) (delivery.ForecastWindow, error) {
	denseStarted := time.Now()
	window := delivery.ForecastWindow{
		ID: id, Label: label, StartDate: start.Format("2006-01-02"), EndDate: end.Format("2006-01-02"),
		DayCount: len(days), GridResolutionKM: resolution, Profiles: make(map[string]delivery.ForecastRangeAsset, 2),
		Lenses: make(map[string]delivery.ImageAsset, len(delivery.RequiredLensAssets())),
	}
	ranges := make(map[string][]delivery.RangeRecord, 2)
	for _, item := range []struct {
		id      string
		profile weather.Profile
	}{{delivery.ProfileNice, weather.ProfileNice}, {delivery.ProfileCycling, weather.ProfileCycling}} {
		records, err := pipeline.ForecastRangeRecords(spec, days, item.profile)
		if err != nil {
			return delivery.ForecastWindow{}, fmt.Errorf("build %s forecast window: %w", item.id, err)
		}
		ranges[item.id] = records
	}
	pointGeometry, pointRecords, err := delivery.RangePointRecords(spec, ranges[delivery.ProfileNice], ranges[delivery.ProfileCycling], days)
	if err != nil {
		return delivery.ForecastWindow{}, fmt.Errorf("build forecast-window point plane: %w", err)
	}
	if writer.denseForecastMask == nil {
		return delivery.ForecastWindow{}, fmt.Errorf("dense forecast mask is required for forecast window")
	}
	pointGeometry, pointRecords, err = pipeline.DenseRangePointRecords(pointGeometry, pointRecords, writer.denseForecastMask.Spec, *writer.denseForecastMask)
	if err != nil {
		return delivery.ForecastWindow{}, fmt.Errorf("build dense forecast-window score plane: %w", err)
	}
	pointDirectory := filepath.Join(writer.dir, "points", "forecast-window")
	pointTemporary := filepath.Join(pointDirectory, "."+id+".building.nwp")
	pointMeta, err := delivery.WriteRangePointAsset(pointTemporary, writer.name, pointGeometry, pointRecords)
	if err != nil {
		return delivery.ForecastWindow{}, fmt.Errorf("write forecast-window point asset: %w", err)
	}
	pointURL := filepath.ToSlash(filepath.Join("points", "forecast-window", id+"-"+pointMeta.SHA256+".nwp"))
	if err := os.Rename(pointTemporary, filepath.Join(writer.dir, filepath.FromSlash(pointURL))); err != nil {
		return delivery.ForecastWindow{}, fmt.Errorf("name forecast-window point asset by content: %w", err)
	}
	window.Point = delivery.PointAssetFromMetadata(pointURL, pointMeta)
	for _, item := range []struct {
		id      string
		profile weather.Profile
	}{{delivery.ProfileNice, weather.ProfileNice}, {delivery.ProfileCycling, weather.ProfileCycling}} {
		image, err := writer.renderRangeOverlay(id, item.id, pointGeometry, pointRecords, item.profile, weather.LensOverall)
		if err != nil {
			return delivery.ForecastWindow{}, err
		}
		window.Profiles[item.id] = delivery.ForecastRangeAsset{Image: image}
	}
	for _, item := range []struct {
		id      string
		profile weather.Profile
		lens    weather.Lens
	}{
		{delivery.LensComfort, weather.ProfileNice, weather.LensComfort},
		{delivery.LensDry, weather.ProfileNice, weather.LensDry},
		{delivery.LensBright, weather.ProfileNice, weather.LensBright},
		{"wind-" + delivery.ProfileNice, weather.ProfileNice, weather.LensCalm},
		{"wind-" + delivery.ProfileCycling, weather.ProfileCycling, weather.LensCalm},
	} {
		image, err := writer.renderRangeOverlay(id, item.id, pointGeometry, pointRecords, item.profile, item.lens)
		if err != nil {
			return delivery.ForecastWindow{}, err
		}
		window.Lenses[item.id] = image
	}
	windowImages := make(map[string]delivery.ImageAsset, len(window.Profiles))
	for profile, asset := range window.Profiles {
		windowImages[profile] = asset.Image
	}
	if err := writer.recordDenseScoreAssets(denseStarted, window.Point, windowImages, window.Lenses); err != nil {
		return delivery.ForecastWindow{}, err
	}
	return window, nil
}

func (writer *releaseWriter) recordDenseScoreAssets(started time.Time, point delivery.PointAsset, groups ...map[string]delivery.ImageAsset) error {
	bytes := point.Bytes
	files := 1
	for _, group := range groups {
		for _, asset := range group {
			info, err := os.Stat(filepath.Join(writer.dir, filepath.FromSlash(asset.URL)))
			if err != nil {
				return fmt.Errorf("measure dense score asset %q: %w", asset.URL, err)
			}
			bytes += uint64(info.Size())
			files++
		}
	}
	writer.denseScoreFiles += files
	writer.denseScoreBytes += bytes
	writer.denseScoreBuildTime += time.Since(started)
	return nil
}

func (writer *releaseWriter) renderRangeOverlay(windowID, assetID string, geometry delivery.PointGeometry, records []delivery.RangePointRecord, weatherProfile weather.Profile, lens weather.Lens) (delivery.ImageAsset, error) {
	directory := filepath.Join(writer.dir, "images", "forecast-window")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return delivery.ImageAsset{}, err
	}
	file, err := os.CreateTemp(directory, ".overlay-*.tmp")
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	temporary := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := pipeline.RenderPointRangeLensRecords(file, geometry, records, weatherProfile, lens, writer.manifest.Palette); err != nil {
		return delivery.ImageAsset{}, fmt.Errorf("render %s %s range overlay: %w", windowID, assetID, err)
	}
	if err := file.Close(); err != nil {
		return delivery.ImageAsset{}, err
	}
	data, err := os.ReadFile(temporary)
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	digest := sha256.Sum256(data)
	checksum := hex.EncodeToString(digest[:])
	url := filepath.ToSlash(filepath.Join("images", "forecast-window", windowID+"-"+assetID+"-"+checksum+".png"))
	if err := os.Rename(temporary, filepath.Join(writer.dir, filepath.FromSlash(url))); err != nil {
		return delivery.ImageAsset{}, err
	}
	committed = true
	width, height := int(geometry.Cols), int(geometry.Rows)
	return delivery.ImageAsset{URL: url, SHA256: checksum, Width: width, Height: height, Projection: delivery.ImageProjectionWebMercator}, nil
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
