package appserver

import (
	"compress/gzip"
	"context"
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
	"testing"
	"time"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/climatetiles"
	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastpoints"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
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
	oldImageURL := releaseImageURL(app.manifest.ReleaseID, day.Images[delivery.ProfileNice].URL)
	oldImage := requestBody(t, app.handler, oldImageURL)

	if err := generateFixture(app.root, "fixture-reload-b", -12); err != nil {
		t.Fatal(err)
	}
	manifestResponse := requestBody(t, app.handler, "/manifest.json")
	newManifest, err := delivery.ParseManifest(manifestResponse)
	if err != nil {
		t.Fatal(err)
	}
	newDay := newManifest.Forecast.Days[0]
	if newDay.Point.SHA256 == day.Point.SHA256 || newDay.Images[delivery.ProfileNice].SHA256 == day.Images[delivery.ProfileNice].SHA256 {
		t.Fatal("reloaded manifest still describes the old point asset or image")
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

func requestBody(t *testing.T, handler http.Handler, path string) []byte {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func TestPointAssetsUseStaticByteRangeContract(t *testing.T) {
	app := newTestApplication(t)
	day := app.manifest.Forecast.Days[0]
	pointURL := "/releases/" + app.manifest.ReleaseID + "/" + day.Point.URL
	current, _ := delivery.CurrentDir(app.root)
	releaseRoot, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(releaseRoot, filepath.FromSlash(day.Point.URL)))
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(data)) != day.Point.Bytes {
		t.Fatalf("point bytes = %d; manifest says %d", len(data), day.Point.Bytes)
	}

	request := httptest.NewRequest(http.MethodGet, pointURL, nil)
	request.Header.Set("Range", "bytes=0-127")
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != string(data[:delivery.PointAssetHeaderBytes]) ||
		response.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-127/%d", len(data)) ||
		response.Header().Get("Content-Length") != fmt.Sprint(delivery.PointAssetHeaderBytes) ||
		response.Header().Get("Content-Type") != "application/octet-stream" || response.Header().Get("Content-Encoding") != "" ||
		response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("point header range: %d %+v body=%d", response.Code, response.Header(), response.Body.Len())
	}

	start, end := delivery.PointAssetHeaderBytes, delivery.PointAssetHeaderBytes+delivery.DailyPointRecordBytes-1
	recordRequest := httptest.NewRequest(http.MethodGet, pointURL, nil)
	recordRequest.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	record := httptest.NewRecorder()
	app.handler.ServeHTTP(record, recordRequest)
	if record.Code != http.StatusPartialContent || record.Body.String() != string(data[start:end+1]) ||
		record.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)) {
		t.Fatalf("point record range: %d %+v body=%d", record.Code, record.Header(), record.Body.Len())
	}

	head := httptest.NewRecorder()
	app.handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, pointURL, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(data)) || head.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("point HEAD: %d %+v body=%d", head.Code, head.Header(), head.Body.Len())
	}

	removedAPI := httptest.NewRecorder()
	app.handler.ServeHTTP(removedAPI, httptest.NewRequest(http.MethodGet, "/api/point?release="+app.manifest.ReleaseID, nil))
	if removedAPI.Code != http.StatusNotFound {
		t.Fatalf("removed runtime point API returned %d: %s", removedAPI.Code, removedAPI.Body.String())
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

func TestScalarCanaryTilesUseBinaryMediaTypeAndCompression(t *testing.T) {
	if got := contentTypeForPath("assets/scalar-canary/nice-weather-scalar-canary-v2/5/16/10.nws"); got != tiledelivery.ScalarMediaType {
		t.Fatalf("scalar tile content type = %q; want %q", got, tiledelivery.ScalarMediaType)
	}
	if !isCompressible(tiledelivery.ScalarMediaType) {
		t.Fatal("scalar tile media type is not eligible for HTTP compression")
	}
	web := t.TempDir()
	web, err := filepath.EvalSymlinks(web)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(web, "assets", "scalar-canary", "nice-weather-scalar-canary-v2", "tile.nws")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, tiledelivery.ScalarTileBytes)
	for index := range body {
		body[index] = tiledelivery.ScalarNoData
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/assets/scalar-canary/nice-weather-scalar-canary-v2/tile.nws", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	(&application{webRoot: web}).serveFile(response, request, &releaseSnapshot{})
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != tiledelivery.ScalarMediaType || response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("scalar tile response: code=%d headers=%v", response.Code, response.Header())
	}
	compressed, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(body) {
		t.Fatal("HTTP compression changed the decoded scalar tile")
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

func TestClimateArtifactsUseIsolatedStaticHTTPContract(t *testing.T) {
	app := newTestApplication(t)
	published, index, climateCatalog := publishClimateHTTPPreview(t, app.root)
	pointerBody := readClimateArtifact(t, app.root, "climate/current.json")
	indexPath := "climate/indexes/" + published.IndexID + ".json"
	indexBody := readClimateArtifact(t, app.root, indexPath)
	catalogPath := "climate/releases/" + index.Months[0].ReleaseID + "/catalog.json"
	catalogBody := readClimateArtifact(t, app.root, catalogPath)
	scalarPath := "climate/releases/" + index.Months[0].ReleaseID + "/" + climateCatalog.ScalarTiles[0].Path
	scalarBody := readClimateArtifact(t, app.root, scalarPath)
	blockPath := "climate/releases/" + index.Months[0].ReleaseID + "/" + climateCatalog.PointBlocks[0].Path
	blockBody := readClimateArtifact(t, app.root, blockPath)

	pointer := httptest.NewRecorder()
	app.handler.ServeHTTP(pointer, httptest.NewRequest(http.MethodGet, "/climate/current.json", nil))
	etag := pointer.Header().Get("ETag")
	if pointer.Code != http.StatusOK || pointer.Body.String() != string(pointerBody) || pointer.Header().Get("Cache-Control") != "no-cache" || pointer.Header().Get("Content-Type") != "application/json" || etag == "" || pointer.Header().Get("Last-Modified") != "" {
		t.Fatalf("climate pointer: %d %+v %q", pointer.Code, pointer.Header(), pointer.Body.String())
	}
	pointerHead := httptest.NewRecorder()
	app.handler.ServeHTTP(pointerHead, httptest.NewRequest(http.MethodHead, "/climate/current.json", nil))
	if pointerHead.Code != http.StatusOK || pointerHead.Body.Len() != 0 || pointerHead.Header().Get("Content-Length") != fmt.Sprint(len(pointerBody)) || pointerHead.Header().Get("ETag") != etag || pointerHead.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("climate pointer HEAD: %d %+v body=%d", pointerHead.Code, pointerHead.Header(), pointerHead.Body.Len())
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/climate/current.json", nil)
	conditionalRequest.Header.Set("If-None-Match", etag)
	conditionalRequest.Header.Set("Accept-Encoding", "gzip")
	conditional := httptest.NewRecorder()
	app.handler.ServeHTTP(conditional, conditionalRequest)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 || conditional.Header().Get("ETag") != etag || conditional.Header().Get("Content-Encoding") != "" {
		t.Fatalf("conditional climate pointer: %d %+v %q", conditional.Code, conditional.Header(), conditional.Body.String())
	}

	query := httptest.NewRecorder()
	app.handler.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/climate/current.json?release=other&path=../../secret", nil))
	if query.Code != http.StatusOK || query.Body.String() != string(pointerBody) || query.Header().Get("ETag") != etag {
		t.Fatalf("query changed climate pointer representation: %d %+v %q", query.Code, query.Header(), query.Body.String())
	}

	indexResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(indexResponse, httptest.NewRequest(http.MethodGet, "/"+indexPath, nil))
	if indexResponse.Code != http.StatusOK || indexResponse.Body.String() != string(indexBody) || indexResponse.Header().Get("Content-Type") != "application/json" || indexResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate index: %d %+v %q", indexResponse.Code, indexResponse.Header(), indexResponse.Body.String())
	}
	indexHead := httptest.NewRecorder()
	app.handler.ServeHTTP(indexHead, httptest.NewRequest(http.MethodHead, "/"+indexPath, nil))
	if indexHead.Code != http.StatusOK || indexHead.Body.Len() != 0 || indexHead.Header().Get("Content-Length") != fmt.Sprint(len(indexBody)) || indexHead.Header().Get("Content-Type") != "application/json" || indexHead.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate index HEAD: %d %+v body=%d", indexHead.Code, indexHead.Header(), indexHead.Body.Len())
	}

	catalogURL := "/" + catalogPath
	catalog := httptest.NewRecorder()
	app.handler.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, catalogURL, nil))
	if catalog.Code != http.StatusOK || catalog.Body.String() != string(catalogBody) || catalog.Header().Get("Content-Type") != "application/json" || catalog.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate catalog: %d %+v %q", catalog.Code, catalog.Header(), catalog.Body.String())
	}

	scalarURL := "/" + scalarPath
	scalar := httptest.NewRecorder()
	app.handler.ServeHTTP(scalar, httptest.NewRequest(http.MethodGet, scalarURL+"?field=cycling_good", nil))
	if scalar.Code != http.StatusOK || scalar.Body.String() != string(scalarBody) || scalar.Header().Get("Content-Type") != tiledelivery.ScalarMediaType || scalar.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate scalar: %d %+v body=%d", scalar.Code, scalar.Header(), scalar.Body.Len())
	}

	blockURL := "/" + blockPath
	rangeRequest := httptest.NewRequest(http.MethodGet, blockURL, nil)
	rangeRequest.Header.Set("Range", "bytes=160-189")
	rangeRequest.Header.Set("Accept-Encoding", "gzip")
	partial := httptest.NewRecorder()
	app.handler.ServeHTTP(partial, rangeRequest)
	if partial.Code != http.StatusPartialContent || partial.Body.String() != string(blockBody[160:190]) || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 160-189/%d", len(blockBody)) || partial.Header().Get("Content-Length") != "30" || partial.Header().Get("Accept-Ranges") != "bytes" || partial.Header().Get("Content-Type") != tiledelivery.ClimateBlockMediaType || partial.Header().Get("Content-Encoding") != "" || partial.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate block range: %d %+v body=%d", partial.Code, partial.Header(), partial.Body.Len())
	}
	head := httptest.NewRecorder()
	app.handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, blockURL, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(blockBody)) || head.Header().Get("Content-Type") != tiledelivery.ClimateBlockMediaType || head.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate block HEAD: %d %+v body=%d", head.Code, head.Header(), head.Body.Len())
	}

	secondPointer := []byte(`{"version":2,"indexId":"second-index","indexSha256":"` + strings.Repeat("b", 64) + `"}`)
	if err := os.WriteFile(filepath.Join(app.root, "climate", "current.json"), secondPointer, 0o644); err != nil {
		t.Fatal(err)
	}
	rotatedRequest := httptest.NewRequest(http.MethodGet, "/climate/current.json", nil)
	rotatedRequest.Header.Set("If-None-Match", etag)
	rotated := httptest.NewRecorder()
	app.handler.ServeHTTP(rotated, rotatedRequest)
	if rotated.Code != http.StatusOK || rotated.Body.String() != string(secondPointer) || rotated.Header().Get("ETag") == etag {
		t.Fatalf("rotated climate pointer: %d %+v %q", rotated.Code, rotated.Header(), rotated.Body.String())
	}
}

