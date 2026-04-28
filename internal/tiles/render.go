package tiles

import (
	"image"
	"image/color"
	"math"

	"nice-weather-finder-poc/internal/grid"
	"nice-weather-finder-poc/internal/scoring"
	"nice-weather-finder-poc/internal/weatherinput"
)

const TileSize = 256

func RenderWeatherGridTile(inputs weatherinput.Grid, profile scoring.Profile, horizon string, z, x, y int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
	for py := 0; py < TileSize; py++ {
		for px := 0; px < TileSize; px++ {
			lon, lat := lonLatForPixel(z, x, y, px, py)
			if !grid.InEurope(lon, lat) {
				img.SetRGBA(px, py, color.RGBA{})
				continue
			}
			input, ok := inputs.InputAt(horizon, lon, lat)
			if !ok {
				img.SetRGBA(px, py, color.RGBA{})
				continue
			}
			result := scoring.Score(input, profile)
			img.SetRGBA(px, py, ColorForScore(result.Score))
		}
	}
	return img
}

func lonLatForPixel(z, x, y, px, py int) (float64, float64) {
	scale := math.Exp2(float64(z)) * TileSize
	worldX := float64(x*TileSize + px)
	worldY := float64(y*TileSize + py)
	lon := worldX/scale*360 - 180
	n := math.Pi - 2*math.Pi*worldY/scale
	lat := 180 / math.Pi * math.Atan(0.5*(math.Exp(n)-math.Exp(-n)))
	return lon, lat
}
