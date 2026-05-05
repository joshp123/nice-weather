package artifact

import "encoding/json"

type Manifest struct {
	Version     int      `json:"version"`
	GeneratedAt string   `json:"generatedAt"`
	Grid        Grid     `json:"grid"`
	Sources     Sources  `json:"sources"`
	Layers      []Layer  `json:"layers"`
	Anchors     []Anchor `json:"anchors"`
	Legend      Legend   `json:"legend"`
}

type Grid struct {
	CRS          string  `json:"crs"`
	CellDeg      float64 `json:"cellDeg"`
	Region       string  `json:"region"`
	TileMaxZoom  int     `json:"tileMaxZoom"`
	TileRenderer int     `json:"tileRenderer"`
}

type Sources struct {
	Forecast    ForecastSource     `json:"forecast"`
	Climatology *ClimatologySource `json:"climatology,omitempty"`
}

type ForecastSource struct {
	Name               string            `json:"name"`
	Run                string            `json:"run"`
	RunStartedAt       string            `json:"runStartedAt,omitempty"`
	ResolutionKMApprox float64           `json:"resolutionKmApprox"`
	Aggregation        string            `json:"aggregation"`
	StaleAfterHours    int               `json:"staleAfterHours,omitempty"`
	Horizons           []string          `json:"horizons"`
	HorizonDetails     []ForecastHorizon `json:"horizonDetails,omitempty"`
}

type ForecastHorizon struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	LocalDate   string `json:"localDate"`
	WindowStart string `json:"windowStart"`
	WindowEnd   string `json:"windowEnd"`
}

type ClimatologySource struct {
	Name                  string   `json:"name"`
	Period                string   `json:"period"`
	SourceResolution      string   `json:"sourceResolution"`
	RenderedResolutionDeg float64  `json:"renderedResolutionDeg"`
	Horizons              []string `json:"horizons"`
}

type Layer struct {
	Basis   string `json:"basis"`
	Profile string `json:"profile"`
	Horizon string `json:"horizon"`
	TileURL string `json:"tileUrl"`
}

type Legend struct {
	Min        int              `json:"min"`
	Max        int              `json:"max"`
	Categories []LegendCategory `json:"categories"`
}

type LegendCategory struct {
	Label string `json:"label"`
	Color string `json:"color"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
}

type Anchor struct {
	Name     string                   `json:"name"`
	Lon      float64                  `json:"lon"`
	Lat      float64                  `json:"lat"`
	Horizons map[string]AnchorHorizon `json:"horizons"`
}

type AnchorHorizon struct {
	Input  AnchorInput            `json:"input"`
	Scores map[string]AnchorScore `json:"scores"`
}

type AnchorInput struct {
	TempC    float64 `json:"tempC"`
	Humidity float64 `json:"humidity"`
	RainMM   float64 `json:"rainMm"`
	CloudPct float64 `json:"cloudPct"`
	WindKMH  float64 `json:"windKmh"`
}

type AnchorScore struct {
	Score    int     `json:"score"`
	Category string  `json:"category"`
	Color    string  `json:"color"`
	Temp     float64 `json:"temp"`
	Rain     float64 `json:"rain"`
	Cloud    float64 `json:"cloud"`
	Wind     float64 `json:"wind"`
	Humidity float64 `json:"humidity"`
	Why      string  `json:"why"`
}

func (manifest Manifest) JSON() ([]byte, error) {
	return json.MarshalIndent(manifest, "", "  ")
}
