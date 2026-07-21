package agera5

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pierrec/lz4/v4"
)

func TestDecodeBloscMatchesCBlosc1216Fixture(t *testing.T) {
	// Generated once with c-blosc 1.21.6 using LZ4 level 5 and byte shuffle.
	// Keeping the bytes here makes the decoder test independent of its fixture
	// encoder and avoids a cgo/runtime dependency.
	compressed, err := hex.DecodeString("0201210400020000000200005c000000140000000b0000001f33010067503333333333110000007f9333d37313b3530700615013b3539333110000007f8889898a8b8b8c070061508b8b8c88890b0000001f43010067504343434343")
	if err != nil {
		t.Fatal(err)
	}
	values := make([]float32, 128)
	for index := range values {
		values[index] = 273.15 + float32(index%7)*1.25
	}
	decoded, err := decodeBloscLZ4(compressed, len(values)*4)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(floatBytesForTest(values)) {
		t.Fatal("c-blosc fixture decoded incorrectly")
	}
}

func TestDecodeBloscLZ4ByteShuffleAndRawSplits(t *testing.T) {
	values := make([]float32, 256)
	for index := range values {
		values[index] = float32(index%7)*1.25 + 273.15
	}
	raw := floatBytesForTest(values)
	compressed := bloscFixture(t, raw, len(raw), false)
	decoded, err := decodeBloscLZ4(compressed, len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(raw) {
		t.Fatal("decoded bytes differ")
	}

	// The copied form is a valid Blosc fallback and is not shuffled.
	copied := bloscCopiedFixture(raw)
	decoded, err = decodeBloscLZ4(copied, len(raw))
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("copied decode error=%v equal=%t", err, string(decoded) == string(raw))
	}
}

func TestDecodeBloscLZ4AssemblesMultipleBlocksAndPartialFinalBlock(t *testing.T) {
	values := make([]float32, 1301)
	for index := range values {
		values[index] = 270 + float32((index*17)%113)/8
	}
	raw := floatBytesForTest(values)
	compressed := bloscFixture(t, raw, 2048, false)
	decoded, err := decodeBloscLZ4(compressed, len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(raw) {
		t.Fatal("multi-block decoded bytes differ")
	}
}

func TestDecodeChunkReturnsTypedFillAwareBlock(t *testing.T) {
	reference := oneReference(t)
	values := make([]float32, chunkValueCount)
	for index := range values {
		values[index] = 280 + float32(index%11)/10
	}
	values[reference.firstOffset*latitudeChunkLength*longitudeChunkLength+17] = fillValue
	block, err := DecodeChunk(reference, bloscFixture(t, floatBytesForTest(values), chunkDecodedBytes, false))
	if err != nil {
		t.Fatal(err)
	}
	value, valid, err := block.Value(0, 0, 17)
	if err != nil || valid || value != fillValue {
		t.Fatalf("fill value = %g valid=%t error=%v", value, valid, err)
	}
	value, valid, err = block.Value(1, 2, 3)
	want := values[((reference.firstOffset+1)*latitudeChunkLength+2)*longitudeChunkLength+3]
	if err != nil || !valid || value != want {
		t.Fatalf("value = %g valid=%t error=%v want=%g", value, valid, err, want)
	}
	if _, _, err := block.Value(block.TimeCount(), 0, 0); err == nil {
		t.Fatal("expected bounds error")
	}
	date, err := block.Date(1)
	if err != nil || date.Format(time.DateOnly) != "2025-11-02" {
		t.Fatalf("date = %s error=%v", date, err)
	}
	latitude, longitude, err := block.Location(0, 0)
	if err != nil || math.Abs(latitude-36.4) > 1e-9 || math.Abs(longitude+7.2) > 1e-9 {
		t.Fatalf("location = %.1f,%.1f error=%v", latitude, longitude, err)
	}
}

func TestDecodeChunkRejectsNonFiniteWeatherValues(t *testing.T) {
	reference := oneReference(t)
	values := make([]float32, chunkValueCount)
	values[123] = float32(math.NaN())
	_, err := DecodeChunk(reference, bloscCopiedFixture(floatBytesForTest(values)))
	if err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("error = %v", err)
	}
}

func TestBlockRejectsRequestedAndGridPadding(t *testing.T) {
	metadata := parsedFixture(t)
	lastStoreDate := sourceFirstDate.AddDate(0, 0, metadata.timeLength-1)
	references, err := metadata.PlanChunks(lastStoreDate, lastStoreDate, BlockCoordinate{Latitude: 112, Longitude: 112})
	if err != nil {
		t.Fatal(err)
	}
	block, err := DecodeChunk(references[0], bloscCopiedFixture(floatBytesForTest(make([]float32, chunkValueCount))))
	if err != nil {
		t.Fatal(err)
	}
	if block.TimeCount() != 1 || block.LatitudeCount() != 9 || block.LongitudeCount() != 16 {
		t.Fatalf("logical shape = %d,%d,%d", block.TimeCount(), block.LatitudeCount(), block.LongitudeCount())
	}
	if _, _, err := block.Value(1, 0, 0); err == nil {
		t.Fatal("accepted unrequested time padding")
	}
	if _, _, err := block.Value(0, 9, 0); err == nil {
		t.Fatal("accepted latitude padding")
	}
	if _, _, err := block.Value(0, 0, 16); err == nil {
		t.Fatal("accepted longitude padding")
	}
}

