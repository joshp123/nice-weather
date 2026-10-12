package appserver

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/forecastcatalog"
	"nice-weather-finder/internal/forecastrange"
	"nice-weather-finder/internal/forecastruns"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/tiledelivery"
)

type Config struct {
	WebRoot      string
	ArtifactRoot string
	Listen       string
}

type application struct {
	webRoot      string
	artifactRoot string
	artifactFS   *os.Root
	forecastMu   sync.Mutex
	forecast     *forecastSnapshot
}

type forecastSnapshot struct {
	pointerRaw  []byte
	pointerETag string
	assets      map[string]forecastAsset
	runs        map[string]bool
}

type forecastAsset struct {
	bytes     uint64
	sha256    string
	mediaType string
}

const (
	maximumForecastPointerBytes = 2 * 1024
	maximumForecastIndexBytes   = 64 * 1024
)

var errForecastPublicationChanged = errors.New("forecast publication changed while loading")

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

const DefaultListen = "127.0.0.1:5198"

func Serve(config Config) error {
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	handler, err := NewHandler(config)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: config.Listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	fmt.Printf("Nice Weather Finder is running at http://%s\n", config.Listen)
	if app, ok := handler.(*application); ok {
		defer app.close()
	}
	return server.ListenAndServe()
}

func NewHandler(config Config) (http.Handler, error) {
	webRootAbsolute, err := filepath.Abs(config.WebRoot)
	if err != nil {
		return nil, err
	}
	webRoot, err := filepath.EvalSymlinks(webRootAbsolute)
	if err != nil {
		return nil, fmt.Errorf("resolve web root: %w", err)
	}
	artifactRootAbsolute, err := filepath.Abs(config.ArtifactRoot)
	if err != nil {
		return nil, err
	}
	artifactRoot, err := filepath.EvalSymlinks(artifactRootAbsolute)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	artifactFS, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact root: %w", err)
	}
	return &application{webRoot: webRoot, artifactRoot: artifactRoot, artifactFS: artifactFS}, nil
}

func (app *application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	app.serveFile(w, r)
}

func (app *application) close() error {
	return app.artifactFS.Close()
}

func (app *application) serveFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or HEAD for files.")
		return
	}
	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	if requestPath == "" {
		requestPath = "index.html"
	}
	if requestPath == "manifest.json" || requestPath == "releases" || strings.HasPrefix(requestPath, "releases/") {
		writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		return
	}
	if strings.HasPrefix(requestPath, "forecast/") {
		app.serveForecastArtifact(w, r, requestPath)
		return
	}
	root, cache := app.webRoot, "no-cache"
	if assetPath, immutable, matched, err := staticArtifactPath(requestPath); matched {
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_path", "The requested path is invalid.")
			return
		}
		cache := "no-cache"
		if immutable {
			cache = "public, max-age=31536000, immutable"
		}
		app.serveStaticArtifact(w, r, assetPath, cache)
		return
	} else if strings.HasPrefix(requestPath, "assets/") {
		cache = "public, max-age=31536000, immutable"
	}
	path, err := safeJoin(root, requestPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_path", "The requested path is invalid.")
		return
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		if root == app.webRoot && filepath.Ext(requestPath) == "" && !strings.HasPrefix(requestPath, "api/") {
			path, err = safeJoin(app.webRoot, "index.html")
			if err == nil {
				info, statErr = os.Stat(path)
				cache = "no-cache"
			}
		}
	}
	if statErr != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		return
	}
	path, err = resolveWithin(root, path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_path", "The requested path is invalid.")
		return
	}
	w.Header().Set("Cache-Control", cache)
	contentType := contentTypeForPath(path)
	if isCompressible(contentType) {
		if acceptsEncoding(r.Header.Get("Accept-Encoding"), "br") {
			compressedPath, resolveErr := resolveWithin(root, path+".br")
			if encoded, err := os.ReadFile(compressedPath); resolveErr == nil && err == nil {
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Vary", "Accept-Encoding")
				w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
				if r.Method != http.MethodHead {
					_, _ = w.Write(encoded)
				}
				return
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", "The requested file could not be read.")
			return
		}
		serveBytes(w, r, data, contentType)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", "The requested file could not be read.")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (app *application) serveForecastArtifact(w http.ResponseWriter, r *http.Request, requestPath string) {
	if err := validateForecastRequestPath(requestPath); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_path", "The requested path is invalid.")
		return
	}
	snapshot, err := app.currentForecastSnapshot()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		} else {
			writeError(w, http.StatusServiceUnavailable, "forecast_unavailable", "The published forecast is not available.")
		}
		return
	}
	if requestPath == "forecast/current.json" {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", snapshot.pointerETag)
		http.ServeContent(w, r, "current.json", time.Time{}, bytes.NewReader(snapshot.pointerRaw))
		return
	}
	asset, ok := snapshot.assets[requestPath]
	if !ok {
		parts := strings.Split(requestPath, "/")
		if len(parts) >= 3 && parts[0] == "forecast" && parts[1] == "runs" && !snapshot.runs[parts[2]] {
			writeError(w, http.StatusConflict, "stale_forecast_run", "This forecast run is not published. Refresh the forecast.")
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", asset.mediaType)
	w.Header().Set("ETag", `"`+asset.sha256+`"`)
	compress := r.Header.Get("Range") == "" && isCompressible(asset.mediaType) && acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip")
	if isCompressible(asset.mediaType) {
		w.Header().Set("Vary", "Accept-Encoding")
	}
	if ifNoneMatch(r.Header.Get("If-None-Match"), w.Header().Get("ETag")) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	info, err := app.statForecastAsset(requestPath, asset)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "forecast_unavailable", "The published forecast file is not available.")
		return
	}
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	if r.Header.Get("If-None-Match") == "" && notModifiedSince(r.Header.Get("If-Modified-Since"), info.ModTime()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Accept-Ranges", "bytes")
		if compress {
			w.Header().Set("Content-Encoding", "gzip")
		} else {
			w.Header().Set("Content-Length", strconv.FormatUint(asset.bytes, 10))
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	payload, err := app.readVerifiedForecastAsset(requestPath, asset)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "forecast_unavailable", "The published forecast file is not available.")
		return
	}
	if compress {
		serveBytes(w, r, payload, asset.mediaType)
		return
	}
	http.ServeContent(w, r, filepath.Base(requestPath), info.ModTime(), bytes.NewReader(payload))
}

