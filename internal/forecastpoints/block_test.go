package forecastpoints

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nice-weather-finder/internal/delivery"
)

func TestNativeForecastBlockRoundTripAndFixedOffsets(t *testing.T) {
	tests := []struct {
		name               string
		provider, tier     string
		grid               delivery.GridSpec
		block              Coordinate
		localRow, localCol int
		wantRows, wantCols uint16
		wantBytes          int
	}{
		{name: "IFS", provider: ProviderIFS, tier: TierGlobal, grid: ifsGrid, block: Coordinate{Row: 3, Column: 4}, localRow: 2, localCol: 3, wantRows: 64, wantCols: 64, wantBytes: HeaderBytes + 64*64*delivery.DailyPointRecordBytes},
		{name: "ICON-EU", provider: ProviderICONEU, tier: TierEurope, grid: iconGrid, block: Coordinate{Row: 0, Column: 0}, localRow: 63, localCol: 63, wantRows: 64, wantCols: 64, wantBytes: HeaderBytes + 64*64*delivery.DailyPointRecordBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := mustIdentity(t, test.provider, test.tier, test.grid, test.block)
			if identity.Rows != test.wantRows || identity.Columns != test.wantCols {
				t.Fatalf("identity = %+v", identity)
			}
			records := make([]delivery.DailyPointRecord, int(identity.Rows)*int(identity.Columns))
			want := forecastRecord(t)
			index := test.localRow*int(identity.Columns) + test.localCol
			records[index] = want
			root := t.TempDir()
			asset, err := WriteBlock(root, identity, records)
			if err != nil {
				t.Fatal(err)
			}
			if asset.Omitted || asset.Identity != identity || asset.Bytes != uint64(test.wantBytes) || asset.Bytes > MaxBytes || asset.MediaType != MediaType || asset.DataRecords != 1 || asset.NoDataRecords != uint32(len(records)-1) {
				t.Fatalf("asset = %+v", asset)
			}
			relative, err := RelativePath(identity)
			if err != nil || asset.Path != filepath.Join(root, filepath.FromSlash(relative)) || !strings.Contains(relative, identity.Provider+"/"+identity.Tier) {
				t.Fatalf("relative=%q asset=%+v err=%v", relative, asset, err)
			}
			block, err := OpenBlock(asset.Path, identity, asset.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			longitude, latitude := cellCentre(identity, test.localRow, test.localCol)
			got, ok, err := block.RecordAt(longitude, latitude)
			if err != nil || !ok || got != want {
				t.Fatalf("record = %+v, %t, %v; want %+v", got, ok, err, want)
			}
			byteRange, ok, err := RecordRange(identity, longitude, latitude)
			wantOffset := int64(HeaderBytes + index*delivery.DailyPointRecordBytes)
			if err != nil || !ok || byteRange.Offset != wantOffset || byteRange.Length != delivery.DailyPointRecordBytes || byteRange.EndInclusive() != wantOffset+delivery.DailyPointRecordBytes-1 {
				t.Fatalf("record range = %+v, %t, %v; want offset %d", byteRange, ok, err, wantOffset)
			}
			if header := HeaderRange(); header != (ByteRange{Offset: 0, Length: HeaderBytes}) || header.EndInclusive() != HeaderBytes-1 {
				t.Fatalf("header range = %+v", header)
			}
			whole, err := WholeRange(identity)
			if err != nil || whole.Offset != 0 || whole.Length != int64(asset.Bytes) || whole.EndInclusive() != int64(asset.Bytes)-1 {
				t.Fatalf("whole range = %+v, %v; asset bytes %d", whole, err, asset.Bytes)
			}
			raw, err := os.ReadFile(asset.Path)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := delivery.DecodeDailyPointRecord(raw[byteRange.Offset : byteRange.Offset+byteRange.Length])
			if err != nil || decoded != want {
				t.Fatalf("range record = %+v, %v", decoded, err)
			}
			parsed, err := ParseBlock(raw, identity, asset.SHA256)
			if err != nil || parsed.Metadata().Path != "" || parsed.Metadata().PayloadSHA256 != asset.PayloadSHA256 {
				t.Fatalf("parsed metadata = %+v, %v", parsed.Metadata(), err)
			}
		})
	}
}

