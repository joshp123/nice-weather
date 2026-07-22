package climatetiles

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
)

const (
	PointerVersion      = 2
	IndexVersion        = 1
	maximumPointerBytes = 1024
	maximumIndexBytes   = 64 * 1024
)

var (
	privateBuildPattern   = regexp.MustCompile(`^\.build-[A-Za-z0-9][A-Za-z0-9._-]*-[0-9]+$`)
	privatePointerPattern = regexp.MustCompile(`^\.current-[0-9a-f]{24}\.json$`)
)

type IndexState string

const (
	IndexStatePreview  IndexState = "preview"
	IndexStateComplete IndexState = "complete"
)

// Pointer is the only mutable climate publication artifact. It binds the
// visible Typical Year product to one immutable, validated year index.
type Pointer struct {
	Version     int    `json:"version"`
	IndexID     string `json:"indexId"`
	IndexSHA256 string `json:"indexSha256"`
}

// MonthReference binds one canonical month to its complete immutable release.
type MonthReference struct {
	Period        string `json:"period"`
	ReleaseID     string `json:"releaseId"`
	CatalogSHA256 string `json:"catalogSha256"`
}

// Index is the complete contract for one visible Typical Year. Validation
// requires every referenced month catalogue to share the same common contract.
type Index struct {
	Version         int              `json:"version"`
	IndexID         string           `json:"indexId"`
	State           IndexState       `json:"state"`
	ReferencePeriod ReferencePeriod  `json:"referencePeriod"`
	Provenance      Provenance       `json:"provenance"`
	Months          []MonthReference `json:"months"`
}

// Stage is a private artifact-shaped workspace. Existing tiledelivery writers
// can write beneath Root; direct point-block writers use ReleaseRoot.
type Stage struct {
	Root        string
	ReleaseRoot string
	ReleaseID   string
}

// Builder writes assets below the supplied private stage and returns their
// complete month catalogue. BuildAndInstallMonth owns catalog.json.
type Builder func(context.Context, Stage) (Catalog, error)

// BuildAndInstallMonth builds and installs one immutable month release. It
// never changes climate/current.json; visibility is owned by the year index.
func BuildAndInstallMonth(ctx context.Context, artifactRoot, releaseID string, build Builder) (result MonthReference, resultErr error) {
	if ctx == nil || build == nil || !validReleaseID(releaseID) {
		return MonthReference{}, errors.New("climate month installation requires a context, release ID and builder")
	}
	climateRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		return MonthReference{}, err
	}
	lock, err := acquirePublicationLock(climateRoot)
	if err != nil {
		return MonthReference{}, err
	}
	defer releasePublicationLock(lock)
	if err := cleanupPrivatePublicationEntries(climateRoot); err != nil {
		return MonthReference{}, err
	}

	target := filepath.Join(climateRoot, "releases", releaseID)
	staging, err := os.MkdirTemp(climateRoot, ".build-"+releaseID+"-")
	if err != nil {
		return MonthReference{}, err
	}
	defer func() {
		if staging == "" {
			return
		}
		if err := removePrivateStage(climateRoot, staging); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean private climate stage: %w", err))
		}
	}()
	releaseRoot := filepath.Join(staging, "releases", releaseID)
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		return MonthReference{}, err
	}
	catalog, err := build(ctx, Stage{Root: staging, ReleaseRoot: releaseRoot, ReleaseID: releaseID})
	if err != nil {
		return MonthReference{}, err
	}
	if err := ctx.Err(); err != nil {
		return MonthReference{}, err
	}
	if catalog.ReleaseID != releaseID {
		return MonthReference{}, errors.New("climate builder returned a different release ID")
	}
	if err := writeCatalog(releaseRoot, catalog); err != nil {
		return MonthReference{}, err
	}
	if _, err := ValidateRelease(releaseRoot); err != nil {
		return MonthReference{}, fmt.Errorf("validate staged climate release: %w", err)
	}
	if err := syncTree(releaseRoot); err != nil {
		return MonthReference{}, fmt.Errorf("sync staged climate release: %w", err)
	}
	candidate, candidateDigest, err := validateReleaseDigest(releaseRoot)
	if err != nil {
		return MonthReference{}, fmt.Errorf("bind staged climate catalog: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return MonthReference{}, err
	}
	installed, installedDigest, err := installedRelease(target)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(releaseRoot, target); err != nil {
			return MonthReference{}, fmt.Errorf("install climate release: %w", err)
		}
		if err := syncDirectory(filepath.Join(climateRoot, "releases")); err != nil {
			return MonthReference{}, fmt.Errorf("sync climate releases: %w", err)
		}
		installed, installedDigest, err = installedRelease(target)
	}
	if err != nil {
		return MonthReference{}, fmt.Errorf("validate installed climate release: %w", err)
	}
	if installedDigest != candidateDigest || installed.ReleaseID != candidate.ReleaseID {
		return MonthReference{}, fmt.Errorf("immutable climate release %s already exists with different content", releaseID)
	}
	if err := syncTree(target); err != nil {
		return MonthReference{}, fmt.Errorf("sync installed climate release: %w", err)
	}
	if err := syncDirectory(filepath.Join(climateRoot, "releases")); err != nil {
		return MonthReference{}, fmt.Errorf("sync climate releases: %w", err)
	}
	if err := removePrivateStage(climateRoot, staging); err != nil {
		return MonthReference{}, fmt.Errorf("clean installed climate stage: %w", err)
	}
	staging = ""
	return MonthReference{Period: installed.Period, ReleaseID: installed.ReleaseID, CatalogSHA256: installedDigest}, nil
}