func TestClimateRoutesRejectUnsafePathsSymlinksAndMethods(t *testing.T) {
	app := newTestApplication(t)
	mainManifest := httptest.NewRecorder()
	app.handler.ServeHTTP(mainManifest, httptest.NewRequest(http.MethodGet, "/manifest.json", nil))
	if mainManifest.Code != http.StatusOK {
		t.Fatalf("main release failed without climate publication: %d %s", mainManifest.Code, mainManifest.Body.String())
	}
	for _, missingPath := range []string{"/climate/current.json", "/climate/indexes/missing.json", "/climate/releases/missing/catalog.json"} {
		missing := httptest.NewRecorder()
		app.handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, missingPath, nil))
		if missing.Code != http.StatusNotFound {
			t.Errorf("missing climate path %q returned %d: %s", missingPath, missing.Code, missing.Body.String())
		}
	}

	releaseID := "safe-release"
	blockPath := "climate/releases/" + releaseID + "/points/month-12/112/112.nwc"
	outside := filepath.Join(t.TempDir(), "secret.nwc")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	fullBlockPath := filepath.Join(app.root, filepath.FromSlash(blockPath))
	if err := os.MkdirAll(filepath.Dir(fullBlockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, fullBlockPath); err != nil {
		t.Fatal(err)
	}
	escape := httptest.NewRecorder()
	app.handler.ServeHTTP(escape, httptest.NewRequest(http.MethodGet, "/"+blockPath, nil))
	if escape.Code != http.StatusNotFound || strings.Contains(escape.Body.String(), "secret") {
		t.Fatalf("climate symlink escape response: %d %q", escape.Code, escape.Body.String())
	}
	indexRoot := filepath.Join(app.root, "climate", "indexes")
	if err := os.MkdirAll(indexRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(indexRoot, "safe-index.json")); err != nil {
		t.Fatal(err)
	}
	indexEscape := httptest.NewRecorder()
	app.handler.ServeHTTP(indexEscape, httptest.NewRequest(http.MethodGet, "/climate/indexes/safe-index.json", nil))
	if indexEscape.Code != http.StatusNotFound || strings.Contains(indexEscape.Body.String(), "secret") {
		t.Fatalf("climate index symlink escape response: %d %q", indexEscape.Code, indexEscape.Body.String())
	}
	if err := os.Symlink("../current.json", filepath.Join(indexRoot, "current-alias.json")); err != nil {
		t.Fatal(err)
	}
	inRootAlias := httptest.NewRecorder()
	app.handler.ServeHTTP(inRootAlias, httptest.NewRequest(http.MethodGet, "/climate/indexes/current-alias.json", nil))
	if inRootAlias.Code != http.StatusNotFound {
		t.Fatalf("in-root climate index symlink returned %d: %q", inRootAlias.Code, inRootAlias.Body.String())
	}

	unsafePaths := []string{
		"/climate/",
		"/climate/indexes/",
		"/climate/indexes/../current.json",
		"/climate/indexes/%2e%2e/current.json",
		"/climate/indexes/%252e%252e.json",
		"/climate/indexes/.hidden.json",
		"/climate/indexes/safe-index.json/extra",
		"/climate/releases/",
		"/climate/releases/safe-release/",
		"/climate/releases/../current.json",
		"/climate/releases/%2e%2e/current.json",
		"/climate/releases/%252e%252e/catalog.json",
		"/climate/releases/safe-release/points/month-01/001/../002.nwc",
		"/climate/releases/safe-release/points/month-01/001/%2e%2e%2f002.nwc",
		"/climate/releases/safe-release/points/month-13/001/002.nwc",
		"/climate/releases/safe-release/points/month-+1/001/002.nwc",
		"/climate/releases/safe-release/points/month-01/01/002.nwc",
		"/climate/releases/safe-release/points/month-01/+01/002.nwc",
		"/climate/releases/safe-release/scores/month-01/unknown/4/1/2.nws",
		"/climate/releases/safe-release/scores/month-01/nice_good/5/1/2.nws",
		"/climate/releases/safe-release/scores/month-01/nice_good/4/01/2.nws",
		"/climate/releases/safe-release/scores/month-01/nice_good/4/+1/2.nws",
		"/climate/releases/safe-release/scores/month-01/nice_good/4/1/-0.nws",
		"/climate/releases/safe-release/scores/month-01/nice_good/4/16/2.nws",
		"/climate/releases/safe-release/catalog.json/extra",
	}
	for _, requestPath := range unsafePaths {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("unsafe climate path %q returned %d: %s", requestPath, response.Code, response.Body.String())
		}
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, requestPath := range []string{"/climate/current.json", "/climate/indexes/safe-index.json", "/climate/releases/safe-release/catalog.json", "/" + blockPath} {
			response := httptest.NewRecorder()
			app.handler.ServeHTTP(response, httptest.NewRequest(method, requestPath, nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s returned %d", method, requestPath, response.Code)
			}
		}
	}
}

