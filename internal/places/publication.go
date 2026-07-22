package places

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
)

const (
	maximumPointerBytes = 1024
	maximumShardBytes   = 4 * 1024 * 1024
)

var (
	releaseIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	shardPrefixPattern = regexp.MustCompile(`^[a-z0-9_]{3}$`)
	adminKeyPattern    = regexp.MustCompile(`^[A-Z]{2}\.[A-Za-z0-9_-]+$`)
	adminCodePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Pointer is the only mutable part of the place-index publication contract.
// The manifest checksum binds it to one complete immutable release.
type Pointer struct {
	ReleaseID      string `json:"releaseId"`
	ManifestSHA256 string `json:"manifestSha256"`
}

type placeIdentity struct {
	Name        string
	Country     string
	Admin1      string
	LatitudeE5  int32
	LongitudeE5 int32
	Population  int64
	FeatureRank uint8
}

// Candidate is one unique GeoNames settlement from a validated place release.
// Labels are resolved here so consumers do not need to understand the search
// manifest or its country/admin code tables.
type Candidate struct {
	GeoNameID   int64
	Name        string
	Country     string
	Admin1      string
	CountryCode string
	Latitude    float64
	Longitude   float64
	Population  int64
	FeatureRank uint8
}

// BuildAndPublishGlobal builds a complete release and installs it below
// <artifactRoot>/places. The pointer changes only after the installed release
// has passed the same validation used for an independently built release.
func BuildAndPublishGlobal(ctx context.Context, sourceDir, artifactRoot, sourceDate, releaseID string, refinement delivery.GridBounds) (Summary, error) {
	if !validReleaseID(releaseID) {
		return Summary{}, fmt.Errorf("invalid place release id %q", releaseID)
	}
	placeRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		return Summary{}, err
	}
	if err := validateExistingPointer(placeRoot); err != nil {
		return Summary{}, err
	}
	staging, err := os.MkdirTemp(placeRoot, ".build-"+releaseID+"-")
	if err != nil {
		return Summary{}, err
	}
	defer os.RemoveAll(staging)
	unpublished := filepath.Join(staging, "release")
	summary, err := BuildGlobal(ctx, sourceDir, unpublished, sourceDate, refinement)
	if err != nil {
		return Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if _, err := installRelease(ctx, placeRoot, unpublished, releaseID); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

// Publish installs an already-built release below artifactRoot and rotates the
// pointer last. Existing release IDs are immutable and are never replaced.
func Publish(artifactRoot, sourceRelease, releaseID string) (Pointer, error) {
	if !validReleaseID(releaseID) {
		return Pointer{}, fmt.Errorf("invalid place release id %q", releaseID)
	}
	placeRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	if err := validateExistingPointer(placeRoot); err != nil {
		return Pointer{}, err
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceRelease)
	if err != nil {
		return Pointer{}, fmt.Errorf("resolve place release: %w", err)
	}
	if _, err := ValidateRelease(resolvedSource); err != nil {
		return Pointer{}, fmt.Errorf("validate place release: %w", err)
	}
	staging, err := os.MkdirTemp(placeRoot, ".publish-"+releaseID+"-")
	if err != nil {
		return Pointer{}, err
	}
	defer os.RemoveAll(staging)
	copyRoot := filepath.Join(staging, "release")
	if err := copyRelease(resolvedSource, copyRoot); err != nil {
		return Pointer{}, err
	}
	return installRelease(context.Background(), placeRoot, copyRoot, releaseID)
}

// ValidateCurrent verifies the pointer, its manifest checksum and the complete
// release it names. It does not repair or rotate anything.
func ValidateCurrent(artifactRoot string) (Pointer, error) {
	placeRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	return validateCurrent(placeRoot)
}

// ValidateRelease verifies the complete immutable place-index schema, every
// shard checksum and the cross-file counts used by the browser reader.
func ValidateRelease(root string) (Summary, error) {
	rootFS, err := openCheckedRoot(root)
	if err != nil {
		return Summary{}, err
	}
	defer rootFS.Close()
	return validateReleaseRoot(rootFS, nil)
}

// WalkCurrent validates the complete current place release and visits each
// unique settlement exactly once. A visitor result must not be used unless the
// function returns nil: later integrity checks may still reject the release.
func WalkCurrent(artifactRoot string, visit func(Candidate) error) (Pointer, error) {
	if visit == nil {
		return Pointer{}, errors.New("place candidate visitor is required")
	}
	placeRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	return validateCurrentWithVisitor(placeRoot, visit)
}

func validateReleaseRoot(rootFS *os.Root, visit func(Candidate) error) (Summary, error) {
	entries, err := fs.ReadDir(rootFS.FS(), ".")
	if err != nil {
		return Summary{}, err
	}
	if len(entries) != 2 || entries[0].Name() != "manifest.json" || entries[1].Name() != "shards" {
		return Summary{}, errors.New("place release must contain only manifest.json and shards")
	}
	manifestPayload, err := readRootRegularBounded(rootFS, "manifest.json", maximumManifestBytes)
	if err != nil {
		return Summary{}, fmt.Errorf("read place manifest: %w", err)
	}
	var got manifest
	if err := decodeExactJSON(manifestPayload, &got); err != nil {
		return Summary{}, fmt.Errorf("decode place manifest: %w", err)
	}
	if err := validateManifest(got); err != nil {
		return Summary{}, err
	}

	shardsInfo, err := rootFS.Lstat("shards")
	if err != nil || !shardsInfo.IsDir() || shardsInfo.Mode()&os.ModeSymlink != 0 {
		return Summary{}, errors.New("place shards must be a real directory")
	}
	shardsFS, err := rootFS.OpenRoot("shards")
	if err != nil {
		return Summary{}, err
	}
	defer shardsFS.Close()
	openedShardsInfo, err := shardsFS.Stat(".")
	if err != nil || !os.SameFile(shardsInfo, openedShardsInfo) {
		return Summary{}, errors.New("place shards directory changed while opening")
	}
	shardEntries, err := fs.ReadDir(shardsFS.FS(), ".")
	if err != nil {
		return Summary{}, err
	}
	if len(shardEntries) != got.ShardCount {
		return Summary{}, fmt.Errorf("place release has %d shard files, manifest declares %d", len(shardEntries), got.ShardCount)
	}

	uniquePlaces := make(map[int64]placeIdentity, got.PlaceCount)
	seenKeys := make(map[string]struct{}, got.SearchKeyCount)
	actual := Summary{ShardCount: len(shardEntries)}
	for _, entry := range shardEntries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			return Summary{}, fmt.Errorf("unexpected place shard entry %q", entry.Name())
		}
		prefix := strings.TrimSuffix(entry.Name(), ".json")
		if !shardPrefixPattern.MatchString(prefix) {
			return Summary{}, fmt.Errorf("invalid place shard name %q", entry.Name())
		}
		wantDigest, ok := got.Shards[prefix]
		if !ok {
			return Summary{}, fmt.Errorf("place shard %q is absent from manifest", prefix)
		}
		payload, err := readRootRegularBounded(shardsFS, entry.Name(), maximumShardBytes)
		if err != nil {
			return Summary{}, fmt.Errorf("read place shard %q: %w", prefix, err)
		}
		digest := sha256.Sum256(payload)
		if hex.EncodeToString(digest[:]) != wantDigest {
			return Summary{}, fmt.Errorf("place shard %q checksum does not match manifest", prefix)
		}
		var decoded shard
		if err := decodeExactJSON(payload, &decoded); err != nil || decoded.Version != 1 || len(decoded.Records) == 0 || len(decoded.Records) > 100_000 {
			return Summary{}, fmt.Errorf("invalid place shard %q", prefix)
		}
		for index, item := range decoded.Records {
			if err := validateRecord(item, prefix, got); err != nil {
				return Summary{}, fmt.Errorf("place shard %q record %d: %w", prefix, index+1, err)
			}
			if index > 0 && lessRecord(item, decoded.Records[index-1]) {
				return Summary{}, fmt.Errorf("place shard %q is not in canonical order", prefix)
			}
			identity := fmt.Sprintf("%d/%s", item.GeoNameID, item.Key)
			if _, exists := seenKeys[identity]; exists {
				return Summary{}, fmt.Errorf("duplicate place search key %s", identity)
			}
			seenKeys[identity] = struct{}{}
			identityFields := placeIdentity{
				Name: item.Name, Country: item.Country, Admin1: item.Admin1,
				LatitudeE5: item.LatitudeE5, LongitudeE5: item.LongitudeE5,
				Population: item.Population, FeatureRank: item.FeatureRank,
			}
			previous, exists := uniquePlaces[item.GeoNameID]
			if exists && previous != identityFields {
				return Summary{}, fmt.Errorf("GeoNames ID %d has inconsistent identity across search keys", item.GeoNameID)
			}
			uniquePlaces[item.GeoNameID] = identityFields
			if !exists && visit != nil {
				admin := ""
				if item.Admin1 != "" {
					admin = got.Admins[item.Country+"."+item.Admin1]
				}
				if err := visit(Candidate{
					GeoNameID: item.GeoNameID, Name: item.Name,
					Country: got.Countries[item.Country], Admin1: admin, CountryCode: item.Country,
					Latitude: float64(item.LatitudeE5) / 100_000, Longitude: float64(item.LongitudeE5) / 100_000,
					Population: item.Population, FeatureRank: item.FeatureRank,
				}); err != nil {
					return Summary{}, fmt.Errorf("visit place %d: %w", item.GeoNameID, err)
				}
			}
		}
		compressed, err := gzipSize(payload)
		if err != nil {
			return Summary{}, err
		}
		actual.SearchKeyCount += len(decoded.Records)
		actual.UncompressedBytes += int64(len(payload))
		actual.GzipBytes += compressed
		actual.LargestShardGzip = max(actual.LargestShardGzip, compressed)
	}
	actual.PlaceCount = len(uniquePlaces)
	if actual != got.Summary || got.PlaceCount != actual.PlaceCount || got.SearchKeyCount != actual.SearchKeyCount {
		return Summary{}, fmt.Errorf("place release summary does not match its files: got %+v, want %+v", actual, got.Summary)
	}
	return actual, nil
}