func (app *application) currentForecastSnapshot() (*forecastSnapshot, error) {
	app.forecastMu.Lock()
	defer app.forecastMu.Unlock()
	pointerRaw, err := app.readBoundedArtifact("forecast/current.json", maximumForecastPointerBytes)
	if err != nil {
		return nil, err
	}
	if app.forecast != nil && bytes.Equal(pointerRaw, app.forecast.pointerRaw) {
		return app.forecast, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		next, err := app.loadForecastSnapshot()
		if errors.Is(err, errForecastPublicationChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		app.forecast = next
		return next, nil
	}
	return nil, errForecastPublicationChanged
}

func (app *application) loadForecastSnapshot() (*forecastSnapshot, error) {
	pointer, current, previous, err := forecastruns.LoadCurrentMetadata(app.artifactRoot)
	if err != nil {
		return nil, err
	}
	pointerRaw, err := app.readBoundedArtifact("forecast/current.json", maximumForecastPointerBytes)
	if err != nil {
		return nil, err
	}
	var visible forecastruns.Pointer
	if err := decodeExactJSON(pointerRaw, &visible); err != nil || !sameForecastPointer(pointer, visible) {
		return nil, errForecastPublicationChanged
	}
	snapshot := &forecastSnapshot{pointerRaw: pointerRaw, assets: make(map[string]forecastAsset), runs: make(map[string]bool)}
	digest := sha256.Sum256(pointerRaw)
	snapshot.pointerETag = `"` + hex.EncodeToString(digest[:]) + `"`
	if err := app.addForecastRun(snapshot, pointer.Current, &current); err != nil {
		return nil, err
	}
	if pointer.Previous != nil {
		if previous == nil {
			return nil, errors.New("forecast previous index metadata is missing")
		}
		if err := app.addForecastRun(snapshot, *pointer.Previous, previous); err != nil {
			return nil, err
		}
	}
	after, err := app.readBoundedArtifact("forecast/current.json", maximumForecastPointerBytes)
	if err != nil || !bytes.Equal(pointerRaw, after) {
		return nil, errForecastPublicationChanged
	}
	return snapshot, nil
}

func (app *application) addForecastRun(snapshot *forecastSnapshot, reference forecastruns.IndexReference, validated *forecastruns.Index) error {
	snapshot.runs[reference.IndexID] = true
	indexPath := "forecast/" + reference.Path
	encoded, err := app.readBoundedArtifact(indexPath, maximumForecastIndexBytes)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != reference.SHA256 {
		return errors.New("forecast index checksum does not match the pointer")
	}
	var index forecastruns.Index
	if err := decodeExactJSON(encoded, &index); err != nil || index.IndexID != reference.IndexID {
		return errors.New("forecast index identity is invalid")
	}
	if validated != nil && !sameForecastIndex(*validated, index) {
		return errForecastPublicationChanged
	}
	if err := addForecastAsset(snapshot.assets, indexPath, forecastAsset{bytes: uint64(len(encoded)), sha256: reference.SHA256, mediaType: "application/json"}); err != nil {
		return err
	}
	for _, day := range index.Days {
		for _, tier := range day.Tiers {
			catalogPath := "forecast/" + tier.CatalogPath
			catalogRaw, err := app.readBoundedArtifact(catalogPath, forecastcatalog.MaximumCatalogBytes)
			if err != nil {
				return err
			}
			catalogDigest := sha256.Sum256(catalogRaw)
			if uint64(len(catalogRaw)) != tier.CatalogBytes || hex.EncodeToString(catalogDigest[:]) != tier.CatalogSHA256 {
				return errors.New("forecast catalogue does not match the run index")
			}
			catalog, err := forecastcatalog.Parse(catalogRaw)
			if err != nil || catalog.ReleaseID != tier.ReleaseID || catalog.LocalDate != day.Date || catalog.Tier != tier.Tier {
				return errors.New("forecast catalogue identity is invalid")
			}
			if err := addForecastAsset(snapshot.assets, catalogPath, forecastAsset{bytes: uint64(len(catalogRaw)), sha256: tier.CatalogSHA256, mediaType: "application/json"}); err != nil {
				return err
			}
			tierRoot := filepath.ToSlash(filepath.Dir(catalogPath))
			for _, point := range catalog.DailyPointBlocks {
				if err := addCatalogForecastAsset(snapshot.assets, tierRoot, point.Path, point.Bytes, point.SHA256, point.MediaType); err != nil {
					return err
				}
			}
			for _, scalar := range catalog.ScalarTiles {
				if err := addCatalogForecastAsset(snapshot.assets, tierRoot, scalar.Path, scalar.Bytes, scalar.SHA256, scalar.MediaType); err != nil {
					return err
				}
			}
		}
	}
	if err := app.addForecastRange(snapshot, index, index.Range); err != nil {
		return err
	}
	if index.RegionalRange != nil {
		return app.addForecastRange(snapshot, index, *index.RegionalRange)
	}
	return nil
}

func (app *application) addForecastRange(snapshot *forecastSnapshot, index forecastruns.Index, reference forecastruns.RangeReference) error {
	catalogPath := "forecast/" + reference.CatalogPath
	catalogRaw, err := app.readBoundedArtifact(catalogPath, forecastrange.MaximumCatalogBytes)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(catalogRaw)
	if uint64(len(catalogRaw)) != reference.CatalogBytes || hex.EncodeToString(digest[:]) != reference.CatalogSHA256 {
		return errors.New("forecast range catalogue does not match the run index")
	}
	catalog, err := forecastrange.Parse(catalogRaw)
	if err != nil || !rangeCatalogMatchesReference(catalog, index, reference) {
		return errors.New("forecast range catalogue identity is invalid")
	}
	if err := addForecastAsset(snapshot.assets, catalogPath, forecastAsset{bytes: uint64(len(catalogRaw)), sha256: reference.CatalogSHA256, mediaType: "application/json"}); err != nil {
		return err
	}
	rangeRoot := filepath.ToSlash(filepath.Dir(catalogPath))
	for _, block := range catalog.PointBlocks {
		if !block.Omitted {
			if err := addCatalogForecastAsset(snapshot.assets, rangeRoot, block.Path, block.Bytes, block.SHA256, catalog.PointBlockLayout.MediaType); err != nil {
				return err
			}
		}
	}
	for _, scalar := range catalog.ScalarTiles {
		if err := addCatalogForecastAsset(snapshot.assets, rangeRoot, scalar.Path, scalar.Bytes, scalar.SHA256, catalog.Scores.MediaType); err != nil {
			return err
		}
	}
	return nil
}

func rangeCatalogMatchesReference(catalog forecastrange.Catalog, index forecastruns.Index, reference forecastruns.RangeReference) bool {
	if catalog.ReleaseID != index.IndexID || catalog.Period != reference.Period ||
		catalog.Source.Provider != reference.Provider || catalog.Tier() != reference.Tier ||
		catalog.Source.Product != reference.Product || catalog.Source.RunUTC != reference.RunUTC ||
		catalog.Source.NativeResolutionKM != reference.NativeResolutionKM || catalog.Source.StaleAfterHours != reference.StaleAfterHours ||
		len(catalog.Dates) != len(index.Days) || len(catalog.Inputs) != len(index.Days) {
		return false
	}
	prefix := filepath.ToSlash(filepath.Join("runs", index.IndexID)) + "/"
	regionalIndex := 0
	for day, entry := range index.Days {
		if len(entry.Tiers) == 0 {
			return false
		}
		global := entry.Tiers[len(entry.Tiers)-1]
		input := catalog.Inputs[day]
		if catalog.Dates[day] != entry.Date || input.Date != entry.Date || input.Path != strings.TrimPrefix(global.CatalogPath, prefix) ||
			input.Bytes != global.CatalogBytes || input.SHA256 != global.CatalogSHA256 {
			return false
		}
		if reference.Tier == forecasttiles.TierEurope && len(entry.Tiers) == 2 {
			if regionalIndex >= len(catalog.RegionalInputs) {
				return false
			}
			regional := entry.Tiers[0]
			input := catalog.RegionalInputs[regionalIndex]
			if input.Date != entry.Date || input.Path != strings.TrimPrefix(regional.CatalogPath, prefix) || input.Bytes != regional.CatalogBytes || input.SHA256 != regional.CatalogSHA256 {
				return false
			}
			regionalIndex++
		}
	}
	return regionalIndex == len(catalog.RegionalInputs)
}

func addCatalogForecastAsset(assets map[string]forecastAsset, root, relative string, size uint64, checksum, mediaType string) error {
	path := filepath.ToSlash(filepath.Join(root, filepath.FromSlash(relative)))
	if !strings.HasPrefix(path, root+"/") {
		return errors.New("forecast catalogue asset path escapes its tier")
	}
	return addForecastAsset(assets, path, forecastAsset{bytes: size, sha256: checksum, mediaType: mediaType})
}

func addForecastAsset(assets map[string]forecastAsset, path string, asset forecastAsset) error {
	if _, err := safeJoin(".", path); err != nil || asset.bytes == 0 || len(asset.sha256) != sha256.Size*2 || asset.mediaType == "" {
		return errors.New("forecast asset identity is invalid")
	}
	if _, exists := assets[path]; exists {
		return errors.New("forecast asset path is duplicated")
	}
	assets[path] = asset
	return nil
}

func (app *application) readBoundedArtifact(requestPath string, maximum int) ([]byte, error) {
	if err := rejectSymlinkComponents(app.artifactFS, requestPath); err != nil {
		return nil, err
	}
	file, err := app.artifactFS.Open(requestPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("forecast publication file is not a bounded regular file")
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(payload) == 0 || len(payload) > maximum {
		return nil, errors.New("forecast publication file could not be read safely")
	}
	return payload, nil
}

func (app *application) statForecastAsset(requestPath string, asset forecastAsset) (fs.FileInfo, error) {
	if err := rejectSymlinkComponents(app.artifactFS, requestPath); err != nil {
		return nil, err
	}
	info, err := app.artifactFS.Lstat(requestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(asset.bytes) {
		return nil, errors.New("forecast asset size does not match its catalogue")
	}
	return info, nil
}

func (app *application) readVerifiedForecastAsset(requestPath string, asset forecastAsset) ([]byte, error) {
	if err := rejectSymlinkComponents(app.artifactFS, requestPath); err != nil {
		return nil, err
	}
	file, err := app.artifactFS.Open(requestPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(asset.bytes) {
		return nil, errors.New("forecast asset size does not match its catalogue")
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(asset.bytes)+1))
	if err != nil || uint64(len(payload)) != asset.bytes {
		return nil, errors.New("forecast asset changed while reading")
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != asset.sha256 {
		return nil, errors.New("forecast asset checksum does not match its catalogue")
	}
	return payload, nil
}

func ifNoneMatch(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

func notModifiedSince(header string, modTime time.Time) bool {
	if header == "" || modTime.IsZero() {
		return false
	}
	since, err := http.ParseTime(header)
	return err == nil && !modTime.Truncate(time.Second).After(since)
}

func validateForecastRequestPath(requestPath string) error {
	if _, err := safeJoin(".", requestPath); err != nil {
		return err
	}
	if requestPath == "forecast/current.json" {
		return nil
	}
	parts := strings.Split(requestPath, "/")
	if len(parts) < 4 || parts[0] != "forecast" || parts[1] != "runs" || !validReleaseID(parts[2]) {
		return errors.New("unsafe forecast asset path")
	}
	return nil
}

func sameForecastPointer(left, right forecastruns.Pointer) bool {
	if left.Version != right.Version || left.Current != right.Current || (left.Previous == nil) != (right.Previous == nil) {
		return false
	}
	return left.Previous == nil || *left.Previous == *right.Previous
}

func sameForecastIndex(left, right forecastruns.Index) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func decodeExactJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON has trailing content")
	}
	return nil
}

func (app *application) serveStaticArtifact(w http.ResponseWriter, r *http.Request, requestPath, cache string) {
	if strings.HasPrefix(requestPath, "climate/") {
		if err := rejectSymlinkComponents(app.artifactFS, requestPath); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
			return
		}
	}
	file, err := app.artifactFS.Open(requestPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found", "The requested file was not found.")
		return
	}
	contentType := contentTypeForPath(requestPath)
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", contentType)
	modTime := info.ModTime()
	if requestPath == "places/current.json" || requestPath == "best-weather/current.json" || requestPath == "climate/current.json" {
		pointerReadError := "The place index pointer could not be read."
		if requestPath == "climate/current.json" {
			pointerReadError = "The climate pointer could not be read."
		}
		payload, err := io.ReadAll(io.LimitReader(file, 1025))
		if err != nil || len(payload) > 1024 {
			writeError(w, http.StatusInternalServerError, "read_failed", pointerReadError)
			return
		}
		digest := sha256.Sum256(payload)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", pointerReadError)
			return
		}
		// The pointer can rotate more than once within a filesystem timestamp
		// tick. Its content-derived ETag is the sole safe cache validator.
		modTime = time.Time{}
	}
	if requestPath != "climate/current.json" && r.Header.Get("Range") == "" && acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip") {
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", "The requested file could not be read.")
			return
		}
		serveBytes(w, r, data, contentType)
		return
	}
	http.ServeContent(w, r, filepath.Base(requestPath), modTime, file)
}