// ValidateInstalledMonth verifies one expected immutable month release. A
// missing release is not an error; an existing malformed or mismatched release
// fails closed.
func ValidateInstalledMonth(artifactRoot, releaseID, period string) (MonthReference, Catalog, bool, error) {
	if !validReleaseID(releaseID) || !periodPattern.MatchString(period) {
		return MonthReference{}, Catalog{}, false, errors.New("installed climate month identity is invalid")
	}
	climateRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return MonthReference{}, Catalog{}, false, err
	}
	releaseRoot := filepath.Join(climateRoot, "releases", releaseID)
	_, err = os.Lstat(releaseRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return MonthReference{}, Catalog{}, false, nil
	}
	if err != nil {
		return MonthReference{}, Catalog{}, false, err
	}
	catalog, digest, err := validateReleaseDigest(releaseRoot)
	if err != nil {
		return MonthReference{}, Catalog{}, false, fmt.Errorf("validate installed climate month: %w", err)
	}
	if catalog.ReleaseID != releaseID || catalog.Period != period {
		return MonthReference{}, Catalog{}, false, errors.New("installed climate month does not match its expected release and period")
	}
	reference := MonthReference{Period: period, ReleaseID: releaseID, CatalogSHA256: digest}
	return reference, catalog, true, nil
}

