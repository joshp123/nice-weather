// Package places builds the immutable, browser-searchable place index.
package places

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"nice-weather-finder/internal/delivery"
)

const (
	schema                     = "nice-places-v1"
	normalization              = "nice-latin-fold-v1"
	shardPrefixLength          = 3
	minimumQueryLength         = 3
	maximumManifestBytes       = 2 * 1024 * 1024
	maximumSourceManifestBytes = 4 * 1024
	maximumShardCount          = 8_000
	maximumShardGzipBytes      = 320 * 1024
	maximumDisplayLabelUnits   = 160
	globalSourceManifestName   = "source-manifest.json"
	globalSourceSchema         = "nice-geonames-source-v1"
)

var globalSourceFiles = []string{"admin1CodesASCII.txt", "allCountries.zip", "alternateNamesV2.zip", "countryInfo.txt"}

var includedFeatureCodes = map[string]uint8{
	"PPL": 2, "PPLA": 4, "PPLA2": 3, "PPLA3": 3, "PPLA4": 3,
	"PPLA5": 3, "PPLC": 5, "PPLF": 1, "PPLG": 4, "PPLL": 1,
	"PPLR": 1, "PPLS": 1, "STLMT": 1,
}

var excludedFeatureCodes = map[string]bool{
	"PPLCH": true, "PPLH": true, "PPLQ": true, "PPLW": true, "PPLX": true,
}

// Summary is the bounded evidence emitted by a successful build.
type Summary struct {
	PlaceCount        int   `json:"placeCount"`
	SearchKeyCount    int   `json:"searchKeyCount"`
	ShardCount        int   `json:"shardCount"`
	UncompressedBytes int64 `json:"uncompressedBytes"`
	GzipBytes         int64 `json:"gzipBytes"`
	LargestShardGzip  int64 `json:"largestShardGzipBytes"`
}

type bounds struct {
	West  float64 `json:"west"`
	East  float64 `json:"east"`
	South float64 `json:"south"`
	North float64 `json:"north"`
}

type requiredPlace struct {
	id      int64
	queries []string
}

type limits struct {
	minPlaces    int
	maxPlaces    int
	maxShards    int
	maxShardGzip int64
	required     []requiredPlace
}

type buildConfig struct {
	sourceDir                    string
	outputDir                    string
	sourceDate                   string
	countries                    []string
	coverage                     bounds
	retainAllWithin              *bounds
	minimumPopulationOutsideArea int64
	sourceBundleSHA256           string
	limits                       limits
}

var globalLimits = limits{
	minPlaces:    800_000,
	maxPlaces:    1_100_000,
	maxShards:    maximumShardCount,
	maxShardGzip: maximumShardGzipBytes,
	required: []requiredPlace{
		{id: 2750053, queries: []string{"Nijmegen"}},
		{id: 611717, queries: []string{"Tbilisi"}},
		{id: 2520600, queries: []string{"Cádiz", "Cadiz"}},
		{id: 3093133, queries: []string{"Łódź", "Lodz"}},
		{id: 2514256, queries: []string{"Málaga", "Malaga"}},
		{id: 2655710, queries: []string{"Biddestone"}},
		{id: 5128581, queries: []string{"New York City"}},
		{id: 3448439, queries: []string{"São Paulo", "Sao Paulo"}},
		{id: 3369157, queries: []string{"Cape Town"}},
		{id: 1880252, queries: []string{"Singapore"}},
		{id: 2147714, queries: []string{"Sydney"}},
		{id: 2193733, queries: []string{"Auckland"}},
		{id: 2127202, queries: []string{"Anadyr"}},
	},
}

