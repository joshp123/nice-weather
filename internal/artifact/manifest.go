package artifact

import "encoding/json"

type Manifest struct {
	Version     int     `json:"version"`
	GeneratedAt string  `json:"generatedAt"`
	Grid        Grid    `json:"grid"`
	Sources     Sources `json:"sources"`
	Layers      []Layer `json:"layers"`
	Legend      Legend  `json:"legend"`
}

type Grid struct {
	CRS     string  `json:"crs"`
	CellDeg float64 `json:"cellDeg"`
	Region  string  `json:"region"`
}

type Sources struct {
	Forecast    ForecastSource     `json:"forecast"`
	Climatology *ClimatologySource `json:"climatology,omitempty"`
}

type ForecastSource struct {
	Name               string   `json:"name"`
	Run                string   `json:"run"`
	ResolutionKMApprox float64  `json:"resolutionKmApprox"`
	Horizons           []string `json:"horizons"`
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

func (manifest Manifest) JSON() ([]byte, error) {
	return json.MarshalIndent(manifest, "", "  ")
}
