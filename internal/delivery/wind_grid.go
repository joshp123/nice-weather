package delivery

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	WindGridVersion      = 1
	WindComponentScale   = 0.5
	windGridRecordSize   = 2
	windNoData           = int8(-128)
	windNoDataByte       = byte(0x80)
	maxWindArtifactBytes = 4 << 20
)

var windGridLayout = binaryGridLayout{
	name:       "wind grid",
	magic:      [8]byte{'N', 'W', 'F', 'W', 'I', 'N', 'D', 0},
	version:    WindGridVersion,
	recordSize: windGridRecordSize,
}

// WindVector is one instantaneous 10 m wind vector in metres per second.
// Invalid cells must be the zero value; the binary format owns its sentinel.
type WindVector struct {
	U, V  float32
	Valid bool
}

type WindGridReader struct {
	file *os.File
	meta GridMetadata
}

type WindAssetMetadata struct {
	Grid              GridMetadata
	SHA256            string
	Bytes             int64
	ClippedComponents uint32
}

func WriteWindGrid(path string, spec GridSpec, vectors []WindVector) (GridMetadata, uint32, error) {
	var clipped uint32
	metadata, err := writeBinaryGrid(path, spec, len(vectors), windGridLayout, func(dst []byte, index uint64) error {
		encoded, saturated, err := encodeWindVector(vectors[index])
		if err != nil {
			return err
		}
		clipped += uint32(saturated)
		copy(dst, encoded[:])
		return nil
	})
	return metadata, clipped, err
}

// WriteWindGridGzip creates the static-origin artifact consumed by browsers.
// Compression is part of the immutable file, not an origin/CDN negotiation.
func WriteWindGridGzip(path string, spec GridSpec, vectors []WindVector) (WindAssetMetadata, error) {
	if filepath.Ext(path) != ".gz" {
		return WindAssetMetadata{}, errors.New("compressed wind artifact must end in .gz")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WindAssetMetadata{}, err
	}
	raw, err := os.CreateTemp(filepath.Dir(path), ".wind-*.nww")
	if err != nil {
		return WindAssetMetadata{}, err
	}
	rawPath := raw.Name()
	if err := raw.Close(); err != nil {
		return WindAssetMetadata{}, err
	}
	defer os.Remove(rawPath)
	grid, clipped, err := WriteWindGrid(rawPath, spec, vectors)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	payload, err := os.ReadFile(rawPath)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".wind-*.gz")
	if err != nil {
		return WindAssetMetadata{}, err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	digest := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(temporary, digest))
	compressed.Header.ModTime = time.Time{}
	if _, err := compressed.Write(payload); err != nil {
		return WindAssetMetadata{}, err
	}
	if err := compressed.Close(); err != nil {
		return WindAssetMetadata{}, err
	}
	if err := temporary.Sync(); err != nil {
		return WindAssetMetadata{}, err
	}
	if err := temporary.Close(); err != nil {
		return WindAssetMetadata{}, err
	}
	info, err := os.Stat(temporaryPath)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	if info.Size() > maxWindArtifactBytes {
		return WindAssetMetadata{}, fmt.Errorf("compressed wind artifact exceeds %d bytes", maxWindArtifactBytes)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return WindAssetMetadata{}, err
	}
	committed = true
	return WindAssetMetadata{Grid: grid, SHA256: hex.EncodeToString(digest.Sum(nil)), Bytes: info.Size(), ClippedComponents: clipped}, nil
}

