package places

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizePlaceNames(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Cádiz":                   "cadiz",
		"Łódź":                    "lodz",
		"Málaga":                  "malaga",
		"Rødovre":                 "rodovre",
		"Straße":                  "strasse",
		"  Saint-Jean  d'Angély ": "saint jean d angely",
		"თბილისი":                 "",
	}
	for input, want := range tests {
		if got := normalize(input); got != want {
			t.Errorf("normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGeoNamesDisplayLabelsAreCanonicalAtIngestion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fields := make([]string, 19)
	fields[0], fields[4], fields[8], fields[15] = "BQ", "Bonaire, Saint Eustatius and Saba ", "NA", "nl,pap,en"
	if err := os.WriteFile(filepath.Join(root, "countryInfo.txt"), []byte(strings.Join(fields, "\t")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "admin1CodesASCII.txt"), []byte("BQ.01\tBonaire \tBonaire\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	countries, err := readCountries(filepath.Join(root, "countryInfo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	admins, err := readAdmins(filepath.Join(root, "admin1CodesASCII.txt"), []string{"BQ"})
	if err != nil {
		t.Fatal(err)
	}
	if countries["BQ"].name != "Bonaire, Saint Eustatius and Saba" || admins["BQ.01"] != "Bonaire" {
		t.Fatalf("labels were not canonicalised: country=%q admin=%q", countries["BQ"].name, admins["BQ.01"])
	}
}

func TestGeoNamesDisplayLabelsRejectInvalidBrowserText(t *testing.T) {
	t.Parallel()
	trimmed, err := canonicalDisplayLabel("\ufeff Bonaire \ufeff")
	if err != nil || trimmed != "Bonaire" {
		t.Fatalf("browser whitespace trim = %q, %v", trimmed, err)
	}
	for name, value := range map[string]string{
		"empty":         " \t ",
		"browser empty": "\ufeff",
		"control":       "Bonaire\x00",
		"C1 control":    "Bonaire\u0080",
		"C1 whitespace": "Bonaire\u0085",
		"overlong":      strings.Repeat("x", maximumDisplayLabelUnits+1),
		"invalid UTF8":  string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := canonicalDisplayLabel(value); err == nil {
				t.Fatalf("canonicalDisplayLabel(%q) succeeded", value)
			}
		})
	}
}

func TestGlobalBuildPreservesEuropeanDetailAndCoversTheWorld(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 2750053, name: "Nijmegen", country: "NL", latitude: "51.8425", longitude: "5.8528", code: "PPLA2", population: "177659"},
		{id: 2655710, name: "Biddestone", country: "GB", latitude: "51.4608", longitude: "-2.1983", code: "PPL", population: "498"},
		{id: 5128581, name: "New York City", country: "US", latitude: "40.7143", longitude: "-74.006", code: "PPLA", population: "8804190"},
		{id: 3448439, name: "São Paulo", ascii: "Sao Paulo", country: "BR", latitude: "-23.5475", longitude: "-46.6361", code: "PPLA", population: "12400232"},
		{id: 3369157, name: "Cape Town", country: "ZA", latitude: "-33.9258", longitude: "18.4232", code: "PPLA", population: "3433441"},
		{id: 1880252, name: "Singapore", country: "SG", latitude: "1.2897", longitude: "103.8501", code: "PPLC", population: "5638700"},
		{id: 2147714, name: "Sydney", country: "AU", latitude: "-33.8679", longitude: "151.2073", code: "PPLA", population: "4627345"},
		{id: 2193733, name: "Auckland", country: "NZ", latitude: "-36.8485", longitude: "174.7635", code: "PPLA", population: "417910"},
		{id: 2127202, name: "Anadyr", country: "RU", latitude: "64.7342", longitude: "177.5103", code: "PPLA", population: "10332"},
		{id: 9000003, name: "Dateline East", country: "NZ", latitude: "-20", longitude: "180", code: "PPL", population: "2000"},
		{id: 9000004, name: "Dateline West", country: "NZ", latitude: "-21", longitude: "-180", code: "PPL", population: "2000"},
		{id: 9000001, name: "Tiny Outside", country: "US", latitude: "20", longitude: "-80", code: "PPL", population: "1499"},
		{id: 9000002, name: "Small Admin", country: "US", latitude: "21", longitude: "-81", code: "PPLA", population: "0"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	refinement := bounds{West: -24, East: 45, South: 29, North: 71.5}
	config := buildConfig{
		sourceDir: source, outputDir: output, sourceDate: "2026-07-20",
		countries:       []string{"AU", "BR", "GB", "NL", "NZ", "RU", "SG", "US", "ZA"},
		coverage:        bounds{West: -180, East: 180, South: -90, North: 90},
		retainAllWithin: &refinement, minimumPopulationOutsideArea: 1_500,
		limits: limits{minPlaces: 1, maxPlaces: 20, maxShards: 30, maxShardGzip: 256 * 1024},
	}
	bundleSHA256, err := validateGlobalSource(context.Background(), source, config.sourceDate)
	if err != nil {
		t.Fatal(err)
	}
	config.sourceBundleSHA256 = bundleSHA256
	summary, err := build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if summary.PlaceCount != 12 {
		t.Fatalf("global place count = %d, want 12", summary.PlaceCount)
	}
	files := readTree(t, filepath.Dir(output), filepath.Base(output))
	for query, id := range map[string]int64{
		"Biddestone": 2655710, "New York City": 5128581, "Sao Paulo": 3448439,
		"Cape Town": 3369157, "Singapore": 1880252, "Sydney": 2147714,
		"Auckland": 2193733, "Anadyr": 2127202, "Small Admin": 9000002,
		"Dateline East": 9000003, "Dateline West": 9000004,
	} {
		assertShardResolves(t, files, query, id)
	}
	if _, exists := files["shards/tin.json"]; exists {
		t.Fatal("sub-1,500 settlement outside the European refinement was indexed")
	}
	var metadata manifest
	if err := json.Unmarshal(files["manifest.json"], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.ProductBounds != config.coverage || len(metadata.Countries) != len(config.countries) {
		t.Fatalf("global manifest coverage is incomplete: bounds=%+v countries=%d", metadata.ProductBounds, len(metadata.Countries))
	}
	for _, query := range []string{"Dateline East", "Dateline West"} {
		key := normalize(query)
		var decoded shard
		if err := json.Unmarshal(files["shards/"+shardName(key)+".json"], &decoded); err != nil {
			t.Fatal(err)
		}
		for _, item := range decoded.Records {
			if item.Key == key && item.LongitudeE5 != -18_000_000 {
				t.Fatalf("%s longitude = %d, want canonical -180 degrees", query, item.LongitudeE5)
			}
		}
	}
}

func TestGlobalBuildIndexesWorldwideEnglishNamesWithoutChangingCanonicalLabels(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 2542997, name: "Marrakesh", country: "MA", latitude: "31.63416", longitude: "-7.99994", code: "PPLA", population: "995871"},
		{id: 1205733, name: "Chattogram", country: "BD", latitude: "22.3384", longitude: "91.83168", code: "PPLA", population: "3920222"},
		{id: 104515, name: "Makkah", country: "SA", latitude: "21.42664", longitude: "39.82563", code: "PPLA", population: "1578722"},
	}, []string{
		alternateRow(2542997, "en", "Marrakech", false, false, false),
		alternateRow(2542997, "en", "Red City", false, false, false),
		alternateRow(2542997, "en", "Old Marrakech", false, false, true),
		alternateRow(1205733, "en", "Chittagong", true, false, false),
		alternateRow(104515, "en", "Mecca", true, false, false),
	})
	output := filepath.Join(t.TempDir(), "release")
	refinement := bounds{West: -24, East: 45, South: 29, North: 71.5}
	config := buildConfig{
		sourceDir: source, outputDir: output, sourceDate: "2026-07-20",
		countries: []string{"BD", "MA", "SA"}, coverage: bounds{West: -180, East: 180, South: -90, North: 90},
		retainAllWithin: &refinement, minimumPopulationOutsideArea: 1_500,
		limits: limits{minPlaces: 1, maxPlaces: 10, maxShards: 20, maxShardGzip: 256 * 1024},
	}
	bundleSHA256, err := validateGlobalSource(context.Background(), source, config.sourceDate)
	if err != nil {
		t.Fatal(err)
	}
	config.sourceBundleSHA256 = bundleSHA256
	if _, err := build(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	files := readTree(t, filepath.Dir(output), filepath.Base(output))
	for query, id := range map[string]int64{
		"Marrakech":  2542997,
		"Chittagong": 1205733,
		"Mecca":      104515,
	} {
		assertShardResolves(t, files, query, id)
	}
	var marrakech shard
	if err := json.Unmarshal(files["shards/mar.json"], &marrakech); err != nil {
		t.Fatal(err)
	}
	for _, item := range marrakech.Records {
		if item.GeoNameID == 2542997 && item.Name != "Marrakesh" {
			t.Fatalf("English alias changed canonical result label to %q", item.Name)
		}
	}
	for _, excluded := range []string{"Red City", "Old Marrakech"} {
		key := normalize(excluded)
		if payload, exists := files["shards/"+shardName(key)+".json"]; exists && bytes.Contains(payload, []byte(`"`+key+`"`)) {
			t.Fatalf("non-variant alias %q was indexed", excluded)
		}
	}
}

func TestCloseLatinVariant(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		candidate string
		canonical string
		want      bool
	}{
		{candidate: "Marrakech", canonical: "Marrakesh", want: true},
		{candidate: "St Petersburg", canonical: "Saint Petersburg", want: true},
		{candidate: "Odessa", canonical: "Odesa", want: true},
		{candidate: "Jo'anna", canonical: "Johannesburg", want: false},
		{candidate: "Zion", canonical: "Jerusalem", want: false},
		{candidate: "L.A.", canonical: "Los Angeles", want: false},
	} {
		if got := closeLatinVariant(test.candidate, test.canonical); got != test.want {
			t.Errorf("closeLatinVariant(%q, %q) = %v, want %v", test.candidate, test.canonical, got, test.want)
		}
	}
}

