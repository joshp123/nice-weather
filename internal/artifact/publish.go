package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func PublishedRoot(root string) string {
	current := filepath.Join(root, "current")
	if _, err := os.Stat(filepath.Join(current, "manifest.json")); err == nil {
		return current
	}
	return root
}

func NewReleaseDir(root, name string) (string, error) {
	if name == "" || strings.Contains(name, string(filepath.Separator)) {
		return "", fmt.Errorf("invalid release name %q", name)
	}
	dir := filepath.Join(root, "releases", name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o755)
}

func PublishRelease(root, releaseDir string) error {
	if _, err := os.Stat(filepath.Join(releaseDir, "manifest.json")); err != nil {
		return fmt.Errorf("cannot publish release without manifest: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	target, err := filepath.Rel(root, releaseDir)
	if err != nil {
		return err
	}
	next := filepath.Join(root, ".current-next")
	if err := os.Remove(next); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, next); err != nil {
		return err
	}
	return os.Rename(next, filepath.Join(root, "current"))
}

func PruneReleases(root string, keep int) error {
	if keep <= 0 {
		return nil
	}
	releasesRoot := filepath.Join(root, "releases")
	entries, err := os.ReadDir(releasesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(releasesRoot, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
