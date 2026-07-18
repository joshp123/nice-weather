package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
)

func TestLandSourceIdentityIsPinned(t *testing.T) {
	if LandSourceURL != "https://naciscdn.org/naturalearth/5.1.1/10m/physical/ne_10m_land.zip" || LandSourceSize != 3269070 || LandSourceSHA256 != "e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc" || LandSourceLicense != "public domain" {
		t.Fatal("Natural Earth source identity drifted")
	}
	if decoded, err := hex.DecodeString(LandSourceSHA256); err != nil || len(decoded) != sha256.Size {
		t.Fatalf("source SHA256 is invalid: %v", err)
	}
}

func TestExtractLandArchiveRejectsCorruptionTraversalAndUnexpectedMembers(t *testing.T) {
	tests := []struct {
		name    string
		archive []byte
	}{
		{name: "corrupt", archive: []byte("not a zip")},
		{name: "truncated", archive: testLandZip(t)[:24]},
		{name: "traversal", archive: testZip(t, append(testLandEntries(), zipEntry{name: "../escape", body: "bad"})...)},
		{name: "unexpected", archive: testZip(t, append(testLandEntries(), zipEntry{name: "extra.txt", body: "bad"})...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "source.zip")
			if err := os.WriteFile(archivePath, test.archive, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := extractLandArchive(archivePath, filepath.Join(t.TempDir(), "out")); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestLandCacheInstallIsTransactionalAndSelfValidating(t *testing.T) {
	valid := testLandZip(t)
	digest := sha256.Sum256(valid)
	var mu sync.Mutex
	body := valid[:len(valid)/2]
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	cacheRoot := t.TempDir()
	source := landSource{URL: server.URL, Size: int64(len(valid)), SHA256: hex.EncodeToString(digest[:])}
	authority := newLandAuthority(cacheRoot, server.Client(), &landFakeRunner{}, source)
	if _, err := authority.Prepare(context.Background()); err == nil {
		t.Fatal("truncated archive was accepted")
	}
	cacheDir := filepath.Join(cacheRoot, "land", landCacheVersion)
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("failed install became visible: %v", err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "partial"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	body = valid
	mu.Unlock()
	shapePath, err := authority.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if shapePath != filepath.Join(cacheDir, landDatasetName+".shp") {
		t.Fatalf("shape path = %q", shapePath)
	}
	if err := validateLandCache(cacheDir, source.SHA256); err != nil {
		t.Fatalf("committed cache is invalid: %v", err)
	}
	installDirs, err := filepath.Glob(filepath.Join(filepath.Dir(cacheDir), ".land-install-*"))
	if err != nil || len(installDirs) != 0 {
		t.Fatalf("temporary install directories leaked: %v, %v", installDirs, err)
	}
	if err := os.WriteFile(shapePath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded := newLandAuthority(cacheRoot, server.Client(), &landFakeRunner{}, source)
	if _, err := reloaded.Prepare(context.Background()); err != nil {
		t.Fatalf("corrupt cache was not rebuilt: %v", err)
	}
	if err := validateLandCache(cacheDir, source.SHA256); err != nil {
		t.Fatalf("rebuilt cache is invalid: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "unmanifested"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLandCache(cacheDir, source.SHA256); err == nil {
		t.Fatal("cache with an unmanifested file was accepted")
	}
}

func TestLandDownloadAttemptDeadlineCancelsStalledBody(t *testing.T) {
	started := make(chan struct{}, retryAttempts)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "128")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("x"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		started <- struct{}{}
		<-request.Context().Done()
	}))
	defer server.Close()
	digest := sha256.Sum256(make([]byte, 128))
	authority := newLandAuthority(t.TempDir(), server.Client(), &landFakeRunner{}, landSource{
		URL: server.URL, Size: 128, SHA256: hex.EncodeToString(digest[:]), AttemptTimeout: 20 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := authority.Prepare(ctx); err == nil {
		t.Fatal("stalled response was accepted")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("stalled response was not bounded: %s", elapsed)
	}
	if len(started) == 0 {
		t.Fatal("server did not receive the download attempt")
	}
}

func TestLandDownloadHonoursCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "128")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("x"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	digest := sha256.Sum256(make([]byte, 128))
	authority := newLandAuthority(t.TempDir(), server.Client(), &landFakeRunner{}, landSource{
		URL: server.URL, Size: 128, SHA256: hex.EncodeToString(digest[:]), AttemptTimeout: time.Minute,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := authority.Prepare(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled download returned no error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelled download did not return promptly")
	}
}

func TestLandMasksUseExactGridDistinctSemanticsAndMemoize(t *testing.T) {
	archive := testLandZip(t)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	runner := &landFakeRunner{payload: []byte{0, 1, 1, 0, 0, 1}}
	authority := newLandAuthority(t.TempDir(), server.Client(), runner, landSource{URL: server.URL, Size: int64(len(archive)), SHA256: hex.EncodeToString(digest[:])})
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}, Rows: 2, Cols: 3}
	first, err := authority.Mask(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.Spec != spec || len(first.Values) != 6 || first.Values[0] != 0 || first.Values[1] != 1 || first.Values[5] != 1 {
		t.Fatalf("unexpected mask: %+v", first)
	}
	first.Values[1] = 0
	second, err := authority.Mask(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if second.Values[1] != 1 || runner.calls != 1 {
		t.Fatalf("mask cache leaked mutation or reran: values=%v calls=%d", second.Values, runner.calls)
	}
	centre, err := authority.CenterMask(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if centre.Spec != spec || runner.calls != 2 {
		t.Fatalf("centre mask did not use its own memoized topology: spec=%+v calls=%d", centre.Spec, runner.calls)
	}
	allTouchedArgs := strings.Join(runner.invocations[0], " ")
	for _, required := range []string{"-at", "-te -24 29 45 71.5", "-ts 3 2", "-l ne_10m_land"} {
		if !strings.Contains(allTouchedArgs, required) {
			t.Errorf("all-touched gdal_rasterize args %q missing %q", allTouchedArgs, required)
		}
	}
	if !strings.Contains(allTouchedArgs, "-init 0 -at") {
		t.Errorf("all-touched arguments split -init value: %q", allTouchedArgs)
	}
	centreArgs := strings.Join(runner.invocations[1], " ")
	if strings.Contains(centreArgs, "-at") || !strings.Contains(centreArgs, "-init 0 -te") {
		t.Errorf("centre arguments have wrong rasterization semantics: %q", centreArgs)
	}
}

func TestLandLiveRasterSmoke(t *testing.T) {
	if os.Getenv("NICE_WEATHER_LIVE_LAND") != "1" {
		t.Skip("set NICE_WEATHER_LIVE_LAND=1 for the pinned-source GDAL smoke")
	}
	spec := delivery.GridSpec{
		Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5},
		Rows:   680,
		Cols:   1104,
	}
	grid, err := NewLandAuthority(t.TempDir(), http.DefaultClient, nil).Mask(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if grid.Spec != spec || len(grid.Values) != 680*1104 {
		t.Fatalf("live mask grid mismatch: spec=%+v cells=%d", grid.Spec, len(grid.Values))
	}
	cell := func(lon, lat float64) float32 {
		col := int((lon - spec.Bounds.West) / (spec.Bounds.East - spec.Bounds.West) * float64(spec.Cols))
		row := int((spec.Bounds.North - lat) / (spec.Bounds.North - spec.Bounds.South) * float64(spec.Rows))
		return grid.Values[row*int(spec.Cols)+col]
	}
	for name, point := range map[string]struct {
		lon, lat float64
		want     float32
	}{
		"Amsterdam": {lon: 4.9041, lat: 52.3676, want: 1},
		"Malaga":    {lon: -4.4214, lat: 36.7213, want: 1},
		"Reykjavik": {lon: -21.9426, lat: 64.1466, want: 1},
		"Madeira":   {lon: -16.9241, lat: 32.6669, want: 1},
		"Atlantic":  {lon: -15, lat: 45, want: 0},
	} {
		if got := cell(point.lon, point.lat); got != point.want {
			t.Errorf("%s cell = %.0f; want %.0f", name, got, point.want)
		}
	}
}

type landFakeRunner struct {
	payload     []byte
	args        []string
	invocations [][]string
	calls       int
}

func (runner *landFakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	if command != "gdal_rasterize" {
		return nil, errors.New("unexpected command " + command)
	}
	runner.calls++
	runner.args = append([]string(nil), args...)
	runner.invocations = append(runner.invocations, append([]string(nil), args...))
	if err := os.WriteFile(args[len(args)-1], runner.payload, 0o600); err != nil {
		return nil, err
	}
	return nil, nil
}

type zipEntry struct{ name, body string }

func testLandEntries() []zipEntry {
	entries := make([]zipEntry, 0, len(landMembers))
	for _, name := range landMembers {
		entries = append(entries, zipEntry{name: name, body: "fixture for " + name})
	}
	return entries
}

func testLandZip(t *testing.T) []byte { return testZip(t, testLandEntries()...) }

func testZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		member, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(member, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
