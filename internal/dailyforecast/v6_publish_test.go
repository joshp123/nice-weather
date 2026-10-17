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
)

func TestPublishKeepsBothForecastsAndSwitchesPointersAfterData(t *testing.T) {
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
				case r.Method == http.MethodGet && r.URL.Path == "/data/forecast/current.json":
					json.NewEncoder(w).Encode(map[string]string{"runId": scenario.previousRun, "previousRunId": scenario.previousPrevious})
				case r.Method == http.MethodGet && r.URL.Path == "/data/v6/forecast/current.json":
					json.NewEncoder(w).Encode(v6Pointer{Run: scenario.previousRun, PreviousRun: scenario.previousPrevious})
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/__publish/"):
					if r.Header.Get("Authorization") != "Bearer test-only-token" {
						t.Error("missing publisher authorization")
					}
					uploaded[r.URL.Path] = body
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodPost && r.URL.Path == "/__prune":
					if uploaded["/__publish/forecast/current.json"] == nil || uploaded["/__publish/v6/forecast/current.json"] == nil {
						t.Error("prune ran before both pointers were published")
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
			if report.Files != 5 {
				t.Fatalf("uploaded files=%d, want five: legacy manifest, legacy day, v6 day, two pointers", report.Files)
			}
			if string(uploaded["/__publish/forecast/"+runID+"/day.bin"]) != "legacy forecast" {
				t.Fatal("legacy day was not preserved")
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
			if requests[len(requests)-1] != "POST /__prune" {
				t.Fatal("cleanup was not last")
			}
		})
	}
}

func TestPublishDoesNotSwitchForecastPointersWhenV6DataUploadFails(t *testing.T) {
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
		if strings.HasSuffix(r.URL.Path, "days.bin") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	_, err := Publish(context.Background(), directory, server.URL, "test-only-token")
	if err == nil || !strings.Contains(err.Error(), "v6/forecast/20261016T0000Z/days.bin") {
		t.Fatalf("expected v6 data upload error, got %v", err)
	}
	if pointers != 0 || prunes != 0 {
		t.Fatalf("failed upload switched %d pointers and ran %d cleanups", pointers, prunes)
	}
}

func publicationFixture(t *testing.T, runID string) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "forecast", runID)
	v6Directory := filepath.Join(root, "v6", "forecast", runID)
	for _, path := range []string{directory, v6Directory} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		filepath.Join(directory, "manifest.json"):                []byte(`{"runId":"` + runID + `"}`),
		filepath.Join(directory, "day.bin"):                      []byte("legacy forecast"),
		filepath.Join(v6Directory, "days.bin"):                   make([]byte, 14*15),
		filepath.Join(filepath.Dir(v6Directory), "current.json"): []byte(`{"run":"` + runID + `","firstDate":"2026-10-16","days":14,"previousRun":"local-state-must-not-win"}`),
	}
	for path, body := range files {
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
