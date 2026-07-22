package places

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCancelledInstallDoesNotPublishReleaseOrPointer(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	placeRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(placeRoot, "cancelled-source")
	if err := writeSampleRelease(source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := installRelease(ctx, placeRoot, source, "places-cancelled"); err != context.Canceled {
		t.Fatalf("cancelled install error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(placeRoot, "releases", "places-cancelled")); !os.IsNotExist(err) {
		t.Fatalf("cancelled release was installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(placeRoot, "current.json")); !os.IsNotExist(err) {
		t.Fatalf("cancelled pointer was published: %v", err)
	}
}

func TestCancellationAfterInstallDoesNotRotatePointer(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	placeRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(placeRoot, "cancel-after-install")
	if err := writeSampleRelease(source); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelOnSecondCheck{}
	if _, err := installRelease(ctx, placeRoot, source, "places-installed-not-current"); err != context.Canceled {
		t.Fatalf("post-install cancellation error = %v", err)
	}
	if _, err := ValidateRelease(filepath.Join(placeRoot, "releases", "places-installed-not-current")); err != nil {
		t.Fatalf("installed immutable release is invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(placeRoot, "current.json")); !os.IsNotExist(err) {
		t.Fatalf("cancelled pointer was published: %v", err)
	}
}

type cancelOnSecondCheck struct{ checks int }

func (state *cancelOnSecondCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (state *cancelOnSecondCheck) Done() <-chan struct{}       { return nil }
func (state *cancelOnSecondCheck) Value(any) any               { return nil }
func (state *cancelOnSecondCheck) Err() error {
	state.checks++
	if state.checks >= 2 {
		return context.Canceled
	}
	return nil
}

func TestPublishInstallsImmutableReleaseAndRotatesPointerLast(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	firstSource := filepath.Join(t.TempDir(), "first")
	if err := writeSampleRelease(firstSource); err != nil {
		t.Fatal(err)
	}
	first, err := Publish(artifactRoot, firstSource, "places-a")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ValidateCurrent(artifactRoot); err != nil || got != first {
		t.Fatalf("current pointer = %+v, %v; want %+v", got, err, first)
	}
	installed := filepath.Join(artifactRoot, "places", "releases", "places-a")
	if _, err := ValidateRelease(installed); err != nil {
		t.Fatalf("installed release is invalid: %v", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(installed, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(manifestBytes)
	if first.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("pointer checksum = %s; want %x", first.ManifestSHA256, digest)
	}

	secondSource := filepath.Join(t.TempDir(), "second")
	if err := writeSampleRelease(secondSource); err != nil {
		t.Fatal(err)
	}
	second, err := Publish(artifactRoot, secondSource, "places-b")
	if err != nil {
		t.Fatal(err)
	}
	if second.ReleaseID != "places-b" {
		t.Fatalf("second pointer = %+v", second)
	}
	if _, err := ValidateRelease(installed); err != nil {
		t.Fatalf("first immutable release changed after rotation: %v", err)
	}
	if got, err := Publish(artifactRoot, secondSource, "places-b"); err != nil || got != second {
		t.Fatalf("identical release retry = %+v, %v", got, err)
	}
	if got, err := ValidateCurrent(artifactRoot); err != nil || got != second {
		t.Fatalf("replacement failure changed current: %+v, %v", got, err)
	}
	differentSource := filepath.Join(t.TempDir(), "different")
	if err := writeSampleRelease(differentSource); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(differentSource, "manifest.json")
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var different manifest
	if err := json.Unmarshal(payload, &different); err != nil {
		t.Fatal(err)
	}
	different.Source.Warranty = "Different valid immutable release."
	payload, err = marshalManifest(different)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(artifactRoot, differentSource, "places-b"); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("release replacement error = %v", err)
	}
	if got, err := ValidateCurrent(artifactRoot); err != nil || got != second {
		t.Fatalf("replacement failure changed current: %+v, %v", got, err)
	}
}

func TestPublishRejectsIncompleteOrCorruptReleaseWithoutChangingCurrent(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	goodSource := filepath.Join(t.TempDir(), "good")
	if err := writeSampleRelease(goodSource); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(artifactRoot, goodSource, "places-good"); err != nil {
		t.Fatal(err)
	}
	pointerPath := filepath.Join(artifactRoot, "places", "current.json")
	wantPointer, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{"altered manifest", func(t *testing.T, root string) {
			path := filepath.Join(root, "manifest.json")
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(payload, &value); err != nil {
				t.Fatal(err)
			}
			value["extra"] = true
			payload, _ = json.Marshal(value)
			if err := os.WriteFile(path, payload, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"noncanonical country label", func(t *testing.T, root string) {
			rewritePlaceManifest(t, root, func(value *manifest) { value.Countries["ES"] += " " })
		}},
		{"noncanonical admin label", func(t *testing.T, root string) {
			rewritePlaceManifest(t, root, func(value *manifest) { value.Admins["ES.51"] += " " })
		}},
		{"noncanonical browser whitespace", func(t *testing.T, root string) {
			rewritePlaceManifest(t, root, func(value *manifest) { value.Countries["ES"] = "\ufeff" + value.Countries["ES"] })
		}},
		{"altered shard", func(t *testing.T, root string) {
			path := firstShard(t, root)
			if err := os.WriteFile(path, []byte(`{"v":1,"r":[]}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing shard", func(t *testing.T, root string) {
			if err := os.Remove(firstShard(t, root)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "release")
			if err := writeSampleRelease(source); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, source)
			if _, err := Publish(artifactRoot, source, "places-bad-"+strings.ReplaceAll(test.name, " ", "-")); err == nil {
				t.Fatal("corrupt release was published")
			}
			gotPointer, err := os.ReadFile(pointerPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(gotPointer) != string(wantPointer) {
				t.Fatalf("failed publication changed pointer: %s", gotPointer)
			}
		})
	}
	if _, err := Publish(artifactRoot, goodSource, "../escape"); err == nil {
		t.Fatal("path-escaping release ID was accepted")
	}
}

func rewritePlaceManifest(t *testing.T, root string, change func(*manifest)) {
	t.Helper()
	path := filepath.Join(root, "manifest.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value manifest
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	change(&value)
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRefusesCorruptCurrentPointer(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	source := filepath.Join(t.TempDir(), "source")
	if err := writeSampleRelease(source); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(artifactRoot, source, "places-a"); err != nil {
		t.Fatal(err)
	}
	pointerPath := filepath.Join(artifactRoot, "places", "current.json")
	corrupt := []byte(`{"releaseId":"places-a","manifestSha256":"bad"}`)
	if err := os.WriteFile(pointerPath, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := writeSampleRelease(other); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(artifactRoot, other, "places-b"); err == nil || !strings.Contains(err.Error(), "invalid current") {
		t.Fatalf("corrupt-current publication error = %v", err)
	}
	got, err := os.ReadFile(pointerPath)
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("corrupt pointer was silently replaced: %q, %v", got, err)
	}
}

func TestPublishRefusesSymlinkedOutputDirectories(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := writeSampleRelease(source); err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"places", "places/releases"} {
		t.Run(child, func(t *testing.T) {
			artifactRoot := filepath.Join(t.TempDir(), "artifacts")
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(artifactRoot, filepath.FromSlash(child))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if _, err := Publish(artifactRoot, source, "places-a"); err == nil || !strings.Contains(err.Error(), "real directory") {
				t.Fatalf("symlinked %s publication error = %v", child, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("publication escaped through %s: %v, %v", child, entries, err)
			}
		})
	}
}

func TestValidateReleaseRejectsSymlinkedShardEvenWhenTargetBytesAreValid(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release")
	if err := writeSampleRelease(root); err != nil {
		t.Fatal(err)
	}
	shardPath := firstShard(t, root)
	payload, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "same-shard.json")
	if err := os.WriteFile(outside, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(shardPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, shardPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "unexpected place shard entry") {
		t.Fatalf("symlinked shard validation error = %v", err)
	}
}

func TestSampleReleaseUsesProductionSchemaAndNamedFixturePlaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	pointer, err := PublishSample(root)
	if err != nil {
		t.Fatal(err)
	}
	if pointer.ReleaseID != SampleReleaseID {
		t.Fatalf("sample release ID = %q", pointer.ReleaseID)
	}
	release := filepath.Join(root, "places", "releases", SampleReleaseID)
	summary, err := ValidateRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	if summary.PlaceCount != 7 || summary.SearchKeyCount != 7 {
		t.Fatalf("sample summary = %+v", summary)
	}
	for query, id := range map[string]int64{
		"madrid": 3_117_735, "nijmegen": 2_750_053, "malaga": 2_514_256, "cadiz": 2_520_600,
		"lodz": 3_093_133, "tbilisi": 611_717, "biddestone": 2_655_710,
	} {
		payload, err := os.ReadFile(filepath.Join(release, "shards", shardName(query)+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var got shard
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range got.Records {
			found = found || item.Key == query && item.GeoNameID == id
		}
		if !found {
			t.Errorf("sample query %q does not resolve GeoNames ID %d", query, id)
		}
	}
}

func TestWalkCurrentVisitsEachValidatedPlaceOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	pointer, err := PublishSample(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]Candidate)
	gotPointer, err := WalkCurrent(root, func(item Candidate) error {
		if _, exists := seen[item.GeoNameID]; exists {
			t.Fatalf("place %d was visited twice", item.GeoNameID)
		}
		seen[item.GeoNameID] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPointer != pointer || len(seen) != 7 {
		t.Fatalf("walk pointer=%+v places=%d", gotPointer, len(seen))
	}
	malaga := seen[2_514_256]
	if malaga.Name != "Málaga" || malaga.Admin1 != "Andalusia" || malaga.Country != "Spain" || malaga.Latitude != 36.72016 || malaga.Longitude != -4.42034 {
		t.Fatalf("Málaga candidate = %+v", malaga)
	}
}

func TestValidateReleaseRejectsAliasWithConflictingPlaceIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "release")
	if err := writeSampleRelease(root); err != nil {
		t.Fatal(err)
	}
	shardPath := filepath.Join(root, "shards", "mal.json")
	payload, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatal(err)
	}
	var malaga shard
	if err := json.Unmarshal(payload, &malaga); err != nil {
		t.Fatal(err)
	}
	alias := malaga.Records[0]
	alias.Key = "malaga city"
	malaga.Records = append(malaga.Records, alias)
	sortRecords(malaga.Records)
	rewriteShardManifest(t, root, "mal", malaga)
	if _, err := ValidateRelease(root); err != nil {
		t.Fatalf("consistent alias was rejected: %v", err)
	}

	malaga.Records[1].LatitudeE5++
	rewriteShardManifest(t, root, "mal", malaga)
	if _, err := ValidateRelease(root); err == nil || !strings.Contains(err.Error(), "inconsistent identity") {
		t.Fatalf("conflicting alias validation error = %v", err)
	}
}

func rewriteShardManifest(t *testing.T, root, prefix string, value shard) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shards", prefix+".json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifestPayload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata manifest
	if err := json.Unmarshal(manifestPayload, &metadata); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	metadata.Shards[prefix] = hex.EncodeToString(digest[:])
	metadata.SearchKeyCount = 0
	metadata.Summary = Summary{PlaceCount: metadata.PlaceCount, ShardCount: metadata.ShardCount}
	entries, err := os.ReadDir(filepath.Join(root, "shards"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, "shards", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var decoded shard
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		compressed, err := gzipSize(data)
		if err != nil {
			t.Fatal(err)
		}
		metadata.SearchKeyCount += len(decoded.Records)
		metadata.Summary.SearchKeyCount += len(decoded.Records)
		metadata.Summary.UncompressedBytes += int64(len(data))
		metadata.Summary.GzipBytes += compressed
		metadata.Summary.LargestShardGzip = max(metadata.Summary.LargestShardGzip, compressed)
	}
	manifestPayload, err = marshalManifest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestPayload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func firstShard(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "shards"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("read sample shards: %v", err)
	}
	return filepath.Join(root, "shards", entries[0].Name())
}
