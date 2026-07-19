package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPointAssetKnownHeaderAndDailyRecordVector(t *testing.T) {
	vector := readPointVector(t)
	geometry := canonicalTestPointGeometry(GridBounds{West: -25, East: 45, South: 30, North: 72}, 1, 1)
	payload := make([]byte, 32)
	for index := range payload {
		payload[index] = 0x11
	}
	if got := hex.EncodeToString(encodePointHeader(PointKindForecastDay, DailyPointRecordBytes, geometry, payload)); got != vector.HeaderHex {
		t.Fatalf("header vector = %s", got)
	}
	weatherRecord := DailyRecord{
		ComfortableTemperatureDeciC: -5, MinTemperatureDeciC: -20, MaxTemperatureDeciC: 10, DewPointDeciC: -15,
		RainDeciMM: 2, WindDeciKMH: 70, GustDeciKMH: 100, SolarCentiMJm2: 800, WetTimePermille: 100,
		CloudPercent: 50, HumidityPercent: 80, Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true,
	}
	record, err := DailyPointRecordFromWeather(weatherRecord)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, DailyPointRecordBytes)
	encodeDailyPointRecord(encoded, record)
	if got := hex.EncodeToString(encoded); got != vector.DailyRecordHex {
		t.Fatalf("daily vector = %s", got)
	}
	decoded, err := decodeDailyPointRecord(encoded)
	if err != nil || decoded != record {
		t.Fatalf("daily round trip = %+v, %v", decoded, err)
	}
}

