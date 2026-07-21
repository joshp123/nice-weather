// Package climatetiles owns the standalone worldwide climate release
// catalogue and publication pointer. It does not depend on the main weather
// manifest or its publisher.
package climatetiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

const (
	CatalogVersion = 1
	MaximumZoom    = 4

	CatalogFilename = "catalog.json"

	SourceDataset                 = "AgERA5"
	SourceVersion                 = "2.0"
	SourceKind                    = "topography-corrected-interpolated-era5"
	SourceLocalDay                = "agera5-eight-longitude-zones-at-three-hour-offsets-not-civil-time"
	SourceLandMask                = "natural-earth-5.1.1-centre-on-land"
	ProbabilityUnit               = "whole-percent"
	ProbabilityDenominator        = "complete-real-days-in-reference-month"
	ProbabilityThreshold          = "canonical-rating-good-or-better"
	ProbabilityGoodMinimumScore   = 55
	ProbabilityNoDataValue        = 255
	DeliveryGridResolutionDegrees = 0.1
	AtmosphericEvidenceKilometres = 30
	PrecipitationEvidenceDegrees  = 0.25
	maximumCatalogBytes           = 32 * 1024 * 1024
	maximumPointBlocks            = 20_000
	maximumScalarTiles            = 20_000
)

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	periodPattern    = regexp.MustCompile(`^month-(0[1-9]|1[0-2])$`)
)

type Field string

const (
	FieldComfortGood     Field = "comfort_good"
	FieldDryGood         Field = "dry_good"
	FieldBrightGood      Field = "bright_good"
	FieldNiceGood        Field = "nice_good"
	FieldNiceCalmGood    Field = "nice_calm_good"
	FieldCyclingGood     Field = "cycling_good"
	FieldCyclingCalmGood Field = "cycling_calm_good"
)

var fields = []Field{
	FieldComfortGood,
	FieldDryGood,
	FieldBrightGood,
	FieldNiceGood,
	FieldNiceCalmGood,
	FieldCyclingGood,
	FieldCyclingCalmGood,
}

// Fields returns the seven scalar products in canonical catalogue order.
func Fields() []Field { return append([]Field(nil), fields...) }

type Geometry struct {
	Projection            string  `json:"projection"`
	West                  float64 `json:"west"`
	East                  float64 `json:"east"`
	South                 float64 `json:"south"`
	North                 float64 `json:"north"`
	Rows                  uint32  `json:"rows"`
	Columns               uint32  `json:"columns"`
	CellResolutionDegrees float64 `json:"cellResolutionDegrees"`
	CellRegistration      string  `json:"cellRegistration"`
}

type ReferencePeriod struct {
	FirstYear int `json:"firstYear"`
	LastYear  int `json:"lastYear"`
}

type Source struct {
	Dataset                      string          `json:"dataset"`
	Version                      string          `json:"version"`
	Kind                         string          `json:"kind"`
	ReferencePeriod              ReferencePeriod `json:"referencePeriod"`
	DeliveryGridDegrees          float64         `json:"deliveryGridDegrees"`
	AtmosphericEvidenceKM        int             `json:"atmosphericEvidenceKm"`
	PrecipitationEvidenceDegrees float64         `json:"precipitationEvidenceDegrees"`
	LocalDay                     string          `json:"localDay"`
	LandMask                     string          `json:"landMask"`
}

type ProbabilitySemantics struct {
	Unit             string  `json:"unit"`
	Denominator      string  `json:"denominator"`
	Threshold        string  `json:"threshold"`
	GoodMinimumScore uint8   `json:"goodMinimumScore"`
	NoDataValue      uint8   `json:"noDataValue"`
	Fields           []Field `json:"fields"`
}

// Provenance binds a release to the checkpoint identities accepted by the
// worldwide month builder. The builder supplies both complete-set digests.
type Provenance struct {
	SourceEvidenceSHA256 string `json:"sourceEvidenceSha256"`
	LandMaskSetSHA256    string `json:"landMaskSetSha256"`
	DerivationContract   string `json:"derivationContract"`
}

