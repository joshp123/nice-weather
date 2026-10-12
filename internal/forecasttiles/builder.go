// Package forecasttiles assembles one provider tier and one forecast day into
// immutable viewport-loaded point and evaluated-score tiles. It deliberately
// has no release, manifest, or provider-composition policy.
package forecasttiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/tiledelivery"
)

const CatalogVersion = 3

const (
	ProviderIFS                       = "ecmwf-ifs"
	ProviderICONEU                    = "dwd-icon-eu"
	TierGlobal                        = "global"
	TierEurope                        = "europe-detail"
	LandAuthorityNaturalEarth         = "Natural Earth"
	LandDatasetNaturalEarth10M        = "ne_10m_land"
	LandVersionNaturalEarth511        = "5.1.1"
	LandDerivationWebMercatorCentreV1 = "web-mercator-centre-on-land-v1"
)

// LocateTimezone returns the civil timezone used to aggregate a source cell.
type LocateTimezone func(longitude, latitude float64) (*time.Location, error)

// DisplayLand reports whether the centre of an exact output texel is on land.
// Native point records remain independent of this display-only boundary, so a
// coastal place can retain provider evidence without colouring nearby water.
type DisplayLand func(bounds delivery.GridBounds) (bool, error)

// LandIdentity names the exact polygon source and raster mask consumed by one
// forecast tier. SourceArchiveSHA256 identifies the downloaded authority;
// MaskSHA256 identifies the exact row-major 0/1 Web Mercator bytes used by the
// predicate. DerivationContract fixes the projection and centre-on-land policy.
type LandIdentity struct {
	Authority           string `json:"authority"`
	Dataset             string `json:"dataset"`
	Version             string `json:"version"`
	SourceArchiveSHA256 string `json:"sourceArchiveSha256"`
	MaskSHA256          string `json:"maskSha256"`
	DerivationContract  string `json:"derivationContract"`
}

// LandMask keeps the predicate and its immutable identity together. Callers
// can inspect the identity but cannot relabel a predicate after construction.
type LandMask struct {
	displays DisplayLand
	identity LandIdentity
}

func NewLandMask(displays DisplayLand, identity LandIdentity) (LandMask, error) {
	if displays == nil || ValidateLandIdentity(identity) != nil {
		return LandMask{}, errors.New("forecast land mask identity is invalid")
	}
	return LandMask{displays: displays, identity: identity}, nil
}

func (mask LandMask) Displays(bounds delivery.GridBounds) (bool, error) {
	if mask.displays == nil || ValidateLandIdentity(mask.identity) != nil {
		return false, errors.New("forecast land mask is invalid")
	}
	return mask.displays(bounds)
}

func (mask LandMask) Identity() LandIdentity { return mask.identity }

