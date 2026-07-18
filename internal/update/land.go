package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
)

const (
	LandSourceURL     = "https://naciscdn.org/naturalearth/5.1.1/10m/physical/ne_10m_land.zip"
	LandSourceSize    = int64(3269070)
	LandSourceSHA256  = "e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc"
	LandSourceLicense = "public domain"

	landDatasetName               = "ne_10m_land"
	landCacheVersion              = "natural-earth-5.1.1-10m-e547d749445e"
	maxLandMemberBytes      int64 = 48 << 20
	maxLandExtractedBytes         = 64 << 20
	maxLandMaskCells              = 100_000_000
	minimumLandDownloadRate       = 64 << 10
	minimumLandDownloadTime       = 30 * time.Second
	maximumLandDownloadTime       = 2 * time.Minute
)

var landMembers = []string{
	"ne_10m_land.README.html",
	"ne_10m_land.VERSION.txt",
	"ne_10m_land.cpg",
	"ne_10m_land.dbf",
	"ne_10m_land.prj",
	"ne_10m_land.shp",
	"ne_10m_land.shx",
}

type landSource struct {
	URL            string
	Size           int64
	SHA256         string
	AttemptTimeout time.Duration
}

type landHTTPStatusError struct {
	url, retryAfter string
	status          int
}

func (e *landHTTPStatusError) Error() string {
	return fmt.Sprintf("Natural Earth resource %s returned HTTP %d", e.url, e.status)
}
func (e *landHTTPStatusError) HTTPStatusCode() int     { return e.status }
func (e *landHTTPStatusError) RetryAfterValue() string { return e.retryAfter }

type landCacheManifest struct {
	SourceSHA256 string               `json:"sourceSha256"`
	Members      []landMemberManifest `json:"members"`
}

type landMemberManifest struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// LandAuthority owns the pinned Natural Earth polygon cache and turns it into
// exact, all-touched delivery-grid masks. A single authority memoizes masks by
// GridSpec for the lifetime of one update run.
type LandAuthority struct {
	cacheRoot string
	client    *http.Client
	runner    pipeline.CommandRunner
	source    landSource

	mu        sync.Mutex
	shapePath string
	masks     map[landMaskKey]pipeline.FloatGrid
}

type landMaskKey struct {
	Spec       delivery.GridSpec
	AllTouched bool
}

func NewLandAuthority(cacheRoot string, client *http.Client, runner pipeline.CommandRunner) *LandAuthority {
	if client == nil {
		client = http.DefaultClient
	}
	if runner == nil {
		runner = pipeline.ExecRunner{}
	}
	return newLandAuthority(cacheRoot, client, runner, landSource{URL: LandSourceURL, Size: LandSourceSize, SHA256: LandSourceSHA256})
}

func newLandAuthority(cacheRoot string, client *http.Client, runner pipeline.CommandRunner, source landSource) *LandAuthority {
	return &LandAuthority{cacheRoot: cacheRoot, client: client, runner: runner, source: source, masks: make(map[landMaskKey]pipeline.FloatGrid)}
}