func rejectSymlinkComponents(root *os.Root, requestPath string) error {
	current := ""
	for _, part := range strings.Split(requestPath, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("artifact path contains a symlink")
		}
	}
	return nil
}

func staticArtifactPath(requestPath string) (assetPath string, immutable, matched bool, err error) {
	if requestPath == "climate/current.json" {
		return requestPath, false, true, nil
	}
	if strings.HasPrefix(requestPath, "climate/") {
		if validClimateArtifactPath(requestPath) {
			return requestPath, true, true, nil
		}
		return "", false, true, errors.New("unsafe climate asset path")
	}
	if requestPath == "best-weather/current.json" {
		return requestPath, false, true, nil
	}
	if strings.HasPrefix(requestPath, "best-weather/") {
		parts := strings.Split(requestPath, "/")
		if len(parts) == 3 && parts[0] == "best-weather" && parts[1] == "releases" && strings.HasSuffix(parts[2], ".json") && validReleaseID(strings.TrimSuffix(parts[2], ".json")) {
			return requestPath, true, true, nil
		}
		return "", false, true, errors.New("unsafe best-weather asset path")
	}
	if requestPath == "places/current.json" {
		return requestPath, false, true, nil
	}
	if !strings.HasPrefix(requestPath, "places/") {
		return "", false, false, nil
	}
	parts := strings.Split(requestPath, "/")
	if len(parts) == 4 && parts[0] == "places" && parts[1] == "releases" && validReleaseID(parts[2]) && parts[3] == "manifest.json" {
		return requestPath, true, true, nil
	}
	if len(parts) == 5 && parts[0] == "places" && parts[1] == "releases" && validReleaseID(parts[2]) && parts[3] == "shards" && validPlaceShardName(parts[4]) {
		return requestPath, true, true, nil
	}
	return "", false, true, errors.New("unsafe place asset path")
}

