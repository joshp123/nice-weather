package delivery

import (
	"fmt"
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

func TestManifestRejectsBrokenContracts(t *testing.T) {
	cases := map[string]func(*Manifest){
		"version":    func(m *Manifest) { m.Version++ },
		"release ID": func(m *Manifest) { m.ReleaseID = "../escape" },
		"product contract invalid": func(m *Manifest) {
			m.ProductContract = "Nice Weather"
		},
		"product contract mismatch": func(m *Manifest) {
			m.ProductContract = "nice-weather-v7"
		},
		"data status":   func(m *Manifest) { m.DataStatus = "preview" },
		"timestamp":     func(m *Manifest) { m.GeneratedAt = "today" },
		"profile":       func(m *Manifest) { m.Profiles[0].ID = "Nice Weather" },
		"legend gap":    func(m *Manifest) { m.Legend[2].Min++ },
		"legend color":  func(m *Manifest) { m.Legend[0].Color = "orange" },
		"palette order": func(m *Manifest) { m.Palette[1].Score = 0 },
		"source time":   func(m *Manifest) { m.Forecast.Days[0].Source.ValidTo = "2026-07-18T07:00:00Z" },
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
		"date":                    func(m *Manifest) { m.Forecast.Days[0].Date = "2026-02-30" },
		"kind":                    func(m *Manifest) { m.Forecast.Days[0].Kind = "maybe" },
		"resolution":              func(m *Manifest) { m.Forecast.Days[0].NativeResolutionKM = 0 },
		"checksum":                func(m *Manifest) { m.Forecast.Days[0].GridSHA256 = "abc" },
		"path escape":             func(m *Manifest) { m.Forecast.Days[0].GridURL = "../secret" },
		"missing profile image":   func(m *Manifest) { delete(m.Forecast.Days[0].Images, "cycling") },
		"missing lens image":      func(m *Manifest) { delete(m.Forecast.Days[0].Lenses, LensComfort) },
		"unexpected lens image":   func(m *Manifest) { m.Forecast.Days[0].Lenses["humidity"] = m.Forecast.Days[0].Lenses[LensComfort] },
		"missing forecast window": func(m *Manifest) { m.Forecast.Window = ForecastWindow{} },
		"window day count":        func(m *Manifest) { m.Forecast.Window.DayCount++ },
		"window date":             func(m *Manifest) { m.Forecast.Window.EndDate = "2026-07-19" },
		"window resolution":       func(m *Manifest) { m.Forecast.Window.GridResolutionKM = 28 },
		"window profile":          func(m *Manifest) { delete(m.Forecast.Window.Profiles, ProfileCycling) },
		"window grid checksum": func(m *Manifest) {
			asset := m.Forecast.Window.Profiles[ProfileNice]
			asset.GridSHA256 = "bad"
			m.Forecast.Window.Profiles[ProfileNice] = asset
		},
		"window image geometry": func(m *Manifest) {
			asset := m.Forecast.Window.Profiles[ProfileNice]
			asset.Image.Height = 0
			m.Forecast.Window.Profiles[ProfileNice] = asset
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
		manifest.Forecast.Days = append(manifest.Forecast.Days, day)
	}
	manifest.Forecast.Window.DayCount = 14
	manifest.Forecast.Window.EndDate = manifest.Forecast.Days[13].Date
	climate := first.Period
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
		GridURL: "grids/day.nwg", GridSHA256: checksum,
	}
	climate := period
	climate.ID, climate.Label, climate.Source.ProviderLabel = "07", "July", "Typical weather"
	return Manifest{
		Version: ManifestVersion, ReleaseID: "test-release", ProductContract: ProductContract, DataStatus: DataStatusLive, GeneratedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Region:   Region{ID: "europe", Label: "Europe", Bounds: GridBounds{West: -25, East: 45, South: 30, North: 72}},
		Profiles: []Profile{{ID: ProfileNice, Label: "Nice"}, {ID: ProfileCycling, Label: "Cycling"}},
		Legend:   CanonicalLegend(),
		Palette:  CanonicalPalette(),
		Forecast: ForecastCatalog{
			UpdatedAt: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC).Format(time.RFC3339), StaleAfterHours: 18,
			Window: ForecastWindow{
				ID: "next-14-days", Label: "Next 14 days", StartDate: "2026-07-18", EndDate: "2026-07-18", DayCount: 1, GridResolutionKM: 7,
				Profiles: map[string]ForecastRangeAsset{
					ProfileNice:    {GridURL: "grids/range-nice.nwr", GridSHA256: strings.Repeat("3", 64), Image: ImageAsset{URL: "images/range-nice.png", SHA256: strings.Repeat("4", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator}},
					ProfileCycling: {GridURL: "grids/range-cycling.nwr", GridSHA256: strings.Repeat("5", 64), Image: ImageAsset{URL: "images/range-cycling.png", SHA256: strings.Repeat("6", 64), Width: 1, Height: 1, Projection: ImageProjectionWebMercator}},
				},
			},
			Days: []ForecastPeriod{{Period: period, Date: "2026-07-18", Kind: "detailed"}},
		},
		Climate: &ClimateCatalog{Label: "Typical year", ReferencePeriod: "1991–2020", Months: []ClimatePeriod{{Period: climate, Month: 7}}},
	}
}
