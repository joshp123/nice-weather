package era5hourly

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func TestDecoderAcceptsPythonProducedGoldenFrame(t *testing.T) {
	// Produced by scripts/era5_hourly_frames.py write_frame with a 2x3 plane
	// for each field, field values 0.25 through 6.25, and 2025-11-01T04:00Z.
	const pythonFrameBase64 = "TldFNUhSMDEBAIAAAgADAAcABADAhQVpAAAAAKgAAAAAAAAAQzJRU0VDOUZQM1NKNk1UNEQ5NEcGCmmM1mLNiTh1c/o67zvUWanaKBVGlXWYDs08yj4QyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAIA+AACAPgAAgD4AAIA+AACAPgAAgD4AAKA/AACgPwAAoD8AAKA/AACgPwAAoD8AABBAAAAQQAAAEEAAABBAAAAQQAAAEEAAAFBAAABQQAAAUEAAAFBAAABQQAAAUEAAAIhAAACIQAAAiEAAAIhAAACIQAAAiEAAAKhAAACoQAAAqEAAAKhAAACoQAAAqEAAAMhAAADIQAAAyEAAAMhAAADIQAAAyECErySeapvyuIKOMNJsRq+5qb2SRQGv6EwnzHyF4OzQyA=="
	input, err := base64.StdEncoding.DecodeString(pythonFrameBase64)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := newDecoder(bytes.NewReader(input), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !frame.ValidTime.Equal(time.Date(2025, 11, 1, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("valid time = %s", frame.ValidTime)
	}
	for fieldIndex, field := range frame.fields() {
		for cellIndex, value := range field {
			if value != float32(fieldIndex)+.25 {
				t.Fatalf("field %d cell %d = %v", fieldIndex, cellIndex, value)
			}
		}
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("end = %v, want EOF", err)
	}
}

func TestDecoderAcceptsPinnedFrameAndConsecutiveHours(t *testing.T) {
	first := time.Date(2025, 11, 1, 4, 0, 0, 0, time.UTC)
	input := append(encodedFrame(t, first, 2, 3, [FieldCount][]float32{}), encodedFrame(t, first.Add(time.Hour), 2, 3, [FieldCount][]float32{})...)
	decoder, err := newDecoder(bytes.NewReader(input), 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		frame, err := decoder.Next()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Rows != 2 || frame.Columns != 3 || frame.TemperatureK[0] != 280 || frame.WindVMPS[5] != 7 {
			t.Fatalf("decoded frame %d = %+v", index, frame)
		}
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("end = %v, want EOF", err)
	}
}

func TestDecoderRejectsSourceTimePayloadAndFiniteValueDrift(t *testing.T) {
	validTime := time.Date(2025, 11, 1, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{"magic", func(input []byte) { input[0] ^= 1 }},
		{"snapshot", func(input []byte) { input[36] ^= 1 }},
		{"contract", func(input []byte) { input[56] ^= 1 }},
		{"reserved", func(input []byte) { input[127] = 1 }},
		{"non-hour time", func(input []byte) { binary.LittleEndian.PutUint64(input[20:28], uint64(validTime.Unix()+60)) }},
		{"time outside source axis", func(input []byte) {
			binary.LittleEndian.PutUint64(input[20:28], uint64(sourceEpoch.Add(SourceHours*time.Hour).Unix()))
		}},
		{"payload length", func(input []byte) { binary.LittleEndian.PutUint64(input[28:36], 1) }},
		{"payload checksum", func(input []byte) { input[len(input)-1] ^= 1 }},
		{"non-finite", func(input []byte) {
			binary.LittleEndian.PutUint32(input[HeaderBytes:HeaderBytes+4], math.Float32bits(float32(math.NaN())))
			sum := sha256.Sum256(input[HeaderBytes : len(input)-TrailerBytes])
			copy(input[len(input)-TrailerBytes:], sum[:])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := encodedFrame(t, validTime, 2, 3, [FieldCount][]float32{})
			test.mutate(input)
			decoder, err := newDecoder(bytes.NewReader(input), 2, 3)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decoder.Next(); err == nil {
				t.Fatal("invalid frame was accepted")
			}
		})
	}
}

func TestDecoderRejectsTimeGap(t *testing.T) {
	first := time.Date(2025, 11, 1, 4, 0, 0, 0, time.UTC)
	input := append(encodedFrame(t, first, 1, 1, [FieldCount][]float32{}), encodedFrame(t, first.Add(2*time.Hour), 1, 1, [FieldCount][]float32{})...)
	decoder, _ := newDecoder(bytes.NewReader(input), 1, 1)
	if _, err := decoder.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Next(); err == nil || !strings.Contains(err.Error(), "not consecutive") {
		t.Fatalf("gap error = %v", err)
	}
}

func encodedFrame(t *testing.T, validTime time.Time, rows, columns int, fields [FieldCount][]float32) []byte {
	t.Helper()
	cells := rows * columns
	if fields[0] == nil {
		values := [FieldCount]float32{280, 270, .001, 1_000_000, .25, 3, 7}
		for field := range fields {
			fields[field] = make([]float32, cells)
			for index := range fields[field] {
				fields[field][index] = values[field]
			}
		}
	}
	header := make([]byte, HeaderBytes)
	copy(header[:8], frameMagic)
	binary.LittleEndian.PutUint16(header[8:10], SchemaVersion)
	binary.LittleEndian.PutUint16(header[10:12], HeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], uint16(rows))
	binary.LittleEndian.PutUint16(header[14:16], uint16(columns))
	binary.LittleEndian.PutUint16(header[16:18], FieldCount)
	binary.LittleEndian.PutUint16(header[18:20], scalarBytes)
	binary.LittleEndian.PutUint64(header[20:28], uint64(validTime.Unix()))
	binary.LittleEndian.PutUint64(header[28:36], uint64(FieldCount*cells*scalarBytes))
	copy(header[36:56], SnapshotID)
	copy(header[56:88], sourceContractSHA256[:])
	var payload bytes.Buffer
	for _, field := range fields {
		for _, value := range field {
			if err := binary.Write(&payload, binary.LittleEndian, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	sum := sha256.Sum256(payload.Bytes())
	return append(append(header, payload.Bytes()...), sum[:]...)
}
