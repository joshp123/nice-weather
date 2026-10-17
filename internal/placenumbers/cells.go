// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package placenumbers

// CellOrdinals reproduces the engine/Worker's nearest geometric land cell,
// including left-column and earlier-row ties and longitude wrapping.
func CellOrdinals(cells [][2]uint16) []int {
	land := make([]int, 180*360)
	for i := range land {
		land[i] = -1
	}
	for ordinal, cell := range cells {
		land[int(cell[0])*360+int(cell[1])] = ordinal
	}
	rowNearest := make([]int, len(land))
	rowDistance := make([]int, len(land))
	for row := range 180 {
		for column := range 360 {
			index := row*360 + column
			rowNearest[index], rowDistance[index] = -1, 1<<30
			for radius := 0; radius <= 180; radius++ {
				left, right := land[row*360+(column-radius+360)%360], land[row*360+(column+radius)%360]
				if left >= 0 || right >= 0 {
					rowNearest[index], rowDistance[index] = right, radius*radius
					if left >= 0 {
						rowNearest[index] = left
					}
					break
				}
			}
		}
	}
	ordinals := make([]int, len(land))
	for row := range 180 {
		for column := range 360 {
			bestDistance := 1 << 30
			for sourceRow := range 180 {
				index := sourceRow*360 + column
				distance := (sourceRow-row)*(sourceRow-row) + rowDistance[index]
				if distance < bestDistance {
					bestDistance = distance
					ordinals[row*360+column] = rowNearest[index]
				}
			}
		}
	}
	return ordinals
}