func TestForecastRunsUseValidatedPointerAllowlistAndImmutableHTTPContract(t *testing.T) {
	app := newForecastTestApplication(t)
	fixture := installForecastHTTPFixture(t, app)

	pointer := httptest.NewRecorder()
	app.handler.ServeHTTP(pointer, httptest.NewRequest(http.MethodGet, "/forecast/current.json?ignored=1", nil))
	etag := pointer.Header().Get("ETag")
	if pointer.Code != http.StatusOK || pointer.Body.String() != string(fixture.pointerRaw) || pointer.Header().Get("Cache-Control") != "no-cache" || pointer.Header().Get("Content-Type") != "application/json" || etag == "" || pointer.Header().Get("Last-Modified") != "" {
		t.Fatalf("forecast pointer: %d %+v %q", pointer.Code, pointer.Header(), pointer.Body.String())
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/forecast/current.json", nil)
	conditionalRequest.Header.Set("If-None-Match", etag)
	conditional := httptest.NewRecorder()
	app.handler.ServeHTTP(conditional, conditionalRequest)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 || conditional.Header().Get("ETag") != etag {
		t.Fatalf("conditional forecast pointer: %d %+v %q", conditional.Code, conditional.Header(), conditional.Body.String())
	}

	index := httptest.NewRecorder()
	app.handler.ServeHTTP(index, httptest.NewRequest(http.MethodHead, "/"+fixture.currentIndexPath, nil))
	if index.Code != http.StatusOK || index.Body.Len() != 0 || index.Header().Get("Content-Length") != fmt.Sprint(len(fixture.currentIndexRaw)) || index.Header().Get("Content-Type") != "application/json" || index.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("forecast index HEAD: %d %+v body=%d", index.Code, index.Header(), index.Body.Len())
	}
	previous := httptest.NewRecorder()
	app.handler.ServeHTTP(previous, httptest.NewRequest(http.MethodGet, "/"+fixture.previousIndexPath, nil))
	if previous.Code != http.StatusOK || previous.Body.String() != string(fixture.previousIndexRaw) {
		t.Fatalf("previous forecast index: %d %+v %q", previous.Code, previous.Header(), previous.Body.String())
	}
	conditionalIndexRequest := httptest.NewRequest(http.MethodGet, "/"+fixture.currentIndexPath, nil)
	conditionalIndexRequest.Header.Set("If-Modified-Since", index.Header().Get("Last-Modified"))
	conditionalIndex := httptest.NewRecorder()
	app.handler.ServeHTTP(conditionalIndex, conditionalIndexRequest)
	if conditionalIndex.Code != http.StatusNotModified || conditionalIndex.Body.Len() != 0 {
		t.Fatalf("conditional forecast index: %d %+v %q", conditionalIndex.Code, conditionalIndex.Header(), conditionalIndex.Body.String())
	}
	catalog := httptest.NewRecorder()
	app.handler.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/"+fixture.catalogPath, nil))
	if catalog.Code != http.StatusOK || catalog.Body.String() != string(fixture.catalogRaw) || catalog.Header().Get("Content-Type") != "application/json" || catalog.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("forecast catalog: %d %+v %q", catalog.Code, catalog.Header(), catalog.Body.String())
	}

	blockRequest := httptest.NewRequest(http.MethodGet, "/"+fixture.pointPath, nil)
	blockRequest.Header.Set("Range", "bytes=16-47")
	blockRequest.Header.Set("Accept-Encoding", "gzip")
	block := httptest.NewRecorder()
	app.handler.ServeHTTP(block, blockRequest)
	if block.Code != http.StatusPartialContent || block.Body.String() != string(fixture.pointRaw[16:48]) || block.Header().Get("Content-Range") != fmt.Sprintf("bytes 16-47/%d", len(fixture.pointRaw)) || block.Header().Get("Content-Length") != "32" || block.Header().Get("Accept-Ranges") != "bytes" || block.Header().Get("Content-Type") != forecastpoints.MediaType || block.Header().Get("Content-Encoding") != "" || block.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("forecast point range: %d %+v body=%d", block.Code, block.Header(), block.Body.Len())
	}

	scalarRequest := httptest.NewRequest(http.MethodGet, "/"+fixture.scalarPath, nil)
	scalarRequest.Header.Set("Accept-Encoding", "gzip")
	scalar := httptest.NewRecorder()
	app.handler.ServeHTTP(scalar, scalarRequest)
	if scalar.Code != http.StatusOK || scalar.Header().Get("Content-Encoding") != "gzip" || scalar.Header().Get("Content-Type") != tiledelivery.ScalarMediaType || scalar.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("forecast scalar: %d %+v", scalar.Code, scalar.Header())
	}
	if scalar.Header().Get("ETag") == "" || scalar.Header().Get("Last-Modified") == "" {
		t.Fatalf("forecast scalar validators: %+v", scalar.Header())
	}
	gzipReader, err := gzip.NewReader(scalar.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(gzipReader)
	if err != nil || string(decoded) != string(fixture.scalarRaw) {
		t.Fatalf("forecast scalar decoded body: %v bytes=%d", err, len(decoded))
	}
	conditionalScalarRequest := httptest.NewRequest(http.MethodGet, "/"+fixture.scalarPath, nil)
	conditionalScalarRequest.Header.Set("Accept-Encoding", "gzip")
	conditionalScalarRequest.Header.Set("If-None-Match", scalar.Header().Get("ETag"))
	conditionalScalar := httptest.NewRecorder()
	app.handler.ServeHTTP(conditionalScalar, conditionalScalarRequest)
	if conditionalScalar.Code != http.StatusNotModified || conditionalScalar.Body.Len() != 0 || conditionalScalar.Header().Get("ETag") != scalar.Header().Get("ETag") || conditionalScalar.Header().Get("Content-Encoding") != "" {
		t.Fatalf("conditional gzip forecast scalar: %d %+v %q", conditionalScalar.Code, conditionalScalar.Header(), conditionalScalar.Body.String())
	}

	unreferenced := filepath.Join(app.root, "forecast", "runs", fixture.currentID, "forecast-days", fixture.currentDate, forecastpoints.TierGlobal, "unreferenced.nwf")
	if err := os.WriteFile(unreferenced, []byte("not catalogued"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreferencedResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(unreferencedResponse, httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(unreferenced, app.root+string(filepath.Separator)), nil))
	if unreferencedResponse.Code != http.StatusNotFound {
		t.Fatalf("unreferenced forecast file returned %d: %s", unreferencedResponse.Code, unreferencedResponse.Body.String())
	}
	stale := httptest.NewRecorder()
	app.handler.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/forecast/runs/stale-run/index.json", nil))
	if stale.Code != http.StatusConflict {
		t.Fatalf("unpublished forecast run returned %d: %s", stale.Code, stale.Body.String())
	}

	outside := filepath.Join(t.TempDir(), "outside.nwt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	displayFullPath := filepath.Join(app.root, filepath.FromSlash(fixture.displayPath))
	if err := os.Remove(displayFullPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, displayFullPath); err != nil {
		t.Fatal(err)
	}
	escape := httptest.NewRecorder()
	app.handler.ServeHTTP(escape, httptest.NewRequest(http.MethodGet, "/"+fixture.displayPath, nil))
	if escape.Code != http.StatusServiceUnavailable || strings.Contains(escape.Body.String(), "secret") {
		t.Fatalf("forecast symlink returned %d: %q", escape.Code, escape.Body.String())
	}
	if err := os.WriteFile(filepath.Join(app.root, filepath.FromSlash(fixture.scalarPath)), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := httptest.NewRecorder()
	app.handler.ServeHTTP(corrupt, httptest.NewRequest(http.MethodGet, "/"+fixture.scalarPath, nil))
	if corrupt.Code != http.StatusServiceUnavailable || strings.Contains(corrupt.Body.String(), "corrupt") {
		t.Fatalf("checksum-mismatched forecast asset returned %d: %q", corrupt.Code, corrupt.Body.String())
	}
	if err := os.WriteFile(filepath.Join(app.root, filepath.FromSlash(fixture.catalogPath)), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptCatalog := httptest.NewRecorder()
	app.handler.ServeHTTP(corruptCatalog, httptest.NewRequest(http.MethodGet, "/"+fixture.catalogPath, nil))
	if corruptCatalog.Code != http.StatusServiceUnavailable {
		t.Fatalf("checksum-mismatched forecast catalog returned %d: %q", corruptCatalog.Code, corruptCatalog.Body.String())
	}
	if err := os.WriteFile(filepath.Join(app.root, filepath.FromSlash(fixture.currentIndexPath)), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptIndex := httptest.NewRecorder()
	app.handler.ServeHTTP(corruptIndex, httptest.NewRequest(http.MethodGet, "/"+fixture.currentIndexPath, nil))
	if corruptIndex.Code != http.StatusServiceUnavailable {
		t.Fatalf("checksum-mismatched forecast index returned %d: %q", corruptIndex.Code, corruptIndex.Body.String())
	}

	for _, unsafe := range []string{
		"/forecast/",
		"/forecast/runs/../current.json",
		"/forecast/runs/%2e%2e/current.json",
		"/forecast/runs/run-current/forecast-days/2026-07-23/global/../../index.json",
	} {
		response := httptest.NewRecorder()
		app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, unsafe, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("unsafe forecast path %q returned %d: %s", unsafe, response.Code, response.Body.String())
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, requestPath := range []string{"/forecast/current.json", "/" + fixture.currentIndexPath, "/" + fixture.pointPath} {
			response := httptest.NewRecorder()
			app.handler.ServeHTTP(response, httptest.NewRequest(method, requestPath, nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s returned %d", method, requestPath, response.Code)
			}
		}
	}
}

func TestForecastColdMetadataLoadDoesNotReadLargeAssetPayload(t *testing.T) {
	app := newForecastTestApplication(t)
	fixture := installForecastHTTPFixture(t, app)
	if err := os.Truncate(filepath.Join(app.root, filepath.FromSlash(fixture.pointPath)), 2<<30); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/forecast/current.json", nil))
	elapsed := time.Since(started)
	if response.Code != http.StatusOK || response.Body.String() != string(fixture.pointerRaw) {
		t.Fatalf("cold forecast pointer: %d %+v %q", response.Code, response.Header(), response.Body.String())
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("cold forecast metadata load took %s; asset payload was expected to remain unread", elapsed)
	}
	concrete := app.handler.(*application)
	concrete.forecastMu.Lock()
	asset := concrete.forecast.assets[fixture.pointPath]
	asset.bytes = 2 << 30
	concrete.forecast.assets[fixture.pointPath] = asset
	concrete.forecastMu.Unlock()
	headStarted := time.Now()
	head := httptest.NewRecorder()
	app.handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/"+fixture.pointPath, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(2<<30) {
		t.Fatalf("large forecast asset HEAD: %d %+v body=%d", head.Code, head.Header(), head.Body.Len())
	}
	if elapsed := time.Since(headStarted); elapsed >= 500*time.Millisecond {
		t.Fatalf("large forecast asset HEAD took %s; payload was expected to remain unread", elapsed)
	}
}

func newForecastTestApplication(t *testing.T) testApplication {
	t.Helper()
	root, web := t.TempDir(), t.TempDir()
	artifactFS, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{webRoot: web, artifactRoot: root, artifactFS: artifactFS}
	t.Cleanup(func() { _ = app.close() })
	return testApplication{handler: app, root: root, web: web}
}

type forecastHTTPFixture struct {
	pointerRaw, currentIndexRaw, previousIndexRaw []byte
	currentID, currentDate                        string
	currentIndexPath, previousIndexPath           string
	pointPath, displayPath, scalarPath            string
	pointRaw, scalarRaw                           []byte
	catalogPath                                   string
	catalogRaw                                    []byte
}

func installForecastHTTPFixture(t *testing.T, app testApplication) forecastHTTPFixture {
	t.Helper()
	concrete, ok := app.handler.(*application)
	if !ok {
		t.Fatal("test handler is not an application")
	}
	currentID, previousID := "run-current", "run-previous"
	currentDate, previousDate := "2026-07-23", "2026-07-22"
	pointRaw := []byte(strings.Repeat("forecast-point-block-", 16))
	scalarRaw := []byte(strings.Repeat("\x32", 1024))
	displayRaw := []byte(strings.Repeat("display", 64))

	makeIndex := func(id, date string, point, display, scalar []byte) (forecastruns.Index, []byte, forecastruns.IndexReference, string, string, string) {
		catalog := forecastcatalog.Catalog{
			ReleaseID: id, LocalDate: date, Tier: forecastpoints.TierGlobal,
			PointBlocks:   []forecastcatalog.PointBlock{{Path: "point.nwf", Bytes: uint64(len(point)), SHA256: sha256Hex(point), MediaType: forecastpoints.MediaType}},
			DisplayPoints: []forecastcatalog.DisplayPointTile{{Path: "display.nwt", Bytes: uint64(len(display)), SHA256: sha256Hex(display), MediaType: tiledelivery.PointMediaType}},
			ScalarTiles:   []forecastcatalog.ScalarTile{{Path: "score.nws", Bytes: uint64(len(scalar)), SHA256: sha256Hex(scalar), MediaType: tiledelivery.ScalarMediaType}},
		}
		catalogRaw, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		catalogRaw = append(catalogRaw, '\n')
		tierRoot := filepath.ToSlash(filepath.Join("forecast", "runs", id, "forecast-days", date, forecastpoints.TierGlobal))
		catalogPath := tierRoot + "/catalog.json"
		writeHTTPFixtureFile(t, app.root, catalogPath, catalogRaw)
		pointPath, displayPath, scalarPath := tierRoot+"/point.nwf", tierRoot+"/display.nwt", tierRoot+"/score.nws"
		writeHTTPFixtureFile(t, app.root, pointPath, point)
		writeHTTPFixtureFile(t, app.root, displayPath, display)
		writeHTTPFixtureFile(t, app.root, scalarPath, scalar)
		index := forecastruns.Index{IndexID: id, Days: []forecastruns.Day{{Date: date, Tiers: []forecastruns.TierReference{{Date: date, Tier: forecastpoints.TierGlobal, CatalogPath: strings.TrimPrefix(catalogPath, "forecast/"), CatalogBytes: uint64(len(catalogRaw)), CatalogSHA256: sha256Hex(catalogRaw)}}}}}
		indexRaw, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		indexRaw = append(indexRaw, '\n')
		indexPath := filepath.ToSlash(filepath.Join("forecast", "runs", id, forecastruns.IndexFilename))
		writeHTTPFixtureFile(t, app.root, indexPath, indexRaw)
		return index, indexRaw, forecastruns.IndexReference{IndexID: id, Path: strings.TrimPrefix(indexPath, "forecast/"), SHA256: sha256Hex(indexRaw)}, pointPath, displayPath, scalarPath
	}
	current, currentRaw, currentReference, pointPath, displayPath, scalarPath := makeIndex(currentID, currentDate, pointRaw, displayRaw, scalarRaw)
	previous, previousRaw, previousReference, _, _, _ := makeIndex(previousID, previousDate, []byte("previous point"), []byte("previous display"), []byte("previous scalar"))
	pointer := forecastruns.Pointer{Version: forecastruns.PointerVersion, Current: currentReference, Previous: &previousReference}
	pointerRaw, err := json.Marshal(pointer)
	if err != nil {
		t.Fatal(err)
	}
	pointerRaw = append(pointerRaw, '\n')
	writeHTTPFixtureFile(t, app.root, "forecast/current.json", pointerRaw)

	originalLoad, originalParse := loadForecastMetadata, parseForecastCatalog
	loadForecastMetadata = func(string) (forecastruns.Pointer, forecastruns.Index, *forecastruns.Index, error) {
		return pointer, current, &previous, nil
	}
	parseForecastCatalog = func(encoded []byte) (forecastcatalog.Catalog, error) {
		var catalog forecastcatalog.Catalog
		err := json.Unmarshal(encoded, &catalog)
		return catalog, err
	}
	t.Cleanup(func() {
		loadForecastMetadata, parseForecastCatalog = originalLoad, originalParse
	})
	concrete.forecastMu.Lock()
	concrete.forecast = nil
	concrete.forecastMu.Unlock()
	return forecastHTTPFixture{
		pointerRaw: pointerRaw, currentIndexRaw: currentRaw, previousIndexRaw: previousRaw,
		currentID: currentID, currentDate: currentDate,
		currentIndexPath: "forecast/" + currentReference.Path, previousIndexPath: "forecast/" + previousReference.Path,
		pointPath: pointPath, displayPath: displayPath, scalarPath: scalarPath, pointRaw: pointRaw, scalarRaw: scalarRaw,
		catalogPath: filepath.ToSlash(filepath.Join("forecast", "runs", currentID, "forecast-days", currentDate, forecastpoints.TierGlobal, "catalog.json")),
		catalogRaw:  readClimateArtifact(t, app.root, filepath.ToSlash(filepath.Join("forecast", "runs", currentID, "forecast-days", currentDate, forecastpoints.TierGlobal, "catalog.json"))),
	}
}

func writeHTTPFixtureFile(t *testing.T, root, relative string, payload []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishClimateHTTPPreview(t *testing.T, artifacts string) (climatetiles.Pointer, climatetiles.Index, climatetiles.Catalog) {
	t.Helper()
	reference := installClimateHTTPFixtureMonth(t, artifacts, 1, 61, climateHTTPFixtureProvenance())
	pointer, err := climatetiles.BuildAndPublishIndex(context.Background(), artifacts, "typical-2006-2025-preview-01", climatetiles.IndexStatePreview, []climatetiles.MonthReference{reference})
	if err != nil {
		t.Fatal(err)
	}
	_, index, err := climatetiles.ValidateCurrentIndex(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := climatetiles.ValidateRelease(filepath.Join(artifacts, "climate", "releases", reference.ReleaseID))
	if err != nil {
		t.Fatal(err)
	}
	return pointer, index, catalog
}

func installClimateHTTPFixtureMonth(t *testing.T, artifacts string, month int, score byte, provenance climatetiles.Provenance) climatetiles.MonthReference {
	t.Helper()
	releaseID := fmt.Sprintf("climate-http-%02d", month)
	period := fmt.Sprintf("month-%02d", month)
	reference, err := climatetiles.BuildAndInstallMonth(context.Background(), artifacts, releaseID, func(ctx context.Context, stage climatetiles.Stage) (climatetiles.Catalog, error) {
		if err := ctx.Err(); err != nil {
			return climatetiles.Catalog{}, err
		}
		block := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
		identity, err := tiledelivery.NewClimateBlockIdentity(releaseID, period, block)
		if err != nil {
			return climatetiles.Catalog{}, err
		}
		records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
		for index := range records {
			records[index] = climateHTTPFixtureRecord()
		}
		pointPath := climatetiles.PointBlockPath(period, block)
		pointAsset, err := tiledelivery.WriteClimateBlock(filepath.Join(stage.ReleaseRoot, filepath.FromSlash(pointPath)), releaseID, period, block, records)
		if err != nil {
			return climatetiles.Catalog{}, err
		}
		point, err := climatetiles.PointBlockFromAsset(stage.ReleaseRoot, pointAsset)
		if err != nil {
			return climatetiles.Catalog{}, err
		}
		tiles := make([]climatetiles.ScalarTile, 0, len(climatetiles.Fields())*(climatetiles.MaximumZoom+1))
		for zoom := uint8(0); zoom <= climatetiles.MaximumZoom; zoom++ {
			id, err := tiledelivery.NewID(string(climatetiles.FieldComfortGood), zoom, 0, 0)
			if err != nil {
				return climatetiles.Catalog{}, err
			}
			encoded, stats, err := tiledelivery.BuildScalarTile(id, func(tiledelivery.ScalarSample) (uint8, bool, error) { return score, true, nil })
			if err != nil {
				return climatetiles.Catalog{}, err
			}
			digest := sha256.Sum256(encoded)
			for _, field := range climatetiles.Fields() {
				relative := climatetiles.ScalarTilePath(period, field, zoom, 0, 0)
				filename := filepath.Join(stage.ReleaseRoot, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
					return climatetiles.Catalog{}, err
				}
				if err := os.WriteFile(filename, encoded, 0o644); err != nil {
					return climatetiles.Catalog{}, err
				}
				tiles = append(tiles, climatetiles.ScalarTile{
					Field: field, Zoom: zoom, Path: relative, Bytes: uint64(len(encoded)),
					SHA256: hex.EncodeToString(digest[:]), MediaType: tiledelivery.ScalarMediaType,
					DataTexels: stats.DataTexels, NoDataTexels: stats.NoDataTexels,
				})
			}
		}
		return climatetiles.NewCatalog(releaseID, period, provenance, []climatetiles.PointBlock{point}, tiles), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func climateHTTPFixtureProvenance() climatetiles.Provenance {
	source := sha256.Sum256([]byte("AgERA5 HTTP fixture"))
	land := sha256.Sum256([]byte("Natural Earth HTTP fixture"))
	return climatetiles.Provenance{
		SourceEvidenceSHA256: hex.EncodeToString(source[:]),
		LandMaskSetSHA256:    hex.EncodeToString(land[:]),
		DerivationContract:   climatebuild.CheckpointDerivationContract,
	}
}

func climateHTTPFixtureRecord() delivery.ClimateDistributionRecord {
	profile := delivery.ClimateProfileDistribution{
		MedianScore: 65, CalmGoodPercent: 75, PoorWeekPermille: 125,
		RatingShares: delivery.ClimateRatingShares{Awful: 50, Poor: 100, Mixed: 200, Good: 300, VeryGood: 250, Excellent: 100},
	}
	cycling := profile
	cycling.MedianScore = 58
	cycling.CalmGoodPercent = 60
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: 70, DryGoodPercent: 80, BrightGoodPercent: 65,
		Nice: profile, Cycling: cycling, Valid: true,
	}
}

func readClimateArtifact(t *testing.T, root, relative string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
