package tiledelivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
)

const (
	// ClimateBlockHeaderBytes is the first range needed by a static client.
	// One second range of ClimateDistributionRecordBytes then returns the
	// selected source-grid record.
	ClimateBlockHeaderBytes = 160
	ClimateBlockVersion     = 1
	ClimateBlockMediaType   = "application/vnd.nice-weather.climate-block"

	climateFixedHeaderBytes = 32
	climateReleaseIDBytes   = 64
	climatePeriodIDBytes    = 32
	climateGridVersion      = 1 // native AgERA5 0.1 degree latitude/longitude grid
	climateRowMajorLayout   = 1 // latitude row, then longitude column
)

var (
	climateBlockMagic  = [8]byte{'N', 'W', 'A', 'G', '5', 'C', 'L', 'M'}
	climateMonthPeriod = regexp.MustCompile(`^month-(0[1-9]|1[0-2])$`)
)

// ClimateBlockIdentity binds one immutable native AgERA5 block to the
// product release and calendar month it belongs to. LatitudeCount and
// LongitudeCount are the logical source dimensions; boundary blocks are
// smaller than AgERA5's physical Zarr chunk.
type ClimateBlockIdentity struct {
	ReleaseID      string
	Period         string
	Block          agera5.BlockCoordinate
	LatitudeCount  uint16
	LongitudeCount uint16
}

// ClimateBlockAsset is the complete static-delivery identity of one block.
// SHA256 covers the whole file. PayloadSHA256 covers only its fixed records
// and is also embedded in the header.
type ClimateBlockAsset struct {
	Identity      ClimateBlockIdentity
	Path          string
	Bytes         uint64
	SHA256        string
	PayloadSHA256 string
	MediaType     string
	DataRecords   uint16
	NoDataRecords uint16
	Omitted       bool
}

// ClimateByteRange is an exact HTTP byte range expressed as offset and
// length. EndInclusive is provided because HTTP Range uses inclusive ends.
type ClimateByteRange struct {
	Offset int64
	Length int64
}

func (byteRange ClimateByteRange) EndInclusive() int64 {
	return byteRange.Offset + byteRange.Length - 1
}

// ClimateHeaderRange is the first request in the browser/CDN two-range read.
func ClimateHeaderRange() ClimateByteRange {
	return ClimateByteRange{Offset: 0, Length: ClimateBlockHeaderBytes}
}

type ClimateBlockReader struct {
	file *os.File
	meta ClimateBlockAsset
}

// NewClimateBlockIdentity derives and validates the exact logical dimensions
// for one AgERA5 block. Period is one canonical month-01 to month-12 ID.
func NewClimateBlockIdentity(releaseID, period string, block agera5.BlockCoordinate) (ClimateBlockIdentity, error) {
	if !validIdentifier(releaseID, climateReleaseIDBytes) {
		return ClimateBlockIdentity{}, errors.New("climate block release ID is invalid")
	}
	if !climateMonthPeriod.MatchString(period) {
		return ClimateBlockIdentity{}, errors.New("climate block period is not a canonical month ID")
	}
	latitudeCount, longitudeCount, err := agera5.BlockShape(block)
	if err != nil {
		return ClimateBlockIdentity{}, err
	}
	return ClimateBlockIdentity{
		ReleaseID: releaseID, Period: period, Block: block,
		LatitudeCount: uint16(latitudeCount), LongitudeCount: uint16(longitudeCount),
	}, nil
}

