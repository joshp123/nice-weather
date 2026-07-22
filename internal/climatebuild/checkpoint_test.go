package climatebuild

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
)

func TestCheckpointRoundTripIsDeterministicAndMonthMajor(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	checkpoint.TemperatureRepairs = 7
	checkpoint.MaximumTemperatureRepair = 1.25
	checkpoint.ComfortTemperatureRepairs = 4
	checkpoint.MaximumComfortTemperatureRepair = 5.25
	checkpoint.DewPointRepairs = 3
	checkpoint.MaximumDewPointRepair = 2.5
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.nwc")
	secondPath := filepath.Join(root, "second.nwc")
	if err := WriteCheckpoint(firstPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(secondPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("identical climate checkpoints produced different bytes")
	}
	wantPayloadBytes := checkpointMonthCount * checkpoint.Identity.Width * checkpoint.Identity.Height * delivery.ClimateDistributionRecordBytes
	if len(first) != checkpointHeaderBytes+wantPayloadBytes {
		t.Fatalf("checkpoint bytes = %d; want %d", len(first), checkpointHeaderBytes+wantPayloadBytes)
	}
	if string(first[:8]) != string(checkpointMagic[:]) || binary.LittleEndian.Uint16(first[8:10]) != CheckpointVersion || binary.LittleEndian.Uint16(first[10:12]) != checkpointHeaderBytes {
		t.Fatal("fixed checkpoint preamble is wrong")
	}
	if binary.LittleEndian.Uint16(first[16:18]) != CheckpointFirstYear || binary.LittleEndian.Uint16(first[18:20]) != CheckpointLastYear {
		t.Fatal("checkpoint does not contain the fixed 2006-2025 reference window")
	}
	if got := binary.LittleEndian.Uint32(first[32:36]); got != uint32(wantPayloadBytes) {
		t.Fatalf("header payload bytes = %d; want %d", got, wantPayloadBytes)
	}
	if got := [sha256.Size]byte(first[88:120]); got != checkpointDerivationSHA256 {
		t.Fatal("header derivation digest differs from the narrow climate contract")
	}

	loaded, ok, err := LoadCheckpoint(firstPath, checkpoint.Identity)
	if err != nil || !ok {
		t.Fatalf("load checkpoint: ok=%t err=%v", ok, err)
	}
	if !reflect.DeepEqual(loaded, checkpoint) {
		t.Fatal("loaded checkpoint differs from the written block")
	}

	// The first record of February must follow all January cells exactly.
	cells := checkpoint.Identity.Width * checkpoint.Identity.Height
	februaryOffset := checkpointHeaderBytes + cells*delivery.ClimateDistributionRecordBytes
	february, err := delivery.DecodeClimateDistributionRecord(first[februaryOffset : februaryOffset+delivery.ClimateDistributionRecordBytes])
	if err != nil {
		t.Fatal(err)
	}
	if february != checkpoint.Months[1][0] {
		t.Fatal("checkpoint payload is not month-major")
	}
}

func TestCheckpointLoadsLegacyV1HeaderWithZeroExtendedRepairTelemetry(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	checkpoint.TemperatureRepairs = 7
	checkpoint.MaximumTemperatureRepair = 1.25
	path := filepath.Join(t.TempDir(), "legacy-v1.nwc")
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !allZero(encoded[216:248]) {
		t.Fatal("zero extended repair telemetry changed the legacy v1 reserved bytes")
	}
	wantLegacyDigest := sha256.Sum256(encoded[0:184])
	if got := [sha256.Size]byte(encoded[184:216]); got != wantLegacyDigest {
		t.Fatal("zero dew-point telemetry changed the legacy v1 header checksum")
	}
	loaded, found, err := LoadCheckpoint(path, checkpoint.Identity)
	if err != nil || !found || loaded.DewPointRepairs != 0 || loaded.MaximumDewPointRepair != 0 || loaded.ComfortTemperatureRepairs != 0 || loaded.MaximumComfortTemperatureRepair != 0 {
		t.Fatalf("load legacy v1 checkpoint: found=%t checkpoint=%+v err=%v", found, loaded, err)
	}
}

func TestCheckpointWriteFailurePreservesPriorCheckpointAndCleansTemporaryFile(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	root := t.TempDir()
	path := filepath.Join(root, "block.nwc")
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lastMonth := len(checkpoint.Months) - 1
	lastRecord := len(checkpoint.Months[lastMonth]) - 1
	checkpoint.Months[lastMonth][lastRecord] = delivery.ClimateDistributionRecord{ComfortGoodPercent: 50}
	if err := WriteCheckpoint(path, checkpoint); err == nil || !strings.Contains(err.Error(), "month 12") {
		t.Fatalf("late invalid-record error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("failed checkpoint write changed the prior complete checkpoint")
	}
	temporary, err := filepath.Glob(filepath.Join(root, ".climate-checkpoint-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("failed writer left temporary files: %v", temporary)
	}
}

func TestLoadCheckpointIgnoresInterruptedTemporaryFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".climate-checkpoint-interrupted.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := checkpointIdentity(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	if _, ok, err := LoadCheckpoint(filepath.Join(root, "block.nwc"), identity); err != nil || ok {
		t.Fatalf("interrupted temporary file became resumable: ok=%t err=%v", ok, err)
	}
}

func TestLoadCheckpointRejectsCorruptTruncatedAndTrailingFiles(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	root := t.TempDir()
	validPath := filepath.Join(root, "valid.nwc")
	if err := WriteCheckpoint(validPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"corrupt payload":  append([]byte(nil), valid...),
		"truncated":        append([]byte(nil), valid[:len(valid)-1]...),
		"trailing":         append(append([]byte(nil), valid...), 0),
		"reserved header":  append([]byte(nil), valid...),
		"repair telemetry": append([]byte(nil), valid...),
	}
	tests["corrupt payload"][checkpointHeaderBytes+17] ^= 0x40
	tests["reserved header"][249] = 1
	binary.LittleEndian.PutUint64(tests["repair telemetry"][40:48], 1)
	binary.LittleEndian.PutUint64(tests["repair telemetry"][48:56], 0x3ff0000000000000) // 1°C
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, strings.ReplaceAll(name, " ", "-")+".nwc")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := LoadCheckpoint(path, checkpoint.Identity); err == nil || ok {
				t.Fatalf("invalid checkpoint loaded: ok=%t err=%v", ok, err)
			}
		})
	}
}