// BuildAndPublishIndex validates the requested month set, installs one
// immutable year index, then changes climate/current.json atomically. A
// complete index can never be replaced by a preview.
func BuildAndPublishIndex(ctx context.Context, artifactRoot, indexID string, state IndexState, months []MonthReference) (result Pointer, resultErr error) {
	if ctx == nil || !validReleaseID(indexID) {
		return Pointer{}, errors.New("climate index publication requires a context and index ID")
	}
	climateRoot, err := publicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	lock, err := acquirePublicationLock(climateRoot)
	if err != nil {
		return Pointer{}, err
	}
	defer releasePublicationLock(lock)
	if err := cleanupPrivatePublicationEntries(climateRoot); err != nil {
		return Pointer{}, err
	}
	current, currentIndex, hasCurrent, err := validateExistingPointer(climateRoot)
	if err != nil {
		return Pointer{}, err
	}
	if hasCurrent && currentIndex.State == IndexStateComplete && state == IndexStatePreview {
		return Pointer{}, errors.New("refuse to replace a complete climate index with a preview")
	}
	index, err := buildIndex(climateRoot, indexID, state, months)
	if err != nil {
		return Pointer{}, err
	}
	if hasCurrent && currentIndex.State == IndexStatePreview && index.State == IndexStatePreview &&
		currentIndex.ReferencePeriod == index.ReferencePeriod && currentIndex.Provenance == index.Provenance &&
		!monthReferenceSuperset(index.Months, currentIndex.Months) {
		return Pointer{}, errors.New("refuse to replace a climate preview with a smaller month set from the same source")
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}

	staging, err := os.MkdirTemp(climateRoot, ".build-index-"+indexID+"-")
	if err != nil {
		return Pointer{}, err
	}
	defer func() {
		if staging == "" {
			return
		}
		if err := removePrivateStage(climateRoot, staging); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean private climate index stage: %w", err))
		}
	}()
	stagedIndexes := filepath.Join(staging, "indexes")
	if err := os.MkdirAll(stagedIndexes, 0o755); err != nil {
		return Pointer{}, err
	}
	stagedIndex := filepath.Join(stagedIndexes, indexID+".json")
	if err := writeIndex(stagedIndex, index); err != nil {
		return Pointer{}, err
	}
	if err := validateIndexFile(stagedIndex, index); err != nil {
		return Pointer{}, fmt.Errorf("validate staged climate index: %w", err)
	}
	if err := syncTree(stagedIndexes); err != nil {
		return Pointer{}, fmt.Errorf("sync staged climate index: %w", err)
	}
	candidatePayload, err := readRegularBounded(stagedIndex, maximumIndexBytes)
	if err != nil {
		return Pointer{}, err
	}
	candidateSum := sha256.Sum256(candidatePayload)
	candidateDigest := hex.EncodeToString(candidateSum[:])
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}

	target := filepath.Join(climateRoot, "indexes", indexID+".json")
	installedDigest, err := installedIndexDigest(target)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Link(stagedIndex, target); err != nil {
			return Pointer{}, fmt.Errorf("install climate index: %w", err)
		}
		if err := syncDirectory(filepath.Join(climateRoot, "indexes")); err != nil {
			return Pointer{}, fmt.Errorf("sync climate indexes: %w", err)
		}
		installedDigest, err = installedIndexDigest(target)
	}
	if err != nil {
		return Pointer{}, fmt.Errorf("validate installed climate index: %w", err)
	}
	if installedDigest != candidateDigest {
		return Pointer{}, fmt.Errorf("immutable climate index %s already exists with different content", indexID)
	}
	installed, installedDigest, err := validateIndexAt(climateRoot, indexID)
	if err != nil {
		return Pointer{}, fmt.Errorf("validate installed climate index: %w", err)
	}
	if installed.IndexID != index.IndexID {
		return Pointer{}, errors.New("installed climate index identity changed")
	}
	if err := syncTree(target); err != nil {
		return Pointer{}, fmt.Errorf("sync installed climate index: %w", err)
	}
	if err := syncDirectory(filepath.Join(climateRoot, "indexes")); err != nil {
		return Pointer{}, fmt.Errorf("sync climate indexes: %w", err)
	}
	if err := removePrivateStage(climateRoot, staging); err != nil {
		return Pointer{}, fmt.Errorf("clean installed climate index stage: %w", err)
	}
	staging = ""

	pointer := Pointer{Version: PointerVersion, IndexID: indexID, IndexSHA256: installedDigest}
	if hasCurrent && current == pointer {
		return pointer, nil
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	committed, err := writePointer(climateRoot, pointer)
	if err != nil {
		if committed {
			visible, _, visibleErr := validateCurrentIndex(climateRoot)
			if visibleErr == nil && visible == pointer {
				return pointer, nil
			}
		}
		return Pointer{}, err
	}
	return pointer, nil
}

func monthReferenceSuperset(candidate, current []MonthReference) bool {
	if len(candidate) < len(current) {
		return false
	}
	candidateSet := make(map[MonthReference]struct{}, len(candidate))
	for _, reference := range candidate {
		candidateSet[reference] = struct{}{}
	}
	for _, reference := range current {
		if _, found := candidateSet[reference]; !found {
			return false
		}
	}
	return true
}

// ValidateCurrentIndex verifies current.json, the exact immutable index and
// every referenced complete month release. It never changes publication state.
func ValidateCurrentIndex(artifactRoot string) (Pointer, Index, error) {
	climateRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, Index{}, err
	}
	return validateCurrentIndex(climateRoot)
}

