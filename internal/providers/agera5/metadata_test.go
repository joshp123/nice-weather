package agera5

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseMetadataRequiresMeasuredNineArrayContract(t *testing.T) {
	metadata, err := parseMetadata(bytes.NewReader(metadataFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.TimeLength() != 17362 {
		t.Fatalf("time length = %d", metadata.TimeLength())
	}
	fields := RequiredFields()
	if len(fields) != 9 || fields[0] != DaytimeMeanTemperature || fields[8] != MeanWindSpeed {
		t.Fatalf("fields = %v", fields)
	}
	fields[0] = "changed"
	if RequiredFields()[0] != DaytimeMeanTemperature {
		t.Fatal("RequiredFields returned shared storage")
	}
}

func TestParseMetadataFailsClosedOnContractDrift(t *testing.T) {
	valid := string(metadataFixture(t))
	tests := map[string]string{
		"missing field": strings.Replace(valid, `"Temperature_Air_2m_Max_24h/.zarray":`, `"removed/.zarray":`, 1),
		"shape":         strings.Replace(valid, `"shape":[17362,1801,3600]`, `"shape":[17362,1800,3600]`, 1),
		"chunk":         strings.Replace(valid, `"chunks":[2112,16,32]`, `"chunks":[2112,32,32]`, 1),
		"dtype":         strings.Replace(valid, `"dtype":"\u003cf4"`, `"dtype":"\u003cf8"`, 1),
		"fill":          strings.Replace(valid, `"fill_value":-9999`, `"fill_value":null`, 1),
		"filter":        strings.Replace(valid, `"filters":null`, `"filters":[]`, 1),
		"compressor":    strings.Replace(valid, `"cname":"lz4"`, `"cname":"zstd"`, 1),
		"separator":     strings.Replace(valid, `"zarr_format":2`, `"zarr_format":2,"dimension_separator":"/"`, 1),
		"unit":          strings.Replace(valid, `"units":"K"`, `"units":"C"`, 1),
		"unknown root":  strings.Replace(valid, `"zarr_consolidated_format":1`, `"zarr_consolidated_format":1,"unknown":true`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMetadata(strings.NewReader(input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseMetadataIsBoundedAndRejectsTruncation(t *testing.T) {
	if _, err := parseMetadata(strings.NewReader(strings.Repeat(" ", maxMetadataBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize error = %v", err)
	}
	fixture := metadataFixture(t)
	if _, err := parseMetadata(bytes.NewReader(fixture[:len(fixture)-1])); err == nil {
		t.Fatal("expected truncation error")
	}
}

func TestPlanChunksMapsDatesAndSpatialBlocksToExactKeys(t *testing.T) {
	metadata := parsedFixture(t)
	first := time.Date(2006, 1, 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	references, err := metadata.PlanChunks(first, last, BlockCoordinate{Latitude: 56, Longitude: 54})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 45 {
		t.Fatalf("references = %d", len(references))
	}
	if got, want := references[0].Key(), "Temperature_Air_2m_Mean_Day_Time/4.56.54"; got != want {
		t.Fatalf("first key = %q, want %q", got, want)
	}
	if references[8].Field() != MeanWindSpeed || references[9].time != 5 || references[44].Key() != "Wind_Speed_10m_Mean_24h/8.56.54" {
		t.Fatalf("unexpected time-major plan: first=%+v tenth=%+v last=%+v", references[8], references[9], references[44])
	}
	if !references[0].FirstDate().Equal(first) || !references[44].LastDate().Equal(last) || references[0].firstOffset != 1414 || references[44].lastOffset != 270 {
		t.Fatalf("range clipping = %s offset %d to %s offset %d", references[0].FirstDate(), references[0].firstOffset, references[44].LastDate(), references[44].lastOffset)
	}
}

func TestPlanChunksRejectsAmbiguousOrOutOfRangeInput(t *testing.T) {
	metadata := parsedFixture(t)
	valid := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		first time.Time
		last  time.Time
		block BlockCoordinate
	}{
		{valid.Add(time.Hour), valid, BlockCoordinate{}},
		{valid, valid.Add(-24 * time.Hour), BlockCoordinate{}},
		{sourceFirstDate.AddDate(0, 0, -1), valid, BlockCoordinate{}},
		{valid, valid, BlockCoordinate{Latitude: 113}},
		{valid, valid, BlockCoordinate{Longitude: 113}},
	}
	for index, test := range tests {
		if _, err := metadata.PlanChunks(test.first, test.last, test.block); err == nil {
			t.Fatalf("case %d: expected error", index)
		}
	}
}

func TestPlanningRequiresExactCoordinateValues(t *testing.T) {
	unbound, err := parseMetadata(bytes.NewReader(metadataFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := unbound.PlanChunks(date, date, BlockCoordinate{}); err == nil {
		t.Fatal("unbound metadata planned chunks")
	}
	fixtures := coordinateFixtures(t, unbound.timeLength)
	timeChunks := [][]byte{fixtures["time/0"], fixtures["time/1"]}

	shifted := make([]int32, timeCoordinateChunk)
	epoch := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	first := daysBetween(epoch, sourceFirstDate) + 1
	for index := range shifted {
		shifted[index] = int32(first + index)
	}
	badTime := append([][]byte(nil), timeChunks...)
	badTime[0] = bloscCopiedFixture(int32BytesForTest(shifted))
	if _, err := unbound.bindCoordinates(badTime, fixtures["latitude/0"], fixtures["longitude/0"]); err == nil {
		t.Fatal("accepted shifted time axis")
	}

	reversedLatitude := make([]float64, latitudeCount)
	for index := range reversedLatitude {
		reversedLatitude[index] = 90 - float64(index)/10
	}
	if _, err := unbound.bindCoordinates(timeChunks, bloscCopiedFixtureWithType(float64BytesForTest(reversedLatitude), 8), fixtures["longitude/0"]); err == nil {
		t.Fatal("accepted reversed latitude axis")
	}
}

func parsedFixture(t *testing.T) Metadata {
	t.Helper()
	metadata, err := parseMetadata(bytes.NewReader(metadataFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := coordinateFixtures(t, metadata.timeLength)
	timeChunks := make([][]byte, blockCount(metadata.timeLength, timeCoordinateChunk))
	for index := range timeChunks {
		timeChunks[index] = fixtures[fmt.Sprintf("time/%d", index)]
	}
	metadata, err = metadata.bindCoordinates(timeChunks, fixtures["latitude/0"], fixtures["longitude/0"])
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func coordinateFixtures(t *testing.T, timeLength int) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	timeChunks := blockCount(timeLength, timeCoordinateChunk)
	epoch := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	first := daysBetween(epoch, sourceFirstDate)
	for chunk := 0; chunk < timeChunks; chunk++ {
		values := make([]int32, timeCoordinateChunk)
		for index := range values {
			values[index] = int32(first + chunk*timeCoordinateChunk + index)
		}
		result[fmt.Sprintf("time/%d", chunk)] = bloscCopiedFixture(int32BytesForTest(values))
	}
	latitude := make([]float64, latitudeCount)
	for index := range latitude {
		latitude[index] = -90 + float64(index)/10
	}
	longitude := make([]float64, longitudeCount)
	for index := range longitude {
		longitude[index] = -180 + float64(index)/10
	}
	result["latitude/0"] = bloscCopiedFixtureWithType(float64BytesForTest(latitude), 8)
	result["longitude/0"] = bloscCopiedFixtureWithType(float64BytesForTest(longitude), 8)
	return result
}

func metadataFixture(t *testing.T) []byte {
	t.Helper()
	metadata := make(map[string]any)
	weatherArray := func() map[string]any {
		return map[string]any{
			"chunks": []int{2112, 16, 32}, "compressor": compressorFixture(), "dtype": "<f4", "fill_value": -9999,
			"filters": nil, "order": "C", "shape": []int{17362, 1801, 3600}, "zarr_format": 2,
		}
	}
	for _, contract := range fieldContracts {
		metadata[string(contract.field)+"/.zarray"] = weatherArray()
		metadata[string(contract.field)+"/.zattrs"] = map[string]any{
			"_ARRAY_DIMENSIONS": []string{"time", "latitude", "longitude"}, "grid_mapping": "crs", "units": contract.unit,
			"missing_value": -9999, "long_name": "fixture", "temporal_aggregation": "fixture",
		}
	}
	metadata["time/.zarray"] = map[string]any{"chunks": []int{17260}, "compressor": compressorFixture(), "dtype": "<i4", "fill_value": nil, "filters": nil, "order": "C", "shape": []int{17362}, "zarr_format": 2}
	metadata["time/.zattrs"] = map[string]any{"_ARRAY_DIMENSIONS": []string{"time"}, "calendar": "proleptic_gregorian", "units": "days since 1900-01-01"}
	metadata["latitude/.zarray"] = map[string]any{"chunks": []int{1801}, "compressor": compressorFixture(), "dtype": "<f8", "fill_value": "NaN", "filters": nil, "order": "C", "shape": []int{1801}, "zarr_format": 2}
	metadata["latitude/.zattrs"] = map[string]any{"_ARRAY_DIMENSIONS": []string{"latitude"}, "units": "degrees_north"}
	metadata["longitude/.zarray"] = map[string]any{"chunks": []int{3600}, "compressor": compressorFixture(), "dtype": "<f8", "fill_value": "NaN", "filters": nil, "order": "C", "shape": []int{3600}, "zarr_format": 2}
	metadata["longitude/.zattrs"] = map[string]any{"_ARRAY_DIMENSIONS": []string{"longitude"}, "units": "degrees_east"}
	document := map[string]any{"zarr_consolidated_format": 1, "metadata": metadata}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func compressorFixture() map[string]any {
	return map[string]any{"blocksize": 0, "clevel": 5, "cname": "lz4", "id": "blosc", "shuffle": 1}
}
