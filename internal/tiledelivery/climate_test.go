package tiledelivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
)

func TestClimateBlockRoundTripAndExactTwoRangeContract(t *testing.T) {
	block := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	identity, err := NewClimateBlockIdentity("release-canary", "month-11", block)
	if err != nil {
		t.Fatal(err)
	}
	if identity.LatitudeCount != 16 || identity.LongitudeCount != 32 {
		t.Fatalf("identity = %+v", identity)
	}
	records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
	const row, column = 2, 3
	want := climateRecordForTest()
	records[row*int(identity.LongitudeCount)+column] = want
	path := filepath.Join(t.TempDir(), "native.nwc")
	asset, err := WriteClimateBlock(path, identity.ReleaseID, identity.Period, identity.Block, records)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := ClimateBlockHeaderBytes + len(records)*delivery.ClimateDistributionRecordBytes
	if asset.Omitted || asset.Identity != identity || asset.Path != path || asset.Bytes != uint64(wantBytes) || asset.MediaType != ClimateBlockMediaType || asset.DataRecords != 1 || asset.NoDataRecords != uint16(len(records)-1) {
		t.Fatalf("asset = %+v", asset)
	}
	if len(asset.SHA256) != 64 || len(asset.PayloadSHA256) != 64 {
		t.Fatalf("digests = %q / %q", asset.SHA256, asset.PayloadSHA256)
	}

	reader, err := OpenClimateBlock(path, identity, asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	longitude := -180 + float64(block.Longitude*32+column)/10
	latitude := -90 + float64(block.Latitude*16+row)/10
	got, ok, err := reader.RecordAt(longitude, latitude)
	if err != nil || !ok || got != want {
		t.Fatalf("record = %+v, %t, %v; want %+v", got, ok, err, want)
	}
	headerRange := ClimateHeaderRange()
	if headerRange.Offset != 0 || headerRange.Length != ClimateBlockHeaderBytes || headerRange.EndInclusive() != ClimateBlockHeaderBytes-1 {
		t.Fatalf("header range = %+v", headerRange)
	}
	recordRange, ok, err := reader.RecordRange(longitude, latitude)
	wantOffset := int64(ClimateBlockHeaderBytes + (row*int(identity.LongitudeCount)+column)*delivery.ClimateDistributionRecordBytes)
	if err != nil || !ok || recordRange.Offset != wantOffset || recordRange.Length != delivery.ClimateDistributionRecordBytes || recordRange.EndInclusive() != wantOffset+delivery.ClimateDistributionRecordBytes-1 {
		t.Fatalf("record range = %+v, %t, %v; want offset %d", recordRange, ok, err, wantOffset)
	}
	standaloneRange, standaloneOK, standaloneErr := ClimateRecordRange(identity, longitude, latitude)
	if standaloneErr != nil || !standaloneOK || standaloneRange != recordRange {
		t.Fatalf("standalone record range = %+v, %t, %v", standaloneRange, standaloneOK, standaloneErr)
	}
	raw, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	encoded := make([]byte, recordRange.Length)
	if _, err := raw.ReadAt(encoded, recordRange.Offset); err != nil {
		t.Fatal(err)
	}
	decoded, err := delivery.DecodeClimateDistributionRecord(encoded)
	if err != nil || decoded != want {
		t.Fatalf("range-decoded record = %+v, %v", decoded, err)
	}
	if meta := reader.Metadata(); meta != asset {
		t.Fatalf("metadata = %+v; want %+v", meta, asset)
	}
}

func TestClimateBlockExactSourceGridAndBoundaryShape(t *testing.T) {
	block := agera5.BlockCoordinate{Latitude: 112, Longitude: 112}
	identity, err := NewClimateBlockIdentity("release-canary", "month-01", block)
	if err != nil {
		t.Fatal(err)
	}
	if identity.LatitudeCount != 9 || identity.LongitudeCount != 16 {
		t.Fatalf("boundary identity = %+v", identity)
	}
	records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
	records[len(records)-1] = climateRecordForTest()
	asset, err := WriteClimateBlock(filepath.Join(t.TempDir(), "edge.nwc"), identity.ReleaseID, identity.Period, block, records)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenClimateBlock(asset.Path, identity, asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, ok, err := reader.RecordAt(179.9, 90); err != nil || !ok {
		t.Fatalf("south-east global record = %t, %v", ok, err)
	}
	if _, ok, err := reader.RecordAt(179.8, 90); err != nil || ok {
		t.Fatalf("adjacent no-data record = %t, %v", ok, err)
	}
	if _, ok, err := reader.RecordAt(-180, -90); err != nil || ok {
		t.Fatalf("source point outside block = %t, %v", ok, err)
	}
	for _, coordinate := range [][2]float64{{179.95, 90}, {180, 90}, {179.9, 90.01}} {
		if _, _, err := reader.RecordAt(coordinate[0], coordinate[1]); err == nil {
			t.Fatalf("accepted non-source coordinate %v", coordinate)
		}
	}
}

func TestAllNoDataClimateBlockIsOmittedWithoutFilesystemChanges(t *testing.T) {
	root := t.TempDir()
	block := agera5.BlockCoordinate{Latitude: 112, Longitude: 112}
	identity, _ := NewClimateBlockIdentity("release-canary", "month-02", block)
	path := filepath.Join(root, "nested", "empty.nwc")
	asset, err := WriteClimateBlock(path, identity.ReleaseID, identity.Period, block, make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount)))
	if err != nil {
		t.Fatal(err)
	}
	if !asset.Omitted || asset.Path != "" || asset.Bytes != 0 || asset.DataRecords != 0 || asset.NoDataRecords != identity.LatitudeCount*identity.LongitudeCount {
		t.Fatalf("omitted asset = %+v", asset)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("omitted block created %d filesystem entries", len(entries))
	}
}

