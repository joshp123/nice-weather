package delivery

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManifestRoundTripAndValidation(t *testing.T) {
	manifest := validManifest(strings.Repeat("a", 64))
	data, err := manifest.JSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ReleaseID != manifest.ReleaseID || parsed.ProductContract != ProductContract || parsed.Forecast.Days[0].NativeResolutionKM != 7 || parsed.Climate.Months[0].Month != 7 {
		t.Fatalf("manifest lost data: %+v", parsed)
	}
}

func TestManifestAcceptsStrictlyIncreasingMixedWindSourceTimes(t *testing.T) {
	manifest := validManifest(strings.Repeat("a", 64))
	start := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	manifest.Forecast.Days[0].WindFrames = nil
	for index, offset := range []int{0, 1, 2, 5, 8, 10} {
		checksum := strings.Repeat(string(rune('1'+index)), 64)
		manifest.Forecast.Days[0].WindFrames = append(manifest.Forecast.Days[0].WindFrames, WindFrameAsset{
			ValidAt: start.Add(time.Duration(offset) * time.Hour).Format(time.RFC3339),
			URL:     "wind/mixed-" + checksum + ".nww.gz", SHA256: checksum,
		})
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("mixed exact source times rejected: %v", err)
	}
}

func TestManifestRejectsBrokenContracts(t *testing.T) {
	cases := map[string]func(*Manifest){
		"version":    func(m *Manifest) { m.Version++ },
		"release ID": func(m *Manifest) { m.ReleaseID = "../escape" },
		"product contract invalid": func(m *Manifest) {
			m.ProductContract = "Nice Weather"
		},
		"product contract v8 mismatch":  func(m *Manifest) { m.ProductContract = "nice-weather-v8" },
		"product contract v9 mismatch":  func(m *Manifest) { m.ProductContract = "nice-weather-v9" },
		"product contract v10 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v10" },
		"product contract v11 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v11" },
		"product contract v12 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v12" },
		"product contract v14 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v14" },
		"product contract v16 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v16" },
		"product contract v18 mismatch": func(m *Manifest) { m.ProductContract = "nice-weather-v18" },
		"data status":                   func(m *Manifest) { m.DataStatus = "preview" },
		"timestamp":                     func(m *Manifest) { m.GeneratedAt = "today" },
		"profile":                       func(m *Manifest) { m.Profiles[0].ID = "Nice Weather" },
		"legend gap":                    func(m *Manifest) { m.Legend[2].Min++ },
		"legend color":                  func(m *Manifest) { m.Legend[0].Color = "orange" },
		"palette order":                 func(m *Manifest) { m.Palette[1].Score = 0 },
		"source time":                   func(m *Manifest) { m.Forecast.Days[0].Source.ValidTo = "2026-07-18T07:00:00Z" },
		"image checksum": func(m *Manifest) {
			asset := m.Forecast.Days[0].Images["nice"]
			asset.SHA256 = "bad"
			m.Forecast.Days[0].Images["nice"] = asset
		},
		"image dimensions": func(m *Manifest) {
			asset := m.Forecast.Days[0].Images["nice"]
			asset.Width = 0
			m.Forecast.Days[0].Images["nice"] = asset
		},
		"image projection": func(m *Manifest) {
			asset := m.Forecast.Days[0].Images["nice"]
			asset.Projection = "epsg-4326"
			m.Forecast.Days[0].Images["nice"] = asset
		},
		"date":                           func(m *Manifest) { m.Forecast.Days[0].Date = "2026-02-30" },
		"kind":                           func(m *Manifest) { m.Forecast.Days[0].Kind = "maybe" },
		"resolution":                     func(m *Manifest) { m.Forecast.Days[0].NativeResolutionKM = 0 },
		"checksum":                       func(m *Manifest) { m.Forecast.Days[0].Point.SHA256 = "abc" },
		"path escape":                    func(m *Manifest) { m.Forecast.Days[0].Point.URL = "../secret" },
		"missing profile image":          func(m *Manifest) { delete(m.Forecast.Days[0].Images, "cycling") },
		"missing lens image":             func(m *Manifest) { delete(m.Forecast.Days[0].Lenses, LensComfort) },
		"unexpected lens image":          func(m *Manifest) { m.Forecast.Days[0].Lenses["humidity"] = m.Forecast.Days[0].Lenses[LensComfort] },
		"missing wind frames":            func(m *Manifest) { m.Forecast.Days[0].WindFrames = nil },
		"missing outlook display scores": func(m *Manifest) { m.Forecast.Days[5].DisplayPoint = nil },
		"unexpected detailed display scores": func(m *Manifest) {
			display := manifestPointAsset(PointKindDisplayScore, strings.Repeat("5", 64))
			m.Forecast.Days[0].DisplayPoint = &display
		},
		"display-score checksum": func(m *Manifest) { m.Forecast.Days[5].DisplayPoint.SHA256 = "bad" },
		"duplicate wind time":    func(m *Manifest) { m.Forecast.Days[0].WindFrames[1].ValidAt = "2026-07-18T08:00:00Z" },
		"out-of-order wind times": func(m *Manifest) {
			m.Forecast.Days[0].WindFrames[0].ValidAt, m.Forecast.Days[0].WindFrames[1].ValidAt = "2026-07-18T09:00:00Z", "2026-07-18T08:00:00Z"
		},
		"wind path":               func(m *Manifest) { m.Forecast.Days[0].WindFrames[0].URL = "images/not-wind.nww" },
		"wind checksum":           func(m *Manifest) { m.Forecast.Days[0].WindFrames[0].SHA256 = "bad" },
		"missing forecast window": func(m *Manifest) { m.Forecast.Window = ForecastWindow{} },
		"window day count":        func(m *Manifest) { m.Forecast.Window.DayCount++ },
		"window date":             func(m *Manifest) { m.Forecast.Window.EndDate = "2026-07-19" },
		"window resolution":       func(m *Manifest) { m.Forecast.Window.GridResolutionKM = 29 },
		"window profile":          func(m *Manifest) { delete(m.Forecast.Window.Profiles, ProfileCycling) },
		"window missing lens":     func(m *Manifest) { delete(m.Forecast.Window.Lenses, LensComfort) },
		"window unexpected lens":  func(m *Manifest) { m.Forecast.Window.Lenses["humidity"] = m.Forecast.Window.Lenses[LensComfort] },
		"window point checksum":   func(m *Manifest) { m.Forecast.Window.Point.SHA256 = "bad" },
		"window image geometry": func(m *Manifest) {
			asset := m.Forecast.Window.Profiles[ProfileNice]
			asset.Image.Height = 0
			m.Forecast.Window.Profiles[ProfileNice] = asset
		},
		"window lens geometry": func(m *Manifest) {
			asset := m.Forecast.Window.Lenses[LensComfort]
			asset.Height = 0
			m.Forecast.Window.Lenses[LensComfort] = asset
		},
		"window duplicate image path": func(m *Manifest) {
			m.Forecast.Window.Lenses[LensComfort] = m.Forecast.Window.Profiles[ProfileNice].Image
		},
		"duplicate period": func(m *Manifest) { m.Climate.Months[0].ID = m.Forecast.Days[0].ID },
		"duplicate date": func(m *Manifest) {
			copy := m.Forecast.Days[0]
			copy.ID = "another-day"
			m.Forecast.Days = append(m.Forecast.Days, copy)
		},
		"duplicate month": func(m *Manifest) {
			copy := m.Climate.Months[0]
			copy.ID = "another-month"
			m.Climate.Months = append(m.Climate.Months, copy)
		},
	}
	for name, breakManifest := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := validManifest(strings.Repeat("a", 64))
			breakManifest(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}

	data := []byte(`{"version":1,"unknown":true}`)
	if _, err := ParseManifest(data); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
	valid, _ := validManifest(strings.Repeat("a", 64)).JSON()
	valid = append(valid, []byte(` {}`)...)
	if _, err := ParseManifest(valid); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func TestCanonicalScoreColoursAreFixed(t *testing.T) {
	wantLegend := []LegendCategory{
		{Label: "Awful", Color: "#22385C", Min: 0, Max: 24},
		{Label: "Poor", Color: "#3A5C78", Min: 25, Max: 39},
		{Label: "Mixed", Color: "#5F7E7F", Min: 40, Max: 54},
		{Label: "Good", Color: "#859D80", Min: 55, Max: 69},
		{Label: "Very good", Color: "#AFB87C", Min: 70, Max: 84},
		{Label: "Excellent", Color: "#E4C46B", Min: 85, Max: 100},
	}
	if got := CanonicalLegend(); !reflect.DeepEqual(got, wantLegend) {
		t.Fatalf("canonical legend = %+v; want %+v", got, wantLegend)
	}
	wantPalette := []PaletteStop{
		{Score: 0, Color: "#22385C"}, {Score: 24, Color: "#365270"},
		{Score: 25, Color: "#3A5C78"}, {Score: 39, Color: "#537484"},
		{Score: 40, Color: "#5F7E7F"}, {Score: 54, Color: "#849082"},
		{Score: 55, Color: "#859D80"}, {Score: 69, Color: "#9DB07F"},
		{Score: 70, Color: "#AFB87C"}, {Score: 84, Color: "#D0C375"},
		{Score: 85, Color: "#E4C46B"}, {Score: 100, Color: "#F6C867"},
	}
	if got := CanonicalPalette(); !reflect.DeepEqual(got, wantPalette) {
		t.Fatalf("canonical palette = %+v; want %+v", got, wantPalette)
	}
}

