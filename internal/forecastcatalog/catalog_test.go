package forecastcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
)

func TestCatalogBindsCompleteIFSBlockSetAndCanonicalScalars(t *testing.T) {
	config := testConfig(forecastpoints.ProviderIFS, forecastpoints.TierGlobal)
	spec, _ := specFor(config.Provider, config.Tier)
	points := pointOutcomes(t, config, spec, false)
	slices.Reverse(points)
	scalars := scalarCatalog(t, config, spec, oneTilePerZoom(t, spec))

	catalog, err := New(config, points, scalars)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Grid != catalogGrid(spec.grid) || catalog.Evidence.Product != ProductIFSControl || catalog.Evidence.NativeResolutionKM != 28 || catalog.ScalarMaxZoom != 3 {
		t.Fatalf("IFS identity = %+v %+v z%d", catalog.Grid, catalog.Evidence, catalog.ScalarMaxZoom)
	}
	if catalog.Coverage != (Coverage{ExpectedPointBlocks: 276, AvailablePointBlocks: 1, OmittedPointBlocks: 275, ExpectedMaxZoomTiles: 64, AvailableMaxZoomTiles: 1, OmittedMaxZoomTiles: 63, ScalarTiles: 28, DisplayPointTiles: 1}) {
		t.Fatalf("coverage = %+v", catalog.Coverage)
	}
	if len(catalog.Scores.Fields) != 7 || len(catalog.Scores.Profiles) != 2 || catalog.Scores.Fields[3].Profile != "nice" || catalog.Scores.Fields[5].Profile != "cycling" {
		t.Fatalf("score contract = %+v", catalog.Scores)
	}
	if first, last := catalog.PointBlocks[0], catalog.PointBlocks[len(catalog.PointBlocks)-1]; first.Row != 0 || first.Column != 0 || last.Row != 11 || last.Column != 22 || last.Rows != 16 || last.Columns != 32 {
		t.Fatalf("point ordering first=%+v last=%+v", first, last)
	}
	encoded, err := Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(encoded)
	if err != nil || !catalogEqual(catalog, parsed) {
		t.Fatalf("round trip mismatch: err=%v", err)
	}
	encodedAgain, err := Marshal(parsed)
	if err != nil || !bytes.Equal(encoded, encodedAgain) {
		t.Fatalf("non-deterministic JSON: err=%v", err)
	}
}

func TestCatalogRequiresExactLandProvenance(t *testing.T) {
	config := testConfig(forecastpoints.ProviderIFS, forecastpoints.TierGlobal)
	spec, _ := specFor(config.Provider, config.Tier)
	catalog, err := New(config, pointOutcomes(t, config, spec, false), scalarCatalog(t, config, spec, oneTilePerZoom(t, spec)))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 2 || catalog.Land.SourceArchiveSHA256 != strings.Repeat("b", 64) || catalog.Land.MaskSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("catalog land identity = v%d %+v", catalog.Version, catalog.Land)
	}
	mutations := []func(*forecasttiles.LandIdentity){
		func(value *forecasttiles.LandIdentity) { value.Authority = "other" },
		func(value *forecasttiles.LandIdentity) { value.Dataset = "ne_50m_land" },
		func(value *forecasttiles.LandIdentity) { value.Version = "5.1.0" },
		func(value *forecasttiles.LandIdentity) { value.SourceArchiveSHA256 = strings.Repeat("A", 64) },
		func(value *forecasttiles.LandIdentity) { value.MaskSHA256 = "short" },
		func(value *forecasttiles.LandIdentity) { value.DerivationContract = "centre-land-v1" },
	}
	for index, mutate := range mutations {
		candidate := catalog
		mutate(&candidate.Land)
		if err := Validate(candidate); err == nil || !strings.Contains(err.Error(), "land provenance") {
			t.Fatalf("land mutation %d error = %v", index, err)
		}
	}
}

