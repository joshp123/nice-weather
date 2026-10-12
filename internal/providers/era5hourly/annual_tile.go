package era5hourly

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

const annualTileHeaderBytes = 256

var annualAcquisitionSHA256 = sha256.Sum256(append([]byte("earthmover-era5-annual-tile-v1\nlayout=single/temporal\n"), sourceContractSHA256[:]...))

// AnnualTile holds one lossless temporal source tile for 2006–2025, including
// boundary hours needed by every civil-local daytime window.
type AnnualTile struct {
	file                              *os.File
	row, column, rows, columns, hours int
	first                             time.Time
}

func OpenAnnualTile(path string, row, column int) (*AnnualTile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*AnnualTile, error) { file.Close(); return nil, err }
	header := make([]byte, annualTileHeaderBytes)
	if _, err := io.ReadFull(file, header); err != nil {
		return fail(err)
	}
	tile := &AnnualTile{file: file, row: int(binary.LittleEndian.Uint32(header[12:])), column: int(binary.LittleEndian.Uint32(header[16:])), rows: int(binary.LittleEndian.Uint32(header[20:])), columns: int(binary.LittleEndian.Uint32(header[24:])), hours: int(binary.LittleEndian.Uint32(header[28:])), first: time.Unix(int64(binary.LittleEndian.Uint64(header[36:])), 0).UTC()}
	first := time.Date(2005, 12, 31, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if string(header[:8]) != "NWE5AT01" || binary.LittleEndian.Uint16(header[8:]) != 1 || binary.LittleEndian.Uint16(header[10:]) != annualTileHeaderBytes || tile.row != row || tile.column != column || row < 0 || column < 0 || row%12 != 0 || column%12 != 0 || tile.rows != min(12, GridRows-row) || tile.columns != min(12, GridColumns-column) || tile.rows <= 0 || tile.columns <= 0 || tile.hours != int(last.Sub(first)/time.Hour) || !tile.first.Equal(first) || binary.LittleEndian.Uint32(header[32:]) != FieldCount || string(header[44:64]) != SnapshotID || !bytes.Equal(header[64:96], annualAcquisitionSHA256[:]) || !allZero(header[96:]) {
		return fail(errors.New("ERA5 annual tile source identity or geometry changed"))
	}
	payloadBytes := int64(FieldCount) * int64(tile.hours) * int64(tile.rows*tile.columns) * 4
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() != annualTileHeaderBytes+payloadBytes+sha256.Size {
		return fail(errors.New("ERA5 annual tile is incomplete"))
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.NewSectionReader(file, 0, annualTileHeaderBytes+payloadBytes)); err != nil {
		return fail(err)
	}
	trailer := make([]byte, sha256.Size)
	if _, err := file.ReadAt(trailer, annualTileHeaderBytes+payloadBytes); err != nil {
		return fail(err)
	}
	if !bytes.Equal(trailer, digest.Sum(nil)) {
		return fail(errors.New("ERA5 annual tile checksum mismatch"))
	}
	return tile, nil
}

func (tile *AnnualTile) Close() error { return tile.file.Close() }

type annualMonthFrames struct {
	first              time.Time
	next, hours, cells int
	fields             [FieldCount][]float32
}

// MonthSource selects exact plan-order values without changing source floats.
func (tile *AnnualTile) MonthSource(year int, month time.Month, cells []Cell) (FrameSource, error) {
	first, last, err := RequiredEnvelope(year, month, cells)
	if err != nil {
		return nil, err
	}
	offset := int(first.Sub(tile.first) / time.Hour)
	hours := int(last.Sub(first)/time.Hour) + 1
	if offset < 0 || offset+hours > tile.hours {
		return nil, errors.New("ERA5 month exceeds annual tile window")
	}
	source := &annualMonthFrames{first: first, hours: hours, cells: len(cells)}
	tileCells := tile.rows * tile.columns
	raw := make([]byte, hours*tileCells*4)
	for field := range source.fields {
		at := int64(annualTileHeaderBytes) + (int64(field)*int64(tile.hours)+int64(offset))*int64(tileCells)*4
		if _, err := tile.file.ReadAt(raw, at); err != nil {
			return nil, err
		}
		selected := make([]float32, hours*len(cells))
		for index, cell := range cells {
			row, column := cell.SourceIndex/GridColumns, cell.SourceIndex%GridColumns
			localRow, localColumn := row-tile.row, column-tile.column
			if localRow < 0 || localColumn < 0 || localRow >= tile.rows || localColumn >= tile.columns {
				return nil, fmt.Errorf("ERA5 cell %d is outside annual tile", index)
			}
			localIndex := localRow*tile.columns + localColumn
			for hour := 0; hour < hours; hour++ {
				value := math.Float32frombits(binary.LittleEndian.Uint32(raw[(hour*tileCells+localIndex)*4:]))
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return nil, errors.New("ERA5 annual tile contains non-finite evidence")
				}
				selected[hour*len(cells)+index] = value
			}
		}
		source.fields[field] = selected
	}
	return source, nil
}

func (source *annualMonthFrames) Next() (Frame, error) {
	if source.next == source.hours {
		return Frame{}, io.EOF
	}
	first := source.next * source.cells
	last := first + source.cells
	frame := Frame{ValidTime: source.first.Add(time.Duration(source.next) * time.Hour), Rows: 1, Columns: source.cells, planOrder: true, TemperatureK: source.fields[0][first:last], DewPointK: source.fields[1][first:last], RainM: source.fields[2][first:last], SolarJM2: source.fields[3][first:last], Cloud: source.fields[4][first:last], WindUMPS: source.fields[5][first:last], WindVMPS: source.fields[6][first:last]}
	source.next++
	return frame, nil
}
