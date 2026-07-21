// Package forecasttiles assembles one provider tier and one forecast day into
// immutable viewport-loaded point and evaluated-score tiles. It deliberately
// has no release, manifest, or provider-composition policy.
package forecasttiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
)

const catalogVersion = 1

// LocateTimezone returns the civil timezone used to aggregate a source cell.
type LocateTimezone func(longitude, latitude float64) (*time.Location, error)

// IsLand is the sole land authority for output texel centres.
type IsLand func(longitude, latitude float64) (bool, error)

// Config is intentionally one day and one complete provider tier. A caller
// that chooses between IFS and ICON must do so outside this module; there is no
// API here through which fields from two providers can be combined.
type Config struct {
	Root      string
	ReleaseID string
	Period    string
	Tier      string
	Date      time.Time
	MaxZoom   uint8
	Series    pipeline.PreparedSeries
	Locate    LocateTimezone
	Land      IsLand
}

type Field string

const (
	FieldThermal      Field = "thermal"
	FieldDryness      Field = "dryness"
	FieldSun          Field = "sun"
	FieldNiceScore    Field = "nice_score"
	FieldNiceWind     Field = "nice_wind"
	FieldCyclingScore Field = "cycling_score"
	FieldCyclingWind  Field = "cycling_wind"
)

var fields = []Field{
	FieldThermal, FieldDryness, FieldSun, FieldNiceScore,
	FieldNiceWind, FieldCyclingScore, FieldCyclingWind,
}

var periodPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type PointTile struct {
	Zoom          uint8  `json:"zoom"`
	X             uint32 `json:"x"`
	Y             uint32 `json:"y"`
	Path          string `json:"path"`
	Bytes         uint64 `json:"bytes"`
	SHA256        string `json:"sha256"`
	PayloadSHA256 string `json:"payloadSha256"`
}

type ScalarTile struct {
	Field        Field  `json:"field"`
	Zoom         uint8  `json:"zoom"`
	X            uint32 `json:"x"`
	Y            uint32 `json:"y"`
	Path         string `json:"path"`
	Bytes        uint64 `json:"bytes"`
	SHA256       string `json:"sha256"`
	DataTexels   uint32 `json:"dataTexels"`
	NoDataTexels uint32 `json:"noDataTexels"`
}

// BuildStats exposes bounded working-set evidence. Catalog metadata is output,
// so it grows with the number of tiles; these counters cover weather and pixel
// planes, which must remain tile-bounded.
type BuildStats struct {
	MaxMetricCells    uint64 `json:"maxMetricCells"`
	MaxDisplayRecords uint64 `json:"maxDisplayRecords"`
	MaxScalarBytes    uint64 `json:"maxScalarBytes"`
}

type Catalog struct {
	Version   int          `json:"version"`
	ReleaseID string       `json:"releaseId"`
	Period    string       `json:"period"`
	Tier      string       `json:"tier"`
	Date      string       `json:"date"`
	MaxZoom   uint8        `json:"maxZoom"`
	Points    []PointTile  `json:"points"`
	Scalars   []ScalarTile `json:"scalars"`
	Stats     BuildStats   `json:"stats"`
}

