package agera5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pierrec/lz4/v4"
)

const (
	bloscHeaderBytes  = 16
	bloscShuffle      = 0x01
	bloscCopied       = 0x02
	bloscBitShuffle   = 0x04
	bloscFuture       = 0x08
	bloscDontSplit    = 0x10
	bloscLZ4Format    = 1
	floatBytes        = 4
	chunkValueCount   = timeChunkLength * latitudeChunkLength * longitudeChunkLength
	chunkDecodedBytes = chunkValueCount * floatBytes
)

// Block is one decoded source chunk in C order: time, latitude, longitude.
// Fill values remain explicit and are reported as unavailable by Value.
type Block struct {
	reference      ChunkRef
	values         []float32
	timeCount      int
	latitudeCount  int
	longitudeCount int
}

func (block Block) TimeCount() int      { return block.timeCount }
func (block Block) LatitudeCount() int  { return block.latitudeCount }
func (block Block) LongitudeCount() int { return block.longitudeCount }

// DecodedBytes reports the retained source allocation. Boundary and partial-
// date blocks still decode the complete physical Zarr chunk.
func (block Block) DecodedBytes() int { return len(block.values) * floatBytes }

func (block Block) Reference() ChunkRef { return block.reference }

func (block Block) Date(timeOffset int) (time.Time, error) {
	if timeOffset < 0 || timeOffset >= block.TimeCount() {
		return time.Time{}, errors.New("AgERA5 time offset is outside the requested dates")
	}
	return block.reference.firstDate.AddDate(0, 0, timeOffset), nil
}

func (block Block) Location(latitudeOffset, longitudeOffset int) (float64, float64, error) {
	if latitudeOffset < 0 || latitudeOffset >= block.LatitudeCount() || longitudeOffset < 0 || longitudeOffset >= block.LongitudeCount() {
		return 0, 0, errors.New("AgERA5 location offset is outside the logical source grid")
	}
	latitudeIndex := block.reference.block.Latitude*latitudeChunkLength + latitudeOffset
	longitudeIndex := block.reference.block.Longitude*longitudeChunkLength + longitudeOffset
	return -90 + float64(latitudeIndex)/10, -180 + float64(longitudeIndex)/10, nil
}

// Value reads one value from the full Zarr chunk. Boundary chunks still have
// the full chunk shape; callers use the reference and array shape to ignore
// overhanging cells.
func (block Block) Value(timeOffset, latitudeOffset, longitudeOffset int) (float32, bool, error) {
	if timeOffset < 0 || timeOffset >= block.TimeCount() || latitudeOffset < 0 || latitudeOffset >= block.LatitudeCount() || longitudeOffset < 0 || longitudeOffset >= block.LongitudeCount() {
		return 0, false, errors.New("AgERA5 block offset is outside the chunk")
	}
	if len(block.values) != chunkValueCount {
		return 0, false, errors.New("AgERA5 block is not initialized")
	}
	physicalTime := block.reference.firstOffset + timeOffset
	index := (physicalTime*latitudeChunkLength+latitudeOffset)*longitudeChunkLength + longitudeOffset
	value := block.values[index]
	return value, value != fillValue, nil
}

// DecodeChunk validates and decodes one measured AgERA5 Blosc/LZ4 float32
// chunk. It accepts no alternate codec, element type or chunk shape.
func DecodeChunk(reference ChunkRef, compressed []byte) (Block, error) {
	if err := validateReference(reference); err != nil {
		return Block{}, err
	}
	decoded, err := decodeBloscLZ4(compressed, chunkDecodedBytes)
	if err != nil {
		return Block{}, fmt.Errorf("decode AgERA5 chunk %s: %w", reference.Key(), err)
	}
	values := make([]float32, chunkValueCount)
	for index := range values {
		bits := binary.LittleEndian.Uint32(decoded[index*floatBytes : (index+1)*floatBytes])
		value := math.Float32frombits(bits)
		if value != fillValue && (float32IsNaN(value) || float32IsInf(value)) {
			return Block{}, fmt.Errorf("decode AgERA5 chunk %s: non-finite value at element %d", reference.Key(), index)
		}
		values[index] = value
	}
	return Block{
		reference: reference, values: values,
		timeCount:      reference.lastOffset - reference.firstOffset + 1,
		latitudeCount:  min(latitudeChunkLength, latitudeCount-reference.block.Latitude*latitudeChunkLength),
		longitudeCount: min(longitudeChunkLength, longitudeCount-reference.block.Longitude*longitudeChunkLength),
	}, nil
}

func decodeBloscLZ4(input []byte, expectedBytes int) ([]byte, error) {
	return decodeBlosc(input, expectedBytes, floatBytes)
}

