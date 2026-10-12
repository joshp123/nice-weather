package appserver

import (
	"bytes"
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
	"testing"
	"time"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/places"
	"nice-weather-finder/internal/tiledelivery"
)

const httpPlaceReleaseID = "places-http-fixture"

type testApplication struct {
	handler http.Handler
	root    string
	web     string
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
	placeManifest := []byte(`{"shards":{"mal":"test-shard"},"name":"HTTP place response fixture"}`)
	manifestDigest := sha256.Sum256(placeManifest)
	writeHTTPFixtureFile(t, root, "places/releases/"+httpPlaceReleaseID+"/manifest.json", placeManifest)
	writeHTTPFixtureFile(t, root, "places/releases/"+httpPlaceReleaseID+"/shards/mal.json", []byte(`{"name":"Málaga"}`))
	pointerBody, err := json.Marshal(places.Pointer{ReleaseID: httpPlaceReleaseID, ManifestSHA256: hex.EncodeToString(manifestDigest[:])})
	if err != nil {
		t.Fatal(err)
	}
	writeHTTPFixtureFile(t, root, "places/current.json", pointerBody)
	handler, err := NewHandler(Config{WebRoot: web, ArtifactRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if concrete, ok := handler.(*application); ok {
		t.Cleanup(func() { _ = concrete.close() })
	}
	return testApplication{handler: handler, root: root, web: web}
}

func TestCompressionCachingAndSPA(t *testing.T) {
	app := newTestApplication(t)

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

	spa := httptest.NewRecorder()
	app.handler.ServeHTTP(spa, httptest.NewRequest(http.MethodGet, "/some/map/route", nil))
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "Nice Weather Finder") || spa.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA response: %d %s %+v", spa.Code, spa.Body.String(), spa.Header())
	}
}

