package tiles

import (
	"image/color"
	"strconv"
	"strings"

	"nice-weather-finder/internal/scoring"
)

func ColorForScore(score int) color.RGBA {
	return hex(scoring.CategoryFor(score).Color)
}

func hex(value string) color.RGBA {
	trimmed := strings.TrimPrefix(value, "#")
	if len(trimmed) != 6 {
		return color.RGBA{R: 255, A: 255}
	}
	r, _ := strconv.ParseUint(trimmed[0:2], 16, 8)
	g, _ := strconv.ParseUint(trimmed[2:4], 16, 8)
	b, _ := strconv.ParseUint(trimmed[4:6], 16, 8)
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}