// BuildGlobal consumes pinned GeoNames allCountries and alternateNamesV2
// extracts and writes one complete immutable release. Every populated place in
// the non-African refinement is retained; elsewhere, settlements with at least 1,500 residents
// and national or first-order administrative seats are retained. It never downloads sources or
// updates a public pointer.
func BuildGlobal(ctx context.Context, sourceDir, outputDir, sourceDate string, refinement delivery.GridBounds) (Summary, error) {
	if err := delivery.ValidateGridSpec(delivery.GridSpec{Bounds: refinement, Rows: 1, Cols: 1}); err != nil {
		return Summary{}, fmt.Errorf("place refinement: %w", err)
	}
	bundleSHA256, err := validateGlobalSource(ctx, sourceDir, sourceDate)
	if err != nil {
		return Summary{}, err
	}
	countries, err := readCountries(filepath.Join(sourceDir, "countryInfo.txt"))
	if err != nil {
		return Summary{}, err
	}
	countryCodes := make([]string, 0, len(countries))
	for code := range countries {
		countryCodes = append(countryCodes, code)
	}
	sort.Strings(countryCodes)
	refinementBounds := bounds{West: refinement.West, East: refinement.East, South: refinement.South, North: refinement.North}
	return build(ctx, buildConfig{
		sourceDir: sourceDir, outputDir: outputDir, sourceDate: sourceDate,
		countries: countryCodes, coverage: bounds{West: -180, East: 180, South: -90, North: 90},
		retainAllWithin: &refinementBounds, minimumPopulationOutsideArea: 1_500,
		sourceBundleSHA256: bundleSHA256, limits: globalLimits,
	})
}

type country struct {
	name      string
	continent string
	languages []string
}

type place struct {
	id             int64
	name           string
	asciiName      string
	country        string
	admin1         string
	latitudeE5     int32
	longitudeE5    int32
	population     int64
	featureRank    uint8
	localName      string
	localPriority  int
	preferredAlias string
}

type record struct {
	Key         string
	Name        string
	Country     string
	Admin1      string
	LatitudeE5  int32
	LongitudeE5 int32
	Population  int64
	FeatureRank uint8
	GeoNameID   int64
}

func (record record) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{
		record.Key, record.Name, record.Country, record.Admin1,
		record.LatitudeE5, record.LongitudeE5, record.Population,
		record.FeatureRank, record.GeoNameID,
	})
}

func (record *record) UnmarshalJSON(payload []byte) error {
	var fields []json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return err
	}
	if len(fields) != 9 {
		return fmt.Errorf("place record has %d fields", len(fields))
	}
	targets := []any{
		&record.Key, &record.Name, &record.Country, &record.Admin1,
		&record.LatitudeE5, &record.LongitudeE5, &record.Population,
		&record.FeatureRank, &record.GeoNameID,
	}
	for index, target := range targets {
		if err := json.Unmarshal(fields[index], target); err != nil {
			return fmt.Errorf("place record field %d: %w", index, err)
		}
	}
	return nil
}

type shard struct {
	Version int      `json:"v"`
	Records []record `json:"r"`
}

type sourceMetadata struct {
	Name        string `json:"name"`
	ExtractDate string `json:"extractDate"`
	DataURL     string `json:"dataUrl"`
	License     string `json:"license"`
	LicenseURL  string `json:"licenseUrl"`
	Attribution string `json:"attribution"`
	Modified    bool   `json:"modified"`
	Warranty    string `json:"warranty"`
}

type manifest struct {
	Schema             string            `json:"schema"`
	Source             sourceMetadata    `json:"source"`
	ProductBounds      bounds            `json:"productBounds"`
	PlaceCount         int               `json:"placeCount"`
	SearchKeyCount     int               `json:"searchKeyCount"`
	ShardCount         int               `json:"shardCount"`
	ShardPrefixLength  int               `json:"shardPrefixLength"`
	MinimumQueryLength int               `json:"minimumQueryLength"`
	Normalization      string            `json:"normalization"`
	Record             []string          `json:"record"`
	Countries          map[string]string `json:"countries"`
	Admins             map[string]string `json:"admins"`
	Shards             map[string]string `json:"shards"`
	Summary            Summary           `json:"summary"`
}