func validClimateArtifactPath(requestPath string) bool {
	parts := strings.Split(requestPath, "/")
	if len(parts) == 3 && parts[0] == "climate" && parts[1] == "indexes" {
		return strings.HasSuffix(parts[2], ".json") && validClimateReleaseID(strings.TrimSuffix(parts[2], ".json"))
	}
	if len(parts) < 4 || parts[0] != "climate" || parts[1] != "releases" || !validClimateReleaseID(parts[2]) {
		return false
	}
	if len(parts) == 4 {
		return parts[3] == "catalog.json"
	}
	if len(parts) == 8 && parts[3] == "points" && validClimatePeriod(parts[4]) && parts[5] == "main" {
		return validFixedWidthNumber(parts[6], 3, 45) && validFixedWidthNumber(strings.TrimSuffix(parts[7], ".nwc"), 3, 44) && strings.HasSuffix(parts[7], ".nwc")
	}
	if len(parts) == 7 && parts[3] == "points" && validClimatePeriod(parts[4]) && parts[5] == "dateline" {
		return validFixedWidthNumber(strings.TrimSuffix(parts[6], ".nwd"), 3, 45) && strings.HasSuffix(parts[6], ".nwd")
	}
	if len(parts) == 9 && parts[3] == "scores" && validClimatePeriod(parts[4]) && validClimateField(parts[5]) {
		zoom, ok := canonicalNumber(parts[6], 4)
		if !ok {
			return false
		}
		maximumCoordinate := (1 << zoom) - 1
		_, xOK := canonicalNumber(parts[7], maximumCoordinate)
		_, yOK := canonicalNumber(strings.TrimSuffix(parts[8], ".nws"), maximumCoordinate)
		return xOK && yOK && strings.HasSuffix(parts[8], ".nws")
	}
	return false
}