func TestClimateBlockPublicationIsImmutable(t *testing.T) {
	block := agera5.BlockCoordinate{Latitude: 112, Longitude: 112}
	identity, _ := NewClimateBlockIdentity("release-canary", "month-03", block)
	records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
	records[0] = climateRecordForTest()
	path := filepath.Join(t.TempDir(), "immutable.nwc")
	first, err := WriteClimateBlock(path, identity.ReleaseID, identity.Period, block, records)
	if err != nil {
		t.Fatal(err)
	}
	replacement := make([]delivery.ClimateDistributionRecord, len(records))
	replacement[1] = climateRecordForTest()
	if _, err := WriteClimateBlock(path, identity.ReleaseID, identity.Period, block, replacement); err == nil {
		t.Fatal("overwrote immutable climate block")
	}
	reader, err := OpenClimateBlock(path, identity, first.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	longitude := -180 + float64(block.Longitude*32)/10
	latitude := -90 + float64(block.Latitude*16)/10
	if _, ok, err := reader.RecordAt(longitude, latitude); err != nil || !ok {
		t.Fatalf("original immutable record = %t, %v", ok, err)
	}
}

func TestClimateBlockRejectsCorruptionSizeAndWholeDigestMismatch(t *testing.T) {
	asset, identity, data := climateFileForTest(t)

	truncated := filepath.Join(t.TempDir(), "truncated.nwc")
	if err := os.WriteFile(truncated, data[:len(data)-1], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClimateBlock(truncated, identity, sha256HexForTest(data[:len(data)-1])); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("truncated error = %v", err)
	}
	oversizedData := append(append([]byte(nil), data...), 0)
	oversized := filepath.Join(t.TempDir(), "oversized.nwc")
	if err := os.WriteFile(oversized, oversizedData, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClimateBlock(oversized, identity, sha256HexForTest(oversizedData)); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversized error = %v", err)
	}
	if _, err := OpenClimateBlock(asset.Path, identity, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "whole-file checksum") {
		t.Fatalf("whole checksum error = %v", err)
	}

	payloadCorrupt := append([]byte(nil), data...)
	payloadCorrupt[ClimateBlockHeaderBytes+10] ^= 1
	payloadCorruptPath := filepath.Join(t.TempDir(), "payload-corrupt.nwc")
	if err := os.WriteFile(payloadCorruptPath, payloadCorrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClimateBlock(payloadCorruptPath, identity, sha256HexForTest(payloadCorrupt)); err == nil || !strings.Contains(err.Error(), "payload checksum") {
		t.Fatalf("payload checksum error = %v", err)
	}

	headerCorrupt := append([]byte(nil), data...)
	headerCorrupt[0] = 'X'
	headerCorruptPath := filepath.Join(t.TempDir(), "header-corrupt.nwc")
	if err := os.WriteFile(headerCorruptPath, headerCorrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClimateBlock(headerCorruptPath, identity, sha256HexForTest(headerCorrupt)); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("header error = %v", err)
	}
}

func TestClimateBlockValidatesEveryRecordAndRejectsWrongIdentity(t *testing.T) {
	asset, identity, data := climateFileForTest(t)
	for name, expected := range map[string]ClimateBlockIdentity{
		"release": mustClimateIdentity(t, "different-release", identity.Period, identity.Block),
		"period":  mustClimateIdentity(t, identity.ReleaseID, "month-12", identity.Block),
		"block":   mustClimateIdentity(t, identity.ReleaseID, identity.Period, agera5.BlockCoordinate{Latitude: identity.Block.Latitude - 1, Longitude: identity.Block.Longitude}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OpenClimateBlock(asset.Path, expected, asset.SHA256); err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("wrong identity error = %v", err)
			}
		})
	}
	wrongDimensions := identity
	wrongDimensions.LongitudeCount--
	if _, err := OpenClimateBlock(asset.Path, wrongDimensions, asset.SHA256); err == nil || !strings.Contains(err.Error(), "logical dimensions") {
		t.Fatalf("wrong dimensions error = %v", err)
	}

	invalidRecord := append([]byte(nil), data...)
	recordStart := ClimateBlockHeaderBytes
	invalidRecord[recordStart] = 1
	invalidRecord[recordStart+1] = 255
	payloadDigest := sha256.Sum256(invalidRecord[ClimateBlockHeaderBytes:])
	copy(invalidRecord[climateFixedHeaderBytes+climateReleaseIDBytes+climatePeriodIDBytes:], payloadDigest[:])
	invalidPath := filepath.Join(t.TempDir(), "invalid-record.nwc")
	if err := os.WriteFile(invalidPath, invalidRecord, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClimateBlock(invalidPath, identity, sha256HexForTest(invalidRecord)); err == nil || !strings.Contains(err.Error(), "record 0") {
		t.Fatalf("invalid record error = %v", err)
	}
}

func TestClimateBlockHeaderCarriesFixedIdentityAndPayloadDigest(t *testing.T) {
	asset, identity, data := climateFileForTest(t)
	header := data[:ClimateBlockHeaderBytes]
	if string(header[:8]) != string(climateBlockMagic[:]) || binary.LittleEndian.Uint16(header[8:10]) != ClimateBlockVersion || binary.LittleEndian.Uint16(header[10:12]) != ClimateBlockHeaderBytes || binary.LittleEndian.Uint16(header[12:14]) != delivery.ClimateDistributionRecordBytes {
		t.Fatalf("fixed header = %x", header[:14])
	}
	if binary.LittleEndian.Uint16(header[14:16]) != identity.LatitudeCount || binary.LittleEndian.Uint16(header[16:18]) != identity.LongitudeCount || int(binary.LittleEndian.Uint16(header[18:20])) != identity.Block.Latitude || int(binary.LittleEndian.Uint16(header[20:22])) != identity.Block.Longitude {
		t.Fatalf("geometry header = %x", header[14:22])
	}
	releaseStart := climateFixedHeaderBytes
	periodStart := releaseStart + climateReleaseIDBytes
	digestStart := periodStart + climatePeriodIDBytes
	if string(header[releaseStart:releaseStart+len(identity.ReleaseID)]) != identity.ReleaseID || string(header[periodStart:periodStart+len(identity.Period)]) != identity.Period || hex.EncodeToString(header[digestStart:]) != asset.PayloadSHA256 {
		t.Fatal("header did not carry exact identity and payload digest")
	}
}

func TestClimateBlockRejectsInvalidConstructionIdentity(t *testing.T) {
	for _, test := range []struct {
		release string
		period  string
		block   agera5.BlockCoordinate
	}{
		{"bad release", "month-01", agera5.BlockCoordinate{}},
		{"release", "January", agera5.BlockCoordinate{}},
		{"release", "month-13", agera5.BlockCoordinate{}},
		{"release", "month-01", agera5.BlockCoordinate{Latitude: 113}},
	} {
		if _, err := NewClimateBlockIdentity(test.release, test.period, test.block); err == nil {
			t.Fatalf("accepted identity %+v", test)
		}
	}
	identity, _ := NewClimateBlockIdentity("release", "month-01", agera5.BlockCoordinate{})
	if _, err := WriteClimateBlock("", identity.ReleaseID, identity.Period, identity.Block, make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))); err == nil {
		t.Fatal("accepted empty output path")
	}
	if _, err := WriteClimateBlock(filepath.Join(t.TempDir(), "wrong-count.nwc"), identity.ReleaseID, identity.Period, identity.Block, nil); err == nil {
		t.Fatal("accepted wrong record count")
	}
	if _, err := OpenClimateBlock(filepath.Join(t.TempDir(), "missing.nwc"), identity, "BAD"); err == nil {
		t.Fatal("accepted invalid expected digest")
	}
}

