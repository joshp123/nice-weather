package era5climateproduct

import (
	"sort"

	"nice-weather-finder/internal/climate"
)

const (
	CatalogVersion    = 1
	LocalDayAuthority = "IANA civil local daytime 10:00–20:00, exact valid dates, 2006–2025"
	LandAuthority     = "Natural Earth 5.1.1 source-cell-footprint intersection"
)

type Geometry struct {
	Projection            string  `json:"projection"`
	West                  float64 `json:"west"`
	East                  float64 `json:"east"`
	South                 float64 `json:"south"`
	North                 float64 `json:"north"`
	Rows                  int     `json:"rows"`
	Columns               int     `json:"columns"`
	CellResolutionDegrees float64 `json:"cellResolutionDegrees"`
	CellRegistration      string  `json:"cellRegistration"`
}

type Source struct {
	Dataset               string  `json:"dataset"`
	Kind                  string  `json:"kind"`
	ReferenceFirstYear    int     `json:"referenceFirstYear"`
	ReferenceLastYear     int     `json:"referenceLastYear"`
	GridDegrees           float64 `json:"gridDegrees"`
	LocalDay              string  `json:"localDay"`
	LandAuthority         string  `json:"landAuthority"`
	LandSourceSHA256      string  `json:"landSourceSha256"`
	PlanSHA256            string  `json:"planSha256"`
	LogicalCellCount      int     `json:"logicalCellCount"`
	DatelineCellCount     int     `json:"datelineCellCount"`
	DisplayMaskAuthority  string  `json:"displayMaskAuthority"`
	DisplayMaskDerivation string  `json:"displayMaskDerivation"`
	DisplayMaskSHA256     string  `json:"displayMaskSha256"`
	DisplayMaskPath       string  `json:"displayMaskPath"`
	DisplayMaskBytes      uint64  `json:"displayMaskBytes"`
}

type DatelineSemantics struct {
	Longitude           float64 `json:"longitude"`
	Meaning             string  `json:"meaning"`
	CanonicalColumns    int     `json:"canonicalColumns"`
	OverrideRecordBytes int     `json:"overrideRecordBytes"`
}

type ProbabilitySemantics struct {
	Unit             string  `json:"unit"`
	Denominator      string  `json:"denominator"`
	Threshold        string  `json:"threshold"`
	GoodMinimumScore uint8   `json:"goodMinimumScore"`
	NoDataValue      uint8   `json:"noDataValue"`
	Fields           []Field `json:"fields"`
}

type ScalarSemantics struct {
	MaximumZoomMeaning string `json:"maximumZoomMeaning"`
	ParentAggregation  string `json:"parentAggregation"`
}

// Catalog describes one source-honest ERA5 month release. The canonical
// 1440-column grid remains separate from the independent +180° civil days.
type Catalog struct {
	Version           int                  `json:"version"`
	ReleaseID         string               `json:"releaseId"`
	Period            string               `json:"period"`
	Geometry          Geometry             `json:"geometry"`
	Source            Source               `json:"source"`
	Dateline          DatelineSemantics    `json:"positiveDateline"`
	Probability       ProbabilitySemantics `json:"probability"`
	ScalarSemantics   ScalarSemantics      `json:"scalarSemantics"`
	MaximumZoom       uint8                `json:"maximumZoom"`
	PointRecordBytes  int                  `json:"pointRecordBytes"`
	PointBlocks       []PointBlock         `json:"pointBlocks"`
	DatelineOverrides []DatelineBlock      `json:"datelineOverrides"`
	ScalarTiles       []ScalarTile         `json:"scalarTiles"`
}

func (builder *Builder) catalog(pointBlocks []PointBlock, overrides []DatelineBlock, scalarTiles []ScalarTile) Catalog {
	window := climate.DefaultReferenceWindow()
	datelineCells := 0
	for _, expected := range builder.expected {
		if expected.override {
			datelineCells++
		}
	}
	sort.Slice(pointBlocks, func(i, j int) bool {
		if pointBlocks[i].LatitudeBlock != pointBlocks[j].LatitudeBlock {
			return pointBlocks[i].LatitudeBlock < pointBlocks[j].LatitudeBlock
		}
		return pointBlocks[i].LongitudeBlock < pointBlocks[j].LongitudeBlock
	})
	sort.Slice(overrides, func(i, j int) bool {
		return overrides[i].LatitudeBlock < overrides[j].LatitudeBlock
	})
	sort.Slice(scalarTiles, func(i, j int) bool {
		left, right := scalarTiles[i], scalarTiles[j]
		if fieldRank(left.Field) != fieldRank(right.Field) {
			return fieldRank(left.Field) < fieldRank(right.Field)
		}
		if left.Zoom != right.Zoom {
			return left.Zoom < right.Zoom
		}
		if left.Y != right.Y {
			return left.Y < right.Y
		}
		return left.X < right.X
	})
	return Catalog{
		Version:   CatalogVersion,
		ReleaseID: builder.config.ReleaseID,
		Period:    builder.period,
		Geometry: Geometry{
			Projection: "EPSG:4326", West: -180, East: 180, South: -90, North: 90,
			Rows: Rows, Columns: Columns, CellResolutionDegrees: GridDegrees,
			CellRegistration: "exact-era5-grid-point-centres",
		},
		Source: Source{
			Dataset: "ERA5", Kind: "hourly-reanalysis-reduced-to-iana-civil-local-days",
			ReferenceFirstYear: window.FirstYear, ReferenceLastYear: window.LastYear,
			GridDegrees:           GridDegrees,
			LocalDay:              LocalDayAuthority,
			LandAuthority:         LandAuthority,
			LandSourceSHA256:      builder.displayMask.landSourceSHA256,
			PlanSHA256:            builder.planSHA256,
			LogicalCellCount:      len(builder.expected),
			DatelineCellCount:     datelineCells,
			DisplayMaskAuthority:  DisplayMaskAuthority,
			DisplayMaskDerivation: builder.displayMask.derivation,
			DisplayMaskSHA256:     builder.displayMask.sha256,
			DisplayMaskPath:       "display-mask.bin",
			DisplayMaskBytes:      uint64(len(builder.displayMask.values)),
		},
		Dateline: DatelineSemantics{
			Longitude:           180,
			Meaning:             "independent +180 degree civil-day records; never alias to -180 degree records",
			CanonicalColumns:    Columns,
			OverrideRecordBytes: 64,
		},
		Probability: ProbabilitySemantics{
			Unit:             "whole-percent",
			Denominator:      "complete-real-days-in-reference-month",
			Threshold:        "canonical-rating-good-or-better",
			GoodMinimumScore: 55,
			NoDataValue:      255,
			Fields:           sortedFields(),
		},
		ScalarSemantics: ScalarSemantics{
			MaximumZoomMeaning: "exact evaluated point value sampled only where the ERA5 grid-point centre is land",
			ParentAggregation:  "rounded arithmetic mean of available four child texels; no-data excluded",
		},
		MaximumZoom:       builder.config.MaximumZoom,
		PointRecordBytes:  64,
		PointBlocks:       pointBlocks,
		DatelineOverrides: overrides,
		ScalarTiles:       scalarTiles,
	}
}
