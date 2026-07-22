// Package forecastpoints stores canonical forecast-day records in small
// immutable blocks on each provider's native latitude-longitude grid. It owns
// no provider selection, scoring, release catalogue or frontend policy.
package forecastpoints

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"nice-weather-finder/internal/delivery"
)

const (
	HeaderBytes = 320
	Version     = 2
	MediaType   = "application/vnd.nice-weather.forecast-block"

	ProviderIFS    = "ecmwf-ifs"
	TierGlobal     = "global"
	ProviderICONEU = "dwd-icon-eu"
	TierEurope     = "europe-detail"

	// MaxBytes keeps one selected block cheap to fetch and verify whole.
	MaxBytes = 128 << 10

	fixedHeaderBytes   = 80
	releaseIDBytes     = 64
	providerIDBytes    = 24
	tierIDBytes        = 24
	dateIDBytes        = 16
	runIDBytes         = 24
	runStart           = fixedHeaderBytes + releaseIDBytes + providerIDBytes + tierIDBytes + dateIDBytes
	sourceDigestStart  = runStart + runIDBytes
	payloadDigestStart = sourceDigestStart + sha256.Size

	rowMajorLayout = 1
	gridIFS        = 1
	gridICONEU     = 2

	// Both tiers use the same record-count block. One selected point therefore
	// has the same bounded transfer cost despite different native resolutions.
	nativeBlockRows = 64
	nativeBlockCols = 64
)

var (
	blockMagic        = [8]byte{'N', 'W', 'F', 'C', 'S', 'T', 'P', 'T'}
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	datePattern       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	ifsGrid           = delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: 720, Cols: 1440}
	iconGrid          = delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}, Rows: 680, Cols: 1104}
)

// Coordinate addresses one native-grid block. Row zero is north; column zero
// is west. A boundary block can be smaller than the standard block shape.
type Coordinate struct {
	Row    uint32
	Column uint32
}

// Identity binds a block to one release, provider, tier, local forecast date
// and exact native grid. Rows and Columns are the clipped block dimensions.
type Identity struct {
	ReleaseID            string
	Provider             string
	Tier                 string
	Date                 string
	RunUTC               string
	SourceEvidenceSHA256 string
	Grid                 delivery.GridSpec
	Block                Coordinate
	Rows                 uint16
	Columns              uint16
}

// Asset is the complete static identity of one immutable native block.
// SHA256 covers the whole file. PayloadSHA256 covers the fixed record plane
// and is embedded in the header.
type Asset struct {
	Identity      Identity
	Path          string
	Bytes         uint64
	SHA256        string
	PayloadSHA256 string
	MediaType     string
	DataRecords   uint32
	NoDataRecords uint32
	Omitted       bool
}

// ByteRange describes one exact byte span. A client must fetch WholeRange and
// verify the whole-file SHA-256 before it trusts a record. HeaderRange and
// RecordRange expose deterministic parser offsets, not independently
// authenticated network reads.
type ByteRange struct {
	Offset int64
	Length int64
}

func (r ByteRange) EndInclusive() int64 { return r.Offset + r.Length - 1 }

func HeaderRange() ByteRange { return ByteRange{Offset: 0, Length: HeaderBytes} }

// WholeRange is the one HTTP Range a static client needs for this block.
func WholeRange(identity Identity) (ByteRange, error) {
	if err := validateIdentity(identity); err != nil {
		return ByteRange{}, err
	}
	length := HeaderBytes + int64(identity.Rows)*int64(identity.Columns)*delivery.DailyPointRecordBytes
	return ByteRange{Offset: 0, Length: length}, nil
}

// Block is a fully verified in-memory native point block.
type Block struct {
	meta    Asset
	payload []byte
}

