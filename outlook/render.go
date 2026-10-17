package outlook

import (
	"bytes"
	"embed"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	resample "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

//go:embed assets/*.bin assets/*.ttf
var assets embed.FS

type coordinate struct{ longitude, latitude float64 }
type posterRenderer struct {
	land, borders, states [][]coordinate
	regular, bold         *opentype.Font
	faces                 map[string]font.Face
}

func newPosterRenderer() (*posterRenderer, error) {
	renderer := &posterRenderer{faces: map[string]font.Face{}}
	for name, target := range map[string]*[][]coordinate{"land.bin": &renderer.land, "borders.bin": &renderer.borders, "states.bin": &renderer.states} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return nil, err
		}
		*target, err = readPaths(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	for name, target := range map[string]**opentype.Font{Style.RegularFont: &renderer.regular, Style.BoldFont: &renderer.bold} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return nil, err
		}
		*target, err = opentype.Parse(body)
		if err != nil {
			return nil, err
		}
	}
	return renderer, nil
}

func readPaths(body []byte) ([][]coordinate, error) {
	reader := bytes.NewReader(body)
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	paths := make([][]coordinate, count)
	for index := range paths {
		var points uint32
		if err := binary.Read(reader, binary.LittleEndian, &points); err != nil {
			return nil, err
		}
		paths[index] = make([]coordinate, points)
		for point := range paths[index] {
			var pair [2]int16
			if err := binary.Read(reader, binary.LittleEndian, &pair); err != nil {
				return nil, err
			}
			paths[index][point] = coordinate{float64(pair[0]) / 100, float64(pair[1]) / 100}
		}
	}
	return paths, nil
}

func (renderer *posterRenderer) face(size float64, bold bool) font.Face {
	key := fmt.Sprintf("%g/%t", size, bold)
	if face, ok := renderer.faces[key]; ok {
		return face
	}
	parsed := renderer.regular
	if bold {
		parsed = renderer.bold
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size * float64(Style.RenderScale), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err)
	}
	renderer.faces[key] = face
	return face
}

func (renderer *posterRenderer) text(canvas *image.NRGBA, text string, x, baseline, size float64, bold bool, ink color.NRGBA) {
	drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(ink), Face: renderer.face(size, bold), Dot: fixed.Point26_6{X: fixed.Int26_6(x * float64(Style.RenderScale) * 64), Y: fixed.Int26_6(baseline * float64(Style.RenderScale) * 64)}}
	drawer.DrawString(text)
}

func (renderer *posterRenderer) fittedText(canvas *image.NRGBA, text string, x, baseline, size, maxWidth float64, bold bool, ink color.NRGBA) {
	width := float64(font.MeasureString(renderer.face(size, bold), text)) / 64 / float64(Style.RenderScale)
	if width > maxWidth {
		size *= maxWidth / width
	}
	renderer.text(canvas, text, x, baseline, size, bold, ink)
}

func (renderer *posterRenderer) write(directory string, outlook Outlook, scores *image.Gray) error {
	canvas := image.NewNRGBA(image.Rect(0, 0, Style.Width*Style.RenderScale, Style.Height*Style.RenderScale))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(Style.Background), image.Point{}, draw.Src)
	renderer.text(canvas, "Nice!", float64(Style.Margin), Layout.BrandBaseline, Style.BrandSize, Style.BrandBold, Style.Text)
	renderer.text(canvas, strings.ToUpper(outlook.Title)+" / ONE-DAY FORECAST", Layout.HeadingX, Layout.EyebrowBaseline, Style.KeySize, Style.EyebrowBold, Style.Accent)
	renderer.text(canvas, "The nicest weather tomorrow", Layout.HeadingX, Layout.HeadingBaseline, Style.HeadingSize, Style.HeadingBold, Style.Text)
	date, _ := time.Parse("2006-01-02", outlook.Date)
	renderer.text(canvas, "Forecast for "+date.Format("Monday 2 January 2006"), Layout.HeadingX, Layout.DateBaseline, Style.DateSize, Style.DateBold, Style.Muted)
	renderer.line(canvas, float64(Style.Margin), float64(Style.HeaderBottom), float64(Style.Width-Style.Margin), float64(Style.HeaderBottom), Style.BorderWidth, Style.Border)
	for _, viewport := range mapViews(outlook.Region) {
		renderer.drawMap(canvas, viewport, scores, outlook.Places)
	}
	columnWidth := float64(Style.Width-Style.Margin*2) / 3
	for index, place := range outlook.Places {
		x, y := float64(Style.Margin)+float64(index)*columnWidth, float64(Style.PlacesTop)
		renderer.text(canvas, fmt.Sprintf("0%d", index+1), x, y+Layout.PlaceNameOffset, Style.DetailSize, Style.NumberBold, Style.Accent)
		renderer.fittedText(canvas, place.Name, x+Layout.PlaceNumberWidth, y+Layout.PlaceNameOffset, Style.PlaceSize, columnWidth-Layout.PlaceTextInset, Style.PlaceBold, Style.Text)
		renderer.fittedText(canvas, place.Region, x+Layout.PlaceNumberWidth, y+Layout.PlaceRegionOffset, Style.DetailSize, columnWidth-Layout.PlaceTextInset, Style.RegionBold, Style.Muted)
		band := Style.Bands[bandIndex(place.Score)]
		renderer.text(canvas, fmt.Sprintf("%d  %s", place.Score, band.Name), x+Layout.PlaceNumberWidth, y+Layout.PlaceScoreOffset, Style.DetailSize, Style.ScoreBold, Style.Text)
	}
	for index, band := range Style.Bands {
		x := float64(Style.Margin) + float64(index)*Layout.KeyStep
		renderer.rectangle(canvas, x, float64(Style.KeyTop)-Layout.KeySwatchHeight, Layout.KeySwatchWidth, Layout.KeySwatchHeight, scoreColour(band.Minimum))
		renderer.text(canvas, band.Name, x+Layout.KeyLabelInset, float64(Style.KeyTop), Style.KeySize, Style.KeyBold, Style.Muted)
	}
	renderer.text(canvas, "nice.jjpcodes.com", Layout.AddressX, float64(Style.KeyTop), Style.KeySize, Style.AddressBold, Style.Text)
	preview := image.NewNRGBA(image.Rect(0, 0, Style.Width, Style.Height))
	resample.CatmullRom.Scale(preview, preview.Bounds(), canvas, canvas.Bounds(), draw.Src, nil)
	for name, picture := range map[string]image.Image{outlook.Region + ".png": preview, outlook.Region + "-full.png": canvas} {
		var encoded bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&encoded, picture); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, name), encoded.Bytes(), 0644); err != nil {
			return err
		}
	}
	return nil
}

