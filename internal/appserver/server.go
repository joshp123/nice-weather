package appserver

import (
	"compress/gzip"
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
	"nice-weather-finder/internal/weather"
)

type Config struct {
	WebRoot      string
	ArtifactRoot string
	Listen       string
}

type application struct {
	webRoot      string
	artifactRoot string
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
	periods     map[string]periodReader
	catalog     *delivery.ReleaseCatalog
}

type periodReader struct {
	period delivery.Period
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type pointScore struct {
	Value      int             `json:"value"`
	Category   string          `json:"category"`
	Components pointComponents `json:"components"`
}

type pointComponents struct {
	Warmth  int `json:"warmth"`
	Dryness int `json:"dryness"`
	Sun     int `json:"sun"`
	Wind    int `json:"wind"`
	Comfort int `json:"comfort"`
}

type pointWeather struct {
	TemperatureC float64  `json:"temperatureC"`
	MinTempC     float64  `json:"minTempC"`
	MaxTempC     float64  `json:"maxTempC"`
	RainMM       float64  `json:"rainMm"`
	WetTime      *float64 `json:"wetTimeFraction,omitempty"`
	SolarMJm2    *float64 `json:"solarEnergyMJm2,omitempty"`
	CloudPercent *float64 `json:"cloudPct,omitempty"`
	WindKPH      float64  `json:"windKmh"`
	GustKPH      *float64 `json:"gustKmh,omitempty"`
	Humidity     *float64 `json:"humidityPct,omitempty"`
	DewPointC    *float64 `json:"dewPointC,omitempty"`
}

type pointSource struct {
	Basis                   string  `json:"basis"`
	DetailLabel             string  `json:"detailLabel"`
	ProviderLabel           string  `json:"providerLabel"`
	ResolutionKM            float64 `json:"resolutionKm"`
	TemporalResolutionHours int     `json:"temporalResolutionHours"`
	ArtifactGeneratedAt     string  `json:"artifactGeneratedAt"`
	RunAt                   string  `json:"runAt,omitempty"`
	ValidFrom               string  `json:"validFrom,omitempty"`
	ValidTo                 string  `json:"validTo,omitempty"`
	ReferencePeriod         string  `json:"referencePeriod,omitempty"`
}

func Serve(config Config) error {
	if config.Listen == "" {
		config.Listen = "127.0.0.1:5173"
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
	snapshot, err := loadCurrentSnapshot(config.ArtifactRoot)
	if err != nil {
		return nil, err
	}
	previous, err := loadPreviousAssets(config.ArtifactRoot, snapshot.releaseRoot)
	if err != nil {
		_ = snapshot.catalog.Close()
		return nil, err
	}
	return &application{webRoot: webRoot, artifactRoot: config.ArtifactRoot, snapshot: snapshot, previous: previous}, nil
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
	catalog, err := delivery.OpenProductionReleaseCatalog(releaseRoot)
	if err != nil {
		return nil, fmt.Errorf("validate current release: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = catalog.Close()
		}
	}()
	manifest := catalog.Manifest()
	manifestRaw, err := os.ReadFile(filepath.Join(releaseRoot, "manifest.json"))
	if err != nil {
		return nil, err
	}
	snapshot := &releaseSnapshot{
		releaseRoot: releaseRoot, manifest: manifest, manifestRaw: manifestRaw,
		periods: make(map[string]periodReader), catalog: catalog,
	}
	all := make([]delivery.Period, 0, len(manifest.Forecast.Days)+12)
	for _, day := range manifest.Forecast.Days {
		all = append(all, day.Period)
	}
	if manifest.Climate != nil {
		for _, month := range manifest.Climate.Months {
			all = append(all, month.Period)
		}
	}
	for _, period := range all {
		snapshot.periods[period.ID] = periodReader{period: period}
	}
	success = true
	return snapshot, nil
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
	case r.URL.Path == "/api/point":
		app.servePoint(w, r, snapshot)
	case r.URL.Path == "/api/search":
		app.serveSearch(w, r)
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
		_ = next.catalog.Close()
		return err
	}
	app.snapshotMu.Lock()
	previous := app.snapshot
	app.snapshot = next
	app.previous = previousAssets
	app.snapshotMu.Unlock()
	return previous.catalog.Close()
}

func (app *application) close() error {
	app.reloadMu.Lock()
	defer app.reloadMu.Unlock()
	app.snapshotMu.Lock()
	defer app.snapshotMu.Unlock()
	return app.snapshot.catalog.Close()
}

func (app *application) servePoint(w http.ResponseWriter, r *http.Request, snapshot *releaseSnapshot) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	if r.URL.Query().Get("release") != snapshot.manifest.ReleaseID {
		writeError(w, http.StatusConflict, "stale_release", "Weather data changed. Refresh the forecast and try again.")
		return
	}
	periodID := strings.TrimSpace(r.URL.Query().Get("period"))
	reader, ok := snapshot.periods[periodID]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_period", "Choose a period from the manifest.")
		return
	}
	profileID := r.URL.Query().Get("profile")
	profile := weather.ProfileNice
	if profileID == "cycling" {
		profile = weather.ProfileCycling
	} else if profileID != "nice" {
		writeError(w, http.StatusBadRequest, "unknown_profile", "Profile must be nice or cycling.")
		return
	}
	lon, lonErr := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	lat, latErr := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	if lonErr != nil || latErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_location", "Latitude and longitude must be numbers.")
		return
	}
	record, valid, err := snapshot.catalog.Lookup(periodID, lon, lat)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", "Weather data could not be read.")
		return
	}
	if !valid {
		writeError(w, http.StatusNotFound, "no_data", "No weather data is available at this point.")
		return
	}
	m, err := record.WeatherMetrics(reader.period.NativeResolutionKM)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decode_failed", "Weather data could not be decoded.")
		return
	}
	result := weather.Score(m, profile)
	response := struct {
		ReleaseID   string             `json:"releaseId"`
		PeriodID    string             `json:"periodId"`
		PeriodLabel string             `json:"periodLabel"`
		Profile     string             `json:"profile"`
		Location    map[string]float64 `json:"location"`
		Summary     string             `json:"summary"`
		Score       pointScore         `json:"score"`
		Weather     pointWeather       `json:"weather"`
		Source      pointSource        `json:"source"`
	}{
		ReleaseID: snapshot.manifest.ReleaseID, PeriodID: periodID, PeriodLabel: reader.period.Label, Profile: profileID,
		Location: map[string]float64{"lat": lat, "lon": lon}, Summary: weather.Summary(m),
		Score: pointScore{Value: result.Score, Category: result.Rating, Components: pointComponents{
			Warmth: result.Components.Warmth, Dryness: result.Components.Dryness, Sun: result.Components.Sun,
			Wind: result.Components.Wind, Comfort: result.Components.Comfort,
		}},
		Weather: pointWeatherResponse(m), Source: snapshot.pointSource(periodID, reader.period),
	}
	writeJSON(w, r, http.StatusOK, response, "no-store")
}

