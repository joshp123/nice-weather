package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const manifestFilename = "manifest.json"

var releaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func BeginRelease(root, name string) (string, error) {
	if !releaseNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid release name %q", name)
	}
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		return "", err
	}
	dir := filepath.Join(releases, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	if err := syncDirectory(releases); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteManifest(releaseDir string, manifest Manifest) error {
	data, err := manifest.JSON()
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(releaseDir, manifestFilename)
	tmp, err := os.CreateTemp(releaseDir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := syncDirectory(releaseDir); err != nil {
		return err
	}
	committed = true
	return nil
}

func ValidateRelease(releaseDir string) (Manifest, error) {
	catalog, err := openReleaseCatalog(releaseDir, false)
	if err != nil {
		return Manifest{}, err
	}
	defer catalog.Close()
	return catalog.Manifest(), nil
}

func ValidateProductionRelease(releaseDir string) (Manifest, error) {
	catalog, err := openReleaseCatalog(releaseDir, true)
	if err != nil {
		return Manifest{}, err
	}
	defer catalog.Close()
	return catalog.Manifest(), nil
}

type ReleaseCatalog struct {
	manifest Manifest
	byPeriod map[string]*GridReader
	byPath   map[string]*GridReader
}

func OpenReleaseCatalog(releaseDir string) (*ReleaseCatalog, error) {
	return openReleaseCatalog(releaseDir, false)
}

func OpenProductionReleaseCatalog(releaseDir string) (*ReleaseCatalog, error) {
	return openReleaseCatalog(releaseDir, true)
}

func (catalog *ReleaseCatalog) Manifest() Manifest { return catalog.manifest }

func (catalog *ReleaseCatalog) Lookup(periodID string, lon, lat float64) (DailyRecord, bool, error) {
	reader, ok := catalog.byPeriod[periodID]
	if !ok {
		return DailyRecord{}, false, fmt.Errorf("unknown period %q", periodID)
	}
	return reader.RecordAt(lon, lat)
}

func (catalog *ReleaseCatalog) Close() error {
	var result error
	for _, reader := range catalog.byPath {
		if err := reader.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}
	catalog.byPath = nil
	catalog.byPeriod = nil
	return result
}

func openReleaseCatalog(releaseDir string, production bool) (*ReleaseCatalog, error) {
	inputInfo, err := os.Lstat(releaseDir)
	if err != nil {
		return nil, fmt.Errorf("inspect release directory: %w", err)
	}
	if inputInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("release directory must not be a symlink")
	}
	absolute, err := filepath.Abs(releaseDir)
	if err != nil {
		return nil, err
	}
	releasesRoot := filepath.Dir(absolute)
	if filepath.Base(releasesRoot) != "releases" || !releaseNamePattern.MatchString(filepath.Base(absolute)) {
		return nil, errors.New("release directory must be a direct child of releases")
	}
	canonicalRoot, err := filepath.EvalSymlinks(releasesRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve releases root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve release directory: %w", err)
	}
	relative, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || filepath.Dir(relative) != "." || !releaseNamePattern.MatchString(relative) {
		return nil, errors.New("resolved release directory escapes the canonical releases root")
	}
	releaseDir = resolved
	info, err := os.Lstat(releaseDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("not a regular directory")
		}
		return nil, fmt.Errorf("validate release directory: %w", err)
	}
	manifestPath, err := releaseAsset(releaseDir, manifestFilename)
	if err != nil {
		return nil, fmt.Errorf("validate release manifest: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read release manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	if manifest.ReleaseID != filepath.Base(releaseDir) {
		return nil, fmt.Errorf("manifest releaseId %q does not match release directory %q", manifest.ReleaseID, filepath.Base(releaseDir))
	}
	if production {
		if err := ValidateProductionManifest(manifest); err != nil {
			return nil, err
		}
	}
	catalog := &ReleaseCatalog{manifest: manifest, byPeriod: make(map[string]*GridReader), byPath: make(map[string]*GridReader)}
	success := false
	defer func() {
		if !success {
			_ = catalog.Close()
		}
	}()
	periods := make([]Period, 0, len(manifest.Forecast.Days)+12)
	for _, day := range manifest.Forecast.Days {
		periods = append(periods, day.Period)
	}
	if manifest.Climate != nil {
		for _, month := range manifest.Climate.Months {
			periods = append(periods, month.Period)
		}
	}
	for _, period := range periods {
		gridPath, err := releaseAsset(releaseDir, period.GridURL)
		if err != nil {
			return nil, err
		}
		reader, exists := catalog.byPath[gridPath]
		if !exists {
			reader, err = OpenGrid(gridPath)
			if err != nil {
				return nil, fmt.Errorf("validate grid %q: %w", period.GridURL, err)
			}
			catalog.byPath[gridPath] = reader
		}
		if reader.Metadata().SHA256 != strings.ToLower(period.GridSHA256) {
			return nil, fmt.Errorf("grid %q checksum does not match manifest", period.GridURL)
		}
		if reader.Metadata().Bounds != manifest.Region.Bounds {
			return nil, fmt.Errorf("grid %q bounds do not match manifest region", period.GridURL)
		}
		catalog.byPeriod[period.ID] = reader
		imageWidth, imageHeight, err := WebMercatorImageDimensions(GridSpec{
			Bounds: reader.Metadata().Bounds, Rows: reader.Metadata().Rows, Cols: reader.Metadata().Cols,
		})
		if err != nil {
			return nil, fmt.Errorf("derive image geometry for %q: %w", period.ID, err)
		}
		for _, asset := range period.Images {
			path, err := releaseAsset(releaseDir, asset.URL)
			if err != nil {
				return nil, err
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				if err == nil {
					err = errors.New("not a regular file")
				}
				return nil, fmt.Errorf("validate image %q: %w", asset.URL, err)
			}
			if err := validatePNG(path, asset); err != nil {
				return nil, err
			}
			if asset.Width != imageWidth || asset.Height != imageHeight {
				return nil, fmt.Errorf("image %q geometry does not match its grid", asset.URL)
			}
		}
	}
	success = true
	return catalog, nil
}

func validatePNG(path string, asset ImageAsset) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(asset.SHA256) {
		return fmt.Errorf("image %q checksum does not match manifest", asset.URL)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	config, err := png.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("image %q is not a valid PNG: %w", asset.URL, err)
	}
	if config.Width != asset.Width || config.Height != asset.Height {
		return fmt.Errorf("image %q dimensions are %dx%d; want %dx%d", asset.URL, config.Width, config.Height, asset.Width, asset.Height)
	}
	return nil
}