type PointBlock struct {
	LatitudeBlock  int    `json:"latitudeBlock"`
	LongitudeBlock int    `json:"longitudeBlock"`
	LatitudeCount  uint16 `json:"latitudeCount"`
	LongitudeCount uint16 `json:"longitudeCount"`
	Path           string `json:"path"`
	Bytes          uint64 `json:"bytes"`
	SHA256         string `json:"sha256"`
	PayloadSHA256  string `json:"payloadSha256"`
	MediaType      string `json:"mediaType"`
	DataRecords    uint16 `json:"dataRecords"`
	NoDataRecords  uint16 `json:"noDataRecords"`
}

type ScalarTile struct {
	Field        Field  `json:"field"`
	Zoom         uint8  `json:"zoom"`
	X            uint32 `json:"x"`
	Y            uint32 `json:"y"`
	Path         string `json:"path"`
	Bytes        uint64 `json:"bytes"`
	SHA256       string `json:"sha256"`
	MediaType    string `json:"mediaType"`
	DataTexels   uint32 `json:"dataTexels"`
	NoDataTexels uint32 `json:"noDataTexels"`
}

// Catalog describes one complete month. Point blocks retain native 0.1
// degree evidence; scalar tiles carry seven evaluated whole-percent fields.
type Catalog struct {
	Version     int                  `json:"version"`
	ReleaseID   string               `json:"releaseId"`
	Period      string               `json:"period"`
	Geometry    Geometry             `json:"geometry"`
	Source      Source               `json:"source"`
	Provenance  Provenance           `json:"provenance"`
	Probability ProbabilitySemantics `json:"probability"`
	MaximumZoom uint8                `json:"maximumZoom"`
	PointBlocks []PointBlock         `json:"pointBlocks"`
	ScalarTiles []ScalarTile         `json:"scalarTiles"`
}

// NewCatalog applies the one current worldwide climate contract and sorts the
// supplied assets into canonical order. Validation still checks every asset.
func NewCatalog(releaseID, period string, provenance Provenance, pointBlocks []PointBlock, scalarTiles []ScalarTile) Catalog {
	grid := climatebuild.WorldwideGridSpec()
	window := climate.DefaultReferenceWindow()
	catalog := Catalog{
		Version:   CatalogVersion,
		ReleaseID: releaseID,
		Period:    period,
		Geometry: Geometry{
			Projection: "EPSG:4326", West: grid.Bounds.West, East: grid.Bounds.East,
			South: grid.Bounds.South, North: grid.Bounds.North,
			Rows: grid.Rows, Columns: grid.Cols,
			CellResolutionDegrees: DeliveryGridResolutionDegrees,
			CellRegistration:      "centre",
		},
		Source: Source{
			Dataset: SourceDataset, Version: SourceVersion, Kind: SourceKind,
			ReferencePeriod:              ReferencePeriod{FirstYear: window.FirstYear, LastYear: window.LastYear},
			DeliveryGridDegrees:          DeliveryGridResolutionDegrees,
			AtmosphericEvidenceKM:        AtmosphericEvidenceKilometres,
			PrecipitationEvidenceDegrees: PrecipitationEvidenceDegrees,
			LocalDay:                     SourceLocalDay, LandMask: SourceLandMask,
		},
		Provenance: provenance,
		Probability: ProbabilitySemantics{
			Unit: ProbabilityUnit, Denominator: ProbabilityDenominator,
			Threshold: ProbabilityThreshold, GoodMinimumScore: ProbabilityGoodMinimumScore,
			NoDataValue: ProbabilityNoDataValue, Fields: Fields(),
		},
		MaximumZoom: MaximumZoom,
		PointBlocks: append([]PointBlock(nil), pointBlocks...),
		ScalarTiles: append([]ScalarTile(nil), scalarTiles...),
	}
	sort.Slice(catalog.PointBlocks, func(i, j int) bool {
		a, b := catalog.PointBlocks[i], catalog.PointBlocks[j]
		if a.LatitudeBlock != b.LatitudeBlock {
			return a.LatitudeBlock < b.LatitudeBlock
		}
		return a.LongitudeBlock < b.LongitudeBlock
	})
	sort.Slice(catalog.ScalarTiles, func(i, j int) bool {
		a, b := catalog.ScalarTiles[i], catalog.ScalarTiles[j]
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
	})
	return catalog
}