// Prepare verifies or transactionally installs the pinned polygon dataset and
// returns the shapefile path. It is safe to call repeatedly on one authority.
func (authority *LandAuthority) Prepare(ctx context.Context) (string, error) {
	if authority == nil || authority.client == nil || authority.runner == nil {
		return "", errors.New("land authority is not configured")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.prepareLocked(ctx)
}

// Mask rasterizes Natural Earth polygons with GDAL all-touched semantics. Land
// is 1 and water is 0; values are row-major north to south like every product
// FloatGrid.
func (authority *LandAuthority) Mask(ctx context.Context, spec delivery.GridSpec) (pipeline.FloatGrid, error) {
	return authority.mask(ctx, spec, true)
}

// CenterMask marks a cell only when its centre falls on land. Climate uses it
// to distinguish core land evidence from all-touched coastal coverage.
func (authority *LandAuthority) CenterMask(ctx context.Context, spec delivery.GridSpec) (pipeline.FloatGrid, error) {
	return authority.mask(ctx, spec, false)
}

func (authority *LandAuthority) mask(ctx context.Context, spec delivery.GridSpec, allTouched bool) (pipeline.FloatGrid, error) {
	if err := validateLandGridSpec(spec); err != nil {
		return pipeline.FloatGrid{}, err
	}
	if authority == nil || authority.client == nil || authority.runner == nil {
		return pipeline.FloatGrid{}, errors.New("land authority is not configured")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	key := landMaskKey{Spec: spec, AllTouched: allTouched}
	if cached, ok := authority.masks[key]; ok {
		return cloneLandGrid(cached), nil
	}
	shapePath, err := authority.prepareLocked(ctx)
	if err != nil {
		return pipeline.FloatGrid{}, err
	}
	grid, err := authority.rasterizeLocked(ctx, shapePath, spec, allTouched)
	if err != nil {
		return pipeline.FloatGrid{}, err
	}
	authority.masks[key] = grid
	return cloneLandGrid(grid), nil
}

func (authority *LandAuthority) prepareLocked(ctx context.Context) (string, error) {
	if authority.shapePath != "" {
		return authority.shapePath, nil
	}
	if strings.TrimSpace(authority.cacheRoot) == "" {
		return "", errors.New("land cache root is required")
	}
	cacheDir := filepath.Join(authority.cacheRoot, "land", landCacheVersion)
	if err := validateLandCache(cacheDir, authority.source.SHA256); err == nil {
		authority.shapePath = filepath.Join(cacheDir, landDatasetName+".shp")
		return authority.shapePath, nil
	} else if _, statErr := os.Lstat(cacheDir); statErr == nil {
		if removeErr := os.RemoveAll(cacheDir); removeErr != nil {
			return "", fmt.Errorf("discard invalid land cache: %w", removeErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect land cache: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(cacheDir), ".land-install-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	archivePath := filepath.Join(temporary, "source.zip")
	if err := withRetry(ctx, func() error { return authority.download(ctx, archivePath) }); err != nil {
		return "", err
	}
	extracted := filepath.Join(temporary, "dataset")
	manifest, err := extractLandArchive(archivePath, extracted)
	if err != nil {
		return "", err
	}
	manifest.SourceSHA256 = authority.source.SHA256
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(extracted, "cache.json"), encoded, 0o644); err != nil {
		return "", err
	}
	if err := validateLandCache(extracted, authority.source.SHA256); err != nil {
		return "", fmt.Errorf("validate staged land cache: %w", err)
	}
	if err := os.Rename(extracted, cacheDir); err != nil {
		return "", fmt.Errorf("commit land cache: %w", err)
	}
	authority.shapePath = filepath.Join(cacheDir, landDatasetName+".shp")
	return authority.shapePath, nil
}

func (authority *LandAuthority) download(ctx context.Context, destination string) error {
	if authority.source.URL == "" || authority.source.Size <= 0 || len(authority.source.SHA256) != sha256.Size*2 {
		return errors.New("Natural Earth source identity is invalid")
	}
	attemptCtx, cancel := context.WithTimeout(ctx, authority.downloadTimeout())
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, authority.source.URL, nil)
	if err != nil {
		return err
	}
	response, err := authority.client.Do(request)
	if err != nil {
		return fmt.Errorf("download Natural Earth land: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &landHTTPStatusError{url: authority.source.URL, status: response.StatusCode, retryAfter: response.Header.Get("Retry-After")}
	}
	if response.ContentLength >= 0 && response.ContentLength != authority.source.Size {
		return fmt.Errorf("Natural Earth archive is %d bytes; want %d", response.ContentLength, authority.source.Size)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, authority.source.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download Natural Earth archive: %w", copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written != authority.source.Size {
		return fmt.Errorf("Natural Earth archive is %d bytes; want %d", written, authority.source.Size)
	}
	if actual := hex.EncodeToString(digest.Sum(nil)); actual != strings.ToLower(authority.source.SHA256) {
		return fmt.Errorf("Natural Earth archive SHA256 is %s; want %s", actual, authority.source.SHA256)
	}
	return nil
}

func (authority *LandAuthority) downloadTimeout() time.Duration {
	if authority.source.AttemptTimeout > 0 {
		return authority.source.AttemptTimeout
	}
	duration := time.Duration(authority.source.Size/minimumLandDownloadRate)*time.Second + 15*time.Second
	if duration < minimumLandDownloadTime {
		return minimumLandDownloadTime
	}
	if duration > maximumLandDownloadTime {
		return maximumLandDownloadTime
	}
	return duration
}

func extractLandArchive(archivePath, destination string) (landCacheManifest, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return landCacheManifest{}, fmt.Errorf("open Natural Earth archive: %w", err)
	}
	defer reader.Close()
	wanted := make(map[string]bool, len(landMembers))
	for _, name := range landMembers {
		wanted[name] = true
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return landCacheManifest{}, err
	}
	manifest := landCacheManifest{Members: make([]landMemberManifest, 0, len(landMembers))}
	seen := make(map[string]bool, len(landMembers))
	var total int64
	for _, member := range reader.File {
		name := member.Name
		if name != filepath.Base(name) || strings.Contains(name, "\\") || !wanted[name] || seen[name] || member.FileInfo().IsDir() || !member.Mode().IsRegular() {
			return landCacheManifest{}, fmt.Errorf("unexpected or unsafe Natural Earth archive member %q", name)
		}
		if member.UncompressedSize64 > uint64(maxLandMemberBytes) || total+int64(member.UncompressedSize64) > maxLandExtractedBytes {
			return landCacheManifest{}, fmt.Errorf("Natural Earth archive member %q exceeds extraction limit", name)
		}
		opened, err := member.Open()
		if err != nil {
			return landCacheManifest{}, err
		}
		path := filepath.Join(destination, name)
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			opened.Close()
			return landCacheManifest{}, err
		}
		digest := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, digest), io.LimitReader(opened, maxLandMemberBytes+1))
		closeErr := output.Close()
		openCloseErr := opened.Close()
		if copyErr != nil || closeErr != nil || openCloseErr != nil {
			return landCacheManifest{}, errors.Join(copyErr, closeErr, openCloseErr)
		}
		if written != int64(member.UncompressedSize64) || written > maxLandMemberBytes {
			return landCacheManifest{}, fmt.Errorf("Natural Earth archive member %q has invalid extracted size", name)
		}
		total += written
		seen[name] = true
		manifest.Members = append(manifest.Members, landMemberManifest{Name: name, Size: written, SHA256: hex.EncodeToString(digest.Sum(nil))})
	}
	for _, name := range landMembers {
		if !seen[name] {
			return landCacheManifest{}, fmt.Errorf("Natural Earth archive is missing %q", name)
		}
	}
	return manifest, nil
}

