package dailyforecast

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
)

//go:embed rain-correction-mask.png
var embeddedRainCorrectionMask []byte

func readRainCorrectionCells() ([]int, error) {
	mask, err := png.Decode(bytes.NewReader(embeddedRainCorrectionMask))
	if err != nil {
		return nil, err
	}
	if mask.Bounds().Dx() != Width || mask.Bounds().Dy() != Height {
		return nil, fmt.Errorf("rain correction mask has bounds %v; want %dx%d", mask.Bounds(), Width, Height)
	}
	var cells []int
	for row := 0; row < Height; row++ {
		for column := 0; column < Width; column++ {
			value, _, _, _ := mask.At(column, row).RGBA()
			if value != 0 {
				cells = append(cells, row*Width+column)
			}
		}
	}
	return cells, nil
}
