// Package forecastruns publishes one complete 14-day forecast run assembled
// from already installed day/tier products. It owns no provider retrieval,
// scoring, colour policy, HTTP delivery or source orchestration.
package forecastruns

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
	"sort"
	"strings"
	"syscall"
	"time"

	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastrange"
)

const (
	PointerVersion = 1
	IndexVersion   = 2

	ProductContract = "nice-weather-forecast-run-v3"
	SelectionMethod = "choose-one-complete-tier-before-colour"

	IndexFilename = "index.json"

	maximumPointerBytes = 2048
	maximumIndexBytes   = 64 * 1024
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	datePattern       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	loadInstalledTier  = validateInstalledTier
	loadInstalledRange = forecastrange.ValidateInstalled
	publicationGate    = func() {}
)

// IndexReference identifies one immutable, cacheable run index.
type IndexReference struct {
	IndexID string `json:"indexId"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// Pointer is the only mutable forecast publication artifact. Previous is the
// exact complete run that Rollback will restore; publication failures leave it
// and Current unchanged.
type Pointer struct {
	Version  int             `json:"version"`
	Current  IndexReference  `json:"current"`
	Previous *IndexReference `json:"previous,omitempty"`
}

// TierReference binds one whole provider tier to its exact installed daily
// catalogue. Provider choice happens at this boundary, before a scalar field
// or colour is selected.
type TierReference struct {
	Date                 string  `json:"date"`
	Provider             string  `json:"provider"`
	Tier                 string  `json:"tier"`
	Product              string  `json:"product"`
	RunUTC               string  `json:"runUtc"`
	NativeResolutionKM   float64 `json:"nativeResolutionKm"`
	SourceEvidenceSHA256 string  `json:"sourceEvidenceSha256"`
	CatalogPath          string  `json:"catalogPath"`
	CatalogBytes         uint64  `json:"catalogBytes"`
	CatalogSHA256        string  `json:"catalogSha256"`
}

type Day struct {
	Date  string          `json:"date"`
	Tiers []TierReference `json:"tiers"`
}

type TierSelection struct {
	Method   string   `json:"method"`
	Priority []string `json:"priority"`
	Blend    bool     `json:"blend"`
}

// RangeReference binds the one worldwide 14-day judgement to its immutable
// catalogue. The catalogue, in turn, binds the exact ordered global daily
// catalogues used to build it.
type RangeReference struct {
	Period             string  `json:"period"`
	Provider           string  `json:"provider"`
	Tier               string  `json:"tier"`
	Product            string  `json:"product"`
	RunUTC             string  `json:"runUtc"`
	NativeResolutionKM float64 `json:"nativeResolutionKm"`
	StaleAfterHours    int     `json:"staleAfterHours"`
	CatalogPath        string  `json:"catalogPath"`
	CatalogBytes       uint64  `json:"catalogBytes"`
	CatalogSHA256      string  `json:"catalogSha256"`
}

// Index is one complete 14-day run. Every day has an IFS global baseline;
// only the first five days may add an ICON-EU tier. Tiers are ordered in the
// exact selection priority, so consumers choose one complete tier and then a
// scalar field. There is no representation for blended fields.
type Index struct {
	Version         int            `json:"version"`
	ProductContract string         `json:"productContract"`
	IndexID         string         `json:"indexId"`
	FirstDate       string         `json:"firstDate"`
	LastDate        string         `json:"lastDate"`
	TierSelection   TierSelection  `json:"tierSelection"`
	Days            []Day          `json:"days"`
	Range           RangeReference `json:"range"`
}

// Publish validates one already installed run, installs its immutable index,
// then changes current.json last. Cooperative publishers are serialized. A
// candidate whose IFS run is not newer than the visible run is stale unless it
// is byte-for-byte the current index; an intentional older move uses Rollback.
func Publish(ctx context.Context, artifactRoot, indexID string) (result Pointer, resultErr error) {
	if ctx == nil || !validIdentifier(indexID) {
		return Pointer{}, errors.New("forecast publication requires a context and safe index ID")
	}
	forecastRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	lock, err := acquireLock(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	defer releaseLock(lock)
	rootIdentity, err := directoryIdentity(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	runLock, err := acquireRunLock(forecastRoot, indexID)
	if err != nil {
		return Pointer{}, err
	}
	defer releaseLock(runLock)

	current, currentIndex, currentPayload, hasCurrent, err := readExistingPointer(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	// Re-publishing the exact visible immutable run is a read-only idempotent
	// operation. This also lets the one previous freshness contract age out;
	// every genuinely new candidate still passes the strict current contract.
	if hasCurrent && current.Current.IndexID == indexID {
		return current, nil
	}
	candidate, err := buildIndex(forecastRoot, indexID, true, false)
	if err != nil {
		return Pointer{}, err
	}
	payload, err := marshalIndex(candidate)
	if err != nil {
		return Pointer{}, err
	}
	sum := sha256.Sum256(payload)
	candidateReference := IndexReference{IndexID: indexID, Path: indexPath(indexID), SHA256: hex.EncodeToString(sum[:])}
	if hasCurrent {
		candidateRun, _ := baselineRun(candidate)
		currentRun, _ := baselineRun(currentIndex)
		if !candidateRun.After(currentRun) {
			return Pointer{}, fmt.Errorf("refuse stale forecast run %s; current IFS run is %s", candidateRun.Format(time.RFC3339), currentRun.Format(time.RFC3339))
		}
		if candidate.FirstDate < currentIndex.FirstDate || candidate.LastDate < currentIndex.LastDate {
			return Pointer{}, errors.New("refuse forecast run whose 14-day horizon moves backwards")
		}
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	if err := installIndex(forecastRoot, candidateReference, payload); err != nil {
		return Pointer{}, err
	}
	installed, err := validateIndexReference(forecastRoot, candidateReference)
	if err != nil {
		return Pointer{}, fmt.Errorf("validate installed forecast index: %w", err)
	}
	if !sameIndex(candidate, installed) {
		return Pointer{}, errors.New("installed forecast index changed from its candidate")
	}

	next := Pointer{Version: PointerVersion, Current: candidateReference}
	if hasCurrent {
		previous := current.Current
		next.Previous = &previous
	}
	publicationGate()
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	if err := verifyDirectoryIdentity(forecastRoot, rootIdentity); err != nil {
		return Pointer{}, err
	}
	// Revalidate after the installation window. A day installer that was still
	// changing a run, or a non-cooperating pointer writer, makes this attempt
	// fail closed. The immutable orphan index is safe to adopt on retry.
	if _, err := validateIndexReference(forecastRoot, candidateReference); err != nil {
		return Pointer{}, fmt.Errorf("forecast run changed before pointer publication: %w", err)
	}
	if err := verifyDirectoryIdentity(forecastRoot, rootIdentity); err != nil {
		return Pointer{}, err
	}
	if err := pointerUnchanged(forecastRoot, currentPayload, hasCurrent); err != nil {
		return Pointer{}, err
	}
	committed, err := writePointer(forecastRoot, next)
	if err != nil {
		if committed {
			visible, _, visibleErr := ValidateCurrent(artifactRoot)
			if visibleErr == nil && samePointer(visible, next) {
				return next, nil
			}
		}
		return Pointer{}, err
	}
	return next, nil
}

// Rollback atomically swaps Current and Previous after validating both exact
// immutable indexes again. It is the only operation that may move to an older
// provider run, and repeating it deterministically toggles the same two runs.
func Rollback(ctx context.Context, artifactRoot string) (Pointer, error) {
	if ctx == nil {
		return Pointer{}, errors.New("forecast rollback requires a context")
	}
	forecastRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, err
	}
	lock, err := acquireLock(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	defer releaseLock(lock)
	rootIdentity, err := directoryIdentity(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	pointer, _, payload, found, err := readExistingPointer(forecastRoot)
	if err != nil {
		return Pointer{}, err
	}
	if !found || pointer.Previous == nil {
		return Pointer{}, errors.New("forecast rollback has no previous complete run")
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	previous := pointer.Current
	next := Pointer{Version: PointerVersion, Current: *pointer.Previous, Previous: &previous}
	publicationGate()
	if err := verifyDirectoryIdentity(forecastRoot, rootIdentity); err != nil {
		return Pointer{}, err
	}
	if _, err := validateIndexReference(forecastRoot, next.Current); err != nil {
		return Pointer{}, fmt.Errorf("validate rollback target: %w", err)
	}
	if _, err := validateIndexReference(forecastRoot, *next.Previous); err != nil {
		return Pointer{}, fmt.Errorf("validate rollback source: %w", err)
	}
	if err := verifyDirectoryIdentity(forecastRoot, rootIdentity); err != nil {
		return Pointer{}, err
	}
	if err := pointerUnchanged(forecastRoot, payload, true); err != nil {
		return Pointer{}, err
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	committed, err := writePointer(forecastRoot, next)
	if err != nil && committed {
		visible, _, visibleErr := ValidateCurrent(artifactRoot)
		if visibleErr == nil && samePointer(visible, next) {
			return next, nil
		}
	}
	return next, err
}

// ValidateCurrent verifies the pointer, its current immutable index, all 14
// referenced day products and the optional previous complete run.
func ValidateCurrent(artifactRoot string) (Pointer, Index, error) {
	forecastRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, Index{}, err
	}
	pointer, index, _, found, err := readExistingPointer(forecastRoot)
	if err != nil {
		return Pointer{}, Index{}, err
	}
	if !found {
		return Pointer{}, Index{}, errors.New("forecast current pointer does not exist")
	}
	return pointer, index, nil
}

// LoadCurrentMetadata validates the mutable pointer and its current and
// previous immutable index bytes without reading any catalogue-referenced
// forecast payload. HTTP delivery uses this cheap metadata boundary to decide
// which run identities may be served; publication remains the owner of the
// exhaustive catalogue and asset validation.
func LoadCurrentMetadata(artifactRoot string) (Pointer, Index, *Index, error) {
	forecastRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, Index{}, nil, err
	}
	payload, err := readRegularBounded(filepath.Join(forecastRoot, "current.json"), maximumPointerBytes)
	if err != nil {
		return Pointer{}, Index{}, nil, err
	}
	pointer, err := parsePointer(payload)
	if err != nil {
		return Pointer{}, Index{}, nil, err
	}
	current, err := loadIndexMetadataReference(forecastRoot, pointer.Current)
	if err != nil {
		return Pointer{}, Index{}, nil, fmt.Errorf("load current forecast index metadata: %w", err)
	}
	var previous *Index
	if pointer.Previous != nil {
		loaded, err := loadIndexMetadataReference(forecastRoot, *pointer.Previous)
		if err != nil {
			return Pointer{}, Index{}, nil, fmt.Errorf("load previous forecast index metadata: %w", err)
		}
		previous = &loaded
	}
	return pointer, current, previous, nil
}

func loadIndexMetadataReference(forecastRoot string, reference IndexReference) (Index, error) {
	if err := validateIndexReferenceShape(reference); err != nil {
		return Index{}, err
	}
	payload, err := readRegularBounded(filepath.Join(forecastRoot, filepath.FromSlash(reference.Path)), maximumIndexBytes)
	if err != nil {
		return Index{}, err
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != reference.SHA256 {
		return Index{}, errors.New("forecast index checksum does not match its pointer")
	}
	index, err := parseIndex(payload)
	if err != nil {
		return Index{}, err
	}
	if index.IndexID != reference.IndexID {
		return Index{}, errors.New("forecast index path and identity differ")
	}
	return index, nil
}

func buildIndex(forecastRoot, indexID string, allowIndex, allowLegacyStaleAfter bool) (Index, error) {
	runRoot := filepath.Join(forecastRoot, "runs", indexID)
	entries, err := readRealDirectory(runRoot)
	if err != nil {
		return Index{}, fmt.Errorf("read forecast run: %w", err)
	}
	seenDays, seenRange := false, false
	for _, entry := range entries {
		switch entry.Name() {
		case "forecast-days":
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return Index{}, errors.New("forecast run forecast-days entry is unsafe")
			}
			seenDays = true
		case "forecast-ranges":
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return Index{}, errors.New("forecast run forecast-ranges entry is unsafe")
			}
			seenRange = true
		case IndexFilename:
			if !allowIndex || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return Index{}, errors.New("forecast run index entry is unexpected or unsafe")
			}
		case ".install.lock":
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return Index{}, errors.New("forecast run install lock is unsafe")
			}
		default:
			return Index{}, fmt.Errorf("forecast run entry %q is unexpected or unsafe", entry.Name())
		}
	}
	if !seenDays || !seenRange {
		return Index{}, errors.New("forecast run requires forecast-days and forecast-ranges directories")
	}
	daysRoot := filepath.Join(runRoot, "forecast-days")
	dateEntries, err := readRealDirectory(daysRoot)
	if err != nil {
		return Index{}, err
	}
	if len(dateEntries) != 14 {
		return Index{}, fmt.Errorf("forecast run has %d dates; want exactly 14", len(dateEntries))
	}
	dates := make([]string, 0, len(dateEntries))
	for _, entry := range dateEntries {
		if !entry.IsDir() || !validDate(entry.Name()) {
			return Index{}, fmt.Errorf("forecast run date entry %q is invalid", entry.Name())
		}
		dates = append(dates, entry.Name())
	}
	sort.Strings(dates)
	first, err := time.Parse("2006-01-02", dates[0])
	if err != nil {
		return Index{}, err
	}
	for offset, date := range dates {
		if date != first.AddDate(0, 0, offset).Format("2006-01-02") {
			return Index{}, errors.New("forecast run dates must be 14 consecutive calendar days")
		}
	}

	index := Index{
		Version: IndexVersion, ProductContract: ProductContract, IndexID: indexID,
		FirstDate: dates[0], LastDate: dates[len(dates)-1],
		TierSelection: TierSelection{Method: SelectionMethod, Priority: []string{forecastpoints.TierEurope, forecastpoints.TierGlobal}, Blend: false},
		Days:          make([]Day, 0, len(dates)),
	}
	for dayIndex, date := range dates {
		dateRoot := filepath.Join(daysRoot, date)
		tierEntries, err := readRealDirectory(dateRoot)
		if err != nil {
			return Index{}, err
		}
		wantTiers := map[string]bool{forecastpoints.TierGlobal: true}
		for _, entry := range tierEntries {
			if entry.Name() == forecastpoints.TierEurope && dayIndex < 5 {
				wantTiers[forecastpoints.TierEurope] = true
			}
		}
		if err := exactEntries(tierEntries, wantTiers, "forecast date "+date); err != nil {
			return Index{}, err
		}
		day := Day{Date: date}
		if _, ok := wantTiers[forecastpoints.TierEurope]; ok {
			reference, err := tierReference(forecastRoot, indexID, date, forecastpoints.TierEurope)
			if err != nil {
				return Index{}, err
			}
			day.Tiers = append(day.Tiers, reference)
		}
		baseline, err := tierReference(forecastRoot, indexID, date, forecastpoints.TierGlobal)
		if err != nil {
			return Index{}, err
		}
		day.Tiers = append(day.Tiers, baseline)
		index.Days = append(index.Days, day)
	}
	rangeReference, err := buildRangeReference(forecastRoot, index)
	if err != nil {
		return Index{}, err
	}
	index.Range = rangeReference
	if err := validateIndexShapeForRead(index, allowLegacyStaleAfter); err != nil {
		return Index{}, err
	}
	return index, nil
}

func buildRangeReference(forecastRoot string, index Index) (RangeReference, error) {
	if err := validateRangeParents(forecastRoot, index.IndexID); err != nil {
		return RangeReference{}, err
	}
	root := filepath.Join(forecastRoot, "runs", index.IndexID, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal)
	catalog, payload, err := loadInstalledRange(root)
	if err != nil {
		return RangeReference{}, fmt.Errorf("validate forecast range: %w", err)
	}
	if catalog.ReleaseID != index.IndexID || catalog.Period != forecastrange.Period ||
		!slices.Equal(catalog.Dates, indexDates(index)) || !slices.Equal(catalog.Inputs, rangeInputs(index)) {
		return RangeReference{}, errors.New("forecast range does not match the indexed global daily catalogues")
	}
	digest := sha256.Sum256(payload)
	return RangeReference{
		Period: forecastrange.Period, Provider: catalog.Source.Provider, Tier: catalog.Source.Tier,
		Product: catalog.Source.Product, RunUTC: catalog.Source.RunUTC,
		NativeResolutionKM: catalog.Source.NativeResolutionKM, StaleAfterHours: catalog.Source.StaleAfterHours,
		CatalogPath:  filepath.ToSlash(filepath.Join("runs", index.IndexID, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal, forecastrange.CatalogFilename)),
		CatalogBytes: uint64(len(payload)), CatalogSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func validateRangeParents(forecastRoot, indexID string) error {
	rangeRoot := filepath.Join(forecastRoot, "runs", indexID, "forecast-ranges")
	entries, err := readRealDirectory(rangeRoot)
	if err != nil {
		return err
	}
	if err := exactEntries(entries, map[string]bool{forecastrange.Period: true}, "forecast range root"); err != nil {
		return err
	}
	periodRoot := filepath.Join(rangeRoot, forecastrange.Period)
	entries, err = readRealDirectory(periodRoot)
	if err != nil {
		return err
	}
	return exactEntries(entries, map[string]bool{forecastpoints.TierGlobal: true}, "forecast range period")
}

func indexDates(index Index) []string {
	dates := make([]string, len(index.Days))
	for day := range index.Days {
		dates[day] = index.Days[day].Date
	}
	return dates
}

func rangeInputs(index Index) []forecastrange.InputCatalog {
	inputs := make([]forecastrange.InputCatalog, len(index.Days))
	prefix := filepath.ToSlash(filepath.Join("runs", index.IndexID)) + "/"
	for day, entry := range index.Days {
		global := entry.Tiers[len(entry.Tiers)-1]
		inputs[day] = forecastrange.InputCatalog{
			Date: entry.Date, Path: strings.TrimPrefix(global.CatalogPath, prefix),
			Bytes: global.CatalogBytes, SHA256: global.CatalogSHA256,
		}
	}
	return inputs
}

func tierReference(forecastRoot, indexID, date, tier string) (TierReference, error) {
	root := filepath.Join(forecastRoot, "runs", indexID, "forecast-days", date, tier)
	catalog, digest, bytes, err := loadInstalledTier(root)
	if err != nil {
		return TierReference{}, fmt.Errorf("validate forecast %s %s: %w", date, tier, err)
	}
	if catalog.ReleaseID != indexID || catalog.LocalDate != date || catalog.Tier != tier {
		return TierReference{}, errors.New("forecast catalogue does not match its run, date and tier path")
	}
	return TierReference{
		Date: date, Provider: catalog.Provider, Tier: tier, Product: catalog.Evidence.Product,
		RunUTC: catalog.Evidence.RunUTC, NativeResolutionKM: catalog.Evidence.NativeResolutionKM,
		SourceEvidenceSHA256: catalog.Evidence.SourceEvidenceSHA256,
		CatalogPath:          filepath.ToSlash(filepath.Join("runs", indexID, "forecast-days", date, tier, "catalog.json")),
		CatalogBytes:         bytes, CatalogSHA256: digest,
	}, nil
}

func validateInstalledTier(root string) (forecastcatalog.Catalog, string, uint64, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return forecastcatalog.Catalog{}, "", 0, errors.New("forecast tier root must be a real directory")
	}
	catalogPath := filepath.Join(root, "catalog.json")
	before, err := readRegularBounded(catalogPath, forecastcatalog.MaximumCatalogBytes)
	if err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	catalog, err := forecastcatalog.Parse(before)
	if err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	if err := validateTierTree(root, catalog); err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	if err := forecastcatalog.ValidateFiles(root, catalog); err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	if err := validateTierTree(root, catalog); err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	after, err := readRegularBounded(catalogPath, forecastcatalog.MaximumCatalogBytes)
	if err != nil {
		return forecastcatalog.Catalog{}, "", 0, err
	}
	beforeSum, afterSum := sha256.Sum256(before), sha256.Sum256(after)
	if beforeSum != afterSum {
		return forecastcatalog.Catalog{}, "", 0, errors.New("forecast catalogue changed during validation")
	}
	return catalog, hex.EncodeToString(beforeSum[:]), uint64(len(before)), nil
}

func validateTierTree(root string, catalog forecastcatalog.Catalog) error {
	expected := map[string]bool{".": true, "catalog.json": false}
	add := func(path string) error {
		if filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || strings.Contains(path, `\`) {
			return fmt.Errorf("forecast catalogue path %q is unsafe", path)
		}
		expected[filepath.FromSlash(path)] = false
		for parent := filepath.Dir(filepath.FromSlash(path)); parent != "."; parent = filepath.Dir(parent) {
			expected[parent] = true
		}
		return nil
	}
	for _, block := range catalog.PointBlocks {
		if !block.Omitted {
			if err := add(block.Path); err != nil {
				return err
			}
		}
	}
	for _, point := range catalog.DisplayPoints {
		if err := add(point.Path); err != nil {
			return err
		}
	}
	for _, scalar := range catalog.ScalarTiles {
		if err := add(scalar.Path); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(expected))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		wantDirectory, ok := expected[relative]
		if !ok {
			return fmt.Errorf("forecast tier has unreferenced entry %q", filepath.ToSlash(relative))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
			return fmt.Errorf("forecast tier entry %q has unsafe type", filepath.ToSlash(relative))
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("forecast tier is missing referenced entries")
	}
	return nil
}

func validateIndexShape(index Index) error {
	return validateIndexShapeForRead(index, false)
}

func validateIndexShapeForRead(index Index, allowLegacyStaleAfter bool) error {
	if index.Version != IndexVersion || index.ProductContract != ProductContract || !validIdentifier(index.IndexID) || len(index.Days) != 14 ||
		index.FirstDate != index.Days[0].Date || index.LastDate != index.Days[13].Date ||
		index.TierSelection.Method != SelectionMethod || index.TierSelection.Blend ||
		!slices.Equal(index.TierSelection.Priority, []string{forecastpoints.TierEurope, forecastpoints.TierGlobal}) {
		return errors.New("forecast run index identity, window or tier selection policy is invalid")
	}
	first, err := time.Parse("2006-01-02", index.FirstDate)
	if err != nil || index.LastDate != first.AddDate(0, 0, 13).Format("2006-01-02") {
		return errors.New("forecast run index dates are invalid")
	}
	var baselineRunUTC, europeRunUTC string
	for dayIndex, day := range index.Days {
		date := first.AddDate(0, 0, dayIndex).Format("2006-01-02")
		if day.Date != date || len(day.Tiers) < 1 || len(day.Tiers) > 2 {
			return errors.New("forecast run days or tier counts are invalid")
		}
		baseline := day.Tiers[len(day.Tiers)-1]
		if err := validateTierReference(index.IndexID, date, baseline, forecastpoints.ProviderIFS, forecastpoints.TierGlobal, forecastcatalog.ProductIFSControl, 28); err != nil {
			return err
		}
		if dayIndex == 0 {
			baselineRunUTC = baseline.RunUTC
		} else if baseline.RunUTC != baselineRunUTC {
			return errors.New("forecast global baseline mixes IFS runs")
		}
		if len(day.Tiers) == 2 {
			if dayIndex >= 5 {
				return errors.New("forecast Europe tier is allowed only for the first five dates")
			}
			europe := day.Tiers[0]
			if err := validateTierReference(index.IndexID, date, europe, forecastpoints.ProviderICONEU, forecastpoints.TierEurope, forecastcatalog.ProductICONEU, 7); err != nil {
				return err
			}
			if europeRunUTC == "" {
				europeRunUTC = europe.RunUTC
			} else if europe.RunUTC != europeRunUTC {
				return errors.New("forecast Europe refinement mixes ICON-EU runs")
			}
		}
	}
	if err := validateRangeReference(index, allowLegacyStaleAfter); err != nil {
		return err
	}
	return nil
}

func validateRangeReference(index Index, allowLegacyStaleAfter bool) error {
	wantPath := filepath.ToSlash(filepath.Join("runs", index.IndexID, "forecast-ranges", forecastrange.Period, forecastpoints.TierGlobal, forecastrange.CatalogFilename))
	run, err := time.Parse(time.RFC3339, index.Range.RunUTC)
	baseline := index.Days[0].Tiers[len(index.Days[0].Tiers)-1]
	staleAfterValid := index.Range.StaleAfterHours == forecastrange.StaleAfterHours || allowLegacyStaleAfter && forecastrange.SupportsStaleAfterHours(index.Range.StaleAfterHours)
	if index.Range.Period != forecastrange.Period || index.Range.Provider != forecastpoints.ProviderIFS ||
		index.Range.Tier != forecastpoints.TierGlobal || index.Range.Product != forecastcatalog.ProductIFSControl ||
		index.Range.RunUTC != baseline.RunUTC || err != nil || run.Location() != time.UTC || run.Nanosecond() != 0 ||
		index.Range.NativeResolutionKM != 28 || !staleAfterValid ||
		index.Range.CatalogPath != wantPath || index.Range.CatalogBytes == 0 || index.Range.CatalogBytes > forecastrange.MaximumCatalogBytes ||
		!validSHA256(index.Range.CatalogSHA256) {
		return errors.New("forecast run range reference is invalid")
	}
	return nil
}

func validateTierReference(indexID, date string, reference TierReference, provider, tier, product string, resolution float64) error {
	wantPath := filepath.ToSlash(filepath.Join("runs", indexID, "forecast-days", date, tier, "catalog.json"))
	run, runErr := time.Parse(time.RFC3339, reference.RunUTC)
	if reference.Date != date || reference.Provider != provider || reference.Tier != tier || reference.Product != product ||
		reference.NativeResolutionKM != resolution || runErr != nil || run.Location() != time.UTC || run.Nanosecond() != 0 ||
		!validSHA256(reference.SourceEvidenceSHA256) || reference.CatalogPath != wantPath || reference.CatalogBytes == 0 ||
		reference.CatalogBytes > forecastcatalog.MaximumCatalogBytes || !validSHA256(reference.CatalogSHA256) {
		return fmt.Errorf("forecast %s %s tier reference is invalid", date, tier)
	}
	return nil
}

func validateIndexReference(forecastRoot string, reference IndexReference) (Index, error) {
	if err := validateIndexReferenceShape(reference); err != nil {
		return Index{}, err
	}
	filename := filepath.Join(forecastRoot, filepath.FromSlash(reference.Path))
	before, err := readRegularBounded(filename, maximumIndexBytes)
	if err != nil {
		return Index{}, err
	}
	sum := sha256.Sum256(before)
	if hex.EncodeToString(sum[:]) != reference.SHA256 {
		return Index{}, errors.New("forecast index checksum does not match its pointer")
	}
	index, err := parseIndex(before)
	if err != nil {
		return Index{}, err
	}
	if index.IndexID != reference.IndexID {
		return Index{}, errors.New("forecast index path and identity differ")
	}
	rebuilt, err := buildIndex(forecastRoot, index.IndexID, true, true)
	if err != nil {
		return Index{}, err
	}
	if !sameIndex(index, rebuilt) {
		return Index{}, errors.New("forecast index does not match its exact installed day products")
	}
	after, err := readRegularBounded(filename, maximumIndexBytes)
	if err != nil {
		return Index{}, err
	}
	if !bytes.Equal(before, after) {
		return Index{}, errors.New("forecast index changed during validation")
	}
	return index, nil
}

func installIndex(forecastRoot string, reference IndexReference, payload []byte) error {
	target := filepath.Join(forecastRoot, filepath.FromSlash(reference.Path))
	if installed, err := readRegularBounded(target, maximumIndexBytes); err == nil {
		if !bytes.Equal(installed, payload) {
			return fmt.Errorf("immutable forecast index %s already exists with different content", reference.IndexID)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := filepath.Join(forecastRoot, ".index-"+hex.EncodeToString(nonce[:]))
	if err := writeExclusiveSynced(temporary, payload); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Link(temporary, target); err != nil {
		if installed, readErr := readRegularBounded(target, maximumIndexBytes); readErr == nil && bytes.Equal(installed, payload) {
			return nil
		}
		return fmt.Errorf("install immutable forecast index: %w", err)
	}
	return syncDirectory(filepath.Dir(target))
}

func readExistingPointer(forecastRoot string) (Pointer, Index, []byte, bool, error) {
	filename := filepath.Join(forecastRoot, "current.json")
	payload, err := readRegularBounded(filename, maximumPointerBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Pointer{}, Index{}, nil, false, nil
	}
	if err != nil {
		return Pointer{}, Index{}, nil, false, fmt.Errorf("read forecast pointer: %w", err)
	}
	pointer, err := parsePointer(payload)
	if err != nil {
		return Pointer{}, Index{}, nil, false, fmt.Errorf("refuse publication with invalid current forecast pointer: %w", err)
	}
	current, err := validateIndexReference(forecastRoot, pointer.Current)
	if err != nil {
		return Pointer{}, Index{}, nil, false, fmt.Errorf("validate current forecast index: %w", err)
	}
	if pointer.Previous != nil {
		if pointer.Current == *pointer.Previous {
			return Pointer{}, Index{}, nil, false, errors.New("forecast current and previous indexes must differ")
		}
		if _, err := validateIndexReference(forecastRoot, *pointer.Previous); err != nil {
			return Pointer{}, Index{}, nil, false, fmt.Errorf("validate previous forecast index: %w", err)
		}
	}
	return pointer, current, payload, true, nil
}

func parsePointer(payload []byte) (Pointer, error) {
	var pointer Pointer
	if err := decodeExactJSON(payload, &pointer); err != nil {
		return Pointer{}, err
	}
	if pointer.Version != PointerVersion || validateIndexReferenceShape(pointer.Current) != nil || (pointer.Previous != nil && validateIndexReferenceShape(*pointer.Previous) != nil) {
		return Pointer{}, errors.New("forecast pointer is invalid")
	}
	return pointer, nil
}

func validateIndexReferenceShape(reference IndexReference) error {
	if !validIdentifier(reference.IndexID) || reference.Path != indexPath(reference.IndexID) || !validSHA256(reference.SHA256) {
		return errors.New("forecast index reference is invalid")
	}
	return nil
}

func baselineRun(index Index) (time.Time, error) {
	if err := validateIndexShapeForRead(index, true); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, index.Days[0].Tiers[len(index.Days[0].Tiers)-1].RunUTC)
}

func marshalIndex(index Index) ([]byte, error) {
	if err := validateIndexShape(index); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if len(payload) > maximumIndexBytes {
		return nil, errors.New("forecast run index exceeds size limit")
	}
	return payload, nil
}

func parseIndex(payload []byte) (Index, error) {
	if len(payload) == 0 || len(payload) > maximumIndexBytes {
		return Index{}, errors.New("forecast run index size is invalid")
	}
	var index Index
	if err := decodeExactJSON(payload, &index); err != nil {
		return Index{}, err
	}
	if err := validateIndexShapeForRead(index, true); err != nil {
		return Index{}, err
	}
	return index, nil
}

func writePointer(forecastRoot string, pointer Pointer) (bool, error) {
	if _, err := parsePointer(mustJSON(pointer)); err != nil {
		return false, err
	}
	payload := mustJSON(pointer)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, err
	}
	temporary := filepath.Join(forecastRoot, ".current-"+hex.EncodeToString(nonce[:])+".json")
	if err := writeExclusiveSynced(temporary, payload); err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := os.Rename(temporary, filepath.Join(forecastRoot, "current.json")); err != nil {
		return false, fmt.Errorf("publish forecast pointer: %w", err)
	}
	committed = true
	if err := syncDirectory(forecastRoot); err != nil {
		return true, fmt.Errorf("sync forecast publication root: %w", err)
	}
	return true, nil
}

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return append(payload, '\n')
}

func pointerUnchanged(forecastRoot string, before []byte, existed bool) error {
	after, err := readRegularBounded(filepath.Join(forecastRoot, "current.json"), maximumPointerBytes)
	if !existed && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || !existed || !bytes.Equal(before, after) {
		return errors.New("forecast current pointer changed during publication")
	}
	return nil
}

func existingPublicationRoot(artifactRoot string) (string, error) {
	if artifactRoot == "" || !filepath.IsAbs(artifactRoot) || filepath.Clean(artifactRoot) != artifactRoot {
		return "", errors.New("artifact root must be an absolute clean path")
	}
	info, err := os.Lstat(artifactRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("artifact root must be an existing real directory")
	}
	resolved, err := filepath.EvalSymlinks(artifactRoot)
	if err != nil {
		return "", err
	}
	forecastRoot := filepath.Join(resolved, "forecast")
	for _, directory := range []string{forecastRoot, filepath.Join(forecastRoot, "runs")} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("forecast publication roots must be existing real directories")
		}
	}
	return forecastRoot, nil
}

func readRealDirectory(path string) ([]fs.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be a real directory", path)
	}
	return os.ReadDir(path)
}

func exactEntries(entries []fs.DirEntry, expected map[string]bool, owner string) error {
	if len(entries) != len(expected) {
		return fmt.Errorf("%s has %d entries; want exactly %d", owner, len(entries), len(expected))
	}
	for _, entry := range entries {
		wantDirectory, ok := expected[entry.Name()]
		if !ok || entry.IsDir() != wantDirectory || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s entry %q is unexpected or unsafe", owner, entry.Name())
		}
	}
	return nil
}

func readRegularBounded(filename string, maximum int) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("forecast publication file must be a bounded regular file")
	}
	payload, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || len(payload) > maximum {
		return nil, errors.New("forecast publication file size changed while reading")
	}
	return payload, nil
}

func writeExclusiveSynced(filename string, payload []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	open := true
	defer func() {
		if open {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	open = false
	return nil
}

func decodeExactJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("forecast publication JSON has trailing content")
	}
	return nil
}

func acquireLock(forecastRoot string) (*os.File, error) {
	filename := filepath.Join(forecastRoot, ".publication.lock")
	fd, err := syscall.Open(filename, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open real forecast publication lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), filename)
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, errors.New("forecast publication lock changed while opening")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another forecast publication is active: %w", err)
	}
	return lock, nil
}

func acquireRunLock(forecastRoot, indexID string) (*os.File, error) {
	runRoot := filepath.Join(forecastRoot, "runs", indexID)
	info, err := os.Lstat(runRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("forecast candidate run must be an existing real directory")
	}
	filename := filepath.Join(runRoot, ".install.lock")
	fd, err := syscall.Open(filename, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open forecast run install lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), filename)
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, errors.New("forecast run install lock changed while opening")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("forecast run installation or publication is active: %w", err)
	}
	return lock, nil
}

func directoryIdentity(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("forecast publication root must remain a real directory")
	}
	return info, nil
}

// verifyDirectoryIdentity is the containment boundary for this trusted,
// single-user build pipeline. It catches a moved or symlink-replaced
// publication root around the explicit publication gate. Legitimate writers
// must also hold the publication lock; this is not a hostile multi-user
// filesystem sandbox.
func verifyDirectoryIdentity(path string, want fs.FileInfo) error {
	got, err := directoryIdentity(path)
	if err != nil || !os.SameFile(want, got) {
		return errors.New("forecast publication root changed during publication")
	}
	return nil
}

func releaseLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func sameIndex(left, right Index) bool {
	return left.Version == right.Version && left.ProductContract == right.ProductContract && left.IndexID == right.IndexID &&
		left.FirstDate == right.FirstDate && left.LastDate == right.LastDate && left.TierSelection.Method == right.TierSelection.Method &&
		left.TierSelection.Blend == right.TierSelection.Blend && slices.Equal(left.TierSelection.Priority, right.TierSelection.Priority) && left.Range == right.Range && slices.EqualFunc(left.Days, right.Days, func(a, b Day) bool {
		return a.Date == b.Date && slices.Equal(a.Tiers, b.Tiers)
	})
}

func samePointer(left, right Pointer) bool {
	return left.Version == right.Version && left.Current == right.Current &&
		((left.Previous == nil && right.Previous == nil) || (left.Previous != nil && right.Previous != nil && *left.Previous == *right.Previous))
}

func indexPath(indexID string) string {
	return filepath.ToSlash(filepath.Join("runs", indexID, IndexFilename))
}

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value) && value != "." && value != ".."
}

func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
