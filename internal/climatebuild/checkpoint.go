package climatebuild

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
)

const (
	CheckpointVersion   = 1
	CheckpointFirstYear = 2006
	CheckpointLastYear  = 2025

	checkpointHeaderBytes     = 256
	checkpointMonthCount      = 12
	checkpointReferenceDays   = 20*365 + 5
	checkpointMaxPayloadBytes = checkpointMonthCount * 16 * 32 * delivery.ClimateDistributionRecordBytes

	// CheckpointDerivationContract identifies only the inputs and transformations
	// that can change a recent-climate block. It is intentionally independent of
	// delivery.ProductContract so frontend-only releases do not discard completed
	// worldwide climate work. Bump it when source mapping, land masking, scoring,
	// aggregation or the climate-distribution record changes.
	CheckpointDerivationContract = "agera5-v2.0|daily-metrics-v1|extrema-order-v1|weather-score-v1|climate-distribution-v1|natural-earth-5.1.1-centre-land|2006-2025"
)

var (
	checkpointMagic            = [8]byte{'N', 'W', 'F', 'A', 'G', 'R', 'C', 0}
	checkpointDerivationSHA256 = sha256.Sum256([]byte(CheckpointDerivationContract))

	// ErrCheckpointMismatch means a complete checkpoint was produced from a
	// different block identity or derivation contract and must be rebuilt.
	ErrCheckpointMismatch = errors.New("climate checkpoint does not match the requested derivation")
)

// CheckpointIdentity binds a block result to its exact source geometry, land
// mask and validated source evidence. Digests contain raw SHA-256 bytes.
type CheckpointIdentity struct {
	Block                  agera5.BlockCoordinate
	Width                  int
	Height                 int
	NaturalEarthMaskSHA256 [sha256.Size]byte
	SourceEvidenceSHA256   [sha256.Size]byte
}

// Checkpoint is one complete AgERA5 spatial block. Months are January through
// December; every plane is row-major in Identity's logical geometry.
type Checkpoint struct {
	Identity                 CheckpointIdentity
	Months                   [checkpointMonthCount][]delivery.ClimateDistributionRecord
	TemperatureRepairs       int
	MaximumTemperatureRepair float64
	DewPointRepairs          int
	MaximumDewPointRepair    float64
}

type checkpointHeader struct {
	identity                 CheckpointIdentity
	recordCount              uint32
	payloadBytes             uint32
	temperatureRepairs       uint64
	maximumTemperatureRepair float64
	dewPointRepairs          uint64
	maximumDewPointRepair    float64
	payloadSHA256            [sha256.Size]byte
}

// WriteCheckpoint transactionally replaces path with a complete checkpoint.
// The checkpoint directory must already exist. A failed encode leaves any
// prior checkpoint untouched.
func WriteCheckpoint(path string, checkpoint Checkpoint) error {
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("stat climate checkpoint directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("climate checkpoint parent is not a directory")
	}
	temporary, err := os.CreateTemp(directory, ".climate-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary climate checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := temporary.Write(make([]byte, checkpointHeaderBytes)); err != nil {
		return fmt.Errorf("reserve climate checkpoint header: %w", err)
	}
	digest := sha256.New()
	payload := io.MultiWriter(temporary, digest)
	encoded := make([]byte, delivery.ClimateDistributionRecordBytes)
	for month := range checkpoint.Months {
		for index, record := range checkpoint.Months[month] {
			if err := delivery.EncodeClimateDistributionRecord(encoded, record); err != nil {
				return fmt.Errorf("encode climate checkpoint month %02d record %d: %w", month+1, index, err)
			}
			if _, err := payload.Write(encoded); err != nil {
				return fmt.Errorf("write climate checkpoint payload: %w", err)
			}
		}
	}
	payloadSHA256 := [sha256.Size]byte(digest.Sum(nil))
	header := encodeCheckpointHeader(checkpoint, payloadSHA256)
	if _, err := temporary.WriteAt(header[:], 0); err != nil {
		return fmt.Errorf("write climate checkpoint header: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary climate checkpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary climate checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish climate checkpoint: %w", err)
	}
	if err := syncCheckpointDirectory(directory); err != nil {
		return fmt.Errorf("sync climate checkpoint directory: %w", err)
	}
	committed = true
	return nil
}

