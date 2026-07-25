package delivery

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"testing"
)

func TestClimatePointRecordLiteralVector(t *testing.T) {
	record := validClimatePointRecord()
	encoded := make([]byte, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("0148514044537d00320064009600fa002c019600363a77016400c800fa00fa0096003200c9ffb6003d01640008014e00a1007b005400380144114f2a3d6c0200")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded = %x; want %x", encoded, want)
	}
	decoded, err := DecodeClimatePointRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %+v; want %+v", decoded, record)
	}
}

func TestClimatePointRecordRoundTripAndBoundaries(t *testing.T) {
	record := validClimatePointRecord()
	record.Physical = ClimatePhysicalSummary{
		DaytimeTemperatureP10C: -3276.8, DaytimeTemperatureP50C: 0, DaytimeTemperatureP90C: 3276.7,
		DaytimeMinimumP50C: -3276.8, DaytimeMaximumP50C: 3276.7,
		DewPointP50C: -3276.8, DewPointP90C: 3276.7,
		RelativeHumidityP50Percent: 0, MuggyDayPercent: 100, RainFreeDayPercent: 0,
		RainP90MM: 6553.5, CloudCoverP50Percent: 100, ClearSkyDayPercent: 0,
		WindP50KPH: 0, WindP90KPH: 6553.5,
	}
	record.Days = math.MaxUint16
	encoded := make([]byte, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClimatePointRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %+v; want %+v", decoded, record)
	}
	second := make([]byte, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(second, decoded); err != nil || !bytes.Equal(second, encoded) {
		t.Fatalf("second encoding changed bytes: %x, %v", second, err)
	}
}

func TestClimatePointRecordInvalidAndNoncanonicalEncodings(t *testing.T) {
	zero := make([]byte, ClimatePointRecordBytes)
	encodedZero := bytes.Repeat([]byte{0xff}, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(encodedZero, ClimatePointRecord{}); err != nil || !bytes.Equal(encodedZero, zero) {
		t.Fatalf("invalid record encoding = %x, %v; want all zero", encodedZero, err)
	}
	if decoded, err := DecodeClimatePointRecord(zero); err != nil || decoded != (ClimatePointRecord{}) {
		t.Fatalf("zero record = %+v, %v", decoded, err)
	}
	for _, mutate := range []struct {
		name string
		at   int
		set  byte
	}{
		{"invalid suffix", 36, 1},
		{"reserved", 63, 1},
		{"nested flags", 0, 2},
		{"nested invalid data", 1, 1},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			hostile := append([]byte(nil), zero...)
			hostile[mutate.at] = mutate.set
			if _, err := DecodeClimatePointRecord(hostile); err == nil {
				t.Fatal("noncanonical record was decoded")
			}
		})
	}
	base := encodeClimatePointForTest(t, validClimatePointRecord())
	for _, mutate := range []func([]byte){
		func(value []byte) { value[37] = 0 }, // p10 becomes 20.0, above p50.
		func(value []byte) { value[56] = 101 },
		func(value []byte) { value[63] = 1 },
		func(value []byte) { value[0] = 0 },
	} {
		hostile := append([]byte(nil), base...)
		mutate(hostile)
		if _, err := DecodeClimatePointRecord(hostile); err == nil {
			t.Fatal("malformed record was decoded")
		}
	}
	for _, size := range []int{0, ClimatePointRecordBytes - 1, ClimatePointRecordBytes + 1} {
		if _, err := DecodeClimatePointRecord(make([]byte, size)); err == nil {
			t.Fatalf("decoded %d bytes", size)
		}
		if err := EncodeClimatePointRecord(make([]byte, size), validClimatePointRecord()); err == nil {
			t.Fatalf("encoded %d bytes", size)
		}
	}
}

