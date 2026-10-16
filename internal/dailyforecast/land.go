package dailyforecast

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
	_ "time/tzdata"
)

//go:generate go run generate_land_mask.go
//go:embed land-mask.png
var embeddedLandMask []byte

func landCells() ([]int, error) {
	mask, err := png.Decode(bytes.NewReader(embeddedLandMask))
	if err != nil {
		return nil, err
	}
	if mask.Bounds().Dx() != Width || mask.Bounds().Dy() != Height {
		return nil, fmt.Errorf("embedded land mask has bounds %v; want %dx%d", mask.Bounds(), Width, Height)
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