func readPointVector(t *testing.T) struct {
	HeaderHex      string `json:"headerHex"`
	DailyRecordHex string `json:"dailyRecordHex"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "src", "data", "testdata", "point-asset-vector.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		HeaderHex      string `json:"headerHex"`
		DailyRecordHex string `json:"dailyRecordHex"`
	}
	if err := json.Unmarshal(data, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

func TestPointAssetDigestsRoundTripAndProfiles(t *testing.T) {
	geometry := canonicalTestPointGeometry(GridBounds{West: -25, East: 45, South: 30, North: 72}, 1, 1)
	weatherRecord := DailyRecord{
		ComfortableTemperatureDeciC: 250, MinTemperatureDeciC: 200, MaxTemperatureDeciC: 290, DewPointDeciC: 150,
		WindDeciKMH: 250, GustDeciKMH: 300, SolarCentiMJm2: 1920, CloudPercent: 18, HumidityPercent: 50,
		Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true,
	}
	record, err := DailyPointRecordFromWeather(weatherRecord)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "point.nwp")
	metadata, err := WriteDailyPointAsset(path, "test-release", PointKindForecastDay, geometry, []DailyPointRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	literal := sha256.Sum256(data)
	payload := sha256.Sum256(data[PointAssetHeaderBytes:])
	if metadata.SHA256 != hex.EncodeToString(literal[:]) || metadata.PayloadSHA256 != hex.EncodeToString(payload[:]) || metadata.PayloadSHA256 == metadata.SHA256 {
		t.Fatalf("literal=%s payload=%s metadata=%+v", hex.EncodeToString(literal[:]), metadata.PayloadSHA256, metadata)
	}
	reader, err := OpenPointAsset(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, valid, err := reader.DailyAt(0, 50)
	if err != nil || !valid || got.NiceScore != record.NiceScore || got.CyclingScore != record.CyclingScore || got.NiceWind == got.CyclingWind {
		t.Fatalf("daily point = %+v valid=%t err=%v", got, valid, err)
	}
	if _, err := ValidatePointAssetRecords(path); err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPointAsset(path); err == nil || !strings.Contains(err.Error(), "payload checksum") {
		t.Fatalf("payload corruption was accepted: %v", err)
	}
}

func TestDisplayScoreAssetRangeRecordAndCorruptionValidation(t *testing.T) {
	geometry := canonicalTestPointGeometry(GridBounds{West: -25, East: 45, South: 30, North: 72}, 1, 1)
	record := DisplayScoreRecord{Thermal: 10, Dryness: 20, Sun: 30, NiceScore: 40, NiceWind: 50, CyclingScore: 60, CyclingWind: 70, Valid: true}
	path := filepath.Join(t.TempDir(), "display.nwp")
	metadata, err := WriteDisplayScoreAsset(path, "test-release", geometry, []DisplayScoreRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Kind != PointKindDisplayScore || metadata.RecordBytes != DisplayScoreRecordBytes || metadata.Bytes != PointAssetHeaderBytes+DisplayScoreRecordBytes {
		t.Fatalf("display metadata = %+v", metadata)
	}
	reader, err := OpenPointAsset(path)
	if err != nil {
		t.Fatal(err)
	}
	got, valid, err := reader.DisplayScoreAt(0, 45)
	_ = reader.Close()
	if err != nil || !valid || got != record {
		t.Fatalf("display record = (%+v, %t, %v)", got, valid, err)
	}
	data, _ := os.ReadFile(path)
	data[len(data)-1] = 2
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePointAssetRecords(path); err == nil {
		t.Fatal("corrupt display-score asset was accepted")
	}
}

func TestPointAssetRejectsNonCanonicalZeroReservedAndHostileGeometry(t *testing.T) {
	geometry := canonicalTestPointGeometry(GridBounds{West: -25, East: 45, South: 30, North: 72}, 1, 1)
	alteredGeometry := geometry
	alteredGeometry.Quad.West += 1.0 / mapLibreTileExtent
	if _, err := WriteDailyPointAsset(filepath.Join(t.TempDir(), "bad-quad.nwp"), "test-release", PointKindForecastDay, alteredGeometry, []DailyPointRecord{{}}); err == nil || !strings.Contains(err.Error(), "canonical MapLibre") {
		t.Fatalf("non-canonical point quad was accepted: %v", err)
	}
	if _, err := WriteDailyPointAsset(filepath.Join(t.TempDir(), "bad.nwp"), "test-release", PointKindForecastDay, geometry, []DailyPointRecord{{Thermal: 1}}); err == nil {
		t.Fatal("non-canonical invalid record was accepted")
	}
	validPath := filepath.Join(t.TempDir(), "valid.nwp")
	if _, err := WriteDailyPointAsset(validPath, "test-release", PointKindForecastDay, geometry, []DailyPointRecord{{}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	data[120] = 1
	if err := os.WriteFile(validPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPointAsset(validPath); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved header corruption was accepted: %v", err)
	}
	header := encodePointHeader(PointKindForecastDay, DailyPointRecordBytes, geometry, make([]byte, 32))
	for index := 16; index < 24; index++ {
		header[index] = 0xff
	}
	if _, err := decodePointHeader(header); err == nil {
		t.Fatal("hostile point dimensions were accepted")
	}
}

func canonicalTestPointGeometry(bounds GridBounds, rows, cols uint32) PointGeometry {
	west, east, north, south := mapLibreImageQuad(GridSpec{Bounds: bounds, Rows: rows, Cols: cols})
	return PointGeometry{Bounds: bounds, Quad: PointQuad{West: west, East: east, North: north, South: south}, Rows: rows, Cols: cols}
}

func TestPointAssetMercatorTexelsPreserveValidCoastFallback(t *testing.T) {
	source := GridSpec{Bounds: GridBounds{West: -10, East: 10, South: 40, North: 50}, Rows: 1, Cols: 2}
	validWeather := DailyRecord{
		ComfortableTemperatureDeciC: 220, MinTemperatureDeciC: 180, MaxTemperatureDeciC: 260, DewPointDeciC: 120,
		WindDeciKMH: 80, SolarCentiMJm2: 1200, CloudPercent: 30, HumidityPercent: 60,
		Valid: true, HasDewPoint: true, HasSolar: true, HasCloud: true, HasHumidity: true,
	}
	geometry, records, err := DailyPointRecords(source, []DailyRecord{{}, validWeather})
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := NewWebMercatorMapper(source)
	if err != nil {
		t.Fatal(err)
	}
	for row := 0; row < int(geometry.Rows); row++ {
		for col := 0; col < int(geometry.Cols); col++ {
			_, _, selected := mapper.Select(row, col, func(candidateRow, candidateCol uint32) bool { return candidateCol == 1 })
			got := records[row*int(geometry.Cols)+col].Weather.Valid
			if got != selected {
				t.Fatalf("texel %d,%d valid=%t; mapper selected=%t", row, col, got, selected)
			}
		}
	}
	if _, ok := pointIndex(geometry, 10, 45); !ok {
		t.Fatal("rounded outer image edge was not addressable")
	}
}

func TestPointAssetRangeFingerprintIsProfileSpecificAndTamperEvident(t *testing.T) {
	record := testRangePointRecord(70)
	for index := range record.CyclingDays {
		record.CyclingDays[index] = 80
	}
	record.Cycling.Score, record.Cycling.MedianScore = 80, 80
	if err := validateRangePointRecord(record); err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, RangePointRecordBytes)
	encodeRangePointRecord(encoded, record)
	decoded, err := decodeRangePointRecord(encoded)
	if err != nil || decoded != record {
		t.Fatalf("range round trip = %+v, %v", decoded, err)
	}
	forged := record
	forged.NiceDays[0] = 10
	if err := validateRangePointRecord(forged); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("forged fingerprint was accepted: %v", err)
	}
}