func (snapshot *releaseSnapshot) pointSource(periodID string, period delivery.Period) pointSource {
	result := pointSource{
		ProviderLabel: period.Source.ProviderLabel, ResolutionKM: period.NativeResolutionKM,
		TemporalResolutionHours: period.TemporalResolutionHours, ArtifactGeneratedAt: snapshot.manifest.GeneratedAt,
	}
	for _, day := range snapshot.manifest.Forecast.Days {
		if day.ID == periodID {
			result.Basis = "forecast"
			result.RunAt, result.ValidFrom, result.ValidTo = period.Source.RunAt, period.Source.ValidFrom, period.Source.ValidTo
			if day.Kind == "detailed" {
				result.DetailLabel = "Detailed forecast"
				return result
			}
			result.DetailLabel = "Outlook"
			return result
		}
	}
	result.Basis, result.DetailLabel = "climate", "Typical weather"
	if snapshot.manifest.Climate != nil {
		result.ReferencePeriod = snapshot.manifest.Climate.ReferencePeriod
	}
	return result
}

func pointWeatherResponse(m weather.DailyMetrics) pointWeather {
	optional := func(value weather.OptionalFloat) *float64 {
		if !value.Available {
			return nil
		}
		result := value.Value
		return &result
	}
	return pointWeather{
		TemperatureC: m.ComfortableTempC, MinTempC: m.MinTempC, MaxTempC: m.MaxTempC,
		RainMM: m.RainMM, WetTime: optional(m.WetTimeFraction), SolarMJm2: optional(m.SolarEnergyMJm2),
		CloudPercent: optional(m.CloudCoverPercent), WindKPH: m.WindSpeedKPH, GustKPH: optional(m.GustKPH),
		Humidity: optional(m.RelativeHumidityPercent), DewPointC: optional(m.DewPointC),
	}
}

func (app *application) serveSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	query := r.URL.Query().Get("q")
	if len([]rune(query)) > 80 {
		writeError(w, http.StatusBadRequest, "query_too_long", "Search terms must be 80 characters or fewer.")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		Results []place `json:"results"`
	}{Results: searchPlaces(query, 8)}, "public, max-age=300")
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
	if releaseID, assetPath, matched, err := releaseAssetPath(requestPath); matched {
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
		if root == app.webRoot && filepath.Ext(requestPath) == "" {
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
	if len(parts) != 2 || !validReleaseID(parts[0]) || !strings.HasPrefix(parts[1], "images/") {
		return "", "", true, errors.New("unsafe release asset path")
	}
	if _, joinErr := safeJoin(".", parts[1]); joinErr != nil {
		return "", "", true, joinErr
	}
	return parts[0], parts[1], true, nil
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