func buildIndex(climateRoot, indexID string, state IndexState, months []MonthReference) (Index, error) {
	if err := validateMonthReferenceOrder(state, months); err != nil {
		return Index{}, err
	}
	var catalogs []Catalog
	for _, reference := range months {
		catalog, digest, err := validateReleaseDigest(filepath.Join(climateRoot, "releases", reference.ReleaseID))
		if err != nil {
			return Index{}, fmt.Errorf("validate climate month %s: %w", reference.Period, err)
		}
		if reference != (MonthReference{Period: catalog.Period, ReleaseID: catalog.ReleaseID, CatalogSHA256: digest}) {
			return Index{}, fmt.Errorf("climate month %s does not match its exact release and catalog digest", reference.Period)
		}
		catalogs = append(catalogs, catalog)
	}
	first := catalogs[0]
	for _, catalog := range catalogs[1:] {
		if catalog.Geometry != first.Geometry || catalog.Source != first.Source || catalog.Provenance != first.Provenance || catalog.SourceQuality != first.SourceQuality || !sameProbability(catalog.Probability, first.Probability) {
			return Index{}, errors.New("climate months do not share identical geometry, source, provenance, quality and probability contracts")
		}
	}
	index := Index{
		Version: IndexVersion, IndexID: indexID, State: state,
		ReferencePeriod: first.Source.ReferencePeriod,
		Provenance:      first.Provenance,
		Months:          append([]MonthReference(nil), months...),
	}
	if err := validateIndexShape(index); err != nil {
		return Index{}, err
	}
	return index, nil
}

func validateMonthReferenceOrder(state IndexState, months []MonthReference) error {
	if state == IndexStatePreview {
		if len(months) < 1 || len(months) > 11 {
			return errors.New("preview climate index requires 1 to 11 months")
		}
	} else if state == IndexStateComplete {
		if len(months) != 12 {
			return errors.New("complete climate index requires exactly 12 months")
		}
	} else {
		return errors.New("climate index state is invalid")
	}
	previous := ""
	for _, reference := range months {
		if !periodPattern.MatchString(reference.Period) || !validReleaseID(reference.ReleaseID) || !validSHA256(reference.CatalogSHA256) {
			return errors.New("climate index month reference is invalid")
		}
		if previous != "" && reference.Period <= previous {
			return errors.New("climate index months must be unique and in canonical order")
		}
		previous = reference.Period
	}
	if state == IndexStateComplete {
		for month, reference := range months {
			if reference.Period != fmt.Sprintf("month-%02d", month+1) {
				return errors.New("complete climate index must contain all 12 canonical months")
			}
		}
	}
	return nil
}

func validateIndexShape(index Index) error {
	if index.Version != IndexVersion || !validReleaseID(index.IndexID) {
		return errors.New("climate index identity is invalid")
	}
	if err := validateMonthReferenceOrder(index.State, index.Months); err != nil {
		return err
	}
	if index.ReferencePeriod.FirstYear > index.ReferencePeriod.LastYear || !validEvidenceSHA256(index.Provenance.SourceEvidenceSHA256) ||
		!validEvidenceSHA256(index.Provenance.LandMaskSetSHA256) || index.Provenance.DerivationContract == "" {
		return errors.New("climate index reference period or provenance is invalid")
	}
	return nil
}

func sameProbability(left, right ProbabilitySemantics) bool {
	return left.Unit == right.Unit && left.Denominator == right.Denominator && left.Threshold == right.Threshold &&
		left.GoodMinimumScore == right.GoodMinimumScore && left.NoDataValue == right.NoDataValue && slices.Equal(left.Fields, right.Fields)
}

func validateIndexAt(climateRoot, indexID string) (*Index, string, error) {
	filename := filepath.Join(climateRoot, "indexes", indexID+".json")
	payload, err := readRegularBounded(filename, maximumIndexBytes)
	if err != nil {
		return nil, "", err
	}
	var index Index
	if err := decodeExactJSON(payload, &index); err != nil {
		return nil, "", fmt.Errorf("decode climate index: %w", err)
	}
	if index.IndexID != indexID {
		return nil, "", errors.New("climate index path and identity differ")
	}
	if err := validateIndexFile(filename, index); err != nil {
		return nil, "", err
	}
	want, err := buildIndex(climateRoot, index.IndexID, index.State, index.Months)
	if err != nil {
		return nil, "", err
	}
	if !sameIndex(index, want) {
		return nil, "", errors.New("climate index common contract does not match its month releases")
	}
	after, err := readRegularBounded(filename, maximumIndexBytes)
	if err != nil {
		return nil, "", err
	}
	beforeSum, afterSum := sha256.Sum256(payload), sha256.Sum256(after)
	if beforeSum != afterSum {
		return nil, "", errors.New("climate index changed during validation")
	}
	return &index, hex.EncodeToString(beforeSum[:]), nil
}