func PointBlockPath(period string, block agera5.BlockCoordinate) string {
	return fmt.Sprintf("points/%s/%03d/%03d.nwc", period, block.Latitude, block.Longitude)
}

func ScalarTilePath(period string, field Field, zoom uint8, x, y uint32) string {
	return fmt.Sprintf("scores/%s/%s/%d/%d/%d.nws", period, field, zoom, x, y)
}

// PointBlockFromAsset converts a completed native block to its catalogue
// entry without making a second copy of its integrity metadata.
func PointBlockFromAsset(releaseRoot string, asset tiledelivery.ClimateBlockAsset) (PointBlock, error) {
	if asset.Omitted || asset.Path == "" {
		return PointBlock{}, errors.New("omitted climate point block has no catalogue entry")
	}
	if filepath.Base(filepath.Clean(releaseRoot)) != asset.Identity.ReleaseID {
		return PointBlock{}, errors.New("climate point block release does not match its release root")
	}
	relative, err := releaseAssetPath(releaseRoot, asset.Path)
	if err != nil {
		return PointBlock{}, err
	}
	wantPath := PointBlockPath(asset.Identity.Period, asset.Identity.Block)
	if relative != wantPath {
		return PointBlock{}, fmt.Errorf("climate point block path %q; want %q", relative, wantPath)
	}
	return PointBlock{
		LatitudeBlock: asset.Identity.Block.Latitude, LongitudeBlock: asset.Identity.Block.Longitude,
		LatitudeCount: asset.Identity.LatitudeCount, LongitudeCount: asset.Identity.LongitudeCount,
		Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, PayloadSHA256: asset.PayloadSHA256,
		MediaType: asset.MediaType, DataRecords: asset.DataRecords, NoDataRecords: asset.NoDataRecords,
	}, nil
}

// ScalarTileFromAsset converts one completed evaluated field tile to its
// catalogue entry. The field must also be the writer tier, so file identity
// and catalogue meaning cannot diverge.
func ScalarTileFromAsset(releaseRoot string, field Field, asset tiledelivery.ScalarAsset) (ScalarTile, error) {
	if asset.Omitted || asset.Path == "" {
		return ScalarTile{}, errors.New("omitted climate scalar has no catalogue entry")
	}
	if filepath.Base(filepath.Clean(releaseRoot)) != asset.ReleaseID {
		return ScalarTile{}, errors.New("climate scalar release does not match its release root")
	}
	relative, err := releaseAssetPath(releaseRoot, asset.Path)
	if err != nil {
		return ScalarTile{}, err
	}
	wantPath := ScalarTilePath(asset.Period, field, asset.ID.Zoom, asset.ID.X, asset.ID.Y)
	if asset.ID.Tier != string(field) || relative != wantPath {
		return ScalarTile{}, fmt.Errorf("climate scalar path or tier does not match field %q", field)
	}
	return ScalarTile{
		Field: field, Zoom: asset.ID.Zoom, X: asset.ID.X, Y: asset.ID.Y,
		Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256, MediaType: asset.MediaType,
		DataTexels: asset.DataTexels, NoDataTexels: asset.NoDataTexels,
	}, nil
}