func build(ctx context.Context, config buildConfig) (Summary, error) {
	if err := validateConfig(config); err != nil {
		return Summary{}, err
	}
	countries, err := readCountries(filepath.Join(config.sourceDir, "countryInfo.txt"))
	if err != nil {
		return Summary{}, err
	}
	admins, err := readAdmins(filepath.Join(config.sourceDir, "admin1CodesASCII.txt"), config.countries)
	if err != nil {
		return Summary{}, err
	}

	places := make(map[int64]*place, config.limits.minPlaces)
	if err := readPlaces(ctx, filepath.Join(config.sourceDir, "allCountries.zip"), config, countries, places); err != nil {
		return Summary{}, err
	}
	if err := readPreferredNames(ctx, filepath.Join(config.sourceDir, "alternateNamesV2.zip"), countries, config.retainAllWithin, places); err != nil {
		return Summary{}, err
	}
	if len(places) < config.limits.minPlaces || len(places) > config.limits.maxPlaces {
		return Summary{}, fmt.Errorf("place count %d is outside gate %d..%d", len(places), config.limits.minPlaces, config.limits.maxPlaces)
	}

	shards := make(map[string][]record)
	ids := make([]int64, 0, len(places))
	includedCountries := make(map[string]bool)
	for id := range places {
		ids = append(ids, id)
		includedCountries[places[id].country] = true
	}
	slices.Sort(ids)
	searchKeyCount := 0
	for placeIndex, id := range ids {
		if placeIndex%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return Summary{}, err
			}
		}
		place := places[id]
		displayName := place.name
		if place.localName != "" && normalize(place.localName) != "" {
			displayName = place.localName
		}
		keys := uniqueKeys(place.name, place.asciiName, place.localName, place.preferredAlias)
		for _, key := range keys {
			shardName := shardName(key)
			shards[shardName] = append(shards[shardName], record{
				Key: key, Name: displayName, Country: place.country, Admin1: place.admin1,
				LatitudeE5: place.latitudeE5, LongitudeE5: place.longitudeE5,
				Population: place.population, FeatureRank: place.featureRank, GeoNameID: place.id,
			})
			searchKeyCount++
		}
	}
	if len(shards) == 0 || len(shards) > config.limits.maxShards {
		return Summary{}, fmt.Errorf("shard count %d exceeds gate 1..%d", len(shards), config.limits.maxShards)
	}
	if err := verifyRequiredPlaces(shards, config.limits.required); err != nil {
		return Summary{}, err
	}

	staging, err := makeStaging(config.outputDir)
	if err != nil {
		return Summary{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(staging)
		}
	}()
	shardsDir := filepath.Join(staging, "shards")
	if err := os.Mkdir(shardsDir, 0o755); err != nil {
		return Summary{}, err
	}

	shardNames := make([]string, 0, len(shards))
	for name := range shards {
		shardNames = append(shardNames, name)
	}
	sort.Strings(shardNames)
	summary := Summary{PlaceCount: len(places), SearchKeyCount: searchKeyCount, ShardCount: len(shards)}
	shardDigests := make(map[string]string, len(shards))
	for _, name := range shardNames {
		if err := ctx.Err(); err != nil {
			return Summary{}, err
		}
		records := shards[name]
		sortRecords(records)
		payload, err := json.Marshal(shard{Version: 1, Records: records})
		if err != nil {
			return Summary{}, err
		}
		compressedBytes, err := gzipSize(payload)
		if err != nil {
			return Summary{}, err
		}
		if compressedBytes > config.limits.maxShardGzip {
			return Summary{}, fmt.Errorf("shard %s gzip size %d exceeds %d", name, compressedBytes, config.limits.maxShardGzip)
		}
		if err := os.WriteFile(filepath.Join(shardsDir, name+".json"), payload, 0o644); err != nil {
			return Summary{}, err
		}
		digest := sha256.Sum256(payload)
		shardDigests[name] = hex.EncodeToString(digest[:])
		summary.UncompressedBytes += int64(len(payload))
		summary.GzipBytes += compressedBytes
		summary.LargestShardGzip = max(summary.LargestShardGzip, compressedBytes)
	}

	countryLabels := make(map[string]string, len(includedCountries))
	for _, code := range config.countries {
		if includedCountries[code] {
			countryLabels[code] = countries[code].name
		}
	}
	includedAdmins := make(map[string]string)
	for code, label := range admins {
		if len(code) >= 3 && includedCountries[code[:2]] {
			includedAdmins[code] = label
		}
	}
	warranty := "GeoNames provides the data as-is without warranty of accuracy, timeliness or completeness."
	if config.sourceBundleSHA256 != "" {
		warranty += " Source bundle SHA-256: " + config.sourceBundleSHA256 + "."
	}
	manifestPayload, err := marshalManifest(manifest{
		Schema: schema,
		Source: sourceMetadata{
			Name: "GeoNames", ExtractDate: config.sourceDate,
			DataURL: "https://download.geonames.org/export/dump/",
			License: "CC BY 4.0", LicenseURL: "https://creativecommons.org/licenses/by/4.0/",
			Attribution: "Contains modified GeoNames data, licensed under CC BY 4.0.",
			Modified:    true, Warranty: warranty,
		},
		ProductBounds: config.coverage, PlaceCount: len(places), SearchKeyCount: searchKeyCount,
		ShardCount: len(shards), ShardPrefixLength: shardPrefixLength,
		MinimumQueryLength: minimumQueryLength, Normalization: normalization,
		Record:    []string{"key", "name", "country", "admin1", "latE5", "lonE5", "population", "featureRank", "geonameId"},
		Countries: countryLabels, Admins: includedAdmins, Shards: shardDigests, Summary: summary,
	})
	if err != nil {
		return Summary{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), manifestPayload, 0o644); err != nil {
		return Summary{}, err
	}
	bundleSHA256, err := validateGlobalSource(ctx, config.sourceDir, config.sourceDate)
	if err != nil {
		return Summary{}, err
	}
	if bundleSHA256 != config.sourceBundleSHA256 {
		return Summary{}, errors.New("GeoNames source bundle changed during build")
	}
	if err := validateOutput(staging, config, summary); err != nil {
		return Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if err := os.Rename(staging, config.outputDir); err != nil {
		return Summary{}, err
	}
	complete = true
	return summary, nil
}

