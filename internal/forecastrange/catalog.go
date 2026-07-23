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
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
	"nice-weather-finder/internal/weather"
)

const (
	CatalogVersion  = 1
	ProductContract = "nice-weather-forecast-range-v2"
	Period          = "next-14-days"
	// The only complete worldwide fourteen-day source is the daily IFS 12 UTC
	// run. Its final long-range tranche is normally not available until about
	// 19:34 UTC on the following day, so the newest usable run can be more than
	// 31 hours old. Thirty-six hours allows normal dissemination and build time
	// while still failing closed when a daily run is missed.
	StaleAfterHours       = 36
	legacyStaleAfterHours = 18
	MaximumCatalogBytes   = 512 * 1024
	CatalogFilename       = "catalog.json"
	MaxZoom               = uint8(3)
)

// SupportsStaleAfterHours accepts the immediately previous publication long
// enough for its immutable run to age out. New catalogues always write
// StaleAfterHours; readers apply the current policy to either representation.
func SupportsStaleAfterHours(hours int) bool {
	return hours == StaleAfterHours || hours == legacyStaleAfterHours
}

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
	if err := validateCatalog(catalog, true); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Validate(catalog Catalog) error {
	return validateCatalog(catalog, false)
}

func validateCatalog(catalog Catalog, allowLegacyStaleAfter bool) error {
	if catalog.Version != CatalogVersion || catalog.ProductContract != ProductContract || !validIdentifier(catalog.ReleaseID) || catalog.Period != Period {
		return errors.New("forecast range catalogue identity is invalid")
	}
	staleAfterValid := catalog.Source.StaleAfterHours == StaleAfterHours || allowLegacyStaleAfter && catalog.Source.StaleAfterHours == legacyStaleAfterHours
	if catalog.Source.Provider != forecastpoints.ProviderIFS || catalog.Source.Tier != forecastpoints.TierGlobal || catalog.Source.Product != forecastcatalog.ProductIFSControl || catalog.Source.NativeResolutionKM != 28 || !staleAfterValid {
		return errors.New("forecast range source must be the global IFS product")
	}
	run, err := time.Parse(time.RFC3339, catalog.Source.RunUTC)
	if err != nil || run.Location() != time.UTC || run.Nanosecond() != 0 || run.Format(time.RFC3339) != catalog.Source.RunUTC {
		return errors.New("forecast range source run is invalid")
	}
	wantGrid := Grid{Projection: "EPSG:3857-XYZ", Zoom: MaxZoom, TileSize: tiledelivery.TileSize, Rows: worldPixels, Columns: worldPixels}
	if catalog.Grid != wantGrid {
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
		wantPath := filepath.ToSlash(filepath.Join("forecast-days", want, forecastpoints.TierGlobal, forecastcatalogFilename))
		if date != want || input.Date != want || input.Path != wantPath || !safeRelative(input.Path) || input.Bytes == 0 || input.Bytes > forecastcatalog.MaximumCatalogBytes || !validSHA256(input.SHA256) {
			return fmt.Errorf("forecast range input catalogue at index %d is invalid or out of order", index)
		}
	}
	return nil
}

func validateBlocks(catalog Catalog) error {
	if len(catalog.PointBlocks) != int(blockCount*blockCount) {
		return fmt.Errorf("forecast range has %d point blocks; want %d", len(catalog.PointBlocks), blockCount*blockCount)
	}
	for row := uint32(0); row < blockCount; row++ {
		for column := uint32(0); column < blockCount; column++ {
			index := int(row*blockCount + column)
			block := catalog.PointBlocks[index]
			if block.Row != row || block.Column != column || block.DataRecords+block.NoDataRecords != uint32(BlockRows)*uint32(BlockColumns) {
				return fmt.Errorf("forecast range point block at index %d is invalid", index)
			}
			if block.Omitted {
				if block.Path != "" || block.Bytes != 0 || block.SHA256 != "" || block.PayloadSHA256 != "" || block.DataRecords != 0 || block.NoDataRecords != uint32(BlockRows)*uint32(BlockColumns) {
					return fmt.Errorf("omitted forecast range block %d/%d is not all no-data", row, column)
				}
				continue
			}
			wantPath := BlockPath(row, column)
			if block.Path != wantPath || block.Bytes != BlockBytes || !validSHA256(block.SHA256) || !validSHA256(block.PayloadSHA256) || block.DataRecords == 0 {
				return fmt.Errorf("forecast range block %d/%d identity is invalid", row, column)
			}
		}
	}
	return nil
}

func validateScalarCoverage(catalog Catalog) error {
	count := uint32(1) << MaxZoom
	if len(catalog.ScalarCoverage) != int(count*count) {
		return errors.New("forecast range max-zoom coverage is incomplete")
	}
	for y := uint32(0); y < count; y++ {
		for x := uint32(0); x < count; x++ {
			outcome := catalog.ScalarCoverage[int(y*count+x)]
			if outcome.Zoom != MaxZoom || outcome.X != x || outcome.Y != y || outcome.DataRecords+outcome.NoDataRecords != tiledelivery.TileSize*tiledelivery.TileSize || outcome.Omitted != (outcome.DataRecords == 0) {
				return fmt.Errorf("forecast range max-zoom outcome %d/%d is invalid", x, y)
			}
		}
	}
	return nil
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
		wantPath, pathErr := scalarPath(tile.Field, tile.Zoom, tile.X, tile.Y)
		if !ok || tile.Zoom > MaxZoom || uint64(tile.X) >= count || uint64(tile.Y) >= count || pathErr != nil || tile.Path != wantPath || tile.Bytes != tiledelivery.ScalarTileBytes || !validSHA256(tile.SHA256) || tile.DataTexels == 0 || uint64(tile.DataTexels)+uint64(tile.NoDataTexels) != tiledelivery.ScalarTileBytes {
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
	suffix := map[forecasttiles.Field]string{
		forecasttiles.FieldThermal: ".th", forecasttiles.FieldDryness: ".dr", forecasttiles.FieldSun: ".su",
		forecasttiles.FieldNiceScore: ".ns", forecasttiles.FieldNiceWind: ".nw", forecasttiles.FieldCyclingScore: ".cs", forecasttiles.FieldCyclingWind: ".cw",
	}[field]
	if suffix == "" {
		return "", fmt.Errorf("unknown forecast range scalar field %q", field)
	}
	return fmt.Sprintf("scores/%s/%s%s/%d/%d/%d.nws", Period, forecastpoints.TierGlobal, suffix, zoom, x, y), nil
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
