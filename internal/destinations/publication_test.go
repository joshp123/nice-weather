package destinations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishKeepsOldPointerWhenImmutableArtifactConflicts(t *testing.T) {
	root := t.TempDir()
	first := WorldwidePointer{Schema: WorldwideSchema, ForecastIndexID: "weather-a", PlaceReleaseID: "places-a", URL: "/best-weather/releases/weather-a-aaaa.json", SHA256: "a"}
	relative := "best-weather/releases/weather-a-aaaa.json"
	if err := publish(root, relative, []byte("first"), first); err != nil {
		t.Fatal(err)
	}
	pointerPath := filepath.Join(root, "best-weather", "current.json")
	before, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	second := WorldwidePointer{Schema: WorldwideSchema, ForecastIndexID: "weather-b", PlaceReleaseID: "places-b", URL: "/best-weather/releases/weather-b-bbbb.json", SHA256: "b"}
	if err := publish(root, relative, []byte("different"), second); err == nil {
		t.Fatal("conflicting immutable artifact was accepted")
	}
	after, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("pointer changed after failed publication: before=%s after=%s", before, after)
	}
}

func TestPublishRejectsReleaseDirectoryAndTargetSymlinks(t *testing.T) {
	pointer := WorldwidePointer{Schema: WorldwideSchema, ForecastIndexID: "weather-a", PlaceReleaseID: "places-a", URL: "/best-weather/releases/weather-a-aaaa.json", SHA256: "a"}
	relative := "best-weather/releases/weather-a-aaaa.json"

	t.Run("release directory", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "best-weather"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "best-weather", "releases")); err != nil {
			t.Fatal(err)
		}
		if err := publish(root, relative, []byte("payload"), pointer); err == nil {
			t.Fatal("symlinked release directory was accepted")
		}
	})

	t.Run("immutable target", func(t *testing.T) {
		root := t.TempDir()
		releases := filepath.Join(root, "best-weather", "releases")
		if err := os.MkdirAll(releases, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(releases, filepath.Base(relative))); err != nil {
			t.Fatal(err)
		}
		if err := publish(root, relative, []byte("payload"), pointer); err == nil {
			t.Fatal("symlinked immutable target was accepted")
		}
		got, err := os.ReadFile(outside)
		if err != nil || string(got) != "outside" {
			t.Fatalf("outside target changed: %q err=%v", got, err)
		}
	})
}

func TestPublishRejectsSymlinkedArtifactRoot(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	pointer := WorldwidePointer{Schema: WorldwideSchema, ForecastIndexID: "weather-a", PlaceReleaseID: "places-a", URL: "/best-weather/releases/weather-a-aaaa.json", SHA256: "a"}
	if err := publish(linkRoot, "best-weather/releases/weather-a-aaaa.json", []byte("payload"), pointer); err == nil {
		t.Fatal("symlinked artifact root was accepted")
	}
}
