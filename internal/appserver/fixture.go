package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/weather"
)

var fixtureTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

const fixtureRelease = "fixture-20260718-v9"

var fixtureLegend = delivery.CanonicalLegend()
var fixturePalette = delivery.CanonicalPalette()

// GenerateFixture writes a complete, deterministic product release. Its
// metadata calls it sample data so it cannot be presented as a live forecast.
func GenerateFixture(root string) error {
	return generateFixture(root, fixtureRelease, 0)
}

func generateFixture(root, releaseName string, temperatureOffset float64) error {
	landMask, err := fixtureLandMask()
	if err != nil {
		return err
	}
	releaseDir, err := delivery.BeginRelease(root, releaseName)
	if err != nil {
		existing := filepath.Join(root, "releases", releaseName)
		if _, validationErr := delivery.ValidateProductionRelease(existing); validationErr == nil {
			if publishErr := delivery.PublishProductionRelease(root, existing); publishErr == nil {
				return delivery.CleanupStaleReleases(root)
			}
		}
		return fmt.Errorf("begin fixture release: %w", err)
	}
	spec := fixtureGridSpec
	manifest := delivery.Manifest{
		Version:         delivery.ManifestVersion,
		ReleaseID:       releaseName,
		ProductContract: delivery.ProductContract,
		DataStatus:      delivery.DataStatusSample,
		GeneratedAt:     fixtureTime.Format(time.RFC3339),
		Region:          delivery.Region{ID: "europe-sample", Label: "Europe (sample data)", Bounds: spec.Bounds},
		Profiles:        []delivery.Profile{{ID: "nice", Label: "Nice"}, {ID: "cycling", Label: "Cycling"}},
		Legend:          fixtureLegend,
		Palette:         fixturePalette,
		Forecast:        delivery.ForecastCatalog{UpdatedAt: fixtureTime.Add(-2 * time.Hour).Format(time.RFC3339), StaleAfterHours: 18},
		Climate:         &delivery.ClimateCatalog{Label: "Typical year (sample data)", ReferencePeriod: "1991–2020"},
	}
	for day := 0; day < 14; day++ {
		date := fixtureTime.AddDate(0, 0, day)
		kind, temporal := "outlook", 6
		if day < 5 {
			kind, temporal = "detailed", 1
		}
		period, err := writeFixturePeriod(releaseDir, spec, landMask, "forecast", date.Format("2006-01-02"), day, 7, 28, temporal, kind, temperatureOffset)
		if err != nil {
			return err
		}
		manifest.Forecast.Days = append(manifest.Forecast.Days, delivery.ForecastPeriod{
			Period: period, Date: date.Format("2006-01-02"), Kind: kind,
		})
	}
	for month := 1; month <= 12; month++ {
		id := fmt.Sprintf("month-%02d", month)
		period, err := writeFixturePeriod(releaseDir, spec, landMask, "climate", id, month-1, month, 28, 24*30, "climate", temperatureOffset)
		if err != nil {
			return err
		}
		period.Label = time.Month(month).String()
		manifest.Climate.Months = append(manifest.Climate.Months, delivery.ClimatePeriod{Period: period, Month: month})
	}
	if err := delivery.WriteManifest(releaseDir, manifest); err != nil {
		return fmt.Errorf("write fixture manifest: %w", err)
	}
	if err := delivery.PublishProductionRelease(root, releaseDir); err != nil {
		return fmt.Errorf("publish fixture release: %w", err)
	}
	return delivery.CleanupStaleReleases(root)
}