func TestGlobalSourceManifestBindsEveryInput(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, nil, nil)
	if _, err := validateGlobalSource(context.Background(), source, "2026-07-20"); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(source, "countryInfo.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := validateGlobalSource(context.Background(), source, "2026-07-20"); err == nil || !strings.Contains(err.Error(), "checksum does not match") {
		t.Fatalf("tampered source error = %v", err)
	}
}

func TestGlobalBuildRejectsNonFiniteCoordinates(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 1, name: "Invalid", country: "US", latitude: "NaN", longitude: "-80", code: "PPL", population: "2000"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	refinement := bounds{West: -24, East: 45, South: 29, North: 71.5}
	config := buildConfig{
		sourceDir: source, outputDir: output, sourceDate: "2026-07-20",
		countries: []string{"US"}, coverage: bounds{West: -180, East: 180, South: -90, North: 90},
		retainAllWithin: &refinement, minimumPopulationOutsideArea: 1_500,
		limits: limits{minPlaces: 1, maxPlaces: 20, maxShards: 30, maxShardGzip: 256 * 1024},
	}
	bundleSHA256, err := validateGlobalSource(context.Background(), source, config.sourceDate)
	if err != nil {
		t.Fatal(err)
	}
	config.sourceBundleSHA256 = bundleSHA256
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), `invalid latitude "NaN"`) {
		t.Fatalf("non-finite coordinate error = %v", err)
	}
}

