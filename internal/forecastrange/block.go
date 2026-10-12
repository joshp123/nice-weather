package forecastrange

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"nice-weather-finder/internal/forecasttiles"
	"os"
	"path/filepath"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/tiledelivery"
)

const (
	BlockVersion     = uint16(1)
	BlockRows        = uint16(64)
	BlockColumns     = uint16(64)
	BlockHeaderBytes = 256
	BlockBytes       = uint64(BlockHeaderBytes + int(BlockRows)*int(BlockColumns)*delivery.RangePointRecordBytes)
	BlockMediaType   = "application/vnd.nice-weather.forecast-range-block"
	worldPixels      = uint32(tiledelivery.TileSize) * (uint32(1) << MaxZoom)
	blockCount       = worldPixels / uint32(BlockColumns)

	releaseIDBytes        = 64
	periodIDBytes         = 32
	tierIDBytes           = 32
	fixedBlockHeaderBytes = 64
	payloadDigestOffset   = fixedBlockHeaderBytes + releaseIDBytes + periodIDBytes + tierIDBytes
)

var blockMagic = [8]byte{'N', 'W', 'F', 'R', 'A', 'N', 'G', 'E'}

type BlockIdentity struct {
	ReleaseID string
	Period    string
	Tier      string
	Row       uint32
	Column    uint32
}

type BlockAsset struct {
	Identity      BlockIdentity
	Path          string
	Bytes         uint64
	SHA256        string
	PayloadSHA256 string
	MediaType     string
	Omitted       bool
	DataRecords   uint32
	NoDataRecords uint32
}

type Block struct {
	meta    BlockAsset
	records []delivery.RangePointRecord
}

func NewBlockIdentity(releaseID string, row, column uint32) (BlockIdentity, error) {
	return NewTierBlockIdentity(releaseID, forecasttiles.TierGlobal, row, column)
}

func NewTierBlockIdentity(releaseID, tier string, row, column uint32) (BlockIdentity, error) {
	identity := BlockIdentity{ReleaseID: releaseID, Period: Period, Tier: tier, Row: row, Column: column}
	count := pixelsForTier(tier) / uint32(BlockColumns)
	if !validIdentifier(releaseID) || (tier != forecasttiles.TierGlobal && tier != forecasttiles.TierEurope) || row >= count || column >= count {
		return BlockIdentity{}, errors.New("forecast range block identity is invalid")
	}
	return identity, nil
}

func BlockPath(row, column uint32) string {
	return TierBlockPath(forecasttiles.TierGlobal, row, column)
}

func TierBlockPath(tier string, row, column uint32) string {
	return filepath.ToSlash(filepath.Join("points", Period, tier, fmt.Sprintf("r%02d-c%02d.nwr", row, column)))
}

