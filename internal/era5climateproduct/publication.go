package era5climateproduct

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
	"slices"
	"sort"
	"syscall"
)

const (
	PointerVersion      = 2
	IndexVersion        = 2
	maximumPointerBytes = 1024
	maximumIndexBytes   = 64 * 1024
)

type IndexState string

const (
	IndexStatePreview  IndexState = "preview"
	IndexStateComplete IndexState = "complete"
)

// Pointer is the only mutable climate artifact. It changes after its exact
// immutable ERA5 index has been installed and synced.
type Pointer struct {
	Version     int    `json:"version"`
	IndexID     string `json:"indexId"`
	IndexSHA256 string `json:"indexSha256"`
}

type ReferencePeriod struct {
	FirstYear int `json:"firstYear"`
	LastYear  int `json:"lastYear"`
}

// IndexSource is the source and display contract which every indexed ERA5
// month must share. Acquisition-plan digests remain month-specific catalogs.
type IndexSource struct {
	Dataset                  string  `json:"dataset"`
	Kind                     string  `json:"kind"`
	GridDegrees              float64 `json:"gridDegrees"`
	LocalDay                 string  `json:"localDay"`
	LandAuthority            string  `json:"landAuthority"`
	LandSourceSHA256         string  `json:"landSourceSha256"`
	PublicationAuthorityKind string  `json:"publicationAuthorityKind"`
	LogicalCellCount         int     `json:"logicalCellCount"`
	DatelineCellCount        int     `json:"datelineCellCount"`
	DisplayMaskAuthority     string  `json:"displayMaskAuthority"`
	DisplayMaskDerivation    string  `json:"displayMaskDerivation"`
	DisplayMaskSHA256        string  `json:"displayMaskSha256"`
	DisplayMaskBytes         uint64  `json:"displayMaskBytes"`
}

type MonthReference struct {
	Period        string `json:"period"`
	ReleaseID     string `json:"releaseId"`
	CatalogSHA256 string `json:"catalogSha256"`
}

// Index is an ERA5-only ordered month set. It deliberately does not model
// alternate climate providers or the retired AgERA5 provenance schema.
type Index struct {
	Version          int                  `json:"version"`
	IndexID          string               `json:"indexId"`
	State            IndexState           `json:"state"`
	ReferencePeriod  ReferencePeriod      `json:"referencePeriod"`
	Geometry         Geometry             `json:"geometry"`
	Source           IndexSource          `json:"source"`
	PositiveDateline DatelineSemantics    `json:"positiveDateline"`
	Probability      ProbabilitySemantics `json:"probability"`
	ScalarSemantics  ScalarSemantics      `json:"scalarSemantics"`
	MaximumZoom      uint8                `json:"maximumZoom"`
	PointRecordBytes int                  `json:"pointRecordBytes"`
	Months           []MonthReference     `json:"months"`
}

type Publication struct {
	Pointer Pointer
	Index   Index
}

type PublicationVerification struct {
	Pointer  Pointer
	Index    Index
	Catalogs []Catalog
}

// PublishValidatedMonth appends one freshly deep-validated installed month to
// the visible ERA5 index. Existing references are checksum-bound and reused.
func PublishValidatedMonth(ctx context.Context, climateRoot string, validated Catalog) (Publication, error) {
	if ctx == nil || !validIdentifier(validated.ReleaseID) {
		return Publication{}, errors.New("ERA5 climate publication requires a context and validated release")
	}
	root, err := preparePublicationRoot(climateRoot)
	if err != nil {
		return Publication{}, err
	}
	lock, err := acquirePublicationLock(root)
	if err != nil {
		return Publication{}, err
	}
	defer releasePublicationLock(lock)

	reference, installed, err := readMonthReference(root, validated.ReleaseID)
	if err != nil {
		return Publication{}, fmt.Errorf("bind validated ERA5 month: %w", err)
	}
	if !sameCatalog(installed, validated) {
		return Publication{}, errors.New("installed ERA5 catalog changed after deep validation")
	}

	pointer, current, exists, err := readCurrentPublication(root)
	if err != nil {
		return Publication{}, err
	}
	months := []MonthReference{reference}
	if exists {
		for _, existing := range current.Months {
			if existing.Period != reference.Period {
				continue
			}
			if existing == reference {
				return Publication{Pointer: pointer, Index: current}, nil
			}
			return Publication{}, errors.New("refuse to replace an existing ERA5 climate month reference")
		}
		if current.State == IndexStateComplete {
			return Publication{}, errors.New("refuse to replace or extend a complete ERA5 climate index")
		}
		if err := requireSameIndexContract(current, installed); err != nil {
			return Publication{}, err
		}
		months = append(append([]MonthReference(nil), current.Months...), reference)
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Period < months[j].Period })
	if err := validateMonthReferences(indexStateForCount(len(months)), months); err != nil {
		return Publication{}, err
	}

	index := indexFromCatalog(validated.ReleaseID, installed, months)
	if err := validateIndexShape(index); err != nil {
		return Publication{}, err
	}
	if exists && !monthReferenceSuperset(index.Months, current.Months) {
		return Publication{}, errors.New("refuse to shrink or change the visible ERA5 month set")
	}
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	return installIndexAndPointer(ctx, root, index)
}