func TestCatalogBindsExactICONEuropeDetailContract(t *testing.T) {
	config := testConfig(forecastpoints.ProviderICONEU, forecastpoints.TierEurope)
	spec, _ := specFor(config.Provider, config.Tier)
	catalog, err := New(config, pointOutcomes(t, config, spec, false), scalarCatalog(t, config, spec, oneTilePerZoom(t, spec)))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Grid != catalogGrid(spec.grid) || catalog.Evidence.Product != ProductICONEU || catalog.Evidence.NativeResolutionKM != 7 || catalog.ScalarMaxZoom != 5 {
		t.Fatalf("ICON identity = %+v %+v z%d", catalog.Grid, catalog.Evidence, catalog.ScalarMaxZoom)
	}
	if catalog.Coverage.ExpectedPointBlocks != 198 || catalog.PointBlocks[197].Row != 10 || catalog.PointBlocks[197].Column != 17 || catalog.PointBlocks[197].Rows != 40 || catalog.PointBlocks[197].Columns != 16 {
		t.Fatalf("ICON point coverage = %+v last=%+v", catalog.Coverage, catalog.PointBlocks[197])
	}
}

func TestCatalogRejectsProviderMixingMissingBlocksAndUnprovenOmissions(t *testing.T) {
	config := testConfig(forecastpoints.ProviderIFS, forecastpoints.TierGlobal)
	spec, _ := specFor(config.Provider, config.Tier)
	validPoints := pointOutcomes(t, config, spec, false)
	validScalars := scalarCatalog(t, config, spec, oneTilePerZoom(t, spec))

	tests := []struct {
		name   string
		mutate func(*Config, *[]forecastpoints.Asset, *forecasttiles.Catalog)
		want   string
	}{
		{name: "unsupported pair", mutate: func(c *Config, _ *[]forecastpoints.Asset, _ *forecasttiles.Catalog) {
			c.Tier = forecastpoints.TierEurope
		}, want: "unsupported"},
		{name: "missing block", mutate: func(_ *Config, p *[]forecastpoints.Asset, _ *forecasttiles.Catalog) { *p = (*p)[:len(*p)-1] }, want: "point blocks"},
		{name: "duplicate block", mutate: func(_ *Config, p *[]forecastpoints.Asset, _ *forecasttiles.Catalog) { (*p)[len(*p)-1] = (*p)[0] }, want: "expected coordinate"},
		{name: "unproven omission", mutate: func(_ *Config, p *[]forecastpoints.Asset, _ *forecasttiles.Catalog) { (*p)[1].NoDataRecords-- }, want: "not all no-data"},
		{name: "scalar tier", mutate: func(_ *Config, _ *[]forecastpoints.Asset, s *forecasttiles.Catalog) {
			s.Tier = forecastpoints.TierEurope
		}, want: "does not match"},
		{name: "scalar zoom", mutate: func(_ *Config, _ *[]forecastpoints.Asset, s *forecasttiles.Catalog) { s.MaxZoom = 4 }, want: "does not match"},
		{name: "scalar source", mutate: func(_ *Config, _ *[]forecastpoints.Asset, s *forecasttiles.Catalog) {
			s.Source.SourceID = "other-model"
		}, want: "scalar source"},
		{name: "source evidence", mutate: func(c *Config, _ *[]forecastpoints.Asset, _ *forecasttiles.Catalog) {
			c.SourceEvidenceSHA256 = strings.Repeat("A", 64)
		}, want: "evidence"},
		{name: "point run", mutate: func(_ *Config, p *[]forecastpoints.Asset, _ *forecasttiles.Catalog) {
			(*p)[0].Identity.RunUTC = "2026-07-22T18:00:00Z"
		}, want: "point block"},
		{name: "point evidence", mutate: func(_ *Config, p *[]forecastpoints.Asset, _ *forecasttiles.Catalog) {
			(*p)[0].Identity.SourceEvidenceSHA256 = strings.Repeat("b", 64)
		}, want: "point block"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateConfig := config
			points := append([]forecastpoints.Asset(nil), validPoints...)
			scalars := cloneScalarCatalog(validScalars)
			test.mutate(&candidateConfig, &points, &scalars)
			_, err := New(candidateConfig, points, scalars)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCatalogRejectsScalarCoverageDriftDuplicatesAndRegionalExtras(t *testing.T) {
	config := testConfig(forecastpoints.ProviderICONEU, forecastpoints.TierEurope)
	spec, _ := specFor(config.Provider, config.Tier)
	points := pointOutcomes(t, config, spec, false)
	valid := scalarCatalog(t, config, spec, oneTilePerZoom(t, spec))

	tests := []struct {
		name   string
		mutate func(*forecasttiles.Catalog)
		want   string
	}{
		{name: "missing field tile", mutate: func(c *forecasttiles.Catalog) { c.Scalars = c.Scalars[1:] }, want: "common coverage"},
		{name: "duplicate", mutate: func(c *forecasttiles.Catalog) { c.Scalars = append(c.Scalars, c.Scalars[0]) }, want: "duplicate"},
		{name: "unknown field", mutate: func(c *forecasttiles.Catalog) { c.Scalars[0].Field = "humidity" }, want: "metadata"},
		{name: "missing producer outcome", mutate: func(c *forecasttiles.Catalog) { c.MaxZoomOutcomes = c.MaxZoomOutcomes[1:] }, want: "outcomes"},
		{name: "missing producer point", mutate: func(c *forecasttiles.Catalog) { c.Points = c.Points[:0] }, want: "producer points"},
		{name: "unproven scalar omission", mutate: func(c *forecasttiles.Catalog) { c.MaxZoomOutcomes[0].NoDataRecords-- }, want: "outcome"},
		{name: "outside regional support", mutate: func(c *forecasttiles.Catalog) {
			for index := range c.Scalars {
				if c.Scalars[index].Zoom == spec.maxZoom {
					c.Scalars[index].X, c.Scalars[index].Y = 0, 0
					c.Scalars[index].Path, _ = scalarPath(config.LocalDate, config.Tier, c.Scalars[index].Field, spec.maxZoom, 0, 0)
				}
			}
		}, want: "metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneScalarCatalog(valid)
			test.mutate(&candidate)
			_, err := New(config, points, candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCatalogWorstCaseJSONFitsHardBound(t *testing.T) {
	for _, pair := range [][2]string{
		{forecastpoints.ProviderIFS, forecastpoints.TierGlobal},
		{forecastpoints.ProviderICONEU, forecastpoints.TierEurope},
	} {
		config := testConfig(pair[0], pair[1])
		spec, _ := specFor(config.Provider, config.Tier)
		coordinates := allSupportedTiles(spec)
		catalog, err := New(config, pointOutcomes(t, config, spec, true), scalarCatalog(t, config, spec, coordinates))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		wantBlocks := 276
		if spec.provider == forecastpoints.ProviderICONEU {
			wantBlocks = 198
		}
		if len(catalog.PointBlocks) != wantBlocks || len(catalog.ScalarTiles) != maximumScalarTiles(spec) || len(encoded) >= MaximumCatalogBytes {
			t.Fatalf("%s/%s blocks=%d scalar=%d bytes=%d max=%d", spec.provider, spec.tier, len(catalog.PointBlocks), len(catalog.ScalarTiles), len(encoded), MaximumCatalogBytes)
		}
		t.Logf("%s/%s worst valid catalogue: %d point outcomes, %d scalar tiles, %d bytes (%.1f%% of hard bound)", spec.provider, spec.tier, len(catalog.PointBlocks), len(catalog.ScalarTiles), len(encoded), 100*float64(len(encoded))/MaximumCatalogBytes)
	}
	if _, err := Parse(make([]byte, MaximumCatalogBytes+1)); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversize parse error = %v", err)
	}
}

func TestValidateFilesChecksPointAndScalarBytes(t *testing.T) {
	config := testConfig(forecastpoints.ProviderIFS, forecastpoints.TierGlobal)
	spec, _ := specFor(config.Provider, config.Tier)
	staging := t.TempDir()
	releaseRoot := filepath.Join(staging, "releases", config.ReleaseID)
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	points := pointOutcomes(t, config, spec, false)
	identity := points[0].Identity
	records := make([]delivery.DailyPointRecord, int(identity.Rows)*int(identity.Columns))
	records[0] = validForecastRecord(t)
	asset, err := forecastpoints.WriteBlock(releaseRoot, identity, records)
	if err != nil {
		t.Fatal(err)
	}
	points[0] = asset

	writer, err := tiledelivery.NewWriter(staging, config.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	scalars := scalarCatalog(t, config, spec, oneTilePerZoom(t, spec))
	scalars.Scalars = nil
	scalars.Points = nil
	for _, coordinate := range oneTilePerZoom(t, spec) {
		for _, field := range fieldContracts {
			id, err := tiledelivery.NewID(scalarTier(config.Tier, field.ID), coordinate.Zoom, int64(coordinate.X), int64(coordinate.Y))
			if err != nil {
				t.Fatal(err)
			}
			written, err := writer.WriteScalar(id, config.LocalDate, func(tiledelivery.ScalarSample) (uint8, bool, error) { return 60, true, nil })
			if err != nil {
				t.Fatal(err)
			}
			path, _ := scalarPath(config.LocalDate, config.Tier, field.ID, coordinate.Zoom, coordinate.X, coordinate.Y)
			if written.Path != filepath.Join(releaseRoot, filepath.FromSlash(path)) {
				t.Fatalf("written scalar path = %q; want %q", written.Path, path)
			}
			scalars.Scalars = append(scalars.Scalars, forecasttiles.ScalarTile{
				Field: field.ID, Zoom: coordinate.Zoom, X: coordinate.X, Y: coordinate.Y, Path: path,
				Bytes: written.Bytes, SHA256: written.SHA256, DataTexels: written.DataTexels, NoDataTexels: written.NoDataTexels,
			})
		}
		if coordinate.Zoom == spec.maxZoom {
			id, err := tiledelivery.NewID(config.Tier, coordinate.Zoom, int64(coordinate.X), int64(coordinate.Y))
			if err != nil {
				t.Fatal(err)
			}
			records := make([]delivery.DisplayScoreRecord, tiledelivery.TileSize*tiledelivery.TileSize)
			records[0] = delivery.DisplayScoreRecord{Thermal: 60, Dryness: 60, Sun: 60, NiceScore: 60, NiceWind: 60, CyclingScore: 60, CyclingWind: 60, Valid: true}
			written, err := writer.Write(id, config.LocalDate, records)
			if err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("points/%s/%s/%d/%d/%d.nwt", config.LocalDate, config.Tier, coordinate.Zoom, coordinate.X, coordinate.Y)
			scalars.Points = append(scalars.Points, forecasttiles.PointTile{
				Zoom: coordinate.Zoom, X: coordinate.X, Y: coordinate.Y, Path: path, Bytes: written.Bytes,
				SHA256: written.SHA256, PayloadSHA256: written.PayloadSHA256,
			})
		}
	}
	catalog, err := New(config, points, scalars)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFiles(releaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(releaseRoot, filepath.FromSlash(catalog.ScalarTiles[0].Path))
	data, err := os.ReadFile(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	data[100] ^= 1
	if err := os.WriteFile(corrupt, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFiles(releaseRoot, catalog); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt scalar error = %v", err)
	}
}

func TestParseRejectsUnknownAndTrailingJSON(t *testing.T) {
	config := testConfig(forecastpoints.ProviderIFS, forecastpoints.TierGlobal)
	spec, _ := specFor(config.Provider, config.Tier)
	catalog, err := New(config, pointOutcomes(t, config, spec, false), scalarCatalog(t, config, spec, oneTilePerZoom(t, spec)))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := Marshal(catalog)
	unknown := bytes.Replace(encoded, []byte(`"version":2`), []byte(`"unknown":true,"version":2`), 1)
	if _, err := Parse(unknown); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := Parse(append(encoded, []byte(`{}`)...)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func testConfig(provider, tier string) Config {
	return Config{
		ReleaseID: "forecast-release-20260722", LocalDate: "2026-07-23", Provider: provider, Tier: tier,
		RunUTC: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC), SourceEvidenceSHA256: strings.Repeat("a", 64),
	}
}

func pointOutcomes(t *testing.T, config Config, spec tierSpec, allAvailable bool) []forecastpoints.Asset {
	t.Helper()
	blockRows, blockColumns, err := forecastpoints.BlockGrid(config.Provider, config.Tier, spec.grid)
	if err != nil {
		t.Fatal(err)
	}
	assets := make([]forecastpoints.Asset, 0, blockRows*blockColumns)
	for row := uint32(0); row < blockRows; row++ {
		for column := uint32(0); column < blockColumns; column++ {
			identity, err := forecastpoints.NewIdentity(config.ReleaseID, config.Provider, config.Tier, config.LocalDate, config.RunUTC.Format(time.RFC3339), config.SourceEvidenceSHA256, spec.grid, forecastpoints.Coordinate{Row: row, Column: column})
			if err != nil {
				t.Fatal(err)
			}
			records := uint32(identity.Rows) * uint32(identity.Columns)
			asset := forecastpoints.Asset{Identity: identity, MediaType: forecastpoints.MediaType}
			if allAvailable || (row == 0 && column == 0) {
				relative, _ := forecastpoints.RelativePath(identity)
				whole, _ := forecastpoints.WholeRange(identity)
				asset.Path, asset.Bytes = relative, uint64(whole.Length)
				asset.SHA256, asset.PayloadSHA256 = fixtureSHA(row*blockColumns+column+1), fixtureSHA(row*blockColumns+column+10_000)
				asset.DataRecords, asset.NoDataRecords = 1, records-1
			} else {
				asset.Omitted, asset.NoDataRecords = true, records
			}
			assets = append(assets, asset)
		}
	}
	return assets
}

func scalarCatalog(t *testing.T, config Config, spec tierSpec, coordinates []tileCoordinate) forecasttiles.Catalog {
	t.Helper()
	catalog := forecasttiles.Catalog{
		Version: CatalogVersion, ReleaseID: config.ReleaseID, Period: config.LocalDate,
		Tier: config.Tier, Date: config.LocalDate, MaxZoom: spec.maxZoom,
		Source: forecasttiles.SourceIdentity{
			SourceID: spec.sourceID, RunUTC: config.RunUTC.Format(time.RFC3339), Grid: spec.grid,
			NativeResolutionKM: spec.nativeResolutionKM, EvidenceSHA256: config.SourceEvidenceSHA256,
		},
		Land: forecasttiles.LandIdentity{
			Authority: forecasttiles.LandAuthorityNaturalEarth, Dataset: forecasttiles.LandDatasetNaturalEarth10M, Version: forecasttiles.LandVersionNaturalEarth511,
			SourceArchiveSHA256: strings.Repeat("b", 64), MaskSHA256: strings.Repeat("c", 64), DerivationContract: forecasttiles.LandDerivationWebMercatorTouchV1,
		},
	}
	produced := make(map[tileCoordinate]struct{})
	for _, coordinate := range coordinates {
		if coordinate.Zoom == spec.maxZoom {
			produced[coordinate] = struct{}{}
		}
	}
	for index, coordinate := range supportedMaxZoomCoordinates(spec) {
		outcome := forecasttiles.MaxZoomOutcome{
			Zoom: coordinate.Zoom, X: coordinate.X, Y: coordinate.Y,
			Omitted: true, NoDataRecords: tiledelivery.TileSize * tiledelivery.TileSize,
		}
		if _, ok := produced[coordinate]; ok {
			outcome.Omitted, outcome.DataRecords, outcome.NoDataRecords = false, 1, tiledelivery.TileSize*tiledelivery.TileSize-1
			path := fmt.Sprintf("points/%s/%s/%d/%d/%d.nwt", config.LocalDate, config.Tier, coordinate.Zoom, coordinate.X, coordinate.Y)
			catalog.Points = append(catalog.Points, forecasttiles.PointTile{
				Zoom: coordinate.Zoom, X: coordinate.X, Y: coordinate.Y, Path: path, Bytes: tiledelivery.PointTileBytes,
				SHA256: fixtureSHA(uint32(80_000 + index)), PayloadSHA256: fixtureSHA(uint32(90_000 + index)),
			})
		}
		catalog.MaxZoomOutcomes = append(catalog.MaxZoomOutcomes, outcome)
	}
	for fieldIndex, field := range fieldContracts {
		for index, coordinate := range coordinates {
			path, err := scalarPath(config.LocalDate, config.Tier, field.ID, coordinate.Zoom, coordinate.X, coordinate.Y)
			if err != nil {
				t.Fatal(err)
			}
			catalog.Scalars = append(catalog.Scalars, forecasttiles.ScalarTile{
				Field: field.ID, Zoom: coordinate.Zoom, X: coordinate.X, Y: coordinate.Y, Path: path,
				Bytes: tiledelivery.ScalarTileBytes, SHA256: fixtureSHA(uint32(fieldIndex*20_000 + index + 1)),
				DataTexels: 1, NoDataTexels: tiledelivery.ScalarTileBytes - 1,
			})
		}
	}
	return catalog
}

func oneTilePerZoom(t *testing.T, spec tierSpec) []tileCoordinate {
	t.Helper()
	coordinates := make([]tileCoordinate, 0, spec.maxZoom+1)
	for zoom := uint8(0); zoom <= spec.maxZoom; zoom++ {
		id, _, _, err := tiledelivery.At("coverage", zoom, 10, 50)
		if err != nil {
			t.Fatal(err)
		}
		coordinate := tileCoordinate{Zoom: zoom, X: id.X, Y: id.Y}
		if !tileIntersectsGrid(coordinate, spec.grid) {
			t.Fatalf("fixture tile %d/%d/%d misses grid", zoom, id.X, id.Y)
		}
		coordinates = append(coordinates, coordinate)
	}
	return coordinates
}

func allSupportedTiles(spec tierSpec) []tileCoordinate {
	set := make(map[tileCoordinate]struct{})
	for _, coordinate := range supportedMaxZoomCoordinates(spec) {
		if !tileIntersectsGrid(coordinate, spec.grid) {
			continue
		}
		for {
			set[coordinate] = struct{}{}
			if coordinate.Zoom == 0 {
				break
			}
			coordinate = tileCoordinate{Zoom: coordinate.Zoom - 1, X: coordinate.X / 2, Y: coordinate.Y / 2}
		}
	}
	coordinates := make([]tileCoordinate, 0, len(set))
	for coordinate := range set {
		coordinates = append(coordinates, coordinate)
	}
	sort.Slice(coordinates, func(i, j int) bool {
		if coordinates[i].Zoom != coordinates[j].Zoom {
			return coordinates[i].Zoom < coordinates[j].Zoom
		}
		if coordinates[i].Y != coordinates[j].Y {
			return coordinates[i].Y < coordinates[j].Y
		}
		return coordinates[i].X < coordinates[j].X
	})
	return coordinates
}

func scalarTier(tier string, field forecasttiles.Field) string {
	path, _ := scalarPath("period", tier, field, 0, 0, 0)
	parts := strings.Split(path, "/")
	return parts[2]
}

func validForecastRecord(t *testing.T) delivery.DailyPointRecord {
	t.Helper()
	record, err := delivery.DailyPointRecordFromWeather(delivery.DailyRecord{
		ComfortableTemperatureDeciC: 240, MinTemperatureDeciC: 180, MaxTemperatureDeciC: 280, DewPointDeciC: 120,
		WindDeciKMH: 100, GustDeciKMH: 180, SolarCentiMJm2: 1900, CloudPercent: 20, HumidityPercent: 55,
		Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func fixtureSHA(value uint32) string {
	digest := sha256.Sum256([]byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)})
	return hex.EncodeToString(digest[:])
}

func cloneScalarCatalog(source forecasttiles.Catalog) forecasttiles.Catalog {
	result := source
	result.MaxZoomOutcomes = append([]forecasttiles.MaxZoomOutcome(nil), source.MaxZoomOutcomes...)
	result.Points = append([]forecasttiles.PointTile(nil), source.Points...)
	result.Scalars = append([]forecasttiles.ScalarTile(nil), source.Scalars...)
	return result
}

func catalogEqual(left, right Catalog) bool {
	return left.Version == right.Version && left.ReleaseID == right.ReleaseID && left.LocalDate == right.LocalDate &&
		left.Provider == right.Provider && left.Tier == right.Tier && left.Grid == right.Grid && left.Evidence == right.Evidence && left.Land == right.Land &&
		left.ScalarMaxZoom == right.ScalarMaxZoom && left.Scores.Unit == right.Scores.Unit && left.Scores.NoDataValue == right.Scores.NoDataValue &&
		slices.Equal(left.Scores.Profiles, right.Scores.Profiles) && slices.Equal(left.Scores.Fields, right.Scores.Fields) &&
		left.Coverage == right.Coverage && slices.Equal(left.PointBlocks, right.PointBlocks) && slices.Equal(left.DisplayPoints, right.DisplayPoints) && slices.Equal(left.ScalarCoverage, right.ScalarCoverage) && slices.Equal(left.ScalarTiles, right.ScalarTiles)
}
