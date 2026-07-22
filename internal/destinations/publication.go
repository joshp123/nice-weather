package destinations

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
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/places"
)

const Schema = "nice-best-weather-v1"

type Artifact struct {
	Schema         string              `json:"schema"`
	WeatherRelease string              `json:"weatherReleaseId"`
	PlaceRelease   string              `json:"placeReleaseId"`
	StartDate      string              `json:"startDate"`
	EndDate        string              `json:"endDate"`
	GeneratedAt    string              `json:"generatedAt"`
	Profiles       map[string][]Result `json:"profiles"`
}

type Pointer struct {
	Schema         string `json:"schema"`
	WeatherRelease string `json:"weatherReleaseId"`
	PlaceRelease   string `json:"placeReleaseId"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
}

func (Pointer) publicationPointer() {}

type BuildSummary struct {
	ScannedPlaces int
	Bytes         int
	Elapsed       time.Duration
	Pointer       Pointer
}

// BuildCurrent evaluates the complete current place release against the exact
// current forecast-window grids, writes one immutable JSON artifact and rotates
// its small pointer last. Public requests only read these files; no ranking or
// full-index scan runs in the serving process.
func BuildCurrent(ctx context.Context, artifactRoot string) (BuildSummary, error) {
	started := time.Now()
	current, ok := delivery.CurrentDir(artifactRoot)
	if !ok {
		return BuildSummary{}, errors.New("weather release is not published")
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return BuildSummary{}, fmt.Errorf("resolve weather release: %w", err)
	}
	catalog, err := delivery.OpenProductionReleaseCatalog(resolved)
	if err != nil {
		return BuildSummary{}, fmt.Errorf("open weather release: %w", err)
	}
	defer catalog.Close()
	manifest := catalog.Manifest()
	var placePointer places.Pointer
	scanned := 0
	profiles, err := Evaluate(func(visit func(places.Candidate) error) error {
		var walkErr error
		placePointer, walkErr = places.WalkCurrent(artifactRoot, func(item places.Candidate) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			scanned++
			return visit(item)
		})
		return walkErr
	}, catalog.LookupRange)
	if err != nil {
		return BuildSummary{}, err
	}
	window := manifest.Forecast.Window
	payload, err := json.Marshal(Artifact{
		Schema: Schema, WeatherRelease: manifest.ReleaseID, PlaceRelease: placePointer.ReleaseID,
		StartDate: window.StartDate, EndDate: window.EndDate, GeneratedAt: manifest.GeneratedAt,
		Profiles: profiles,
	})
	if err != nil {
		return BuildSummary{}, err
	}
	if len(payload) > 32*1024 {
		return BuildSummary{}, fmt.Errorf("best-weather artifact has %d bytes; limit is 32768", len(payload))
	}
	digest := sha256.Sum256(payload)
	checksum := hex.EncodeToString(digest[:])
	relative := filepath.ToSlash(filepath.Join("best-weather", "releases", manifest.ReleaseID+"-"+checksum[:16]+".json"))
	pointer := Pointer{Schema: Schema, WeatherRelease: manifest.ReleaseID, PlaceRelease: placePointer.ReleaseID, URL: "/" + relative, SHA256: checksum}
	if err := publish(artifactRoot, relative, payload, pointer); err != nil {
		return BuildSummary{}, err
	}
	return BuildSummary{ScannedPlaces: scanned, Bytes: len(payload), Elapsed: time.Since(started), Pointer: pointer}, nil
}

type publicationPointer interface{ publicationPointer() }

func publish(root, relative string, payload []byte, pointer publicationPointer) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootInfo, err := os.Lstat(absolute)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact root must be a real directory")
	}
	rootFS, err := os.OpenRoot(absolute)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	openedRootInfo, err := rootFS.Stat(".")
	if err != nil || !os.SameFile(rootInfo, openedRootInfo) {
		return errors.New("artifact root changed while opening")
	}
	if err := ensureDirectory(rootFS, "best-weather"); err != nil {
		return err
	}
	bestFS, err := openCheckedSubroot(rootFS, "best-weather")
	if err != nil {
		return err
	}
	defer bestFS.Close()
	if err := ensureDirectory(bestFS, "releases"); err != nil {
		return err
	}
	releasesFS, err := openCheckedSubroot(bestFS, "releases")
	if err != nil {
		return err
	}
	defer releasesFS.Close()
	wantPrefix := filepath.ToSlash(filepath.Join("best-weather", "releases")) + "/"
	if filepath.ToSlash(filepath.Dir(relative))+"/" != wantPrefix {
		return errors.New("best-weather artifact escapes release root")
	}
	if err := installImmutable(releasesFS, filepath.Base(relative), payload); err != nil {
		return err
	}
	pointerPayload, err := json.Marshal(pointer)
	if err != nil {
		return err
	}
	return replacePointer(bestFS, pointerPayload)
}

func ensureDirectory(root *os.Root, name string) error {
	if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a real directory", name)
	}
	return nil
}

func openCheckedSubroot(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be a real directory", name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		_ = root.Close()
		return nil, fmt.Errorf("%s changed while opening", name)
	}
	return root, nil
}

func installImmutable(root *os.Root, target string, payload []byte) error {
	temporaryName, err := randomName()
	if err != nil {
		return err
	}
	temporary, err := root.OpenFile(temporaryName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = temporary.Close()
		_ = root.Remove(temporaryName)
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
	if err := root.Link(temporaryName, target); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := verifyExisting(root, target, payload); err != nil {
			return err
		}
		if err := root.Remove(temporaryName); err != nil {
			return err
		}
		return syncRoot(root)
	}
	if err := root.Remove(temporaryName); err != nil {
		return err
	}
	return syncRoot(root)
}

func verifyExisting(root *os.Root, target string, payload []byte) error {
	before, err := root.Lstat(target)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return errors.New("immutable best-weather artifact must be a regular file")
	}
	file, err := root.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return errors.New("immutable best-weather artifact changed while opening")
	}
	existing, err := io.ReadAll(io.LimitReader(file, int64(len(payload))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(existing, payload) {
		return errors.New("immutable best-weather artifact has different content")
	}
	return nil
}

func replacePointer(root *os.Root, payload []byte) error {
	temporaryName, err := randomName()
	if err != nil {
		return err
	}
	temporary, err := root.OpenFile(temporaryName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = root.Remove(temporaryName)
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
	if err := root.Rename(temporaryName, "current.json"); err != nil {
		return err
	}
	if err := syncRoot(root); err != nil {
		return err
	}
	committed = true
	return nil
}

func randomName() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return ".best-weather-" + hex.EncodeToString(value[:]) + ".tmp", nil
}

func syncRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
