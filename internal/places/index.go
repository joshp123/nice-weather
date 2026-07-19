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

	"golang.org/x/text/unicode/norm"
)

const (
	schema               = "nice-places-v1"
	normalization        = "nice-latin-fold-v1"
	shardPrefixLength    = 3
	minimumQueryLength   = 3
	maximumManifestBytes = 2 * 1024 * 1024
)

var europeCountries = []string{
	"AD", "AL", "AM", "AT", "AX", "AZ", "BA", "BE", "BG", "BY", "CH", "CY", "CZ", "DE", "DK", "EE", "ES", "FI", "FO", "FR", "GB", "GE", "GG", "GI", "GR", "HR", "HU", "IE", "IM", "IS", "IT", "JE", "LI", "LT", "LU", "LV", "MC", "MD", "ME", "MK", "MT", "NL", "NO", "PL", "PT", "RO", "RS", "RU", "SE", "SI", "SK", "SM", "TR", "UA", "VA", "XK",
}

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
	minPlaces        int
	maxPlaces        int
	maxShards        int
	maxShardGzip     int64
	required         []requiredPlace
	requireCountries bool
}

type buildConfig struct {
	sourceDir  string
	outputDir  string
	sourceDate string
	countries  []string
	coverage   bounds
	limits     limits
}

var europeLimits = limits{
	minPlaces:        500_000,
	maxPlaces:        1_100_000,
	maxShards:        7_500,
	maxShardGzip:     256 * 1024,
	requireCountries: true,
	required: []requiredPlace{
		{id: 2750053, queries: []string{"Nijmegen"}},
		{id: 611717, queries: []string{"Tbilisi"}},
		{id: 2520600, queries: []string{"Cádiz", "Cadiz"}},
		{id: 3093133, queries: []string{"Łódź", "Lodz"}},
		{id: 2514256, queries: []string{"Málaga", "Malaga"}},
		{id: 2655710, queries: []string{"Biddestone"}},
	},
}

// BuildEurope consumes pinned GeoNames country extracts and writes one complete
// immutable release. It never downloads sources or updates a public pointer.
func BuildEurope(ctx context.Context, sourceDir, outputDir, sourceDate string) (Summary, error) {
	return build(ctx, buildConfig{
		sourceDir:  sourceDir,
		outputDir:  outputDir,
		sourceDate: sourceDate,
		countries:  europeCountries,
		coverage:   bounds{West: -24, East: 45, South: 29, North: 71.5},
		limits:     europeLimits,
	})
}

type country struct {
	name      string
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
	seenCountries := make(map[string]bool, len(config.countries))
	for _, countryCode := range config.countries {
		if err := ctx.Err(); err != nil {
			return Summary{}, err
		}
		country, ok := countries[countryCode]
		if !ok {
			return Summary{}, fmt.Errorf("country metadata missing %s", countryCode)
		}
		path := filepath.Join(config.sourceDir, "countries", countryCode+".zip")
		countryPlaceCount, err := readPlaces(path, countryCode, config.coverage, places)
		if err != nil {
			return Summary{}, err
		}
		if countryPlaceCount > 0 {
			seenCountries[countryCode] = true
		}
		alternatePath := filepath.Join(config.sourceDir, "alternatenames", countryCode+".zip")
		if err := readPreferredNames(alternatePath, countryCode, country.languages, places); err != nil {
			return Summary{}, err
		}
	}
	if config.limits.requireCountries && len(seenCountries) != len(config.countries) {
		return Summary{}, fmt.Errorf("only %d of %d countries contain places inside coverage", len(seenCountries), len(config.countries))
	}
	if len(places) < config.limits.minPlaces || len(places) > config.limits.maxPlaces {
		return Summary{}, fmt.Errorf("place count %d is outside gate %d..%d", len(places), config.limits.minPlaces, config.limits.maxPlaces)
	}

	shards := make(map[string][]record)
	ids := make([]int64, 0, len(places))
	for id := range places {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	searchKeyCount := 0
	for _, id := range ids {
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

	countryLabels := make(map[string]string, len(seenCountries))
	for _, code := range config.countries {
		if seenCountries[code] {
			countryLabels[code] = countries[code].name
		}
	}
	manifestPayload, err := marshalManifest(manifest{
		Schema: schema,
		Source: sourceMetadata{
			Name: "GeoNames", ExtractDate: config.sourceDate,
			DataURL: "https://download.geonames.org/export/dump/",
			License: "CC BY 4.0", LicenseURL: "https://creativecommons.org/licenses/by/4.0/",
			Attribution: "Contains modified GeoNames data, licensed under CC BY 4.0.",
			Modified:    true, Warranty: "GeoNames provides the data as-is without warranty of accuracy, timeliness or completeness.",
		},
		ProductBounds: config.coverage, PlaceCount: len(places), SearchKeyCount: searchKeyCount,
		ShardCount: len(shards), ShardPrefixLength: shardPrefixLength,
		MinimumQueryLength: minimumQueryLength, Normalization: normalization,
		Record:    []string{"key", "name", "country", "admin1", "latE5", "lonE5", "population", "featureRank", "geonameId"},
		Countries: countryLabels, Admins: admins, Shards: shardDigests, Summary: summary,
	})
	if err != nil {
		return Summary{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), manifestPayload, 0o644); err != nil {
		return Summary{}, err
	}
	if err := validateOutput(staging, config, summary); err != nil {
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
		languages := make([]string, 0)
		for language := range strings.SplitSeq(fields[15], ",") {
			language = strings.Split(language, "-")[0]
			if language != "" && !slices.Contains(languages, language) {
				languages = append(languages, language)
			}
		}
		result[fields[0]] = country{name: fields[4], languages: languages}
		return nil
	})
	return result, err
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
			result[fields[0]] = fields[1]
		}
		return nil
	})
	return result, err
}

