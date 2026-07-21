package delivery

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func TestClimateDistributionRecordRoundTripAndFixedLayout(t *testing.T) {
	record := validClimateDistributionRecord()
	encoded := make([]byte, ClimateDistributionRecordBytes)
	if err := EncodeClimateDistributionRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 36 || encoded[0] != 1 || encoded[1] != 72 || encoded[2] != 81 || encoded[3] != 64 {
		t.Fatalf("fixed header fields = %v", encoded[:4])
	}
	assertClimateProfileLayout(t, encoded[4:20], record.Nice)
	assertClimateProfileLayout(t, encoded[20:36], record.Cycling)

	decoded, err := DecodeClimateDistributionRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %+v; want %+v", decoded, record)
	}

	second := make([]byte, ClimateDistributionRecordBytes)
	if err := EncodeClimateDistributionRecord(second, decoded); err != nil || !reflect.DeepEqual(second, encoded) {
		t.Fatalf("second encoding changed bytes: %v, %v", second, err)
	}
}

func TestClimateDistributionInvalidRecordIsExactlyAllZero(t *testing.T) {
	encoded := make([]byte, ClimateDistributionRecordBytes)
	for index := range encoded {
		encoded[index] = 0xff
	}
	if err := EncodeClimateDistributionRecord(encoded, ClimateDistributionRecord{}); err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeClimateDistributionRecord(encoded); err != nil || decoded != (ClimateDistributionRecord{}) {
		t.Fatalf("zero record = %+v, %v", decoded, err)
	}
	for index := 1; index < ClimateDistributionRecordBytes; index++ {
		hostile := make([]byte, ClimateDistributionRecordBytes)
		hostile[index] = 1
		if _, err := DecodeClimateDistributionRecord(hostile); err == nil {
			t.Fatalf("invalid record with nonzero byte %d was accepted", index)
		}
	}

	invalid := validClimateDistributionRecord()
	invalid.Valid = false
	if err := EncodeClimateDistributionRecord(encoded, invalid); err == nil {
		t.Fatal("invalid record with evidence was accepted")
	}
}

func TestClimateDistributionRejectsUnknownFlagsAndWrongLengths(t *testing.T) {
	valid := encodeClimateRecordForTest(t, validClimateDistributionRecord())
	for _, flag := range []byte{2, 3, 0x80, 0xff} {
		hostile := append([]byte(nil), valid...)
		hostile[0] = flag
		if _, err := DecodeClimateDistributionRecord(hostile); err == nil || !strings.Contains(err.Error(), "unknown flags") {
			t.Fatalf("flag %#x error = %v", flag, err)
		}
	}
	for _, size := range []int{0, ClimateDistributionRecordBytes - 1, ClimateDistributionRecordBytes + 1} {
		if err := EncodeClimateDistributionRecord(make([]byte, size), validClimateDistributionRecord()); err == nil {
			t.Fatalf("encoded into %d bytes", size)
		}
		if _, err := DecodeClimateDistributionRecord(make([]byte, size)); err == nil {
			t.Fatalf("decoded %d bytes", size)
		}
	}
}

func TestClimateDistributionRejectsEveryOutOfRangeProbability(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ClimateDistributionRecord)
	}{
		{"comfort", func(record *ClimateDistributionRecord) { record.ComfortGoodPercent = 101 }},
		{"dry", func(record *ClimateDistributionRecord) { record.DryGoodPercent = 101 }},
		{"bright", func(record *ClimateDistributionRecord) { record.BrightGoodPercent = 101 }},
		{"nice median", func(record *ClimateDistributionRecord) { record.Nice.MedianScore = 101 }},
		{"nice calm", func(record *ClimateDistributionRecord) { record.Nice.CalmGoodPercent = 101 }},
		{"nice poor week", func(record *ClimateDistributionRecord) { record.Nice.PoorWeekPermille = 1001 }},
		{"cycling median", func(record *ClimateDistributionRecord) { record.Cycling.MedianScore = 101 }},
		{"cycling calm", func(record *ClimateDistributionRecord) { record.Cycling.CalmGoodPercent = 101 }},
		{"cycling poor week", func(record *ClimateDistributionRecord) { record.Cycling.PoorWeekPermille = 1001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validClimateDistributionRecord()
			test.change(&record)
			assertClimateRecordRejectedByEncoderAndDecoder(t, record)
		})
	}
}