// LoadCheckpoint returns false for an absent final path. Temporary files from
// interrupted writers are deliberately ignored. Existing invalid files fail
// closed so the caller can quarantine or rebuild them explicitly.
func LoadCheckpoint(path string, expected CheckpointIdentity) (Checkpoint, bool, error) {
	if err := validateCheckpointIdentity(expected); err != nil {
		return Checkpoint{}, false, fmt.Errorf("invalid expected climate checkpoint identity: %w", err)
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("open climate checkpoint: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("stat climate checkpoint: %w", err)
	}
	maximumFileBytes := int64(checkpointHeaderBytes + checkpointMaxPayloadBytes)
	if info.Size() < checkpointHeaderBytes || info.Size() > maximumFileBytes {
		return Checkpoint{}, false, fmt.Errorf("climate checkpoint has %d bytes; allowed range is %d to %d", info.Size(), checkpointHeaderBytes, maximumFileBytes)
	}
	headerBytes := make([]byte, checkpointHeaderBytes)
	if _, err := io.ReadFull(file, headerBytes); err != nil {
		return Checkpoint{}, false, fmt.Errorf("read climate checkpoint header: %w", err)
	}
	header, err := decodeCheckpointHeader(headerBytes)
	if err != nil {
		return Checkpoint{}, false, err
	}
	if header.identity != expected {
		return Checkpoint{}, false, fmt.Errorf("%w: block, geometry, mask or source evidence changed", ErrCheckpointMismatch)
	}
	wantFileBytes := int64(checkpointHeaderBytes) + int64(header.payloadBytes)
	if info.Size() != wantFileBytes {
		return Checkpoint{}, false, fmt.Errorf("climate checkpoint has %d bytes; header declares %d", info.Size(), wantFileBytes)
	}
	payload := make([]byte, int(header.payloadBytes))
	if _, err := io.ReadFull(file, payload); err != nil {
		return Checkpoint{}, false, fmt.Errorf("read climate checkpoint payload: %w", err)
	}
	var trailing [1]byte
	if count, err := file.Read(trailing[:]); count != 0 || !errors.Is(err, io.EOF) {
		return Checkpoint{}, false, errors.New("climate checkpoint contains trailing bytes")
	}
	if actual := sha256.Sum256(payload); actual != header.payloadSHA256 {
		return Checkpoint{}, false, errors.New("climate checkpoint payload SHA-256 does not match")
	}

	checkpoint := Checkpoint{
		Identity:                 header.identity,
		TemperatureRepairs:       int(header.temperatureRepairs),
		MaximumTemperatureRepair: header.maximumTemperatureRepair,
		DewPointRepairs:          int(header.dewPointRepairs),
		MaximumDewPointRepair:    header.maximumDewPointRepair,
	}
	cells := expected.Width * expected.Height
	for month := range checkpoint.Months {
		checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, cells)
		for index := range checkpoint.Months[month] {
			offset := (month*cells + index) * delivery.ClimateDistributionRecordBytes
			record, err := delivery.DecodeClimateDistributionRecord(payload[offset : offset+delivery.ClimateDistributionRecordBytes])
			if err != nil {
				return Checkpoint{}, false, fmt.Errorf("decode climate checkpoint month %02d record %d: %w", month+1, index, err)
			}
			checkpoint.Months[month][index] = record
		}
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return Checkpoint{}, false, fmt.Errorf("validate climate checkpoint payload: %w", err)
	}
	return checkpoint, true, nil
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if err := validateCheckpointIdentity(checkpoint.Identity); err != nil {
		return err
	}
	cells := checkpoint.Identity.Width * checkpoint.Identity.Height
	for month, records := range checkpoint.Months {
		if len(records) != cells {
			return fmt.Errorf("climate checkpoint month %02d has %d records; want %d", month+1, len(records), cells)
		}
	}
	maximumRepairs := checkpointReferenceDays * cells
	if err := validateRepairTelemetry("temperature", checkpoint.TemperatureRepairs, checkpoint.MaximumTemperatureRepair, maximumRepairs, 0); err != nil {
		return err
	}
	if err := validateRepairTelemetry("dew-point", checkpoint.DewPointRepairs, checkpoint.MaximumDewPointRepair, maximumRepairs, materialDewPointCrossingC); err != nil {
		return err
	}
	return nil
}

func validateCheckpointIdentity(identity CheckpointIdentity) error {
	height, width, err := agera5.BlockShape(identity.Block)
	if err != nil {
		return err
	}
	if identity.Width != width || identity.Height != height {
		return fmt.Errorf("AgERA5 block %+v has logical shape %dx%d; checkpoint declares %dx%d", identity.Block, width, height, identity.Width, identity.Height)
	}
	if identity.NaturalEarthMaskSHA256 == ([sha256.Size]byte{}) {
		return errors.New("Natural Earth mask SHA-256 is required")
	}
	if identity.SourceEvidenceSHA256 == ([sha256.Size]byte{}) {
		return errors.New("source evidence SHA-256 is required")
	}
	return nil
}

