// Package ifsgrib decodes the regular latitude/longitude CCSDS-packed surface
// fields published by ECMWF IFS open data. It does not implement other GRIB grids.
package ifsgrib

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

type Field struct {
	Name               string
	RunTime            time.Time
	StartStep, EndStep int
	Width, Height      int
	Values             []float64
}

// Decode returns north-to-south rows beginning at 180° (equivalent to -180°),
// the same grid order used by the published PNGs and point records.
func Decode(message []byte) (Field, error) {
	var field Field
	if len(message) < 20 || !bytes.Equal(message[:4], []byte("GRIB")) || message[6] != 0 || message[7] != 2 || binary.BigEndian.Uint64(message[8:16]) != uint64(len(message)) || !bytes.Equal(message[len(message)-4:], []byte("7777")) {
		return field, fmt.Errorf("invalid meteorological GRIB2 envelope")
	}
	var sections [8][]byte
	for offset := 16; offset < len(message)-4; {
		if offset+5 > len(message)-4 {
			return field, fmt.Errorf("truncated GRIB2 section")
		}
		length := int(binary.BigEndian.Uint32(message[offset : offset+4]))
		number := int(message[offset+4])
		if length < 5 || offset+length > len(message)-4 || number < 1 || number > 7 || sections[number] != nil {
			return field, fmt.Errorf("invalid GRIB2 section %d length %d", number, length)
		}
		sections[number] = message[offset : offset+length]
		offset += length
	}
	for _, number := range []int{1, 3, 4, 5, 6, 7} {
		if sections[number] == nil {
			return field, fmt.Errorf("missing GRIB2 section %d", number)
		}
	}
	identification := sections[1]
	if len(identification) != 21 || binary.BigEndian.Uint16(identification[5:7]) != 98 {
		return field, fmt.Errorf("not an ECMWF GRIB2 field")
	}
	field.RunTime = time.Date(int(binary.BigEndian.Uint16(identification[12:14])), time.Month(identification[14]), int(identification[15]), int(identification[16]), int(identification[17]), int(identification[18]), 0, time.UTC)
	grid := sections[3]
	if len(grid) != 72 || binary.BigEndian.Uint16(grid[12:14]) != 0 {
		return field, fmt.Errorf("unsupported GRIB2 grid template")
	}
	field.Width = int(binary.BigEndian.Uint32(grid[30:34]))
	field.Height = int(binary.BigEndian.Uint32(grid[34:38]))
	if field.Width != 1440 || field.Height != 721 || binary.BigEndian.Uint32(grid[6:10]) != 1440*721 || binary.BigEndian.Uint32(grid[38:42]) != 0 || signed32(grid[46:50]) != 90000000 || signed32(grid[50:54]) != 180000000 || signed32(grid[55:59]) != -90000000 || signed32(grid[59:63]) != 179750000 || binary.BigEndian.Uint32(grid[63:67]) != 250000 || binary.BigEndian.Uint32(grid[67:71]) != 250000 || grid[71] != 0 {
		return field, fmt.Errorf("unsupported IFS grid geometry")
	}
	product := sections[4]
	if len(product) < 34 || binary.BigEndian.Uint16(product[5:7]) != 0 || product[17] != 1 {
		return field, fmt.Errorf("unsupported IFS product definition")
	}
	template := binary.BigEndian.Uint16(product[7:9])
	field.StartStep = int(binary.BigEndian.Uint32(product[18:22]))
	field.EndStep = field.StartStep
	switch {
	case product[9] == 0 && product[10] == 0 && product[22] == 103 && product[23] == 0 && binary.BigEndian.Uint32(product[24:28]) == 2:
		field.Name = "2t"
	case product[9] == 0 && product[10] == 6 && product[22] == 103 && product[23] == 0 && binary.BigEndian.Uint32(product[24:28]) == 2:
		field.Name = "2d"
	case product[9] == 2 && product[10] == 2 && product[22] == 103 && product[23] == 0 && binary.BigEndian.Uint32(product[24:28]) == 10:
		field.Name = "10u"
	case product[9] == 2 && product[10] == 3 && product[22] == 103 && product[23] == 0 && binary.BigEndian.Uint32(product[24:28]) == 10:
		field.Name = "10v"
	case product[9] == 6 && product[10] == 192 && product[22] == 1:
		field.Name = "tcc"
	case product[9] == 4 && product[10] == 7 && product[22] == 1:
		field.Name = "ssrd"
	case product[9] == 1 && product[10] == 193 && product[22] == 1:
		field.Name = "tp"
	default:
		return field, fmt.Errorf("unsupported IFS parameter category=%d number=%d surface=%d", product[9], product[10], product[22])
	}
	if field.Name == "tp" || field.Name == "ssrd" {
		if template != 8 || len(product) != 58 || field.StartStep != 0 || product[41] != 1 || product[46] != 1 || product[47] != 2 || product[48] != 1 {
			return field, fmt.Errorf("IFS %s is not accumulated from run time", field.Name)
		}
		field.EndStep = int(binary.BigEndian.Uint32(product[49:53]))
		intervalEnd := time.Date(int(binary.BigEndian.Uint16(product[34:36])), time.Month(product[36]), int(product[37]), int(product[38]), int(product[39]), int(product[40]), 0, time.UTC)
		if !intervalEnd.Equal(field.RunTime.Add(time.Duration(field.EndStep) * time.Hour)) {
			return field, fmt.Errorf("IFS %s interval end disagrees with step", field.Name)
		}
	} else if template != 0 || len(product) != 34 {
		return field, fmt.Errorf("IFS %s is not instantaneous", field.Name)
	}
	representation := sections[5]
	if len(representation) != 25 || binary.BigEndian.Uint16(representation[9:11]) != 42 || binary.BigEndian.Uint32(representation[5:9]) != 1440*721 || representation[20] != 0 || representation[21] != 14 {
		return field, fmt.Errorf("unsupported IFS CCSDS representation")
	}
	if len(sections[6]) != 6 || sections[6][5] != 255 {
		return field, fmt.Errorf("IFS bitmap is unsupported")
	}
	reference := float64(math.Float32frombits(binary.BigEndian.Uint32(representation[11:15])))
	binaryScale := math.Ldexp(1, signed16(representation[15:17]))
	decimalScale := math.Pow10(-signed16(representation[17:19]))
	sampleBits := int(representation[19])
	count := field.Width * field.Height
	field.Values = make([]float64, count)
	if sampleBits == 0 {
		for index := range field.Values {
			field.Values[index] = reference * decimalScale
		}
		return field, nil
	}
	integers, err := decodeAEC(sections[7][5:], sampleBits, int(representation[22]), int(binary.BigEndian.Uint16(representation[23:25])), count)
	if err != nil {
		return field, fmt.Errorf("decode IFS %s CCSDS: %w", field.Name, err)
	}
	for index, value := range integers {
		field.Values[index] = (reference + float64(value)*binaryScale) * decimalScale
	}
	return field, nil
}
func signed16(value []byte) int {
	number := int(binary.BigEndian.Uint16(value))
	if number&0x8000 != 0 {
		return -(number & 0x7fff)
	}
	return number
}
func signed32(value []byte) int64 {
	number := int64(binary.BigEndian.Uint32(value))
	if number&0x80000000 != 0 {
		return -(number & 0x7fffffff)
	}
	return number
}
