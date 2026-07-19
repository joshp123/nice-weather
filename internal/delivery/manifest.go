package delivery

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const ManifestVersion = 7

// ProductContract identifies the complete weather-product semantics represented
// by a release. Bump it whenever scoring, aggregation, climate, or grid semantics
// change so incompatible releases cannot be reused as if they meant the same thing.
const ProductContract = "nice-weather-v15"

const ImageProjectionWebMercator = "web-mercator"

const webMercatorMaxLatitude = 85.0511287798066

type DataStatus string

const (
	DataStatusLive   DataStatus = "live"
	DataStatusSample DataStatus = "sample"
)

const (
	ProfileNice    = "nice"
	ProfileCycling = "cycling"
)

var (
	safeIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

type Manifest struct {
	Version         int              `json:"version"`
	ReleaseID       string           `json:"releaseId"`
	ProductContract string           `json:"productContract"`
	DataStatus      DataStatus       `json:"dataStatus"`
	GeneratedAt     string           `json:"generatedAt"`
	Region          Region           `json:"region"`
	Profiles        []Profile        `json:"profiles"`
	Legend          []LegendCategory `json:"legend"`
	Palette         []PaletteStop    `json:"palette"`
	Forecast        ForecastCatalog  `json:"forecast"`
	Climate         *ClimateCatalog  `json:"climate,omitempty"`
}

type Region struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Bounds are the geographic edges of every period grid and overlay image.
	// Grids are regular EPSG:4326; ImageAsset.Projection declares how image rows
	// are spaced between the north and south edges.
	Bounds GridBounds `json:"bounds"`
}

