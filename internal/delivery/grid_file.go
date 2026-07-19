package delivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// binaryGridLayout identifies one immutable grid format. Daily weather and
// forecast-range grids share the same checked container while retaining
// distinct magic, versions and record codecs.
type binaryGridLayout struct {
	name       string
	magic      [8]byte
	version    uint16
	recordSize uint16
}

type binaryGridEncoder func(dst []byte, index uint64) error
type binaryGridValidator func(src []byte) error

func writeBinaryGrid(path string, spec GridSpec, count int, layout binaryGridLayout, encode binaryGridEncoder) (GridMetadata, error) {
	if err := validateGridSpecForRecordSize(spec, layout.recordSize); err != nil {
		return GridMetadata{}, err
	}
	wantCount := uint64(spec.Rows) * uint64(spec.Cols)
	if count < 0 || uint64(count) != wantCount {
		return GridMetadata{}, fmt.Errorf("%s has %d records; want %d", layout.name, count, wantCount)
	}
	if encode == nil {
		return GridMetadata{}, fmt.Errorf("%s encoder is required", layout.name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return GridMetadata{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".grid-*.tmp")
	if err != nil {
		return GridMetadata{}, err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(make([]byte, gridHeaderSize)); err != nil {
		return GridMetadata{}, err
	}
	header := encodeBinaryGridHeader(layout, spec, nil)
	digest := sha256.New()
	_, _ = digest.Write(header[:64])
	payload := io.MultiWriter(tmp, digest)
	encoded := make([]byte, layout.recordSize)
	for index := uint64(0); index < wantCount; index++ {
		clear(encoded)
		if err := encode(encoded, index); err != nil {
			return GridMetadata{}, fmt.Errorf("%s record %d: %w", layout.name, index, err)
		}
		if _, err := payload.Write(encoded); err != nil {
			return GridMetadata{}, err
		}
	}
	checksum := digest.Sum(nil)
	header = encodeBinaryGridHeader(layout, spec, checksum)
	if _, err := tmp.WriteAt(header, 0); err != nil {
		return GridMetadata{}, err
	}
	if err := tmp.Sync(); err != nil {
		return GridMetadata{}, err
	}
	if err := tmp.Close(); err != nil {
		return GridMetadata{}, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return GridMetadata{}, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return GridMetadata{}, err
	}
	committed = true
	return binaryGridMetadata(layout, spec, checksum), nil
}

func openBinaryGrid(path string, layout binaryGridLayout, validate binaryGridValidator) (*os.File, GridMetadata, *WebMercatorMapper, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, GridMetadata{}, nil, err
	}
	meta, mapper, err := validateBinaryGrid(file, layout, validate)
	if err != nil {
		_ = file.Close()
		return nil, GridMetadata{}, nil, err
	}
	return file, meta, mapper, nil
}

func validateBinaryGrid(file *os.File, layout binaryGridLayout, validate binaryGridValidator) (GridMetadata, *WebMercatorMapper, error) {
	if validate == nil {
		return GridMetadata{}, nil, fmt.Errorf("%s validator is required", layout.name)
	}
	var header [gridHeaderSize]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return GridMetadata{}, nil, fmt.Errorf("read %s header: %w", layout.name, err)
	}
	meta, err := decodeBinaryGridHeader(layout, header[:])
	if err != nil {
		return GridMetadata{}, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return GridMetadata{}, nil, err
	}
	recordCount := uint64(meta.Rows) * uint64(meta.Cols)
	wantSize := int64(gridHeaderSize) + int64(recordCount*uint64(meta.RecordSize))
	if info.Size() != wantSize {
		return GridMetadata{}, nil, fmt.Errorf("%s file size is %d; want %d", layout.name, info.Size(), wantSize)
	}
	digest := sha256.New()
	_, _ = digest.Write(header[:64])
	encoded := make([]byte, layout.recordSize)
	for index := uint64(0); index < recordCount; index++ {
		offset := int64(gridHeaderSize) + int64(index*uint64(layout.recordSize))
		if _, err := file.ReadAt(encoded, offset); err != nil {
			return GridMetadata{}, nil, err
		}
		_, _ = digest.Write(encoded)
		if err := validate(encoded); err != nil {
			return GridMetadata{}, nil, fmt.Errorf("%s record %d: %w", layout.name, index, err)
		}
	}
	if hex.EncodeToString(digest.Sum(nil)) != meta.SHA256 {
		return GridMetadata{}, nil, fmt.Errorf("%s checksum mismatch", layout.name)
	}
	mapper, err := NewWebMercatorMapper(GridSpec{Bounds: meta.Bounds, Rows: meta.Rows, Cols: meta.Cols})
	if err != nil {
		return GridMetadata{}, nil, err
	}
	return meta, mapper, nil
}

func encodeBinaryGridHeader(layout binaryGridLayout, spec GridSpec, checksum []byte) []byte {
	header := make([]byte, gridHeaderSize)
	copy(header[0:8], layout.magic[:])
	binary.LittleEndian.PutUint16(header[8:10], layout.version)
	binary.LittleEndian.PutUint16(header[10:12], gridHeaderSize)
	binary.LittleEndian.PutUint16(header[12:14], layout.recordSize)
	binary.LittleEndian.PutUint32(header[16:20], spec.Rows)
	binary.LittleEndian.PutUint32(header[20:24], spec.Cols)
	putFloat64(header[24:32], spec.Bounds.West)
	putFloat64(header[32:40], spec.Bounds.East)
	putFloat64(header[40:48], spec.Bounds.South)
	putFloat64(header[48:56], spec.Bounds.North)
	copy(header[64:96], checksum)
	return header
}

func decodeBinaryGridHeader(layout binaryGridLayout, header []byte) (GridMetadata, error) {
	if len(header) != gridHeaderSize || string(header[:8]) != string(layout.magic[:]) {
		return GridMetadata{}, fmt.Errorf("invalid %s magic", layout.name)
	}
	if !allZero(header[14:16]) || !allZero(header[56:64]) || !allZero(header[96:128]) {
		return GridMetadata{}, fmt.Errorf("%s header reserved bytes are not zero", layout.name)
	}
	version := binary.LittleEndian.Uint16(header[8:10])
	if version != layout.version {
		return GridMetadata{}, fmt.Errorf("unsupported %s version %d", layout.name, version)
	}
	if binary.LittleEndian.Uint16(header[10:12]) != gridHeaderSize || binary.LittleEndian.Uint16(header[12:14]) != layout.recordSize {
		return GridMetadata{}, fmt.Errorf("unsupported %s layout", layout.name)
	}
	spec := GridSpec{
		Rows: binary.LittleEndian.Uint32(header[16:20]),
		Cols: binary.LittleEndian.Uint32(header[20:24]),
		Bounds: GridBounds{
			West: getFloat64(header[24:32]), East: getFloat64(header[32:40]),
			South: getFloat64(header[40:48]), North: getFloat64(header[48:56]),
		},
	}
	if err := validateGridSpecForRecordSize(spec, layout.recordSize); err != nil {
		return GridMetadata{}, err
	}
	return binaryGridMetadata(layout, spec, header[64:96]), nil
}

func binaryGridMetadata(layout binaryGridLayout, spec GridSpec, checksum []byte) GridMetadata {
	return GridMetadata{
		Version: layout.version, Bounds: spec.Bounds, Rows: spec.Rows, Cols: spec.Cols,
		RecordSize: layout.recordSize, SHA256: hex.EncodeToString(checksum),
	}
}

func allZero(values []byte) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
}

func putFloat64(dst []byte, value float64) {
	binary.LittleEndian.PutUint64(dst, math.Float64bits(value))
}

func getFloat64(src []byte) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(src)) }