func WriteBlock(root string, identity BlockIdentity, records []delivery.RangePointRecord) (BlockAsset, error) {
	canonical, err := NewTierBlockIdentity(identity.ReleaseID, identity.Tier, identity.Row, identity.Column)
	if err != nil || canonical != identity || root == "" || !filepath.IsAbs(root) {
		return BlockAsset{}, errors.New("forecast range block writer identity or root is invalid")
	}
	if len(records) != int(BlockRows)*int(BlockColumns) {
		return BlockAsset{}, fmt.Errorf("forecast range block has %d records; want %d", len(records), int(BlockRows)*int(BlockColumns))
	}
	asset := BlockAsset{Identity: identity, MediaType: BlockMediaType}
	payload := make([]byte, len(records)*delivery.RangePointRecordBytes)
	for index, record := range records {
		start := index * delivery.RangePointRecordBytes
		if err := delivery.EncodeRangePointRecord(payload[start:start+delivery.RangePointRecordBytes], record); err != nil {
			return BlockAsset{}, fmt.Errorf("forecast range block record %d: %w", index, err)
		}
		if record.Nice.Valid {
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
	encoded := append(encodeBlockHeader(identity, payloadDigest), payload...)
	wholeDigest := sha256.Sum256(encoded)
	relative := TierBlockPath(identity.Tier, identity.Row, identity.Column)
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return BlockAsset{}, err
	}
	if err := writeImmutable(path, encoded, ".range-block-*.tmp"); err != nil {
		return BlockAsset{}, err
	}
	asset.Path, asset.Bytes = path, uint64(len(encoded))
	asset.SHA256, asset.PayloadSHA256 = hex.EncodeToString(wholeDigest[:]), hex.EncodeToString(payloadDigest[:])
	return asset, nil
}

func OpenBlock(path string, expected BlockIdentity, expectedSHA256 string) (*Block, error) {
	canonical, err := NewTierBlockIdentity(expected.ReleaseID, expected.Tier, expected.Row, expected.Column)
	if err != nil || canonical != expected || !validSHA256(expectedSHA256) {
		return nil, errors.New("expected forecast range block identity or checksum is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(BlockBytes) {
		return nil, errors.New("forecast range block is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	header := make([]byte, BlockHeaderBytes)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, err
	}
	identity, payloadWant, err := decodeBlockHeader(header)
	if err != nil {
		return nil, err
	}
	if identity != expected {
		return nil, errors.New("forecast range block identity does not match its catalogue")
	}
	payload := make([]byte, int(BlockBytes)-BlockHeaderBytes)
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, err
	}
	payloadGot := sha256.Sum256(payload)
	if payloadGot != payloadWant {
		return nil, errors.New("forecast range block payload checksum mismatch")
	}
	whole := sha256.New()
	_, _ = whole.Write(header)
	_, _ = whole.Write(payload)
	if hex.EncodeToString(whole.Sum(nil)) != expectedSHA256 {
		return nil, errors.New("forecast range block whole-file checksum mismatch")
	}
	records := make([]delivery.RangePointRecord, int(BlockRows)*int(BlockColumns))
	asset := BlockAsset{Identity: identity, Path: path, Bytes: BlockBytes, SHA256: expectedSHA256, PayloadSHA256: hex.EncodeToString(payloadGot[:]), MediaType: BlockMediaType}
	for index := range records {
		start := index * delivery.RangePointRecordBytes
		record, err := delivery.DecodeRangePointRecord(payload[start : start+delivery.RangePointRecordBytes])
		if err != nil {
			return nil, fmt.Errorf("forecast range block record %d: %w", index, err)
		}
		records[index] = record
		if record.Nice.Valid {
			asset.DataRecords++
		} else {
			asset.NoDataRecords++
		}
	}
	if asset.DataRecords == 0 {
		return nil, errors.New("all-no-data forecast range block must be omitted")
	}
	return &Block{meta: asset, records: records}, nil
}

// RecordAt uses canonical XYZ geometry: longitude wraps at the antimeridian,
// while latitude outside Web Mercator is rejected and never wraps.
func (block *Block) RecordAt(longitude, latitude float64) (delivery.RangePointRecord, bool, error) {
	if block == nil || len(block.records) == 0 {
		return delivery.RangePointRecord{}, false, errors.New("forecast range block is unavailable")
	}
	id, row, column, err := tiledelivery.At(block.meta.Identity.Tier, zoomForTier(block.meta.Identity.Tier), longitude, latitude)
	if err != nil {
		return delivery.RangePointRecord{}, false, err
	}
	globalRow := id.Y*tiledelivery.TileSize + uint32(row)
	globalColumn := id.X*tiledelivery.TileSize + uint32(column)
	if globalRow/uint32(BlockRows) != block.meta.Identity.Row || globalColumn/uint32(BlockColumns) != block.meta.Identity.Column {
		return delivery.RangePointRecord{}, false, nil
	}
	record := block.records[(globalRow%uint32(BlockRows))*uint32(BlockColumns)+globalColumn%uint32(BlockColumns)]
	return record, record.Nice.Valid, nil
}

func (block *Block) Metadata() BlockAsset {
	if block == nil {
		return BlockAsset{}
	}
	return block.meta
}

func encodeBlockHeader(identity BlockIdentity, payload [sha256.Size]byte) []byte {
	header := make([]byte, BlockHeaderBytes)
	copy(header[:8], blockMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], BlockVersion)
	binary.LittleEndian.PutUint16(header[10:12], BlockHeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], delivery.RangePointRecordBytes)
	binary.LittleEndian.PutUint16(header[14:16], BlockRows)
	binary.LittleEndian.PutUint16(header[16:18], BlockColumns)
	header[18], header[19] = zoomForTier(identity.Tier), 1
	binary.LittleEndian.PutUint32(header[20:24], identity.Row)
	binary.LittleEndian.PutUint32(header[24:28], identity.Column)
	binary.LittleEndian.PutUint32(header[28:32], pixelsForTier(identity.Tier))
	binary.LittleEndian.PutUint32(header[32:36], pixelsForTier(identity.Tier))
	header[36], header[37], header[38] = byte(len(identity.ReleaseID)), byte(len(identity.Period)), byte(len(identity.Tier))
	releaseStart := fixedBlockHeaderBytes
	periodStart := releaseStart + releaseIDBytes
	tierStart := periodStart + periodIDBytes
	copy(header[releaseStart:periodStart], identity.ReleaseID)
	copy(header[periodStart:tierStart], identity.Period)
	copy(header[tierStart:payloadDigestOffset], identity.Tier)
	copy(header[payloadDigestOffset:payloadDigestOffset+sha256.Size], payload[:])
	return header
}