func validateLandCache(cacheDir, sourceSHA string) error {
	encoded, err := os.ReadFile(filepath.Join(cacheDir, "cache.json"))
	if err != nil {
		return err
	}
	var manifest landCacheManifest
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode land cache manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("land cache manifest has trailing content")
	}
	if manifest.SourceSHA256 != sourceSHA || len(manifest.Members) != len(landMembers) {
		return errors.New("land cache source identity or member count does not match")
	}
	wanted := make(map[string]bool, len(landMembers))
	for _, name := range landMembers {
		wanted[name] = true
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return err
	}
	if len(entries) != len(landMembers)+1 {
		return errors.New("land cache contains unexpected entries")
	}
	for _, entry := range entries {
		if entry.Name() != "cache.json" && !wanted[entry.Name()] {
			return fmt.Errorf("land cache contains unexpected entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("land cache entry %q is not a regular file", entry.Name())
		}
	}
	seen := make(map[string]bool, len(landMembers))
	var total int64
	for _, member := range manifest.Members {
		if !wanted[member.Name] || seen[member.Name] || member.Name != filepath.Base(member.Name) || member.Size < 0 || member.Size > maxLandMemberBytes {
			return fmt.Errorf("land cache member %q is invalid", member.Name)
		}
		path := filepath.Join(cacheDir, member.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != member.Size {
			return fmt.Errorf("land cache member %q is missing or has changed", member.Name)
		}
		digest, err := fileSHA256(path)
		if err != nil || digest != member.SHA256 {
			return fmt.Errorf("land cache member %q checksum changed", member.Name)
		}
		total += member.Size
		if total > maxLandExtractedBytes {
			return errors.New("land cache exceeds extraction limit")
		}
		seen[member.Name] = true
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (authority *LandAuthority) rasterizeLocked(ctx context.Context, shapePath string, spec delivery.GridSpec, allTouched bool) (pipeline.FloatGrid, error) {
	temporary, err := os.MkdirTemp("", "nice-weather-land-")
	if err != nil {
		return pipeline.FloatGrid{}, err
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "land.bil")
	bounds := spec.Bounds
	arguments := []string{"-q", "-of", "EHdr", "-ot", "Byte", "-burn", "1", "-init", "0"}
	if allTouched {
		arguments = append(arguments, "-at")
	}
	arguments = append(arguments,
		"-te", landCoordinate(bounds.West), landCoordinate(bounds.South), landCoordinate(bounds.East), landCoordinate(bounds.North),
		"-ts", strconv.FormatUint(uint64(spec.Cols), 10), strconv.FormatUint(uint64(spec.Rows), 10),
		"-l", landDatasetName, shapePath, output,
	)
	_, err = authority.runner.Run(ctx, "gdal_rasterize", arguments...)
	if err != nil {
		return pipeline.FloatGrid{}, fmt.Errorf("rasterize Natural Earth land: %w", err)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		return pipeline.FloatGrid{}, fmt.Errorf("read Natural Earth land mask: %w", err)
	}
	want := int64(spec.Rows) * int64(spec.Cols)
	if int64(len(payload)) != want {
		return pipeline.FloatGrid{}, fmt.Errorf("land mask has %d bytes; want %d", len(payload), want)
	}
	values := make([]float32, len(payload))
	for index, value := range payload {
		if value != 0 && value != 1 {
			return pipeline.FloatGrid{}, fmt.Errorf("land mask cell %d has value %d; want 0 or 1", index, value)
		}
		values[index] = float32(value)
	}
	return pipeline.FloatGrid{Spec: spec, Values: values}, nil
}

func validateLandGridSpec(spec delivery.GridSpec) error {
	if spec.Rows == 0 || spec.Cols == 0 || uint64(spec.Rows)*uint64(spec.Cols) > maxLandMaskCells {
		return errors.New("land mask grid dimensions are invalid or too large")
	}
	bounds := spec.Bounds
	coordinates := []float64{bounds.West, bounds.East, bounds.South, bounds.North}
	for _, coordinate := range coordinates {
		if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
			return errors.New("land mask grid bounds must be finite")
		}
	}
	if bounds.West >= bounds.East || bounds.South >= bounds.North || bounds.West < -180 || bounds.East > 180 || bounds.South < -90 || bounds.North > 90 {
		return errors.New("land mask grid bounds are invalid")
	}
	return nil
}

func cloneLandGrid(grid pipeline.FloatGrid) pipeline.FloatGrid {
	return pipeline.FloatGrid{Spec: grid.Spec, Values: append([]float32(nil), grid.Values...)}
}

func landCoordinate(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
