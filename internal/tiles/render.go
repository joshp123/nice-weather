package tiles

import (
	"image"
	"math"

	"nice-weather-finder/internal/scoring"
	"nice-weather-finder/internal/weatherinput"
)

const TileSize = 256
const MaxZoom = 7
const RendererVersion = 2

type ScoreGrid struct {
	West    float64
	South   float64
	StepDeg float64
	Width   int
	Height  int
	Scores  []float64
	Visible []bool
}

func NewScoreGrid(inputs weatherinput.Grid, profile scoring.Profile, horizon string) ScoreGrid {
	values := inputs.Horizons[horizon]
	scores := make([]float64, len(values))
	visible := make([]bool, len(values))
	valid := inputs.Valid[horizon]
	for i, input := range values {
		if len(valid) == len(values) && !valid[i] {
			continue
		}
		if len(inputs.Land) == len(values) && !inputs.Land[i] {
			continue
		}
		scores[i] = float64(scoring.Score(input, profile).Score)
		visible[i] = true
	}
	return ScoreGrid{
		West:    inputs.West,
		South:   inputs.South,
		StepDeg: inputs.StepDeg,
		Width:   inputs.Width,
		Height:  inputs.Height,
		Scores:  scores,
		Visible: visible,
	}
}

func RenderWeatherGridTile(inputs weatherinput.Grid, profile scoring.Profile, horizon string, z, x, y int) *image.RGBA {
	return RenderScoreGridTile(NewScoreGrid(inputs, profile, horizon), z, x, y)
}

func RenderScoreGridTile(scores ScoreGrid, z, x, y int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
	xSamples := tileXSamples(scores, z, x)
	ySamples := tileYSamples(scores, z, y)
	for py := 0; py < TileSize; py++ {
		ySample := ySamples[py]
		if !ySample.ok {
			continue
		}
		for px := 0; px < TileSize; px++ {
			xSample := xSamples[px]
			if !xSample.ok {
				continue
			}
			index := ySample.nearest*scores.Width + xSample.nearest
			if index >= len(scores.Visible) || !scores.Visible[index] {
				continue
			}
			img.SetRGBA(px, py, ColorForScore(int(math.Round(interpolatedScore(scores, xSample, ySample, index)))))
		}
	}
	return img
}

type axisSample struct {
	nearest int
	lower   int
	upper   int
	frac    float64
	ok      bool
}

func tileXSamples(scores ScoreGrid, z, x int) [TileSize]axisSample {
	var samples [TileSize]axisSample
	for px := 0; px < TileSize; px++ {
		lon, _ := lonLatForPixel(z, x, 0, px, 0)
		samples[px] = gridSample(lon, scores.West, scores.StepDeg, scores.Width)
	}
	return samples
}

func tileYSamples(scores ScoreGrid, z, y int) [TileSize]axisSample {
	var samples [TileSize]axisSample
	for py := 0; py < TileSize; py++ {
		_, lat := lonLatForPixel(z, 0, y, 0, py)
		samples[py] = gridSample(lat, scores.South, scores.StepDeg, scores.Height)
	}
	return samples
}

func gridSample(value, start, step float64, count int) axisSample {
	position := (value - start) / step
	nearest := int(math.Round(position))
	if nearest < 0 || nearest >= count {
		return axisSample{}
	}
	lower := int(math.Floor(position))
	upper := lower + 1
	frac := position - float64(lower)
	if lower < 0 {
		lower = nearest
		upper = nearest
		frac = 0
	} else if upper >= count {
		lower = nearest
		upper = nearest
		frac = 0
	}
	return axisSample{nearest: nearest, lower: lower, upper: upper, frac: frac, ok: true}
}

func interpolatedScore(scores ScoreGrid, xSample, ySample axisSample, fallbackIndex int) float64 {
	i00 := ySample.lower*scores.Width + xSample.lower
	i10 := ySample.lower*scores.Width + xSample.upper
	i01 := ySample.upper*scores.Width + xSample.lower
	i11 := ySample.upper*scores.Width + xSample.upper
	if !visibleIndexes(scores, i00, i10, i01, i11) {
		return scores.Scores[fallbackIndex]
	}
	top := lerp(scores.Scores[i00], scores.Scores[i10], xSample.frac)
	bottom := lerp(scores.Scores[i01], scores.Scores[i11], xSample.frac)
	return lerp(top, bottom, ySample.frac)
}

func visibleIndexes(scores ScoreGrid, indexes ...int) bool {
	for _, index := range indexes {
		if index < 0 || index >= len(scores.Visible) || !scores.Visible[index] {
			return false
		}
	}
	return true
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
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
