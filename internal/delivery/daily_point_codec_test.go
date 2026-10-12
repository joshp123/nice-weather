package delivery

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestExportedDailyPointCodecUsesCanonicalValidation(t *testing.T) {
	record, err := DailyPointRecordFromWeather(DailyRecord{
		ComfortableTemperatureDeciC: 240, MinTemperatureDeciC: 180, MaxTemperatureDeciC: 280, DewPointDeciC: 120,
		WindDeciKMH: 100, GustDeciKMH: 180, SolarCentiMJm2: 1900, CloudPercent: 20, HumidityPercent: 55,
		Valid: true, HasDewPoint: true, HasGust: true, HasSolar: true, HasCloud: true, HasHumidity: true, HasWetTime: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, DailyPointRecordBytes)
	if err := EncodeDailyPointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDailyPointRecord(encoded)
	if err != nil || decoded != record {
		t.Fatalf("round trip = %+v, %v; want %+v", decoded, err, record)
	}
	if err := EncodeDailyPointRecord(encoded[:len(encoded)-1], record); err == nil {
		t.Fatal("accepted short daily record destination")
	}
	invalid := record
	invalid.NiceScore = 101
	if err := EncodeDailyPointRecord(encoded, invalid); err == nil {
		t.Fatal("exported codec bypassed canonical score validation")
	}
	for index := range encoded {
		encoded[index] = 0xff
	}
	if err := EncodeDailyPointRecord(encoded, DailyPointRecord{}); err != nil {
		t.Fatal(err)
	}
	for index, value := range encoded {
		if value != 0 {
			t.Fatalf("no-data byte %d = %d", index, value)
		}
	}
}

func TestEuropeanGustBoundaryRecordUsesCanonicalScores(t *testing.T) {
	encoded, err := hex.DecodeString("ceffccffd4ffa3ff250070017b0276005e0364487f0008000004040300e3")
	if err != nil {
		t.Fatal(err)
	}
	point, err := DecodeDailyPointRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if point.NiceWind != 4 || point.NiceScore != 4 || point.CyclingWind != 0 || point.CyclingScore != 3 {
		t.Fatalf("European Oct 12 record: %+v", point)
	}
	canonical, err := DailyPointRecordFromWeather(point.Weather)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := make([]byte, DailyPointRecordBytes)
	if err := EncodeDailyPointRecord(roundTrip, canonical); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("canonical record = %x; want %x", roundTrip, encoded)
	}
	t.Logf("Oct 12 European record = %x", roundTrip)
}