func writeFixturePeriod(releaseDir string, spec delivery.GridSpec, landMask []byte, group, id string, phase, month int, resolution float64, temporal int, sourceKind string, temperatureOffset float64) (delivery.Period, error) {
	if spec != fixtureGridSpec || len(landMask)*8 != int(spec.Rows*spec.Cols) {
		return delivery.Period{}, fmt.Errorf("fixture land mask does not match fixture grid")
	}
	records := make([]delivery.DailyRecord, int(spec.Rows*spec.Cols))
	for row := uint32(0); row < spec.Rows; row++ {
		lat := spec.Bounds.North - (float64(row)+.5)/float64(spec.Rows)*(spec.Bounds.North-spec.Bounds.South)
		for col := uint32(0); col < spec.Cols; col++ {
			index := int(row*spec.Cols + col)
			if !fixtureLandCell(landMask, index) {
				continue
			}
			lon := spec.Bounds.West + (float64(col)+.5)/float64(spec.Cols)*(spec.Bounds.East-spec.Bounds.West)
			record, err := fixtureRecord(lon, lat, phase, month, sourceKind != "climate", temperatureOffset)
			if err != nil {
				return delivery.Period{}, fmt.Errorf("build fixture cell: %w", err)
			}
			records[index] = record
		}
	}
	gridDirectory := filepath.Join(releaseDir, "grids", group)
	gridTemporary := filepath.Join(gridDirectory, "."+id+".building.nwg")
	metadata, err := delivery.WriteGrid(gridTemporary, spec, records)
	if err != nil {
		return delivery.Period{}, fmt.Errorf("write fixture grid %s: %w", id, err)
	}
	gridURL := filepath.ToSlash(filepath.Join("grids", group, id+"-"+metadata.SHA256+".nwg"))
	if err := os.Rename(gridTemporary, filepath.Join(releaseDir, filepath.FromSlash(gridURL))); err != nil {
		return delivery.Period{}, fmt.Errorf("name fixture grid %s by content: %w", id, err)
	}
	metrics := make([]weather.DailyMetrics, len(records))
	for index, record := range records {
		if !record.Valid {
			continue
		}
		metric, err := record.WeatherMetrics(1)
		if err != nil {
			return delivery.Period{}, fmt.Errorf("rebuild fixture weather cell %d: %w", index, err)
		}
		metrics[index] = metric
	}
	images := make(map[string]delivery.ImageAsset, 2)
	for _, profile := range []struct {
		id string
		p  weather.Profile
	}{{"nice", weather.ProfileNice}, {"cycling", weather.ProfileCycling}} {
		asset, err := writeOverlay(releaseDir, group, id, profile.id, spec, records, metrics, profile.p, weather.LensOverall)
		if err != nil {
			return delivery.Period{}, err
		}
		images[profile.id] = asset
	}
	lenses := make(map[string]delivery.ImageAsset, len(delivery.RequiredLensAssets()))
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
		asset, err := writeOverlay(releaseDir, group, id, item.id, spec, records, metrics, item.profile, item.lens)
		if err != nil {
			return delivery.Period{}, err
		}
		lenses[item.id] = asset
	}
	source := fixtureSource(group, id, sourceKind)
	return delivery.Period{
		ID: id, Label: id, Source: source,
		NativeResolutionKM: resolution, TemporalResolutionHours: temporal,
		Images: images, Lenses: lenses, GridURL: gridURL, GridSHA256: metadata.SHA256,
	}, nil
}

func fixtureSource(group, id, sourceKind string) delivery.Source {
	if group == "climate" {
		return delivery.Source{
			ProviderID: "sample-climate", ProviderLabel: "Sample typical weather",
			RunAt: "1991-01-01T00:00:00Z", ValidFrom: "1991-01-01T00:00:00Z", ValidTo: "2021-01-01T00:00:00Z",
		}
	}
	date, _ := time.Parse("2006-01-02", id)
	providerID, label := "sample-outlook", "Sample outlook"
	if sourceKind == "detailed" {
		providerID, label = "sample-detailed", "Sample detailed forecast"
	}
	return delivery.Source{
		ProviderID: providerID, ProviderLabel: label, RunAt: fixtureTime.Add(-2 * time.Hour).Format(time.RFC3339),
		ValidFrom: date.Add(10 * time.Hour).Format(time.RFC3339), ValidTo: date.Add(20 * time.Hour).Format(time.RFC3339),
	}
}

