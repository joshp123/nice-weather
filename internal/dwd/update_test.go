package dwd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneCachedRunsKeepsNewestRuns(t *testing.T) {
	root := t.TempDir()
	for _, run := range []string{"2026050418", "2026050500", "2026050506"} {
		if err := os.MkdirAll(filepath.Join(root, run), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneCachedRuns(root, 2); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, "2026050418")); !os.IsNotExist(err) {
		t.Fatalf("oldest cache run still exists: %v", err)
	}
	for _, run := range []string{"2026050500", "2026050506"} {
		if _, err := os.Stat(filepath.Join(root, run)); err != nil {
			t.Fatalf("expected cached run %s: %v", run, err)
		}
	}
}

func TestPruneCachedRunsRemovesCompressedCopies(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "2026050506")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gribPath := filepath.Join(runDir, "field.grib2")
	if err := os.WriteFile(gribPath, []byte("decoded"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gribPath+".bz2", []byte("compressed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PruneCachedRuns(root, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gribPath + ".bz2"); !os.IsNotExist(err) {
		t.Fatalf("compressed cache file still exists: %v", err)
	}
}

func TestDownloadFieldRemovesCompressedFileWhenGribExists(t *testing.T) {
	root := t.TempDir()
	run := Run{ID: "2026050506", Hour: "06"}
	gribPath := filepath.Join(root, "icon-eu_europe_regular-lat-lon_single-level_2026050506_000_t_2m.grib2")
	if err := os.WriteFile(gribPath, []byte("decoded"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gribPath+".bz2", []byte("compressed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := downloadField(context.Background(), run, root, "t_2m", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gribPath + ".bz2"); !os.IsNotExist(err) {
		t.Fatalf("compressed cache file still exists: %v", err)
	}
}

func TestRunCoversRequestedFullLocalDays(t *testing.T) {
	sixZ := Run{ID: "2026050506", Hour: "06", StartedAt: time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC)}
	twelveZ := Run{ID: "2026050512", Hour: "12", StartedAt: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)}
	if !RunCoversLocalDays(sixZ, 5, 120) {
		t.Fatal("06Z run should cover five full Europe local daytime windows")
	}
	if RunCoversLocalDays(twelveZ, 5, 120) {
		t.Fatal("12Z run should not cover five full Europe local daytime windows")
	}
	if !RunCoversLocalDays(twelveZ, 4, 120) {
		t.Fatal("12Z run should cover four full Europe local daytime windows")
	}
}