// ValidateRelease checks the exact catalogue, every listed asset and the
// complete on-disk tree. It rejects symlinks and unlisted files or directories.
func ValidateRelease(root string) (Catalog, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return Catalog{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Catalog{}, errors.New("climate release root must be a real directory")
	}
	payload, err := readRegularBounded(filepath.Join(root, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, fmt.Errorf("read climate catalog: %w", err)
	}
	var catalog Catalog
	if err := decodeExactJSON(payload, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode climate catalog: %w", err)
	}
	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, err
	}

	expectedFiles := map[string]struct{}{CatalogFilename: {}}
	for _, block := range catalog.PointBlocks {
		if _, duplicate := expectedFiles[block.Path]; duplicate {
			return Catalog{}, fmt.Errorf("duplicate climate asset path %q", block.Path)
		}
		expectedFiles[block.Path] = struct{}{}
	}
	for _, tile := range catalog.ScalarTiles {
		if _, duplicate := expectedFiles[tile.Path]; duplicate {
			return Catalog{}, fmt.Errorf("duplicate climate asset path %q", tile.Path)
		}
		expectedFiles[tile.Path] = struct{}{}
	}
	if err := validateExactTree(root, expectedFiles); err != nil {
		return Catalog{}, err
	}
	for _, block := range catalog.PointBlocks {
		identity, err := tiledelivery.NewClimateBlockIdentity(catalog.ReleaseID, catalog.Period, agera5.BlockCoordinate{Latitude: block.LatitudeBlock, Longitude: block.LongitudeBlock})
		if err != nil {
			return Catalog{}, err
		}
		reader, err := tiledelivery.OpenClimateBlock(filepath.Join(root, filepath.FromSlash(block.Path)), identity, block.SHA256)
		if err != nil {
			return Catalog{}, fmt.Errorf("validate climate point block %d/%d: %w", block.LatitudeBlock, block.LongitudeBlock, err)
		}
		metadata := reader.Metadata()
		geometryErr := validatePointGeometry(reader, identity.Block)
		closeErr := reader.Close()
		if geometryErr != nil {
			return Catalog{}, geometryErr
		}
		if closeErr != nil {
			return Catalog{}, closeErr
		}
		if metadata.Bytes != block.Bytes || metadata.PayloadSHA256 != block.PayloadSHA256 || metadata.MediaType != block.MediaType || metadata.DataRecords != block.DataRecords || metadata.NoDataRecords != block.NoDataRecords {
			return Catalog{}, fmt.Errorf("climate point block %d/%d metadata does not match its file", block.LatitudeBlock, block.LongitudeBlock)
		}
	}
	for _, tile := range catalog.ScalarTiles {
		_, stats, err := tiledelivery.ReadScalar(filepath.Join(root, filepath.FromSlash(tile.Path)), tile.SHA256)
		if err != nil {
			return Catalog{}, fmt.Errorf("validate climate scalar %s %d/%d/%d: %w", tile.Field, tile.Zoom, tile.X, tile.Y, err)
		}
		if tile.Bytes != tiledelivery.ScalarTileBytes || tile.MediaType != tiledelivery.ScalarMediaType || stats.DataTexels != tile.DataTexels || stats.NoDataTexels != tile.NoDataTexels {
			return Catalog{}, fmt.Errorf("climate scalar %s %d/%d/%d metadata does not match its file", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
	}
	after, err := readRegularBounded(filepath.Join(root, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, fmt.Errorf("reread climate catalog: %w", err)
	}
	if !bytes.Equal(payload, after) {
		return Catalog{}, errors.New("climate catalog changed during validation")
	}
	return catalog, nil
}

func validatePointGeometry(reader *tiledelivery.ClimateBlockReader, block agera5.BlockCoordinate) error {
	latitudeCount, longitudeCount, err := agera5.BlockShape(block)
	if err != nil {
		return err
	}
	intersects := false
	for latitudeOffset := 0; latitudeOffset < latitudeCount; latitudeOffset++ {
		for longitudeOffset := 0; longitudeOffset < longitudeCount; longitudeOffset++ {
			latitudeIndex, longitudeIndex, err := climatebuild.Locate(block, latitudeOffset, longitudeOffset)
			if err != nil {
				return err
			}
			if _, included := climatebuild.WorldIndex(latitudeIndex, longitudeIndex); included {
				intersects = true
				continue
			}
			longitude := -180 + float64(longitudeIndex)/10
			latitude := -90 + float64(latitudeIndex)/10
			_, valid, err := reader.RecordAt(longitude, latitude)
			if err != nil {
				return err
			}
			if valid {
				return fmt.Errorf("climate point block %d/%d contains data outside catalogue geometry", block.Latitude, block.Longitude)
			}
		}
	}
	if !intersects {
		return fmt.Errorf("climate point block %d/%d is outside catalogue geometry", block.Latitude, block.Longitude)
	}
	return nil
}

func validateCatalog(catalog Catalog) error {
	if catalog.Version != CatalogVersion || !validReleaseID(catalog.ReleaseID) || !periodPattern.MatchString(catalog.Period) {
		return errors.New("climate catalog identity is invalid")
	}
	want := NewCatalog(catalog.ReleaseID, catalog.Period, catalog.Provenance, catalog.PointBlocks, catalog.ScalarTiles)
	if catalog.Geometry != want.Geometry || catalog.Source != want.Source || catalog.MaximumZoom != MaximumZoom ||
		catalog.Probability.Unit != want.Probability.Unit || catalog.Probability.Denominator != want.Probability.Denominator ||
		catalog.Probability.Threshold != want.Probability.Threshold || catalog.Probability.GoodMinimumScore != want.Probability.GoodMinimumScore ||
		catalog.Probability.NoDataValue != want.Probability.NoDataValue || !slices.Equal(catalog.Probability.Fields, fields) {
		return errors.New("climate catalog geometry, source or probability contract is invalid")
	}
	if !validEvidenceSHA256(catalog.Provenance.SourceEvidenceSHA256) || !validEvidenceSHA256(catalog.Provenance.LandMaskSetSHA256) || catalog.Provenance.DerivationContract != climatebuild.CheckpointDerivationContract {
		return errors.New("climate catalog checkpoint provenance is invalid")
	}
	if len(catalog.PointBlocks) == 0 || len(catalog.PointBlocks) > maximumPointBlocks || len(catalog.ScalarTiles) == 0 || len(catalog.ScalarTiles) > maximumScalarTiles {
		return errors.New("climate catalog asset counts are invalid")
	}
	if !slices.Equal(catalog.PointBlocks, want.PointBlocks) || !slices.Equal(catalog.ScalarTiles, want.ScalarTiles) {
		return errors.New("climate catalog assets are not in canonical order")
	}
	seenBlocks := make(map[[2]int]struct{}, len(catalog.PointBlocks))
	for _, block := range catalog.PointBlocks {
		coordinate := agera5.BlockCoordinate{Latitude: block.LatitudeBlock, Longitude: block.LongitudeBlock}
		latitudeCount, longitudeCount, err := agera5.BlockShape(coordinate)
		if err != nil || block.LatitudeCount != uint16(latitudeCount) || block.LongitudeCount != uint16(longitudeCount) ||
			!validAssetPath(block.Path) || block.Path != PointBlockPath(catalog.Period, coordinate) || block.MediaType != tiledelivery.ClimateBlockMediaType ||
			block.Bytes != uint64(tiledelivery.ClimateBlockHeaderBytes+latitudeCount*longitudeCount*delivery.ClimateDistributionRecordBytes) ||
			!validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) || block.DataRecords == 0 ||
			int(block.DataRecords)+int(block.NoDataRecords) != latitudeCount*longitudeCount {
			return fmt.Errorf("climate point block %d/%d metadata is invalid", block.LatitudeBlock, block.LongitudeBlock)
		}
		key := [2]int{block.LatitudeBlock, block.LongitudeBlock}
		if _, duplicate := seenBlocks[key]; duplicate {
			return fmt.Errorf("duplicate climate point block %d/%d", block.LatitudeBlock, block.LongitudeBlock)
		}
		seenBlocks[key] = struct{}{}
	}
	return validateScalarCoverage(catalog)
}

type tileCoordinate struct {
	Zoom uint8
	X    uint32
	Y    uint32
}

func validateScalarCoverage(catalog Catalog) error {
	coverage := make(map[Field]map[tileCoordinate]struct{}, len(fields))
	for _, field := range fields {
		coverage[field] = make(map[tileCoordinate]struct{})
	}
	for _, tile := range catalog.ScalarTiles {
		fieldCoverage, ok := coverage[tile.Field]
		count := uint64(1) << tile.Zoom
		if !ok || tile.Zoom > MaximumZoom || uint64(tile.X) >= count || uint64(tile.Y) >= count || !validAssetPath(tile.Path) ||
			tile.Path != ScalarTilePath(catalog.Period, tile.Field, tile.Zoom, tile.X, tile.Y) ||
			tile.Bytes != tiledelivery.ScalarTileBytes || tile.MediaType != tiledelivery.ScalarMediaType || !validSHA256(tile.SHA256) ||
			tile.DataTexels == 0 || uint64(tile.DataTexels)+uint64(tile.NoDataTexels) != tiledelivery.ScalarTileBytes {
			return fmt.Errorf("climate scalar %s %d/%d/%d metadata is invalid", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
		coordinate := tileCoordinate{Zoom: tile.Zoom, X: tile.X, Y: tile.Y}
		if _, duplicate := fieldCoverage[coordinate]; duplicate {
			return fmt.Errorf("duplicate climate scalar %s %d/%d/%d", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
		fieldCoverage[coordinate] = struct{}{}
	}
	baseline := coverage[fields[0]]
	for zoom := uint8(0); zoom <= MaximumZoom; zoom++ {
		found := false
		for coordinate := range baseline {
			found = found || coordinate.Zoom == zoom
		}
		if !found {
			return fmt.Errorf("climate scalar pyramid has no zoom %d tiles", zoom)
		}
	}
	if _, ok := baseline[tileCoordinate{}]; !ok {
		return errors.New("climate scalar pyramid is missing worldwide zoom 0 tile")
	}
	for _, field := range fields[1:] {
		if !sameCoordinates(baseline, coverage[field]) {
			return fmt.Errorf("climate scalar field %s does not have complete field/zoom coverage", field)
		}
	}
	for coordinate := range baseline {
		if coordinate.Zoom > 0 {
			parent := tileCoordinate{Zoom: coordinate.Zoom - 1, X: coordinate.X / 2, Y: coordinate.Y / 2}
			if _, ok := baseline[parent]; !ok {
				return fmt.Errorf("climate scalar tile %d/%d/%d has no parent", coordinate.Zoom, coordinate.X, coordinate.Y)
			}
		}
		if coordinate.Zoom < MaximumZoom {
			hasChild := false
			for y := coordinate.Y * 2; y < coordinate.Y*2+2; y++ {
				for x := coordinate.X * 2; x < coordinate.X*2+2; x++ {
					_, has := baseline[tileCoordinate{Zoom: coordinate.Zoom + 1, X: x, Y: y}]
					hasChild = hasChild || has
				}
			}
			if !hasChild {
				return fmt.Errorf("climate scalar tile %d/%d/%d has no child", coordinate.Zoom, coordinate.X, coordinate.Y)
			}
		}
	}
	return nil
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

func validateExactTree(root string, expectedFiles map[string]struct{}) error {
	expectedDirectories := map[string]struct{}{":root": {}}
	for file := range expectedFiles {
		for directory := path.Dir(file); directory != "."; directory = path.Dir(directory) {
			expectedDirectories[directory] = struct{}{}
		}
	}
	actualFiles := make(map[string]struct{}, len(expectedFiles))
	actualDirectories := make(map[string]struct{}, len(expectedDirectories))
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			actualDirectories[":root"] = struct{}{}
			return nil
		}
		relative = filepath.ToSlash(relative)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("climate release entry %q must not be a symlink", relative)
		}
		if info.IsDir() {
			actualDirectories[relative] = struct{}{}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("climate release entry %q must be regular", relative)
		}
		actualFiles[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if !sameStringSet(expectedFiles, actualFiles) || !sameStringSet(expectedDirectories, actualDirectories) {
		return errors.New("climate release contains missing or extra files or directories")
	}
	return nil
}

func sameStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for item := range left {
		if _, ok := right[item]; !ok {
			return false
		}
	}
	return true
}

func validAssetPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00") || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func releaseAssetPath(releaseRoot, filename string) (string, error) {
	if releaseRoot == "" || filename == "" {
		return "", errors.New("climate release root and asset path are required")
	}
	relative, err := filepath.Rel(releaseRoot, filename)
	if err != nil {
		return "", err
	}
	relative = filepath.ToSlash(relative)
	if !validAssetPath(relative) {
		return "", fmt.Errorf("climate asset %q is outside its release", filename)
	}
	return relative, nil
}

func validReleaseID(value string) bool {
	return len(value) <= 64 && releaseIDPattern.MatchString(value)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validEvidenceSHA256(value string) bool {
	return validSHA256(value) && strings.Trim(value, "0") != ""
}

func fieldRank(field Field) int {
	for index, candidate := range fields {
		if field == candidate {
			return index
		}
	}
	return len(fields)
}

func readRegularBounded(filename string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
		return nil, errors.New("file must be a bounded regular file and not a symlink")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximum {
		return nil, errors.New("file exceeds size limit")
	}
	return payload, nil
}

func decodeExactJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON has trailing content")
	}
	return nil
}