// NewIdentity derives the exact clipped block shape and rejects provider or
// grid combinations that would imply resampling.
func NewIdentity(releaseID, provider, tier, date, runUTC, sourceEvidenceSHA256 string, grid delivery.GridSpec, block Coordinate) (Identity, error) {
	if !validIdentifier(releaseID, releaseIDBytes) {
		return Identity{}, errors.New("forecast block release ID is invalid")
	}
	if !validDate(date) {
		return Identity{}, errors.New("forecast block date is invalid")
	}
	if !validRunUTC(runUTC) || !validSHA256(sourceEvidenceSHA256) {
		return Identity{}, errors.New("forecast block run or source evidence is invalid")
	}
	_, blockRows, blockCols, err := nativeLayout(provider, tier, grid)
	if err != nil {
		return Identity{}, err
	}
	firstRow, firstCol := uint64(block.Row)*uint64(blockRows), uint64(block.Column)*uint64(blockCols)
	if firstRow >= uint64(grid.Rows) || firstCol >= uint64(grid.Cols) {
		return Identity{}, errors.New("forecast block coordinate is outside its native grid")
	}
	rows := min(uint64(blockRows), uint64(grid.Rows)-firstRow)
	cols := min(uint64(blockCols), uint64(grid.Cols)-firstCol)
	return Identity{
		ReleaseID: releaseID, Provider: provider, Tier: tier, Date: date, RunUTC: runUTC, SourceEvidenceSHA256: sourceEvidenceSHA256,
		Grid: grid, Block: block,
		Rows: uint16(rows), Columns: uint16(cols),
	}, nil
}

// BlockGrid returns the deterministic north-south and west-east block counts
// needed to enumerate one provider day.
func BlockGrid(provider, tier string, grid delivery.GridSpec) (rows, columns uint32, err error) {
	_, blockRows, blockCols, err := nativeLayout(provider, tier, grid)
	if err != nil {
		return 0, 0, err
	}
	rows = (grid.Rows + uint32(blockRows) - 1) / uint32(blockRows)
	columns = (grid.Cols + uint32(blockCols) - 1) / uint32(blockCols)
	return rows, columns, nil
}

// CellWindow returns the exact half-open native-grid window owned by one
// canonical block identity.
func CellWindow(identity Identity) (row, column, rows, columns uint32, err error) {
	if err := validateIdentity(identity); err != nil {
		return 0, 0, 0, 0, err
	}
	_, blockRows, blockColumns, _ := nativeLayout(identity.Provider, identity.Tier, identity.Grid)
	return identity.Block.Row * uint32(blockRows), identity.Block.Column * uint32(blockColumns), uint32(identity.Rows), uint32(identity.Columns), nil
}

// RelativePath is the deterministic path beneath an immutable release root.
func RelativePath(identity Identity) (string, error) {
	if err := validateIdentity(identity); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("forecast-points", identity.Date, identity.Provider, identity.Tier,
		fmt.Sprintf("r%03d-c%03d.nwf", identity.Block.Row, identity.Block.Column))), nil
}

// CoordinateIdentity returns the exact native block that owns a coordinate.
// It selects a source cell; it never interpolates or changes grid resolution.
func CoordinateIdentity(releaseID, provider, tier, date, runUTC, sourceEvidenceSHA256 string, grid delivery.GridSpec, longitude, latitude float64) (Identity, bool, error) {
	_, blockRows, blockCols, err := nativeLayout(provider, tier, grid)
	if err != nil {
		return Identity{}, false, err
	}
	row, col, ok, err := nativeCell(grid, provider == ProviderIFS && tier == TierGlobal, longitude, latitude)
	if err != nil || !ok {
		return Identity{}, ok, err
	}
	identity, err := NewIdentity(releaseID, provider, tier, date, runUTC, sourceEvidenceSHA256, grid, Coordinate{Row: row / uint32(blockRows), Column: col / uint32(blockCols)})
	return identity, err == nil, err
}

