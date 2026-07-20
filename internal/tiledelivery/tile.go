// Package tiledelivery owns exact XYZ viewport geometry and fixed-record point
// tiles. It is independent of the legacy ImageSource point geometry, whose
// rows originate in a regular latitude-longitude grid.
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
)

const (
	TileSize = 256
	MaxZoom  = 22

	fixedHeaderBytes = 32
	releaseIDBytes   = 64
	periodIDBytes    = 32
	tierIDBytes      = 32
	digestBytes      = sha256.Size
	pointHeaderSize  = fixedHeaderBytes + releaseIDBytes + periodIDBytes + tierIDBytes + digestBytes
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	pointMagic        = [8]byte{'N', 'W', 'F', 'X', 'Y', 'Z', 'P', 'T'}
)

type ID struct {
	Tier string
	Zoom uint8
	X    uint32
	Y    uint32
}

type PointAsset struct {
	ID            ID
	ReleaseID     string
	Period        string
	Bounds        delivery.GridBounds
	Path          string
	Bytes         uint64
	SHA256        string
	PayloadSHA256 string
	Omitted       bool
}

type PointReader struct {
	file *os.File
	meta PointAsset
}

type Writer struct {
	root      string
	releaseID string
}

func NewWriter(root, releaseID string) (*Writer, error) {
	if root == "" || !validIdentifier(releaseID, releaseIDBytes) {
		return nil, errors.New("tile writer requires root and release ID")
	}
	return &Writer{root: root, releaseID: releaseID}, nil
}

// NewID canonicalises wrapped X addresses and rejects invalid Y addresses.
func NewID(tier string, zoom uint8, x, y int64) (ID, error) {
	if !validIdentifier(tier, tierIDBytes) || zoom > MaxZoom {
		return ID{}, errors.New("tile tier or zoom is invalid")
	}
	count := int64(uint64(1) << zoom)
	if y < 0 || y >= count {
		return ID{}, errors.New("tile Y is outside its zoom")
	}
	x = ((x % count) + count) % count
	return ID{Tier: tier, Zoom: zoom, X: uint32(x), Y: uint32(y)}, nil
}

func (id ID) validate() error {
	canonical, err := NewID(id.Tier, id.Zoom, int64(id.X), int64(id.Y))
	if err != nil || canonical != id {
		return errors.New("tile ID is not canonical")
	}
	return nil
}

// Bounds returns exact, unsnapped XYZ geographic edges.
func (id ID) Bounds() (delivery.GridBounds, error) {
	if err := id.validate(); err != nil {
		return delivery.GridBounds{}, err
	}
	count := math.Exp2(float64(id.Zoom))
	longitude := func(x float64) float64 { return x/count*360 - 180 }
	latitude := func(y float64) float64 {
		return math.Atan(math.Sinh(math.Pi*(1-2*y/count))) * 180 / math.Pi
	}
	return delivery.GridBounds{
		West: longitude(float64(id.X)), East: longitude(float64(id.X + 1)),
		North: latitude(float64(id.Y)), South: latitude(float64(id.Y + 1)),
	}, nil
}

// At returns the canonical XYZ tile and exact Mercator record index. Longitude
// wraps at the antimeridian. West and north edges are inclusive; east and south
// edges belong to the adjacent tile. The outer south edge remains addressable.
// Latitude outside Web Mercator is rejected rather than silently moved.
func At(tier string, zoom uint8, longitude, latitude float64) (ID, int, int, error) {
	if !finite(longitude) || !finite(latitude) || math.Abs(latitude) > 85.0511287798066 {
		return ID{}, 0, 0, errors.New("coordinate is outside Web Mercator")
	}
	if !validIdentifier(tier, tierIDBytes) || zoom > MaxZoom {
		return ID{}, 0, 0, errors.New("tile tier or zoom is invalid")
	}
	wrapped := math.Mod(longitude+180, 360)
	if wrapped < 0 {
		wrapped += 360
	}
	normalizedX := wrapped / 360
	radians := latitude * math.Pi / 180
	normalizedY := 0.5 - math.Asinh(math.Tan(radians))/(2*math.Pi)
	count := math.Exp2(float64(zoom))
	globalX, globalY := normalizedX*count, normalizedY*count
	globalX = snapInteger(globalX)
	globalY = snapInteger(globalY)
	if globalY < 0 && globalY > -1e-12 {
		globalY = 0
	}
	if globalY >= count && globalY < count+1e-12 {
		globalY = math.Nextafter(count, 0)
	}
	id, err := NewID(tier, zoom, int64(math.Floor(globalX)), int64(math.Floor(globalY)))
	if err != nil {
		return ID{}, 0, 0, err
	}
	col := int(math.Floor((globalX - math.Floor(globalX)) * TileSize))
	row := int(math.Floor((globalY - math.Floor(globalY)) * TileSize))
	if row < 0 || row >= TileSize || col < 0 || col >= TileSize {
		return ID{}, 0, 0, errors.New("coordinate produced an invalid tile record")
	}
	return id, row, col, nil
}