// BuildDay owns the complete, deterministic sequence for its exact day/tier
// namespace. Root is a caller-owned assembly directory, not a published
// release. Completed output is installed at forecast-days/<period>/<tier>, so
// other days and tiers can be composed before a separate release writer acts.
// Cancellation or failure removes the private staging tree.
func BuildDay(ctx context.Context, config Config) (Catalog, error) {
	if err := validateConfig(config); err != nil {
		return Catalog{}, err
	}
	if err := ctx.Err(); err != nil {
		return Catalog{}, err
	}
	source, err := sourceSpec(config.Series)
	if err != nil {
		return Catalog{}, err
	}
	final := filepath.Join(config.Root, "forecast-days", config.Period, config.Tier)
	if _, err := os.Stat(final); err == nil {
		return Catalog{}, fmt.Errorf("immutable forecast day tier already exists: %s", final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Catalog{}, err
	}
	if err := os.MkdirAll(config.Root, 0o755); err != nil {
		return Catalog{}, err
	}
	staging, err := os.MkdirTemp(config.Root, ".forecasttiles-")
	if err != nil {
		return Catalog{}, err
	}
	installed := false
	defer func() {
		if !installed {
			_ = os.RemoveAll(staging)
		}
	}()

	writer, err := tiledelivery.NewWriter(staging, config.ReleaseID)
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{
		Version: catalogVersion, ReleaseID: config.ReleaseID, Period: config.Period,
		Tier: config.Tier, Date: config.Date.Format("2006-01-02"), MaxZoom: config.MaxZoom,
		Points: []PointTile{}, Scalars: []ScalarTile{},
	}
	maxAssets := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
	count := uint32(1) << config.MaxZoom
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			if err := ctx.Err(); err != nil {
				return Catalog{}, err
			}
			id, _ := tiledelivery.NewID(config.Tier, config.MaxZoom, int64(x), int64(y))
			window, ok := tileSourceWindow(source, id)
			if !ok {
				continue
			}
			aggregated, err := pipeline.AggregatePreparedDayWindow(ctx, config.Date, window, config.Locate, config.Series)
			if err != nil {
				return Catalog{}, fmt.Errorf("aggregate %d/%d/%d: %w", id.Zoom, id.X, id.Y, err)
			}
			catalog.Stats.MaxMetricCells = max(catalog.Stats.MaxMetricCells, uint64(len(aggregated.Metrics)))
			texture, records, err := evaluateMaxTile(ctx, id, aggregated, config.Land)
			if err != nil {
				return Catalog{}, fmt.Errorf("evaluate %d/%d/%d: %w", id.Zoom, id.X, id.Y, err)
			}
			catalog.Stats.MaxDisplayRecords = max(catalog.Stats.MaxDisplayRecords, uint64(len(texture)+len(records)))
			if !hasData(records) {
				continue
			}
			point, err := writer.Write(id, config.Period, records)
			if err != nil {
				return Catalog{}, err
			}
			pointCatalog, err := pointEntry(staging, point)
			if err != nil {
				return Catalog{}, err
			}
			catalog.Points = append(catalog.Points, pointCatalog)
			for _, field := range fields {
				scalarID, err := scalarID(config.Tier, field, id.Zoom, id.X, id.Y)
				if err != nil {
					return Catalog{}, err
				}
				asset, err := writer.WriteScalar(scalarID, config.Period, textureSampler(id, texture, field))
				if err != nil {
					return Catalog{}, err
				}
				if asset.Omitted {
					return Catalog{}, fmt.Errorf("field %s unexpectedly omitted for data tile %d/%d/%d", field, id.Zoom, id.X, id.Y)
				}
				maxAssets[scalarID] = asset
				scalarCatalog, err := scalarEntry(staging, asset, field)
				if err != nil {
					return Catalog{}, err
				}
				catalog.Scalars = append(catalog.Scalars, scalarCatalog)
			}
			catalog.Stats.MaxScalarBytes = max(catalog.Stats.MaxScalarBytes, uint64(len(texture))*delivery.DisplayScoreRecordBytes+tiledelivery.ScalarTileBytes)
		}
	}

	levelAssets := maxAssets
	for zoom := int(config.MaxZoom) - 1; zoom >= 0; zoom-- {
		next := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
		count := uint32(1) << uint8(zoom)
		for y := uint32(0); y < count; y++ {
			for x := uint32(0); x < count; x++ {
				if err := ctx.Err(); err != nil {
					return Catalog{}, err
				}
				for _, field := range fields {
					id, err := scalarID(config.Tier, field, uint8(zoom), x, y)
					if err != nil {
						return Catalog{}, err
					}
					parent, err := tiledelivery.NewScalarParentSampler(id, func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) {
						asset, ok := levelAssets[id]
						return asset, ok
					})
					if err != nil {
						return Catalog{}, err
					}
					if !parent.HasInteriorData() {
						continue
					}
					asset, err := writer.WriteScalar(id, config.Period, parent.Sample)
					if err != nil {
						return Catalog{}, err
					}
					if asset.Omitted {
						return Catalog{}, fmt.Errorf("lower scalar %s %d/%d/%d lost child data", field, zoom, x, y)
					}
					next[id] = asset
					scalarCatalog, err := scalarEntry(staging, asset, field)
					if err != nil {
						return Catalog{}, err
					}
					catalog.Scalars = append(catalog.Scalars, scalarCatalog)
					catalog.Stats.MaxScalarBytes = max(catalog.Stats.MaxScalarBytes, parent.PeakDecodedBytes()+tiledelivery.ScalarTileBytes)
				}
			}
		}
		levelAssets = next
	}

	sortCatalog(&catalog)
	releaseDir := filepath.Join(staging, "releases", config.ReleaseID)
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return Catalog{}, err
	}
	encoded, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return Catalog{}, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(releaseDir, "forecast-tiles.json"), encoded, 0o644); err != nil {
		return Catalog{}, err
	}
	if err := ctx.Err(); err != nil {
		return Catalog{}, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return Catalog{}, err
	}
	if err := os.Rename(releaseDir, final); err != nil {
		return Catalog{}, err
	}
	installed = true
	_ = os.RemoveAll(staging)
	return catalog, nil
}

