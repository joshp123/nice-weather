package era5climateproduct

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"nice-weather-finder/internal/delivery"
)

type PointKind string

const (
	PointKindMain     PointKind = "main"
	PointKindDateline PointKind = "positive-dateline"
)

type PointHeader struct {
	Kind           PointKind
	LatitudeBlock  uint32
	LongitudeBlock uint32
	Rows           uint16
	Columns        uint16
	PayloadSHA256  string
}

// PointAssetDescriptor binds a read to one exact catalogue asset and block.
type PointAssetDescriptor struct {
	Kind           PointKind
	LatitudeBlock  uint32
	LongitudeBlock uint32
	SHA256         string
	PayloadSHA256  string
}

func (block PointBlock) Descriptor() PointAssetDescriptor {
	return PointAssetDescriptor{
		Kind: PointKindMain, LatitudeBlock: uint32(block.LatitudeBlock),
		LongitudeBlock: uint32(block.LongitudeBlock), SHA256: block.SHA256,
		PayloadSHA256: block.PayloadSHA256,
	}
}

func (block DatelineBlock) Descriptor() PointAssetDescriptor {
	return PointAssetDescriptor{
		Kind: PointKindDateline, LatitudeBlock: uint32(block.LatitudeBlock),
		SHA256: block.SHA256, PayloadSHA256: block.PayloadSHA256,
	}
}

// ParsePointHeader validates one fixed ERA5 climate point-block header.
func ParsePointHeader(encoded []byte) (PointHeader, error) {
	if len(encoded) != pointHeaderBytes {
		return PointHeader{}, fmt.Errorf("ERA5 climate point header has %d bytes; want %d", len(encoded), pointHeaderBytes)
	}
	var kind PointKind
	switch {
	case bytes.Equal(encoded[:8], mainPointMagic[:]):
		kind = PointKindMain
	case bytes.Equal(encoded[:8], overridePointMagic[:]):
		kind = PointKindDateline
	default:
		return PointHeader{}, errors.New("ERA5 climate point header has unknown magic")
	}
	if binary.LittleEndian.Uint16(encoded[8:10]) != 1 ||
		binary.LittleEndian.Uint16(encoded[10:12]) != pointHeaderBytes ||
		binary.LittleEndian.Uint16(encoded[12:14]) != delivery.ClimatePointRecordBytes ||
		!allZeroBytes(encoded[18:20]) || !allZeroBytes(encoded[28:32]) {
		return PointHeader{}, errors.New("ERA5 climate point header contract is invalid")
	}
	header := PointHeader{
		Kind:           kind,
		Rows:           binary.LittleEndian.Uint16(encoded[14:16]),
		Columns:        binary.LittleEndian.Uint16(encoded[16:18]),
		LatitudeBlock:  binary.LittleEndian.Uint32(encoded[20:24]),
		LongitudeBlock: binary.LittleEndian.Uint32(encoded[24:28]),
		PayloadSHA256:  hex.EncodeToString(encoded[32:64]),
	}
	if err := validatePointHeaderValue(header); err != nil {
		return PointHeader{}, err
	}
	return header, nil
}

// PointRecordByteRange returns the exact byte range for one record.
func PointRecordByteRange(header PointHeader, recordIndex int) (offset, length int64, err error) {
	if err := validatePointHeaderValue(header); err != nil {
		return 0, 0, err
	}
	count := int(header.Rows) * int(header.Columns)
	if count < 1 || recordIndex < 0 || recordIndex >= count {
		return 0, 0, errors.New("ERA5 climate point record index is outside its block")
	}
	return int64(pointHeaderBytes + recordIndex*delivery.ClimatePointRecordBytes),
		delivery.ClimatePointRecordBytes, nil
}

func validatePointHeaderValue(header PointHeader) error {
	if !validSHA256(header.PayloadSHA256) ||
		header.LatitudeBlock >= (Rows+PointBlockRows-1)/PointBlockRows {
		return errors.New("ERA5 climate point header identity is invalid")
	}
	switch header.Kind {
	case PointKindMain:
		if header.Rows != PointBlockRows || header.Columns != PointBlockCols ||
			header.LongitudeBlock >= Columns/PointBlockCols {
			return errors.New("ERA5 climate main point header geometry is invalid")
		}
	case PointKindDateline:
		if header.Rows != PointBlockRows || header.Columns != 1 || header.LongitudeBlock != 0 {
			return errors.New("ERA5 climate dateline point header geometry is invalid")
		}
	default:
		return errors.New("ERA5 climate point header kind is invalid")
	}
	return nil
}

// ReadPointRecord validates the complete immutable asset before decoding one
// fixed record.
func ReadPointRecord(path string, expected PointAssetDescriptor, recordIndex int) (delivery.ClimatePointRecord, bool, error) {
	header, payload, err := readPointAsset(path, expected.SHA256)
	if err != nil {
		return delivery.ClimatePointRecord{}, false, err
	}
	if header.Kind != expected.Kind || header.LatitudeBlock != expected.LatitudeBlock ||
		header.LongitudeBlock != expected.LongitudeBlock ||
		header.PayloadSHA256 != expected.PayloadSHA256 {
		return delivery.ClimatePointRecord{}, false, errors.New("ERA5 climate point asset does not match the expected block")
	}
	offset, length, err := PointRecordByteRange(header, recordIndex)
	if err != nil {
		return delivery.ClimatePointRecord{}, false, err
	}
	start := int(offset) - pointHeaderBytes
	record, err := delivery.DecodeClimatePointRecord(payload[start : start+int(length)])
	return record, record.Distribution.Valid, err
}

func readPointAsset(path, expectedSHA256 string) (PointHeader, []byte, error) {
	if !validSHA256(expectedSHA256) {
		return PointHeader{}, nil, errors.New("ERA5 climate point asset requires an expected SHA-256")
	}
	file, err := os.Open(path)
	if err != nil {
		return PointHeader{}, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return PointHeader{}, nil, err
	}
	maximum := int64(pointHeaderBytes + PointBlockRows*PointBlockCols*delivery.ClimatePointRecordBytes)
	if !info.Mode().IsRegular() || info.Size() < pointHeaderBytes || info.Size() > maximum {
		return PointHeader{}, nil, errors.New("ERA5 climate point asset size is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return PointHeader{}, nil, err
	}
	if int64(len(body)) != info.Size() {
		return PointHeader{}, nil, errors.New("ERA5 climate point asset changed while it was read")
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return PointHeader{}, nil, errors.New("ERA5 climate point asset checksum mismatch")
	}
	header, err := ParsePointHeader(body[:pointHeaderBytes])
	if err != nil {
		return PointHeader{}, nil, err
	}
	wantBytes := pointHeaderBytes + int(header.Rows)*int(header.Columns)*delivery.ClimatePointRecordBytes
	if len(body) != wantBytes {
		return PointHeader{}, nil, fmt.Errorf("ERA5 climate point asset has %d bytes; want %d", len(body), wantBytes)
	}
	payload := body[pointHeaderBytes:]
	payloadDigest := sha256.Sum256(payload)
	if hex.EncodeToString(payloadDigest[:]) != header.PayloadSHA256 {
		return PointHeader{}, nil, errors.New("ERA5 climate point payload checksum mismatch")
	}
	return header, payload, nil
}

func allZeroBytes(values []byte) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
}
