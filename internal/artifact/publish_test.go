package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishReleaseMakesCurrentVisible(t *testing.T) {
	root := t.TempDir()
	release, err := NewReleaseDir(root, "2026050506")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishRelease(root, release); err != nil {
		t.Fatal(err)
	}
	if PublishedRoot(root) != filepath.Join(root, "current") {
		t.Fatalf("PublishedRoot did not use current symlink")
	}
	data, err := os.ReadFile(filepath.Join(PublishedRoot(root), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("published manifest = %s", data)
	}
}