// Write publishes one immutable point tile. Every record is already evaluated
// by the canonical server-side scoring path. All-water/no-data tiles are
// omitted without creating directories.
func (writer *Writer) Write(id ID, period string, records []delivery.DisplayScoreRecord) (PointAsset, error) {
	if writer == nil || !validIdentifier(period, periodIDBytes) {
		return PointAsset{}, errors.New("tile writer or period is invalid")
	}
	if err := id.validate(); err != nil {
		return PointAsset{}, err
	}
	if len(records) != TileSize*TileSize {
		return PointAsset{}, fmt.Errorf("tile has %d records; want %d", len(records), TileSize*TileSize)
	}
	bounds, err := id.Bounds()
	if err != nil {
		return PointAsset{}, err
	}
	asset := PointAsset{ID: id, ReleaseID: writer.releaseID, Period: period, Bounds: bounds}
	path := filepath.Join(writer.root, "releases", writer.releaseID, "points", period, id.Tier, fmt.Sprint(id.Zoom), fmt.Sprint(id.X), fmt.Sprintf("%d.nwt", id.Y))
	if _, err := os.Stat(path); err == nil {
		return PointAsset{}, fmt.Errorf("immutable tile already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return PointAsset{}, err
	}
	anyData := false
	for _, record := range records {
		if record.Valid {
			anyData = true
			break
		}
	}
	if !anyData {
		asset.Omitted = true
		return asset, nil
	}

	payload, err := pointPayload(records)
	if err != nil {
		return PointAsset{}, err
	}
	payloadDigest := sha256.Sum256(payload)
	data := append(encodePointHeader(writer.releaseID, period, id, payloadDigest), payload...)
	wholeDigest := sha256.Sum256(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PointAsset{}, err
	}
	if err := writeImmutable(path, data); err != nil {
		return PointAsset{}, err
	}
	asset.Path = path
	asset.Bytes = uint64(len(data))
	asset.SHA256 = hex.EncodeToString(wholeDigest[:])
	asset.PayloadSHA256 = hex.EncodeToString(payloadDigest[:])
	return asset, nil
}

func OpenPoint(path string) (*PointReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	meta, err := validatePointFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &PointReader{file: file, meta: meta}, nil
}

func (reader *PointReader) At(longitude, latitude float64) (delivery.DisplayScoreRecord, bool, error) {
	if reader == nil || reader.file == nil {
		return delivery.DisplayScoreRecord{}, false, errors.New("point tile reader is closed")
	}
	id, row, col, err := At(reader.meta.ID.Tier, reader.meta.ID.Zoom, longitude, latitude)
	if err != nil || id != reader.meta.ID {
		return delivery.DisplayScoreRecord{}, false, err
	}
	encoded := make([]byte, delivery.DisplayScoreRecordBytes)
	offset := int64(pointHeaderSize + (row*TileSize+col)*delivery.DisplayScoreRecordBytes)
	if _, err := reader.file.ReadAt(encoded, offset); err != nil {
		return delivery.DisplayScoreRecord{}, false, err
	}
	record, err := delivery.DecodeDisplayScoreRecord(encoded)
	return record, record.Valid, err
}

func (reader *PointReader) Metadata() PointAsset { return reader.meta }
func (reader *PointReader) Close() error {
	if reader == nil || reader.file == nil {
		return nil
	}
	err := reader.file.Close()
	reader.file = nil
	return err
}

func pointPayload(records []delivery.DisplayScoreRecord) ([]byte, error) {
	payload := make([]byte, TileSize*TileSize*delivery.DisplayScoreRecordBytes)
	for index, record := range records {
		start := index * delivery.DisplayScoreRecordBytes
		if err := delivery.EncodeDisplayScoreRecord(payload[start:start+delivery.DisplayScoreRecordBytes], record); err != nil {
			return nil, fmt.Errorf("point tile record %d: %w", index, err)
		}
	}
	return payload, nil
}

func writeImmutable(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".point-*.nwt")
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
	// Linking publishes the completed inode without replacing an existing path.
	// A preflight Stat gives a useful error, while Link enforces immutability
	// if another writer wins the race.
	if err := os.Link(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("immutable tile already exists: %s", path)
		}
		return err
	}
	return nil
}