func TestLoadCheckpointRejectsContractAndEvidenceMismatch(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	root := t.TempDir()
	path := filepath.Join(root, "block.nwc")
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[88] ^= 0xff
	headerSHA256 := sha256.Sum256(data[0:184])
	copy(data[184:216], headerSHA256[:])
	contractPath := filepath.Join(root, "old-contract.nwc")
	if err := os.WriteFile(contractPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadCheckpoint(contractPath, checkpoint.Identity); !errors.Is(err, ErrCheckpointMismatch) || ok {
		t.Fatalf("derivation mismatch: ok=%t err=%v", ok, err)
	}

	wrongEvidence := checkpoint.Identity
	wrongEvidence.SourceEvidenceSHA256 = sha256.Sum256([]byte("different source evidence"))
	if _, ok, err := LoadCheckpoint(path, wrongEvidence); !errors.Is(err, ErrCheckpointMismatch) || ok {
		t.Fatalf("source-evidence mismatch: ok=%t err=%v", ok, err)
	}
	wrongMask := checkpoint.Identity
	wrongMask.NaturalEarthMaskSHA256 = sha256.Sum256([]byte("different Natural Earth mask"))
	if _, ok, err := LoadCheckpoint(path, wrongMask); !errors.Is(err, ErrCheckpointMismatch) || ok {
		t.Fatalf("mask mismatch: ok=%t err=%v", ok, err)
	}
}

func TestCheckpointPayloadIsBoundedToTwelveFullSourcePlanes(t *testing.T) {
	if checkpointMaxPayloadBytes != 12*16*32*36 {
		t.Fatalf("maximum checkpoint payload = %d; want %d", checkpointMaxPayloadBytes, 12*16*32*36)
	}
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 7, Longitude: 8})
	path := filepath.Join(t.TempDir(), "maximum.nwc")
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != checkpointHeaderBytes+checkpointMaxPayloadBytes {
		t.Fatalf("maximum checkpoint has %d bytes; want %d", info.Size(), checkpointHeaderBytes+checkpointMaxPayloadBytes)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadCheckpoint(path, checkpoint.Identity); err == nil || ok || !strings.Contains(err.Error(), "allowed range") {
		t.Fatalf("oversized checkpoint: ok=%t err=%v", ok, err)
	}
}