// ValidateCurrentIndex checks the pointer, exact index checksum, every month
// catalog checksum, and the common ERA5 index contract without changing state.
func ValidateCurrentIndex(climateRoot string) (Pointer, Index, error) {
	root, err := existingPublicationRoot(climateRoot)
	if err != nil {
		return Pointer{}, Index{}, err
	}
	pointer, index, exists, err := readCurrentPublication(root)
	if err != nil {
		return Pointer{}, Index{}, err
	}
	if !exists {
		return Pointer{}, Index{}, errors.New("ERA5 climate current pointer does not exist")
	}
	return pointer, index, nil
}

// VerifyCompletePublication is the ERA5-native read-only deployment gate.
func VerifyCompletePublication(artifactRoot string) (PublicationVerification, error) {
	absoluteRoot, err := filepath.Abs(artifactRoot)
	if err != nil {
		return PublicationVerification{}, fmt.Errorf("resolve ERA5 climate artifact root: %w", err)
	}
	climateRoot := filepath.Join(absoluteRoot, "climate")
	pointer, index, err := ValidateCurrentIndex(climateRoot)
	if err != nil {
		return PublicationVerification{}, err
	}
	if index.State != IndexStateComplete || len(index.Months) != 12 {
		return PublicationVerification{}, errors.New("ERA5 climate publication is not a complete 12-month year")
	}
	result := PublicationVerification{Pointer: pointer, Index: index, Catalogs: make([]Catalog, 0, 12)}
	for _, reference := range index.Months {
		catalog, err := ValidatePublishedRelease(filepath.Join(climateRoot, "releases", reference.ReleaseID))
		if err != nil {
			return PublicationVerification{}, fmt.Errorf("verify ERA5 %s: %w", reference.Period, err)
		}
		body, err := readBounded(filepath.Join(climateRoot, "releases", reference.ReleaseID, CatalogFilename), maximumCatalogBytes)
		if err != nil {
			return PublicationVerification{}, err
		}
		digest := sha256.Sum256(body)
		if reference != (MonthReference{Period: catalog.Period, ReleaseID: catalog.ReleaseID, CatalogSHA256: hex.EncodeToString(digest[:])}) {
			return PublicationVerification{}, errors.New("ERA5 climate release changed after index validation")
		}
		result.Catalogs = append(result.Catalogs, catalog)
	}
	after, _, err := ValidateCurrentIndex(climateRoot)
	if err != nil || after != pointer {
		return PublicationVerification{}, errors.New("ERA5 climate publication changed during verification")
	}
	return result, nil
}

func indexFromCatalog(indexID string, catalog Catalog, months []MonthReference) Index {
	return Index{
		Version: IndexVersion, IndexID: indexID, State: indexStateForCount(len(months)),
		ReferencePeriod: ReferencePeriod{FirstYear: catalog.Source.ReferenceFirstYear, LastYear: catalog.Source.ReferenceLastYear},
		Geometry:        catalog.Geometry, Source: sourceContract(catalog.Source),
		PositiveDateline: catalog.Dateline, Probability: catalog.Probability,
		ScalarSemantics: catalog.ScalarSemantics, MaximumZoom: catalog.MaximumZoom,
		PointRecordBytes: catalog.PointRecordBytes,
		Months:           append([]MonthReference(nil), months...),
	}
}