type mapViewport struct {
	west, east, south, north float64
	x, y, width, height      float64
	label                    string
	eastEdgeFade             float64
}

func (viewport mapViewport) fitted() mapViewport {
	scale := min(viewport.width/(viewport.east-viewport.west), viewport.height/(viewport.north-viewport.south))
	width, height := scale*(viewport.east-viewport.west), scale*(viewport.north-viewport.south)
	viewport.x += (viewport.width - width) / 2
	viewport.y += (viewport.height - height) / 2
	viewport.width, viewport.height = width, height
	return viewport
}

func (viewport mapViewport) project(point coordinate) (float64, float64) {
	return viewport.x + (point.longitude-viewport.west)/(viewport.east-viewport.west)*viewport.width, viewport.y + (viewport.north-point.latitude)/(viewport.north-viewport.south)*viewport.height
}

func (renderer *posterRenderer) drawMap(canvas *image.NRGBA, view mapViewport, scores *image.Gray, places []Place) {
	view = view.fitted()
	scale := float64(Style.RenderScale)
	width, height := int(math.Ceil(view.width*scale)), int(math.Ceil(view.height*scale))
	renderer.rectangle(canvas, view.x, view.y, view.width, view.height, Style.Sea)
	landMask := image.NewAlpha(image.Rect(0, 0, width, height))
	raster := vector.NewRasterizer(width, height)
	for _, path := range renderer.land {
		for i, point := range path {
			x, y := view.project(point)
			x, y = (x-view.x)*scale, (y-view.y)*scale
			if i == 0 {
				raster.MoveTo(float32(x), float32(y))
			} else {
				raster.LineTo(float32(x), float32(y))
			}
		}
		raster.ClosePath()
	}
	raster.Draw(landMask, landMask.Bounds(), image.Opaque, image.Point{})
	paint := image.NewNRGBA(landMask.Bounds())
	for y := range height {
		latitude := view.north - (float64(y)+.5)/float64(height)*(view.north-view.south)
		for x := range width {
			longitude := view.west + (float64(x)+.5)/float64(width)*(view.east-view.west)
			score := smoothScore(scores, latitude, longitude)
			ink := Style.Land
			if score != 255 {
				ink = scoreColour(score)
			}
			paint.SetNRGBA(x, y, ink)
		}
	}
	left, top := int(view.x*scale), int(view.y*scale)
	draw.DrawMask(canvas, image.Rect(left, top, left+width, top+height), paint, image.Point{}, landMask, image.Point{}, draw.Over)
	clip := canvas.SubImage(image.Rect(left, top, left+width, top+height).Intersect(canvas.Bounds())).(*image.NRGBA)
	for _, set := range []struct {
		paths  [][]coordinate
		width  float64
		colour color.NRGBA
	}{{renderer.borders, Style.BorderWidth, Style.Border}, {renderer.states, Style.StateWidth, Style.State}} {
		if set.width == Style.StateWidth && view.east > -60 {
			continue
		}
		for _, path := range set.paths {
			for index := 1; index < len(path); index++ {
				a, b := path[index-1], path[index]
				if math.Abs(a.longitude-b.longitude) > 180 {
					continue
				}
				if max(a.longitude, b.longitude) < view.west || min(a.longitude, b.longitude) > view.east || max(a.latitude, b.latitude) < view.south || min(a.latitude, b.latitude) > view.north {
					continue
				}
				x1, y1 := view.project(a)
				x2, y2 := view.project(b)
				renderer.line(clip, x1, y1, x2, y2, set.width, set.colour)
			}
		}
	}
	if view.eastEdgeFade > 0 {
		fadeWidth := view.eastEdgeFade * scale
		for x := max(left, left+width-int(fadeWidth)); x < left+width; x++ {
			ink := Style.Sea
			ink.A = uint8(255 * (1 - float64(left+width-1-x)/fadeWidth))
			for y := top; y < top+height; y++ {
				blend(canvas, x, y, ink)
			}
		}
	}
	if view.label != "" {
		renderer.text(canvas, view.label, view.x, view.y+Style.DetailSize, Style.DetailSize, Style.InsetBold, Style.Muted)
	}
	for index, place := range places {
		if place.Longitude < view.west || place.Longitude > view.east || place.Latitude < view.south || place.Latitude > view.north {
			continue
		}
		x, y := view.project(coordinate{place.Longitude, place.Latitude})
		renderer.circle(canvas, x, y, Style.MarkerRadius+Style.MarkerRing, Style.Text)
		renderer.circle(canvas, x, y, Style.MarkerRadius, Style.Background)
		label := strconv.Itoa(index + 1)
		textWidth := float64(font.MeasureString(renderer.face(Style.MarkerSize, Style.MarkerBold), label)) / 64 / scale
		renderer.text(canvas, label, x-textWidth/2, y+Style.MarkerSize*Layout.MarkerBaselineFraction, Style.MarkerSize, Style.MarkerBold, Style.Text)
	}
}