func marshalManifest(value manifest) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(payload) > maximumManifestBytes {
		return nil, fmt.Errorf("place manifest size %d exceeds %d", len(payload), maximumManifestBytes)
	}
	return payload, nil
}

func validateConfig(config buildConfig) error {
	if config.sourceDir == "" || config.outputDir == "" {
		return errors.New("source and output directories are required")
	}
	if _, err := time.Parse("2006-01-02", config.sourceDate); err != nil {
		return fmt.Errorf("source date must be YYYY-MM-DD: %w", err)
	}
	if len(config.countries) == 0 {
		return errors.New("at least one country is required")
	}
	if config.coverage.West >= config.coverage.East || config.coverage.South >= config.coverage.North || config.coverage.West < -180 || config.coverage.East > 180 || config.coverage.South < -90 || config.coverage.North > 90 {
		return errors.New("coverage bounds are invalid")
	}
	if config.retainAllWithin != nil && (config.retainAllWithin.West >= config.retainAllWithin.East || config.retainAllWithin.South >= config.retainAllWithin.North || config.retainAllWithin.West < config.coverage.West || config.retainAllWithin.East > config.coverage.East || config.retainAllWithin.South < config.coverage.South || config.retainAllWithin.North > config.coverage.North) {
		return errors.New("refinement bounds are invalid")
	}
	if config.minimumPopulationOutsideArea < 0 {
		return errors.New("minimum population is invalid")
	}
	if config.limits.minPlaces < 1 || config.limits.maxPlaces < config.limits.minPlaces || config.limits.maxShards < 1 || config.limits.maxShardGzip < 1 {
		return errors.New("build gates are invalid")
	}
	if _, err := os.Stat(config.outputDir); err == nil {
		return fmt.Errorf("output already exists: %s", config.outputDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readCountries(path string) (map[string]country, error) {
	result := make(map[string]country)
	err := readLines(path, func(line int, fields []string) error {
		if strings.HasPrefix(fields[0], "#") {
			return nil
		}
		if len(fields) != 19 {
			return fmt.Errorf("line %d: country row has %d fields", line, len(fields))
		}
		name, err := canonicalDisplayLabel(fields[4])
		if err != nil {
			return fmt.Errorf("line %d: invalid country label: %w", line, err)
		}
		languages := make([]string, 0)
		for language := range strings.SplitSeq(fields[15], ",") {
			language = strings.Split(language, "-")[0]
			if language != "" && !slices.Contains(languages, language) {
				languages = append(languages, language)
			}
		}
		result[fields[0]] = country{name: name, continent: fields[8], languages: languages}
		return nil
	})
	return result, err
}

type globalSourceManifest struct {
	Schema      string            `json:"schema"`
	ExtractDate string            `json:"extractDate"`
	Files       map[string]string `json:"files"`
}

func validateGlobalSource(ctx context.Context, sourceDir, sourceDate string) (string, error) {
	payload, err := os.ReadFile(filepath.Join(sourceDir, globalSourceManifestName))
	if err != nil {
		return "", fmt.Errorf("read GeoNames source manifest: %w", err)
	}
	if len(payload) > maximumSourceManifestBytes {
		return "", errors.New("GeoNames source manifest is too large")
	}
	var source globalSourceManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil || decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("GeoNames source manifest is invalid")
	}
	if source.Schema != globalSourceSchema || source.ExtractDate != sourceDate || len(source.Files) != len(globalSourceFiles) {
		return "", errors.New("GeoNames source manifest does not match the requested extract")
	}
	bundle := sha256.New()
	for _, name := range globalSourceFiles {
		expected := source.Files[name]
		if len(expected) != 64 {
			return "", fmt.Errorf("GeoNames source manifest has invalid digest for %s", name)
		}
		if _, err := hex.DecodeString(expected); err != nil {
			return "", fmt.Errorf("GeoNames source manifest has invalid digest for %s", name)
		}
		actual, err := hashRegularFile(ctx, filepath.Join(sourceDir, name))
		if err != nil {
			return "", fmt.Errorf("hash GeoNames source %s: %w", name, err)
		}
		if actual != expected {
			return "", fmt.Errorf("GeoNames source %s checksum does not match manifest", name)
		}
		_, _ = io.WriteString(bundle, name+"\x00"+actual+"\x00")
	}
	return hex.EncodeToString(bundle.Sum(nil)), nil
}

func hashRegularFile(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("source must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("source changed while opening")
	}
	digest := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = digest.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func readAdmins(path string, countries []string) (map[string]string, error) {
	allowed := make(map[string]bool, len(countries))
	for _, code := range countries {
		allowed[code] = true
	}
	result := make(map[string]string)
	err := readLines(path, func(line int, fields []string) error {
		if len(fields) != 4 {
			return fmt.Errorf("line %d: admin row has %d fields", line, len(fields))
		}
		if len(fields[0]) >= 3 && allowed[fields[0][:2]] {
			label, err := canonicalDisplayLabel(fields[1])
			if err != nil {
				return fmt.Errorf("line %d: invalid admin label: %w", line, err)
			}
			result[fields[0]] = label
		}
		return nil
	})
	return result, err
}

func canonicalDisplayLabel(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("label is not valid UTF-8")
	}
	value = strings.TrimFunc(value, browserTrimSpace)
	if value == "" {
		return "", errors.New("label is empty")
	}
	units := 0
	for _, current := range value {
		if unicode.IsControl(current) {
			return "", errors.New("label contains a control character")
		}
		units += utf16.RuneLen(current)
	}
	if units > maximumDisplayLabelUnits {
		return "", fmt.Errorf("label exceeds %d UTF-16 units", maximumDisplayLabelUnits)
	}
	return value, nil
}

func browserTrimSpace(value rune) bool {
	switch value {
	case '\u0009', '\u000a', '\u000b', '\u000c', '\u000d', '\u0020', '\u00a0', '\u1680',
		'\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	default:
		return value >= '\u2000' && value <= '\u200a'
	}
}

func readPlaces(ctx context.Context, path string, config buildConfig, countries map[string]country, places map[int64]*place) error {
	return readZipLines(path, "allCountries.txt", func(line int, fields []string) error {
		if line%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if len(fields) != 19 {
			return fmt.Errorf("line %d: place row has %d fields", line, len(fields))
		}
		if fields[6] != "P" {
			return nil
		}
		featureRank, include := includedFeatureCodes[fields[7]]
		if fields[7] == "" || excludedFeatureCodes[fields[7]] {
			return nil
		}
		if !include {
			return fmt.Errorf("line %d: unknown populated-place feature code %q", line, fields[7])
		}
		countryCode := fields[8]
		if _, ok := countries[countryCode]; !ok {
			return fmt.Errorf("line %d: country metadata missing %q", line, countryCode)
		}
		id, latitude, longitude, population, err := parsePlaceFields(line, fields)
		if err != nil {
			return err
		}
		if !contains(config.coverage, latitude, longitude) || !retainPlace(config, countries[countryCode], latitude, longitude, population, featureRank) {
			return nil
		}
		if normalize(fields[2]) == "" && normalize(fields[1]) == "" {
			return fmt.Errorf("line %d: place %d has no searchable name", line, id)
		}
		if _, exists := places[id]; exists {
			return fmt.Errorf("line %d: duplicate geoname id %d", line, id)
		}
		places[id] = &place{
			id: id, name: fields[1], asciiName: fields[2], country: countryCode,
			admin1: fields[10], latitudeE5: int32(math.Round(latitude * 100_000)),
			longitudeE5: int32(math.Round(longitude * 100_000)), population: population,
			featureRank: featureRank, localPriority: math.MaxInt,
		}
		return nil
	})
}

func parsePlaceFields(line int, fields []string) (int64, float64, float64, int64, error) {
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("line %d: invalid geoname id %q", line, fields[0])
	}
	latitude, err := strconv.ParseFloat(fields[4], 64)
	if err != nil || math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
		return 0, 0, 0, 0, fmt.Errorf("line %d: invalid latitude %q", line, fields[4])
	}
	longitude, err := strconv.ParseFloat(fields[5], 64)
	if err != nil || math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
		return 0, 0, 0, 0, fmt.Errorf("line %d: invalid longitude %q", line, fields[5])
	}
	if longitude == 180 {
		longitude = -180
	}
	population := int64(0)
	if fields[14] != "" {
		population, err = strconv.ParseInt(fields[14], 10, 64)
		if err != nil || population < 0 {
			return 0, 0, 0, 0, fmt.Errorf("line %d: invalid population %q", line, fields[14])
		}
	}
	return id, latitude, longitude, population, nil
}