// WriteClimateBlock writes one complete native AgERA5 climate block to the
// supplied path. Path choice and release publication remain the caller's
// concern. The completed inode is fsynced and linked without replacement.
// An all-no-data block is omitted and creates no directory or file.
func WriteClimateBlock(path, releaseID, period string, block agera5.BlockCoordinate, records []delivery.ClimateDistributionRecord) (ClimateBlockAsset, error) {
	identity, err := NewClimateBlockIdentity(releaseID, period, block)
	if err != nil {
		return ClimateBlockAsset{}, err
	}
	if path == "" {
		return ClimateBlockAsset{}, errors.New("climate block path is required")
	}
	wantRecords := int(identity.LatitudeCount) * int(identity.LongitudeCount)
	if len(records) != wantRecords {
		return ClimateBlockAsset{}, fmt.Errorf("climate block has %d records; want %d", len(records), wantRecords)
	}
	asset := ClimateBlockAsset{Identity: identity, MediaType: ClimateBlockMediaType}
	if _, err := os.Stat(path); err == nil {
		return ClimateBlockAsset{}, fmt.Errorf("immutable climate block already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ClimateBlockAsset{}, err
	}

	payload := make([]byte, wantRecords*delivery.ClimateDistributionRecordBytes)
	for index, record := range records {
		start := index * delivery.ClimateDistributionRecordBytes
		if err := delivery.EncodeClimateDistributionRecord(payload[start:start+delivery.ClimateDistributionRecordBytes], record); err != nil {
			return ClimateBlockAsset{}, fmt.Errorf("climate block record %d: %w", index, err)
		}
		if record.Valid {
			asset.DataRecords++
		} else {
			asset.NoDataRecords++
		}
	}
	if asset.DataRecords == 0 {
		asset.Omitted = true
		return asset, nil
	}

	payloadDigest := sha256.Sum256(payload)
	header := encodeClimateHeader(identity, payloadDigest)
	data := append(header, payload...)
	wholeDigest := sha256.Sum256(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ClimateBlockAsset{}, err
	}
	if err := writeImmutable(path, data); err != nil {
		return ClimateBlockAsset{}, err
	}
	asset.Path = path
	asset.Bytes = uint64(len(data))
	asset.SHA256 = hex.EncodeToString(wholeDigest[:])
	asset.PayloadSHA256 = hex.EncodeToString(payloadDigest[:])
	return asset, nil
}

// OpenClimateBlock validates the exact expected identity, whole-file digest,
// embedded payload digest, file size and every encoded record before exposing
// random access.
func OpenClimateBlock(path string, expected ClimateBlockIdentity, expectedSHA256 string) (*ClimateBlockReader, error) {
	if err := validateClimateIdentity(expected); err != nil {
		return nil, fmt.Errorf("expected climate block identity: %w", err)
	}
	if !validSHA256(expectedSHA256) {
		return nil, errors.New("climate block requires an expected SHA-256")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	meta, err := validateClimateBlockFile(file, expected, expectedSHA256)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &ClimateBlockReader{file: file, meta: meta}, nil
}

// RecordRange returns the second request in the browser/CDN two-range read.
// A coordinate must be an exact AgERA5 0.1 degree source-grid point. A valid
// source coordinate outside this block returns ok=false.
func (reader *ClimateBlockReader) RecordRange(longitude, latitude float64) (byteRange ClimateByteRange, ok bool, err error) {
	if reader == nil || reader.file == nil {
		return ClimateByteRange{}, false, errors.New("climate block reader is closed")
	}
	return ClimateRecordRange(reader.meta.Identity, longitude, latitude)
}

// ClimateRecordRange resolves the exact second range without opening the
// block. Static publication and browser conformance tests can therefore share
// the same native-grid offset contract.
func ClimateRecordRange(identity ClimateBlockIdentity, longitude, latitude float64) (byteRange ClimateByteRange, ok bool, err error) {
	if err := validateClimateIdentity(identity); err != nil {
		return ClimateByteRange{}, false, err
	}
	index, ok, err := climateRecordIndex(identity, longitude, latitude)
	if err != nil || !ok {
		return ClimateByteRange{}, ok, err
	}
	return ClimateByteRange{
		Offset: ClimateBlockHeaderBytes + int64(index*delivery.ClimateDistributionRecordBytes),
		Length: delivery.ClimateDistributionRecordBytes,
	}, true, nil
}

// RecordAt reads and validates one exact native source-grid record.
func (reader *ClimateBlockReader) RecordAt(longitude, latitude float64) (delivery.ClimateDistributionRecord, bool, error) {
	byteRange, ok, err := reader.RecordRange(longitude, latitude)
	if err != nil || !ok {
		return delivery.ClimateDistributionRecord{}, ok, err
	}
	encoded := make([]byte, delivery.ClimateDistributionRecordBytes)
	if _, err := reader.file.ReadAt(encoded, byteRange.Offset); err != nil {
		return delivery.ClimateDistributionRecord{}, false, err
	}
	record, err := delivery.DecodeClimateDistributionRecord(encoded)
	return record, record.Valid, err
}

func (reader *ClimateBlockReader) Metadata() ClimateBlockAsset { return reader.meta }

func (reader *ClimateBlockReader) Close() error {
	if reader == nil || reader.file == nil {
		return nil
	}
	err := reader.file.Close()
	reader.file = nil
	return err
}

func encodeClimateHeader(identity ClimateBlockIdentity, payloadDigest [sha256.Size]byte) []byte {
	header := make([]byte, ClimateBlockHeaderBytes)
	copy(header[:8], climateBlockMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], ClimateBlockVersion)
	binary.LittleEndian.PutUint16(header[10:12], ClimateBlockHeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], delivery.ClimateDistributionRecordBytes)
	binary.LittleEndian.PutUint16(header[14:16], identity.LatitudeCount)
	binary.LittleEndian.PutUint16(header[16:18], identity.LongitudeCount)
	binary.LittleEndian.PutUint16(header[18:20], uint16(identity.Block.Latitude))
	binary.LittleEndian.PutUint16(header[20:22], uint16(identity.Block.Longitude))
	header[22], header[23] = byte(len(identity.ReleaseID)), byte(len(identity.Period))
	header[24], header[25] = climateGridVersion, climateRowMajorLayout
	releaseStart := climateFixedHeaderBytes
	periodStart := releaseStart + climateReleaseIDBytes
	digestStart := periodStart + climatePeriodIDBytes
	copy(header[releaseStart:periodStart], identity.ReleaseID)
	copy(header[periodStart:digestStart], identity.Period)
	copy(header[digestStart:], payloadDigest[:])
	return header
}