func TestDecodeBloscRejectsHostileHeadersAndTruncation(t *testing.T) {
	raw := floatBytesForTest(make([]float32, 256))
	valid := bloscFixture(t, raw, len(raw), false)
	mutate := func(offset int, value byte) []byte {
		result := append([]byte(nil), valid...)
		result[offset] = value
		return result
	}
	tests := map[string][]byte{
		"empty":              nil,
		"truncated header":   valid[:15],
		"truncated body":     valid[:len(valid)-1],
		"version":            mutate(0, 3),
		"compressor version": mutate(1, 2),
		"no shuffle":         mutate(2, 0x20),
		"bit shuffle":        mutate(2, 0x25),
		"future flag":        mutate(2, 0x29),
		"wrong compressor":   mutate(2, 0x41),
		"type size":          mutate(3, 8),
	}
	zeroBlock := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(zeroBlock[8:12], 0)
	tests["zero block"] = zeroBlock
	wrongOutput := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(wrongOutput[4:8], uint32(len(raw)+4))
	tests["output size"] = wrongOutput
	wrongCompressed := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(wrongCompressed[12:16], uint32(len(valid)+1))
	tests["compressed size"] = wrongCompressed
	wrongStart := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(wrongStart[16:20], 21)
	tests["block start"] = wrongStart
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBloscLZ4(input, len(raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDecodeBloscRejectsInvalidSplitLengthsAndLZ4(t *testing.T) {
	raw := floatBytesForTest(make([]float32, 256))
	valid := bloscFixture(t, raw, len(raw), false)
	blockStart := int(binary.LittleEndian.Uint32(valid[16:20]))

	zeroSplit := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(zeroSplit[blockStart:blockStart+4], 0)
	if _, err := decodeBloscLZ4(zeroSplit, len(raw)); err == nil {
		t.Fatal("expected zero split error")
	}

	corrupt := append([]byte(nil), valid...)
	splitLength := int(binary.LittleEndian.Uint32(corrupt[blockStart : blockStart+4]))
	if splitLength >= len(raw)/4 {
		t.Skip("fixture used an uncompressed split")
	}
	for index := blockStart + 4; index < blockStart+4+splitLength; index++ {
		corrupt[index] = 0xff
	}
	if _, err := decodeBloscLZ4(corrupt, len(raw)); err == nil {
		t.Fatal("expected corrupt LZ4 error")
	}
}

func oneReference(t *testing.T) ChunkRef {
	t.Helper()
	metadata := parsedFixture(t)
	first := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 0, 1)
	references, err := metadata.PlanChunks(first, last, BlockCoordinate{Latitude: 79, Longitude: 54})
	if err != nil {
		t.Fatal(err)
	}
	return references[0]
}

func floatBytesForTest(values []float32) []byte {
	result := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(result[index*4:], math.Float32bits(value))
	}
	return result
}

func int32BytesForTest(values []int32) []byte {
	result := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(result[index*4:], uint32(value))
	}
	return result
}

func float64BytesForTest(values []float64) []byte {
	result := make([]byte, len(values)*8)
	for index, value := range values {
		binary.LittleEndian.PutUint64(result[index*8:], math.Float64bits(value))
	}
	return result
}

func bloscCopiedFixture(raw []byte) []byte {
	return bloscCopiedFixtureWithType(raw, 4)
}

func bloscCopiedFixtureWithType(raw []byte, typeSize byte) []byte {
	result := make([]byte, bloscHeaderBytes+len(raw))
	result[0], result[1], result[2], result[3] = 2, 1, 0x23, typeSize
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(raw)))
	binary.LittleEndian.PutUint32(result[8:12], uint32(len(raw)))
	binary.LittleEndian.PutUint32(result[12:16], uint32(len(result)))
	copy(result[16:], raw)
	return result
}

func bloscFixture(t *testing.T, raw []byte, blockSize int, dontSplit bool) []byte {
	t.Helper()
	if blockSize <= 0 || blockSize > len(raw) || blockSize%4 != 0 || len(raw)%4 != 0 {
		t.Fatal("invalid fixture block size")
	}
	blocks := (len(raw) + blockSize - 1) / blockSize
	result := make([]byte, bloscHeaderBytes+blocks*4)
	flags := byte(0x21)
	if dontSplit {
		flags |= bloscDontSplit
	}
	result[0], result[1], result[2], result[3] = 2, 1, flags, 4
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(raw)))
	binary.LittleEndian.PutUint32(result[8:12], uint32(blockSize))
	for blockIndex := 0; blockIndex < blocks; blockIndex++ {
		binary.LittleEndian.PutUint32(result[16+blockIndex*4:], uint32(len(result)))
		start := blockIndex * blockSize
		end := min(start+blockSize, len(raw))
		block := raw[start:end]
		shuffled := shuffleForTest(block, 4)
		splits := 4
		if dontSplit || len(block) != blockSize || len(block)/4 < 128 {
			splits = 1
		}
		for split := 0; split < splits; split++ {
			source := shuffled[split*len(shuffled)/splits : (split+1)*len(shuffled)/splits]
			maximum := lz4.CompressBlockBound(len(source))
			compressed := make([]byte, maximum)
			var compressor lz4.Compressor
			written, err := compressor.CompressBlock(source, compressed)
			if err != nil {
				t.Fatal(err)
			}
			if written == 0 || written == len(source) {
				compressed = source
			} else {
				compressed = compressed[:written]
			}
			length := make([]byte, 4)
			binary.LittleEndian.PutUint32(length, uint32(len(compressed)))
			result = append(result, length...)
			result = append(result, compressed...)
		}
	}
	binary.LittleEndian.PutUint32(result[12:16], uint32(len(result)))
	return result
}

func shuffleForTest(input []byte, typeSize int) []byte {
	elements := len(input) / typeSize
	result := make([]byte, len(input))
	for byteIndex := 0; byteIndex < typeSize; byteIndex++ {
		for element := 0; element < elements; element++ {
			result[byteIndex*elements+element] = input[element*typeSize+byteIndex]
		}
	}
	return result
}