func decodeBlockHeader(header []byte) (BlockIdentity, [sha256.Size]byte, error) {
	if len(header) != BlockHeaderBytes || string(header[:8]) != string(blockMagic[:]) || binary.LittleEndian.Uint16(header[8:10]) != BlockVersion || binary.LittleEndian.Uint16(header[10:12]) != BlockHeaderBytes || binary.LittleEndian.Uint16(header[12:14]) != delivery.RangePointRecordBytes || binary.LittleEndian.Uint16(header[14:16]) != BlockRows || binary.LittleEndian.Uint16(header[16:18]) != BlockColumns || (header[18] != MaxZoom && header[18] != RegionalMaxZoom) || header[19] != 1 || binary.LittleEndian.Uint32(header[28:32]) != uint32(tiledelivery.TileSize)<<header[18] || binary.LittleEndian.Uint32(header[32:36]) != uint32(tiledelivery.TileSize)<<header[18] || !allZero(header[39:fixedBlockHeaderBytes]) || !allZero(header[payloadDigestOffset+sha256.Size:]) {
		return BlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid forecast range block header")
	}
	releaseLength, periodLength, tierLength := int(header[36]), int(header[37]), int(header[38])
	if releaseLength > releaseIDBytes || periodLength > periodIDBytes || tierLength > tierIDBytes {
		return BlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid forecast range block identity length")
	}
	releaseStart := fixedBlockHeaderBytes
	periodStart := releaseStart + releaseIDBytes
	tierStart := periodStart + periodIDBytes
	identity := BlockIdentity{ReleaseID: string(header[releaseStart : releaseStart+releaseLength]), Period: string(header[periodStart : periodStart+periodLength]), Tier: string(header[tierStart : tierStart+tierLength]), Row: binary.LittleEndian.Uint32(header[20:24]), Column: binary.LittleEndian.Uint32(header[24:28])}
	canonical, err := NewTierBlockIdentity(identity.ReleaseID, identity.Tier, identity.Row, identity.Column)
	if err != nil || canonical != identity || header[18] != zoomForTier(identity.Tier) || !allZero(header[releaseStart+releaseLength:periodStart]) || !allZero(header[periodStart+periodLength:tierStart]) || !allZero(header[tierStart+tierLength:payloadDigestOffset]) {
		return BlockIdentity{}, [sha256.Size]byte{}, errors.New("invalid forecast range block identity")
	}
	var payload [sha256.Size]byte
	copy(payload[:], header[payloadDigestOffset:payloadDigestOffset+sha256.Size])
	return identity, payload, nil
}

func writeImmutable(path string, data []byte, pattern string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("immutable forecast range asset already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func zoomForTier(tier string) uint8 {
	if tier == forecasttiles.TierEurope {
		return RegionalMaxZoom
	}
	return MaxZoom
}
func pixelsForTier(tier string) uint32 { return uint32(tiledelivery.TileSize) << zoomForTier(tier) }
