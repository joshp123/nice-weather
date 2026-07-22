package delivery

import "testing"

func TestRangePointCodecExportsEstablishedCheckedLayout(t *testing.T) {
	record := testRangePointRecord(70)
	encoded := make([]byte, RangePointRecordBytes)
	if err := EncodeRangePointRecord(encoded, record); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRangePointRecord(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != record {
		t.Fatalf("decoded range record = %+v; want %+v", decoded, record)
	}
	if err := EncodeRangePointRecord(encoded[:len(encoded)-1], record); err == nil {
		t.Fatal("short range record destination was accepted")
	}
	encoded[43] = 2
	if _, err := DecodeRangePointRecord(encoded); err == nil {
		t.Fatal("unknown range record flags were accepted")
	}
}