func sourceContract(source Source) IndexSource {
	return IndexSource{
		Dataset: source.Dataset, Kind: source.Kind, GridDegrees: source.GridDegrees,
		LocalDay: source.LocalDay, LandAuthority: source.LandAuthority,
		LandSourceSHA256:         source.LandSourceSHA256,
		PublicationAuthorityKind: source.PublicationAuthorityKind,
		LogicalCellCount:         source.LogicalCellCount,
		DatelineCellCount:        source.DatelineCellCount,
		DisplayMaskAuthority:     source.DisplayMaskAuthority,
		DisplayMaskDerivation:    source.DisplayMaskDerivation,
		DisplayMaskSHA256:        source.DisplayMaskSHA256, DisplayMaskBytes: source.DisplayMaskBytes,
	}
}

func requireSameIndexContract(index Index, catalog Catalog) error {
	want := indexFromCatalog(index.IndexID, catalog, index.Months)
	if index.ReferencePeriod != want.ReferencePeriod || index.Geometry != want.Geometry ||
		index.Source != want.Source || index.PositiveDateline != want.PositiveDateline ||
		!sameProbability(index.Probability, want.Probability) ||
		index.ScalarSemantics != want.ScalarSemantics || index.MaximumZoom != want.MaximumZoom ||
		index.PointRecordBytes != want.PointRecordBytes {
		return errors.New("ERA5 climate month uses a different source or display authority")
	}
	return nil
}

func sameProbability(left, right ProbabilitySemantics) bool {
	return left.Unit == right.Unit && left.Denominator == right.Denominator &&
		left.Threshold == right.Threshold && left.GoodMinimumScore == right.GoodMinimumScore &&
		left.NoDataValue == right.NoDataValue && slices.Equal(left.Fields, right.Fields)
}

func indexStateForCount(count int) IndexState {
	if count == 12 {
		return IndexStateComplete
	}
	return IndexStatePreview
}

func validateMonthReferences(state IndexState, months []MonthReference) error {
	if state == IndexStatePreview {
		if len(months) < 1 || len(months) > 11 {
			return errors.New("preview ERA5 climate index requires 1 to 11 months")
		}
	} else if state == IndexStateComplete {
		if len(months) != 12 {
			return errors.New("complete ERA5 climate index requires exactly 12 months")
		}
	} else {
		return errors.New("ERA5 climate index state is invalid")
	}
	previous := ""
	for _, reference := range months {
		if !validPeriodValue(reference.Period) || !validIdentifier(reference.ReleaseID) || !validSHA256(reference.CatalogSHA256) {
			return errors.New("ERA5 climate index month reference is invalid")
		}
		if previous != "" && reference.Period <= previous {
			return errors.New("ERA5 climate index months must be unique and in canonical order")
		}
		previous = reference.Period
	}
	if state == IndexStateComplete {
		for month, reference := range months {
			if reference.Period != fmt.Sprintf("month-%02d", month+1) {
				return errors.New("complete ERA5 climate index must contain all 12 canonical months")
			}
		}
	}
	return nil
}

