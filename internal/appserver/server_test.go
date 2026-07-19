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
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/weather"
)

type testApplication struct {
	handler  http.Handler
	root     string
	web      string
	manifest delivery.Manifest
}

func newTestApplication(t testing.TB) testApplication {
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

func TestForecastWindowPointReturnsCanonicalRangeWithoutFakeDailyWeather(t *testing.T) {
	app := newTestApplication(t)
	window := app.manifest.Forecast.Window
	body := requestBody(t, app.handler, "/api/point?release="+app.manifest.ReleaseID+"&period="+window.ID+"&profile=nice&lat=52.3676&lon=4.9041")
	var payload struct {
		PeriodID string                    `json:"periodId"`
		Score    pointScore                `json:"score"`
		Window   pointForecastWindow       `json:"window"`
		Days     []pointForecastDay        `json:"days"`
		Source   pointForecastWindowSource `json:"source"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	current, _ := delivery.CurrentDir(app.root)
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := delivery.OpenProductionReleaseCatalog(resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	record, valid, err := catalog.LookupRange(delivery.ProfileNice, 4.9041, 52.3676)
	if err != nil || !valid {
		t.Fatalf("range lookup valid=%t err=%v", valid, err)
	}
	want, err := record.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if payload.PeriodID != window.ID || payload.Score.Value != want.Score || payload.Score.Category != want.Rating ||
		payload.Score.Components != (pointComponents{Comfort: componentResponse(want.Components.Thermal), Dryness: componentResponse(want.Components.Dryness), Sun: componentResponse(want.Components.Sun), Wind: componentResponse(want.Components.Wind)}) {
		t.Fatalf("range score = %+v; want %+v", payload.Score, want)
	}
	if payload.Window != (pointForecastWindow{
		StartDate: window.StartDate, EndDate: window.EndDate, DayCount: want.Days, NiceDays: want.NiceDays,
		AwfulDays: want.AwfulDays, MedianScore: want.MedianScore, LongestPoorRun: want.LongestPoorRun,
	}) {
		t.Fatalf("range evidence = %+v; want %+v", payload.Window, want)
	}
	if len(payload.Days) != len(app.manifest.Forecast.Days) {
		t.Fatalf("forecast fingerprint has %d days; want %d", len(payload.Days), len(app.manifest.Forecast.Days))
	}
	for index, day := range app.manifest.Forecast.Days {
		dailyRecord, dailyValid, lookupErr := catalog.Lookup(day.ID, 4.9041, 52.3676)
		if lookupErr != nil || !dailyValid {
			t.Fatalf("day %d lookup valid=%t err=%v", index, dailyValid, lookupErr)
		}
		metrics, metricsErr := dailyRecord.WeatherMetrics(day.NativeResolutionKM)
		if metricsErr != nil {
			t.Fatalf("day %d metrics: %v", index, metricsErr)
		}
		dailyScore := weather.Score(metrics, weather.ProfileNice)
		if payload.Days[index] != (pointForecastDay{Date: day.Date, Score: dailyScore.Score, Category: dailyScore.Rating}) {
			t.Fatalf("forecast fingerprint day %d = %+v; want date=%s score=%d category=%s", index, payload.Days[index], day.Date, dailyScore.Score, dailyScore.Rating)
		}
	}
	if payload.Source.Basis != "forecast-window" || payload.Source.DetailLabel != "14-day forecast" || payload.Source.ResolutionKM != 28 || payload.Source.ArtifactGeneratedAt != app.manifest.GeneratedAt {
		t.Fatalf("range source = %+v", payload.Source)
	}
	if strings.Contains(string(body), `"weather"`) || strings.Contains(string(body), `"temperatureC"`) || strings.Contains(string(body), `"summary"`) {
		t.Fatalf("range response pretends to be one physical day: %s", body)
	}
}

func BenchmarkForecastWindowPoint(b *testing.B) {
	app := newTestApplication(b)
	path := "/api/point?release=" + app.manifest.ReleaseID + "&period=" + app.manifest.Forecast.Window.ID + "&profile=nice&lat=52.3676&lon=4.9041"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			b.Fatalf("point returned %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestCompressionCachingAndSPA(t *testing.T) {
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
	windAsset := app.manifest.Forecast.Days[0].WindFrames[0]
	windURL := "/releases/" + app.manifest.ReleaseID + "/" + windAsset.URL
	windRequest := httptest.NewRequest(http.MethodGet, windURL, nil)
	windRequest.Header.Set("Accept-Encoding", "gzip")
	wind := httptest.NewRecorder()
	app.handler.ServeHTTP(wind, windRequest)
	if wind.Code != http.StatusOK || wind.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || wind.Header().Get("Content-Type") != "application/gzip" || wind.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(wind.Body.String(), "\x1f\x8b") {
		t.Fatalf("wind response: %d %+v body prefix=%x", wind.Code, wind.Header(), wind.Body.Bytes()[:min(2, wind.Body.Len())])
	}
	windHead := httptest.NewRecorder()
	app.handler.ServeHTTP(windHead, httptest.NewRequest(http.MethodHead, windURL, nil))
	if windHead.Code != http.StatusOK || windHead.Body.Len() != 0 || windHead.Header().Get("Content-Length") != fmt.Sprint(wind.Body.Len()) || windHead.Header().Get("Content-Encoding") != "" || windHead.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("wind HEAD: %d %+v body=%q", windHead.Code, windHead.Header(), windHead.Body.String())
	}
	staleWind := httptest.NewRecorder()
	app.handler.ServeHTTP(staleWind, httptest.NewRequest(http.MethodGet, "/releases/old-release/"+windAsset.URL, nil))
	if staleWind.Code != http.StatusConflict {
		t.Fatalf("stale wind release returned %d: %s", staleWind.Code, staleWind.Body.String())
	}
	unsafeImage := httptest.NewRecorder()
	app.handler.ServeHTTP(unsafeImage, httptest.NewRequest(http.MethodGet, "/releases/../images/forecast/nope.png", nil))
	if unsafeImage.Code != http.StatusBadRequest {
		t.Fatalf("unsafe release path returned %d", unsafeImage.Code)
	}

	removedSearch := httptest.NewRecorder()
	app.handler.ServeHTTP(removedSearch, httptest.NewRequest(http.MethodGet, "/api/search?q=ams", nil))
	if removedSearch.Code != http.StatusNotFound {
		t.Fatalf("removed runtime search endpoint returned %d: %s", removedSearch.Code, removedSearch.Body.String())
	}

	spa := httptest.NewRecorder()
	app.handler.ServeHTTP(spa, httptest.NewRequest(http.MethodGet, "/some/map/route", nil))
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "Nice Weather Finder") || spa.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA response: %d %s %+v", spa.Code, spa.Body.String(), spa.Header())
	}
}

func TestBestWeatherArtifactsAreStaticReleaseBoundFiles(t *testing.T) {
	app := newTestApplication(t)
	pointerResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(pointerResponse, httptest.NewRequest(http.MethodGet, "/best-weather/current.json", nil))
	if pointerResponse.Code != http.StatusOK || pointerResponse.Header().Get("Cache-Control") != "no-cache" || pointerResponse.Header().Get("ETag") == "" {
		t.Fatalf("best-weather pointer: %d %+v %s", pointerResponse.Code, pointerResponse.Header(), pointerResponse.Body.String())
	}
	var pointer struct {
		WeatherRelease string `json:"weatherReleaseId"`
		PlaceRelease   string `json:"placeReleaseId"`
		URL            string `json:"url"`
		SHA256         string `json:"sha256"`
	}
	if err := json.Unmarshal(pointerResponse.Body.Bytes(), &pointer); err != nil {
		t.Fatal(err)
	}
	if pointer.WeatherRelease != app.manifest.ReleaseID || pointer.PlaceRelease != places.SampleReleaseID || !strings.HasPrefix(pointer.URL, "/best-weather/releases/") {
		t.Fatalf("best-weather pointer = %+v", pointer)
	}
	artifactResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(artifactResponse, httptest.NewRequest(http.MethodGet, pointer.URL, nil))
	digest := sha256.Sum256(artifactResponse.Body.Bytes())
	if artifactResponse.Code != http.StatusOK || artifactResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || hex.EncodeToString(digest[:]) != pointer.SHA256 || !strings.Contains(artifactResponse.Body.String(), `"weatherReleaseId":"`+app.manifest.ReleaseID+`"`) {
		t.Fatalf("best-weather artifact: %d %+v %s", artifactResponse.Code, artifactResponse.Header(), artifactResponse.Body.String())
	}
	unsafe := httptest.NewRecorder()
	app.handler.ServeHTTP(unsafe, httptest.NewRequest(http.MethodGet, "/best-weather/releases/../current.json", nil))
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("unsafe best-weather path returned %d", unsafe.Code)
	}
}

func TestPlaceIndexFilesUsePointerAndImmutableHTTPContract(t *testing.T) {
	app := newTestApplication(t)
	pointerResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(pointerResponse, httptest.NewRequest(http.MethodGet, "/places/current.json", nil))
	oldETag := pointerResponse.Header().Get("ETag")
	oldModified := pointerResponse.Header().Get("Last-Modified")
	if pointerResponse.Code != http.StatusOK || pointerResponse.Header().Get("Cache-Control") != "no-cache" || pointerResponse.Header().Get("Content-Type") != "application/json" || pointerResponse.Header().Get("Content-Encoding") != "" || oldETag == "" || oldModified != "" {
		t.Fatalf("place pointer response: %d %+v", pointerResponse.Code, pointerResponse.Header())
	}
	var pointer places.Pointer
	if err := json.Unmarshal(pointerResponse.Body.Bytes(), &pointer); err != nil {
		t.Fatal(err)
	}
	if pointer.ReleaseID != places.SampleReleaseID || len(pointer.ManifestSHA256) != 64 {
		t.Fatalf("place pointer = %+v", pointer)
	}

	manifestURL := "/places/releases/" + pointer.ReleaseID + "/manifest.json"
	manifestPath := filepath.Join(app.root, filepath.FromSlash(strings.TrimPrefix(manifestURL, "/")))
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	head := httptest.NewRecorder()
	app.handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, manifestURL, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(manifestBytes)) || head.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || head.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("place manifest HEAD: %d %+v body=%q", head.Code, head.Header(), head.Body.String())
	}
	gzipRequest := httptest.NewRequest(http.MethodGet, manifestURL, nil)
	gzipRequest.Header.Set("Accept-Encoding", "gzip")
	gzipResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(gzipResponse, gzipRequest)
	if gzipResponse.Code != http.StatusOK || gzipResponse.Header().Get("Content-Encoding") != "gzip" || gzipResponse.Header().Get("Vary") != "Accept-Encoding" || gzipResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("compressed place manifest: %d %+v", gzipResponse.Code, gzipResponse.Header())
	}
	gzipReader, err := gzip.NewReader(gzipResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed, err := io.ReadAll(gzipReader)
	if err != nil || string(uncompressed) != string(manifestBytes) {
		t.Fatalf("compressed place manifest body: %v", err)
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, manifestURL, nil)
	rangeRequest.Header.Set("Range", "bytes=0-31")
	rangeRequest.Header.Set("Accept-Encoding", "gzip")
	partial := httptest.NewRecorder()
	app.handler.ServeHTTP(partial, rangeRequest)
	if partial.Code != http.StatusPartialContent || partial.Body.Len() != 32 || partial.Body.String() != string(manifestBytes[:32]) || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-31/%d", len(manifestBytes)) || partial.Header().Get("Content-Encoding") != "" || partial.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("place manifest range: %d %+v %q", partial.Code, partial.Header(), partial.Body.String())
	}

	var metadata struct {
		Shards map[string]string `json:"shards"`
	}
	if err := json.Unmarshal(manifestBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	shardURL := "/places/releases/" + pointer.ReleaseID + "/shards/mal.json"
	shard := httptest.NewRecorder()
	app.handler.ServeHTTP(shard, httptest.NewRequest(http.MethodGet, shardURL, nil))
	if shard.Code != http.StatusOK || shard.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || shard.Header().Get("Content-Type") != "application/json" || !strings.Contains(shard.Body.String(), "Málaga") || metadata.Shards["mal"] == "" {
		t.Fatalf("sample place shard: %d %+v %s", shard.Code, shard.Header(), shard.Body.String())
	}

	sampleRoot := filepath.Join(app.root, "places", "releases", pointer.ReleaseID)
	second, err := places.Publish(app.root, sampleRoot, "fixture-places-second")
	if err != nil {
		t.Fatal(err)
	}
	oldModified = time.Now().UTC().Format(http.TimeFormat)
	conditional := httptest.NewRequest(http.MethodGet, "/places/current.json", nil)
	conditional.Header.Set("If-None-Match", oldETag)
	conditional.Header.Set("If-Modified-Since", oldModified)
	rotated := httptest.NewRecorder()
	app.handler.ServeHTTP(rotated, conditional)
	if rotated.Code != http.StatusOK || !strings.Contains(rotated.Body.String(), second.ReleaseID) || rotated.Header().Get("ETag") == oldETag {
		t.Fatalf("running server did not expose rotated pointer: %s", rotated.Body.String())
	}
	modifiedOnly := httptest.NewRequest(http.MethodGet, "/places/current.json", nil)
	modifiedOnly.Header.Set("If-Modified-Since", oldModified)
	modifiedResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(modifiedResponse, modifiedOnly)
	if modifiedResponse.Code != http.StatusOK || !strings.Contains(modifiedResponse.Body.String(), second.ReleaseID) {
		t.Fatalf("place pointer incorrectly used timestamp validator: %d %s", modifiedResponse.Code, modifiedResponse.Body.String())
	}
	old := httptest.NewRecorder()
	app.handler.ServeHTTP(old, httptest.NewRequest(http.MethodGet, manifestURL, nil))
	if old.Code != http.StatusOK || old.Body.String() != string(manifestBytes) {
		t.Fatal("old immutable place release changed after pointer rotation")
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"secret":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	secondShard := filepath.Join(app.root, "places", "releases", second.ReleaseID, "shards", "mal.json")
	if err := os.Remove(secondShard); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, secondShard); err != nil {
		t.Fatal(err)
	}
	escape := httptest.NewRecorder()
	app.handler.ServeHTTP(escape, httptest.NewRequest(http.MethodGet, "/places/releases/"+second.ReleaseID+"/shards/mal.json", nil))
	if escape.Code != http.StatusNotFound || strings.Contains(escape.Body.String(), "secret") {
		t.Fatalf("artifact-root symlink escape response: %d %s", escape.Code, escape.Body.String())
	}

	for _, unsafe := range []string{
		"/places/releases/../manifest.json",
		"/places/releases/fixture-places-second/shards/../../current.json",
		"/places/releases/fixture-places-second/shards/MAL.json",
		"/places/anything-else.json",
	} {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, unsafe, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("unsafe place path %q returned %d", unsafe, response.Code)
		}
	}
	post := httptest.NewRecorder()
	app.handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/places/current.json", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("place POST returned %d", post.Code)
	}
}