func encodeCheckpointHeader(checkpoint Checkpoint, payloadSHA256 [sha256.Size]byte) [checkpointHeaderBytes]byte {
	var header [checkpointHeaderBytes]byte
	copy(header[0:8], checkpointMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], CheckpointVersion)
	binary.LittleEndian.PutUint16(header[10:12], checkpointHeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], delivery.ClimateDistributionRecordBytes)
	binary.LittleEndian.PutUint16(header[14:16], checkpointMonthCount)
	binary.LittleEndian.PutUint16(header[16:18], CheckpointFirstYear)
	binary.LittleEndian.PutUint16(header[18:20], CheckpointLastYear)
	binary.LittleEndian.PutUint16(header[20:22], uint16(checkpoint.Identity.Block.Latitude))
	binary.LittleEndian.PutUint16(header[22:24], uint16(checkpoint.Identity.Block.Longitude))
	binary.LittleEndian.PutUint16(header[24:26], uint16(checkpoint.Identity.Width))
	binary.LittleEndian.PutUint16(header[26:28], uint16(checkpoint.Identity.Height))
	recordCount := checkpointMonthCount * checkpoint.Identity.Width * checkpoint.Identity.Height
	payloadBytes := recordCount * delivery.ClimateDistributionRecordBytes
	binary.LittleEndian.PutUint32(header[28:32], uint32(recordCount))
	binary.LittleEndian.PutUint32(header[32:36], uint32(payloadBytes))
	binary.LittleEndian.PutUint64(header[40:48], uint64(checkpoint.TemperatureRepairs))
	binary.LittleEndian.PutUint64(header[48:56], math.Float64bits(checkpoint.MaximumTemperatureRepair))
	copy(header[56:88], checkpoint.Identity.NaturalEarthMaskSHA256[:])
	copy(header[88:120], checkpointDerivationSHA256[:])
	copy(header[120:152], checkpoint.Identity.SourceEvidenceSHA256[:])
	copy(header[152:184], payloadSHA256[:])
	binary.LittleEndian.PutUint64(header[216:224], uint64(checkpoint.DewPointRepairs))
	binary.LittleEndian.PutUint64(header[224:232], math.Float64bits(checkpoint.MaximumDewPointRepair))
	headerSHA256 := checkpointHeaderSHA256(header[:])
	copy(header[184:216], headerSHA256[:])
	return header
}

func decodeCheckpointHeader(encoded []byte) (checkpointHeader, error) {
	if len(encoded) != checkpointHeaderBytes {
		return checkpointHeader{}, fmt.Errorf("climate checkpoint header has %d bytes; want %d", len(encoded), checkpointHeaderBytes)
	}
	if string(encoded[0:8]) != string(checkpointMagic[:]) {
		return checkpointHeader{}, errors.New("climate checkpoint magic is invalid")
	}
	var storedHeaderSHA256 [sha256.Size]byte
	copy(storedHeaderSHA256[:], encoded[184:216])
	if actual := checkpointHeaderSHA256(encoded); actual != storedHeaderSHA256 {
		return checkpointHeader{}, errors.New("climate checkpoint header SHA-256 does not match")
	}
	if version := binary.LittleEndian.Uint16(encoded[8:10]); version != CheckpointVersion {
		return checkpointHeader{}, fmt.Errorf("climate checkpoint version is %d; want %d", version, CheckpointVersion)
	}
	if size := binary.LittleEndian.Uint16(encoded[10:12]); size != checkpointHeaderBytes {
		return checkpointHeader{}, fmt.Errorf("climate checkpoint header size is %d; want %d", size, checkpointHeaderBytes)
	}
	if size := binary.LittleEndian.Uint16(encoded[12:14]); size != delivery.ClimateDistributionRecordBytes {
		return checkpointHeader{}, fmt.Errorf("climate checkpoint record size is %d; want %d", size, delivery.ClimateDistributionRecordBytes)
	}
	if months := binary.LittleEndian.Uint16(encoded[14:16]); months != checkpointMonthCount {
		return checkpointHeader{}, fmt.Errorf("climate checkpoint has %d months; want %d", months, checkpointMonthCount)
	}
	firstYear, lastYear := binary.LittleEndian.Uint16(encoded[16:18]), binary.LittleEndian.Uint16(encoded[18:20])
	if firstYear != CheckpointFirstYear || lastYear != CheckpointLastYear {
		return checkpointHeader{}, fmt.Errorf("%w: reference window is %d-%d; want %d-%d", ErrCheckpointMismatch, firstYear, lastYear, CheckpointFirstYear, CheckpointLastYear)
	}
	if !allZero(encoded[36:40]) || !allZero(encoded[232:]) {
		return checkpointHeader{}, errors.New("climate checkpoint reserved header bytes are nonzero")
	}
	var derivation [sha256.Size]byte
	copy(derivation[:], encoded[88:120])
	if derivation != checkpointDerivationSHA256 {
		return checkpointHeader{}, fmt.Errorf("%w: derivation contract changed", ErrCheckpointMismatch)
	}

	header := checkpointHeader{
		identity: CheckpointIdentity{
			Block: agera5.BlockCoordinate{
				Latitude:  int(binary.LittleEndian.Uint16(encoded[20:22])),
				Longitude: int(binary.LittleEndian.Uint16(encoded[22:24])),
			},
			Width:  int(binary.LittleEndian.Uint16(encoded[24:26])),
			Height: int(binary.LittleEndian.Uint16(encoded[26:28])),
		},
		recordCount:              binary.LittleEndian.Uint32(encoded[28:32]),
		payloadBytes:             binary.LittleEndian.Uint32(encoded[32:36]),
		temperatureRepairs:       binary.LittleEndian.Uint64(encoded[40:48]),
		maximumTemperatureRepair: math.Float64frombits(binary.LittleEndian.Uint64(encoded[48:56])),
		dewPointRepairs:          binary.LittleEndian.Uint64(encoded[216:224]),
		maximumDewPointRepair:    math.Float64frombits(binary.LittleEndian.Uint64(encoded[224:232])),
	}
	copy(header.identity.NaturalEarthMaskSHA256[:], encoded[56:88])
	copy(header.identity.SourceEvidenceSHA256[:], encoded[120:152])
	copy(header.payloadSHA256[:], encoded[152:184])
	if err := validateCheckpointHeader(header); err != nil {
		return checkpointHeader{}, err
	}
	return header, nil
}

