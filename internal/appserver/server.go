package appserver

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nice-weather-finder/internal/delivery"
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
	reloadMu     sync.Mutex
	snapshotMu   sync.RWMutex
	snapshot     *releaseSnapshot
	previous     *releaseAssets
}

type releaseAssets struct {
	releaseID   string
	releaseRoot string
}

type releaseSnapshot struct {
	releaseRoot string
	manifest    delivery.Manifest
	manifestRaw []byte
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func Serve(config Config) error {
	if config.Listen == "" {
		config.Listen = "127.0.0.1:5198"
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
	snapshot, err := loadCurrentSnapshot(artifactRoot)
	if err != nil {
		return nil, err
	}
	previous, err := loadPreviousAssets(artifactRoot, snapshot.releaseRoot)
	if err != nil {
		return nil, err
	}
	artifactFS, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact root: %w", err)
	}
	return &application{webRoot: webRoot, artifactRoot: artifactRoot, artifactFS: artifactFS, snapshot: snapshot, previous: previous}, nil
}

func loadPreviousAssets(artifactRoot, currentRoot string) (*releaseAssets, error) {
	previous, ok := delivery.PreviousDir(artifactRoot)
	if !ok {
		return nil, nil
	}
	releaseRoot, err := filepath.EvalSymlinks(previous)
	if err != nil {
		return nil, fmt.Errorf("resolve previous release: %w", err)
	}
	if releaseRoot == currentRoot {
		return nil, nil
	}
	manifest, err := delivery.ValidateProductionRelease(releaseRoot)
	if err != nil {
		return nil, fmt.Errorf("validate previous release: %w", err)
	}
	return &releaseAssets{releaseID: manifest.ReleaseID, releaseRoot: releaseRoot}, nil
}

func loadCurrentSnapshot(artifactRoot string) (*releaseSnapshot, error) {
	current, ok := delivery.CurrentDir(artifactRoot)
	if !ok {
		return nil, fmt.Errorf("no published release in %s", artifactRoot)
	}
	releaseRoot, err := filepath.EvalSymlinks(current)
	if err != nil {
		return nil, fmt.Errorf("resolve current release: %w", err)
	}
	manifest, err := delivery.ValidateProductionRelease(releaseRoot)
	if err != nil {
		return nil, fmt.Errorf("validate current release: %w", err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(releaseRoot, "manifest.json"))
	if err != nil {
		return nil, err
	}
	return &releaseSnapshot{releaseRoot: releaseRoot, manifest: manifest, manifestRaw: manifestRaw}, nil
}

func (app *application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = app.reloadIfChanged()
	app.snapshotMu.RLock()
	defer app.snapshotMu.RUnlock()
	snapshot := app.snapshot
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	switch {
	case r.URL.Path == "/manifest.json":
		w.Header().Set("Cache-Control", "no-cache")
		serveBytes(w, r, snapshot.manifestRaw, "application/json; charset=utf-8")
	default:
		app.serveFile(w, r, snapshot)
	}
}

func (app *application) reloadIfChanged() error {
	current, ok := delivery.CurrentDir(app.artifactRoot)
	if !ok {
		return errors.New("published release is missing")
	}
	releaseRoot, err := filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	app.snapshotMu.RLock()
	unchanged := app.snapshot.releaseRoot == releaseRoot
	app.snapshotMu.RUnlock()
	if unchanged {
		return nil
	}
	app.reloadMu.Lock()
	defer app.reloadMu.Unlock()
	app.snapshotMu.RLock()
	unchanged = app.snapshot.releaseRoot == releaseRoot
	app.snapshotMu.RUnlock()
	if unchanged {
		return nil
	}
	next, err := loadCurrentSnapshot(app.artifactRoot)
	if err != nil {
		return err
	}
	previousAssets, err := loadPreviousAssets(app.artifactRoot, next.releaseRoot)
	if err != nil {
		return err
	}
	app.snapshotMu.Lock()
	app.snapshot = next
	app.previous = previousAssets
	app.snapshotMu.Unlock()
	return nil
}

func (app *application) close() error {
	app.reloadMu.Lock()
	defer app.reloadMu.Unlock()
	app.snapshotMu.Lock()
	defer app.snapshotMu.Unlock()
	return app.artifactFS.Close()
}

func (app *application) serveFile(w http.ResponseWriter, r *http.Request, snapshot *releaseSnapshot) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or HEAD for files.")
		return
	}
	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	if requestPath == "" {
		requestPath = "index.html"
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
	} else if releaseID, assetPath, matched, err := releaseAssetPath(requestPath); matched {
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_path", "The requested path is invalid.")
			return
		}
		root = app.releaseRoot(snapshot, releaseID)
		if root == "" {
			writeError(w, http.StatusConflict, "stale_release", "This weather release is no longer available. Refresh the forecast.")
			return
		}
		requestPath, cache = assetPath, "public, max-age=31536000, immutable"
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
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
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

func (app *application) serveStaticArtifact(w http.ResponseWriter, r *http.Request, requestPath, cache string) {
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
	contentType := mime.TypeByExtension(filepath.Ext(requestPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", contentType)
	modTime := info.ModTime()
	if requestPath == "places/current.json" || requestPath == "best-weather/current.json" {
		payload, err := io.ReadAll(io.LimitReader(file, 1025))
		if err != nil || len(payload) > 1024 {
			writeError(w, http.StatusInternalServerError, "read_failed", "The place index pointer could not be read.")
			return
		}
		digest := sha256.Sum256(payload)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", "The place index pointer could not be read.")
			return
		}
		// The pointer can rotate more than once within a filesystem timestamp
		// tick. Its content-derived ETag is the sole safe cache validator.
		modTime = time.Time{}
	}
	if r.Header.Get("Range") == "" && acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip") {
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

func (app *application) releaseRoot(current *releaseSnapshot, releaseID string) string {
	if releaseID == current.manifest.ReleaseID {
		return current.releaseRoot
	}
	if app.previous != nil && releaseID == app.previous.releaseID {
		return app.previous.releaseRoot
	}
	return ""
}

func releaseAssetPath(requestPath string) (releaseID, assetPath string, matched bool, err error) {
	if !strings.HasPrefix(requestPath, "releases/") {
		return "", "", false, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(requestPath, "releases/"), "/", 2)
	if len(parts) != 2 || !validReleaseID(parts[0]) || (!strings.HasPrefix(parts[1], "images/") && !strings.HasPrefix(parts[1], "wind/") && !strings.HasPrefix(parts[1], "points/")) {
		return "", "", true, errors.New("unsafe release asset path")
	}
	if _, joinErr := safeJoin(".", parts[1]); joinErr != nil {
		return "", "", true, joinErr
	}
	return parts[0], parts[1], true, nil
}

func staticArtifactPath(requestPath string) (assetPath string, immutable, matched bool, err error) {
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
	return strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "json") || strings.Contains(contentType, "svg")
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
