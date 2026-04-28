package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesArtifactsOutsideWebBuild(t *testing.T) {
	root := t.TempDir()
	webDir := filepath.Join(root, "dist")
	artifactDir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(filepath.Join(artifactDir, "tiles", "forecast", "general", "d1", "0", "0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>app shell</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "manifest.json"), []byte(`{"layers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tilePath := filepath.Join(artifactDir, "tiles", "forecast", "general", "d1", "0", "0", "0.png")
	if err := os.WriteFile(tilePath, []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(Handler(webDir, artifactDir))
	defer server.Close()

	assertBody(t, server.URL+"/", http.StatusOK, "app shell")
	assertBody(t, server.URL+"/artifacts/manifest.json", http.StatusOK, `{"layers":[]}`)
	assertStatus(t, server.URL+"/artifacts/tiles/forecast/general/d1/0/0/0.png", http.StatusOK)
	assertStatus(t, server.URL+"/artifacts/tiles/forecast/general/d1/0/0/missing.png", http.StatusNotFound)
}

func assertBody(t *testing.T, url string, wantStatus int, wantSubstring string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status = %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if !strings.Contains(string(body), wantSubstring) {
		t.Fatalf("%s body = %q, want substring %q", url, body, wantSubstring)
	}
}

func assertStatus(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status = %d, want %d", url, resp.StatusCode, wantStatus)
	}
}
