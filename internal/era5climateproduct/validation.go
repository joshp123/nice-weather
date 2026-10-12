package era5climateproduct

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/era5hourly"
	"nice-weather-finder/internal/tiledelivery"
)

const maximumCatalogBytes = 32 * 1024 * 1024

// ParseCatalog strictly decodes and validates bounded catalogue metadata.
func ParseCatalog(encoded []byte) (Catalog, error) {
	if len(encoded) == 0 || len(encoded) > maximumCatalogBytes {
		return Catalog{}, errors.New("ERA5 climate catalog size is invalid")
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode ERA5 climate catalog: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("ERA5 climate catalog has trailing JSON")
	}
	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// ValidateRelease verifies the catalogue, exact file set, every checksum and
// record, maximum-zoom point parity, and every derived scalar parent.
func ValidateRelease(releaseRoot string, plan era5hourly.CellPlan, displayMask DisplayMask) (Catalog, error) {
	if plan.CellCount() == 0 || !displayMask.matchesPlan(plan) {
		return Catalog{}, errors.New("ERA5 climate release validation requires its exact source plan and display mask")
	}
	info, err := os.Lstat(releaseRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Catalog{}, errors.New("ERA5 climate release root must be a real directory")
	}
	catalogBytes, err := readBounded(filepath.Join(releaseRoot, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	catalog, err := ParseCatalog(catalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	if filepath.Base(releaseRoot) != catalog.ReleaseID {
		return Catalog{}, errors.New("ERA5 climate release directory does not match its catalog")
	}
	if catalog.Period != fmt.Sprintf("month-%02d", plan.Month()) ||
		catalog.Source.AcquisitionPlanSHA256 != plan.SHA256() ||
		catalog.Source.LogicalPlanSHA256 != plan.LogicalSHA256() ||
		catalog.Source.LogicalCellCount != plan.CellCount() {
		return Catalog{}, errors.New("ERA5 climate release does not match its exact source plan")
	}
	if catalog.Source.LandSourceSHA256 != displayMask.landSourceSHA256 ||
		catalog.Source.DisplayMaskDerivation != displayMask.derivation ||
		catalog.Source.DisplayMaskSHA256 != displayMask.sha256 ||
		catalog.Source.PublicationAuthorityKind != displayMask.publicationAuthorityKind ||
		catalog.Source.PublicationAuthoritySHA256 != displayMask.publicationAuthoritySHA256 ||
		catalog.Source.DisplayMaskBytes != uint64(len(displayMask.values)) ||
		catalog.Source.LogicalPlanSHA256 != displayMask.logicalPlanSHA256 {
		return Catalog{}, errors.New("ERA5 climate release does not match its canonical display mask authority")
	}

	maskBytes, err := readBounded(filepath.Join(releaseRoot, catalog.Source.DisplayMaskPath), Rows*Columns)
	if err != nil {
		return Catalog{}, err
	}
	if len(maskBytes) != Rows*Columns {
		return Catalog{}, errors.New("ERA5 climate display mask size changed")
	}
	maskDigest := sha256.Sum256(maskBytes)
	if hex.EncodeToString(maskDigest[:]) != catalog.Source.DisplayMaskSHA256 {
		return Catalog{}, errors.New("ERA5 climate display mask checksum mismatch")
	}
	for index, value := range maskBytes {
		if value > 1 {
			return Catalog{}, fmt.Errorf("ERA5 climate display mask cell %d is invalid", index)
		}
	}
	if !bytes.Equal(maskBytes, displayMask.values) {
		return Catalog{}, errors.New("ERA5 climate release display mask differs from its canonical authority")
	}

	cells := plan.Cells()
	expectedMain := make([]bool, Rows*Columns)
	expectedOverride := make([]bool, Rows)
	datelineCells := 0
	for index, cell := range cells {
		row, column, override, err := gridPosition(cell.Latitude, cell.Longitude)
		if err != nil {
			return Catalog{}, fmt.Errorf("ERA5 climate source plan cell %d: %w", index, err)
		}
		if override {
			expectedOverride[row] = true
			datelineCells++
		} else {
			expectedMain[row*Columns+column] = true
		}
	}
	if catalog.Source.DatelineCellCount != datelineCells {
		return Catalog{}, errors.New("ERA5 climate release dateline count differs from its source plan")
	}

	verifier := &Builder{
		config:          Config{ReleaseID: catalog.ReleaseID, MaximumZoom: catalog.MaximumZoom},
		period:          catalog.Period,
		planSHA256:      catalog.Source.AcquisitionPlanSHA256,
		displayMask:     displayMask,
		mainRecords:     make([]byte, Rows*Columns*delivery.ClimatePointRecordBytes),
		overrideRecords: make([]byte, Rows*delivery.ClimatePointRecordBytes),
		mainScalars:     make([][]byte, len(fields)),
		overrideScalars: make([][]byte, len(fields)),
		overridePresent: make([]bool, Rows),
	}
	for field := range fields {
		verifier.mainScalars[field] = make([]byte, Rows*Columns)
		verifier.overrideScalars[field] = make([]byte, Rows)
	}

	expectedFiles := map[string]struct{}{
		CatalogFilename:                {},
		catalog.Source.DisplayMaskPath: {},
	}
	mainCount, datelineCount, err := validatePointAssets(
		releaseRoot, catalog, verifier, expectedMain, expectedOverride, expectedFiles,
	)
	if err != nil {
		return Catalog{}, err
	}
	if mainCount+datelineCount != catalog.Source.LogicalCellCount ||
		datelineCount != catalog.Source.DatelineCellCount {
		return Catalog{}, errors.New("ERA5 climate catalog logical cell counts do not match point assets")
	}
	if err := validateScalarAssets(releaseRoot, catalog, verifier, expectedFiles); err != nil {
		return Catalog{}, err
	}
	if err := validateExactFileSet(releaseRoot, expectedFiles); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// ValidatePublishedRelease verifies an already deep-validated immutable
// release without requiring its external acquisition plan. It checks the
// catalog, exact file set, all checksums and records, and point/scalar parity.
// ValidateRelease remains the stronger source-plan publication gate.
func ValidatePublishedRelease(releaseRoot string) (Catalog, error) {
	info, err := os.Lstat(releaseRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Catalog{}, errors.New("ERA5 climate release root must be a real directory")
	}
	catalogBytes, err := readBounded(filepath.Join(releaseRoot, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	catalog, err := ParseCatalog(catalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	if filepath.Base(releaseRoot) != catalog.ReleaseID {
		return Catalog{}, errors.New("ERA5 climate release directory does not match its catalog")
	}
	maskBytes, err := readBounded(filepath.Join(releaseRoot, catalog.Source.DisplayMaskPath), Rows*Columns)
	if err != nil {
		return Catalog{}, err
	}
	if len(maskBytes) != Rows*Columns {
		return Catalog{}, errors.New("ERA5 climate display mask size changed")
	}
	maskDigest := sha256.Sum256(maskBytes)
	if hex.EncodeToString(maskDigest[:]) != catalog.Source.DisplayMaskSHA256 {
		return Catalog{}, errors.New("ERA5 climate display mask checksum mismatch")
	}
	for index, value := range maskBytes {
		if value > 1 {
			return Catalog{}, fmt.Errorf("ERA5 climate display mask cell %d is invalid", index)
		}
	}

	verifier := &Builder{
		config:          Config{ReleaseID: catalog.ReleaseID, MaximumZoom: catalog.MaximumZoom},
		period:          catalog.Period,
		planSHA256:      catalog.Source.AcquisitionPlanSHA256,
		displayMask:     DisplayMask{values: maskBytes},
		mainRecords:     make([]byte, Rows*Columns*delivery.ClimatePointRecordBytes),
		overrideRecords: make([]byte, Rows*delivery.ClimatePointRecordBytes),
		mainScalars:     make([][]byte, len(fields)),
		overrideScalars: make([][]byte, len(fields)),
		overridePresent: make([]bool, Rows),
	}
	for field := range fields {
		verifier.mainScalars[field] = make([]byte, Rows*Columns)
		verifier.overrideScalars[field] = make([]byte, Rows)
	}
	expectedFiles := map[string]struct{}{
		CatalogFilename:                {},
		catalog.Source.DisplayMaskPath: {},
	}
	mainCount, datelineCount, err := validatePointAssets(
		releaseRoot, catalog, verifier, nil, nil, expectedFiles,
	)
	if err != nil {
		return Catalog{}, err
	}
	if mainCount+datelineCount != catalog.Source.LogicalCellCount ||
		datelineCount != catalog.Source.DatelineCellCount {
		return Catalog{}, errors.New("ERA5 climate catalog logical cell counts do not match point assets")
	}
	if err := validateScalarAssets(releaseRoot, catalog, verifier, expectedFiles); err != nil {
		return Catalog{}, err
	}
	if err := validateExactFileSet(releaseRoot, expectedFiles); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func validateCatalog(catalog Catalog) error {
	if catalog.Version != CatalogVersion || !validIdentifier(catalog.ReleaseID) ||
		!validPeriodValue(catalog.Period) || catalog.MaximumZoom > MaximumZoom ||
		catalog.PointRecordBytes != delivery.ClimatePointRecordBytes {
		return errors.New("ERA5 climate catalog identity is invalid")
	}
	if catalog.Geometry != (Geometry{
		Projection: "EPSG:4326", West: -180, East: 180, South: -90, North: 90,
		Rows: Rows, Columns: Columns, CellResolutionDegrees: GridDegrees,
		CellRegistration: "exact-era5-grid-point-centres",
	}) {
		return errors.New("ERA5 climate catalog geometry is invalid")
	}
	source := catalog.Source
	if source.Dataset != "ERA5" || source.Kind != "hourly-reanalysis-reduced-to-iana-civil-local-days" ||
		source.ReferenceFirstYear != 2006 || source.ReferenceLastYear != 2025 ||
		source.GridDegrees != GridDegrees || source.LocalDay != LocalDayAuthority || source.LandAuthority != LandAuthority ||
		!validTrustedSHA256(source.LandSourceSHA256) || source.DisplayMaskDerivation == "" ||
		!validTrustedSHA256(source.AcquisitionPlanSHA256) ||
		!validTrustedSHA256(source.LogicalPlanSHA256) ||
		!validTrustedSHA256(source.PublicationAuthoritySHA256) ||
		source.PublicationAuthorityKind != sidecarAuthorityKind ||
		source.PublicationAuthoritySHA256 == source.AcquisitionPlanSHA256 ||
		source.LogicalCellCount < 1 ||
		source.LogicalCellCount > Rows*Columns+Rows || source.DatelineCellCount < 0 ||
		source.DatelineCellCount > Rows || source.DisplayMaskAuthority != DisplayMaskAuthority ||
		!validTrustedSHA256(source.DisplayMaskSHA256) || source.DisplayMaskPath != "display-mask.bin" ||
		source.DisplayMaskBytes != Rows*Columns {
		return errors.New("ERA5 climate catalog source contract is invalid")
	}
	if catalog.Dateline != (DatelineSemantics{
		Longitude:        180,
		Meaning:          "independent +180 degree civil-day records; never alias to -180 degree records",
		CanonicalColumns: Columns, OverrideRecordBytes: delivery.ClimatePointRecordBytes,
	}) {
		return errors.New("ERA5 climate catalog dateline contract is invalid")
	}
	if catalog.Probability.Unit != "whole-percent" ||
		catalog.Probability.Denominator != "complete-real-days-in-reference-month" ||
		catalog.Probability.Threshold != "canonical-rating-good-or-better" ||
		catalog.Probability.GoodMinimumScore != 55 || catalog.Probability.NoDataValue != tiledelivery.ScalarNoData ||
		!slices.Equal(catalog.Probability.Fields, fields) {
		return errors.New("ERA5 climate catalog probability contract is invalid")
	}
	if catalog.ScalarSemantics != (ScalarSemantics{
		MaximumZoomMeaning: "exact evaluated point value sampled only where the ERA5 grid-point centre is land",
		ParentAggregation:  "rounded arithmetic mean of available four child texels; no-data excluded",
	}) {
		return errors.New("ERA5 climate catalog scalar contract is invalid")
	}
	return validateCatalogAssets(catalog)
}

func validateCatalogAssets(catalog Catalog) error {
	mainSeen := make(map[[2]int]struct{}, len(catalog.PointBlocks))
	for _, block := range catalog.PointBlocks {
		key := [2]int{block.LatitudeBlock, block.LongitudeBlock}
		if _, duplicate := mainSeen[key]; duplicate {
			return errors.New("ERA5 climate catalog has duplicate point blocks")
		}
		mainSeen[key] = struct{}{}
		if block.LatitudeBlock < 0 || block.LatitudeBlock >= (Rows+PointBlockRows-1)/PointBlockRows ||
			block.LongitudeBlock < 0 || block.LongitudeBlock >= Columns/PointBlockCols ||
			block.LatitudeCount != uint16(min(PointBlockRows, Rows-block.LatitudeBlock*PointBlockRows)) ||
			block.LongitudeCount != PointBlockCols ||
			block.Path != mainPointPath(catalog.Period, block.LatitudeBlock, block.LongitudeBlock) ||
			block.MediaType != PointMediaType || !validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) ||
			block.Bytes != pointHeaderBytes+PointBlockRows*PointBlockCols*delivery.ClimatePointRecordBytes ||
			int(block.DataRecords)+int(block.NoDataRecords) != PointBlockRows*PointBlockCols ||
			block.DataRecords == 0 {
			return errors.New("ERA5 climate catalog point block metadata is invalid")
		}
	}
	overrideSeen := make(map[int]struct{}, len(catalog.DatelineOverrides))
	for _, block := range catalog.DatelineOverrides {
		if _, duplicate := overrideSeen[block.LatitudeBlock]; duplicate {
			return errors.New("ERA5 climate catalog has duplicate dateline blocks")
		}
		overrideSeen[block.LatitudeBlock] = struct{}{}
		if block.LatitudeBlock < 0 || block.LatitudeBlock >= (Rows+PointBlockRows-1)/PointBlockRows ||
			block.LatitudeCount != uint16(min(PointBlockRows, Rows-block.LatitudeBlock*PointBlockRows)) ||
			block.Path != datelinePointPath(catalog.Period, block.LatitudeBlock) ||
			block.MediaType != OverrideMediaType || !validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) ||
			block.Bytes != pointHeaderBytes+PointBlockRows*delivery.ClimatePointRecordBytes ||
			int(block.DataRecords)+int(block.NoDataRecords) != PointBlockRows || block.DataRecords == 0 {
			return errors.New("ERA5 climate catalog dateline block metadata is invalid")
		}
	}
	scalarSeen := make(map[[4]uint32]struct{}, len(catalog.ScalarTiles))
	for _, tile := range catalog.ScalarTiles {
		rank := fieldRank(tile.Field)
		count := uint32(1) << tile.Zoom
		key := [4]uint32{uint32(rank), uint32(tile.Zoom), tile.X, tile.Y}
		if rank == len(fields) || tile.Zoom > catalog.MaximumZoom || tile.X >= count || tile.Y >= count {
			return errors.New("ERA5 climate catalog scalar coordinate is invalid")
		}
		if _, duplicate := scalarSeen[key]; duplicate {
			return errors.New("ERA5 climate catalog has duplicate scalar tiles")
		}
		scalarSeen[key] = struct{}{}
		if tile.Path != scalarPath(catalog.Period, tile.Field, tile.Zoom, tile.X, tile.Y) ||
			tile.Bytes != tiledelivery.ScalarTileBytes || !validSHA256(tile.SHA256) ||
			tile.MediaType != tiledelivery.ScalarMediaType ||
			uint64(tile.DataTexels)+uint64(tile.NoDataTexels) != tiledelivery.ScalarTileBytes ||
			tile.DataTexels == 0 {
			return errors.New("ERA5 climate catalog scalar metadata is invalid")
		}
	}
	return nil
}

func validatePointAssets(
	releaseRoot string,
	catalog Catalog,
	verifier *Builder,
	expectedMain []bool,
	expectedOverride []bool,
	files map[string]struct{},
) (int, int, error) {
	mainCount := 0
	for _, block := range catalog.PointBlocks {
		files[block.Path] = struct{}{}
		header, payload, err := readPointAsset(filepath.Join(releaseRoot, filepath.FromSlash(block.Path)), block.SHA256)
		if err != nil {
			return 0, 0, err
		}
		if header.Kind != PointKindMain || int(header.LatitudeBlock) != block.LatitudeBlock ||
			int(header.LongitudeBlock) != block.LongitudeBlock || header.PayloadSHA256 != block.PayloadSHA256 {
			return 0, 0, errors.New("ERA5 climate point header does not match catalog")
		}
		data := 0
		for index := 0; index < PointBlockRows*PointBlockCols; index++ {
			start := index * delivery.ClimatePointRecordBytes
			record, err := delivery.DecodeClimatePointRecord(payload[start : start+delivery.ClimatePointRecordBytes])
			if err != nil {
				return 0, 0, err
			}
			row := block.LatitudeBlock*PointBlockRows + index/PointBlockCols
			column := block.LongitudeBlock*PointBlockCols + index%PointBlockCols
			if row >= Rows || column >= Columns {
				if record.Distribution.Valid {
					return 0, 0, errors.New("ERA5 climate point block invents data outside the grid")
				}
				continue
			}
			if !record.Distribution.Valid {
				continue
			}
			if expectedMain != nil && !expectedMain[row*Columns+column] {
				return 0, 0, errors.New("ERA5 climate point block occupies a cell outside the source plan")
			}
			data++
			offset := row*Columns + column
			copy(verifier.mainRecords[offset*delivery.ClimatePointRecordBytes:], payload[start:start+delivery.ClimatePointRecordBytes])
			values := scalarValues(record.Distribution)
			for field := range fields {
				verifier.mainScalars[field][offset] = values[field]
			}
		}
		if data != int(block.DataRecords) {
			return 0, 0, errors.New("ERA5 climate point block data count does not match catalog")
		}
		mainCount += data
	}
	datelineCount := 0
	for _, block := range catalog.DatelineOverrides {
		files[block.Path] = struct{}{}
		header, payload, err := readPointAsset(filepath.Join(releaseRoot, filepath.FromSlash(block.Path)), block.SHA256)
		if err != nil {
			return 0, 0, err
		}
		if header.Kind != PointKindDateline || int(header.LatitudeBlock) != block.LatitudeBlock ||
			header.LongitudeBlock != 0 || header.PayloadSHA256 != block.PayloadSHA256 {
			return 0, 0, errors.New("ERA5 climate dateline header does not match catalog")
		}
		data := 0
		for index := 0; index < PointBlockRows; index++ {
			start := index * delivery.ClimatePointRecordBytes
			record, err := delivery.DecodeClimatePointRecord(payload[start : start+delivery.ClimatePointRecordBytes])
			if err != nil {
				return 0, 0, err
			}
			row := block.LatitudeBlock*PointBlockRows + index
			if row >= Rows {
				if record.Distribution.Valid {
					return 0, 0, errors.New("ERA5 climate dateline block invents data outside the grid")
				}
				continue
			}
			if !record.Distribution.Valid {
				continue
			}
			if expectedOverride != nil && !expectedOverride[row] {
				return 0, 0, errors.New("ERA5 climate dateline block occupies a row outside the source plan")
			}
			data++
			verifier.overridePresent[row] = true
			copy(verifier.overrideRecords[row*delivery.ClimatePointRecordBytes:], payload[start:start+delivery.ClimatePointRecordBytes])
			values := scalarValues(record.Distribution)
			for field := range fields {
				verifier.overrideScalars[field][row] = values[field]
			}
		}
		if data != int(block.DataRecords) {
			return 0, 0, errors.New("ERA5 climate dateline block data count does not match catalog")
		}
		datelineCount += data
	}
	if expectedMain != nil {
		for offset, expected := range expectedMain {
			if expected != (verifier.mainRecords[offset*delivery.ClimatePointRecordBytes] != 0) {
				return 0, 0, errors.New("ERA5 climate main point occupancy does not equal the source plan")
			}
		}
	}
	if expectedOverride != nil {
		for row, expected := range expectedOverride {
			if expected != (verifier.overrideRecords[row*delivery.ClimatePointRecordBytes] != 0) {
				return 0, 0, errors.New("ERA5 climate dateline occupancy does not equal the source plan")
			}
		}
	}
	return mainCount, datelineCount, nil
}

func validateScalarAssets(releaseRoot string, catalog Catalog, verifier *Builder, files map[string]struct{}) error {
	actual := make(map[tiledelivery.ID]tiledelivery.ScalarAsset, len(catalog.ScalarTiles))
	catalogByID := make(map[tiledelivery.ID]ScalarTile, len(catalog.ScalarTiles))
	for _, tile := range catalog.ScalarTiles {
		files[tile.Path] = struct{}{}
		id, err := tiledelivery.NewID(string(tile.Field), tile.Zoom, int64(tile.X), int64(tile.Y))
		if err != nil {
			return err
		}
		path := filepath.Join(releaseRoot, filepath.FromSlash(tile.Path))
		_, stats, err := tiledelivery.ReadScalar(path, tile.SHA256)
		if err != nil {
			return err
		}
		if stats.DataTexels != tile.DataTexels || stats.NoDataTexels != tile.NoDataTexels {
			return errors.New("ERA5 climate scalar counts do not match catalog")
		}
		actual[id] = tiledelivery.ScalarAsset{
			ID: id, ReleaseID: catalog.ReleaseID, Period: catalog.Period,
			Path: path, Bytes: tile.Bytes, SHA256: tile.SHA256, MediaType: tile.MediaType,
			DataTexels: tile.DataTexels, NoDataTexels: tile.NoDataTexels,
		}
		catalogByID[id] = tile
	}
	expected := make(map[tiledelivery.ID]struct{}, len(actual))
	for fieldIndex, field := range fields {
		maximum := catalog.MaximumZoom
		count := 1 << maximum
		for y := 0; y < count; y++ {
			for x := 0; x < count; x++ {
				id, _ := tiledelivery.NewID(string(field), maximum, int64(x), int64(y))
				if err := compareExpectedScalar(id, verifier.scalarSampler(fieldIndex), actual, expected); err != nil {
					return err
				}
			}
		}
		for zoom := int(maximum) - 1; zoom >= 0; zoom-- {
			count := 1 << uint8(zoom)
			for y := 0; y < count; y++ {
				for x := 0; x < count; x++ {
					id, _ := tiledelivery.NewID(string(field), uint8(zoom), int64(x), int64(y))
					parent, err := tiledelivery.NewScalarParentSampler(id, func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) {
						asset, ok := actual[id]
						return asset, ok
					})
					if err != nil {
						return err
					}
					if !parent.HasInteriorData() {
						if _, exists := actual[id]; exists {
							return fmt.Errorf("ERA5 climate scalar %s %d/%d/%d has no child data", field, zoom, x, y)
						}
						continue
					}
					if err := compareExpectedScalar(id, parent.Sample, actual, expected); err != nil {
						return err
					}
				}
			}
		}
	}
	if len(expected) != len(catalogByID) {
		return errors.New("ERA5 climate catalog has an unexpected scalar tile")
	}
	return nil
}

func compareExpectedScalar(
	id tiledelivery.ID,
	sampler tiledelivery.EvaluatedScoreSampler,
	actual map[tiledelivery.ID]tiledelivery.ScalarAsset,
	expected map[tiledelivery.ID]struct{},
) error {
	encoded, stats, err := tiledelivery.BuildScalarTile(id, sampler)
	if err != nil {
		return err
	}
	asset, exists := actual[id]
	if stats.DataTexels == 0 {
		if exists {
			return fmt.Errorf("ERA5 climate scalar %s %d/%d/%d invents data", id.Tier, id.Zoom, id.X, id.Y)
		}
		return nil
	}
	if !exists {
		return fmt.Errorf("ERA5 climate scalar %s %d/%d/%d is missing", id.Tier, id.Zoom, id.X, id.Y)
	}
	actualBytes, actualStats, err := tiledelivery.ReadScalar(asset.Path, asset.SHA256)
	if err != nil {
		return err
	}
	if !bytes.Equal(encoded, actualBytes) || stats.DataTexels != actualStats.DataTexels ||
		stats.NoDataTexels != actualStats.NoDataTexels {
		return fmt.Errorf("ERA5 climate scalar %s %d/%d/%d does not match its point/child contract", id.Tier, id.Zoom, id.X, id.Y)
	}
	expected[id] = struct{}{}
	return nil
}

func validateExactFileSet(root string, expected map[string]struct{}) error {
	seen := make(map[string]struct{}, len(expected))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("ERA5 climate release contains a symlink")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("ERA5 climate release contains a non-regular file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := expected[relative]; !ok {
			return fmt.Errorf("ERA5 climate release contains unexpected file %s", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("ERA5 climate release file set is incomplete")
	}
	return nil
}

func readBounded(path string, maximum int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("ERA5 climate file size is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != info.Size() {
		return nil, errors.New("ERA5 climate file changed while it was read")
	}
	return body, nil
}

func validPeriodValue(value string) bool {
	if len(value) != 8 || !strings.HasPrefix(value, "month-") ||
		value[6] < '0' || value[6] > '9' || value[7] < '0' || value[7] > '9' {
		return false
	}
	month := int(value[6]-'0')*10 + int(value[7]-'0')
	return month >= 1 && month <= 12
}

func mainPointPath(period string, latitudeBlock, longitudeBlock int) string {
	return fmt.Sprintf("points/%s/main/%03d/%03d.nwc", period, latitudeBlock, longitudeBlock)
}

func datelinePointPath(period string, latitudeBlock int) string {
	return fmt.Sprintf("points/%s/dateline/%03d.nwd", period, latitudeBlock)
}

func scalarPath(period string, field Field, zoom uint8, x, y uint32) string {
	return fmt.Sprintf("scores/%s/%s/%d/%d/%d.nws", period, field, zoom, x, y)
}