func TestBuildIsDeterministicAndHonoursSearchContract(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 2520600, name: "Cadiz", ascii: "Cadiz", latitude: "36.52672", longitude: "-6.2891", code: "PPLA2", population: "116979"},
		{id: 2514256, name: "Málaga", ascii: "Malaga", latitude: "36.72016", longitude: "-4.42034", code: "PPLA2", population: "592346"},
		{id: 9990001, name: "Old Cádiz", ascii: "Old Cadiz", latitude: "36.5", longitude: "-6.2", code: "PPLQ", population: "0"},
		{id: 9990002, name: "Outside", ascii: "Outside", latitude: "10", longitude: "10", code: "PPL", population: "10"},
	}, []string{
		alternateRow(2520600, "es", "Cádiz", true, false, false),
		alternateRow(2520600, "en", "Cadiz", true, false, false),
		alternateRow(2520600, "es", "La tacita de plata", false, false, false),
		alternateRow(2520600, "es", "Gades", true, false, true),
	})

	firstParent := t.TempDir()
	config := fixtureConfig(t, source, filepath.Join(firstParent, "release"))

	first, err := build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	secondParent := t.TempDir()
	config.outputDir = filepath.Join(secondParent, "release")
	second, err := build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("summaries differ: %+v != %+v", first, second)
	}
	firstFiles := readTree(t, firstParent, "release")
	secondFiles := readTree(t, secondParent, "release")
	if !reflect.DeepEqual(firstFiles, secondFiles) {
		t.Fatal("same source produced different output bytes")
	}
	if first.PlaceCount != 2 || first.SearchKeyCount != 2 || first.ShardCount != 2 {
		t.Fatalf("unexpected summary: %+v", first)
	}

	manifestPayload := firstFiles["manifest.json"]
	var metadata manifest
	if err := json.Unmarshal(manifestPayload, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Schema != schema || metadata.Source.License != "CC BY 4.0" || !metadata.Source.Modified || metadata.Source.ExtractDate != "2026-07-20" {
		t.Fatalf("source contract missing from manifest: %+v", metadata.Source)
	}
	if metadata.ProductBounds != config.coverage || metadata.Normalization != normalization || metadata.MinimumQueryLength != 3 {
		t.Fatalf("search contract mismatch: %+v", metadata)
	}
	if len(metadata.Shards) != first.ShardCount || metadata.Shards["cad"] != payloadSHA256(firstFiles["shards/cad.json"]) || metadata.Shards["mal"] != payloadSHA256(firstFiles["shards/mal.json"]) {
		t.Fatalf("manifest does not bind every shard: %+v", metadata.Shards)
	}

	var cadiz shard
	if err := json.Unmarshal(firstFiles["shards/cad.json"], &cadiz); err != nil {
		t.Fatal(err)
	}
	if len(cadiz.Records) != 1 || cadiz.Records[0].Key != "cadiz" || cadiz.Records[0].Name != "Cádiz" || cadiz.Records[0].GeoNameID != 2520600 {
		t.Fatalf("accented canonical result is wrong: %+v", cadiz.Records)
	}
	if _, exists := firstFiles["shards/old.json"]; exists {
		t.Fatal("abandoned place was indexed")
	}
	if _, exists := firstFiles["shards/out.json"]; exists {
		t.Fatal("out-of-coverage place was indexed")
	}
}

