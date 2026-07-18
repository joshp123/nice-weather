package appserver

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

type testApplication struct {
	handler  http.Handler
	root     string
	web      string
	manifest delivery.Manifest
}

func newTestApplication(t *testing.T) testApplication {
	t.Helper()
	root, web := filepath.Join(t.TempDir(), "artifacts"), filepath.Join(t.TempDir(), "web")
	if err := os.MkdirAll(filepath.Join(web, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>Nice Weather Finder</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "assets", "app-abc.js"), []byte("console.log('weather')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "assets", "app-abc.js.br"), []byte("precompressed"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(web, "assets", "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := GenerateFixture(root); err != nil {
		t.Fatal(err)
	}
	current, ok := delivery.CurrentDir(root)
	if !ok {
		t.Fatal("fixture was not published")
	}
	resolvedCurrent, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := delivery.ValidateProductionRelease(resolvedCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Forecast.Days) != 14 || manifest.Climate == nil || len(manifest.Climate.Months) != 12 {
		t.Fatalf("fixture is incomplete: %d days, %d months", len(manifest.Forecast.Days), len(manifest.Climate.Months))
	}
	if manifest.DataStatus != delivery.DataStatusSample {
		t.Fatalf("fixture data status = %q; want sample", manifest.DataStatus)
	}
	if manifest.ProductContract != delivery.ProductContract {
		t.Fatalf("fixture product contract = %q; want %q", manifest.ProductContract, delivery.ProductContract)
	}
	if !strings.Contains(manifest.Region.Label, "sample data") || !strings.Contains(manifest.Forecast.Days[0].Source.ProviderLabel, "Sample") {
		t.Fatal("fixture metadata does not identify sample data")
	}
	handler, err := NewHandler(Config{WebRoot: web, ArtifactRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if concrete, ok := handler.(*application); ok {
		t.Cleanup(func() { _ = concrete.close() })
	}
	return testApplication{handler: handler, root: root, web: web, manifest: manifest}
}

func TestRunningServerReloadsPublishedReleaseCoherently(t *testing.T) {
	app := newTestApplication(t)
	day := app.manifest.Forecast.Days[0]
	oldPoint := requestPointScore(t, app.handler, app.manifest.ReleaseID, day.ID)
	oldImageURL := releaseImageURL(app.manifest.ReleaseID, day.Images[delivery.ProfileNice].URL)
	oldImage := requestBody(t, app.handler, oldImageURL)

	if err := generateFixture(app.root, "fixture-reload-b", -12); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan string, 16)
	start := make(chan struct{})
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			response := httptest.NewRecorder()
			app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID+"&period="+day.ID+"&profile=nice&lat=52.3676&lon=4.9041", nil))
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "stale_release") {
				errorsFound <- response.Body.String()
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for message := range errorsFound {
		t.Errorf("concurrent reload request failed: %s", message)
	}

	manifestResponse := requestBody(t, app.handler, "/manifest.json")
	newManifest, err := delivery.ParseManifest(manifestResponse)
	if err != nil {
		t.Fatal(err)
	}
	newDay := newManifest.Forecast.Days[0]
	if newDay.GridSHA256 == day.GridSHA256 || newDay.Images[delivery.ProfileNice].SHA256 == day.Images[delivery.ProfileNice].SHA256 {
		t.Fatal("reloaded manifest still describes the old grid or image")
	}
	stalePoint := httptest.NewRecorder()
	app.handler.ServeHTTP(stalePoint, httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID+"&period="+day.ID+"&profile=nice&lat=52.3676&lon=4.9041", nil))
	if stalePoint.Code != http.StatusConflict || !strings.Contains(stalePoint.Body.String(), `"code":"stale_release"`) {
		t.Fatalf("stale release point = %d %s", stalePoint.Code, stalePoint.Body.String())
	}
	newPoint := requestPointScore(t, app.handler, newManifest.ReleaseID, newDay.ID)
	if newPoint == oldPoint {
		t.Fatalf("point score did not change after publication: %d", newPoint)
	}
	oldImageAfterSwap := httptest.NewRecorder()
	app.handler.ServeHTTP(oldImageAfterSwap, httptest.NewRequest(http.MethodGet, oldImageURL, nil))
	if oldImageAfterSwap.Code != http.StatusOK || oldImageAfterSwap.Body.String() != string(oldImage) {
		t.Fatalf("previous release image changed after publication: %d", oldImageAfterSwap.Code)
	}
	newImage := requestBody(t, app.handler, releaseImageURL(newManifest.ReleaseID, newDay.Images[delivery.ProfileNice].URL))
	if string(newImage) == string(oldImage) {
		t.Fatal("image did not change after publication")
	}
	digest := sha256.Sum256(newImage)
	if got := hex.EncodeToString(digest[:]); got != newDay.Images[delivery.ProfileNice].SHA256 {
		t.Fatalf("served image checksum %s; manifest says %s", got, newDay.Images[delivery.ProfileNice].SHA256)
	}

	legacy := httptest.NewRecorder()
	app.handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/"+newDay.Images[delivery.ProfileNice].URL, nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("mutable legacy image URL returned %d", legacy.Code)
	}
	staleAsset := httptest.NewRecorder()
	app.handler.ServeHTTP(staleAsset, httptest.NewRequest(http.MethodHead, "/releases/fixture-no-longer-kept/images/forecast/missing.png", nil))
	if staleAsset.Code != http.StatusConflict {
		t.Fatalf("stale release asset = %d %s", staleAsset.Code, staleAsset.Body.String())
	}
	restarted, err := NewHandler(Config{WebRoot: app.web, ArtifactRoot: app.root})
	if err != nil {
		t.Fatal(err)
	}
	if concrete, ok := restarted.(*application); ok {
		defer concrete.close()
	}
	if got := requestBody(t, restarted, oldImageURL); string(got) != string(oldImage) {
		t.Fatal("restarted server did not retain the previous release image")
	}

	currentB, ok := delivery.CurrentDir(app.root)
	if !ok {
		t.Fatal("release B was not published")
	}
	currentB, err = filepath.EvalSymlinks(currentB)
	if err != nil {
		t.Fatal(err)
	}
	manifestC, currentC := clonePublishedRelease(t, app.root, currentB, "fixture-reload-c")
	dayC := manifestC.Forecast.Days[0]
	imageC, err := os.ReadFile(filepath.Join(currentC, filepath.FromSlash(dayC.Images[delivery.ProfileNice].URL)))
	if err != nil {
		t.Fatal(err)
	}
	clonePublishedRelease(t, app.root, currentC, "fixture-reload-d")
	manifestD, err := delivery.ParseManifest(requestBody(t, app.handler, "/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifestD.ReleaseID != "fixture-reload-d" {
		t.Fatalf("server reloaded %q; want fixture-reload-d", manifestD.ReleaseID)
	}
	if got := requestBody(t, app.handler, releaseImageURL(manifestC.ReleaseID, dayC.Images[delivery.ProfileNice].URL)); string(got) != string(imageC) {
		t.Fatal("server skipped a publication but did not serve the disk-published previous release")
	}
	evictedB := httptest.NewRecorder()
	app.handler.ServeHTTP(evictedB, httptest.NewRequest(http.MethodHead, releaseImageURL(newManifest.ReleaseID, newDay.Images[delivery.ProfileNice].URL), nil))
	if evictedB.Code != http.StatusConflict {
		t.Fatalf("evicted release B returned %d", evictedB.Code)
	}
	restartedAfterSkip, err := NewHandler(Config{WebRoot: app.web, ArtifactRoot: app.root})
	if err != nil {
		t.Fatal(err)
	}
	if concrete, ok := restartedAfterSkip.(*application); ok {
		defer concrete.close()
	}
	if got := requestBody(t, restartedAfterSkip, releaseImageURL(manifestC.ReleaseID, dayC.Images[delivery.ProfileNice].URL)); string(got) != string(imageC) {
		t.Fatal("running and restarted servers disagree about the retained previous release")
	}
}