func sameIndex(left, right Index) bool {
	return left.Version == right.Version && left.IndexID == right.IndexID && left.State == right.State &&
		left.ReferencePeriod == right.ReferencePeriod && left.Provenance == right.Provenance && slices.Equal(left.Months, right.Months)
}

func validateIndexFile(filename string, index Index) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumIndexBytes {
		return errors.New("climate index must be a bounded regular file and not a symlink")
	}
	return validateIndexShape(index)
}

func installedIndexDigest(target string) (string, error) {
	payload, err := readRegularBounded(target, maximumIndexBytes)
	if err != nil {
		return "", err
	}
	var index Index
	if err := decodeExactJSON(payload, &index); err != nil {
		return "", err
	}
	if err := validateIndexFile(target, index); err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func writeIndex(filename string, index Index) error {
	if err := validateIndexShape(index); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if len(payload) > maximumIndexBytes {
		return errors.New("climate index exceeds size limit")
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func installedRelease(target string) (Catalog, string, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return Catalog{}, "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Catalog{}, "", errors.New("climate release target must be a real directory")
	}
	return validateReleaseDigest(target)
}

func writeCatalog(root string, catalog Catalog) error {
	if err := validateCatalog(catalog); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if len(payload) > maximumCatalogBytes {
		return errors.New("climate catalog exceeds size limit")
	}
	file, err := os.OpenFile(filepath.Join(root, CatalogFilename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validateExistingPointer(climateRoot string) (Pointer, Index, bool, error) {
	_, err := os.Lstat(filepath.Join(climateRoot, "current.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Pointer{}, Index{}, false, nil
	}
	if err != nil {
		return Pointer{}, Index{}, false, err
	}
	pointer, index, err := validateCurrentIndex(climateRoot)
	if err != nil {
		return Pointer{}, Index{}, false, fmt.Errorf("refuse publication with invalid current climate index: %w", err)
	}
	return pointer, index, true, nil
}

func validateCurrentIndex(climateRoot string) (Pointer, Index, error) {
	payload, err := readRegularBounded(filepath.Join(climateRoot, "current.json"), maximumPointerBytes)
	if err != nil {
		return Pointer{}, Index{}, fmt.Errorf("read climate pointer: %w", err)
	}
	var pointer Pointer
	if err := decodeExactJSON(payload, &pointer); err != nil || pointer.Version != PointerVersion || !validReleaseID(pointer.IndexID) || !validSHA256(pointer.IndexSHA256) {
		return Pointer{}, Index{}, errors.New("climate pointer is invalid")
	}
	index, digest, err := validateIndexAt(climateRoot, pointer.IndexID)
	if err != nil {
		return Pointer{}, Index{}, fmt.Errorf("validate current climate index: %w", err)
	}
	want := Pointer{Version: PointerVersion, IndexID: index.IndexID, IndexSHA256: digest}
	if pointer != want {
		return Pointer{}, Index{}, errors.New("climate pointer checksum does not match index")
	}
	return pointer, *index, nil
}

func validateReleaseDigest(releaseRoot string) (Catalog, string, error) {
	before, err := readRegularBounded(filepath.Join(releaseRoot, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, "", err
	}
	catalog, err := ValidateRelease(releaseRoot)
	if err != nil {
		return Catalog{}, "", err
	}
	after, err := readRegularBounded(filepath.Join(releaseRoot, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return Catalog{}, "", err
	}
	beforeDigest, afterDigest := sha256.Sum256(before), sha256.Sum256(after)
	if beforeDigest != afterDigest {
		return Catalog{}, "", errors.New("climate catalog changed around release validation")
	}
	return catalog, hex.EncodeToString(beforeDigest[:]), nil
}

func writePointer(climateRoot string, pointer Pointer) (bool, error) {
	payload, err := json.Marshal(pointer)
	if err != nil {
		return false, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, err
	}
	temporary := filepath.Join(climateRoot, ".current-"+hex.EncodeToString(random[:])+".json")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporary, filepath.Join(climateRoot, "current.json")); err != nil {
		return false, fmt.Errorf("publish climate pointer: %w", err)
	}
	committed = true
	if err := syncDirectory(climateRoot); err != nil {
		return true, fmt.Errorf("sync climate publication root: %w", err)
	}
	return true, nil
}

func publicationRoot(artifactRoot string) (string, error) {
	if artifactRoot == "" {
		return "", errors.New("artifact root is required")
	}
	absolute, err := filepath.Abs(artifactRoot)
	if err != nil {
		return "", err
	}
	artifactInfo, err := os.Lstat(absolute)
	if err != nil || !artifactInfo.IsDir() || artifactInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("artifact root must be an existing real directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	climateRoot := filepath.Join(resolved, "climate")
	created, err := ensureRealDirectory(climateRoot)
	if err != nil {
		return "", err
	}
	if created {
		if err := syncDirectory(resolved); err != nil {
			return "", fmt.Errorf("sync climate parent: %w", err)
		}
		if err := syncDirectory(climateRoot); err != nil {
			return "", fmt.Errorf("sync climate root: %w", err)
		}
	}
	for _, name := range []string{"releases", "indexes"} {
		root := filepath.Join(climateRoot, name)
		created, err = ensureRealDirectory(root)
		if err != nil {
			return "", err
		}
		if created {
			if err := syncDirectory(climateRoot); err != nil {
				return "", fmt.Errorf("sync climate %s parent: %w", name, err)
			}
			if err := syncDirectory(root); err != nil {
				return "", fmt.Errorf("sync climate %s root: %w", name, err)
			}
		}
	}
	return climateRoot, nil
}

func existingPublicationRoot(artifactRoot string) (string, error) {
	absolute, err := filepath.Abs(artifactRoot)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	climateRoot := filepath.Join(resolved, "climate")
	for _, directory := range []string{climateRoot, filepath.Join(climateRoot, "releases"), filepath.Join(climateRoot, "indexes")} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("climate publication roots must be real directories")
		}
	}
	return climateRoot, nil
}

func ensureRealDirectory(directory string) (bool, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return true, os.Mkdir(directory, 0o755)
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s must be a real directory", directory)
	}
	return false, nil
}

func acquirePublicationLock(climateRoot string) (*os.File, error) {
	filename := filepath.Join(climateRoot, ".publication.lock")
	lock, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another climate publication is active: %w", err)
	}
	return lock, nil
}

func releasePublicationLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func cleanupPrivatePublicationEntries(climateRoot string) error {
	entries, err := os.ReadDir(climateRoot)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		entryPath := filepath.Join(climateRoot, name)
		switch {
		case privateBuildPattern.MatchString(name):
			info, err := os.Lstat(entryPath)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("private climate build entry %q is not a real directory", name)
			}
			if err := os.RemoveAll(entryPath); err != nil {
				return fmt.Errorf("remove stale climate build %q: %w", name, err)
			}
			changed = true
		case privatePointerPattern.MatchString(name):
			info, err := os.Lstat(entryPath)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("private climate pointer entry %q is not a regular file", name)
			}
			if err := os.Remove(entryPath); err != nil {
				return fmt.Errorf("remove stale climate pointer %q: %w", name, err)
			}
			changed = true
		}
	}
	if changed {
		return syncDirectory(climateRoot)
	}
	return nil
}

func removePrivateStage(climateRoot, staging string) error {
	relative, err := filepath.Rel(climateRoot, staging)
	if err != nil || filepath.Dir(relative) != "." || !privateBuildPattern.MatchString(relative) {
		return errors.New("refuse to remove unsafe private climate stage")
	}
	info, err := os.Lstat(staging)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private climate stage must be a real directory")
	}
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	return syncDirectory(climateRoot)
}

func syncTree(root string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to sync symlink %s", current)
		}
		if info.IsDir() {
			directories = append(directories, current)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to sync non-regular file %s", current)
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDirectory(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