func TestCheckpointRejectsInvalidIdentityAndRepairTelemetry(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	root := t.TempDir()

	wrongShape := checkpoint
	wrongShape.Identity.Width++
	if err := WriteCheckpoint(filepath.Join(root, "shape.nwc"), wrongShape); err == nil || !strings.Contains(err.Error(), "logical shape") {
		t.Fatalf("wrong-shape error = %v", err)
	}
	missingMask := checkpoint
	missingMask.Identity.NaturalEarthMaskSHA256 = [sha256.Size]byte{}
	if err := WriteCheckpoint(filepath.Join(root, "mask.nwc"), missingMask); err == nil || !strings.Contains(err.Error(), "mask SHA-256") {
		t.Fatalf("missing-mask error = %v", err)
	}
	badTelemetry := checkpoint
	badTelemetry.TemperatureRepairs = 0
	badTelemetry.MaximumTemperatureRepair = 0.1
	if err := WriteCheckpoint(filepath.Join(root, "telemetry.nwc"), badTelemetry); err == nil || !strings.Contains(err.Error(), "nonzero maximum") {
		t.Fatalf("bad-telemetry error = %v", err)
	}
	badDewTelemetry := checkpoint
	badDewTelemetry.DewPointRepairs = 0
	badDewTelemetry.MaximumDewPointRepair = 0.1
	if err := WriteCheckpoint(filepath.Join(root, "dew-telemetry.nwc"), badDewTelemetry); err == nil || !strings.Contains(err.Error(), "nonzero maximum") {
		t.Fatalf("bad dew-point telemetry error = %v", err)
	}
	badComfortTelemetry := checkpoint
	badComfortTelemetry.ComfortTemperatureRepairs = 0
	badComfortTelemetry.MaximumComfortTemperatureRepair = 0.1
	if err := WriteCheckpoint(filepath.Join(root, "comfort-telemetry.nwc"), badComfortTelemetry); err == nil || !strings.Contains(err.Error(), "nonzero maximum") {
		t.Fatalf("bad daytime-range telemetry error = %v", err)
	}
	for _, maximum := range []float64{2, 0.1} {
		impossible := checkpoint
		impossible.DewPointRepairs = 1
		impossible.MaximumDewPointRepair = maximum
		if err := WriteCheckpoint(filepath.Join(root, fmt.Sprintf("impossible-dew-%g.nwc", maximum)), impossible); err == nil || !strings.Contains(err.Error(), "maximum dew-point correction") {
			t.Fatalf("impossible dew-point maximum %g error = %v", maximum, err)
		}
	}
}

func TestLoadCheckpointRejectsImpossibleDewPointExtension(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	checkpoint.DewPointRepairs = 1
	checkpoint.MaximumDewPointRepair = 2.5
	root := t.TempDir()
	path := filepath.Join(root, "valid.nwc")
	if err := WriteCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint64(encoded[224:232], math.Float64bits(2))
	digest := checkpointHeaderSHA256(encoded)
	copy(encoded[184:216], digest[:])
	impossiblePath := filepath.Join(root, "impossible.nwc")
	if err := os.WriteFile(impossiblePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadCheckpoint(impossiblePath, checkpoint.Identity); err == nil || found || !strings.Contains(err.Error(), "maximum dew-point correction") {
		t.Fatalf("impossible dew-point extension loaded: found=%t err=%v", found, err)
	}
}

func TestCheckpointWriterRequiresAnExistingDirectory(t *testing.T) {
	checkpoint := checkpointFixture(t, agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	path := filepath.Join(t.TempDir(), "absent", "block.nwc")
	if err := WriteCheckpoint(path, checkpoint); err == nil || !strings.Contains(err.Error(), "stat climate checkpoint directory") {
		t.Fatalf("absent-directory error = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("checkpoint writer created its parent directory: %v", err)
	}
}

func checkpointFixture(t *testing.T, coordinate agera5.BlockCoordinate) Checkpoint {
	t.Helper()
	identity := checkpointIdentity(t, coordinate)
	cells := identity.Width * identity.Height
	checkpoint := Checkpoint{Identity: identity}
	for month := range checkpoint.Months {
		checkpoint.Months[month] = make([]delivery.ClimateDistributionRecord, cells)
		for index := range checkpoint.Months[month] {
			checkpoint.Months[month][index] = checkpointRecord(uint8(40 + (month+index)%50))
		}
	}
	return checkpoint
}

func checkpointIdentity(t *testing.T, coordinate agera5.BlockCoordinate) CheckpointIdentity {
	t.Helper()
	height, width, err := agera5.BlockShape(coordinate)
	if err != nil {
		t.Fatal(err)
	}
	return CheckpointIdentity{
		Block:                  coordinate,
		Width:                  width,
		Height:                 height,
		NaturalEarthMaskSHA256: sha256.Sum256([]byte("Natural Earth mask fixture")),
		SourceEvidenceSHA256:   sha256.Sum256([]byte("AgERA5 source evidence fixture")),
	}
}

func checkpointRecord(median uint8) delivery.ClimateDistributionRecord {
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: 65,
		DryGoodPercent:     80,
		BrightGoodPercent:  55,
		Nice: delivery.ClimateProfileDistribution{
			MedianScore:      median,
			CalmGoodPercent:  75,
			PoorWeekPermille: 250,
			RatingShares: delivery.ClimateRatingShares{
				Awful: 100, Poor: 100, Mixed: 200, Good: 250, VeryGood: 200, Excellent: 150,
			},
		},
		Cycling: delivery.ClimateProfileDistribution{
			MedianScore:      median,
			CalmGoodPercent:  60,
			PoorWeekPermille: 350,
			RatingShares: delivery.ClimateRatingShares{
				Awful: 150, Poor: 150, Mixed: 200, Good: 225, VeryGood: 175, Excellent: 100,
			},
		},
		Valid: true,
	}
}
