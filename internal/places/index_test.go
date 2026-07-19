package places

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeEuropeanNames(t *testing.T) {
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

func TestBuildIsDeterministicAndHonoursSearchContract(t *testing.T) {
	t.Parallel()
	source := makeSource(t, []sourcePlace{
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
	config := fixtureConfig(source, filepath.Join(firstParent, "release"))
	config.limits.required = []requiredPlace{
		{id: 2520600, queries: []string{"Cádiz", "Cadiz"}},
		{id: 2514256, queries: []string{"Málaga", "Malaga"}},
	}
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
	if metadata.Schema != schema || metadata.Source.License != "CC BY 4.0" || !metadata.Source.Modified || metadata.Source.ExtractDate != "2026-07-18" {
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
	source := makeSource(t, []sourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(source, output)
	config.limits.required = nil
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

func TestCurrentScaleGeneratedManifestFitsBrowserBound(t *testing.T) {
	const currentShardCount = 6_643
	places := make([]sourcePlace, 0, currentShardCount)
	for index := range currentShardCount {
		name := base36ShardName(index)
		places = append(places, sourcePlace{
			id: int64(index + 1), name: name, ascii: name,
			latitude: "36.7", longitude: "-4.4", code: "PPL", population: "1",
		})
	}
	source := makeSource(t, places, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(source, output)
	config.limits.maxPlaces = currentShardCount
	config.limits.maxShards = currentShardCount
	config.limits.required = nil
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
	source := makeSource(t, []sourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
	}, nil)
	output := filepath.Join(t.TempDir(), "release")
	config := fixtureConfig(source, output)
	config.limits.maxShardGzip = 1
	config.limits.required = nil
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
	source := makeSource(t, []sourcePlace{
		{id: 1, name: "Málaga", ascii: "Malaga", latitude: "36.7", longitude: "-4.4", code: "PPL", population: "100"},
		{id: 2, name: "Cádiz", ascii: "Cadiz", latitude: "36.5", longitude: "-6.2", code: "PPL", population: "100"},
	}, nil)
	config := fixtureConfig(source, filepath.Join(t.TempDir(), "release"))
	config.limits.maxShards = 1
	config.limits.required = nil
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), "shard count 2") {
		t.Fatalf("shard-count error = %v", err)
	}
}

func TestBuildRejectsMalformedGeoNamesRows(t *testing.T) {
	t.Parallel()
	source := makeSource(t, nil, nil)
	writeZip(t, filepath.Join(source, "countries", "ES.zip"), "ES.txt", "1\tshort\n")
	config := fixtureConfig(source, filepath.Join(t.TempDir(), "release"))
	config.limits.required = nil
	if _, err := build(context.Background(), config); err == nil || !strings.Contains(err.Error(), "place row has 2 fields") {
		t.Fatalf("malformed source error = %v", err)
	}
}

type sourcePlace struct {
	id                               int64
	name, ascii, latitude, longitude string
	code, population                 string
}

func fixtureConfig(source, output string) buildConfig {
	return buildConfig{
		sourceDir: source, outputDir: output, sourceDate: "2026-07-18",
		countries: []string{"ES"}, coverage: bounds{West: -24, East: 45, South: 29, North: 71.5},
		limits: limits{minPlaces: 1, maxPlaces: 20, maxShards: 20, maxShardGzip: 256 * 1024, requireCountries: true},
	}
}

func makeSource(t *testing.T, places []sourcePlace, alternates []string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "countries"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "alternatenames"), 0o755); err != nil {
		t.Fatal(err)
	}
	countryFields := []string{"ES", "ESP", "724", "SP", "Spain", "Madrid", "504782", "46723749", "EU", ".es", "EUR", "Euro", "34", "#####", "^(\\d{5})$", "es-ES,ca,gl,eu,oc", "2510769", "AD,PT,GI,FR,MA", ""}
	if err := os.WriteFile(filepath.Join(root, "countryInfo.txt"), []byte(strings.Join(countryFields, "\t")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "admin1CodesASCII.txt"), []byte("ES.51\tAndalusia\tAndalusia\t2593109\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 0, len(places))
	for _, place := range places {
		fields := []string{
			strconvFormat(place.id), place.name, place.ascii, "", place.latitude, place.longitude,
			"P", place.code, "ES", "", "51", "", "", "", place.population, "", "10", "Europe/Madrid", "2026-07-18",
		}
		rows = append(rows, strings.Join(fields, "\t"))
	}
	writeZip(t, filepath.Join(root, "countries", "ES.zip"), "ES.txt", strings.Join(rows, "\n")+"\n")
	writeZip(t, filepath.Join(root, "alternatenames", "ES.zip"), "ES.txt", strings.Join(alternates, "\n")+"\n")
	return root
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