func encodePointHeader(releaseID, period string, id ID, payloadDigest [sha256.Size]byte) []byte {
	header := make([]byte, pointHeaderSize)
	copy(header[:8], pointMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], 1)
	binary.LittleEndian.PutUint16(header[10:12], pointHeaderSize)
	binary.LittleEndian.PutUint16(header[12:14], delivery.DisplayScoreRecordBytes)
	binary.LittleEndian.PutUint16(header[14:16], TileSize)
	header[16], header[17] = id.Zoom, 1 // projection 1 is exact XYZ Web Mercator
	binary.LittleEndian.PutUint32(header[18:22], id.X)
	binary.LittleEndian.PutUint32(header[22:26], id.Y)
	header[26], header[27], header[28] = byte(len(releaseID)), byte(len(period)), byte(len(id.Tier))
	releaseStart := fixedHeaderBytes
	periodStart := releaseStart + releaseIDBytes
	tierStart := periodStart + periodIDBytes
	digestStart := tierStart + tierIDBytes
	copy(header[releaseStart:periodStart], releaseID)
	copy(header[periodStart:tierStart], period)
	copy(header[tierStart:digestStart], id.Tier)
	copy(header[digestStart:pointHeaderSize], payloadDigest[:])
	return header
}

func validatePointFile(file *os.File) (PointAsset, error) {
	header := make([]byte, pointHeaderSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return PointAsset{}, err
	}
	if string(header[:8]) != string(pointMagic[:]) || binary.LittleEndian.Uint16(header[8:10]) != 1 || binary.LittleEndian.Uint16(header[10:12]) != pointHeaderSize || binary.LittleEndian.Uint16(header[12:14]) != delivery.DisplayScoreRecordBytes || binary.LittleEndian.Uint16(header[14:16]) != TileSize || header[17] != 1 || !allZero(header[29:fixedHeaderBytes]) {
		return PointAsset{}, errors.New("invalid XYZ point tile header")
	}
	releaseLength, periodLength, tierLength := int(header[26]), int(header[27]), int(header[28])
	if releaseLength > releaseIDBytes || periodLength > periodIDBytes || tierLength > tierIDBytes {
		return PointAsset{}, errors.New("invalid XYZ point tile identity length")
	}
	releaseStart := fixedHeaderBytes
	periodStart := releaseStart + releaseIDBytes
	tierStart := periodStart + periodIDBytes
	digestStart := tierStart + tierIDBytes
	releaseID := string(header[releaseStart : releaseStart+releaseLength])
	period := string(header[periodStart : periodStart+periodLength])
	tier := string(header[tierStart : tierStart+tierLength])
	if !validIdentifier(releaseID, releaseIDBytes) || !validIdentifier(period, periodIDBytes) || !validIdentifier(tier, tierIDBytes) || !allZero(header[releaseStart+releaseLength:periodStart]) || !allZero(header[periodStart+periodLength:tierStart]) || !allZero(header[tierStart+tierLength:digestStart]) {
		return PointAsset{}, errors.New("invalid XYZ point tile identity")
	}
	id, err := NewID(tier, header[16], int64(binary.LittleEndian.Uint32(header[18:22])), int64(binary.LittleEndian.Uint32(header[22:26])))
	if err != nil {
		return PointAsset{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return PointAsset{}, err
	}
	wantBytes := int64(pointHeaderSize + TileSize*TileSize*delivery.DisplayScoreRecordBytes)
	if info.Size() != wantBytes {
		return PointAsset{}, errors.New("XYZ point tile has invalid size")
	}
	payloadDigest := sha256.New()
	wholeDigest := sha256.New()
	if _, err := wholeDigest.Write(header); err != nil {
		return PointAsset{}, err
	}
	if _, err := io.Copy(io.MultiWriter(payloadDigest, wholeDigest), io.NewSectionReader(file, pointHeaderSize, wantBytes-pointHeaderSize)); err != nil {
		return PointAsset{}, err
	}
	if got := payloadDigest.Sum(nil); string(got) != string(header[digestStart:pointHeaderSize]) {
		return PointAsset{}, errors.New("XYZ point tile payload checksum mismatch")
	}
	bounds, err := id.Bounds()
	if err != nil {
		return PointAsset{}, err
	}
	return PointAsset{
		ID: id, ReleaseID: releaseID, Period: period, Bounds: bounds, Path: file.Name(), Bytes: uint64(wantBytes),
		SHA256: hex.EncodeToString(wholeDigest.Sum(nil)), PayloadSHA256: hex.EncodeToString(header[digestStart:pointHeaderSize]),
	}, nil
}

func validIdentifier(value string, maxBytes int) bool {
	return len(value) <= maxBytes && identifierPattern.MatchString(value)
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func snapInteger(value float64) float64 {
	nearest := math.Round(value)
	if math.Abs(value-nearest) <= 1e-12 {
		return nearest
	}
	return value
}
