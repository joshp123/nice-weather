// Package forecastcatalog describes one immutable provider tier for one local
// forecast day. It joins native forecast point blocks to evaluated scalar
// tiles without selecting providers, blending tiers or scoring in a client.
package forecastcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	CatalogVersion      = 3
	MaximumCatalogBytes = 1024 * 1024

	ProductIFSControl = "ecmwf-ifs-control-open-data-0.25-degree"
	ProductICONEU     = "dwd-icon-eu-open-data-0.0625-degree"

	ScoreUnit        = "canonical-score-0-100"
	ScoreNoData      = uint8(255)
	SharedField      = "shared"
	GridProjection   = "EPSG:4326"
	GridRegistration = "cell-centre"
)

var (
	releasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	datePattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Config binds source evidence that is not already carried by the point and
// scalar assets. SourceEvidenceSHA256 identifies the exact accepted source
// envelope for this provider run.
type Config struct {
	ReleaseID            string
	LocalDate            string
	Provider             string
	Tier                 string
	RunUTC               time.Time
	SourceEvidenceSHA256 string
}

type Grid struct {
	Projection       string  `json:"projection"`
	West             float64 `json:"west"`
	East             float64 `json:"east"`
	South            float64 `json:"south"`
	North            float64 `json:"north"`
	Rows             uint32  `json:"rows"`
	Columns          uint32  `json:"columns"`
	CellDegrees      float64 `json:"cellDegrees"`
	CellRegistration string  `json:"cellRegistration"`
}

type Evidence struct {
	Product              string  `json:"product"`
	RunUTC               string  `json:"runUtc"`
	NativeResolutionKM   float64 `json:"nativeResolutionKm"`
	SourceEvidenceSHA256 string  `json:"sourceEvidenceSha256"`
}

// FieldContract says which canonical profile owns each evaluated scalar.
// Shared component fields have profile "shared".
type FieldContract struct {
	ID      forecasttiles.Field `json:"id"`
	Profile string              `json:"profile"`
}

type Scores struct {
	Unit          string            `json:"unit"`
	NoDataValue   uint8             `json:"noDataValue"`
	FallbackValue *uint8            `json:"fallbackValue,omitempty"`
	Profiles      []weather.Profile `json:"profiles"`
	Fields        []FieldContract   `json:"fields"`
}

// PointBlock covers one required native block coordinate. Omitted is valid
// only when every record is no-data; in that case no file identity is present.
type PointBlock struct {
	Row           uint32 `json:"row"`
	Column        uint32 `json:"column"`
	Rows          uint16 `json:"rows"`
	Columns       uint16 `json:"columns"`
	Omitted       bool   `json:"omitted"`
	Path          string `json:"path,omitempty"`
	Bytes         uint64 `json:"bytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	PayloadSHA256 string `json:"payloadSha256,omitempty"`
	MediaType     string `json:"mediaType"`
	DataRecords   uint32 `json:"dataRecords"`
	NoDataRecords uint32 `json:"noDataRecords"`
}

type ScalarTile struct {
	Field        forecasttiles.Field `json:"field"`
	Zoom         uint8               `json:"zoom"`
	X            uint32              `json:"x"`
	Y            uint32              `json:"y"`
	Path         string              `json:"path"`
	Bytes        uint64              `json:"bytes"`
	SHA256       string              `json:"sha256"`
	MediaType    string              `json:"mediaType"`
	DataTexels   uint32              `json:"dataTexels"`
	NoDataTexels uint32              `json:"noDataTexels"`
}

// DailyPointBlock stores the exact physical weather, summary and evaluated
// scores behind one 64x64 region of the max-zoom scalar plane.
type DailyPointBlock struct {
	Zoom          uint8  `json:"zoom"`
	X             uint32 `json:"x"`
	Y             uint32 `json:"y"`
	Path          string `json:"path"`
	Bytes         uint64 `json:"bytes"`
	SHA256        string `json:"sha256"`
	PayloadSHA256 string `json:"payloadSha256"`
	MediaType     string `json:"mediaType"`
	DataRecords   uint32 `json:"dataRecords"`
	NoDataRecords uint32 `json:"noDataRecords"`
}

type Coverage struct {
	ExpectedPointBlocks   uint32 `json:"expectedPointBlocks"`
	AvailablePointBlocks  uint32 `json:"availablePointBlocks"`
	OmittedPointBlocks    uint32 `json:"omittedPointBlocks"`
	ExpectedMaxZoomTiles  uint32 `json:"expectedMaxZoomTiles"`
	AvailableMaxZoomTiles uint32 `json:"availableMaxZoomTiles"`
	OmittedMaxZoomTiles   uint32 `json:"omittedMaxZoomTiles"`
	ScalarTiles           uint32 `json:"scalarTiles"`
	DailyPointBlocks      uint32 `json:"dailyPointBlocks"`
}

type Catalog struct {
	Version          int                            `json:"version"`
	ReleaseID        string                         `json:"releaseId"`
	LocalDate        string                         `json:"localDate"`
	Provider         string                         `json:"provider"`
	Tier             string                         `json:"tier"`
	Grid             Grid                           `json:"grid"`
	Evidence         Evidence                       `json:"evidence"`
	Land             forecasttiles.LandIdentity     `json:"land"`
	ScalarMaxZoom    uint8                          `json:"scalarMaxZoom"`
	Scores           Scores                         `json:"scores"`
	Coverage         Coverage                       `json:"coverage"`
	PointBlocks      []PointBlock                   `json:"pointBlocks"`
	DailyPointBlocks []DailyPointBlock              `json:"dailyPointBlocks"`
	ScalarCoverage   []forecasttiles.MaxZoomOutcome `json:"scalarCoverage"`
	ScalarTiles      []ScalarTile                   `json:"scalarTiles"`
}

type tierSpec struct {
	provider, tier, product, sourceID string
	grid                              delivery.GridSpec
	nativeResolutionKM                float64
	maxZoom                           uint8
}

var tierSpecs = []tierSpec{
	{
		provider: forecastpoints.ProviderIFS, tier: forecastpoints.TierGlobal, product: ProductIFSControl, sourceID: "ecmwf-ifs-control",
		grid:               delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 720, Cols: 1440},
		nativeResolutionKM: 28, maxZoom: 3,
	},
	{
		provider: forecastpoints.ProviderICONEU, tier: forecastpoints.TierEurope, product: ProductICONEU, sourceID: "dwd-icon-eu",
		grid:               delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}, Rows: 680, Cols: 1104},
		nativeResolutionKM: 7, maxZoom: 5,
	},
}

var fieldContracts = []FieldContract{
	{ID: forecasttiles.FieldThermal, Profile: SharedField},
	{ID: forecasttiles.FieldDryness, Profile: SharedField},
	{ID: forecasttiles.FieldSun, Profile: SharedField},
	{ID: forecasttiles.FieldNiceScore, Profile: string(weather.ProfileNice)},
	{ID: forecasttiles.FieldNiceWind, Profile: string(weather.ProfileNice)},
	{ID: forecasttiles.FieldCyclingScore, Profile: string(weather.ProfileCycling)},
	{ID: forecasttiles.FieldCyclingWind, Profile: string(weather.ProfileCycling)},
}

// New assembles one catalogue from the complete native block outcome set and
// the scalar assets produced by forecasttiles.BuildDay.
func New(config Config, points []forecastpoints.Asset, scalarCatalog forecasttiles.Catalog) (Catalog, error) {
	spec, err := specFor(config.Provider, config.Tier)
	if err != nil {
		return Catalog{}, err
	}
	if err := validateConfig(config); err != nil {
		return Catalog{}, err
	}
	if scalarCatalog.Version != CatalogVersion || scalarCatalog.ReleaseID != config.ReleaseID || scalarCatalog.Period != config.LocalDate || scalarCatalog.Date != config.LocalDate || scalarCatalog.Tier != config.Tier || scalarCatalog.MaxZoom != spec.maxZoom {
		return Catalog{}, errors.New("forecast scalar catalogue does not match the requested release, day or tier")
	}
	if err := validateScalarSource(config, spec, scalarCatalog.Source); err != nil {
		return Catalog{}, err
	}

	blocks := make([]PointBlock, 0, len(points))
	for _, asset := range points {
		block, err := pointBlockFromAsset(config, spec, asset)
		if err != nil {
			return Catalog{}, err
		}
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Row != blocks[j].Row {
			return blocks[i].Row < blocks[j].Row
		}
		return blocks[i].Column < blocks[j].Column
	})

	tiles := make([]ScalarTile, 0, len(scalarCatalog.Scalars))
	for _, source := range scalarCatalog.Scalars {
		tile, err := scalarTileFromAsset(config, source)
		if err != nil {
			return Catalog{}, err
		}
		tiles = append(tiles, tile)
	}
	sortScalarTiles(tiles)
	dailyPointBlocks := make([]DailyPointBlock, 0, len(scalarCatalog.Points))
	for _, source := range scalarCatalog.Points {
		point, err := dailyPointBlockFromAsset(config, spec, source)
		if err != nil {
			return Catalog{}, err
		}
		dailyPointBlocks = append(dailyPointBlocks, point)
	}
	sort.Slice(dailyPointBlocks, func(i, j int) bool {
		if dailyPointBlocks[i].Y != dailyPointBlocks[j].Y {
			return dailyPointBlocks[i].Y < dailyPointBlocks[j].Y
		}
		return dailyPointBlocks[i].X < dailyPointBlocks[j].X
	})
	scalarCoverage := append([]forecasttiles.MaxZoomOutcome(nil), scalarCatalog.MaxZoomOutcomes...)
	sort.Slice(scalarCoverage, func(i, j int) bool {
		if scalarCoverage[i].Y != scalarCoverage[j].Y {
			return scalarCoverage[i].Y < scalarCoverage[j].Y
		}
		return scalarCoverage[i].X < scalarCoverage[j].X
	})
	if err := validateProducerPointCoverage(config, spec, scalarCatalog.Points, scalarCoverage); err != nil {
		return Catalog{}, err
	}

	catalog := Catalog{
		Version: CatalogVersion, ReleaseID: config.ReleaseID, LocalDate: config.LocalDate,
		Provider: config.Provider, Tier: config.Tier, Grid: catalogGrid(spec.grid),
		Evidence: Evidence{
			Product: spec.product, RunUTC: scalarCatalog.Source.RunUTC, NativeResolutionKM: scalarCatalog.Source.NativeResolutionKM,
			SourceEvidenceSHA256: scalarCatalog.Source.EvidenceSHA256,
		},
		Land:          scalarCatalog.Land,
		ScalarMaxZoom: spec.maxZoom,
		Scores: Scores{
			Unit: ScoreUnit, NoDataValue: ScoreNoData,
			Profiles: []weather.Profile{weather.ProfileNice, weather.ProfileCycling},
			Fields:   append([]FieldContract(nil), fieldContracts...),
		},
		PointBlocks: blocks, DailyPointBlocks: dailyPointBlocks, ScalarCoverage: scalarCoverage, ScalarTiles: tiles,
	}
	if config.Tier == forecastpoints.TierEurope {
		fallback := tiledelivery.ForecastScalarFallback
		catalog.Scores.FallbackValue = &fallback
	}
	catalog.Coverage = coverage(catalog)
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Marshal returns deterministic compact JSON and enforces the static-delivery
// size ceiling.
func Marshal(catalog Catalog) ([]byte, error) {
	if err := Validate(catalog); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaximumCatalogBytes {
		return nil, fmt.Errorf("forecast catalogue has %d bytes; maximum is %d", len(encoded), MaximumCatalogBytes)
	}
	return encoded, nil
}

// Parse decodes exact bounded JSON. Unknown fields and trailing values fail.
func Parse(encoded []byte) (Catalog, error) {
	if len(encoded) == 0 || len(encoded) > MaximumCatalogBytes {
		return Catalog{}, errors.New("forecast catalogue size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("forecast catalogue has trailing JSON")
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate checks the complete in-memory contract. It does not read files.
func Validate(catalog Catalog) error {
	spec, err := specFor(catalog.Provider, catalog.Tier)
	if err != nil {
		return err
	}
	config := Config{
		ReleaseID: catalog.ReleaseID, LocalDate: catalog.LocalDate, Provider: catalog.Provider, Tier: catalog.Tier,
		SourceEvidenceSHA256: catalog.Evidence.SourceEvidenceSHA256,
	}
	config.RunUTC, err = time.Parse(time.RFC3339, catalog.Evidence.RunUTC)
	if err != nil || validateConfig(config) != nil {
		return errors.New("forecast catalogue identity or source evidence is invalid")
	}
	if catalog.Version != CatalogVersion || catalog.Grid != catalogGrid(spec.grid) || catalog.Evidence.Product != spec.product || catalog.Evidence.NativeResolutionKM != spec.nativeResolutionKM || catalog.ScalarMaxZoom != spec.maxZoom {
		return errors.New("forecast catalogue grid, resolution or provider contract is invalid")
	}
	if err := forecasttiles.ValidateLandIdentity(catalog.Land); err != nil {
		return errors.New("forecast catalogue land provenance is invalid")
	}
	validFallback := catalog.Tier == forecastpoints.TierEurope && catalog.Scores.FallbackValue != nil && *catalog.Scores.FallbackValue == tiledelivery.ForecastScalarFallback ||
		catalog.Tier == forecastpoints.TierGlobal && catalog.Scores.FallbackValue == nil
	if catalog.Scores.Unit != ScoreUnit || catalog.Scores.NoDataValue != ScoreNoData || !validFallback ||
		!slices.Equal(catalog.Scores.Profiles, []weather.Profile{weather.ProfileNice, weather.ProfileCycling}) ||
		!slices.Equal(catalog.Scores.Fields, fieldContracts) {
		return errors.New("forecast catalogue score fields or profiles are invalid")
	}
	if err := validatePointBlocks(catalog, spec); err != nil {
		return err
	}
	if err := validateScalarCoverageOutcomes(catalog, spec); err != nil {
		return err
	}
	if err := validateDailyPointBlocks(catalog, spec); err != nil {
		return err
	}
	if err := validateScalarTiles(catalog, spec); err != nil {
		return err
	}
	if catalog.Coverage != coverage(catalog) {
		return errors.New("forecast catalogue coverage summary is invalid")
	}
	return nil
}

// ValidateFiles verifies every referenced immutable file against catalogue
// identity, byte count, media type, record coverage and SHA-256.
func ValidateFiles(root string, catalog Catalog) error {
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("forecast asset root must be absolute")
	}
	if err := Validate(catalog); err != nil {
		return err
	}
	spec, _ := specFor(catalog.Provider, catalog.Tier)
	for _, block := range catalog.PointBlocks {
		if block.Omitted {
			continue
		}
		identity, err := forecastpoints.NewIdentity(catalog.ReleaseID, catalog.Provider, catalog.Tier, catalog.LocalDate, catalog.Evidence.RunUTC, catalog.Evidence.SourceEvidenceSHA256, spec.grid, forecastpoints.Coordinate{Row: block.Row, Column: block.Column})
		if err != nil {
			return err
		}
		opened, err := forecastpoints.OpenBlock(filepath.Join(root, filepath.FromSlash(block.Path)), identity, block.SHA256)
		if err != nil {
			return fmt.Errorf("validate forecast point block %d/%d: %w", block.Row, block.Column, err)
		}
		metadata := opened.Metadata()
		if metadata.Bytes != block.Bytes || metadata.PayloadSHA256 != block.PayloadSHA256 || metadata.MediaType != block.MediaType || metadata.DataRecords != block.DataRecords || metadata.NoDataRecords != block.NoDataRecords {
			return fmt.Errorf("forecast point block %d/%d metadata does not match its file", block.Row, block.Column)
		}
	}
	for _, tile := range catalog.ScalarTiles {
		var stats tiledelivery.ScalarStats
		var err error
		if catalog.Tier == forecastpoints.TierEurope {
			_, stats, err = tiledelivery.ReadForecastScalar(filepath.Join(root, filepath.FromSlash(tile.Path)), tile.SHA256)
		} else {
			_, stats, err = tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(tile.Path)), tile.SHA256)
		}
		if err != nil {
			return fmt.Errorf("validate forecast scalar %s %d/%d/%d: %w", tile.Field, tile.Zoom, tile.X, tile.Y, err)
		}
		if tile.Bytes != tiledelivery.ScalarTileBytes || tile.MediaType != tiledelivery.ScalarMediaType || tile.DataTexels != stats.DataTexels || tile.NoDataTexels != stats.NoDataTexels {
			return fmt.Errorf("forecast scalar %s %d/%d/%d metadata does not match its file", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
	}
	for _, point := range catalog.DailyPointBlocks {
		reader, err := tiledelivery.OpenPoint(filepath.Join(root, filepath.FromSlash(point.Path)))
		if err != nil {
			return fmt.Errorf("validate forecast display point %d/%d/%d: %w", point.Zoom, point.X, point.Y, err)
		}
		metadata := reader.Metadata()
		closeErr := reader.Close()
		if closeErr != nil {
			return closeErr
		}
		if metadata.ID.Zoom != point.Zoom || metadata.ID.X != point.X || metadata.ID.Y != point.Y || metadata.ReleaseID != catalog.ReleaseID || metadata.Period != catalog.LocalDate || metadata.SHA256 != point.SHA256 || metadata.PayloadSHA256 != point.PayloadSHA256 || metadata.Bytes != point.Bytes || metadata.DataRecords != point.DataRecords || metadata.NoDataRecords != point.NoDataRecords {
			return fmt.Errorf("forecast daily point block %d/%d/%d metadata does not match its file", point.Zoom, point.X, point.Y)
		}
	}
	return nil
}

func dailyPointBlockFromAsset(config Config, spec tierSpec, source forecasttiles.PointTile) (DailyPointBlock, error) {
	wantPath := fmt.Sprintf("points/%s/%s/%d/%d/%d.nwt", config.LocalDate, config.Tier, source.Zoom, source.X, source.Y)
	count := (uint64(1) << source.Zoom) * (tiledelivery.TileSize / tiledelivery.PointBlockSize)
	if source.Zoom != spec.maxZoom || uint64(source.X) >= count || uint64(source.Y) >= count || source.Path != wantPath || source.Bytes != tiledelivery.PointBlockBytes || source.DataRecords == 0 || source.DataRecords+source.NoDataRecords != tiledelivery.PointBlockSize*tiledelivery.PointBlockSize || !validSHA256(source.SHA256) || !validSHA256(source.PayloadSHA256) {
		return DailyPointBlock{}, fmt.Errorf("forecast daily point block %d/%d/%d metadata is invalid", source.Zoom, source.X, source.Y)
	}
	return DailyPointBlock{
		Zoom: source.Zoom, X: source.X, Y: source.Y, Path: source.Path, Bytes: source.Bytes,
		SHA256: source.SHA256, PayloadSHA256: source.PayloadSHA256, MediaType: tiledelivery.PointMediaType,
		DataRecords: source.DataRecords, NoDataRecords: source.NoDataRecords,
	}, nil
}

func pointBlockFromAsset(config Config, spec tierSpec, asset forecastpoints.Asset) (PointBlock, error) {
	identity := asset.Identity
	if identity.ReleaseID != config.ReleaseID || identity.Provider != config.Provider || identity.Tier != config.Tier || identity.Date != config.LocalDate ||
		identity.RunUTC != config.RunUTC.Format(time.RFC3339) || identity.SourceEvidenceSHA256 != config.SourceEvidenceSHA256 || identity.Grid != spec.grid {
		return PointBlock{}, errors.New("forecast point block does not match the requested release, day or provider tier")
	}
	canonical, err := forecastpoints.NewIdentity(config.ReleaseID, config.Provider, config.Tier, config.LocalDate, config.RunUTC.Format(time.RFC3339), config.SourceEvidenceSHA256, spec.grid, identity.Block)
	if err != nil || canonical != identity {
		return PointBlock{}, errors.New("forecast point block identity is not canonical")
	}
	records := uint32(identity.Rows) * uint32(identity.Columns)
	relative, _ := forecastpoints.RelativePath(identity)
	block := PointBlock{
		Row: identity.Block.Row, Column: identity.Block.Column, Rows: identity.Rows, Columns: identity.Columns,
		Omitted: asset.Omitted, MediaType: asset.MediaType, DataRecords: asset.DataRecords, NoDataRecords: asset.NoDataRecords,
	}
	if asset.Omitted {
		if asset.Path != "" || asset.Bytes != 0 || asset.SHA256 != "" || asset.PayloadSHA256 != "" || asset.MediaType != forecastpoints.MediaType || asset.DataRecords != 0 || asset.NoDataRecords != records {
			return PointBlock{}, fmt.Errorf("omitted forecast point block %d/%d is not all no-data", block.Row, block.Column)
		}
		return block, nil
	}
	if !pathMatches(asset.Path, relative) || asset.MediaType != forecastpoints.MediaType || asset.DataRecords == 0 || asset.DataRecords+asset.NoDataRecords != records || !validSHA256(asset.SHA256) || !validSHA256(asset.PayloadSHA256) {
		return PointBlock{}, fmt.Errorf("forecast point block %d/%d metadata is invalid", block.Row, block.Column)
	}
	wantRange, _ := forecastpoints.WholeRange(identity)
	if asset.Bytes != uint64(wantRange.Length) {
		return PointBlock{}, fmt.Errorf("forecast point block %d/%d byte size is invalid", block.Row, block.Column)
	}
	block.Path, block.Bytes, block.SHA256, block.PayloadSHA256 = relative, asset.Bytes, asset.SHA256, asset.PayloadSHA256
	return block, nil
}

func scalarTileFromAsset(config Config, source forecasttiles.ScalarTile) (ScalarTile, error) {
	if fieldRank(source.Field) < 0 || source.Bytes != tiledelivery.ScalarTileBytes || !validSHA256(source.SHA256) || source.DataTexels == 0 || uint64(source.DataTexels)+uint64(source.NoDataTexels) != tiledelivery.ScalarTileBytes {
		return ScalarTile{}, fmt.Errorf("forecast scalar %s %d/%d/%d metadata is invalid", source.Field, source.Zoom, source.X, source.Y)
	}
	wantPath, err := scalarPath(config.LocalDate, config.Tier, source.Field, source.Zoom, source.X, source.Y)
	if err != nil || source.Path != wantPath {
		return ScalarTile{}, fmt.Errorf("forecast scalar %s %d/%d/%d path is invalid", source.Field, source.Zoom, source.X, source.Y)
	}
	return ScalarTile{
		Field: source.Field, Zoom: source.Zoom, X: source.X, Y: source.Y, Path: source.Path,
		Bytes: source.Bytes, SHA256: source.SHA256, MediaType: tiledelivery.ScalarMediaType,
		DataTexels: source.DataTexels, NoDataTexels: source.NoDataTexels,
	}, nil
}

func validatePointBlocks(catalog Catalog, spec tierSpec) error {
	rows, columns, err := forecastpoints.BlockGrid(spec.provider, spec.tier, spec.grid)
	if err != nil || len(catalog.PointBlocks) != int(rows*columns) {
		return fmt.Errorf("forecast catalogue has %d point blocks; want %d", len(catalog.PointBlocks), rows*columns)
	}
	available := 0
	for row := uint32(0); row < rows; row++ {
		for column := uint32(0); column < columns; column++ {
			index := int(row*columns + column)
			block := catalog.PointBlocks[index]
			identity, err := forecastpoints.NewIdentity(catalog.ReleaseID, catalog.Provider, catalog.Tier, catalog.LocalDate, catalog.Evidence.RunUTC, catalog.Evidence.SourceEvidenceSHA256, spec.grid, forecastpoints.Coordinate{Row: row, Column: column})
			if err != nil {
				return err
			}
			records := uint32(identity.Rows) * uint32(identity.Columns)
			if block.Row != row || block.Column != column || block.Rows != identity.Rows || block.Columns != identity.Columns || block.MediaType != forecastpoints.MediaType || block.DataRecords+block.NoDataRecords != records {
				return fmt.Errorf("forecast point block at index %d does not match expected coordinate %d/%d", index, row, column)
			}
			if block.Omitted {
				if block.Path != "" || block.Bytes != 0 || block.SHA256 != "" || block.PayloadSHA256 != "" || block.DataRecords != 0 || block.NoDataRecords != records {
					return fmt.Errorf("omitted forecast point block %d/%d is not all no-data", row, column)
				}
				continue
			}
			available++
			relative, _ := forecastpoints.RelativePath(identity)
			whole, _ := forecastpoints.WholeRange(identity)
			if block.Path != relative || block.Bytes != uint64(whole.Length) || !validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) || block.DataRecords == 0 {
				return fmt.Errorf("forecast point block %d/%d file identity is invalid", row, column)
			}
		}
	}
	if available == 0 {
		return errors.New("forecast catalogue has no available native point blocks")
	}
	return nil
}

func validateScalarSource(config Config, spec tierSpec, source forecasttiles.SourceIdentity) error {
	if source.SourceID != spec.sourceID || source.RunUTC != config.RunUTC.Format(time.RFC3339) || source.Grid != spec.grid ||
		source.NativeResolutionKM != spec.nativeResolutionKM || source.EvidenceSHA256 != config.SourceEvidenceSHA256 || !validSHA256(source.EvidenceSHA256) {
		return errors.New("forecast scalar source does not match the requested provider, run, grid, resolution or evidence")
	}
	return nil
}

func validateScalarCoverageOutcomes(catalog Catalog, spec tierSpec) error {
	expected := supportedMaxZoomCoordinates(spec)
	if len(catalog.ScalarCoverage) != len(expected) {
		return fmt.Errorf("forecast scalar coverage has %d max-zoom outcomes; want %d", len(catalog.ScalarCoverage), len(expected))
	}
	for index, coordinate := range expected {
		outcome := catalog.ScalarCoverage[index]
		if outcome.Zoom != spec.maxZoom || outcome.X != coordinate.X || outcome.Y != coordinate.Y ||
			uint64(outcome.DataRecords)+uint64(outcome.FallbackRecords)+uint64(outcome.NoDataRecords) != tiledelivery.TileSize*tiledelivery.TileSize ||
			(outcome.Omitted && (outcome.DataRecords != 0 || outcome.FallbackRecords != 0 || outcome.NoDataRecords != tiledelivery.TileSize*tiledelivery.TileSize)) ||
			(!outcome.Omitted && outcome.DataRecords+outcome.FallbackRecords == 0) ||
			(catalog.Tier != forecastpoints.TierEurope && outcome.FallbackRecords != 0) {
			return fmt.Errorf("forecast scalar coverage outcome at index %d is invalid", index)
		}
	}
	return nil
}

func validateDailyPointBlocks(catalog Catalog, spec tierSpec) error {
	seen := make(map[tileCoordinate]struct{}, len(catalog.DailyPointBlocks))
	dataByTile := make(map[tileCoordinate]uint32)
	expectedByTile := make(map[tileCoordinate]uint32, len(catalog.ScalarCoverage))
	blocksPerTile := uint32(tiledelivery.TileSize / tiledelivery.PointBlockSize)
	for index, point := range catalog.DailyPointBlocks {
		coordinate := tileCoordinate{Zoom: point.Zoom, X: point.X, Y: point.Y}
		wantPath := fmt.Sprintf("points/%s/%s/%d/%d/%d.nwt", catalog.LocalDate, catalog.Tier, point.Zoom, point.X, point.Y)
		count := (uint64(1) << point.Zoom) * uint64(blocksPerTile)
		if point.Zoom != spec.maxZoom || uint64(point.X) >= count || uint64(point.Y) >= count || point.Path != wantPath || point.Bytes != tiledelivery.PointBlockBytes || point.MediaType != tiledelivery.PointMediaType || point.DataRecords == 0 || point.DataRecords+point.NoDataRecords != tiledelivery.PointBlockSize*tiledelivery.PointBlockSize || !validSHA256(point.SHA256) || !validSHA256(point.PayloadSHA256) {
			return fmt.Errorf("forecast daily point block at index %d is invalid", index)
		}
		if _, duplicate := seen[coordinate]; duplicate {
			return fmt.Errorf("duplicate forecast daily point block %d/%d/%d", point.Zoom, point.X, point.Y)
		}
		seen[coordinate] = struct{}{}
		parent := tileCoordinate{Zoom: point.Zoom, X: point.X / blocksPerTile, Y: point.Y / blocksPerTile}
		dataByTile[parent] += point.DataRecords
	}
	for _, outcome := range catalog.ScalarCoverage {
		coordinate := tileCoordinate{Zoom: outcome.Zoom, X: outcome.X, Y: outcome.Y}
		if outcome.DataRecords > 0 {
			expectedByTile[coordinate] = outcome.DataRecords
		}
	}
	if len(catalog.DailyPointBlocks) == 0 && len(expectedByTile) != 0 {
		return errors.New("forecast catalogue has no daily point blocks")
	}
	if !maps.Equal(dataByTile, expectedByTile) {
		return errors.New("forecast daily point blocks do not exactly match scalar coverage")
	}
	return nil
}

func validateProducerPointCoverage(config Config, spec tierSpec, points []forecasttiles.PointTile, outcomes []forecasttiles.MaxZoomOutcome) error {
	produced := make(map[tileCoordinate]uint32, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.DataRecords > 0 {
			produced[tileCoordinate{Zoom: outcome.Zoom, X: outcome.X, Y: outcome.Y}] = outcome.DataRecords
		}
	}
	seen := make(map[tileCoordinate]struct{}, len(points))
	actual := make(map[tileCoordinate]uint32, len(outcomes))
	blocksPerTile := uint32(tiledelivery.TileSize / tiledelivery.PointBlockSize)
	for _, point := range points {
		coordinate := tileCoordinate{Zoom: point.Zoom, X: point.X, Y: point.Y}
		wantPath := fmt.Sprintf("points/%s/%s/%d/%d/%d.nwt", config.LocalDate, config.Tier, point.Zoom, point.X, point.Y)
		if point.Zoom != spec.maxZoom || point.Path != wantPath || point.Bytes != tiledelivery.PointBlockBytes || point.DataRecords == 0 || point.DataRecords+point.NoDataRecords != tiledelivery.PointBlockSize*tiledelivery.PointBlockSize || !validSHA256(point.SHA256) || !validSHA256(point.PayloadSHA256) {
			return fmt.Errorf("forecast scalar producer point %d/%d/%d metadata is invalid", point.Zoom, point.X, point.Y)
		}
		if _, duplicate := seen[coordinate]; duplicate {
			return fmt.Errorf("duplicate forecast scalar producer point %d/%d/%d", point.Zoom, point.X, point.Y)
		}
		seen[coordinate] = struct{}{}
		actual[tileCoordinate{Zoom: point.Zoom, X: point.X / blocksPerTile, Y: point.Y / blocksPerTile}] += point.DataRecords
	}
	if !maps.Equal(actual, produced) {
		return errors.New("forecast scalar producer points do not exactly match produced max-zoom outcomes")
	}
	return nil
}

type tileCoordinate struct {
	Zoom uint8
	X    uint32
	Y    uint32
}

func validateScalarTiles(catalog Catalog, spec tierSpec) error {
	if len(catalog.ScalarTiles) == 0 || len(catalog.ScalarTiles) > maximumScalarTiles(spec) {
		return errors.New("forecast catalogue scalar tile count is invalid")
	}
	if !sort.SliceIsSorted(catalog.ScalarTiles, func(i, j int) bool { return scalarLess(catalog.ScalarTiles[i], catalog.ScalarTiles[j]) }) {
		return errors.New("forecast catalogue scalar tiles are not in canonical order")
	}
	coverageByField := make(map[forecasttiles.Field]map[tileCoordinate]struct{}, len(fieldContracts))
	for _, field := range fieldContracts {
		coverageByField[field.ID] = make(map[tileCoordinate]struct{})
	}
	for _, tile := range catalog.ScalarTiles {
		fieldCoverage, ok := coverageByField[tile.Field]
		count := uint64(1) << tile.Zoom
		wantPath, pathErr := scalarPath(catalog.LocalDate, catalog.Tier, tile.Field, tile.Zoom, tile.X, tile.Y)
		if !ok || tile.Zoom > spec.maxZoom || uint64(tile.X) >= count || uint64(tile.Y) >= count || pathErr != nil || tile.Path != wantPath || tile.Bytes != tiledelivery.ScalarTileBytes || tile.MediaType != tiledelivery.ScalarMediaType || !validSHA256(tile.SHA256) || tile.DataTexels == 0 || uint64(tile.DataTexels)+uint64(tile.NoDataTexels) != tiledelivery.ScalarTileBytes || !tileIntersectsGrid(tileCoordinate{tile.Zoom, tile.X, tile.Y}, spec.grid) {
			return fmt.Errorf("forecast scalar %s %d/%d/%d metadata is invalid", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
		coordinate := tileCoordinate{Zoom: tile.Zoom, X: tile.X, Y: tile.Y}
		if _, duplicate := fieldCoverage[coordinate]; duplicate {
			return fmt.Errorf("duplicate forecast scalar %s %d/%d/%d", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
		fieldCoverage[coordinate] = struct{}{}
	}
	baseline := coverageByField[fieldContracts[0].ID]
	if len(baseline) == 0 {
		return errors.New("forecast scalar pyramid is empty")
	}
	for _, field := range fieldContracts[1:] {
		if !sameCoordinates(baseline, coverageByField[field.ID]) {
			return fmt.Errorf("forecast scalar field %s does not have complete common coverage", field.ID)
		}
	}
	wantCoverage := make(map[tileCoordinate]struct{})
	for _, outcome := range catalog.ScalarCoverage {
		if outcome.Omitted {
			continue
		}
		coordinate := tileCoordinate{Zoom: outcome.Zoom, X: outcome.X, Y: outcome.Y}
		for {
			wantCoverage[coordinate] = struct{}{}
			if coordinate.Zoom == 0 {
				break
			}
			coordinate = tileCoordinate{Zoom: coordinate.Zoom - 1, X: coordinate.X / 2, Y: coordinate.Y / 2}
		}
	}
	if !sameCoordinates(baseline, wantCoverage) {
		return errors.New("forecast scalar pyramid does not exactly match its max-zoom production outcomes")
	}
	return nil
}

func validateConfig(config Config) error {
	if !validIdentifier(config.ReleaseID) || !validDate(config.LocalDate) || !validSHA256(config.SourceEvidenceSHA256) {
		return errors.New("forecast catalogue config identity or evidence is invalid")
	}
	if _, err := specFor(config.Provider, config.Tier); err != nil {
		return err
	}
	if config.RunUTC.IsZero() || config.RunUTC.Location() != time.UTC || config.RunUTC.Nanosecond() != 0 {
		return errors.New("forecast catalogue run must be an exact UTC second")
	}
	return nil
}

func specFor(provider, tier string) (tierSpec, error) {
	for _, spec := range tierSpecs {
		if spec.provider == provider && spec.tier == tier {
			return spec, nil
		}
	}
	return tierSpec{}, errors.New("forecast catalogue provider and tier combination is unsupported")
}

func catalogGrid(grid delivery.GridSpec) Grid {
	return Grid{
		Projection: GridProjection, West: grid.Bounds.West, East: grid.Bounds.East, South: grid.Bounds.South, North: grid.Bounds.North,
		Rows: grid.Rows, Columns: grid.Cols, CellDegrees: (grid.Bounds.East - grid.Bounds.West) / float64(grid.Cols), CellRegistration: GridRegistration,
	}
}

func coverage(catalog Catalog) Coverage {
	result := Coverage{
		ExpectedPointBlocks: uint32(len(catalog.PointBlocks)), ExpectedMaxZoomTiles: uint32(len(catalog.ScalarCoverage)),
		ScalarTiles: uint32(len(catalog.ScalarTiles)), DailyPointBlocks: uint32(len(catalog.DailyPointBlocks)),
	}
	for _, block := range catalog.PointBlocks {
		if block.Omitted {
			result.OmittedPointBlocks++
		} else {
			result.AvailablePointBlocks++
		}
	}
	for _, outcome := range catalog.ScalarCoverage {
		if outcome.Omitted {
			result.OmittedMaxZoomTiles++
		} else {
			result.AvailableMaxZoomTiles++
		}
	}
	return result
}

func scalarPath(period, tier string, field forecasttiles.Field, zoom uint8, x, y uint32) (string, error) {
	suffix := map[forecasttiles.Field]string{
		forecasttiles.FieldThermal: ".th", forecasttiles.FieldDryness: ".dr", forecasttiles.FieldSun: ".su",
		forecasttiles.FieldNiceScore: ".ns", forecasttiles.FieldNiceWind: ".nw",
		forecasttiles.FieldCyclingScore: ".cs", forecasttiles.FieldCyclingWind: ".cw",
	}[field]
	if suffix == "" {
		return "", fmt.Errorf("unknown forecast scalar field %q", field)
	}
	return fmt.Sprintf("scores/%s/%s%s/%d/%d/%d.nws", period, tier, suffix, zoom, x, y), nil
}

func sortScalarTiles(tiles []ScalarTile) {
	sort.Slice(tiles, func(i, j int) bool { return scalarLess(tiles[i], tiles[j]) })
}

func scalarLess(a, b ScalarTile) bool {
	if fieldRank(a.Field) != fieldRank(b.Field) {
		return fieldRank(a.Field) < fieldRank(b.Field)
	}
	if a.Zoom != b.Zoom {
		return a.Zoom < b.Zoom
	}
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	return a.X < b.X
}

func fieldRank(field forecasttiles.Field) int {
	for index, contract := range fieldContracts {
		if contract.ID == field {
			return index
		}
	}
	return -1
}

func sameCoordinates(left, right map[tileCoordinate]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for coordinate := range left {
		if _, ok := right[coordinate]; !ok {
			return false
		}
	}
	return true
}

func tileIntersectsGrid(coordinate tileCoordinate, grid delivery.GridSpec) bool {
	id, err := tiledelivery.NewID("coverage", coordinate.Zoom, int64(coordinate.X), int64(coordinate.Y))
	if err != nil {
		return false
	}
	bounds, err := id.Bounds()
	if err != nil {
		return false
	}
	return bounds.East > grid.Bounds.West && bounds.West < grid.Bounds.East && bounds.North > grid.Bounds.South && bounds.South < grid.Bounds.North
}

func maximumScalarTiles(spec tierSpec) int {
	coverage := make(map[tileCoordinate]struct{})
	for _, coordinate := range supportedMaxZoomCoordinates(spec) {
		if !tileIntersectsGrid(coordinate, spec.grid) {
			continue
		}
		for {
			coverage[coordinate] = struct{}{}
			if coordinate.Zoom == 0 {
				break
			}
			coordinate = tileCoordinate{Zoom: coordinate.Zoom - 1, X: coordinate.X / 2, Y: coordinate.Y / 2}
		}
	}
	return len(coverage) * len(fieldContracts)
}

func supportedMaxZoomCoordinates(spec tierSpec) []tileCoordinate {
	count := uint32(1) << spec.maxZoom
	coordinates := make([]tileCoordinate, 0, count*count)
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			coordinate := tileCoordinate{Zoom: spec.maxZoom, X: x, Y: y}
			if tileSourceSamplesIntersect(coordinate, spec.grid) {
				coordinates = append(coordinates, coordinate)
			}
		}
	}
	return coordinates
}

// tileSourceSamplesIntersect mirrors the scalar producer's one-real-neighbour
// gutter when it decides whether a max-zoom tile intersects a regional grid.
func tileSourceSamplesIntersect(coordinate tileCoordinate, grid delivery.GridSpec) bool {
	worldPixels := int64(tiledelivery.TileSize) * (int64(1) << coordinate.Zoom)
	westPixel := int64(coordinate.X) * tiledelivery.TileSize
	northPixel := int64(coordinate.Y) * tiledelivery.TileSize
	worldwide := grid.Bounds.West == -180 && grid.Bounds.East == 180
	longitudeIntersects := worldwide
	for textureX := 0; !longitudeIntersects && textureX < tiledelivery.ScalarTextureSize; textureX++ {
		globalX := westPixel + int64(textureX-tiledelivery.ScalarGutter)
		longitude := (float64(globalX)+0.5)/float64(worldPixels)*360 - 180
		for longitude < -180 {
			longitude += 360
		}
		for longitude >= 180 {
			longitude -= 360
		}
		longitudeIntersects = longitude >= grid.Bounds.West && longitude < grid.Bounds.East
	}
	if !longitudeIntersects {
		return false
	}
	for textureY := 0; textureY < tiledelivery.ScalarTextureSize; textureY++ {
		globalY := northPixel + int64(textureY-tiledelivery.ScalarGutter)
		if globalY < 0 || globalY >= worldPixels {
			continue
		}
		y := (float64(globalY) + 0.5) / float64(worldPixels)
		latitude := math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
		if latitude > grid.Bounds.South && latitude <= grid.Bounds.North {
			return true
		}
	}
	return false
}

func pathMatches(actual, relative string) bool {
	if actual == "" {
		return false
	}
	clean := filepath.Clean(actual)
	want := filepath.Clean(filepath.FromSlash(relative))
	return clean == want || strings.HasSuffix(clean, string(filepath.Separator)+want)
}

func validIdentifier(value string) bool {
	return len(value) <= 64 && releasePattern.MatchString(value)
}

func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