func TestClimatePointRecordRejectsInvalidPhysicalEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ClimatePointRecord)
	}{
		{"zero days", func(record *ClimatePointRecord) { record.Days = 0 }},
		{"nan temperature", func(record *ClimatePointRecord) { record.Physical.DaytimeTemperatureP10C = math.NaN() }},
		{"temperature quantiles", func(record *ClimatePointRecord) { record.Physical.DaytimeTemperatureP10C = 20 }},
		{"temperature range", func(record *ClimatePointRecord) { record.Physical.DaytimeMinimumP50C = 20 }},
		{"dew point quantiles", func(record *ClimatePointRecord) { record.Physical.DewPointP50C = 20 }},
		{"wind quantiles", func(record *ClimatePointRecord) { record.Physical.WindP50KPH = 40 }},
		{"fractional temperature tenth", func(record *ClimatePointRecord) { record.Physical.DaytimeTemperatureP50C = 18.25 }},
		{"fractional percentage", func(record *ClimatePointRecord) { record.Physical.MuggyDayPercent = 17.5 }},
		{"percentage exceeds 100", func(record *ClimatePointRecord) { record.Physical.CloudCoverP50Percent = 101 }},
		{"negative rain", func(record *ClimatePointRecord) { record.Physical.RainP90MM = -0.1 }},
		{"negative wind", func(record *ClimatePointRecord) { record.Physical.WindP50KPH = -0.1 }},
		{"rain exceeds uint16", func(record *ClimatePointRecord) { record.Physical.RainP90MM = 6553.6 }},
		{"temperature exceeds int16", func(record *ClimatePointRecord) { record.Physical.DaytimeTemperatureP90C = 3276.8 }},
		{"invalid nested distribution", func(record *ClimatePointRecord) { record.Distribution.Nice.MedianScore = 101 }},
		{"invalid record with evidence", func(record *ClimatePointRecord) { record.Distribution.Valid = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validClimatePointRecord()
			test.change(&record)
			destination := bytes.Repeat([]byte{0xa5}, ClimatePointRecordBytes)
			want := append([]byte(nil), destination...)
			if err := EncodeClimatePointRecord(destination, record); err == nil {
				t.Fatal("invalid record was encoded")
			}
			if !bytes.Equal(destination, want) {
				t.Fatal("rejected encoding changed destination")
			}
		})
	}
}

func FuzzClimatePointRecord(f *testing.F) {
	seed := make([]byte, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(seed, validClimatePointRecord()); err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add(make([]byte, ClimatePointRecordBytes))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) != ClimatePointRecordBytes {
			return
		}
		record, err := DecodeClimatePointRecord(encoded)
		if err != nil {
			return
		}
		roundTrip := make([]byte, ClimatePointRecordBytes)
		if err := EncodeClimatePointRecord(roundTrip, record); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(roundTrip, encoded) {
			t.Fatalf("decoded accepted noncanonical bytes: %x != %x", roundTrip, encoded)
		}
	})
}

func validClimatePointRecord() ClimatePointRecord {
	return ClimatePointRecord{
		Distribution: validClimateDistributionRecord(),
		Physical: ClimatePhysicalSummary{
			DaytimeTemperatureP10C: -5.5, DaytimeTemperatureP50C: 18.2, DaytimeTemperatureP90C: 31.7,
			DaytimeMinimumP50C: 10, DaytimeMaximumP50C: 26.4,
			DewPointP50C: 7.8, DewPointP90C: 16.1,
			RelativeHumidityP50Percent: 68, MuggyDayPercent: 17, RainFreeDayPercent: 79,
			RainP90MM: 12.3, CloudCoverP50Percent: 42, ClearSkyDayPercent: 61,
			WindP50KPH: 8.4, WindP90KPH: 31.2,
		},
		Days: 620,
	}
}

func encodeClimatePointForTest(t testing.TB, record ClimatePointRecord) []byte {
	t.Helper()
	encoded := make([]byte, ClimatePointRecordBytes)
	if err := EncodeClimatePointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	return encoded
}