func validClimateReleaseID(value string) bool {
	return len(value) <= 64 && validReleaseID(value)
}

func validClimatePeriod(value string) bool {
	if len(value) != len("month-01") || !strings.HasPrefix(value, "month-") {
		return false
	}
	digits := strings.TrimPrefix(value, "month-")
	if !asciiDecimal(digits) {
		return false
	}
	month, err := strconv.Atoi(digits)
	return err == nil && month >= 1 && month <= 12
}

func validClimateField(value string) bool {
	for _, field := range era5climateproduct.Fields() {
		if value == string(field) {
			return true
		}
	}
	return false
}

func validFixedWidthNumber(value string, width, maximum int) bool {
	if len(value) != width || !asciiDecimal(value) {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 0 && number <= maximum
}

func canonicalNumber(value string, maximum int) (int, bool) {
	if !asciiDecimal(value) || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	return number, err == nil && number >= 0 && number <= maximum
}

func asciiDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validPlaceShardName(value string) bool {
	if len(value) != len("abc.json") || !strings.HasSuffix(value, ".json") {
		return false
	}
	for _, character := range strings.TrimSuffix(value, ".json") {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validReleaseID(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '.' || character == '_' || character == '-')) {
			continue
		}
		return false
	}
	return true
}

func safeJoin(root, relative string) (string, error) {
	relative = filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe path")
	}
	path := filepath.Join(root, relative)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe path")
	}
	return path, nil
}