func TestClimateDistributionRejectsEveryInvalidRatingShare(t *testing.T) {
	for _, profile := range []string{"Nice", "Cycling"} {
		for index, rating := range []string{"Awful", "Poor", "Mixed", "Good", "VeryGood", "Excellent"} {
			t.Run(profile+" "+rating, func(t *testing.T) {
				record := validClimateDistributionRecord()
				shares := &record.Nice.RatingShares
				if profile == "Cycling" {
					shares = &record.Cycling.RatingShares
				}
				setClimateRatingShare(shares, index, 1001)
				assertClimateRecordRejectedByEncoderAndDecoder(t, record)
			})
		}
		for _, total := range []uint16{999, 1001} {
			t.Run(profile+" total", func(t *testing.T) {
				record := validClimateDistributionRecord()
				shares := &record.Nice.RatingShares
				if profile == "Cycling" {
					shares = &record.Cycling.RatingShares
				}
				*shares = ClimateRatingShares{Awful: total}
				assertClimateRecordRejectedByEncoderAndDecoder(t, record)
			})
		}
	}
}

func TestClimateDistributionRejectsCyclingCalmerThanNice(t *testing.T) {
	record := validClimateDistributionRecord()
	record.Cycling.CalmGoodPercent = record.Nice.CalmGoodPercent + 1
	assertClimateRecordRejectedByEncoderAndDecoder(t, record)
}

func TestClimateDistributionDoesNotInferFromQuantisedShares(t *testing.T) {
	record := validClimateDistributionRecord()
	record.Nice.MedianScore = 100
	record.Nice.PoorWeekPermille = 1
	record.Nice.RatingShares = ClimateRatingShares{Mixed: 1000}
	encoded := encodeClimateRecordForTest(t, record)
	if _, err := DecodeClimateDistributionRecord(encoded); err != nil {
		t.Fatalf("quantised distribution was rejected: %v", err)
	}
}

func TestClimateDistributionRejectedEncodingPreservesDestination(t *testing.T) {
	destination := make([]byte, ClimateDistributionRecordBytes)
	for index := range destination {
		destination[index] = byte(index + 1)
	}
	want := append([]byte(nil), destination...)
	record := validClimateDistributionRecord()
	record.ComfortGoodPercent = 101
	if err := EncodeClimateDistributionRecord(destination, record); err == nil {
		t.Fatal("invalid record was encoded")
	}
	if !reflect.DeepEqual(destination, want) {
		t.Fatalf("rejected encoding changed destination: %v", destination)
	}
}

func TestClimateDistributionDerivesGoodAndAwfulProbabilities(t *testing.T) {
	profile := ClimateProfileDistribution{RatingShares: ClimateRatingShares{
		Awful: 54, Poor: 100, Mixed: 291, Good: 200, VeryGood: 200, Excellent: 155,
	}}
	if got := profile.GoodOrBetterPermille(); got != 555 {
		t.Fatalf("Good-or-better permille = %d; want 555", got)
	}
	if got := profile.GoodOrBetterPercent(); got != 56 {
		t.Fatalf("Good-or-better percent = %d; want nearest 56", got)
	}
	if got := profile.AwfulPermille(); got != 54 {
		t.Fatalf("Awful permille = %d; want 54", got)
	}
	if got := profile.AwfulPercent(); got != 5 {
		t.Fatalf("Awful percent = %d; want nearest 5", got)
	}
	profile.RatingShares.Good--
	profile.RatingShares.Mixed++
	if got := profile.GoodOrBetterPercent(); got != 55 {
		t.Fatalf("554 permille rounded to %d percent; want 55", got)
	}
}