// WriteBlock atomically links one completed immutable block beneath root.
// All-no-data blocks are omitted without creating a directory or file.
func WriteBlock(root string, identity Identity, records []delivery.DailyPointRecord) (Asset, error) {
	if root == "" {
		return Asset{}, errors.New("forecast block root is required")
	}
	if err := validateIdentity(identity); err != nil {
		return Asset{}, err
	}
	relative, _ := RelativePath(identity)
	path := filepath.Join(root, filepath.FromSlash(relative))
	if _, err := os.Stat(path); err == nil {
		return Asset{}, fmt.Errorf("immutable forecast block already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Asset{}, err
	}
	wantRecords := int(identity.Rows) * int(identity.Columns)
	if len(records) != wantRecords {
		return Asset{}, fmt.Errorf("forecast block has %d records; want %d", len(records), wantRecords)
	}
	asset := Asset{Identity: identity, MediaType: MediaType}
	payload := make([]byte, wantRecords*delivery.DailyPointRecordBytes)
	for index, record := range records {
		start := index * delivery.DailyPointRecordBytes
		if err := delivery.EncodeDailyPointRecord(payload[start:start+delivery.DailyPointRecordBytes], record); err != nil {
			return Asset{}, fmt.Errorf("forecast block record %d: %w", index, err)
		}
		if record.Weather.Valid {
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
	data := append(encodeHeader(identity, payloadDigest), payload...)
	if len(data) > MaxBytes {
		return Asset{}, errors.New("forecast block exceeds the maximum block size")
	}
	wholeDigest := sha256.Sum256(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Asset{}, err
	}
	if err := writeImmutable(path, data); err != nil {
		return Asset{}, err
	}
	asset.Path = path
	asset.Bytes = uint64(len(data))
	asset.SHA256 = hex.EncodeToString(wholeDigest[:])
	asset.PayloadSHA256 = hex.EncodeToString(payloadDigest[:])
	return asset, nil
}

// OpenBlock reads and verifies one complete bounded block.
func OpenBlock(path string, expected Identity, expectedSHA256 string) (*Block, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < HeaderBytes || info.Size() > MaxBytes {
		return nil, errors.New("forecast block is not a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, err := ParseBlock(data, expected, expectedSHA256)
	if err != nil {
		return nil, err
	}
	block.meta.Path = path
	return block, nil
}

// ParseBlock verifies a fetched whole file before exposing any record.
func ParseBlock(data []byte, expected Identity, expectedSHA256 string) (*Block, error) {
	if err := validateIdentity(expected); err != nil {
		return nil, fmt.Errorf("expected forecast block identity: %w", err)
	}
	if !validSHA256(expectedSHA256) {
		return nil, errors.New("forecast block requires an expected SHA-256")
	}
	if len(data) < HeaderBytes || len(data) > MaxBytes {
		return nil, errors.New("forecast block byte size is outside its limit")
	}
	identity, embeddedPayloadDigest, err := decodeHeader(data[:HeaderBytes])
	if err != nil {
		return nil, err
	}
	if identity != expected {
		return nil, errors.New("forecast block identity does not match expected release, provider, tier, date, grid or coordinate")
	}
	wantBytes := HeaderBytes + int(identity.Rows)*int(identity.Columns)*delivery.DailyPointRecordBytes
	if len(data) != wantBytes {
		return nil, fmt.Errorf("forecast block has %d bytes; want %d", len(data), wantBytes)
	}
	payload := data[HeaderBytes:]
	payloadDigest := sha256.Sum256(payload)
	if payloadDigest != embeddedPayloadDigest {
		return nil, errors.New("forecast block payload checksum mismatch")
	}
	wholeDigest := sha256.Sum256(data)
	wholeSHA256 := hex.EncodeToString(wholeDigest[:])
	if wholeSHA256 != expectedSHA256 {
		return nil, errors.New("forecast block whole-file checksum mismatch")
	}
	asset := Asset{
		Identity: identity, Bytes: uint64(len(data)), SHA256: wholeSHA256,
		PayloadSHA256: hex.EncodeToString(payloadDigest[:]), MediaType: MediaType,
	}
	for index := 0; index < int(identity.Rows)*int(identity.Columns); index++ {
		start := index * delivery.DailyPointRecordBytes
		record, err := delivery.DecodeDailyPointRecord(payload[start : start+delivery.DailyPointRecordBytes])
		if err != nil {
			return nil, fmt.Errorf("forecast block record %d: %w", index, err)
		}
		if record.Weather.Valid {
			asset.DataRecords++
		} else {
			asset.NoDataRecords++
		}
	}
	if asset.DataRecords == 0 {
		return nil, errors.New("all-no-data forecast block must be omitted")
	}
	return &Block{meta: asset, payload: append([]byte(nil), payload...)}, nil
}

// RecordRange returns the fixed record offset for a coordinate owned by this
// block. Coordinates outside the block return ok=false.
func RecordRange(identity Identity, longitude, latitude float64) (ByteRange, bool, error) {
	if err := validateIdentity(identity); err != nil {
		return ByteRange{}, false, err
	}
	row, col, ok, err := nativeCell(identity.Grid, identity.Provider == ProviderIFS && identity.Tier == TierGlobal, longitude, latitude)
	if err != nil || !ok {
		return ByteRange{}, ok, err
	}
	_, blockRows, blockCols, _ := nativeLayout(identity.Provider, identity.Tier, identity.Grid)
	if row/uint32(blockRows) != identity.Block.Row || col/uint32(blockCols) != identity.Block.Column {
		return ByteRange{}, false, nil
	}
	localRow := row - identity.Block.Row*uint32(blockRows)
	localCol := col - identity.Block.Column*uint32(blockCols)
	index := uint64(localRow)*uint64(identity.Columns) + uint64(localCol)
	return ByteRange{Offset: HeaderBytes + int64(index)*delivery.DailyPointRecordBytes, Length: delivery.DailyPointRecordBytes}, true, nil
}

// RecordAt returns the exact native source-cell record for a coordinate.
func (block *Block) RecordAt(longitude, latitude float64) (delivery.DailyPointRecord, bool, error) {
	if block == nil || len(block.payload) == 0 {
		return delivery.DailyPointRecord{}, false, errors.New("forecast block is unavailable")
	}
	byteRange, ok, err := RecordRange(block.meta.Identity, longitude, latitude)
	if err != nil || !ok {
		return delivery.DailyPointRecord{}, ok, err
	}
	start := int(byteRange.Offset) - HeaderBytes
	record, err := delivery.DecodeDailyPointRecord(block.payload[start : start+delivery.DailyPointRecordBytes])
	return record, record.Weather.Valid, err
}

func (block *Block) Metadata() Asset {
	if block == nil {
		return Asset{}
	}
	return block.meta
}

func encodeHeader(identity Identity, payloadDigest [sha256.Size]byte) []byte {
	header := make([]byte, HeaderBytes)
	gridKind, _, _, _ := nativeLayout(identity.Provider, identity.Tier, identity.Grid)
	copy(header[:8], blockMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], Version)
	binary.LittleEndian.PutUint16(header[10:12], HeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], delivery.DailyPointRecordBytes)
	header[14], header[15] = rowMajorLayout, gridKind
	binary.LittleEndian.PutUint16(header[16:18], identity.Rows)
	binary.LittleEndian.PutUint16(header[18:20], identity.Columns)
	binary.LittleEndian.PutUint32(header[20:24], identity.Block.Row)
	binary.LittleEndian.PutUint32(header[24:28], identity.Block.Column)
	binary.LittleEndian.PutUint32(header[28:32], identity.Grid.Rows)
	binary.LittleEndian.PutUint32(header[32:36], identity.Grid.Cols)
	header[36], header[37], header[38], header[39] = byte(len(identity.ReleaseID)), byte(len(identity.Provider)), byte(len(identity.Tier)), byte(len(identity.Date))
	header[76] = byte(len(identity.RunUTC))
	putFloat64(header[40:48], identity.Grid.Bounds.West)
	putFloat64(header[48:56], identity.Grid.Bounds.East)
	putFloat64(header[56:64], identity.Grid.Bounds.South)
	putFloat64(header[64:72], identity.Grid.Bounds.North)
	binary.LittleEndian.PutUint32(header[72:76], uint32(identity.Rows)*uint32(identity.Columns)*delivery.DailyPointRecordBytes)
	releaseStart := fixedHeaderBytes
	providerStart := releaseStart + releaseIDBytes
	tierStart := providerStart + providerIDBytes
	dateStart := tierStart + tierIDBytes
	runEnd := runStart + runIDBytes
	copy(header[releaseStart:providerStart], identity.ReleaseID)
	copy(header[providerStart:tierStart], identity.Provider)
	copy(header[tierStart:dateStart], identity.Tier)
	copy(header[dateStart:runStart], identity.Date)
	copy(header[runStart:runEnd], identity.RunUTC)
	sourceDigest, _ := hex.DecodeString(identity.SourceEvidenceSHA256)
	copy(header[sourceDigestStart:payloadDigestStart], sourceDigest)
	copy(header[payloadDigestStart:payloadDigestStart+sha256.Size], payloadDigest[:])
	return header
}

func decodeHeader(header []byte) (Identity, [sha256.Size]byte, error) {
	if len(header) != HeaderBytes || string(header[:8]) != string(blockMagic[:]) ||
		binary.LittleEndian.Uint16(header[8:10]) != Version || binary.LittleEndian.Uint16(header[10:12]) != HeaderBytes ||
		binary.LittleEndian.Uint16(header[12:14]) != delivery.DailyPointRecordBytes || header[14] != rowMajorLayout ||
		!allZero(header[77:fixedHeaderBytes]) || !allZero(header[payloadDigestStart+sha256.Size:]) {
		return Identity{}, [sha256.Size]byte{}, errors.New("invalid forecast block header")
	}
	releaseLength, providerLength, tierLength, dateLength, runLength := int(header[36]), int(header[37]), int(header[38]), int(header[39]), int(header[76])
	if releaseLength > releaseIDBytes || providerLength > providerIDBytes || tierLength > tierIDBytes || dateLength > dateIDBytes || runLength > runIDBytes {
		return Identity{}, [sha256.Size]byte{}, errors.New("invalid forecast block identity length")
	}
	releaseStart := fixedHeaderBytes
	providerStart := releaseStart + releaseIDBytes
	tierStart := providerStart + providerIDBytes
	dateStart := tierStart + tierIDBytes
	runEnd := runStart + runIDBytes
	identity := Identity{
		ReleaseID:            string(header[releaseStart : releaseStart+releaseLength]),
		Provider:             string(header[providerStart : providerStart+providerLength]),
		Tier:                 string(header[tierStart : tierStart+tierLength]),
		Date:                 string(header[dateStart : dateStart+dateLength]),
		RunUTC:               string(header[runStart : runStart+runLength]),
		SourceEvidenceSHA256: hex.EncodeToString(header[sourceDigestStart:payloadDigestStart]),
		Grid: delivery.GridSpec{
			Bounds: delivery.GridBounds{West: getFloat64(header[40:48]), East: getFloat64(header[48:56]), South: getFloat64(header[56:64]), North: getFloat64(header[64:72])},
			Rows:   binary.LittleEndian.Uint32(header[28:32]), Cols: binary.LittleEndian.Uint32(header[32:36]),
		},
		Block: Coordinate{Row: binary.LittleEndian.Uint32(header[20:24]), Column: binary.LittleEndian.Uint32(header[24:28])},
		Rows:  binary.LittleEndian.Uint16(header[16:18]), Columns: binary.LittleEndian.Uint16(header[18:20]),
	}
	if !allZero(header[releaseStart+releaseLength:providerStart]) || !allZero(header[providerStart+providerLength:tierStart]) ||
		!allZero(header[tierStart+tierLength:dateStart]) || !allZero(header[dateStart+dateLength:runStart]) || !allZero(header[runStart+runLength:runEnd]) {
		return Identity{}, [sha256.Size]byte{}, errors.New("invalid forecast block identity padding")
	}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, [sha256.Size]byte{}, err
	}
	wantPayload := uint32(identity.Rows) * uint32(identity.Columns) * delivery.DailyPointRecordBytes
	if binary.LittleEndian.Uint32(header[72:76]) != wantPayload {
		return Identity{}, [sha256.Size]byte{}, errors.New("forecast block payload size does not match its geometry")
	}
	if header[15] != mustGridKind(identity.Provider, identity.Tier, identity.Grid) {
		return Identity{}, [sha256.Size]byte{}, errors.New("forecast block grid kind does not match its identity")
	}
	var digest [sha256.Size]byte
	copy(digest[:], header[payloadDigestStart:payloadDigestStart+sha256.Size])
	return identity, digest, nil
}

func validateIdentity(identity Identity) error {
	canonical, err := NewIdentity(identity.ReleaseID, identity.Provider, identity.Tier, identity.Date, identity.RunUTC, identity.SourceEvidenceSHA256, identity.Grid, identity.Block)
	if err != nil {
		return err
	}
	if identity != canonical {
		return errors.New("forecast block dimensions do not match its native grid")
	}
	return nil
}

func nativeLayout(provider, tier string, grid delivery.GridSpec) (kind byte, blockRows, blockCols uint16, err error) {
	switch {
	case provider == ProviderIFS && tier == TierGlobal:
		if grid != ifsGrid {
			return 0, 0, 0, errors.New("global IFS forecast block requires the native 0.25 degree world grid")
		}
		return gridIFS, nativeBlockRows, nativeBlockCols, nil
	case provider == ProviderICONEU && tier == TierEurope:
		if grid != iconGrid {
			return 0, 0, 0, errors.New("ICON-EU forecast block requires the native 0.0625 degree Europe grid")
		}
		return gridICONEU, nativeBlockRows, nativeBlockCols, nil
	default:
		return 0, 0, 0, errors.New("forecast block provider and tier are unsupported")
	}
}

func mustGridKind(provider, tier string, grid delivery.GridSpec) byte {
	kind, _, _, err := nativeLayout(provider, tier, grid)
	if err != nil {
		return 0
	}
	return kind
}

func nativeCell(grid delivery.GridSpec, wrapLongitude bool, longitude, latitude float64) (uint32, uint32, bool, error) {
	if !finite(longitude) || !finite(latitude) {
		return 0, 0, false, errors.New("forecast coordinate is not finite")
	}
	if wrapLongitude {
		longitude = math.Mod(longitude-grid.Bounds.West, 360)
		if longitude < 0 {
			longitude += 360
		}
		longitude += grid.Bounds.West
	}
	if longitude < grid.Bounds.West || longitude > grid.Bounds.East || latitude < grid.Bounds.South || latitude > grid.Bounds.North {
		return 0, 0, false, nil
	}
	col := int(math.Floor((longitude - grid.Bounds.West) / (grid.Bounds.East - grid.Bounds.West) * float64(grid.Cols)))
	row := int(math.Floor((grid.Bounds.North - latitude) / (grid.Bounds.North - grid.Bounds.South) * float64(grid.Rows)))
	if col == int(grid.Cols) {
		col--
	}
	if row == int(grid.Rows) {
		row--
	}
	if row < 0 || col < 0 || row >= int(grid.Rows) || col >= int(grid.Cols) {
		return 0, 0, false, nil
	}
	return uint32(row), uint32(col), true, nil
}

func writeImmutable(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".forecast-block-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
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
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("immutable forecast block already exists: %s", path)
		}
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func putFloat64(dst []byte, value float64) {
	binary.LittleEndian.PutUint64(dst, math.Float64bits(value))
}
func getFloat64(src []byte) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(src)) }
func finite(value float64) bool     { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
func validIdentifier(value string, maximum int) bool {
	return len(value) <= maximum && identifierPattern.MatchString(value)
}
func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
func validRunUTC(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.Location() == time.UTC && parsed.Nanosecond() == 0 && parsed.Format(time.RFC3339) == value
}
func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