func installRelease(ctx context.Context, placeRoot, source, releaseID string) (Pointer, error) {
	placeFS, err := os.OpenRoot(placeRoot)
	if err != nil {
		return Pointer{}, err
	}
	defer placeFS.Close()
	releasesInfo, err := placeFS.Lstat("releases")
	if err != nil || !releasesInfo.IsDir() || releasesInfo.Mode()&os.ModeSymlink != 0 {
		return Pointer{}, errors.New("places releases root must be a real directory")
	}
	sourceRelative, err := filepath.Rel(placeRoot, source)
	if err != nil || sourceRelative == "." || sourceRelative == ".." || strings.HasPrefix(sourceRelative, ".."+string(filepath.Separator)) {
		return Pointer{}, errors.New("staged place release is outside publication root")
	}
	targetRelative := filepath.ToSlash(filepath.Join("releases", releaseID))
	sourceFS, err := placeFS.OpenRoot(filepath.ToSlash(sourceRelative))
	if err != nil {
		return Pointer{}, fmt.Errorf("open staged place release: %w", err)
	}
	candidateDigest, err := validatedManifestDigestRoot(sourceFS)
	_ = sourceFS.Close()
	if err != nil {
		return Pointer{}, fmt.Errorf("validate staged place release: %w", err)
	}
	candidate := Pointer{ReleaseID: releaseID, ManifestSHA256: candidateDigest}
	if _, err := placeFS.Lstat(targetRelative); err == nil {
		existingFS, err := placeFS.OpenRoot(targetRelative)
		if err != nil {
			return Pointer{}, fmt.Errorf("open existing place release %s: %w", releaseID, err)
		}
		existingDigest, err := validatedManifestDigestRoot(existingFS)
		_ = existingFS.Close()
		if err != nil {
			return Pointer{}, fmt.Errorf("existing place release %s is invalid: %w", releaseID, err)
		}
		existing := Pointer{ReleaseID: releaseID, ManifestSHA256: existingDigest}
		if existing != candidate {
			return Pointer{}, fmt.Errorf("place release already exists with different content: %s", releaseID)
		}
		if err := ctx.Err(); err != nil {
			return Pointer{}, err
		}
		if err := writePointerRoot(placeFS, existing); err != nil {
			return Pointer{}, err
		}
		return existing, nil
	} else if !os.IsNotExist(err) {
		return Pointer{}, err
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	if err := placeFS.Rename(filepath.ToSlash(sourceRelative), targetRelative); err != nil {
		return Pointer{}, fmt.Errorf("install place release: %w", err)
	}
	installedFS, err := placeFS.OpenRoot(targetRelative)
	if err != nil {
		return Pointer{}, fmt.Errorf("open installed place release: %w", err)
	}
	installedDigest, err := validatedManifestDigestRoot(installedFS)
	_ = installedFS.Close()
	if err != nil {
		return Pointer{}, fmt.Errorf("validate installed place release: %w", err)
	}
	installed := Pointer{ReleaseID: releaseID, ManifestSHA256: installedDigest}
	if installed != candidate {
		return Pointer{}, errors.New("installed place release changed after validation")
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	if err := writePointerRoot(placeFS, candidate); err != nil {
		return Pointer{}, err
	}
	return candidate, nil
}

func validatedManifestDigest(root string) (string, error) {
	rootFS, err := openCheckedRoot(root)
	if err != nil {
		return "", err
	}
	defer rootFS.Close()
	return validatedManifestDigestRoot(rootFS)
}

func validatedManifestDigestRoot(rootFS *os.Root) (string, error) {
	before, err := readRootRegularBounded(rootFS, "manifest.json", maximumManifestBytes)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(before)
	if _, err := validateReleaseRoot(rootFS, nil); err != nil {
		return "", err
	}
	after, err := readRootRegularBounded(rootFS, "manifest.json", maximumManifestBytes)
	if err != nil {
		return "", err
	}
	afterDigest := sha256.Sum256(after)
	if digest != afterDigest {
		return "", errors.New("place manifest changed during validation")
	}
	return hex.EncodeToString(digest[:]), nil
}

func validateExistingPointer(placeRoot string) error {
	_, err := os.Lstat(filepath.Join(placeRoot, "current.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := validateCurrent(placeRoot); err != nil {
		return fmt.Errorf("refuse publication with invalid current place index: %w", err)
	}
	return nil
}

func validateCurrent(placeRoot string) (Pointer, error) {
	return validateCurrentWithVisitor(placeRoot, nil)
}

func validateCurrentWithVisitor(placeRoot string, visit func(Candidate) error) (Pointer, error) {
	payload, err := readRegularBounded(filepath.Join(placeRoot, "current.json"), maximumPointerBytes)
	if err != nil {
		return Pointer{}, fmt.Errorf("read place pointer: %w", err)
	}
	var pointer Pointer
	if err := decodeExactJSON(payload, &pointer); err != nil {
		return Pointer{}, fmt.Errorf("decode place pointer: %w", err)
	}
	if !validReleaseID(pointer.ReleaseID) || !validSHA256(pointer.ManifestSHA256) {
		return Pointer{}, errors.New("place pointer is invalid")
	}
	releaseRoot := filepath.Join(placeRoot, "releases", pointer.ReleaseID)
	rootFS, err := openCheckedRoot(releaseRoot)
	if err != nil {
		return Pointer{}, fmt.Errorf("validate current place release: %w", err)
	}
	defer rootFS.Close()
	before, err := readRootRegularBounded(rootFS, "manifest.json", maximumManifestBytes)
	if err != nil {
		return Pointer{}, fmt.Errorf("validate current place release: %w", err)
	}
	manifestHash := sha256.Sum256(before)
	if _, err := validateReleaseRoot(rootFS, visit); err != nil {
		return Pointer{}, fmt.Errorf("validate current place release: %w", err)
	}
	after, err := readRootRegularBounded(rootFS, "manifest.json", maximumManifestBytes)
	if err != nil {
		return Pointer{}, fmt.Errorf("validate current place release: %w", err)
	}
	if sha256.Sum256(after) != manifestHash {
		return Pointer{}, errors.New("place manifest changed during validation")
	}
	manifestDigest := hex.EncodeToString(manifestHash[:])
	want := Pointer{ReleaseID: pointer.ReleaseID, ManifestSHA256: manifestDigest}
	if pointer != want {
		return Pointer{}, errors.New("place pointer manifest checksum does not match release")
	}
	return pointer, nil
}

func pointerForRelease(root, releaseID string) (Pointer, error) {
	payload, err := readRegularBounded(filepath.Join(root, "manifest.json"), maximumManifestBytes)
	if err != nil {
		return Pointer{}, err
	}
	digest := sha256.Sum256(payload)
	return Pointer{ReleaseID: releaseID, ManifestSHA256: hex.EncodeToString(digest[:])}, nil
}

func writePointer(placeRoot string, pointer Pointer) error {
	placeFS, err := os.OpenRoot(placeRoot)
	if err != nil {
		return err
	}
	defer placeFS.Close()
	return writePointerRoot(placeFS, pointer)
}

func writePointerRoot(placeFS *os.Root, pointer Pointer) error {
	payload, err := json.Marshal(pointer)
	if err != nil {
		return err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temporaryName := ".current-" + hex.EncodeToString(random[:]) + ".json"
	temporary, err := placeFS.OpenFile(temporaryName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = placeFS.Remove(temporaryName)
		}
	}()
	if _, err := temporary.Write(payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := placeFS.Rename(temporaryName, "current.json"); err != nil {
		return fmt.Errorf("publish place pointer: %w", err)
	}
	directory, err := placeFS.Open(".")
	if err != nil {
		return fmt.Errorf("open place publication root for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync place publication root: %w", err)
	}
	if err := directory.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func publicationRoot(artifactRoot string) (string, error) {
	absolute, err := filepath.Abs(artifactRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", err
	}
	resolvedArtifactRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	placeRoot := filepath.Join(resolvedArtifactRoot, "places")
	if err := ensureRealDirectory(placeRoot); err != nil {
		return "", err
	}
	if err := ensureRealDirectory(filepath.Join(placeRoot, "releases")); err != nil {
		return "", err
	}
	return placeRoot, nil
}

func existingPublicationRoot(artifactRoot string) (string, error) {
	absolute, err := filepath.Abs(artifactRoot)
	if err != nil {
		return "", err
	}
	resolvedArtifactRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	placeRoot := filepath.Join(resolvedArtifactRoot, "places")
	info, err := os.Lstat(placeRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("places publication root must be a real directory")
	}
	releasesInfo, err := os.Lstat(filepath.Join(placeRoot, "releases"))
	if err != nil || !releasesInfo.IsDir() || releasesInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("places releases root must be a real directory")
	}
	return placeRoot, nil
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.Mkdir(path, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a real directory", path)
	}
	return nil
}

func copyRelease(source, target string) error {
	sourceFS, err := openCheckedRoot(source)
	if err != nil {
		return err
	}
	defer sourceFS.Close()
	if err := os.Mkdir(target, 0o755); err != nil {
		return err
	}
	if err := copyRootRegularFile(sourceFS, "manifest.json", filepath.Join(target, "manifest.json")); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(target, "shards"), 0o755); err != nil {
		return err
	}
	shardsInfo, err := sourceFS.Lstat("shards")
	if err != nil || !shardsInfo.IsDir() || shardsInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("place shards must be a real directory")
	}
	shardsFS, err := sourceFS.OpenRoot("shards")
	if err != nil {
		return err
	}
	defer shardsFS.Close()
	openedInfo, err := shardsFS.Stat(".")
	if err != nil || !os.SameFile(shardsInfo, openedInfo) {
		return errors.New("place shards directory changed while opening")
	}
	entries, err := fs.ReadDir(shardsFS.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyRootRegularFile(shardsFS, entry.Name(), filepath.Join(target, "shards", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyRootRegularFile(root *os.Root, source, target string) error {
	info, err := root.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("place release entry %s is not a regular file", source)
	}
	input, err := root.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return errors.New("place release entry changed while opening")
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		_ = output.Close()
		if !success {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

func openCheckedRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("place release root must be a real directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	openedInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(info, openedInfo) {
		_ = root.Close()
		return nil, errors.New("place release root changed while opening")
	}
	return root, nil
}

func readRootRegularBounded(root *os.Root, path string, limit int64) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("file must be regular and not a symlink")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file size %d exceeds %d", info.Size(), limit)
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.New("file changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return payload, nil
}

func readRegularBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("file must be regular and not a symlink")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file size %d exceeds %d", info.Size(), limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.New("file changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return payload, nil
}

func decodeExactJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func validateManifest(got manifest) error {
	if got.Schema != schema || got.Normalization != normalization || got.ShardPrefixLength != shardPrefixLength || got.MinimumQueryLength != minimumQueryLength {
		return errors.New("unsupported place manifest contract")
	}
	if got.Source.Name != "GeoNames" || got.Source.DataURL != "https://download.geonames.org/export/dump/" || got.Source.License != "CC BY 4.0" || got.Source.LicenseURL != "https://creativecommons.org/licenses/by/4.0/" || !got.Source.Modified || strings.TrimSpace(got.Source.Attribution) == "" || strings.TrimSpace(got.Source.Warranty) == "" {
		return errors.New("unsupported place source contract")
	}
	if _, err := time.Parse("2006-01-02", got.Source.ExtractDate); err != nil {
		return errors.New("place source date is invalid")
	}
	if got.ProductBounds.West < -180 || got.ProductBounds.East > 180 || got.ProductBounds.South < -90 || got.ProductBounds.North > 90 || got.ProductBounds.West >= got.ProductBounds.East || got.ProductBounds.South >= got.ProductBounds.North {
		return errors.New("place product bounds are invalid")
	}
	wantRecord := []string{"key", "name", "country", "admin1", "latE5", "lonE5", "population", "featureRank", "geonameId"}
	if !slices.Equal(got.Record, wantRecord) {
		return errors.New("unsupported place record contract")
	}
	if got.PlaceCount < 1 || got.PlaceCount > 1_100_000 || got.SearchKeyCount < got.PlaceCount || got.SearchKeyCount > 5_000_000 || got.ShardCount < 1 || got.ShardCount > maximumShardCount || len(got.Shards) != got.ShardCount {
		return errors.New("place manifest counts are invalid")
	}
	if got.Summary.PlaceCount != got.PlaceCount || got.Summary.SearchKeyCount != got.SearchKeyCount || got.Summary.ShardCount != got.ShardCount || got.Summary.UncompressedBytes < 1 || got.Summary.GzipBytes < 1 || got.Summary.LargestShardGzip < 1 || got.Summary.LargestShardGzip > maximumShardGzipBytes || got.Summary.GzipBytes < got.Summary.LargestShardGzip {
		return errors.New("place summary is invalid")
	}
	if len(got.Countries) == 0 {
		return errors.New("place manifest contains no countries")
	}
	for code, label := range got.Countries {
		canonical, err := canonicalDisplayLabel(label)
		if len(code) != 2 || code != strings.ToUpper(code) || err != nil || canonical != label {
			return errors.New("place country metadata is invalid")
		}
	}
	for code, label := range got.Admins {
		parts := strings.SplitN(code, ".", 2)
		canonical, err := canonicalDisplayLabel(label)
		if !adminKeyPattern.MatchString(code) || got.Countries[parts[0]] == "" || err != nil || canonical != label {
			return errors.New("place admin metadata is invalid")
		}
	}
	for prefix, digest := range got.Shards {
		if !shardPrefixPattern.MatchString(prefix) || !validSHA256(digest) {
			return errors.New("place shard metadata is invalid")
		}
	}
	return nil
}

func validateRecord(item record, prefix string, metadata manifest) error {
	if item.Key == "" || item.Key != normalize(item.Key) || shardName(item.Key) != prefix || strings.TrimSpace(item.Name) == "" || len([]rune(item.Key)) > 200 || len([]rune(item.Name)) > 200 {
		return errors.New("invalid key or name")
	}
	if metadata.Countries[item.Country] == "" || item.Admin1 != "" && !adminCodePattern.MatchString(item.Admin1) {
		return errors.New("invalid country or admin code")
	}
	latitude := float64(item.LatitudeE5) / 100_000
	longitude := float64(item.LongitudeE5) / 100_000
	if longitude < metadata.ProductBounds.West || longitude > metadata.ProductBounds.East || latitude < metadata.ProductBounds.South || latitude > metadata.ProductBounds.North {
		return errors.New("coordinate is outside product bounds")
	}
	if item.Population < 0 || item.FeatureRank < 1 || item.FeatureRank > 5 || item.GeoNameID <= 0 {
		return errors.New("invalid rank, population or GeoNames ID")
	}
	return nil
}

func lessRecord(left, right record) bool {
	items := []struct {
		less  bool
		equal bool
	}{
		{left.Key < right.Key, left.Key == right.Key},
		{left.FeatureRank > right.FeatureRank, left.FeatureRank == right.FeatureRank},
		{left.Population > right.Population, left.Population == right.Population},
		{left.Name < right.Name, left.Name == right.Name},
		{left.GeoNameID < right.GeoNameID, left.GeoNameID == right.GeoNameID},
	}
	for _, item := range items {
		if !item.equal {
			return item.less
		}
	}
	return false
}

func validReleaseID(value string) bool {
	return len(value) <= 128 && releaseIDPattern.MatchString(value) && value != "." && value != ".."
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