func TestValidateOutputRejectsStructurallyValidAlteredShard(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(t, source, output)
	summary, err := build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(output, "shards", "mal.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var altered shard
	if err := json.Unmarshal(payload, &altered); err != nil {
		t.Fatal(err)
	}
	altered.Records[0].Population++
	payload, err = json.Marshal(altered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(output, config, summary); err == nil || !strings.Contains(err.Error(), "checksum does not match manifest") {
		t.Fatalf("altered shard validation error = %v", err)
	}
}

func TestGlobalShardHeadroomFitsManifestBound(t *testing.T) {
	const currentShardCount = maximumShardCount
	places := make([]globalSourcePlace, 0, currentShardCount)
	for index := range currentShardCount {
		name := base36ShardName(index)
		places = append(places, globalSourcePlace{
			id: int64(index + 1), name: name, ascii: name,
			latitude: "36.7", longitude: "-4.4", code: "PPL", population: "1",
		})
	}
	source := makeGlobalSource(t, places, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(t, source, output)
	config.limits.maxPlaces = currentShardCount
	config.limits.maxShards = currentShardCount
	summary, err := build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata manifest
	if err := json.Unmarshal(payload, &metadata); err != nil {
		t.Fatal(err)
	}
	if summary.ShardCount != currentShardCount || len(metadata.Shards) != currentShardCount {
		t.Fatalf("full build contains %d summary shards and %d manifest digests, want %d", summary.ShardCount, len(metadata.Shards), currentShardCount)
	}
	if len(payload) > maximumManifestBytes {
		t.Fatalf("full %d-shard manifest requires %d bytes, exceeding the %d-byte client bound", currentShardCount, len(payload), maximumManifestBytes)
	}
	t.Logf("full %d-shard manifest is %d bytes (%.1f%% of the 2 MiB client bound)", currentShardCount, len(payload), 100*float64(len(payload))/maximumManifestBytes)
}

func TestMarshalManifestRejectsBrowserOverflow(t *testing.T) {
	t.Parallel()
	_, err := marshalManifest(manifest{Admins: map[string]string{
		"ES.overflow": strings.Repeat("x", maximumManifestBytes),
	}})
	if err == nil || !strings.Contains(err.Error(), "place manifest size") || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize manifest error = %v", err)
	}
}

func TestBuildRejectsShardSizeAndLeavesNoRelease(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(t, source, output)
	config.limits.maxShardGzip = 1
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), "gzip size") {
		t.Fatalf("oversize shard error = %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed build published output: %v", err)
	}
	building, err := filepath.Glob(filepath.Join(filepath.Dir(output), ".release.building-*"))
	if err != nil || len(building) != 0 {
		t.Fatalf("failed build left staging directories: %v, %v", building, err)
	}
}