type Profile struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type LegendCategory struct {
	Label string `json:"label"`
	Color string `json:"color"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
}

// PaletteStop describes rendering colour independently of semantic rating
// bands. Adjacent bands may therefore have a deliberate visible boundary.
type PaletteStop struct {
	Score int    `json:"score"`
	Color string `json:"color"`
}

type ForecastCatalog struct {
	UpdatedAt       string           `json:"updatedAt"`
	StaleAfterHours int              `json:"staleAfterHours"`
	Window          ForecastWindow   `json:"window"`
	Days            []ForecastPeriod `json:"days"`
}

// ForecastWindow is the canonical judgement across every promised forecast
// day. Its grid uses the coarsest forecast grid. Detailed days preserve the
// centre-containing finer cell when valid, otherwise the nearest valid finer
// cell owned by the same coarse cell; the outlook is never upsampled.
type ForecastWindow struct {
	ID               string                        `json:"id"`
	Label            string                        `json:"label"`
	StartDate        string                        `json:"startDate"`
	EndDate          string                        `json:"endDate"`
	DayCount         int                           `json:"dayCount"`
	GridResolutionKM float64                       `json:"gridResolutionKm"`
	Point            PointAsset                    `json:"point"`
	Profiles         map[string]ForecastRangeAsset `json:"profiles"`
	Lenses           map[string]ImageAsset         `json:"lenses"`
}

type ForecastRangeAsset struct {
	Image ImageAsset `json:"image"`
}

type ClimateCatalog struct {
	Label           string          `json:"label"`
	ReferencePeriod string          `json:"referencePeriod"`
	Months          []ClimatePeriod `json:"months"`
}

type ForecastPeriod struct {
	Period
	Date string `json:"date"`
	Kind string `json:"kind"`
}

type ClimatePeriod struct {
	Period
	Month int `json:"month"`
}

type Period struct {
	ID                      string                `json:"id"`
	Label                   string                `json:"label"`
	Source                  Source                `json:"source"`
	NativeResolutionKM      float64               `json:"nativeResolutionKm"`
	TemporalResolutionHours int                   `json:"temporalResolutionHours"`
	Images                  map[string]ImageAsset `json:"images"`
	Lenses                  map[string]ImageAsset `json:"lenses"`
	Point                   PointAsset            `json:"point"`
	WindFrames              []WindFrameAsset      `json:"windFrames,omitempty"`
}

// WindFrameAsset is one immutable instantaneous u/v field. Its binary format
// fixes component units and quantisation; the manifest owns time and identity.
type WindFrameAsset struct {
	ValidAt           string `json:"validAt"`
	URL               string `json:"url"`
	SHA256            string `json:"sha256"`
	ClippedComponents uint32 `json:"clippedComponents"`
}

const (
	LensComfort = "comfort"
	LensDry     = "dry"
	LensBright  = "bright"
)

func RequiredLensAssets() []string {
	return []string{LensComfort, LensDry, LensBright, "wind-" + ProfileNice, "wind-" + ProfileCycling}
}

type Source struct {
	ProviderID    string `json:"providerId"`
	ProviderLabel string `json:"providerLabel"`
	RunAt         string `json:"runAt"`
	ValidFrom     string `json:"validFrom"`
	ValidTo       string `json:"validTo"`
}

type ImageAsset struct {
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Projection string `json:"projection"`
}

// WebMercatorImageDimensions preserves the geographic grid's longitude
// columns and chooses enough rows for approximately square projected pixels.
// It is the shared geometry contract for writers and release validation.
func WebMercatorImageDimensions(spec GridSpec) (width, height int, err error) {
	if spec.Rows == 0 || spec.Cols == 0 || spec.Bounds.South < -webMercatorMaxLatitude || spec.Bounds.North > webMercatorMaxLatitude || spec.Bounds.South >= spec.Bounds.North || spec.Bounds.West >= spec.Bounds.East {
		return 0, 0, errors.New("overlay grid has invalid Web Mercator bounds or dimensions")
	}
	longitudeSpan := (spec.Bounds.East - spec.Bounds.West) * math.Pi / 180
	northRadians := spec.Bounds.North * math.Pi / 180
	southRadians := spec.Bounds.South * math.Pi / 180
	projectedHeight := math.Asinh(math.Tan(northRadians)) - math.Asinh(math.Tan(southRadians))
	width = int(spec.Cols)
	height = int(math.Round(float64(width) * projectedHeight / longitudeSpan))
	if height < 1 {
		height = 1
	}
	return width, height, nil
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, errors.New("manifest contains trailing JSON")
		}
		return Manifest{}, fmt.Errorf("decode trailing manifest data: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("manifest version must be %d", ManifestVersion)
	}
	if !releaseNamePattern.MatchString(manifest.ReleaseID) {
		return errors.New("manifest releaseId must be a safe release name")
	}
	if !safeIDPattern.MatchString(manifest.ProductContract) {
		return errors.New("manifest productContract must be a safe nonempty ID")
	}
	if manifest.ProductContract != ProductContract {
		return fmt.Errorf("manifest productContract must be %q", ProductContract)
	}
	if manifest.DataStatus != DataStatusLive && manifest.DataStatus != DataStatusSample {
		return errors.New("manifest dataStatus must be live or sample")
	}
	if _, err := time.Parse(time.RFC3339, manifest.GeneratedAt); err != nil {
		return errors.New("manifest generatedAt must be RFC3339")
	}
	if manifest.Region.ID == "" || manifest.Region.Label == "" {
		return errors.New("manifest region is incomplete")
	}
	if err := validateGridSpec(GridSpec{Bounds: manifest.Region.Bounds, Rows: 1, Cols: 1}); err != nil {
		return fmt.Errorf("manifest region: %w", err)
	}
	profiles := make(map[string]struct{}, len(manifest.Profiles))
	for _, profile := range manifest.Profiles {
		if !safeIDPattern.MatchString(profile.ID) || strings.TrimSpace(profile.Label) == "" {
			return fmt.Errorf("invalid profile %+v", profile)
		}
		if _, exists := profiles[profile.ID]; exists {
			return fmt.Errorf("duplicate profile %q", profile.ID)
		}
		profiles[profile.ID] = struct{}{}
	}
	if len(profiles) == 0 {
		return errors.New("manifest needs at least one profile")
	}
	if err := validateLegend(manifest.Legend); err != nil {
		return err
	}
	if err := validatePalette(manifest.Palette); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, manifest.Forecast.UpdatedAt); err != nil {
		return errors.New("forecast updatedAt must be RFC3339")
	}
	if manifest.Forecast.StaleAfterHours <= 0 || len(manifest.Forecast.Days) == 0 {
		return errors.New("forecast freshness and days are required")
	}
	seen := make(map[string]struct{})
	seenDates := make(map[string]struct{})
	seenWindPaths := make(map[string]string)
	for _, day := range manifest.Forecast.Days {
		if !datePattern.MatchString(day.Date) || !validDate(day.Date) || (day.Kind != "detailed" && day.Kind != "outlook") {
			return fmt.Errorf("invalid forecast day %q", day.ID)
		}
		if err := validatePeriod(day.Period, profiles, seen, manifest.ReleaseID, PointKindForecastDay); err != nil {
			return fmt.Errorf("forecast day %q: %w", day.ID, err)
		}
		if err := validateWindFrames(day.Period, seenWindPaths); err != nil {
			return fmt.Errorf("forecast day %q: %w", day.ID, err)
		}
		if _, exists := seenDates[day.Date]; exists {
			return fmt.Errorf("duplicate forecast date %q", day.Date)
		}
		seenDates[day.Date] = struct{}{}
	}
	if err := validateForecastWindow(manifest.Forecast.Window, manifest.Forecast.Days, profiles, seen, manifest.ReleaseID); err != nil {
		return fmt.Errorf("forecast window: %w", err)
	}
	if manifest.Climate != nil {
		if strings.TrimSpace(manifest.Climate.Label) == "" || strings.TrimSpace(manifest.Climate.ReferencePeriod) == "" || len(manifest.Climate.Months) == 0 {
			return errors.New("climate label, reference period and months are required")
		}
		seenMonths := make(map[int]struct{}, len(manifest.Climate.Months))
		for _, month := range manifest.Climate.Months {
			if month.Month < 1 || month.Month > 12 {
				return fmt.Errorf("invalid climate month %d", month.Month)
			}
			if _, exists := seenMonths[month.Month]; exists {
				return fmt.Errorf("duplicate climate month %d", month.Month)
			}
			seenMonths[month.Month] = struct{}{}
			if err := validatePeriod(month.Period, profiles, seen, manifest.ReleaseID, PointKindLegacyClimateMonth); err != nil {
				return fmt.Errorf("climate month %q: %w", month.ID, err)
			}
			if len(month.WindFrames) != 0 {
				return fmt.Errorf("climate month %q must not contain forecast wind frames", month.ID)
			}
		}
	}
	return nil
}

func validateForecastWindow(window ForecastWindow, days []ForecastPeriod, profiles, seen map[string]struct{}, releaseID string) error {
	if !safeIDPattern.MatchString(window.ID) || strings.TrimSpace(window.Label) == "" {
		return errors.New("identity and label are required")
	}
	if _, exists := seen[window.ID]; exists {
		return fmt.Errorf("duplicate period ID %q", window.ID)
	}
	seen[window.ID] = struct{}{}
	if len(days) != ForecastPointDayCount || window.DayCount != ForecastPointDayCount || window.StartDate != days[0].Date || window.EndDate != days[len(days)-1].Date {
		return errors.New("dates and day count must match the complete forecast catalog")
	}
	if !finite(window.GridResolutionKM) || window.GridResolutionKM <= 0 {
		return errors.New("grid resolution must be positive")
	}
	coarsest := 0.0
	for _, day := range days {
		if day.NativeResolutionKM > coarsest {
			coarsest = day.NativeResolutionKM
		}
	}
	if window.GridResolutionKM != coarsest {
		return errors.New("grid resolution must match the coarsest forecast day")
	}
	if err := ValidatePointAssetDescriptor(window.Point, releaseID, PointKindForecastWindow); err != nil {
		return fmt.Errorf("point asset: %w", err)
	}
	if window.Point.Geometry.Bounds != days[0].Point.Geometry.Bounds {
		return errors.New("point asset bounds must match forecast periods")
	}
	if len(window.Profiles) != len(profiles) {
		return errors.New("must contain one range grid and image per profile")
	}
	paths := make(map[string]string, len(profiles)+len(RequiredLensAssets()))
	for profile := range profiles {
		asset, ok := window.Profiles[profile]
		if !ok {
			return fmt.Errorf("missing profile %q", profile)
		}
		if err := validateImageAsset(asset.Image); err != nil {
			return fmt.Errorf("profile %q image: %w", profile, err)
		}
		if asset.Image.Width != int(window.Point.Geometry.Cols) || asset.Image.Height != int(window.Point.Geometry.Rows) {
			return fmt.Errorf("profile %q image geometry does not match point asset", profile)
		}
		if owner, exists := paths[asset.Image.URL]; exists {
			return fmt.Errorf("profile %q image path duplicates %s", profile, owner)
		}
		paths[asset.Image.URL] = profile + " image"
	}
	requiredLenses := RequiredLensAssets()
	if len(window.Lenses) != len(requiredLenses) {
		return errors.New("must contain every component lens image")
	}
	for _, lens := range requiredLenses {
		asset, exists := window.Lenses[lens]
		if !exists {
			return fmt.Errorf("missing component lens %q", lens)
		}
		if err := validateImageAsset(asset); err != nil {
			return fmt.Errorf("component lens %q: %w", lens, err)
		}
		if asset.Width != int(window.Point.Geometry.Cols) || asset.Height != int(window.Point.Geometry.Rows) {
			return fmt.Errorf("component lens %q geometry does not match point asset", lens)
		}
		if owner, exists := paths[asset.URL]; exists {
			return fmt.Errorf("component lens %q image path duplicates %s", lens, owner)
		}
		paths[asset.URL] = lens + " lens"
	}
	return nil
}

func validateLegend(legend []LegendCategory) error {
	if len(legend) != 6 {
		return errors.New("manifest legend must contain six categories")
	}
	wantMin := 0
	for _, category := range legend {
		if strings.TrimSpace(category.Label) == "" || !hexColorPattern.MatchString(category.Color) {
			return errors.New("manifest legend labels and six-digit hex colors are required")
		}
		if category.Min != wantMin || category.Max < category.Min || category.Max > 100 {
			return errors.New("manifest legend must cover 0–100 in order without gaps or overlaps")
		}
		wantMin = category.Max + 1
	}
	if wantMin != 101 {
		return errors.New("manifest legend must cover 0–100 in order without gaps or overlaps")
	}
	return nil
}

func validatePalette(palette []PaletteStop) error {
	if len(palette) < 2 || palette[0].Score != 0 || palette[len(palette)-1].Score != 100 {
		return errors.New("manifest palette must run from 0 to 100")
	}
	previous := -1
	for _, stop := range palette {
		if stop.Score <= previous || stop.Score < 0 || stop.Score > 100 || !hexColorPattern.MatchString(stop.Color) {
			return errors.New("manifest palette stops must have ordered scores and six-digit hex colors")
		}
		previous = stop.Score
	}
	return nil
}

func validDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func validatePeriod(period Period, profiles map[string]struct{}, seen map[string]struct{}, releaseID string, pointKind PointKind) error {
	if period.ID == "" || period.Label == "" {
		return errors.New("period identity and provider are required")
	}
	if err := validateSource(period.Source); err != nil {
		return err
	}
	if _, exists := seen[period.ID]; exists {
		return fmt.Errorf("duplicate period ID %q", period.ID)
	}
	seen[period.ID] = struct{}{}
	if !finite(period.NativeResolutionKM) || period.NativeResolutionKM <= 0 || period.TemporalResolutionHours <= 0 {
		return errors.New("native and temporal resolution must be positive")
	}
	if err := ValidatePointAssetDescriptor(period.Point, releaseID, pointKind); err != nil {
		return fmt.Errorf("point asset: %w", err)
	}
	if len(period.Images) != len(profiles) {
		return errors.New("period must have one image per profile")
	}
	for profile := range profiles {
		asset, exists := period.Images[profile]
		if !exists {
			return fmt.Errorf("period is missing image for profile %q", profile)
		}
		if err := validateAssetPath(asset.URL); err != nil {
			return fmt.Errorf("image for profile %q: %w", profile, err)
		}
		if !validSHA256(asset.SHA256) || asset.Width <= 0 || asset.Height <= 0 {
			return fmt.Errorf("image for profile %q needs SHA-256 and positive dimensions", profile)
		}
		if asset.Projection != ImageProjectionWebMercator {
			return fmt.Errorf("image for profile %q projection must be %q", profile, ImageProjectionWebMercator)
		}
		if asset.Width != int(period.Point.Geometry.Cols) || asset.Height != int(period.Point.Geometry.Rows) {
			return fmt.Errorf("image for profile %q geometry does not match point asset", profile)
		}
	}
	requiredLenses := RequiredLensAssets()
	if len(period.Lenses) != len(requiredLenses) {
		return errors.New("period must have every component lens image")
	}
	for _, lens := range requiredLenses {
		asset, exists := period.Lenses[lens]
		if !exists {
			return fmt.Errorf("period is missing component lens %q", lens)
		}
		if err := validateImageAsset(asset); err != nil {
			return fmt.Errorf("component lens %q: %w", lens, err)
		}
		if asset.Width != int(period.Point.Geometry.Cols) || asset.Height != int(period.Point.Geometry.Rows) {
			return fmt.Errorf("component lens %q geometry does not match point asset", lens)
		}
	}
	return nil
}

func validateWindFrames(period Period, seenPaths map[string]string) error {
	if len(period.WindFrames) < 2 {
		return errors.New("forecast period needs at least two wind frames")
	}
	validFrom, _ := time.Parse(time.RFC3339, period.Source.ValidFrom)
	validTo, _ := time.Parse(time.RFC3339, period.Source.ValidTo)
	var previous time.Time
	for index, asset := range period.WindFrames {
		validAt, err := time.Parse(time.RFC3339, asset.ValidAt)
		if err != nil {
			return fmt.Errorf("wind frame %d validAt must be RFC3339", index)
		}
		if validAt.Before(validFrom) || validAt.After(validTo) {
			return fmt.Errorf("wind frame %d is outside the period validity window", index)
		}
		if index > 0 && !validAt.After(previous) {
			return fmt.Errorf("wind frame %d must follow the previous source timestamp", index)
		}
		if err := validateAssetPath(asset.URL); err != nil || !strings.HasPrefix(asset.URL, "wind/") || !strings.HasSuffix(asset.URL, ".nww.gz") {
			return fmt.Errorf("wind frame %d URL must be a clean wind asset path", index)
		}
		if !validSHA256(asset.SHA256) {
			return fmt.Errorf("wind frame %d needs SHA-256", index)
		}
		if !strings.Contains(asset.URL, strings.ToLower(asset.SHA256)) {
			return fmt.Errorf("wind frame %d URL must contain its SHA-256", index)
		}
		if owner, exists := seenPaths[asset.URL]; exists {
			return fmt.Errorf("wind frame %d duplicates asset path owned by %s", index, owner)
		}
		seenPaths[asset.URL] = period.ID
		previous = validAt
	}
	return nil
}

func validateImageAsset(asset ImageAsset) error {
	if err := validateAssetPath(asset.URL); err != nil {
		return err
	}
	if !validSHA256(asset.SHA256) || asset.Width <= 0 || asset.Height <= 0 {
		return errors.New("image needs SHA-256 and positive dimensions")
	}
	if asset.Projection != ImageProjectionWebMercator {
		return fmt.Errorf("image projection must be %q", ImageProjectionWebMercator)
	}
	return nil
}

func validateSource(source Source) error {
	if !safeIDPattern.MatchString(source.ProviderID) || strings.TrimSpace(source.ProviderLabel) == "" {
		return errors.New("period source provider is invalid")
	}
	runAt, err := time.Parse(time.RFC3339, source.RunAt)
	if err != nil {
		return errors.New("period source runAt must be RFC3339")
	}
	validFrom, err := time.Parse(time.RFC3339, source.ValidFrom)
	if err != nil {
		return errors.New("period source validFrom must be RFC3339")
	}
	validTo, err := time.Parse(time.RFC3339, source.ValidTo)
	if err != nil {
		return errors.New("period source validTo must be RFC3339")
	}
	if !validFrom.Before(validTo) || runAt.After(validFrom) {
		return errors.New("period source times are out of order")
	}
	return nil
}

func validSHA256(value string) bool {
	checksum, err := hex.DecodeString(value)
	return err == nil && len(checksum) == sha256Size
}

func ValidateProductionManifest(manifest Manifest) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	wantProfiles := []Profile{{ID: ProfileNice, Label: "Nice"}, {ID: ProfileCycling, Label: "Cycling"}}
	if len(manifest.Profiles) != len(wantProfiles) {
		return errors.New("production manifest must contain exactly the nice and cycling profiles")
	}
	for index := range wantProfiles {
		if manifest.Profiles[index] != wantProfiles[index] {
			return errors.New("production manifest profiles must be nice then cycling")
		}
	}
	wantLegend := CanonicalLegend()
	for index := range wantLegend {
		if manifest.Legend[index] != wantLegend[index] {
			return errors.New("production manifest must use the canonical score legend")
		}
	}
	wantPalette := CanonicalPalette()
	if len(manifest.Palette) != len(wantPalette) {
		return errors.New("production manifest must use the canonical score palette")
	}
	for index := range wantPalette {
		if manifest.Palette[index] != wantPalette[index] {
			return errors.New("production manifest must use the canonical score palette")
		}
	}
	if len(manifest.Forecast.Days) != 14 {
		return errors.New("production manifest must contain exactly 14 forecast days")
	}
	for index, day := range manifest.Forecast.Days {
		date, _ := time.Parse("2006-01-02", day.Date)
		if index > 0 {
			previous, _ := time.Parse("2006-01-02", manifest.Forecast.Days[index-1].Date)
			if !date.Equal(previous.AddDate(0, 0, 1)) {
				return errors.New("production forecast dates must be consecutive and ordered")
			}
		}
	}
	if manifest.Climate == nil || len(manifest.Climate.Months) != 12 {
		return errors.New("production manifest must contain exactly 12 climate months")
	}
	for index, month := range manifest.Climate.Months {
		if month.Month != index+1 {
			return errors.New("production climate months must be ordered January to December")
		}
	}
	return nil
}

func CanonicalLegend() []LegendCategory {
	return []LegendCategory{
		{Label: "Awful", Color: "#34364F", Min: 0, Max: 24},
		{Label: "Poor", Color: "#555977", Min: 25, Max: 39},
		{Label: "Mixed", Color: "#7D7E94", Min: 40, Max: 54},
		{Label: "Good", Color: "#B9ACA4", Min: 55, Max: 69},
		{Label: "Very good", Color: "#E3B37F", Min: 70, Max: 84},
		{Label: "Excellent", Color: "#F27643", Min: 85, Max: 100},
	}
}

func CanonicalPalette() []PaletteStop {
	return []PaletteStop{
		{Score: 0, Color: "#34364F"}, {Score: 24, Color: "#4E526E"},
		{Score: 25, Color: "#555977"}, {Score: 39, Color: "#696B86"},
		{Score: 40, Color: "#7D7E94"}, {Score: 54, Color: "#98939D"},
		{Score: 55, Color: "#A69DA0"}, {Score: 69, Color: "#CBB198"},
		{Score: 70, Color: "#D1B49A"}, {Score: 84, Color: "#E3B37F"},
		{Score: 85, Color: "#E7AA75"}, {Score: 100, Color: "#F27643"},
	}
}

const sha256Size = 32

func validateAssetPath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || strings.Contains(path, "\\") {
		return errors.New("asset path must be a clean relative path")
	}
	return nil
}

func (manifest Manifest) JSON() ([]byte, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return json.MarshalIndent(manifest, "", "  ")
}
