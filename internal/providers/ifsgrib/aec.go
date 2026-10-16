package ifsgrib

import (
	"fmt"
	"io"
	"math"
	"math/bits"
)

// Decode the unsigned, preprocessed CCSDS 121 blocks used by ECMWF. The
// predictor and code options follow libaec; see LICENSE.libaec.
func decodeAEC(data []byte, sampleBits, blockSize, referenceBlocks, count int) ([]uint32, error) {
	if sampleBits < 1 || sampleBits > 32 || blockSize != 32 || referenceBlocks != 128 || count%blockSize != 0 {
		return nil, fmt.Errorf("unsupported CCSDS geometry: bits=%d block=%d reference=%d count=%d", sampleBits, blockSize, referenceBlocks, count)
	}
	reader := bitReader{data: data}
	identifierBits := 3
	if sampleBits > 8 {
		identifierBits = 4
	}
	if sampleBits > 16 {
		identifierBits = 5
	}
	output := make([]uint32, count)
	maximum := uint32((uint64(1) << sampleBits) - 1)
	for intervalStart := 0; intervalStart < count; intervalStart += blockSize * referenceBlocks {
		intervalEnd := min(count, intervalStart+blockSize*referenceBlocks)
		position := intervalStart
		for position < intervalEnd {
			blockStart := position
			identifier := reader.read(identifierBits)
			lowSelector := uint32(0)
			if identifier == 0 {
				lowSelector = reader.read(1)
			}
			first := position == intervalStart
			encodedStart := position
			if first && identifier != uint32((1<<identifierBits)-1) {
				output[position] = reader.read(sampleBits)
				position++
				encodedStart++
			}
			blockEnd := blockStart + blockSize
			switch identifier {
			case 0:
				if lowSelector == 0 {
					zeroBlocks := int(reader.unary()) + 1
					blockInInterval := (blockStart - intervalStart) / blockSize
					if zeroBlocks == 5 {
						zeroBlocks = min(referenceBlocks-blockInInterval, 64-blockInInterval%64)
					} else if zeroBlocks > 5 {
						zeroBlocks--
					}
					position = blockStart + zeroBlocks*blockSize
					if position > intervalStart+referenceBlocks*blockSize {
						return nil, fmt.Errorf("CCSDS zero run crosses reference interval")
					}
					position = min(position, intervalEnd)
				} else {
					for position < blockEnd {
						code := reader.unary()
						if code > 90 {
							return nil, fmt.Errorf("CCSDS second-extension code %d exceeds 90", code)
						}
						sum := uint32((math.Sqrt(float64(8*code+1)) - 1) / 2)
						second := code - sum*(sum+1)/2
						if (position-blockStart)%2 == 0 {
							output[position] = sum - second
							position++
						}
						output[position] = second
						position++
					}
				}
			case uint32((1 << identifierBits) - 1):
				for position < blockEnd {
					output[position] = reader.read(sampleBits)
					position++
				}
			default:
				remainderBits := int(identifier) - 1
				for position < blockEnd {
					output[position] = reader.unary() << remainderBits
					position++
				}
				for index := encodedStart; index < blockEnd; index++ {
					output[index] |= reader.read(remainderBits)
				}
			}
			if reader.err != nil {
				return nil, reader.err
			}
		}
		previous := output[intervalStart]
		for index := intervalStart + 1; index < intervalEnd; index++ {
			difference := output[index]
			halfDifference := (difference >> 1) + (difference & 1)
			boundary := previous
			mask := uint32(0)
			if previous&(1<<uint(sampleBits-1)) != 0 {
				boundary = maximum ^ previous
				mask = maximum
			}
			if halfDifference <= boundary {
				if difference&1 == 0 {
					previous += difference >> 1
				} else {
					previous -= (difference >> 1) + 1
				}
			} else {
				previous = mask ^ difference
			}
			output[index] = previous
		}
	}
	return output, nil
}

type bitReader struct {
	data        []byte
	accumulator uint64
	available   int
	offset      int
	err         error
}

func (reader *bitReader) read(count int) uint32 {
	for reader.available < count {
		if reader.offset == len(reader.data) {
			reader.err = io.ErrUnexpectedEOF
			return 0
		}
		reader.accumulator = (reader.accumulator << 8) | uint64(reader.data[reader.offset])
		reader.offset++
		reader.available += 8
	}
	reader.available -= count
	if count == 0 {
		return 0
	}
	return uint32(reader.accumulator>>reader.available) & uint32((uint64(1)<<count)-1)
}
func (reader *bitReader) unary() uint32 {
	var zeros uint32
	for {
		if reader.available == 0 {
			reader.read(1)
			if reader.err != nil {
				return 0
			}
			reader.available++
		}
		remaining := reader.accumulator & ((uint64(1) << reader.available) - 1)
		if remaining != 0 {
			leading := reader.available - bits.Len64(remaining)
			reader.available -= leading + 1
			return zeros + uint32(leading)
		}
		zeros += uint32(reader.available)
		reader.available = 0
	}
}