func TestBestWeatherArtifactsAreStaticReleaseBoundFiles(t *testing.T) {
	app := newTestApplication(t)
	const forecastID = "forecast-run"
	artifact := []byte(`{"schema":"nice-best-weather-world-v1","forecastIndexId":"forecast-run","placeReleaseId":"` + httpPlaceReleaseID + `","places":[]}`)
	digest := sha256.Sum256(artifact)
	checksum := hex.EncodeToString(digest[:])
	artifactURL := "/best-weather/releases/forecast-run-world-" + checksum[:16] + ".json"
	writeHTTPFixtureFile(t, app.root, strings.TrimPrefix(artifactURL, "/"), artifact)
	pointerBody := []byte(`{"schema":"nice-best-weather-world-v1","forecastIndexId":"forecast-run","placeReleaseId":"` + httpPlaceReleaseID + `","url":"` + artifactURL + `","sha256":"` + checksum + `"}`)
	writeHTTPFixtureFile(t, app.root, "best-weather/current.json", pointerBody)
	pointerResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(pointerResponse, httptest.NewRequest(http.MethodGet, "/best-weather/current.json", nil))
	if pointerResponse.Code != http.StatusOK || pointerResponse.Header().Get("Cache-Control") != "no-cache" || pointerResponse.Header().Get("ETag") == "" {
		t.Fatalf("best-weather pointer: %d %+v %s", pointerResponse.Code, pointerResponse.Header(), pointerResponse.Body.String())
	}
	var pointer struct {
		ForecastID   string `json:"forecastIndexId"`
		PlaceRelease string `json:"placeReleaseId"`
		URL          string `json:"url"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(pointerResponse.Body.Bytes(), &pointer); err != nil {
		t.Fatal(err)
	}
	if pointer.ForecastID != forecastID || pointer.PlaceRelease != httpPlaceReleaseID || pointer.URL != artifactURL {
		t.Fatalf("best-weather pointer = %+v", pointer)
	}
	artifactResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(artifactResponse, httptest.NewRequest(http.MethodGet, pointer.URL, nil))
	servedDigest := sha256.Sum256(artifactResponse.Body.Bytes())
	if artifactResponse.Code != http.StatusOK || artifactResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || hex.EncodeToString(servedDigest[:]) != pointer.SHA256 || !strings.Contains(artifactResponse.Body.String(), `"forecastIndexId":"`+forecastID+`"`) {
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
	if pointer.ReleaseID != httpPlaceReleaseID || len(pointer.ManifestSHA256) != 64 {
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

	second := places.Pointer{ReleaseID: "fixture-places-second", ManifestSHA256: pointer.ManifestSHA256}
	secondBody, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	writeHTTPFixtureFile(t, app.root, "places/current.json", secondBody)
	writeHTTPFixtureFile(t, app.root, "places/releases/"+second.ReleaseID+"/shards/mal.json", []byte(`{"name":"Málaga"}`))
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
	rangeRequest.Header.Set("Range", "bytes=64-127")
	rangeRequest.Header.Set("Accept-Encoding", "gzip")
	partial := httptest.NewRecorder()
	app.handler.ServeHTTP(partial, rangeRequest)
	if partial.Code != http.StatusPartialContent || partial.Body.String() != string(blockBody[64:128]) || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 64-127/%d", len(blockBody)) || partial.Header().Get("Content-Length") != "64" || partial.Header().Get("Accept-Ranges") != "bytes" || partial.Header().Get("Content-Type") != era5climateproduct.PointMediaType || partial.Header().Get("Content-Encoding") != "" || partial.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate block range: %d %+v body=%d", partial.Code, partial.Header(), partial.Body.Len())
	}
	head := httptest.NewRecorder()
	app.handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, blockURL, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(blockBody)) || head.Header().Get("Content-Type") != era5climateproduct.PointMediaType || head.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("climate block HEAD: %d %+v body=%d", head.Code, head.Header(), head.Body.Len())
	}

	datelinePath := "climate/releases/" + index.Months[0].ReleaseID + "/points/" + climateCatalog.Period + "/dateline/020.nwd"
	datelineBody := append([]byte(nil), blockBody[:128]...)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(app.root, filepath.FromSlash(datelinePath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.root, filepath.FromSlash(datelinePath)), datelineBody, 0o644); err != nil {
		t.Fatal(err)
	}
	datelineRequest := httptest.NewRequest(http.MethodGet, "/"+datelinePath, nil)
	datelineRequest.Header.Set("Range", "bytes=64-127")
	dateline := httptest.NewRecorder()
	app.handler.ServeHTTP(dateline, datelineRequest)
	if dateline.Code != http.StatusPartialContent || dateline.Body.String() != string(datelineBody[64:128]) || dateline.Header().Get("Content-Type") != era5climateproduct.OverrideMediaType || dateline.Header().Get("Content-Range") != "bytes 64-127/128" {
		t.Fatalf("climate dateline range: %d %+v body=%d", dateline.Code, dateline.Header(), dateline.Body.Len())
	}

	clearSkyPath := "climate/releases/" + index.Months[0].ReleaseID + "/scores/" + climateCatalog.Period + "/clear_sky_good/4/0/0.nws"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(app.root, filepath.FromSlash(clearSkyPath))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.root, filepath.FromSlash(clearSkyPath)), scalarBody, 0o644); err != nil {
		t.Fatal(err)
	}
	clearSky := httptest.NewRecorder()
	app.handler.ServeHTTP(clearSky, httptest.NewRequest(http.MethodGet, "/"+clearSkyPath, nil))
	if clearSky.Code != http.StatusOK || clearSky.Header().Get("Content-Type") != tiledelivery.ScalarMediaType {
		t.Fatalf("clear-sky scalar: %d %+v", clearSky.Code, clearSky.Header())
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
	for _, missingPath := range []string{"/climate/current.json", "/climate/indexes/missing.json", "/climate/releases/missing/catalog.json"} {
		missing := httptest.NewRecorder()
		app.handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, missingPath, nil))
		if missing.Code != http.StatusNotFound {
			t.Errorf("missing climate path %q returned %d: %s", missingPath, missing.Code, missing.Body.String())
		}
	}

	releaseID := "safe-release"
	blockPath := "climate/releases/" + releaseID + "/points/month-12/main/045/044.nwc"
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
		"/climate/releases/safe-release/points/month-01/001/002.nwc",
		"/climate/releases/safe-release/points/month-13/001/002.nwc",
		"/climate/releases/safe-release/points/month-+1/001/002.nwc",
		"/climate/releases/safe-release/points/month-01/01/002.nwc",
		"/climate/releases/safe-release/points/month-01/+01/002.nwc",
		"/climate/releases/safe-release/points/month-01/main/046/000.nwc",
		"/climate/releases/safe-release/points/month-01/main/000/045.nwc",
		"/climate/releases/safe-release/points/month-01/dateline/046.nwd",
		"/climate/releases/safe-release/scores/month-01/unknown/4/1/2.nws",
		"/climate/releases/safe-release/scores/month-01/bright_good/4/1/2.nws",
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

func writeHTTPFixtureFile(t testing.TB, root, relative string, payload []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishClimateHTTPPreview(t *testing.T, artifacts string) (era5climateproduct.Pointer, era5climateproduct.Index, era5climateproduct.Catalog) {
	t.Helper()
	releaseID, period, indexID := "era5-http-01", "month-01", "era5-http-preview"
	releaseRoot := filepath.Join(artifacts, "climate", "releases", releaseID)
	pointPath := "points/" + period + "/main/020/020.nwc"
	scalarPath := "scores/" + period + "/comfort_good/4/0/0.nws"
	pointBody := make([]byte, 64+16*32*64)
	copy(pointBody, []byte("NWE5CPT1"))
	scalarBody := bytes.Repeat([]byte{61}, tiledelivery.ScalarTileBytes)
	for relative, body := range map[string][]byte{pointPath: pointBody, scalarPath: scalarBody} {
		path := filepath.Join(releaseRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog := era5climateproduct.Catalog{
		Version: era5climateproduct.CatalogVersion, ReleaseID: releaseID, Period: period,
		PointBlocks: []era5climateproduct.PointBlock{{Path: pointPath}},
		ScalarTiles: []era5climateproduct.ScalarTile{{Field: era5climateproduct.FieldComfortGood, Path: scalarPath}},
	}
	catalogBody := writeClimateHTTPJSON(t, filepath.Join(releaseRoot, "catalog.json"), catalog)
	catalogDigest := sha256.Sum256(catalogBody)
	index := era5climateproduct.Index{
		Version: era5climateproduct.IndexVersion, IndexID: indexID, State: era5climateproduct.IndexStatePreview,
		Months: []era5climateproduct.MonthReference{{Period: period, ReleaseID: releaseID, CatalogSHA256: hex.EncodeToString(catalogDigest[:])}},
	}
	indexPath := filepath.Join(artifacts, "climate", "indexes", indexID+".json")
	indexBody := writeClimateHTTPJSON(t, indexPath, index)
	indexDigest := sha256.Sum256(indexBody)
	pointer := era5climateproduct.Pointer{Version: era5climateproduct.PointerVersion, IndexID: indexID, IndexSHA256: hex.EncodeToString(indexDigest[:])}
	writeClimateHTTPJSON(t, filepath.Join(artifacts, "climate", "current.json"), pointer)
	return pointer, index, catalog
}

func writeClimateHTTPJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return body
}

func readClimateArtifact(t *testing.T, root, relative string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