func TestBuildRejectsShardCount(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, []globalSourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
		{id: 2, name: "Cádiz", ascii: "Cadiz", latitude: "36.5", longitude: "-6.2", code: "PPL", population: "100"},
	}, nil)
	config := fixtureConfig(t, source, filepath.Join(t.TempDir(), "release"))
	config.limits.maxShards = 1
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), "shard count 2") {
		t.Fatalf("shard-count error = %v", err)
	}
}

func TestBuildRejectsMalformedGeoNamesRows(t *testing.T) {
	t.Parallel()
	source := makeGlobalSource(t, nil, nil)
	config := fixtureConfig(t, source, filepath.Join(t.TempDir(), "release"))
	writeZip(t, filepath.Join(source, "allCountries.zip"), "allCountries.txt", "1\tshort\n")
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), "place row has 2 fields") {
		t.Fatalf("malformed source error = %v", err)
	}
}

type globalSourcePlace struct {
	id                                                          int64
	name, ascii, country, latitude, longitude, code, population string
}

func fixtureConfig(t *testing.T, source, output string) buildConfig {
	t.Helper()
	bundleSHA256, err := validateGlobalSource(context.Background(), source, "2026-07-20")
	if err != nil {
		t.Fatal(err)
	}
	return buildConfig{
		sourceDir: source, outputDir: output, sourceDate: "2026-07-20",
		countries: []string{"ES"}, coverage: bounds{West: -24, East: 45, South: 29, North: 71.5},
		retainAllWithin:    &bounds{West: -24, East: 45, South: 29, North: 71.5},
		sourceBundleSHA256: bundleSHA256,
		limits:             limits{minPlaces: 1, maxPlaces: 20, maxShards: 20, maxShardGzip: 256 * 1024},
	}
}