func TestValidateProductionManifestRequiresCompleteOrderedPeriods(t *testing.T) {
	manifest := validManifest(strings.Repeat("a", 64))
	manifest.Climate = &ClimateCatalog{Label: "Typical year", ReferencePeriod: "1991–2020"}
	first := manifest.Forecast.Days[0]
	manifest.Forecast.Days = nil
	for index := 0; index < 14; index++ {
		day := first
		date := time.Date(2026, 7, 18+index, 0, 0, 0, 0, time.UTC)
		day.ID, day.Date = date.Format("2006-01-02"), date.Format("2006-01-02")
		day.Source.ValidFrom, day.Source.ValidTo = date.Format(time.RFC3339), date.Add(12*time.Hour).Format(time.RFC3339)
		day.WindFrames = testWindFrames(date, 1)
		day.Kind = "detailed"
		day.DisplayPoint = nil
		if index >= 4 {
			day.Kind = "outlook"
			day.NativeResolutionKM = 28
			display := manifestPointAsset(PointKindDisplayScore, strings.Repeat("5", 64))
			day.DisplayPoint = &display
		}
		manifest.Forecast.Days = append(manifest.Forecast.Days, day)
	}
	manifest.Forecast.Window.DayCount = 14
	manifest.Forecast.Window.EndDate = manifest.Forecast.Days[13].Date
	climate := first.Period
	climate.WindFrames = nil
	climate.Point = manifestPointAsset(PointKindLegacyClimateMonth, strings.Repeat("9", 64))
	for month := 1; month <= 12; month++ {
		period := climate
		period.ID = fmt.Sprintf("m%02d", month)
		manifest.Climate.Months = append(manifest.Climate.Months, ClimatePeriod{Period: period, Month: month})
	}
	if err := ValidateProductionManifest(manifest); err != nil {
		t.Fatal(err)
	}
	wrongProfile := manifest
	wrongProfile.Profiles = append([]Profile(nil), manifest.Profiles...)
	wrongProfile.Profiles[0].ID = "general"
	if err := ValidateProductionManifest(wrongProfile); err == nil {
		t.Fatal("non-canonical production profiles were accepted")
	}
	wrongLegend := manifest
	wrongLegend.Legend = append([]LegendCategory(nil), manifest.Legend...)
	wrongLegend.Legend[0].Color = "#000000"
	if err := ValidateProductionManifest(wrongLegend); err == nil {
		t.Fatal("non-canonical production legend was accepted")
	}
	manifest.Forecast.Days[5], manifest.Forecast.Days[6] = manifest.Forecast.Days[6], manifest.Forecast.Days[5]
	if err := ValidateProductionManifest(manifest); err == nil {
		t.Fatal("unordered production forecast was accepted")
	}
}