func climateFileForTest(t *testing.T) (ClimateBlockAsset, ClimateBlockIdentity, []byte) {
	t.Helper()
	identity := mustClimateIdentity(t, "release-canary", "month-04", agera5.BlockCoordinate{Latitude: 112, Longitude: 112})
	records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
	records[1] = climateRecordForTest()
	asset, err := WriteClimateBlock(filepath.Join(t.TempDir(), "climate.nwc"), identity.ReleaseID, identity.Period, identity.Block, records)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	return asset, identity, data
}

func mustClimateIdentity(t *testing.T, releaseID, period string, block agera5.BlockCoordinate) ClimateBlockIdentity {
	t.Helper()
	identity, err := NewClimateBlockIdentity(releaseID, period, block)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func climateRecordForTest() delivery.ClimateDistributionRecord {
	return delivery.ClimateDistributionRecord{
		ComfortGoodPercent: 74,
		DryGoodPercent:     82,
		BrightGoodPercent:  68,
		Nice: delivery.ClimateProfileDistribution{
			MedianScore: 72, CalmGoodPercent: 80, PoorWeekPermille: 120,
			RatingShares: delivery.ClimateRatingShares{Awful: 50, Poor: 100, Mixed: 150, Good: 250, VeryGood: 300, Excellent: 150},
		},
		Cycling: delivery.ClimateProfileDistribution{
			MedianScore: 65, CalmGoodPercent: 70, PoorWeekPermille: 210,
			RatingShares: delivery.ClimateRatingShares{Awful: 100, Poor: 150, Mixed: 200, Good: 250, VeryGood: 200, Excellent: 100},
		},
		Valid: true,
	}
}

func sha256HexForTest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