func validateIndexShape(index Index) error {
	if index.Version != IndexVersion || !validIdentifier(index.IndexID) {
		return errors.New("ERA5 climate index identity is invalid")
	}
	if err := validateMonthReferences(index.State, index.Months); err != nil {
		return err
	}
	if index.ReferencePeriod != (ReferencePeriod{FirstYear: 2006, LastYear: 2025}) ||
		index.Geometry != (Geometry{Projection: "EPSG:4326", West: -180, East: 180, South: -90, North: 90, Rows: Rows, Columns: Columns, CellResolutionDegrees: GridDegrees, CellRegistration: "exact-era5-grid-point-centres"}) ||
		index.Source.Dataset != "ERA5" || index.Source.Kind != "hourly-reanalysis-reduced-to-iana-civil-local-days" ||
		index.Source.GridDegrees != GridDegrees || index.Source.LocalDay != LocalDayAuthority ||
		index.Source.LandAuthority != LandAuthority || !validTrustedSHA256(index.Source.LandSourceSHA256) ||
		index.Source.PublicationAuthorityKind != sidecarAuthorityKind ||
		index.Source.LogicalCellCount < 1 || index.Source.LogicalCellCount > Rows*Columns+Rows ||
		index.Source.DatelineCellCount < 0 || index.Source.DatelineCellCount > Rows ||
		index.Source.DisplayMaskAuthority != DisplayMaskAuthority || index.Source.DisplayMaskDerivation == "" ||
		!validTrustedSHA256(index.Source.DisplayMaskSHA256) || index.Source.DisplayMaskBytes != Rows*Columns ||
		index.PositiveDateline != (DatelineSemantics{Longitude: 180, Meaning: "independent +180 degree civil-day records; never alias to -180 degree records", CanonicalColumns: Columns, OverrideRecordBytes: 64}) ||
		index.MaximumZoom > MaximumZoom || index.PointRecordBytes != 64 {
		return errors.New("ERA5 climate index common contract is invalid")
	}
	if index.Probability.Unit != "whole-percent" || index.Probability.Denominator != "complete-real-days-in-reference-month" ||
		index.Probability.Threshold != "canonical-rating-good-or-better" || index.Probability.GoodMinimumScore != 55 ||
		index.Probability.NoDataValue != 255 || !slices.Equal(index.Probability.Fields, fields) {
		return errors.New("ERA5 climate index probability contract is invalid")
	}
	if index.ScalarSemantics != (ScalarSemantics{MaximumZoomMeaning: "exact evaluated point value sampled only where the ERA5 grid-point centre is land", ParentAggregation: "rounded arithmetic mean of available four child texels; no-data excluded"}) {
		return errors.New("ERA5 climate index scalar contract is invalid")
	}
	return nil
}

func monthReferenceSuperset(candidate, current []MonthReference) bool {
	if len(candidate) < len(current) {
		return false
	}
	set := make(map[MonthReference]struct{}, len(candidate))
	for _, reference := range candidate {
		set[reference] = struct{}{}
	}
	for _, reference := range current {
		if _, ok := set[reference]; !ok {
			return false
		}
	}
	return true
}

func readCurrentPublication(root string) (Pointer, Index, bool, error) {
	pointerPath := filepath.Join(root, "current.json")
	body, err := readRegularBounded(pointerPath, maximumPointerBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Pointer{}, Index{}, false, nil
	}
	if err != nil {
		return Pointer{}, Index{}, false, fmt.Errorf("read ERA5 climate pointer: %w", err)
	}
	var pointer Pointer
	if err := decodeExactJSON(body, &pointer); err != nil {
		return Pointer{}, Index{}, false, fmt.Errorf("decode ERA5 climate pointer: %w", err)
	}
	if pointer.Version != PointerVersion || !validIdentifier(pointer.IndexID) || !validSHA256(pointer.IndexSHA256) {
		return Pointer{}, Index{}, false, errors.New("ERA5 climate pointer is invalid")
	}
	index, digest, err := readIndex(root, pointer.IndexID)
	if err != nil {
		return Pointer{}, Index{}, false, err
	}
	if digest != pointer.IndexSHA256 {
		return Pointer{}, Index{}, false, errors.New("ERA5 climate index checksum does not match current pointer")
	}
	for _, reference := range index.Months {
		bound, catalog, err := readMonthReference(root, reference.ReleaseID)
		if err != nil {
			return Pointer{}, Index{}, false, fmt.Errorf("validate indexed ERA5 month %s: %w", reference.Period, err)
		}
		if bound != reference || catalog.Period != reference.Period {
			return Pointer{}, Index{}, false, errors.New("ERA5 climate index month reference changed")
		}
		if err := requireSameIndexContract(index, catalog); err != nil {
			return Pointer{}, Index{}, false, err
		}
	}
	return pointer, index, true, nil
}

func readIndex(root, indexID string) (Index, string, error) {
	path := filepath.Join(root, "indexes", indexID+".json")
	body, err := readRegularBounded(path, maximumIndexBytes)
	if err != nil {
		return Index{}, "", fmt.Errorf("read ERA5 climate index: %w", err)
	}
	var index Index
	if err := decodeExactJSON(body, &index); err != nil {
		return Index{}, "", fmt.Errorf("decode ERA5 climate index: %w", err)
	}
	if index.IndexID != indexID {
		return Index{}, "", errors.New("ERA5 climate index path and identity differ")
	}
	if err := validateIndexShape(index); err != nil {
		return Index{}, "", err
	}
	digest := sha256.Sum256(body)
	return index, hex.EncodeToString(digest[:]), nil
}