func TestLiveProductionManifestRequiresCanonicalDenseGeometry(t *testing.T) {
	manifest := validManifest(strings.Repeat("a", 64))
	manifest.DataStatus = DataStatusLive
	if err := ValidateProductionManifest(manifest); err == nil || !strings.Contains(err.Error(), "canonical dense score geometry") {
		t.Fatalf("noncanonical live dense geometry accepted: %v", err)
	}
}

func validManifest(checksum string) Manifest {
	period := Period{
		ID: "2026-07-18", Label: "Sat 18",
		Source:             Source{ProviderID: "dwd", ProviderLabel: "Detailed forecast", RunAt: "2026-07-18T00:00:00Z", ValidFrom: "2026-07-18T08:00:00Z", ValidTo: "2026-07-18T20:00:00Z"},
		NativeResolutionKM: 7, TemporalResolutionHours: 1,
		Images: map[string]ImageAsset{
			"nice":    {URL: "images/nice.png", SHA256: strings.Repeat("b", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
			"cycling": {URL: "images/cycling.png", SHA256: strings.Repeat("c", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
		},
		Lenses: map[string]ImageAsset{
			LensComfort:    {URL: "images/comfort.png", SHA256: strings.Repeat("d", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
			LensDry:        {URL: "images/dry.png", SHA256: strings.Repeat("e", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
			LensBright:     {URL: "images/bright.png", SHA256: strings.Repeat("f", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
			"wind-nice":    {URL: "images/wind-nice.png", SHA256: strings.Repeat("1", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
			"wind-cycling": {URL: "images/wind-cycling.png", SHA256: strings.Repeat("2", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
		},
		Point:      manifestPointAsset(PointKindForecastDay, checksum),
		WindFrames: testWindFrames(time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC), 1),
	}
	climate := period
	climate.ID, climate.Label, climate.Source.ProviderLabel = "07", "July", "Typical weather"
	climate.Point = manifestPointAsset(PointKindLegacyClimateMonth, strings.Repeat("9", 64))
	climate.WindFrames = nil
	days := make([]ForecastPeriod, ForecastPointDayCount)
	for index := range days {
		day := period
		date := time.Date(2026, 7, 18+index, 0, 0, 0, 0, time.UTC)
		day.ID, day.Label = date.Format("2006-01-02"), date.Format("Mon 2")
		day.Source.ValidFrom, day.Source.ValidTo = date.Add(8*time.Hour).Format(time.RFC3339), date.Add(20*time.Hour).Format(time.RFC3339)
		day.WindFrames = testWindFrames(date.Add(8*time.Hour), 1)
		kind := "detailed"
		if index >= 4 {
			kind = "outlook"
			day.NativeResolutionKM = 28
			display := manifestPointAsset(PointKindDisplayScore, strings.Repeat("5", 64))
			day.DisplayPoint = &display
		}
		days[index] = ForecastPeriod{Period: day, Date: date.Format("2006-01-02"), Kind: kind}
	}
	return Manifest{
		Version: ManifestVersion, ReleaseID: "test-release", ProductContract: ProductContract, DataStatus: DataStatusSample, GeneratedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Region:   Region{ID: "europe", Label: "Europe", Bounds: GridBounds{West: -25, East: 45, South: 30, North: 72}},
		Profiles: []Profile{{ID: ProfileNice, Label: "Nice"}, {ID: ProfileCycling, Label: "Cycling"}},
		Legend:   CanonicalLegend(),
		Palette:  CanonicalPalette(),
		Forecast: ForecastCatalog{
			UpdatedAt: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), StaleAfterHours: 36,
			Window: ForecastWindow{
				ID: "next-14-days", Label: "Next 14 days", StartDate: "2026-07-18", EndDate: "2026-07-31", DayCount: ForecastPointDayCount, GridResolutionKM: 28,
				Point: manifestPointAsset(PointKindForecastWindow, strings.Repeat("3", 64)),
				Profiles: map[string]ForecastRangeAsset{
					ProfileNice:    {Image: ImageAsset{URL: "images/range-nice.png", SHA256: strings.Repeat("4", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator}},
					ProfileCycling: {Image: ImageAsset{URL: "images/range-cycling.png", SHA256: strings.Repeat("6", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator}},
				},
				Lenses: map[string]ImageAsset{
					LensComfort:    {URL: "images/range-comfort.png", SHA256: strings.Repeat("a", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
					LensDry:        {URL: "images/range-dry.png", SHA256: strings.Repeat("b", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
					LensBright:     {URL: "images/range-bright.png", SHA256: strings.Repeat("c", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
					"wind-nice":    {URL: "images/range-wind-nice.png", SHA256: strings.Repeat("d", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
					"wind-cycling": {URL: "images/range-wind-cycling.png", SHA256: strings.Repeat("e", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator},
				},
			},
			Days: days,
		},
		Climate: &ClimateCatalog{Label: "Typical year", ReferencePeriod: "1991–2020", Months: []ClimatePeriod{{Period: climate, Month: 7}}},
	}
}

func manifestPointAsset(kind PointKind, checksum string) PointAsset {
	geometry, err := NewPointGeometry(GridSpec{Bounds: GridBounds{West: -25, East: 45, South: 30, North: 72}, Rows: 1, Cols: 1})
	if err != nil {
		panic(err)
	}
	recordBytes, _ := pointRecordBytes(kind)
	return PointAsset{
		URL: "points/test-" + checksum + ".nwp", SHA256: checksum, PayloadSHA256: strings.Repeat("0", 64),
		Bytes: uint64(PointAssetHeaderBytes) + uint64(geometry.Rows)*uint64(geometry.Cols)*uint64(recordBytes),
		Kind:  PointKindName(kind), HeaderBytes: PointAssetHeaderBytes, RecordBytes: recordBytes, Projection: "web-mercator-image", Geometry: geometry,
	}
}

func testWindFrames(start time.Time, cadenceHours int) []WindFrameAsset {
	prefix := start.UTC().Format("20060102-1504")
	firstSHA := strings.Repeat("7", 64)
	secondSHA := strings.Repeat("8", 64)
	return []WindFrameAsset{
		{ValidAt: start.Format(time.RFC3339), URL: "wind/" + prefix + "-" + firstSHA + ".nww.gz", SHA256: firstSHA},
		{ValidAt: start.Add(time.Duration(cadenceHours) * time.Hour).Format(time.RFC3339), URL: "wind/" + prefix + "-" + secondSHA + ".nww.gz", SHA256: secondSHA},
	}
}
