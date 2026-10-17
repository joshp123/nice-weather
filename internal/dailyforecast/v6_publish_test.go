package dailyforecast

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nice-weather-finder/outlook"
)

func TestPublishSwitchesV6PointerAfterData(t *testing.T) {
	for _, scenario := range []struct{ name, previousRun, previousPrevious, wantPrevious string }{
		{"new run", "20261015T0000Z", "20261014T0000Z", "20261015T0000Z"},
		{"same run", "20261016T0000Z", "20261015T0000Z", "20261015T0000Z"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runID := "20261016T0000Z"
			directory := publicationFixture(t, runID)
			var mutex sync.Mutex
			var requests []string
			uploaded := map[string][]byte{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mutex.Lock()
				defer mutex.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/data/v6/forecast/current.json":
					json.NewEncoder(w).Encode(v6Pointer{Run: scenario.previousRun, PreviousRun: scenario.previousPrevious})
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/__publish/"):
					if r.Header.Get("Authorization") != "Bearer test-only-token" {
						t.Error("missing publisher authorization")
					}
					uploaded[r.URL.Path] = body
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodPost && r.URL.Path == "/__prune":
					if uploaded["/__publish/v6/forecast/current.json"] == nil {
						t.Error("prune ran before the v6 pointer was published")
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			report, err := Publish(context.Background(), directory+string(filepath.Separator), server.URL, "test-only-token")
			if err != nil {
				t.Fatal(err)
			}
			if report.Files != 26 {
				t.Fatalf("uploaded files=%d, want 26: v6 day, fifteen score images, nine outlook files, pointer", report.Files)
			}
			if len(uploaded["/__publish/v6/forecast/"+runID+"/days.bin"]) != 14*15 {
				t.Fatal("v6 day records were not uploaded intact")
			}
			var pointer v6Pointer
			if err = json.Unmarshal(uploaded["/__publish/v6/forecast/current.json"], &pointer); err != nil {
				t.Fatal(err)
			}
			if pointer.Run != runID || pointer.FirstDate != "2026-10-16" || pointer.Days != 14 || pointer.PreviousRun != scenario.wantPrevious {
				t.Fatalf("wrong v6 pointer: %+v", pointer)
			}
			positions := map[string]int{}
			for i, r := range requests {
				positions[r] = i
			}
			if positions["PUT /__publish/v6/forecast/"+runID+"/days.bin"] >= positions["PUT /__publish/v6/forecast/current.json"] {
				t.Fatal("v6 pointer was published before its data")
			}
			for _, name := range v6ScoreImageNames() {
				key := "/__publish/v6/forecast/" + runID + "/" + name
				if string(uploaded[key]) != "score image" {
					t.Fatalf("score image %s was not uploaded intact", name)
				}
				if positions["PUT "+key] >= positions["PUT /__publish/v6/forecast/current.json"] {
					t.Fatalf("v6 pointer was published before %s", name)
				}
			}
			for _, name := range outlook.FileNames() {
				key := "/__publish/v6/forecast/" + runID + "/" + name
				if string(uploaded[key]) != "outlook file" || positions["PUT "+key] >= positions["PUT /__publish/v6/forecast/current.json"] {
					t.Fatalf("outlook %s was not uploaded intact before the pointer", name)
				}
			}
			if requests[len(requests)-1] != "POST /__prune" {
				t.Fatal("cleanup was not last")
			}
		})
	}
}

func TestPublishDoesNotSwitchForecastPointersWhenV6DataUploadFails(t *testing.T) {
	for _, failedName := range []string{"days.bin", "scores-day-06.png", "outlook/europe.png"} {
		t.Run(failedName, func(t *testing.T) {
			directory := publicationFixture(t, "20261016T0000Z")
			var mutex sync.Mutex
			var pointers, prunes int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutex.Lock()
				defer mutex.Unlock()
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == http.MethodPost {
					prunes++
					w.WriteHeader(http.StatusOK)
					return
				}
				if strings.HasSuffix(r.URL.Path, "current.json") {
					pointers++
				}
				if strings.HasSuffix(r.URL.Path, "/"+failedName) {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			_, err := Publish(context.Background(), directory, server.URL, "test-only-token")
			if err == nil || !strings.Contains(err.Error(), "v6/forecast/20261016T0000Z/"+failedName) {
				t.Fatalf("expected v6 data upload error, got %v", err)
			}
			if pointers != 0 || prunes != 0 {
				t.Fatalf("failed upload switched %d pointers and ran %d cleanups", pointers, prunes)
			}
		})
	}
}

func TestPublishRejectsMissingV6ScoreImageBeforeSwitchingPointers(t *testing.T) {
	directory := publicationFixture(t, "20261016T0000Z")
	imagePath := filepath.Join(directory, "scores-all.png")
	if err := os.Rename(imagePath, imagePath+".missing"); err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var pointers, v6Uploads, prunes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "current.json") {
			pointers++
		}
		if strings.HasPrefix(r.URL.Path, "/__publish/v6/") {
			v6Uploads++
		}
		if r.Method == http.MethodPost {
			prunes++
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	_, err := Publish(context.Background(), directory, server.URL, "test-only-token")
	if err == nil || !strings.Contains(err.Error(), "scores-all.png") {
		t.Fatalf("expected missing score image error, got %v", err)
	}
	if pointers != 0 || v6Uploads != 0 || prunes != 0 {
		t.Fatalf("incomplete run switched %d pointers, uploaded %d v6 files and ran %d cleanups", pointers, v6Uploads, prunes)
	}
}

func publicationFixture(t *testing.T, runID string) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "v6", "forecast", runID)
	v6Directory := directory
	for _, path := range []string{directory} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		filepath.Join(v6Directory, "days.bin"):                   make([]byte, 14*15),
		filepath.Join(filepath.Dir(v6Directory), "current.json"): []byte(`{"run":"` + runID + `","firstDate":"2026-10-16","days":14,"previousRun":"local-state-must-not-win"}`),
	}
	for _, name := range v6ScoreImageNames() {
		files[filepath.Join(v6Directory, name)] = []byte("score image")
	}
	for _, name := range outlook.FileNames() {
		files[filepath.Join(v6Directory, name)] = []byte("outlook file")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(v6Directory, name)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range files {
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