func ValidateLandIdentity(identity LandIdentity) error {
	if identity.Authority == LandAuthorityNaturalEarth && identity.Dataset == LandDatasetNaturalEarth10M &&
		identity.Version == LandVersionNaturalEarth511 && identity.DerivationContract == LandDerivationWebMercatorCentreV1 &&
		validLowerSHA256(identity.SourceArchiveSHA256) && validLowerSHA256(identity.MaskSHA256) {
		return nil
	}
	return errors.New("forecast land identity is invalid")
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

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
	Land      LandMask
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
	DataRecords   uint32 `json:"dataRecords"`
	NoDataRecords uint32 `json:"noDataRecords"`
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

// SourceIdentity is derived from every prepared frame consumed by BuildDay.
// EvidenceSHA256 covers their ordered physical metadata; callers do not get
// to label scalar output with a different source after the build.
type SourceIdentity struct {
	SourceID           string            `json:"sourceId"`
	RunUTC             string            `json:"runUtc"`
	Grid               delivery.GridSpec `json:"grid"`
	NativeResolutionKM float64           `json:"nativeResolutionKm"`
	EvidenceSHA256     string            `json:"evidenceSha256"`
}

// MaxZoomOutcome records the evaluated interior of every max-zoom tile that
// intersects the provider grid. An omitted outcome is valid only when all
// records are no-data.
type MaxZoomOutcome struct {
	Zoom            uint8  `json:"zoom"`
	X               uint32 `json:"x"`
	Y               uint32 `json:"y"`
	Omitted         bool   `json:"omitted"`
	DataRecords     uint32 `json:"dataRecords"`
	FallbackRecords uint32 `json:"fallbackRecords"`
	NoDataRecords   uint32 `json:"noDataRecords"`
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
	Version         int              `json:"version"`
	ReleaseID       string           `json:"releaseId"`
	Period          string           `json:"period"`
	Tier            string           `json:"tier"`
	Date            string           `json:"date"`
	MaxZoom         uint8            `json:"maxZoom"`
	Source          SourceIdentity   `json:"source"`
	Land            LandIdentity     `json:"land"`
	MaxZoomOutcomes []MaxZoomOutcome `json:"maxZoomOutcomes"`
	Points          []PointTile      `json:"points"`
	Scalars         []ScalarTile     `json:"scalars"`
	Stats           BuildStats       `json:"stats"`
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
	sourceIdentity, err := sourceSpec(config.Series)
	if err != nil {
		return Catalog{}, err
	}
	source := sourceIdentity.Grid
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
		Version: CatalogVersion, ReleaseID: config.ReleaseID, Period: config.Period,
		Tier: config.Tier, Date: config.Date.Format("2006-01-02"), MaxZoom: config.MaxZoom,
		Source: sourceIdentity, Land: config.Land.Identity(), MaxZoomOutcomes: []MaxZoomOutcome{}, Points: []PointTile{}, Scalars: []ScalarTile{},
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
			allowFallback := config.Tier == "europe-detail"
			texture, records, err := evaluateMaxTile(ctx, id, aggregated, source, config.Land.Displays, allowFallback)
			if err != nil {
				return Catalog{}, fmt.Errorf("evaluate %d/%d/%d: %w", id.Zoom, id.X, id.Y, err)
			}
			catalog.Stats.MaxDisplayRecords = max(catalog.Stats.MaxDisplayRecords, uint64(len(texture)+len(records)))
			outcome := maxZoomOutcome(id, texture)
			catalog.MaxZoomOutcomes = append(catalog.MaxZoomOutcomes, outcome)
			if outcome.Omitted {
				continue
			}
			blocksPerTile := tiledelivery.TileSize / tiledelivery.PointBlockSize
			for blockY := 0; blockY < blocksPerTile; blockY++ {
				for blockX := 0; blockX < blocksPerTile; blockX++ {
					pointID, err := tiledelivery.NewPointID(config.Tier, id.Zoom, int64(id.X)*int64(blocksPerTile)+int64(blockX), int64(id.Y)*int64(blocksPerTile)+int64(blockY))
					if err != nil {
						return Catalog{}, err
					}
					point, err := writer.Write(pointID, config.Period, dailyPointBlock(records, blockY, blockX))
					if err != nil {
						return Catalog{}, err
					}
					if point.Omitted {
						continue
					}
					pointCatalog, err := pointEntry(staging, point)
					if err != nil {
						return Catalog{}, err
					}
					catalog.Points = append(catalog.Points, pointCatalog)
				}
			}
			for _, field := range fields {
				scalarID, err := scalarID(config.Tier, field, id.Zoom, id.X, id.Y)
				if err != nil {
					return Catalog{}, err
				}
				var asset tiledelivery.ScalarAsset
				if allowFallback {
					asset, err = writer.WriteForecastScalar(scalarID, config.Period, textureSampler(id, texture, field))
				} else {
					asset, err = writer.WriteScalar(scalarID, config.Period, textureSampler(id, texture, field))
				}
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
			catalog.Stats.MaxScalarBytes = max(catalog.Stats.MaxScalarBytes, uint64(len(texture))*delivery.DailyPointRecordBytes+tiledelivery.ScalarTileBytes)
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
					lookup := func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) {
						asset, ok := levelAssets[id]
						return asset, ok
					}
					var parent *tiledelivery.ScalarParentSampler
					if config.Tier == "europe-detail" {
						parent, err = tiledelivery.NewForecastScalarParentSampler(id, lookup)
					} else {
						parent, err = tiledelivery.NewScalarParentSampler(id, lookup)
					}
					if err != nil {
						return Catalog{}, err
					}
					if !parent.HasInteriorData() {
						continue
					}
					var asset tiledelivery.ScalarAsset
					if config.Tier == "europe-detail" {
						asset, err = writer.WriteForecastScalar(id, config.Period, parent.Sample)
					} else {
						asset, err = writer.WriteScalar(id, config.Period, parent.Sample)
					}
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
	if config.Root == "" || !filepath.IsAbs(config.Root) || config.ReleaseID == "" || config.Period == "" || config.Tier == "" || config.Date.IsZero() || config.Locate == nil || ValidateLandIdentity(config.Land.Identity()) != nil || len(config.Series) == 0 {
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

func sourceSpec(series pipeline.PreparedSeries) (SourceIdentity, error) {
	var result delivery.GridSpec
	var resolution float64
	var sourceID string
	var run time.Time
	var evidence []preparedEvidence
	for field, frames := range series {
		if len(frames) == 0 {
			return SourceIdentity{}, fmt.Errorf("prepared field %s is empty", field)
		}
		for _, frame := range frames {
			metadata, err := frame.Metadata()
			if err != nil {
				return SourceIdentity{}, err
			}
			if metadata.Field != field {
				return SourceIdentity{}, fmt.Errorf("prepared series key %s contains %s", field, metadata.Field)
			}
			if metadata.SourceID == "" || metadata.Run.IsZero() || len(metadata.ContentSHA256) != sha256.Size*2 {
				return SourceIdentity{}, errors.New("forecast tile tier requires prepared source and run identity")
			}
			if _, err := hex.DecodeString(metadata.ContentSHA256); err != nil {
				return SourceIdentity{}, errors.New("forecast tile tier requires prepared content identity")
			}
			if result.Rows == 0 {
				result, resolution, sourceID, run = metadata.Grid, metadata.NativeResolutionKM, metadata.SourceID, metadata.Run
			} else if result != metadata.Grid || math.Abs(resolution-metadata.NativeResolutionKM) > 1e-9*math.Max(1, math.Max(resolution, metadata.NativeResolutionKM)) {
				return SourceIdentity{}, errors.New("one forecast tile tier requires one source grid and resolution")
			} else if metadata.SourceID != sourceID || !metadata.Run.Equal(run) {
				return SourceIdentity{}, errors.New("one forecast tile tier cannot mix provider products or runs")
			}
			evidence = append(evidence, preparedEvidenceFrom(metadata))
		}
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Field != evidence[j].Field {
			return evidence[i].Field < evidence[j].Field
		}
		if evidence[i].StartUTC != evidence[j].StartUTC {
			return evidence[i].StartUTC < evidence[j].StartUTC
		}
		if evidence[i].EndUTC != evidence[j].EndUTC {
			return evidence[i].EndUTC < evidence[j].EndUTC
		}
		if evidence[i].Semantics != evidence[j].Semantics {
			return evidence[i].Semantics < evidence[j].Semantics
		}
		if evidence[i].Units != evidence[j].Units {
			return evidence[i].Units < evidence[j].Units
		}
		if evidence[i].PackingError != evidence[j].PackingError {
			return evidence[i].PackingError < evidence[j].PackingError
		}
		if evidence[i].PackingQuantum != evidence[j].PackingQuantum {
			return evidence[i].PackingQuantum < evidence[j].PackingQuantum
		}
		return evidence[i].ContentSHA256 < evidence[j].ContentSHA256
	})
	envelope := struct {
		SourceID           string             `json:"sourceId"`
		RunUTC             string             `json:"runUtc"`
		Grid               delivery.GridSpec  `json:"grid"`
		NativeResolutionKM float64            `json:"nativeResolutionKm"`
		Frames             []preparedEvidence `json:"frames"`
	}{
		SourceID: sourceID, RunUTC: run.UTC().Format(time.RFC3339), Grid: result,
		NativeResolutionKM: resolution, Frames: evidence,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return SourceIdentity{}, err
	}
	digest := sha256.Sum256(encoded)
	return SourceIdentity{
		SourceID: sourceID, RunUTC: run.UTC().Format(time.RFC3339), Grid: result,
		NativeResolutionKM: resolution, EvidenceSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

type preparedEvidence struct {
	Field          pipeline.Field     `json:"field"`
	StartUTC       string             `json:"startUtc"`
	EndUTC         string             `json:"endUtc"`
	Semantics      pipeline.Semantics `json:"semantics"`
	Units          string             `json:"units"`
	PackingError   float64            `json:"packingError"`
	PackingQuantum float64            `json:"packingQuantum"`
	ContentSHA256  string             `json:"contentSha256"`
}

func preparedEvidenceFrom(metadata pipeline.PreparedFrameMetadata) preparedEvidence {
	return preparedEvidence{
		Field: metadata.Field, StartUTC: metadata.Start.UTC().Format(time.RFC3339Nano), EndUTC: metadata.End.UTC().Format(time.RFC3339Nano),
		Semantics: metadata.Semantics, Units: metadata.Units, PackingError: metadata.PackingError, PackingQuantum: metadata.PackingQuantum,
		ContentSHA256: metadata.ContentSHA256,
	}
}

func maxZoomOutcome(id tiledelivery.ID, texture []delivery.DailyPointRecord) MaxZoomOutcome {
	outcome := MaxZoomOutcome{Zoom: id.Zoom, X: id.X, Y: id.Y}
	for row := tiledelivery.ScalarGutter; row < tiledelivery.ScalarGutter+tiledelivery.TileSize; row++ {
		for column := tiledelivery.ScalarGutter; column < tiledelivery.ScalarGutter+tiledelivery.TileSize; column++ {
			record := texture[row*tiledelivery.ScalarTextureSize+column]
			switch {
			case record.Weather.Valid:
				outcome.DataRecords++
			case isFallbackRecord(record):
				outcome.FallbackRecords++
			default:
				outcome.NoDataRecords++
			}
		}
	}
	outcome.Omitted = outcome.DataRecords+outcome.FallbackRecords == 0
	return outcome
}

func dailyPointBlock(records []delivery.DailyPointRecord, blockY, blockX int) []delivery.DailyPointRecord {
	result := make([]delivery.DailyPointRecord, tiledelivery.PointBlockSize*tiledelivery.PointBlockSize)
	for row := 0; row < tiledelivery.PointBlockSize; row++ {
		source := (blockY*tiledelivery.PointBlockSize+row)*tiledelivery.TileSize + blockX*tiledelivery.PointBlockSize
		copy(result[row*tiledelivery.PointBlockSize:(row+1)*tiledelivery.PointBlockSize], records[source:source+tiledelivery.PointBlockSize])
	}
	return result
}
