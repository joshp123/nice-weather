package daymeasures

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

// SchemaVersion is part of the stream header. A record is 48 bytes before
// compression, in little-endian order. Readme.md specifies every field and unit.
const SchemaVersion = 1
const RecordBytes = 48

var streamHeader = [8]byte{'N', 'W', 'D', 'M', SchemaVersion, 0, RecordBytes, 0}

type Writer struct{ destination io.Writer }

func NewWriter(destination io.Writer) (*Writer, error) {
	if _, err := destination.Write(streamHeader[:]); err != nil {
		return nil, err
	}
	return &Writer{destination: destination}, nil
}

func (writer *Writer) Write(cell uint32, date time.Time, measures DayMeasures) error {
	var record [RecordBytes]byte
	binary.LittleEndian.PutUint32(record[0:4], cell)
	year, month, day := date.Date()
	epochDay := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Unix() / 86400
	binary.LittleEndian.PutUint32(record[4:8], uint32(int32(epochDay)))
	values := make([]float64, 0, 19)
	values = append(values, measures.TemperatureC[:]...)
	values = append(values, measures.DewPointC, measures.DaytimeRainMM, measures.WholeDayRainMM, measures.SunShare, measures.WindKPH, measures.GustKPH, measures.DaytimeHours, measures.WholeDayRainHours, measures.SunUpHours)
	for index, value := range values {
		scale := 100.0
		if index == 11 || index == 12 {
			scale = 10
		}
		if index == 13 {
			scale = 10000
		}
		quantized := math.Round(value * scale)
		minimum, maximum := 0.0, 65535.0
		if index <= 10 {
			minimum, maximum = -32768, 32767
		}
		if !finite(value) || quantized < minimum || quantized > maximum {
			return fmt.Errorf("day measure field %d is outside binary range: %g", index, value)
		}
		binary.LittleEndian.PutUint16(record[8+index*2:], uint16(int64(quantized)))
	}
	if measures.SunAvailable {
		record[46] |= 1
	}
	if measures.GustAvailable {
		record[46] |= 2
	}
	_, err := writer.destination.Write(record[:])
	return err
}

type Reader struct{ source io.Reader }

func NewReader(source io.Reader) (*Reader, error) {
	var header [8]byte
	if _, err := io.ReadFull(source, header[:]); err != nil {
		return nil, err
	}
	if header != streamHeader {
		return nil, fmt.Errorf("unsupported day-measures stream header %x", header)
	}
	return &Reader{source: source}, nil
}

func (reader *Reader) Read() (cell uint32, date time.Time, measures DayMeasures, err error) {
	var record [RecordBytes]byte
	if _, err = io.ReadFull(reader.source, record[:]); err != nil {
		return
	}
	cell = binary.LittleEndian.Uint32(record[0:4])
	epochDay := int32(binary.LittleEndian.Uint32(record[4:8]))
	date = time.Unix(int64(epochDay)*86400, 0).UTC()
	var values [19]float64
	for index := range values {
		raw := binary.LittleEndian.Uint16(record[8+index*2:])
		value := float64(raw)
		if index <= 10 {
			value = float64(int16(raw))
		}
		scale := 100.0
		if index == 11 || index == 12 {
			scale = 10
		}
		if index == 13 {
			scale = 10000
		}
		values[index] = value / scale
	}
	copy(measures.TemperatureC[:], values[:10])
	measures.DewPointC, measures.DaytimeRainMM, measures.WholeDayRainMM = values[10], values[11], values[12]
	measures.SunShare, measures.WindKPH, measures.GustKPH = values[13], values[14], values[15]
	measures.DaytimeHours, measures.WholeDayRainHours, measures.SunUpHours = values[16], values[17], values[18]
	measures.SunAvailable, measures.GustAvailable = record[46]&1 != 0, record[46]&2 != 0
	return
}