func contains(area bounds, latitude, longitude float64) bool {
	return longitude >= area.West && longitude < area.East && latitude > area.South && latitude <= area.North
}

func retainPlace(config buildConfig, country country, latitude, longitude float64, population int64, featureRank uint8) bool {
	return country.continent != "AF" && config.retainAllWithin != nil && contains(*config.retainAllWithin, latitude, longitude) ||
		population >= config.minimumPopulationOutsideArea || featureRank >= 4
}

func readPreferredNames(ctx context.Context, path string, countries map[string]country, refinement *bounds, places map[int64]*place) error {
	include := func(place *place) bool {
		return countries[place.country].continent != "AF" && refinement != nil && contains(*refinement, float64(place.latitudeE5)/100_000, float64(place.longitudeE5)/100_000)
	}
	return readPreferredNameRows(path, "alternateNamesV2.txt", ctx, include, func(place *place) []string {
		return countries[place.country].languages
	}, places)
}

func readPreferredNameRows(path, member string, ctx context.Context, include func(*place) bool, languagesFor func(*place) []string, places map[int64]*place) error {
	prioritiesByCountry := make(map[string]map[string]int)
	return readZipLines(path, member, func(line int, fields []string) error {
		if line%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if len(fields) < 8 || len(fields) > 10 {
			return fmt.Errorf("line %d: alternate-name row has %d fields", line, len(fields))
		}
		alternateID, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || alternateID <= 0 {
			return fmt.Errorf("line %d: invalid alternate-name id %q", line, fields[0])
		}
		id, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("line %d: invalid alternate geoname id %q", line, fields[1])
		}
		place, ok := places[id]
		if !ok || include != nil && !include(place) {
			return nil
		}
		priorities, ok := prioritiesByCountry[place.country]
		if !ok {
			languages := languagesFor(place)
			priorities = make(map[string]int, len(languages))
			for priority, language := range languages {
				priorities[language] = priority
			}
			prioritiesByCountry[place.country] = priorities
		}
		for _, position := range []int{4, 5, 6, 7} {
			if fields[position] != "" && fields[position] != "1" {
				return fmt.Errorf("line %d: invalid alternate-name flag %q", line, fields[position])
			}
		}
		if fields[4] != "1" || fields[6] == "1" || fields[7] == "1" {
			return nil
		}
		language := strings.Split(fields[2], "-")[0]
		name := fields[3]
		if language == "en" && betterName(name, place.preferredAlias) {
			place.preferredAlias = name
		}
		if priority, ok := priorities[language]; ok && normalize(name) != "" && (priority < place.localPriority || priority == place.localPriority && betterName(name, place.localName)) {
			place.localPriority = priority
			place.localName = name
		}
		return nil
	})
}

