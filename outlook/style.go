package outlook

import (
	"image/color"
	scorebands "nice-weather-finder/hosting/sites"
)

// PosterStyle holds the poster's visual choices in 1200 × 630 coordinates.
// The score colours are the light theme's bands in src/design-tokens.css, composited over its land colour.
type PosterStyle struct {
	RegularFont, BoldFont                                                        string
	Width, Height, RenderScale                                                   int
	Margin, HeaderBottom, MapTop, MapBottom, PlacesTop, KeyTop                   int
	BrandSize, HeadingSize, DateSize, PlaceSize, DetailSize, KeySize, MarkerSize float64
	BrandBold, EyebrowBold, HeadingBold, DateBold, PlaceBold, NumberBold         bool
	RegionBold, ScoreBold, KeyBold, AddressBold, InsetBold, MarkerBold           bool
	EastEdgeFade                                                                 float64
	BorderWidth, StateWidth, MarkerRadius, MarkerRing                            float64
	Background, Sea, Land, Text, Muted, Border, State, Accent                    color.NRGBA
	Bands                                                                        [6]struct {
		Name    string
		Minimum int
		Colour  color.NRGBA
	}
}

var Style = PosterStyle{
	RegularFont: "Barlow-Regular.ttf", BoldFont: "Barlow-Bold.ttf",
	Width: 1200, Height: 630, RenderScale: 2,
	Margin: 40, HeaderBottom: 108, MapTop: 122, MapBottom: 459, PlacesTop: 487, KeyTop: 594,
	BrandSize: 67, HeadingSize: 31, DateSize: 19, PlaceSize: 29, DetailSize: 17, KeySize: 16, MarkerSize: 17,
	BrandBold: true, EyebrowBold: true, HeadingBold: true, PlaceBold: true, NumberBold: true,
	ScoreBold: true, AddressBold: true, InsetBold: true, MarkerBold: true,
	EastEdgeFade: 24,
	BorderWidth:  .8, StateWidth: .55, MarkerRadius: 12, MarkerRing: 2,
	// The site's light paper and ink (src/design-tokens.css), so a poster looks like the map it came from.
	// Colour means weather; the rank numbers and the eyebrow are muted ink.
	Background: color.NRGBA{251, 250, 246, 255}, Sea: color.NRGBA{251, 250, 246, 255},
	Land: color.NRGBA{220, 220, 215, 255}, Text: color.NRGBA{26, 25, 23, 255},
	Muted: color.NRGBA{93, 90, 84, 255}, Border: color.NRGBA{26, 25, 23, 56},
	State: color.NRGBA{26, 25, 23, 20}, Accent: color.NRGBA{93, 90, 84, 255},
	Bands: [6]struct {
		Name    string
		Minimum int
		Colour  color.NRGBA
	}{
		{"Awful", 0, color.NRGBA{123, 45, 63, 128}},
		{"Poor", scorebands.AwfulMaximum, color.NRGBA{194, 91, 79, 153}},
		{"Fair", 40, color.NRGBA{142, 152, 164, 153}},
		{"Nice", 55, color.NRGBA{47, 151, 216, 230}},
		{"Beautiful", 70, color.NRGBA{60, 174, 102, 230}},
		{"Perfect", 85, color.NRGBA{240, 180, 41, 240}},
	},
}

var Layout = struct {
	BrandBaseline, HeadingX, EyebrowBaseline, HeadingBaseline, DateBaseline                   float64
	PlaceNameOffset, PlaceNumberWidth, PlaceTextInset, PlaceRegionOffset, PlaceScoreOffset    float64
	KeyStep, KeySwatchWidth, KeySwatchHeight, KeyLabelInset, AddressX, MarkerBaselineFraction float64
}{75, 240, 30, 66, 94, 24, 34, 60, 48, 71, 140, 18, 12, 26, 1005, .35}

func mapViews(region string) []mapViewport {
	margin, top, width, height := float64(Style.Margin), float64(Style.MapTop), float64(Style.Width-Style.Margin*2), float64(Style.MapBottom-Style.MapTop)
	switch region {
	case "usa":
		return []mapViewport{
			{-126, -66, 24, 50, 285, top, 875, height, "", 0},
			{-180, -129, 50, 72, margin, top, 220, 180, "ALASKA", 0},
			{-161, -154, 18, 23, margin, 330, 220, 110, "HAWAII", 0},
		}
	case "europe":
		return []mapViewport{{-32, 40, 26, 72, margin, top, width, height, "", Style.EastEdgeFade}}
	default:
		return []mapViewport{{-180, 180, -60, 84, margin, top, width, height, "", 0}}
	}
}

func bandIndex(score int) int {
	for index := len(Style.Bands) - 1; index >= 0; index-- {
		if score >= Style.Bands[index].Minimum {
			return index
		}
	}
	return 0
}

func scoreColour(score int) color.NRGBA {
	band := Style.Bands[bandIndex(score)].Colour
	background := Style.Land
	a := uint32(band.A)
	return color.NRGBA{
		uint8((uint32(band.R)*a + uint32(background.R)*(255-a)) / 255),
		uint8((uint32(band.G)*a + uint32(background.G)*(255-a)) / 255),
		uint8((uint32(band.B)*a + uint32(background.B)*(255-a)) / 255), 255,
	}
}