func clonePublishedRelease(t *testing.T, root, sourceRoot, releaseID string) (delivery.Manifest, string) {
	t.Helper()
	manifest, err := delivery.ValidateProductionRelease(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot, err := delivery.BeginRelease(root, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil || relative == "." || relative == "manifest.json" {
			return err
		}
		target := filepath.Join(targetRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return os.Link(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest.ReleaseID = releaseID
	if err := delivery.WriteManifest(targetRoot, manifest); err != nil {
		t.Fatal(err)
	}
	if err := delivery.PublishProductionRelease(root, targetRoot); err != nil {
		t.Fatal(err)
	}
	if err := delivery.CleanupStaleReleases(root); err != nil {
		t.Fatal(err)
	}
	return manifest, targetRoot
}

func releaseImageURL(releaseID, imagePath string) string {
	return "/releases/" + releaseID + "/" + strings.TrimPrefix(imagePath, "/")
}

func requestPointScore(t *testing.T, handler http.Handler, releaseID, periodID string) int {
	t.Helper()
	body := requestBody(t, handler, "/api/point?release="+releaseID+"&period="+periodID+"&profile=nice&lat=52.3676&lon=4.9041")
	var response struct {
		Score struct {
			Value int `json:"value"`
		} `json:"score"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response.Score.Value
}

func requestBody(t *testing.T, handler http.Handler, path string) []byte {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func TestPointUsesCanonicalGridAndScore(t *testing.T) {
	app := newTestApplication(t)
	day := app.manifest.Forecast.Days[0]
	request := httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID+"&period="+day.ID+"&profile=nice&lat=52.3676&lon=4.9041", nil)
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("point returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Summary string `json:"summary"`
		Score   struct {
			Value      int             `json:"value"`
			Category   string          `json:"category"`
			Components pointComponents `json:"components"`
		} `json:"score"`
		Weather struct {
			WetTime *float64 `json:"wetTimeFraction"`
		} `json:"weather"`
		Source pointSource `json:"source"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	current, _ := delivery.CurrentDir(app.root)
	grid, err := delivery.OpenGrid(filepath.Join(current, filepath.FromSlash(day.GridURL)))
	if err != nil {
		t.Fatal(err)
	}
	defer grid.Close()
	record, valid, err := grid.RecordAt(4.9041, 52.3676)
	if err != nil || !valid {
		t.Fatalf("grid point: valid=%v err=%v", valid, err)
	}
	wantMetrics, err := record.WeatherMetrics(day.NativeResolutionKM)
	if err != nil {
		t.Fatal(err)
	}
	if want := weather.Score(wantMetrics, weather.ProfileNice); payload.Score.Value != want.Score || payload.Score.Category != want.Rating ||
		payload.Score.Components != (pointComponents{Comfort: componentResponse(want.Components.Thermal), Dryness: componentResponse(want.Components.Dryness), Sun: componentResponse(want.Components.Sun), Wind: componentResponse(want.Components.Wind)}) {
		t.Fatalf("API score %+v; want %+v", payload.Score, want)
	}
	if want := weather.Summary(wantMetrics); payload.Summary != want {
		t.Fatalf("API summary %q; want %q", payload.Summary, want)
	}
	if payload.Weather.WetTime == nil {
		t.Fatal("forecast point omitted available wet-time evidence")
	}
	if payload.Source.Basis != "forecast" || payload.Source.DetailLabel != "Detailed forecast" || payload.Source.ProviderLabel == "" || payload.Source.RunAt == "" || payload.Source.ValidFrom == "" || payload.Source.ValidTo == "" || payload.Source.ArtifactGeneratedAt != app.manifest.GeneratedAt {
		t.Fatalf("forecast source is incomplete: %+v", payload.Source)
	}
	if payload.Source.ReferencePeriod != "" || strings.Contains(response.Body.String(), `"updatedAt"`) || strings.Contains(response.Body.String(), `"referencePeriod"`) {
		t.Fatalf("forecast source contains climate or overloaded freshness fields: %s", response.Body.String())
	}

	climateID := app.manifest.Climate.Months[0].ID
	climateRequest := httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID+"&period="+climateID+"&profile=nice&lat=52.3676&lon=4.9041", nil)
	climateResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(climateResponse, climateRequest)
	if climateResponse.Code != http.StatusOK {
		t.Fatalf("climate point returned %d: %s", climateResponse.Code, climateResponse.Body.String())
	}
	var climatePayload struct {
		Weather struct {
			WetTime *float64 `json:"wetTimeFraction"`
		} `json:"weather"`
		Source pointSource `json:"source"`
	}
	if err := json.Unmarshal(climateResponse.Body.Bytes(), &climatePayload); err != nil {
		t.Fatal(err)
	}
	if climatePayload.Weather.WetTime != nil || strings.Contains(climateResponse.Body.String(), `"wetTimeFraction"`) {
		t.Fatalf("climate point invented wet-time evidence: %s", climateResponse.Body.String())
	}
	if climatePayload.Source.Basis != "climate" || climatePayload.Source.DetailLabel != "Typical weather" || climatePayload.Source.ReferencePeriod != app.manifest.Climate.ReferencePeriod || climatePayload.Source.ArtifactGeneratedAt != app.manifest.GeneratedAt {
		t.Fatalf("climate source is incomplete: %+v", climatePayload.Source)
	}
	if climatePayload.Source.RunAt != "" || climatePayload.Source.ValidFrom != "" || climatePayload.Source.ValidTo != "" || strings.Contains(climateResponse.Body.String(), `"updatedAt"`) || strings.Contains(climateResponse.Body.String(), `"runAt"`) || strings.Contains(climateResponse.Body.String(), `"validFrom"`) || strings.Contains(climateResponse.Body.String(), `"validTo"`) {
		t.Fatalf("climate source was presented as a fresh forecast: %s", climateResponse.Body.String())
	}

	missing := httptest.NewRecorder()
	app.handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID+"&period=nope&profile=nice&lat=52&lon=5", nil))
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "unknown_period") {
		t.Fatalf("unexpected structured error: %d %s", missing.Code, missing.Body.String())
	}
}

func TestCompressionCachingSearchAndSPA(t *testing.T) {
	app := newTestApplication(t)

	manifestRequest := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	manifestRequest.Header.Set("Accept-Encoding", "gzip")
	manifest := httptest.NewRecorder()
	app.handler.ServeHTTP(manifest, manifestRequest)
	if manifest.Code != http.StatusOK || manifest.Header().Get("Content-Encoding") != "gzip" || manifest.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("manifest headers: %d %+v", manifest.Code, manifest.Header())
	}
	compressed, err := gzip.NewReader(manifest.Body)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(compressed)
	if err != nil || !strings.Contains(string(data), fmt.Sprintf(`"version": %d`, delivery.ManifestVersion)) || !strings.Contains(string(data), fmt.Sprintf(`"productContract": %q`, delivery.ProductContract)) || !strings.Contains(string(data), `"dataStatus": "sample"`) {
		t.Fatalf("compressed manifest: %v %q", err, data)
	}

	assetRequest := httptest.NewRequest(http.MethodGet, "/assets/app-abc.js", nil)
	assetRequest.Header.Set("Accept-Encoding", "gzip")
	asset := httptest.NewRecorder()
	app.handler.ServeHTTP(asset, assetRequest)
	if asset.Header().Get("Content-Encoding") != "gzip" || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset headers: %+v", asset.Header())
	}
	brotliRequest := httptest.NewRequest(http.MethodGet, "/assets/app-abc.js", nil)
	brotliRequest.Header.Set("Accept-Encoding", "br, gzip")
	brotli := httptest.NewRecorder()
	app.handler.ServeHTTP(brotli, brotliRequest)
	if brotli.Header().Get("Content-Encoding") != "br" || brotli.Body.String() != "precompressed" {
		t.Fatalf("Brotli asset: %q %+v", brotli.Body.String(), brotli.Header())
	}
	escape := httptest.NewRecorder()
	app.handler.ServeHTTP(escape, httptest.NewRequest(http.MethodGet, "/assets/escape.txt", nil))
	if escape.Code != http.StatusBadRequest || strings.Contains(escape.Body.String(), "secret") {
		t.Fatalf("symlink escape response: %d %q", escape.Code, escape.Body.String())
	}

	image := httptest.NewRecorder()
	app.handler.ServeHTTP(image, httptest.NewRequest(http.MethodGet, releaseImageURL(app.manifest.ReleaseID, app.manifest.Forecast.Days[0].Images["nice"].URL), nil))
	if image.Code != http.StatusOK || image.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || image.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image response: %d %+v", image.Code, image.Header())
	}
	unsafeImage := httptest.NewRecorder()
	app.handler.ServeHTTP(unsafeImage, httptest.NewRequest(http.MethodGet, "/releases/../images/forecast/nope.png", nil))
	if unsafeImage.Code != http.StatusBadRequest {
		t.Fatalf("unsafe release path returned %d", unsafeImage.Code)
	}

	search := httptest.NewRecorder()
	app.handler.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/search?q=ams", nil))
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "Amsterdam") || search.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("search response: %d %s %+v", search.Code, search.Body.String(), search.Header())
	}

	spa := httptest.NewRecorder()
	app.handler.ServeHTTP(spa, httptest.NewRequest(http.MethodGet, "/some/map/route", nil))
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "Nice Weather Finder") || spa.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA response: %d %s %+v", spa.Code, spa.Body.String(), spa.Header())
	}
}