func resolveWithin(root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes root through a symlink")
	}
	return resolved, nil
}

func serveBytes(w http.ResponseWriter, r *http.Request, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Vary", "Accept-Encoding")
	if acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		if r.Method == http.MethodHead {
			return
		}
		writer := gzip.NewWriter(w)
		_, _ = writer.Write(data)
		_ = writer.Close()
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func acceptsEncoding(header, wanted string) bool {
	for _, entry := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ";")
		if parts[0] != wanted && parts[0] != "*" {
			continue
		}
		allowed := true
		for _, parameter := range parts[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				quality, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64)
				if err == nil && quality <= 0 {
					allowed = false
				}
			}
		}
		if allowed {
			return true
		}
	}
	return false
}

func isCompressible(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "json") || strings.Contains(contentType, "svg") || contentType == tiledelivery.ScalarMediaType
}

func contentTypeForPath(path string) string {
	if filepath.Ext(path) == ".nws" {
		return tiledelivery.ScalarMediaType
	}
	if filepath.Ext(path) == ".nwc" {
		return era5climateproduct.PointMediaType
	}
	if filepath.Ext(path) == ".nwd" {
		return era5climateproduct.OverrideMediaType
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any, cache string) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed", "The response could not be encoded.")
		return
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	serveBytes(w, r, append(data, '\n'), "application/json; charset=utf-8")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	value := apiError{}
	value.Error.Code, value.Error.Message = code, message
	data, _ := json.Marshal(value)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, string(data)+"\n")
}