func makeGlobalSource(t *testing.T, places []globalSourcePlace, alternates []string) string {
	t.Helper()
	root := t.TempDir()
	countryNames := map[string]string{
		"AU": "Australia", "BR": "Brazil", "GB": "United Kingdom", "NL": "Netherlands",
		"NZ": "New Zealand", "RU": "Russia", "SG": "Singapore", "US": "United States", "ZA": "South Africa",
		"BD": "Bangladesh", "ES": "Spain", "MA": "Morocco", "SA": "Saudi Arabia",
	}
	continents := map[string]string{
		"GB": "EU", "NL": "EU", "RU": "EU", "AU": "OC", "BR": "SA",
		"NZ": "OC", "SG": "AS", "US": "NA", "ZA": "AF", "ES": "EU",
		"BD": "AS", "MA": "AF", "SA": "AS",
	}
	codes := make([]string, 0, len(countryNames))
	for code := range countryNames {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	countryRows := make([]string, 0, len(codes))
	adminRows := make([]string, 0, len(codes))
	for _, code := range codes {
		fields := make([]string, 19)
		languages := "en"
		if code == "ES" {
			languages = "es-ES,ca,gl,eu,oc"
		}
		fields[0], fields[4], fields[8], fields[15] = code, countryNames[code], continents[code], languages
		countryRows = append(countryRows, strings.Join(fields, "\t"))
		adminRows = append(adminRows, code+".01\tAdmin\tAdmin\t1")
	}
	if err := os.WriteFile(filepath.Join(root, "countryInfo.txt"), []byte(strings.Join(countryRows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "admin1CodesASCII.txt"), []byte(strings.Join(adminRows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 0, len(places))
	for _, place := range places {
		countryCode := place.country
		if countryCode == "" {
			countryCode = "ES"
		}
		ascii := place.ascii
		if ascii == "" {
			ascii = place.name
		}
		fields := []string{
			strconvFormat(place.id), place.name, ascii, "", place.latitude, place.longitude,
			"P", place.code, countryCode, "", "01", "", "", "", place.population, "", "10", "UTC", "2026-07-20",
		}
		rows = append(rows, strings.Join(fields, "\t"))
	}
	writeZip(t, filepath.Join(root, "allCountries.zip"), "allCountries.txt", strings.Join(rows, "\n")+"\n")
	writeZip(t, filepath.Join(root, "alternateNamesV2.zip"), "alternateNamesV2.txt", strings.Join(alternates, "\n")+"\n")
	files := make(map[string]string, len(globalSourceFiles))
	for _, name := range globalSourceFiles {
		digest, err := hashRegularFile(context.Background(), filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = digest
	}
	payload, err := json.Marshal(globalSourceManifest{Schema: globalSourceSchema, ExtractDate: "2026-07-20", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, globalSourceManifestName), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertShardResolves(t *testing.T, files map[string][]byte, query string, id int64) {
	t.Helper()
	key := normalize(query)
	var decoded shard
	if err := json.Unmarshal(files["shards/"+shardName(key)+".json"], &decoded); err != nil {
		t.Fatal(err)
	}
	for _, item := range decoded.Records {
		if item.Key == key && item.GeoNameID == id {
			return
		}
	}
	t.Fatalf("query %q does not resolve GeoNames ID %d", query, id)
}

func alternateRow(id int64, language, name string, preferred, colloquial, historic bool) string {
	flag := func(value bool) string {
		if value {
			return "1"
		}
		return ""
	}
	return strings.Join([]string{"1", strconvFormat(id), language, name, flag(preferred), "", flag(colloquial), flag(historic), "", ""}, "\t")
}

func strconvFormat(value int64) string {
	return strconv.FormatInt(value, 10)
}

func payloadSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func base36ShardName(value int) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	return string([]byte{
		alphabet[(value/(36*36))%36],
		alphabet[(value/36)%36],
		alphabet[value%36],
	})
}

func writeZip(t *testing.T, path, member, payload string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create(member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readTree(t *testing.T, parent, name string) map[string][]byte {
	t.Helper()
	root := filepath.Join(parent, name)
	result := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)], err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