func betterName(candidate, current string) bool {
	return current == "" || candidate < current
}

func uniqueKeys(names ...string) []string {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if key := normalize(name); key != "" {
			set[key] = true
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalize(value string) string {
	value = strings.ToLower(norm.NFKD.String(value))
	var result strings.Builder
	space := true
	for _, current := range value {
		if unicode.Is(unicode.Mn, current) {
			continue
		}
		folded := foldSpecial(current)
		for _, item := range folded {
			if item >= 'a' && item <= 'z' || item >= '0' && item <= '9' {
				result.WriteRune(item)
				space = false
			} else if !space && result.Len() > 0 {
				result.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(result.String())
}

func foldSpecial(value rune) string {
	switch value {
	case 'ł':
		return "l"
	case 'ø':
		return "o"
	case 'đ', 'ð':
		return "d"
	case 'þ':
		return "th"
	case 'æ':
		return "ae"
	case 'œ':
		return "oe"
	case 'ß':
		return "ss"
	case 'ħ':
		return "h"
	case 'ı':
		return "i"
	case 'ŧ':
		return "t"
	default:
		return string(value)
	}
}

func shardName(key string) string {
	padded := key + strings.Repeat("_", shardPrefixLength)
	return strings.ReplaceAll(padded[:shardPrefixLength], " ", "_")
}

func verifyRequiredPlaces(shards map[string][]record, required []requiredPlace) error {
	for _, fixture := range required {
		for _, query := range fixture.queries {
			key := normalize(query)
			found := false
			for _, record := range shards[shardName(key)] {
				if record.GeoNameID == fixture.id && record.Key == key {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("required query %q does not resolve geoname id %d", query, fixture.id)
			}
		}
	}
	return nil
}

func sortRecords(records []record) {
	sort.Slice(records, func(left, right int) bool {
		a, b := records[left], records[right]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.FeatureRank != b.FeatureRank {
			return a.FeatureRank > b.FeatureRank
		}
		if a.Population != b.Population {
			return a.Population > b.Population
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.GeoNameID < b.GeoNameID
	})
}

func gzipSize(payload []byte) (int64, error) {
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	writer.Header.ModTime = time.Time{}
	writer.Header.OS = 255
	if _, err := writer.Write(payload); err != nil {
		return 0, err
	}
	if err := writer.Close(); err != nil {
		return 0, err
	}
	return int64(output.Len()), nil
}

func makeStaging(output string) (string, error) {
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "."+filepath.Base(output)+".building-")
}

func validateOutput(root string, config buildConfig, want Summary) error {
	validated, err := ValidateRelease(root)
	if err != nil {
		return err
	}
	if validated != want {
		return fmt.Errorf("generated place summary %+v does not match expected %+v", validated, want)
	}
	payload, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return err
	}
	var got manifest
	if err := json.Unmarshal(payload, &got); err != nil {
		return err
	}
	if got.Schema != schema || got.Source.ExtractDate != config.sourceDate || got.Source.License != "CC BY 4.0" || !got.Source.Modified || got.ProductBounds != config.coverage || got.Summary != want || got.ShardCount != want.ShardCount || len(got.Shards) != want.ShardCount {
		return errors.New("generated manifest failed contract validation")
	}
	return nil
}

func readLines(path string, consume func(int, []string) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return scanLines(file, consume)
}

func readZipLines(path, member string, consume func(int, []string) error) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	var selected *zip.File
	for _, candidate := range archive.File {
		if filepath.Base(candidate.Name) == member {
			if selected != nil {
				return fmt.Errorf("archive %s contains duplicate member %s", path, member)
			}
			selected = candidate
		}
	}
	if selected == nil {
		return fmt.Errorf("archive %s is missing %s", path, member)
	}
	reader, err := selected.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	return scanLines(reader, consume)
}

func scanLines(reader io.Reader, consume func(int, []string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSuffix(scanner.Text(), "\r")
		if text == "" {
			continue
		}
		if err := consume(line, strings.Split(text, "\t")); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