func validateConfig(config Config) error {
	if config.Root == "" || !filepath.IsAbs(config.Root) || config.ReleaseID == "" || config.Period == "" || config.Tier == "" || config.Date.IsZero() || config.Locate == nil || config.Land == nil || len(config.Series) == 0 {
		return errors.New("forecast tile config is incomplete")
	}
	if len(config.Period) > 32 || !periodPattern.MatchString(config.Period) {
		return errors.New("forecast tile period is invalid")
	}
	if config.Date.Location() != time.UTC || config.Date.Hour() != 0 || config.Date.Minute() != 0 || config.Date.Second() != 0 || config.Date.Nanosecond() != 0 || config.Period != config.Date.Format("2006-01-02") {
		return errors.New("forecast tile period and date must be the same UTC calendar day")
	}
	if config.MaxZoom > tiledelivery.MaxZoom {
		return errors.New("forecast tile maximum zoom is invalid")
	}
	if _, err := tiledelivery.NewID(config.Tier, config.MaxZoom, 0, 0); err != nil {
		return err
	}
	for _, field := range fields {
		if _, err := scalarID(config.Tier, field, config.MaxZoom, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func sourceSpec(series pipeline.PreparedSeries) (delivery.GridSpec, error) {
	var result delivery.GridSpec
	var resolution float64
	var sourceID string
	var run time.Time
	for field, frames := range series {
		if len(frames) == 0 {
			return delivery.GridSpec{}, fmt.Errorf("prepared field %s is empty", field)
		}
		for _, frame := range frames {
			metadata, err := frame.Metadata()
			if err != nil {
				return delivery.GridSpec{}, err
			}
			if metadata.Field != field {
				return delivery.GridSpec{}, fmt.Errorf("prepared series key %s contains %s", field, metadata.Field)
			}
			if metadata.SourceID == "" || metadata.Run.IsZero() {
				return delivery.GridSpec{}, errors.New("forecast tile tier requires prepared source and run identity")
			}
			if result.Rows == 0 {
				result, resolution, sourceID, run = metadata.Grid, metadata.NativeResolutionKM, metadata.SourceID, metadata.Run
			} else if result != metadata.Grid || math.Abs(resolution-metadata.NativeResolutionKM) > 1e-9*math.Max(1, math.Max(resolution, metadata.NativeResolutionKM)) {
				return delivery.GridSpec{}, errors.New("one forecast tile tier requires one source grid and resolution")
			} else if metadata.SourceID != sourceID || !metadata.Run.Equal(run) {
				return delivery.GridSpec{}, errors.New("one forecast tile tier cannot mix provider products or runs")
			}
		}
	}
	return result, nil
}