func readPlaces(path, countryCode string, coverage bounds, places map[int64]*place) (int, error) {
	count := 0
	err := readZipLines(path, countryCode+".txt", func(line int, fields []string) error {
		if len(fields) != 19 {
			return fmt.Errorf("line %d: place row has %d fields", line, len(fields))
		}
		if fields[6] != "P" {
			return nil
		}
		featureRank, include := includedFeatureCodes[fields[7]]
		if excludedFeatureCodes[fields[7]] {
			return nil
		}
		if !include {
			return fmt.Errorf("line %d: unknown populated-place feature code %q", line, fields[7])
		}
		if fields[8] != countryCode {
			return fmt.Errorf("line %d: country %q does not match archive %s", line, fields[8], countryCode)
		}
		id, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("line %d: invalid geoname id %q", line, fields[0])
		}
		latitude, err := strconv.ParseFloat(fields[4], 64)
		if err != nil || latitude < -90 || latitude > 90 {
			return fmt.Errorf("line %d: invalid latitude %q", line, fields[4])
		}
		longitude, err := strconv.ParseFloat(fields[5], 64)
		if err != nil || longitude < -180 || longitude > 180 {
			return fmt.Errorf("line %d: invalid longitude %q", line, fields[5])
		}
		if longitude < coverage.West || longitude >= coverage.East || latitude <= coverage.South || latitude > coverage.North {
			return nil
		}
		population := int64(0)
		if fields[14] != "" {
			population, err = strconv.ParseInt(fields[14], 10, 64)
			if err != nil || population < 0 {
				return fmt.Errorf("line %d: invalid population %q", line, fields[14])
			}
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
		count++
		return nil
	})
	return count, err
}

func readPreferredNames(path, countryCode string, languages []string, places map[int64]*place) error {
	priorities := make(map[string]int, len(languages))
	for priority, language := range languages {
		priorities[language] = priority
	}
	return readZipLines(path, countryCode+".txt", func(line int, fields []string) error {
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
		if !ok {
			return nil
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
	entries, err := os.ReadDir(filepath.Join(root, "shards"))
	if err != nil {
		return err
	}
	if len(entries) != want.ShardCount {
		return fmt.Errorf("generated %d shard files, expected %d", len(entries), want.ShardCount)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return fmt.Errorf("unexpected shard entry %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, "shards", entry.Name()))
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		wantDigest, ok := got.Shards[name]
		if !ok || len(wantDigest) != sha256.Size*2 {
			return fmt.Errorf("shard %q has no valid manifest digest", entry.Name())
		}
		if _, err := hex.DecodeString(wantDigest); err != nil {
			return fmt.Errorf("shard %q has invalid manifest digest: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != wantDigest {
			return fmt.Errorf("shard %q checksum does not match manifest", entry.Name())
		}
		var decoded shard
		if err := json.Unmarshal(data, &decoded); err != nil || decoded.Version != 1 || len(decoded.Records) == 0 {
			return fmt.Errorf("invalid shard %q", entry.Name())
		}
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
