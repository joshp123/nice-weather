package outlook

import "image/color"

// PosterStyle holds the poster's visual choices in 1200 × 630 coordinates.
// The score colours are src/palette.ts, composited over the site's dark land colour.
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
	Background: color.NRGBA{14, 17, 22, 255}, Sea: color.NRGBA{14, 17, 22, 255},
	Land: color.NRGBA{54, 59, 67, 255}, Text: color.NRGBA{246, 247, 249, 255},
	Muted: color.NRGBA{168, 178, 190, 255}, Border: color.NRGBA{246, 247, 249, 110},
	State: color.NRGBA{246, 247, 249, 65}, Accent: color.NRGBA{245, 197, 66, 255},
	Bands: [6]struct {
		Name    string
		Minimum int
		Colour  color.NRGBA
	}{
		{"Awful", 0, color.NRGBA{74, 18, 34, 153}},
		{"Poor", 25, color.NRGBA{156, 64, 64, 179}},
		{"Fair", 40, color.NRGBA{111, 123, 138, 153}},
		{"Nice", 55, color.NRGBA{59, 158, 219, 220}},
		{"Beautiful", 70, color.NRGBA{85, 194, 122, 220}},
		{"Perfect", 85, color.NRGBA{245, 197, 66, 230}},
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