func PublishRelease(root, releaseDir string) error {
	return publishRelease(root, releaseDir, false)
}

func PublishProductionRelease(root, releaseDir string) error {
	return publishRelease(root, releaseDir, true)
}

func publishRelease(root, releaseDir string, production bool) error {
	releasesRoot := filepath.Join(root, "releases")
	rel, err := filepath.Rel(releasesRoot, releaseDir)
	if err != nil || rel == "." || filepath.Dir(rel) != "." || strings.HasPrefix(rel, "..") {
		return errors.New("release must be a direct child of the releases directory")
	}
	if production {
		_, err = ValidateProductionRelease(releaseDir)
	} else {
		_, err = ValidateRelease(releaseDir)
	}
	if err != nil {
		return fmt.Errorf("release is not publishable: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	newTarget, err := filepath.Rel(root, releaseDir)
	if err != nil {
		return err
	}
	oldCurrent, currentValid := usablePointer(root, "current")
	previousValid := false
	if !currentValid {
		_, previousValid = usablePointer(root, "previous")
	}
	if currentValid {
		if err := replaceSymlink(root, ".previous-next", "previous", oldCurrent); err != nil {
			return err
		}
	}
	if err := replaceSymlink(root, ".current-next", "current", newTarget); err != nil {
		return err
	}
	if !currentValid && !previousValid {
		if err := os.Remove(filepath.Join(root, "previous")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return syncDirectory(root)
	}
	return nil
}

func CurrentDir(root string) (string, bool)  { return publishedDir(root, "current") }
func PreviousDir(root string) (string, bool) { return publishedDir(root, "previous") }

func replaceSymlink(root, temporaryName, publicName, target string) error {
	if err := validatePointerTarget(target); err != nil {
		return err
	}
	temporary := filepath.Join(root, temporaryName)
	if err := os.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := syncDirectory(root); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, publicName)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(root)
}

func validatePointerTarget(target string) error {
	clean := filepath.Clean(target)
	if clean != target || filepath.IsAbs(target) || filepath.Dir(clean) != "releases" || !releaseNamePattern.MatchString(filepath.Base(clean)) {
		return errors.New("release pointer must target a direct child of releases")
	}
	return nil
}

func CleanupStaleReleases(root string) error {
	currentTarget, currentValid := usablePointer(root, "current")
	previousTarget, previousValid := usablePointer(root, "previous")
	if !currentValid && !previousValid {
		return errors.New("refusing cleanup without a usable current or previous release")
	}
	if !currentValid {
		if err := replaceSymlink(root, ".current-recovery", "current", previousTarget); err != nil {
			return fmt.Errorf("promote previous release to current: %w", err)
		}
		currentTarget, currentValid = usablePointer(root, "current")
		if !currentValid {
			return errors.New("recovered current release did not validate")
		}
	}
	if !previousValid {
		if err := os.Remove(filepath.Join(root, "previous")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := syncDirectory(root); err != nil {
			return err
		}
	}
	keep := map[string]struct{}{filepath.Base(currentTarget): {}}
	if previousValid {
		keep[filepath.Base(previousTarget)] = struct{}{}
	}
	for _, staging := range []string{".current-next", ".previous-next", ".current-recovery"} {
		if err := os.Remove(filepath.Join(root, staging)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	releases := filepath.Join(root, "releases")
	entries, err := os.ReadDir(releases)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, retained := keep[entry.Name()]; retained {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(releases, entry.Name())
		if _, err := ValidateRelease(candidate); err != nil {
			// The directory may be another writer's active transaction. Its
			// owner is responsible for removing it after a failed build.
			continue
		}
		if err := os.RemoveAll(candidate); err != nil {
			return err
		}
	}
	if err := syncDirectory(releases); err != nil {
		return err
	}
	return syncDirectory(root)
}

func usablePointer(root, name string) (string, bool) {
	target, err := os.Readlink(filepath.Join(root, name))
	if err != nil || validatePointerTarget(target) != nil {
		return "", false
	}
	if _, err := ValidateRelease(filepath.Join(root, target)); err != nil {
		return "", false
	}
	return target, true
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func publishedDir(root, name string) (string, bool) {
	path := filepath.Join(root, name)
	info, err := os.Stat(filepath.Join(path, manifestFilename))
	return path, err == nil && info.Mode().IsRegular()
}

func releaseAsset(releaseDir, relative string) (string, error) {
	if err := validateAssetPath(relative); err != nil {
		return "", err
	}
	path := releaseDir
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("release asset %q must not contain symlinks", relative)
		}
	}
	return path, nil
}
