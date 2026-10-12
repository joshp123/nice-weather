// Package forecastrange builds and validates the one worldwide 14-day
// forecast judgement from the exact stored IFS daily display scores.
package forecastrange

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	CatalogVersion  = 1
	ProductContract = "nice-weather-forecast-range-v3"
	Period          = "next-14-days"
	// The only complete worldwide fourteen-day source is the daily IFS 12 UTC
	// run. Its final long-range tranche is normally not available until about
	// 19:34 UTC on the following day, so the newest usable run can be more than
	// 31 hours old. Thirty-six hours allows normal dissemination and build time
	// while still failing closed when a daily run is missed.
	StaleAfterHours     = 36
	MaximumCatalogBytes = 2 * 1024 * 1024
	CatalogFilename     = "catalog.json"
	MaxZoom             = uint8(3)
	RegionalMaxZoom     = uint8(5)
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type InputCatalog struct {
	Date   string `json:"date"`
	Path   string `json:"path"`
	Bytes  uint64 `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Source struct {
	Provider           string  `json:"provider"`
	Tier               string  `json:"tier"`
	Product            string  `json:"product"`
	RunUTC             string  `json:"runUtc"`
	NativeResolutionKM float64 `json:"nativeResolutionKm"`
	StaleAfterHours    int     `json:"staleAfterHours"`
}

type Grid struct {
	Projection string `json:"projection"`
	Zoom       uint8  `json:"zoom"`
	TileSize   uint16 `json:"tileSize"`
	Rows       uint32 `json:"rows"`
	Columns    uint32 `json:"columns"`
}

type FieldContract struct {
	ID      forecasttiles.Field `json:"id"`
	Profile string              `json:"profile"`
}

type Scores struct {
	Unit        string            `json:"unit"`
	NoDataValue uint8             `json:"noDataValue"`
	MediaType   string            `json:"mediaType"`
	Profiles    []weather.Profile `json:"profiles"`
	Fields      []FieldContract   `json:"fields"`
}

type PointBlockLayout struct {
	Rows        uint16 `json:"rows"`
	Columns     uint16 `json:"columns"`
	HeaderBytes uint16 `json:"headerBytes"`
	RecordBytes uint16 `json:"recordBytes"`
	MediaType   string `json:"mediaType"`
}

type PointBlock struct {
	Row           uint32 `json:"row"`
	Column        uint32 `json:"column"`
	Omitted       bool   `json:"omitted"`
	Path          string `json:"path,omitempty"`
	Bytes         uint64 `json:"bytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	PayloadSHA256 string `json:"payloadSha256,omitempty"`
	DataRecords   uint32 `json:"dataRecords"`
	NoDataRecords uint32 `json:"noDataRecords"`
}

type MaxZoomOutcome struct {
	Zoom          uint8  `json:"zoom"`
	X             uint32 `json:"x"`
	Y             uint32 `json:"y"`
	Omitted       bool   `json:"omitted"`
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
	DataTexels   uint32              `json:"dataTexels"`
	NoDataTexels uint32              `json:"noDataTexels"`
}

type Coverage struct {
	ExpectedPointBlocks   uint32 `json:"expectedPointBlocks"`
	AvailablePointBlocks  uint32 `json:"availablePointBlocks"`
	OmittedPointBlocks    uint32 `json:"omittedPointBlocks"`
	ExpectedMaxZoomTiles  uint32 `json:"expectedMaxZoomTiles"`
	AvailableMaxZoomTiles uint32 `json:"availableMaxZoomTiles"`
	OmittedMaxZoomTiles   uint32 `json:"omittedMaxZoomTiles"`
	ScalarTiles           uint32 `json:"scalarTiles"`
}

type Catalog struct {
	Version          int              `json:"version"`
	ProductContract  string           `json:"productContract"`
	ReleaseID        string           `json:"releaseId"`
	Period           string           `json:"period"`
	Dates            []string         `json:"dates"`
	Source           Source           `json:"source"`
	Grid             Grid             `json:"grid"`
	Scores           Scores           `json:"scores"`
	PointBlockLayout PointBlockLayout `json:"pointBlockLayout"`
	Inputs           []InputCatalog   `json:"inputs"`
	RegionalInputs   []InputCatalog   `json:"regionalInputs,omitempty"`
	Coverage         Coverage         `json:"coverage"`
	PointBlocks      []PointBlock     `json:"pointBlocks"`
	ScalarCoverage   []MaxZoomOutcome `json:"scalarCoverage"`
	ScalarTiles      []ScalarTile     `json:"scalarTiles"`
}