func readMonthReference(root, releaseID string) (MonthReference, Catalog, error) {
	releaseRoot := filepath.Join(root, "releases", releaseID)
	info, err := os.Lstat(releaseRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return MonthReference{}, Catalog{}, errors.New("ERA5 climate release must be a real directory")
	}
	body, err := readBounded(filepath.Join(releaseRoot, CatalogFilename), maximumCatalogBytes)
	if err != nil {
		return MonthReference{}, Catalog{}, err
	}
	catalog, err := ParseCatalog(body)
	if err != nil {
		return MonthReference{}, Catalog{}, err
	}
	if catalog.ReleaseID != releaseID || filepath.Base(releaseRoot) != catalog.ReleaseID {
		return MonthReference{}, Catalog{}, errors.New("ERA5 climate release identity changed")
	}
	digest := sha256.Sum256(body)
	return MonthReference{Period: catalog.Period, ReleaseID: releaseID, CatalogSHA256: hex.EncodeToString(digest[:])}, catalog, nil
}

func sameCatalog(left, right Catalog) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func installIndexAndPointer(ctx context.Context, root string, index Index) (Publication, error) {
	indexBody, err := encodeJSON(index, maximumIndexBytes)
	if err != nil {
		return Publication{}, err
	}
	indexDigest := sha256.Sum256(indexBody)
	indexSHA := hex.EncodeToString(indexDigest[:])
	target := filepath.Join(root, "indexes", index.IndexID+".json")
	if existing, err := readRegularBounded(target, maximumIndexBytes); err == nil {
		if !bytes.Equal(existing, indexBody) {
			return Publication{}, errors.New("immutable ERA5 climate index already exists with different content")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Publication{}, err
	} else {
		temporary, err := writeTemporary(filepath.Join(root, "indexes"), ".index-", indexBody)
		if err != nil {
			return Publication{}, err
		}
		defer os.Remove(temporary)
		if err := os.Link(temporary, target); err != nil {
			return Publication{}, fmt.Errorf("install immutable ERA5 climate index: %w", err)
		}
		if err := syncDirectory(filepath.Join(root, "indexes")); err != nil {
			return Publication{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	pointer := Pointer{Version: PointerVersion, IndexID: index.IndexID, IndexSHA256: indexSHA}
	pointerBody, err := encodeJSON(pointer, maximumPointerBytes)
	if err != nil {
		return Publication{}, err
	}
	temporary, err := writeTemporary(root, ".current-", pointerBody)
	if err != nil {
		return Publication{}, err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, filepath.Join(root, "current.json")); err != nil {
		return Publication{}, fmt.Errorf("publish ERA5 climate pointer: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		return Publication{}, fmt.Errorf("sync ERA5 climate pointer: %w", err)
	}
	return Publication{Pointer: pointer, Index: index}, nil
}

func preparePublicationRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return "", errors.New("ERA5 climate publication root must be a clean absolute path other than the filesystem root")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("ERA5 climate publication root must be an existing real directory")
	}
	for _, name := range []string{"releases", "indexes"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", err
		}
		child, err := os.Lstat(path)
		if err != nil || !child.IsDir() || child.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("ERA5 climate publication directories must be real directories")
		}
	}
	return root, nil
}

func existingPublicationRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return "", errors.New("ERA5 climate publication root must be a clean absolute path other than the filesystem root")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("ERA5 climate publication root must be an existing real directory")
	}
	return root, nil
}

func acquirePublicationLock(root string) (*os.File, error) {
	path := filepath.Join(root, ".publication.lock")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("ERA5 climate publication lock must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, errors.New("ERA5 climate publication lock must be a regular file")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func releasePublicationLock(lock *os.File) {
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func writeTemporary(directory, prefix string, body []byte) (string, error) {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	path := filepath.Join(directory, prefix+hex.EncodeToString(suffix[:])+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	written, writeErr := file.Write(body)
	if writeErr == nil && written != len(body) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func encodeJSON(value any, maximum int) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maximum {
		return nil, errors.New("ERA5 climate publication JSON exceeds its size limit")
	}
	return body, nil
}

func decodeExactJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON has trailing content")
	}
	return nil
}

func readRegularBounded(path string, maximum int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("ERA5 climate publication file must be a bounded regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != info.Size() {
		return nil, errors.New("ERA5 climate publication file changed while read")
	}
	return body, nil
}