func decodeClimateHeader(header []byte) (ClimateBlockIdentity, [sha256.Size]byte, error) {
	if len(header) != ClimateBlockHeaderBytes {
		return ClimateBlockIdentity{}, [sha256.Size]byte{}, errors.New("climate block header has invalid size")
	}
	if string(header[:8]) != string(climateBlockMagic[:]) ||
		binary.LittleEndian.Uint16(header[8:10]) != ClimateBlockVersion ||
		binary.LittleEndian.Uint16(header[10:12]) != ClimateBlockHeaderBytes ||
		binary.LittleEndian.Uint16(header[12:14]) != delivery.ClimateDistributionRecordBytes ||
		header[24] != climateGridVersion || header[25] != climateRowMajorLayout ||
		!allZero(header[26:climateFixedHeaderBytes]) {
		return ClimateBlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid climate block header")
	}
	releaseLength, periodLength := int(header[22]), int(header[23])
	if releaseLength > climateReleaseIDBytes || periodLength > climatePeriodIDBytes {
		return ClimateBlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid climate block identity length")
	}
	releaseStart := climateFixedHeaderBytes
	periodStart := releaseStart + climateReleaseIDBytes
	digestStart := periodStart + climatePeriodIDBytes
	identity := ClimateBlockIdentity{
		ReleaseID: string(header[releaseStart : releaseStart+releaseLength]),
		Period:    string(header[periodStart : periodStart+periodLength]),
		Block: agera5.BlockCoordinate{
			Latitude:  int(binary.LittleEndian.Uint16(header[18:20])),
			Longitude: int(binary.LittleEndian.Uint16(header[20:22])),
		},
		LatitudeCount:  binary.LittleEndian.Uint16(header[14:16]),
		LongitudeCount: binary.LittleEndian.Uint16(header[16:18]),
	}
	if !allZero(header[releaseStart+releaseLength:periodStart]) || !allZero(header[periodStart+periodLength:digestStart]) {
		return ClimateBlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid climate block identity padding")
	}
	if err := validateClimateIdentity(identity); err != nil {
		return ClimateBlockIdentity{}, [sha256.Size]byte{}, err
	}
	var payloadDigest [sha256.Size]byte
	copy(payloadDigest[:], header[digestStart:])
	return identity, payloadDigest, nil
}

func validateClimateIdentity(identity ClimateBlockIdentity) error {
	canonical, err := NewClimateBlockIdentity(identity.ReleaseID, identity.Period, identity.Block)
	if err != nil {
		return err
	}
	if identity != canonical {
		return errors.New("climate block logical dimensions do not match AgERA5")
	}
	return nil
}