func validateCheckpointHeader(header checkpointHeader) error {
	if err := validateCheckpointIdentity(header.identity); err != nil {
		return fmt.Errorf("invalid climate checkpoint identity: %w", err)
	}
	recordCount := checkpointMonthCount * header.identity.Width * header.identity.Height
	payloadBytes := recordCount * delivery.ClimateDistributionRecordBytes
	if payloadBytes > checkpointMaxPayloadBytes {
		return fmt.Errorf("climate checkpoint payload has %d bytes; maximum is %d", payloadBytes, checkpointMaxPayloadBytes)
	}
	if header.recordCount != uint32(recordCount) || header.payloadBytes != uint32(payloadBytes) {
		return fmt.Errorf("climate checkpoint declares %d records and %d payload bytes; want %d and %d", header.recordCount, header.payloadBytes, recordCount, payloadBytes)
	}
	maximumRepairs := checkpointReferenceDays * header.identity.Width * header.identity.Height
	if header.temperatureRepairs > uint64(^uint(0)>>1) || header.dewPointRepairs > uint64(^uint(0)>>1) {
		return errors.New("climate checkpoint repair count exceeds this platform")
	}
	if err := validateRepairTelemetry("temperature", int(header.temperatureRepairs), header.maximumTemperatureRepair, maximumRepairs, 0); err != nil {
		return err
	}
	if err := validateRepairTelemetry("dew-point", int(header.dewPointRepairs), header.maximumDewPointRepair, maximumRepairs, materialDewPointCrossingC); err != nil {
		return err
	}
	if header.payloadSHA256 == ([sha256.Size]byte{}) {
		return errors.New("climate checkpoint payload SHA-256 is missing")
	}
	return nil
}

func validateRepairTelemetry(label string, count int, maximum float64, allowed int, minimumExclusive float64) error {
	if count < 0 || count > allowed {
		return fmt.Errorf("climate checkpoint has %d %s repairs; allowed range is 0 to %d", count, label, allowed)
	}
	if count == 0 {
		if math.Float64bits(maximum) != 0 {
			return fmt.Errorf("climate checkpoint without %s repairs has a nonzero maximum correction", label)
		}
		return nil
	}
	if math.IsNaN(maximum) || math.IsInf(maximum, 0) || maximum <= minimumExclusive || maximum > 170 {
		return fmt.Errorf("climate checkpoint maximum %s correction %.9g°C is invalid", label, maximum)
	}
	return nil
}

func allZero(values []byte) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
}

// Legacy v1 checkpoints hashed the original header through byte 183. The
// optional dew-point repair extension uses formerly reserved bytes and adds
// them to the checksum only when present. A legacy all-zero extension remains
// byte-for-byte valid and decodes with zero dew-point repairs.
func checkpointHeaderSHA256(encoded []byte) [sha256.Size]byte {
	digest := sha256.New()
	_, _ = digest.Write(encoded[0:184])
	if !allZero(encoded[216:232]) {
		_, _ = digest.Write(encoded[216:232])
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func syncCheckpointDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
