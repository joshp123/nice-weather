package climatebuild

import (
	"errors"
	"fmt"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/agera5"
)

const (
	worldFirstSourceLatitude  = 50
	worldLastSourceLatitude   = 1750
	worldFirstSourceLongitude = 1
	worldLastSourceLongitude  = 3599
	worldRows                 = worldLastSourceLatitude - worldFirstSourceLatitude + 1
	worldCols                 = worldLastSourceLongitude - worldFirstSourceLongitude + 1
)

// WorldwideGridSpec is the exact 0.1-degree delivery geometry backed by
// AgERA5 source-cell centres. Rows run north to south and columns west to east.
func WorldwideGridSpec() delivery.GridSpec {
	return delivery.GridSpec{
		Bounds: delivery.GridBounds{
			West:  -179.95,
			East:  179.95,
			South: -85.05,
			North: 85.05,
		},
		Rows: worldRows,
		Cols: worldCols,
	}
}

// Locate resolves a logical cell inside one AgERA5 source block to the source
// array's latitude and longitude indices. The source latitude axis runs south
// to north; WorldIndex performs the deliberate reversal into delivery order.
func Locate(block agera5.BlockCoordinate, latitudeOffset, longitudeOffset int) (latitudeIndex, longitudeIndex int, err error) {
	latitudeCount, longitudeCount, err := agera5.BlockShape(block)
	if err != nil {
		return 0, 0, err
	}
	if latitudeOffset < 0 || latitudeOffset >= latitudeCount || longitudeOffset < 0 || longitudeOffset >= longitudeCount {
		return 0, 0, fmt.Errorf("AgERA5 offset (%d,%d) is outside block %+v with shape %dx%d", latitudeOffset, longitudeOffset, block, latitudeCount, longitudeCount)
	}
	return block.Latitude*16 + latitudeOffset, block.Longitude*32 + longitudeOffset, nil
}

// WorldIndex maps one AgERA5 source-array cell to the row-major worldwide
// delivery plane. It returns false for valid source cells outside the cropped
// delivery extent and for indices outside the measured source array.
func WorldIndex(sourceLatitudeIndex, sourceLongitudeIndex int) (int, bool) {
	if sourceLatitudeIndex < worldFirstSourceLatitude || sourceLatitudeIndex > worldLastSourceLatitude ||
		sourceLongitudeIndex < worldFirstSourceLongitude || sourceLongitudeIndex > worldLastSourceLongitude {
		return 0, false
	}
	row := worldLastSourceLatitude - sourceLatitudeIndex
	column := sourceLongitudeIndex - worldFirstSourceLongitude
	return row*worldCols + column, true
}

// BlockLandMask extracts a Natural Earth centre mask into the exact row-major
// order of one AgERA5 source block. Source cells outside the worldwide crop are
// false; publication therefore cannot accidentally wrap or duplicate a
// dateline column.
func BlockLandMask(world pipeline.FloatGrid, block agera5.BlockCoordinate) ([]bool, error) {
	if err := world.Validate(); err != nil {
		return nil, fmt.Errorf("worldwide land mask: %w", err)
	}
	if world.Spec != WorldwideGridSpec() {
		return nil, errors.New("land mask does not use the canonical worldwide climate grid")
	}
	latitudeCount, longitudeCount, err := agera5.BlockShape(block)
	if err != nil {
		return nil, err
	}
	land := make([]bool, latitudeCount*longitudeCount)
	for latitudeOffset := range latitudeCount {
		for longitudeOffset := range longitudeCount {
			sourceLatitudeIndex, sourceLongitudeIndex, err := Locate(block, latitudeOffset, longitudeOffset)
			if err != nil {
				return nil, err
			}
			worldIndex, included := WorldIndex(sourceLatitudeIndex, sourceLongitudeIndex)
			if !included {
				continue
			}
			value := world.Values[worldIndex]
			if value != 0 && value != 1 {
				return nil, fmt.Errorf("worldwide land mask cell %d has value %g; want 0 or 1", worldIndex, value)
			}
			land[latitudeOffset*longitudeCount+longitudeOffset] = value == 1
		}
	}
	return land, nil
}
