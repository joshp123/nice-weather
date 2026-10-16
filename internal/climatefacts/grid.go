// Package climatefacts reads ERA5 weather facts without assigning weather scores.
package climatefacts

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/ringsaturn/tzf"
)

const Width, Height, TileSize = 1440, 721, 12

//go:embed land-mask.png
var landMaskPNG []byte

// StaticGrid arrays use ERA5's north-to-south, zero-to-360 longitude order.
type StaticGrid struct {
	LandFraction, SourceElevationM, GroundElevationM []float32
}

func ReadStatic(path string) (*StaticGrid, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	header := make([]byte, 8)
	if _, err = io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if string(header) != "NWFSTAT1" {
		return nil, fmt.Errorf("invalid climate static grid header")
	}
	grid := &StaticGrid{}
	for _, destination := range []*[]float32{&grid.LandFraction, &grid.SourceElevationM, &grid.GroundElevationM} {
		*destination = make([]float32, Width*Height)
		if err = binary.Read(reader, binary.LittleEndian, *destination); err != nil {
			return nil, err
		}
	}
	return grid, nil
}

type Cell struct {
	Index                                          uint32
	Latitude, Longitude, ElevationM                float64
	Row, Column, TemperatureRow, TemperatureColumn int
	Location                                       *time.Location
	MonthlyTurbidity                               *[12]float64
}

func (grid *StaticGrid) TemperatureCell(latitude, longitude float64, row, column int) (int, int) {
	bestRow, bestColumn, bestDistance, bestLand := row, column, math.Inf(1), -1.0
	eligibleFound := false
	for rowOffset := -1; rowOffset <= 1; rowOffset++ {
		candidateRow := row + rowOffset
		if candidateRow < 0 || candidateRow >= Height {
			continue
		}
		for columnOffset := -1; columnOffset <= 1; columnOffset++ {
			candidateColumn := (column + columnOffset + Width) % Width
			land := float64(grid.LandFraction[candidateRow*Width+candidateColumn])
			eligible := land >= .8
			if eligibleFound && !eligible {
				continue
			}
			candidateLatitude, candidateLongitude := Coordinates(candidateRow, candidateColumn)
			longitudeDistance := math.Remainder(candidateLongitude-longitude, 360) * math.Cos(latitude*math.Pi/180)
			distance := math.Pow(candidateLatitude-latitude, 2) + longitudeDistance*longitudeDistance
			better := (eligible && !eligibleFound) || (eligible && distance < bestDistance) || (!eligibleFound && (land > bestLand || (land == bestLand && distance < bestDistance)))
			if better {
				bestRow, bestColumn, bestDistance, bestLand = candidateRow, candidateColumn, distance, land
				eligibleFound = eligible
			}
		}
	}
	return bestRow, bestColumn
}

func Coordinates(row, column int) (float64, float64) {
	return 90 - float64(row)*.25, math.Remainder(float64(column)*.25, 360)
}

func (grid *StaticGrid) TileCells(tileRow, tileColumn int) ([]Cell, error) {
	if tileRow < 0 || tileRow >= (Height+TileSize-1)/TileSize || tileColumn < 0 || tileColumn >= Width/TileSize {
		return nil, fmt.Errorf("tile must be row 0..60, column 0..119")
	}
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return nil, err
	}
	locations := map[string]*time.Location{}
	landMask, err := png.Decode(bytes.NewReader(landMaskPNG))
	if err != nil {
		return nil, err
	}
	var cells []Cell
	for row := tileRow * TileSize; row < min(Height, (tileRow+1)*TileSize); row++ {
		for column := tileColumn * TileSize; column < (tileColumn+1)*TileSize; column++ {
			mapColumn := (column + Width/2) % Width
			maskValue, _, _, _ := landMask.At(mapColumn, row).RGBA()
			if maskValue == 0 {
				continue
			}
			latitude, longitude := Coordinates(row, column)
			name := finder.GetTimezoneName(longitude, latitude)
			if name == "" {
				for _, offset := range [][2]float64{{1e-7, 1e-7}, {-1e-7, 1e-7}, {1e-7, -1e-7}, {-1e-7, -1e-7}} {
					name = finder.GetTimezoneName(max(-180+1e-7, min(180-1e-7, longitude+offset[0])), max(-90+1e-7, min(90-1e-7, latitude+offset[1])))
					if name != "" {
						break
					}
				}
			}
			if name == "" {
				return nil, fmt.Errorf("no IANA time zone at %.2f,%.2f", latitude, longitude)
			}
			location := locations[name]
			if location == nil {
				location, err = time.LoadLocation(name)
				if err != nil {
					return nil, err
				}
				locations[name] = location
			}
			temperatureRow, temperatureColumn := grid.TemperatureCell(latitude, longitude, row, column)
			cells = append(cells, Cell{Index: uint32(row*Width + mapColumn), Latitude: latitude, Longitude: longitude, ElevationM: float64(grid.GroundElevationM[row*Width+column]), Row: row, Column: column, TemperatureRow: temperatureRow, TemperatureColumn: temperatureColumn, Location: location})
		}
	}
	return cells, nil
}