func fixtureRecord(lon, lat float64, phase, month int, hasWetTime bool, temperatureOffset float64) (delivery.DailyRecord, error) {
	season := math.Cos((float64(month) - 7) / 12 * 2 * math.Pi)
	southWarmth := (52 - lat) * .42
	atlantic := math.Sin((lon+float64(phase)*2)*.18) * math.Cos(lat*.21)
	continental := math.Sin((lon - lat + float64(phase)*3) * .11)
	comfort := 18 + 7*season + southWarmth + 3*atlantic + 2*continental + temperatureOffset
	rain := math.Max(0, 2.1+2.3*math.Sin(lon*.27+lat*.19+float64(phase)*.9)-.08*(52-lat))
	wet := math.Min(.85, math.Max(0, rain/9+.08*math.Sin(float64(phase)+lon)))
	cloud := math.Min(100, math.Max(4, 37+rain*7+18*math.Sin(lat*.17-lon*.09+float64(phase)*.55)))
	wind := math.Max(3, 12+7*math.Abs(math.Sin(lon*.13+lat*.09+float64(phase)*.4))+math.Max(0, lat-55)*.3)
	dew := math.Min(comfort-1, comfort-8+rain*.45)
	humidity := math.Min(98, math.Max(30, 48+rain*6+(lat-45)*.35))
	solar := math.Max(.8, (20-cloud*.15)*(0.55+.45*math.Max(0, season)))
	metrics := weather.DailyMetrics{
		ComfortableTempC: comfort, MinTempC: comfort - 4.5, MaxTempC: comfort + 4.5,
		RainMM:            rain,
		SolarEnergyMJm2:   weather.OptionalFloat{Value: solar, Available: true},
		CloudCoverPercent: weather.OptionalFloat{Value: cloud, Available: true},
		WindSpeedKPH:      wind, GustKPH: weather.OptionalFloat{Value: wind*1.55 + 5, Available: true},
		DewPointC:               weather.OptionalFloat{Value: dew, Available: true},
		RelativeHumidityPercent: weather.OptionalFloat{Value: humidity, Available: true},
		NativeResolutionKM:      1, Valid: true,
	}
	if hasWetTime {
		metrics.WetTimeFraction = weather.OptionalFloat{Value: wet, Available: true}
	}
	return delivery.RecordFromWeather(metrics)
}

func writeOverlay(releaseDir, group, periodID, assetID string, spec delivery.GridSpec, records []delivery.DailyRecord, metrics []weather.DailyMetrics, profile weather.Profile, lens weather.Lens) (delivery.ImageAsset, error) {
	directory := filepath.Join(releaseDir, "images", group)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return delivery.ImageAsset{}, err
	}
	imageWidth, imageHeight, err := delivery.WebMercatorImageDimensions(spec)
	if err != nil {
		return delivery.ImageAsset{}, err
	}
	file, err := os.CreateTemp(directory, ".overlay-*.png")
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
	digest := sha256.New()
	if err := pipeline.RenderWebMercatorLensRecords(io.MultiWriter(file, digest), spec, records, metrics, profile, lens, fixturePalette); err != nil {
		_ = file.Close()
		return delivery.ImageAsset{}, err
	}
	if err := file.Close(); err != nil {
		return delivery.ImageAsset{}, err
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	filename := fmt.Sprintf("%s-%s-%s.png", periodID, assetID, checksum)
	path := filepath.Join(directory, filename)
	if err := os.Rename(temporary, path); err != nil {
		return delivery.ImageAsset{}, err
	}
	committed = true
	url := filepath.ToSlash(filepath.Join("images", group, filename))
	return delivery.ImageAsset{
		URL: url, SHA256: checksum, Width: imageWidth, Height: imageHeight,
		Projection: delivery.ImageProjectionWebMercator,
	}, nil
}