func decodeBlosc(input []byte, expectedBytes, expectedTypeSize int) ([]byte, error) {
	if expectedBytes <= 0 || expectedTypeSize <= 0 || expectedBytes%expectedTypeSize != 0 {
		return nil, errors.New("expected Blosc output has an invalid element size")
	}
	if len(input) < bloscHeaderBytes {
		return nil, errors.New("truncated Blosc header")
	}
	if len(input) > expectedBytes+bloscHeaderBytes {
		return nil, fmt.Errorf("Blosc input exceeds %d-byte bound", expectedBytes+bloscHeaderBytes)
	}
	version, compressorVersion, flags, typeSize := input[0], input[1], input[2], input[3]
	decodedBytes := int(binary.LittleEndian.Uint32(input[4:8]))
	blockSize := int(binary.LittleEndian.Uint32(input[8:12]))
	compressedBytes := int(binary.LittleEndian.Uint32(input[12:16]))
	if version != 2 || compressorVersion != 1 {
		return nil, fmt.Errorf("unsupported Blosc/LZ4 versions %d/%d", version, compressorVersion)
	}
	// Metadata pins LZ4; the header's format bits must agree.
	if flags&bloscFuture != 0 || flags&bloscBitShuffle != 0 || flags&bloscShuffle == 0 || int(flags>>5) != bloscLZ4Format {
		return nil, fmt.Errorf("unsupported Blosc flags 0x%02x", flags)
	}
	if int(typeSize) != expectedTypeSize {
		return nil, fmt.Errorf("Blosc type size is %d, want %d", typeSize, expectedTypeSize)
	}
	if decodedBytes != expectedBytes {
		return nil, fmt.Errorf("Blosc output size is %d, want %d", decodedBytes, expectedBytes)
	}
	if compressedBytes != len(input) {
		return nil, fmt.Errorf("Blosc compressed size is %d, body has %d bytes", compressedBytes, len(input))
	}
	if blockSize <= 0 || blockSize > decodedBytes || blockSize%int(typeSize) != 0 {
		return nil, fmt.Errorf("invalid Blosc block size %d", blockSize)
	}

	output := make([]byte, decodedBytes)
	if flags&bloscCopied != 0 {
		if compressedBytes != decodedBytes+bloscHeaderBytes {
			return nil, errors.New("copied Blosc payload has inconsistent size")
		}
		copy(output, input[bloscHeaderBytes:])
		return output, nil
	}

	blockCount := (decodedBytes + blockSize - 1) / blockSize
	tableEnd := bloscHeaderBytes + blockCount*4
	if tableEnd > compressedBytes {
		return nil, errors.New("truncated Blosc block table")
	}
	starts := make([]int, blockCount+1)
	for index := 0; index < blockCount; index++ {
		starts[index] = int(binary.LittleEndian.Uint32(input[bloscHeaderBytes+index*4:]))
		if starts[index] < tableEnd || starts[index] > compressedBytes || (index > 0 && starts[index] <= starts[index-1]) {
			return nil, fmt.Errorf("invalid Blosc block %d offset %d", index, starts[index])
		}
	}
	if starts[0] != tableEnd {
		return nil, fmt.Errorf("first Blosc block starts at %d, want %d", starts[0], tableEnd)
	}
	starts[blockCount] = compressedBytes

	for blockIndex := 0; blockIndex < blockCount; blockIndex++ {
		decodedStart := blockIndex * blockSize
		decodedBlockSize := blockSize
		leftover := decodedStart+decodedBlockSize > decodedBytes
		if leftover {
			decodedBlockSize = decodedBytes - decodedStart
		}
		block, consumed, err := decodeBloscBlock(input[starts[blockIndex]:starts[blockIndex+1]], decodedBlockSize, int(typeSize), flags&bloscDontSplit != 0, leftover)
		if err != nil {
			return nil, fmt.Errorf("Blosc block %d: %w", blockIndex, err)
		}
		if consumed != starts[blockIndex+1]-starts[blockIndex] {
			return nil, fmt.Errorf("Blosc block %d consumed %d of %d bytes", blockIndex, consumed, starts[blockIndex+1]-starts[blockIndex])
		}
		copy(output[decodedStart:decodedStart+decodedBlockSize], block)
	}
	return output, nil
}

func decodeBloscBlock(input []byte, decodedBytes, typeSize int, dontSplit, leftover bool) ([]byte, int, error) {
	splitCount := 1
	if !dontSplit && !leftover && typeSize <= 16 && decodedBytes/typeSize >= 128 {
		splitCount = typeSize
	}
	if decodedBytes%splitCount != 0 {
		return nil, 0, errors.New("block size is not divisible by split count")
	}
	splitOutputBytes := decodedBytes / splitCount
	shuffled := make([]byte, decodedBytes)
	offset := 0
	for split := 0; split < splitCount; split++ {
		if offset+4 > len(input) {
			return nil, 0, errors.New("truncated split header")
		}
		compressedBytes := int(binary.LittleEndian.Uint32(input[offset : offset+4]))
		offset += 4
		if compressedBytes <= 0 || compressedBytes > len(input)-offset {
			return nil, 0, fmt.Errorf("invalid split size %d", compressedBytes)
		}
		destination := shuffled[split*splitOutputBytes : (split+1)*splitOutputBytes]
		if compressedBytes == splitOutputBytes {
			copy(destination, input[offset:offset+compressedBytes])
		} else {
			written, err := lz4.UncompressBlock(input[offset:offset+compressedBytes], destination)
			if err != nil {
				return nil, 0, fmt.Errorf("decompress LZ4 split: %w", err)
			}
			if written != splitOutputBytes {
				return nil, 0, fmt.Errorf("LZ4 split produced %d bytes, want %d", written, splitOutputBytes)
			}
		}
		offset += compressedBytes
	}
	return unshuffle(shuffled, typeSize), offset, nil
}

func unshuffle(input []byte, typeSize int) []byte {
	elements := len(input) / typeSize
	output := make([]byte, len(input))
	for byteIndex := 0; byteIndex < typeSize; byteIndex++ {
		for element := 0; element < elements; element++ {
			output[element*typeSize+byteIndex] = input[byteIndex*elements+element]
		}
	}
	return output
}

func float32IsNaN(value float32) bool {
	bits := math.Float32bits(value)
	return bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0
}

func float32IsInf(value float32) bool {
	return math.Float32bits(value)&0x7fffffff == 0x7f800000
}
