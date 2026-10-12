package places

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

const sampleReleaseID = "fixture-places-20260718-v1"

// publishSample supplies a small valid release for publication operating-path tests.
func publishSample(artifactRoot string) (Pointer, error) {
	fixtureRoot, err := os.MkdirTemp(filepath.Dir(artifactRoot), ".places-test-")
	if err != nil {
		return Pointer{}, err
	}
	defer os.RemoveAll(fixtureRoot)
	if err := writeSampleRelease(fixtureRoot); err != nil {
		return Pointer{}, err
	}
	return Publish(artifactRoot, fixtureRoot, sampleReleaseID)
}

func writeSampleRelease(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "shards"), 0o755); err != nil {
		return err
	}
	items := []record{
		{Key: "cadiz", Name: "Cádiz", Country: "ES", Admin1: "51", LatitudeE5: 3_652_672, LongitudeE5: -628_910, Population: 116_979, FeatureRank: 3, GeoNameID: 2_520_600},
		{Key: "lodz", Name: "Łódź", Country: "PL", Admin1: "74", LatitudeE5: 5_176_925, LongitudeE5: 1_945_598, Population: 682_679, FeatureRank: 4, GeoNameID: 3_093_133},
		{Key: "madrid", Name: "Madrid", Country: "ES", Admin1: "29", LatitudeE5: 4_041_680, LongitudeE5: -370_370, Population: 3_255_944, FeatureRank: 5, GeoNameID: 3_117_735},
		{Key: "malaga", Name: "Málaga", Country: "ES", Admin1: "51", LatitudeE5: 3_672_016, LongitudeE5: -442_034, Population: 592_346, FeatureRank: 3, GeoNameID: 2_514_256},
		{Key: "nijmegen", Name: "Nijmegen", Country: "NL", Admin1: "03", LatitudeE5: 5_184_254, LongitudeE5: 586_252, Population: 177_659, FeatureRank: 3, GeoNameID: 2_750_053},
		{Key: "tbilisi", Name: "თბილისი", Country: "GE", Admin1: "TB", LatitudeE5: 4_169_380, LongitudeE5: 4_480_150, Population: 1_049_498, FeatureRank: 5, GeoNameID: 611_717},
		{Key: "biddestone", Name: "Biddestone", Country: "GB", Admin1: "ENG", LatitudeE5: 5_146_100, LongitudeE5: -220_000, Population: 498, FeatureRank: 2, GeoNameID: 2_655_710},
	}
	byShard := make(map[string][]record)
	for _, item := range items {
		byShard[shardName(item.Key)] = append(byShard[shardName(item.Key)], item)
	}
	shardNames := make([]string, 0, len(byShard))
	for name := range byShard {
		shardNames = append(shardNames, name)
	}
	sort.Strings(shardNames)
	summary := Summary{PlaceCount: len(items), SearchKeyCount: len(items), ShardCount: len(byShard)}
	digests := make(map[string]string, len(byShard))
	for _, name := range shardNames {
		records := byShard[name]
		sortRecords(records)
		payload, err := json.Marshal(shard{Version: 1, Records: records})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "shards", name+".json"), payload, 0o644); err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		digests[name] = hex.EncodeToString(digest[:])
		compressed, err := gzipSize(payload)
		if err != nil {
			return err
		}
		summary.UncompressedBytes += int64(len(payload))
		summary.GzipBytes += compressed
		summary.LargestShardGzip = max(summary.LargestShardGzip, compressed)
	}
	metadata := manifest{
		Schema: schema,
		Source: sourceMetadata{
			Name: "GeoNames", ExtractDate: "2026-07-18", DataURL: "https://download.geonames.org/export/dump/",
			License: "CC BY 4.0", LicenseURL: "https://creativecommons.org/licenses/by/4.0/",
			Attribution: "Sample subset containing modified GeoNames data, licensed under CC BY 4.0.",
			Modified:    true, Warranty: "Deterministic sample fixture. It is not the production Europe place index.",
		},
		ProductBounds: bounds{West: -24, East: 45, South: 29, North: 71.5},
		PlaceCount:    len(items), SearchKeyCount: len(items), ShardCount: len(byShard),
		ShardPrefixLength: shardPrefixLength, MinimumQueryLength: minimumQueryLength, Normalization: normalization,
		Record:    []string{"key", "name", "country", "admin1", "latE5", "lonE5", "population", "featureRank", "geonameId"},
		Countries: map[string]string{"ES": "Spain", "GB": "United Kingdom", "GE": "Georgia", "NL": "Netherlands", "PL": "Poland"},
		Admins: map[string]string{
			"ES.29": "Madrid", "ES.51": "Andalusia", "GB.ENG": "England", "GE.TB": "Tbilisi",
			"NL.03": "Gelderland", "PL.74": "Łódź Voivodeship",
		},
		Shards: digests, Summary: summary,
	}
	payload, err := marshalManifest(metadata)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), payload, 0o644); err != nil {
		return err
	}
	_, err = ValidateRelease(root)
	return err
}