func ValidateWindGridGzip(path string) (WindAssetMetadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	if info.Size() > maxWindArtifactBytes {
		return WindAssetMetadata{}, fmt.Errorf("compressed wind artifact exceeds %d bytes", maxWindArtifactBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	defer file.Close()
	compressed, err := io.ReadAll(io.LimitReader(file, maxWindArtifactBytes+1))
	if err != nil {
		return WindAssetMetadata{}, err
	}
	if len(compressed) > maxWindArtifactBytes {
		return WindAssetMetadata{}, fmt.Errorf("compressed wind artifact exceeds %d bytes", maxWindArtifactBytes)
	}
	compressedReader := bytes.NewReader(compressed)
	reader, err := gzip.NewReader(compressedReader)
	if err != nil {
		return WindAssetMetadata{}, fmt.Errorf("open compressed wind artifact: %w", err)
	}
	reader.Multistream(false)
	raw, readErr := io.ReadAll(io.LimitReader(reader, maxWindArtifactBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return WindAssetMetadata{}, readErr
	}
	if closeErr != nil {
		return WindAssetMetadata{}, closeErr
	}
	if compressedReader.Len() != 0 {
		return WindAssetMetadata{}, errors.New("compressed wind artifact has trailing data")
	}
	if len(raw) > maxWindArtifactBytes {
		return WindAssetMetadata{}, fmt.Errorf("wind grid exceeds %d bytes", maxWindArtifactBytes)
	}
	temporary, err := os.CreateTemp("", "nice-weather-wind-*.nww")
	if err != nil {
		return WindAssetMetadata{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return WindAssetMetadata{}, err
	}
	if err := temporary.Close(); err != nil {
		return WindAssetMetadata{}, err
	}
	grid, err := ValidateWindGrid(temporaryPath)
	if err != nil {
		return WindAssetMetadata{}, err
	}
	digest := sha256.Sum256(compressed)
	return WindAssetMetadata{Grid: grid, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(compressed))}, nil
}

func OpenWindGrid(path string) (*WindGridReader, error) {
	file, meta, _, err := openBinaryGrid(path, windGridLayout, func(src []byte) error {
		_, err := decodeWindVector(src)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &WindGridReader{file: file, meta: meta}, nil
}

func ValidateWindGrid(path string) (GridMetadata, error) {
	reader, err := OpenWindGrid(path)
	if err != nil {
		return GridMetadata{}, err
	}
	defer reader.Close()
	return reader.Metadata(), nil
}

func (reader *WindGridReader) Close() error { return reader.file.Close() }

func (reader *WindGridReader) Metadata() GridMetadata { return reader.meta }

func (reader *WindGridReader) Vector(row, col uint32) (WindVector, error) {
	if row >= reader.meta.Rows || col >= reader.meta.Cols {
		return WindVector{}, errors.New("wind grid cell is outside the grid")
	}
	offset := int64(gridHeaderSize) + int64((uint64(row)*uint64(reader.meta.Cols)+uint64(col))*windGridRecordSize)
	var encoded [windGridRecordSize]byte
	if _, err := reader.file.ReadAt(encoded[:], offset); err != nil {
		return WindVector{}, err
	}
	return decodeWindVector(encoded[:])
}

func encodeWindVector(vector WindVector) ([windGridRecordSize]byte, int, error) {
	if !vector.Valid {
		if vector.U != 0 || vector.V != 0 {
			return [windGridRecordSize]byte{}, 0, errors.New("invalid wind vector contains hidden values")
		}
		return [windGridRecordSize]byte{windNoDataByte, windNoDataByte}, 0, nil
	}
	u, uClipped, err := quantizeWindComponent(vector.U)
	if err != nil {
		return [windGridRecordSize]byte{}, 0, fmt.Errorf("u component: %w", err)
	}
	v, vClipped, err := quantizeWindComponent(vector.V)
	if err != nil {
		return [windGridRecordSize]byte{}, 0, fmt.Errorf("v component: %w", err)
	}
	return [windGridRecordSize]byte{byte(u), byte(v)}, int(boolInt(uClipped) + boolInt(vClipped)), nil
}

func decodeWindVector(src []byte) (WindVector, error) {
	if len(src) != windGridRecordSize {
		return WindVector{}, errors.New("invalid wind vector size")
	}
	u, v := int8(src[0]), int8(src[1])
	if u == windNoData || v == windNoData {
		if u != windNoData || v != windNoData {
			return WindVector{}, errors.New("wind vector has a partial no-data sentinel")
		}
		return WindVector{}, nil
	}
	return WindVector{U: float32(u) * WindComponentScale, V: float32(v) * WindComponentScale, Valid: true}, nil
}

func quantizeWindComponent(value float32) (int8, bool, error) {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return 0, false, errors.New("wind component must be finite")
	}
	quantized := math.Round(float64(value) / WindComponentScale)
	if quantized < -127 {
		return -127, true, nil
	}
	if quantized > 127 {
		return 127, true, nil
	}
	return int8(quantized), false, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
