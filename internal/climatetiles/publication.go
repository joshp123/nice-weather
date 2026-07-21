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
	"syscall"
)

const maximumPointerBytes = 1024

var (
	privateBuildPattern   = regexp.MustCompile(`^\.build-[A-Za-z0-9][A-Za-z0-9._-]*-[0-9]+$`)
	privatePointerPattern = regexp.MustCompile(`^\.current-[0-9a-f]{24}\.json$`)
)

// Pointer is the only mutable climate publication artifact. Its checksum
// binds the pointer to one complete immutable catalogue.
type Pointer struct {
	ReleaseID     string `json:"releaseId"`
	CatalogSHA256 string `json:"catalogSha256"`
}

// Stage is a private artifact-shaped workspace. Existing tiledelivery writers
// can write beneath Root; direct point-block writers use ReleaseRoot.
type Stage struct {
	Root        string
	ReleaseRoot string
	ReleaseID   string
}

// Builder writes assets below the supplied private stage and returns their
// complete catalogue. BuildAndPublish owns catalog.json and publication.
type Builder func(context.Context, Stage) (Catalog, error)

// BuildAndPublish stages one release privately, validates and syncs it, moves
// it into the immutable releases namespace, validates it again, and writes
// climate/current.json last. It never accesses the main artifacts/current.
func BuildAndPublish(ctx context.Context, artifactRoot, releaseID string, build Builder) (result Pointer, resultErr error) {
	if ctx == nil || build == nil || !validReleaseID(releaseID) {
		return Pointer{}, errors.New("climate publication requires a context, release ID and builder")
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
	if err := validateExistingPointer(climateRoot); err != nil {
		return Pointer{}, err
	}
	target := filepath.Join(climateRoot, "releases", releaseID)
	staging, err := os.MkdirTemp(climateRoot, ".build-"+releaseID+"-")
	if err != nil {
		return Pointer{}, err
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
		return Pointer{}, err
	}
	catalog, err := build(ctx, Stage{Root: staging, ReleaseRoot: releaseRoot, ReleaseID: releaseID})
	if err != nil {
		return Pointer{}, err
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	if catalog.ReleaseID != releaseID {
		return Pointer{}, errors.New("climate builder returned a different release ID")
	}
	if err := writeCatalog(releaseRoot, catalog); err != nil {
		return Pointer{}, err
	}
	if _, err := ValidateRelease(releaseRoot); err != nil {
		return Pointer{}, fmt.Errorf("validate staged climate release: %w", err)
	}
	if err := syncTree(releaseRoot); err != nil {
		return Pointer{}, fmt.Errorf("sync staged climate release: %w", err)
	}
	candidate, candidateDigest, err := validateReleaseDigest(releaseRoot)
	if err != nil {
		return Pointer{}, fmt.Errorf("bind staged climate catalog: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	installed, installedDigest, err := installedRelease(target)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(releaseRoot, target); err != nil {
			return Pointer{}, fmt.Errorf("install climate release: %w", err)
		}
		if err := syncDirectory(filepath.Join(climateRoot, "releases")); err != nil {
			return Pointer{}, fmt.Errorf("sync climate releases: %w", err)
		}
		installed, installedDigest, err = installedRelease(target)
	}
	if err != nil {
		return Pointer{}, fmt.Errorf("validate installed climate release: %w", err)
	}
	if installedDigest != candidateDigest || installed.ReleaseID != candidate.ReleaseID {
		return Pointer{}, fmt.Errorf("immutable climate release %s already exists with different content", releaseID)
	}
	if err := syncTree(target); err != nil {
		return Pointer{}, fmt.Errorf("sync installed climate release: %w", err)
	}
	if err := syncDirectory(filepath.Join(climateRoot, "releases")); err != nil {
		return Pointer{}, fmt.Errorf("sync climate releases: %w", err)
	}
	if err := removePrivateStage(climateRoot, staging); err != nil {
		return Pointer{}, fmt.Errorf("clean installed climate stage: %w", err)
	}
	staging = ""
	pointer := Pointer{ReleaseID: installed.ReleaseID, CatalogSHA256: installedDigest}
	if err := ctx.Err(); err != nil {
		return Pointer{}, err
	}
	committed, err := writePointer(climateRoot, pointer)
	if err != nil {
		if committed {
			visible, _, visibleErr := validateCurrent(climateRoot)
			if visibleErr == nil && visible == pointer {
				return pointer, nil
			}
		}
		return Pointer{}, err
	}
	return pointer, nil
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

// ValidateCurrent verifies current.json, its exact catalog checksum and the
// complete release. It does not repair or change publication state.
func ValidateCurrent(artifactRoot string) (Pointer, Catalog, error) {
	climateRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return Pointer{}, Catalog{}, err
	}
	return validateCurrent(climateRoot)
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

func validateExistingPointer(climateRoot string) error {
	_, err := os.Lstat(filepath.Join(climateRoot, "current.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, _, err := validateCurrent(climateRoot); err != nil {
		return fmt.Errorf("refuse publication with invalid current climate release: %w", err)
	}
	return nil
}

func validateCurrent(climateRoot string) (Pointer, Catalog, error) {
	payload, err := readRegularBounded(filepath.Join(climateRoot, "current.json"), maximumPointerBytes)
	if err != nil {
		return Pointer{}, Catalog{}, fmt.Errorf("read climate pointer: %w", err)
	}
	var pointer Pointer
	if err := decodeExactJSON(payload, &pointer); err != nil || !validReleaseID(pointer.ReleaseID) || !validSHA256(pointer.CatalogSHA256) {
		return Pointer{}, Catalog{}, errors.New("climate pointer is invalid")
	}
	releaseRoot := filepath.Join(climateRoot, "releases", pointer.ReleaseID)
	catalog, catalogDigest, err := validateReleaseDigest(releaseRoot)
	if err != nil {
		return Pointer{}, Catalog{}, fmt.Errorf("validate current climate release: %w", err)
	}
	want := Pointer{ReleaseID: catalog.ReleaseID, CatalogSHA256: catalogDigest}
	if pointer != want {
		return Pointer{}, Catalog{}, errors.New("climate pointer catalog checksum does not match release")
	}
	return pointer, catalog, nil
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
	created, err = ensureRealDirectory(filepath.Join(climateRoot, "releases"))
	if err != nil {
		return "", err
	}
	if created {
		if err := syncDirectory(climateRoot); err != nil {
			return "", fmt.Errorf("sync climate releases parent: %w", err)
		}
		if err := syncDirectory(filepath.Join(climateRoot, "releases")); err != nil {
			return "", fmt.Errorf("sync climate releases root: %w", err)
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
	for _, directory := range []string{climateRoot, filepath.Join(climateRoot, "releases")} {
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
		path := filepath.Join(climateRoot, name)
		switch {
		case privateBuildPattern.MatchString(name):
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("private climate build entry %q is not a real directory", name)
			}
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove stale climate build %q: %w", name, err)
			}
			changed = true
		case privatePointerPattern.MatchString(name):
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("private climate pointer entry %q is not a regular file", name)
			}
			if err := os.Remove(path); err != nil {
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