func validateClimateBlockFile(file *os.File, expected ClimateBlockIdentity, expectedSHA256 string) (ClimateBlockAsset, error) {
	header := make([]byte, ClimateBlockHeaderBytes)
	if _, err := file.ReadAt(header, 0); err != nil {
		return ClimateBlockAsset{}, err
	}
	identity, embeddedPayloadDigest, err := decodeClimateHeader(header)
	if err != nil {
		return ClimateBlockAsset{}, err
	}
	if identity != expected {
		return ClimateBlockAsset{}, errors.New("climate block identity does not match expected release, period or source block")
	}
	recordCount := int(identity.LatitudeCount) * int(identity.LongitudeCount)
	wantBytes := int64(ClimateBlockHeaderBytes + recordCount*delivery.ClimateDistributionRecordBytes)
	info, err := file.Stat()
	if err != nil {
		return ClimateBlockAsset{}, err
	}
	if info.Size() != wantBytes {
		return ClimateBlockAsset{}, fmt.Errorf("climate block has %d bytes; want %d", info.Size(), wantBytes)
	}

	payloadDigest := sha256.New()
	wholeDigest := sha256.New()
	if _, err := wholeDigest.Write(header); err != nil {
		return ClimateBlockAsset{}, err
	}
	copied, err := io.Copy(io.MultiWriter(payloadDigest, wholeDigest), io.NewSectionReader(file, ClimateBlockHeaderBytes, wantBytes-ClimateBlockHeaderBytes))
	if err != nil {
		return ClimateBlockAsset{}, err
	}
	if copied != wantBytes-ClimateBlockHeaderBytes {
		return ClimateBlockAsset{}, errors.New("climate block payload is truncated")
	}
	if got := payloadDigest.Sum(nil); string(got) != string(embeddedPayloadDigest[:]) {
		return ClimateBlockAsset{}, errors.New("climate block payload checksum mismatch")
	}
	wholeSHA256 := hex.EncodeToString(wholeDigest.Sum(nil))
	if wholeSHA256 != expectedSHA256 {
		return ClimateBlockAsset{}, errors.New("climate block whole-file checksum mismatch")
	}

	encoded := make([]byte, delivery.ClimateDistributionRecordBytes)
	var dataRecords, noDataRecords uint16
	for index := 0; index < recordCount; index++ {
		offset := int64(ClimateBlockHeaderBytes + index*delivery.ClimateDistributionRecordBytes)
		if _, err := file.ReadAt(encoded, offset); err != nil {
			return ClimateBlockAsset{}, err
		}
		record, err := delivery.DecodeClimateDistributionRecord(encoded)
		if err != nil {
			return ClimateBlockAsset{}, fmt.Errorf("climate block record %d: %w", index, err)
		}
		if record.Valid {
			dataRecords++
		} else {
			noDataRecords++
		}
	}
	if dataRecords == 0 {
		return ClimateBlockAsset{}, errors.New("all-no-data climate block must be omitted")
	}
	return ClimateBlockAsset{
		Identity: identity, Path: file.Name(), Bytes: uint64(wantBytes),
		SHA256: wholeSHA256, PayloadSHA256: hex.EncodeToString(embeddedPayloadDigest[:]),
		MediaType: ClimateBlockMediaType, DataRecords: dataRecords, NoDataRecords: noDataRecords,
	}, nil
}

func climateRecordIndex(identity ClimateBlockIdentity, longitude, latitude float64) (int, bool, error) {
	if !finite(longitude) || !finite(latitude) {
		return 0, false, errors.New("climate coordinate is not finite")
	}
	longitudeIndex, exact := exactTenthIndex(longitude, -180, 3600)
	if !exact {
		return 0, false, errors.New("longitude is not an exact AgERA5 source-grid coordinate")
	}
	latitudeIndex, exact := exactTenthIndex(latitude, -90, 1801)
	if !exact {
		return 0, false, errors.New("latitude is not an exact AgERA5 source-grid coordinate")
	}
	firstLatitude := identity.Block.Latitude * 16
	firstLongitude := identity.Block.Longitude * 32
	row, column := latitudeIndex-firstLatitude, longitudeIndex-firstLongitude
	if row < 0 || column < 0 || row >= int(identity.LatitudeCount) || column >= int(identity.LongitudeCount) {
		return 0, false, nil
	}
	return row*int(identity.LongitudeCount) + column, true, nil
}

func exactTenthIndex(value, origin float64, count int) (int, bool) {
	scaled := (value - origin) * 10
	nearest := math.Round(scaled)
	if math.Abs(scaled-nearest) > 1e-9 || nearest < 0 || nearest >= float64(count) {
		return 0, false
	}
	return int(nearest), true
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