func TestBoundaryShapesAndNativeGridEnforcement(t *testing.T) {
	ifsRows, ifsColumns, err := BlockGrid(ProviderIFS, TierGlobal, ifsGrid)
	if err != nil || ifsRows != 12 || ifsColumns != 23 || ifsRows*ifsColumns != 276 {
		t.Fatalf("IFS block grid = %dx%d, %v", ifsRows, ifsColumns, err)
	}
	iconRows, iconColumns, err := BlockGrid(ProviderICONEU, TierEurope, iconGrid)
	if err != nil || iconRows != 11 || iconColumns != 18 || iconRows*iconColumns != 198 {
		t.Fatalf("ICON block grid = %dx%d, %v", iconRows, iconColumns, err)
	}
	ifsBoundary := mustIdentity(t, ProviderIFS, TierGlobal, ifsGrid, Coordinate{Row: 11, Column: 22})
	if ifsBoundary.Rows != 16 || ifsBoundary.Columns != 32 {
		t.Fatalf("IFS boundary = %+v", ifsBoundary)
	}
	iconBoundary := mustIdentity(t, ProviderICONEU, TierEurope, iconGrid, Coordinate{Row: 10, Column: 17})
	if iconBoundary.Rows != 40 || iconBoundary.Columns != 16 {
		t.Fatalf("ICON boundary = %+v", iconBoundary)
	}
	badIFS := ifsGrid
	badIFS.Cols *= 2
	for name, attempt := range map[string]func() error{
		"resampled IFS": func() error {
			_, err := NewIdentity("release-canary", ProviderIFS, TierGlobal, "2026-07-22", badIFS, Coordinate{})
			return err
		},
		"mixed pair": func() error {
			_, err := NewIdentity("release-canary", ProviderIFS, TierEurope, "2026-07-22", ifsGrid, Coordinate{})
			return err
		},
		"outside block": func() error {
			_, err := NewIdentity("release-canary", ProviderICONEU, TierEurope, "2026-07-22", iconGrid, Coordinate{Row: 11})
			return err
		},
		"invalid date": func() error {
			_, err := NewIdentity("release-canary", ProviderIFS, TierGlobal, "2026-02-30", ifsGrid, Coordinate{})
			return err
		},
	} {
		if err := attempt(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	wrongShape := iconBoundary
	wrongShape.Rows++
	if _, err := RelativePath(wrongShape); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("wrong shape error = %v", err)
	}
}

func TestCoordinateOwnershipWrapAndNoResampling(t *testing.T) {
	west, ok, err := CoordinateIdentity("release-canary", ProviderIFS, TierGlobal, "2026-07-22", ifsGrid, -180, 0)
	if err != nil || !ok {
		t.Fatal(err)
	}
	east, ok, err := CoordinateIdentity("release-canary", ProviderIFS, TierGlobal, "2026-07-22", ifsGrid, 180, 0)
	if err != nil || !ok || east != west {
		t.Fatalf("antimeridian east=%+v west=%+v ok=%t err=%v", east, west, ok, err)
	}
	wrapped, ok, err := CoordinateIdentity("release-canary", ProviderIFS, TierGlobal, "2026-07-22", ifsGrid, 540, 0)
	if err != nil || !ok || wrapped != west {
		t.Fatalf("wrapped = %+v, %t, %v", wrapped, ok, err)
	}
}

func TestCoordinateBlockOwnershipAndCoastalNoData(t *testing.T) {
	identity, ok, err := CoordinateIdentity("release-canary", ProviderIFS, TierGlobal, "2026-07-22", ifsGrid, -164, 80)
	if err != nil || !ok || identity.Block.Column != 1 || identity.Block.Row != 0 {
		t.Fatalf("16 degree boundary = %+v, %t, %v", identity, ok, err)
	}
	westBlock := mustIdentity(t, ProviderIFS, TierGlobal, ifsGrid, Coordinate{Row: 0, Column: 0})
	if _, ok, err := RecordRange(westBlock, -164, 80); err != nil || ok {
		t.Fatalf("east boundary belonged to west block: ok=%t err=%v", ok, err)
	}
	icon, ok, err := CoordinateIdentity("release-canary", ProviderICONEU, TierEurope, "2026-07-22", iconGrid, -4.4214, 36.7213)
	if err != nil || !ok {
		t.Fatal(err)
	}
	records := make([]delivery.DailyPointRecord, int(icon.Rows)*int(icon.Columns))
	// A neighbouring land cell is valid; the selected coastal cell stays absent.
	longitude, latitude := cellCentre(icon, 1, 1)
	records[1*int(icon.Columns)+2] = forecastRecord(t)
	asset, err := WriteBlock(t.TempDir(), icon, records)
	if err != nil {
		t.Fatal(err)
	}
	block, err := OpenBlock(asset.Path, icon, asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if record, found, err := block.RecordAt(longitude, latitude); err != nil || found || record != (delivery.DailyPointRecord{}) {
		t.Fatalf("coastal no-data = %+v, %t, %v", record, found, err)
	}
	if _, ok, err := CoordinateIdentity("release-canary", ProviderICONEU, TierEurope, "2026-07-22", iconGrid, -30, 40); err != nil || ok {
		t.Fatalf("outside ICON = %t, %v", ok, err)
	}
}

func TestAllNoDataOmissionAndImmutablePublication(t *testing.T) {
	identity := mustIdentity(t, ProviderIFS, TierGlobal, ifsGrid, Coordinate{})
	records := make([]delivery.DailyPointRecord, int(identity.Rows)*int(identity.Columns))
	root := t.TempDir()
	asset, err := WriteBlock(root, identity, records)
	if err != nil || !asset.Omitted || asset.Path != "" || asset.DataRecords != 0 {
		t.Fatalf("omitted = %+v, %v", asset, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("omission wrote %d entries: %v", len(entries), err)
	}
	records[0] = forecastRecord(t)
	var successes int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := WriteBlock(root, identity, records); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("immutable concurrent writers succeeded %d times", successes)
	}
	if _, err := WriteBlock(root, identity, make([]delivery.DailyPointRecord, len(records))); err == nil {
		t.Fatal("existing data block was silently replaced by omission")
	}
}

func TestBlockRejectsCorruptionIdentityAndUnboundedFiles(t *testing.T) {
	identity := mustIdentity(t, ProviderIFS, TierGlobal, ifsGrid, Coordinate{})
	records := make([]delivery.DailyPointRecord, int(identity.Rows)*int(identity.Columns))
	records[0] = forecastRecord(t)
	asset, err := WriteBlock(t.TempDir(), identity, records)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	corruptPayload := append([]byte(nil), data...)
	corruptPayload[HeaderBytes+25] ^= 1
	corruptDigest := sha256.Sum256(corruptPayload)
	if _, err := ParseBlock(corruptPayload, identity, hex.EncodeToString(corruptDigest[:])); err == nil || !strings.Contains(err.Error(), "payload checksum") {
		t.Fatalf("payload corruption error = %v", err)
	}
	wrongIdentity := mustIdentity(t, ProviderIFS, TierGlobal, ifsGrid, Coordinate{Column: 1})
	if _, err := ParseBlock(data, wrongIdentity, asset.SHA256); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong identity error = %v", err)
	}
	if _, err := ParseBlock(data[:len(data)-1], identity, asset.SHA256); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("truncated error = %v", err)
	}
	invalidRecord := append([]byte(nil), data...)
	invalidRecord[HeaderBytes+25] = 101
	payloadDigest := sha256.Sum256(invalidRecord[HeaderBytes:])
	copy(invalidRecord[digestStart:digestStart+sha256.Size], payloadDigest[:])
	wholeDigest := sha256.Sum256(invalidRecord)
	if _, err := ParseBlock(invalidRecord, identity, hex.EncodeToString(wholeDigest[:])); err == nil || !strings.Contains(err.Error(), "record 0") {
		t.Fatalf("invalid canonical record error = %v", err)
	}
	oversized := filepath.Join(t.TempDir(), "oversized.nwf")
	if err := os.WriteFile(oversized, make([]byte, MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBlock(oversized, identity, asset.SHA256); err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("oversized error = %v", err)
	}
	symlink := filepath.Join(t.TempDir(), "link.nwf")
	if err := os.Symlink(asset.Path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBlock(symlink, identity, asset.SHA256); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestHeaderCarriesExactNativeIdentityAndPayloadDigest(t *testing.T) {
	identity := mustIdentity(t, ProviderICONEU, TierEurope, iconGrid, Coordinate{Row: 10, Column: 17})
	records := make([]delivery.DailyPointRecord, int(identity.Rows)*int(identity.Columns))
	records[len(records)-1] = forecastRecord(t)
	asset, err := WriteBlock(t.TempDir(), identity, records)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	header := data[:HeaderBytes]
	if string(header[:8]) != string(blockMagic[:]) || binary.LittleEndian.Uint16(header[8:10]) != Version || binary.LittleEndian.Uint16(header[10:12]) != HeaderBytes || binary.LittleEndian.Uint16(header[12:14]) != delivery.DailyPointRecordBytes || header[14] != rowMajorLayout || header[15] != gridICONEU {
		t.Fatalf("fixed header = %x", header[:16])
	}
	decoded, digest, err := decodeHeader(header)
	if err != nil || decoded != identity || hex.EncodeToString(digest[:]) != asset.PayloadSHA256 {
		t.Fatalf("decoded = %+v digest=%x err=%v", decoded, digest, err)
	}
	if binary.LittleEndian.Uint32(header[72:76]) != uint32(len(records)*delivery.DailyPointRecordBytes) {
		t.Fatalf("payload bytes = %d", binary.LittleEndian.Uint32(header[72:76]))
	}
}

func mustIdentity(t *testing.T, provider, tier string, grid delivery.GridSpec, coordinate Coordinate) Identity {
	t.Helper()
	identity, err := NewIdentity("release-canary", provider, tier, "2026-07-22", grid, coordinate)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func forecastRecord(t *testing.T) delivery.DailyPointRecord {
	t.Helper()
	record, err := delivery.DailyPointRecordFromWeather(delivery.DailyRecord{
		ComfortableTemperatureDeciC: 240, MinTemperatureDeciC: 180, MaxTemperatureDeciC: 280, DewPointDeciC: 120,
		WindDeciKMH: 100, GustDeciKMH: 180, SolarCentiMJm2: 1900, CloudPercent: 20, HumidityPercent: 55,
		Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func cellCentre(identity Identity, localRow, localCol int) (float64, float64) {
	_, blockRows, blockCols, _ := nativeLayout(identity.Provider, identity.Tier, identity.Grid)
	globalRow := int(identity.Block.Row)*int(blockRows) + localRow
	globalCol := int(identity.Block.Column)*int(blockCols) + localCol
	cellWidth := (identity.Grid.Bounds.East - identity.Grid.Bounds.West) / float64(identity.Grid.Cols)
	cellHeight := (identity.Grid.Bounds.North - identity.Grid.Bounds.South) / float64(identity.Grid.Rows)
	return identity.Grid.Bounds.West + (float64(globalCol)+0.5)*cellWidth,
		identity.Grid.Bounds.North - (float64(globalRow)+0.5)*cellHeight
}