func TestClimateDistributionDecoderRejectsCorruptValidBytes(t *testing.T) {
	base := encodeClimateRecordForTest(t, validClimateDistributionRecord())
	for _, offset := range []int{1, 2, 3, 4, 5, 20, 21} {
		hostile := append([]byte(nil), base...)
		hostile[offset] = 101
		if _, err := DecodeClimateDistributionRecord(hostile); err == nil {
			t.Fatalf("out-of-range byte %d was accepted", offset)
		}
	}
	for _, offset := range []int{6, 22} {
		hostile := append([]byte(nil), base...)
		binary.LittleEndian.PutUint16(hostile[offset:offset+2], 1001)
		if _, err := DecodeClimateDistributionRecord(hostile); err == nil {
			t.Fatalf("out-of-range poor-week field at %d was accepted", offset)
		}
	}
	for _, offset := range []int{8, 10, 12, 14, 16, 18, 24, 26, 28, 30, 32, 34} {
		hostile := append([]byte(nil), base...)
		binary.LittleEndian.PutUint16(hostile[offset:offset+2], 1001)
		if _, err := DecodeClimateDistributionRecord(hostile); err == nil {
			t.Fatalf("out-of-range rating field at %d was accepted", offset)
		}
	}
}

func validClimateDistributionRecord() ClimateDistributionRecord {
	return ClimateDistributionRecord{
		ComfortGoodPercent: 72,
		DryGoodPercent:     81,
		BrightGoodPercent:  64,
		Nice: ClimateProfileDistribution{
			MedianScore: 68, CalmGoodPercent: 83, PoorWeekPermille: 125,
			RatingShares: ClimateRatingShares{Awful: 50, Poor: 100, Mixed: 150, Good: 250, VeryGood: 300, Excellent: 150},
		},
		Cycling: ClimateProfileDistribution{
			MedianScore: 54, CalmGoodPercent: 58, PoorWeekPermille: 375,
			RatingShares: ClimateRatingShares{Awful: 100, Poor: 200, Mixed: 250, Good: 250, VeryGood: 150, Excellent: 50},
		},
		Valid: true,
	}
}

func assertClimateProfileLayout(t *testing.T, encoded []byte, want ClimateProfileDistribution) {
	t.Helper()
	if len(encoded) != 16 || encoded[0] != want.MedianScore || encoded[1] != want.CalmGoodPercent || binary.LittleEndian.Uint16(encoded[2:4]) != want.PoorWeekPermille {
		t.Fatalf("profile fixed fields = %v; want %+v", encoded[:4], want)
	}
	shares := []uint16{want.RatingShares.Awful, want.RatingShares.Poor, want.RatingShares.Mixed, want.RatingShares.Good, want.RatingShares.VeryGood, want.RatingShares.Excellent}
	for index, share := range shares {
		if got := binary.LittleEndian.Uint16(encoded[4+index*2 : 6+index*2]); got != share {
			t.Fatalf("rating share %d = %d; want %d", index, got, share)
		}
	}
}

func assertClimateRecordRejectedByEncoderAndDecoder(t *testing.T, record ClimateDistributionRecord) {
	t.Helper()
	encoded := make([]byte, ClimateDistributionRecordBytes)
	if err := EncodeClimateDistributionRecord(encoded, record); err == nil {
		t.Fatal("invalid record was encoded")
	}
	encoded = encodeClimateRecordForTest(t, validClimateDistributionRecord())
	encoded[0] = 1
	if record.ComfortGoodPercent != 72 {
		encoded[1] = record.ComfortGoodPercent
	} else if record.DryGoodPercent != 81 {
		encoded[2] = record.DryGoodPercent
	} else if record.BrightGoodPercent != 64 {
		encoded[3] = record.BrightGoodPercent
	} else {
		profile, offset := record.Nice, 4
		if !reflect.DeepEqual(record.Cycling, validClimateDistributionRecord().Cycling) {
			profile, offset = record.Cycling, 20
		}
		encodeClimateProfile(encoded[offset:offset+16], profile)
	}
	if _, err := DecodeClimateDistributionRecord(encoded); err == nil {
		t.Fatal("invalid encoded record was decoded")
	}
}

func encodeClimateRecordForTest(t *testing.T, record ClimateDistributionRecord) []byte {
	t.Helper()
	encoded := make([]byte, ClimateDistributionRecordBytes)
	if err := EncodeClimateDistributionRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func setClimateRatingShare(shares *ClimateRatingShares, index int, value uint16) {
	switch index {
	case 0:
		shares.Awful = value
	case 1:
		shares.Poor = value
	case 2:
		shares.Mixed = value
	case 3:
		shares.Good = value
	case 4:
		shares.VeryGood = value
	case 5:
		shares.Excellent = value
	}
}