// The app also draws its point grid through a cubic B-spline, omitting sea.
// Ranking always uses the actual selected point, before this display smoothing.
func smoothScore(scores *image.Gray, latitude, longitude float64) int {
	x, y := (longitude+180)*4, (90-latitude)*4
	column, row := int(math.Floor(x)), int(math.Floor(y))
	weights := func(fraction float64) [4]float64 {
		squared, cubed := fraction*fraction, fraction*fraction*fraction
		return [4]float64{math.Pow(1-fraction, 3) / 6, (3*cubed - 6*squared + 4) / 6, (-3*cubed + 3*squared + 3*fraction + 1) / 6, cubed / 6}
	}
	wx, wy := weights(x-float64(column)), weights(y-float64(row))
	total, weight := 0.0, 0.0
	for dr := range 4 {
		r := row + dr - 1
		if r < 0 || r >= gridHeight {
			continue
		}
		for dc := range 4 {
			c := ((column+dc-1)%gridWidth + gridWidth) % gridWidth
			score := scores.GrayAt(c, r).Y
			if score == 255 {
				continue
			}
			w := wx[dc] * wy[dr]
			total += float64(score) * w
			weight += w
		}
	}
	if weight == 0 {
		return 255
	}
	return int(total / weight)
}

func (renderer *posterRenderer) rectangle(canvas *image.NRGBA, x, y, width, height float64, ink color.NRGBA) {
	scale := float64(Style.RenderScale)
	draw.Draw(canvas, image.Rect(int(x*scale), int(y*scale), int((x+width)*scale), int((y+height)*scale)), image.NewUniform(ink), image.Point{}, draw.Over)
}

func (renderer *posterRenderer) circle(canvas *image.NRGBA, x, y, radius float64, ink color.NRGBA) {
	scale := float64(Style.RenderScale)
	for row := int((y - radius) * scale); row <= int((y+radius)*scale); row++ {
		for column := int((x - radius) * scale); column <= int((x+radius)*scale); column++ {
			if math.Pow(float64(column)/scale-x, 2)+math.Pow(float64(row)/scale-y, 2) <= radius*radius {
				blend(canvas, column, row, ink)
			}
		}
	}
}

func (renderer *posterRenderer) line(canvas *image.NRGBA, x1, y1, x2, y2, width float64, ink color.NRGBA) {
	scale := float64(Style.RenderScale)
	steps := int(math.Ceil(max(math.Abs(x2-x1), math.Abs(y2-y1)) * scale))
	for step := 0; step <= steps; step++ {
		fraction := float64(step) / float64(max(1, steps))
		renderer.circle(canvas, x1+(x2-x1)*fraction, y1+(y2-y1)*fraction, width/2, ink)
	}
}

func blend(canvas *image.NRGBA, x, y int, ink color.NRGBA) {
	if !image.Pt(x, y).In(canvas.Bounds()) {
		return
	}
	background := canvas.NRGBAAt(x, y)
	a := uint32(ink.A)
	canvas.SetNRGBA(x, y, color.NRGBA{uint8((uint32(ink.R)*a + uint32(background.R)*(255-a)) / 255), uint8((uint32(ink.G)*a + uint32(background.G)*(255-a)) / 255), uint8((uint32(ink.B)*a + uint32(background.B)*(255-a)) / 255), 255})
}