var fieldContracts = []FieldContract{
	{ID: forecasttiles.FieldThermal, Profile: forecastcatalog.SharedField},
	{ID: forecasttiles.FieldDryness, Profile: forecastcatalog.SharedField},
	{ID: forecasttiles.FieldSun, Profile: forecastcatalog.SharedField},
	{ID: forecasttiles.FieldNiceScore, Profile: string(weather.ProfileNice)},
	{ID: forecasttiles.FieldNiceWind, Profile: string(weather.ProfileNice)},
	{ID: forecasttiles.FieldCyclingScore, Profile: string(weather.ProfileCycling)},
	{ID: forecasttiles.FieldCyclingWind, Profile: string(weather.ProfileCycling)},
}

func Fields() []forecasttiles.Field {
	result := make([]forecasttiles.Field, len(fieldContracts))
	for index, contract := range fieldContracts {
		result[index] = contract.ID
	}
	return result
}

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
		return nil, fmt.Errorf("forecast range catalogue has %d bytes; maximum is %d", len(encoded), MaximumCatalogBytes)
	}
	return encoded, nil
}

func Parse(encoded []byte) (Catalog, error) {
	if len(encoded) == 0 || len(encoded) > MaximumCatalogBytes {
		return Catalog{}, errors.New("forecast range catalogue size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("forecast range catalogue has trailing JSON")
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Validate(catalog Catalog) error {
	if catalog.Version != CatalogVersion || catalog.ProductContract != ProductContract || !validIdentifier(catalog.ReleaseID) || catalog.Period != Period {
		return errors.New("forecast range catalogue identity is invalid")
	}
	staleAfterValid := catalog.Source.StaleAfterHours == StaleAfterHours
	if catalog.Source.Provider != forecasttiles.ProviderIFS || catalog.Source.Tier != forecasttiles.TierGlobal || catalog.Source.Product != forecastcatalog.ProductIFSControl || catalog.Source.NativeResolutionKM != 28 || !staleAfterValid {
		return errors.New("forecast range source must be the global IFS product")
	}
	run, err := time.Parse(time.RFC3339, catalog.Source.RunUTC)
	if err != nil || run.Location() != time.UTC || run.Nanosecond() != 0 || run.Format(time.RFC3339) != catalog.Source.RunUTC {
		return errors.New("forecast range source run is invalid")
	}
	wantGrid := Grid{Projection: "EPSG:3857-XYZ", Zoom: catalog.Grid.Zoom, TileSize: tiledelivery.TileSize, Rows: uint32(tiledelivery.TileSize) << catalog.Grid.Zoom, Columns: uint32(tiledelivery.TileSize) << catalog.Grid.Zoom}
	if (catalog.Grid.Zoom != MaxZoom && catalog.Grid.Zoom != RegionalMaxZoom) || catalog.Grid != wantGrid || (catalog.Grid.Zoom == RegionalMaxZoom) != (len(catalog.RegionalInputs) > 0) {
		return errors.New("forecast range grid is invalid")
	}
	if catalog.Scores.Unit != forecastcatalog.ScoreUnit || catalog.Scores.NoDataValue != forecastcatalog.ScoreNoData || catalog.Scores.MediaType != tiledelivery.ScalarMediaType || !slices.Equal(catalog.Scores.Profiles, []weather.Profile{weather.ProfileNice, weather.ProfileCycling}) || !slices.Equal(catalog.Scores.Fields, fieldContracts) {
		return errors.New("forecast range score contract is invalid")
	}
	if catalog.PointBlockLayout != (PointBlockLayout{Rows: BlockRows, Columns: BlockColumns, HeaderBytes: BlockHeaderBytes, RecordBytes: delivery.RangePointRecordBytes, MediaType: BlockMediaType}) {
		return errors.New("forecast range point block layout is invalid")
	}
	if err := validateDatesAndInputs(catalog); err != nil {
		return err
	}
	if err := validateBlocks(catalog); err != nil {
		return err
	}
	if err := validateScalarCoverage(catalog); err != nil {
		return err
	}
	if err := validateScalars(catalog); err != nil {
		return err
	}
	if catalog.Coverage != calculateCoverage(catalog) {
		return errors.New("forecast range coverage summary is invalid")
	}
	return nil
}

func validateDatesAndInputs(catalog Catalog) error {
	if len(catalog.Dates) != delivery.ForecastPointDayCount || len(catalog.Inputs) != delivery.ForecastPointDayCount {
		return errors.New("forecast range requires exactly 14 dates and input catalogues")
	}
	first, err := time.Parse(time.DateOnly, catalog.Dates[0])
	if err != nil {
		return errors.New("forecast range dates are invalid")
	}
	for index, date := range catalog.Dates {
		want := first.AddDate(0, 0, index).Format(time.DateOnly)
		input := catalog.Inputs[index]
		wantPath := filepath.ToSlash(filepath.Join("forecast-days", want, forecasttiles.TierGlobal, forecastcatalogFilename))
		if date != want || input.Date != want || input.Path != wantPath || !safeRelative(input.Path) || input.Bytes == 0 || input.Bytes > forecastcatalog.MaximumCatalogBytes || !validSHA256(input.SHA256) {
			return fmt.Errorf("forecast range input catalogue at index %d is invalid or out of order", index)
		}
	}
	return nil
}

func validateBlocks(catalog Catalog) error {
	count := catalog.Grid.Rows / uint32(BlockRows)
	if catalog.Grid.Zoom == MaxZoom && len(catalog.PointBlocks) != int(count*count) {
		return errors.New("worldwide range point coverage is incomplete")
	}
	if len(catalog.PointBlocks) != len(catalog.ScalarCoverage)*16 {
		return errors.New("range point coverage does not match its tiles")
	}
	seen := make(map[[2]uint32]bool)
	for index, block := range catalog.PointBlocks {
		key := [2]uint32{block.Row, block.Column}
		if seen[key] || block.Row >= count || block.Column >= count || block.DataRecords+block.NoDataRecords != uint32(BlockRows)*uint32(BlockColumns) {
			return fmt.Errorf("range point block %d is invalid", index)
		}
		seen[key] = true
		if block.Omitted {
			if block.Path != "" || block.Bytes != 0 || block.SHA256 != "" || block.PayloadSHA256 != "" || block.DataRecords != 0 {
				return errors.New("omitted range block has data")
			}
			continue
		}
		if block.Path != TierBlockPath(catalog.Tier(), block.Row, block.Column) || block.Bytes != BlockBytes || !validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) || block.DataRecords == 0 {
			return errors.New("range point block identity is invalid")
		}
	}
	return nil
}
func validateScalarCoverage(catalog Catalog) error {
	count := uint32(1) << catalog.Grid.Zoom
	if catalog.Grid.Zoom == MaxZoom && len(catalog.ScalarCoverage) != int(count*count) {
		return errors.New("worldwide range tile coverage is incomplete")
	}
	seen := make(map[[2]uint32]bool)
	for _, outcome := range catalog.ScalarCoverage {
		key := [2]uint32{outcome.X, outcome.Y}
		if seen[key] || outcome.Zoom != catalog.Grid.Zoom || outcome.X >= count || outcome.Y >= count || outcome.DataRecords+outcome.NoDataRecords != tiledelivery.TileSize*tiledelivery.TileSize || outcome.Omitted != (outcome.DataRecords == 0) {
			return errors.New("range tile coverage is invalid")
		}
		seen[key] = true
	}
	return nil
}
func (catalog Catalog) Tier() string {
	if catalog.Grid.Zoom == RegionalMaxZoom {
		return forecasttiles.TierEurope
	}
	return forecasttiles.TierGlobal
}

type tileKey struct {
	zoom uint8
	x, y uint32
}

func validateScalars(catalog Catalog) error {
	if len(catalog.ScalarTiles) == 0 || !sort.SliceIsSorted(catalog.ScalarTiles, func(i, j int) bool { return scalarLess(catalog.ScalarTiles[i], catalog.ScalarTiles[j]) }) {
		return errors.New("forecast range scalar tiles are empty or out of order")
	}
	want := make(map[tileKey]struct{})
	for _, outcome := range catalog.ScalarCoverage {
		if outcome.Omitted {
			continue
		}
		key := tileKey{outcome.Zoom, outcome.X, outcome.Y}
		for {
			want[key] = struct{}{}
			if key.zoom == 0 {
				break
			}
			key = tileKey{key.zoom - 1, key.x / 2, key.y / 2}
		}
	}
	byField := make(map[forecasttiles.Field]map[tileKey]struct{}, len(fieldContracts))
	for _, contract := range fieldContracts {
		byField[contract.ID] = make(map[tileKey]struct{})
	}
	for _, tile := range catalog.ScalarTiles {
		fieldSet, ok := byField[tile.Field]
		count := uint64(1) << tile.Zoom
		wantPath, pathErr := tierScalarPath(catalog.Tier(), tile.Field, tile.Zoom, tile.X, tile.Y)
		if !ok || tile.Zoom > catalog.Grid.Zoom || uint64(tile.X) >= count || uint64(tile.Y) >= count || pathErr != nil || tile.Path != wantPath || tile.Bytes != tiledelivery.ScalarTileBytes || !validSHA256(tile.SHA256) || tile.DataTexels == 0 || uint64(tile.DataTexels)+uint64(tile.NoDataTexels) != tiledelivery.ScalarTileBytes {
			return fmt.Errorf("forecast range scalar %s %d/%d/%d is invalid", tile.Field, tile.Zoom, tile.X, tile.Y)
		}
		key := tileKey{tile.Zoom, tile.X, tile.Y}
		if _, duplicate := fieldSet[key]; duplicate {
			return errors.New("duplicate forecast range scalar tile")
		}
		fieldSet[key] = struct{}{}
	}
	for field, got := range byField {
		if !sameKeys(got, want) {
			return fmt.Errorf("forecast range scalar field %s has incomplete coverage", field)
		}
	}
	return nil
}

func calculateCoverage(catalog Catalog) Coverage {
	result := Coverage{ExpectedPointBlocks: uint32(len(catalog.PointBlocks)), ExpectedMaxZoomTiles: uint32(len(catalog.ScalarCoverage)), ScalarTiles: uint32(len(catalog.ScalarTiles))}
	for _, block := range catalog.PointBlocks {
		if block.Omitted {
			result.OmittedPointBlocks++
		} else {
			result.AvailablePointBlocks++
		}
	}
	for _, tile := range catalog.ScalarCoverage {
		if tile.Omitted {
			result.OmittedMaxZoomTiles++
		} else {
			result.AvailableMaxZoomTiles++
		}
	}
	return result
}

func scalarPath(field forecasttiles.Field, zoom uint8, x, y uint32) (string, error) {
	return tierScalarPath(forecasttiles.TierGlobal, field, zoom, x, y)
}
func tierScalarPath(tier string, field forecasttiles.Field, zoom uint8, x, y uint32) (string, error) {
	suffix := map[forecasttiles.Field]string{
		forecasttiles.FieldThermal: ".th", forecasttiles.FieldDryness: ".dr", forecasttiles.FieldSun: ".su",
		forecasttiles.FieldNiceScore: ".ns", forecasttiles.FieldNiceWind: ".nw", forecasttiles.FieldCyclingScore: ".cs", forecasttiles.FieldCyclingWind: ".cw",
	}[field]
	if suffix == "" {
		return "", fmt.Errorf("unknown forecast range scalar field %q", field)
	}
	return fmt.Sprintf("scores/%s/%s%s/%d/%d/%d.nws", Period, tier, suffix, zoom, x, y), nil
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
func sameKeys(left, right map[tileKey]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return false
		}
	}
	return true
}
func validIdentifier(value string) bool {
	return len(value) <= 64 && identifierPattern.MatchString(value)
}
func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) == path && path != "." && !strings.HasPrefix(path, "../") && !strings.Contains(path, `\`)
}

const forecastcatalogFilename = "catalog.json"
