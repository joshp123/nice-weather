package delivery

import "errors"

// EncodeRangePointRecord is the checked fixed-record seam used by tiled
// forecast-range delivery. It preserves the established 44-byte range record;
// callers cannot introduce another range representation.
func EncodeRangePointRecord(dst []byte, record RangePointRecord) error {
	if len(dst) != RangePointRecordBytes {
		return errors.New("invalid forecast-window point record size")
	}
	if err := validateRangePointRecord(record); err != nil {
		return err
	}
	clear(dst)
	encodeRangePointRecord(dst, record)
	return nil
}

// DecodeRangePointRecord decodes and validates the established fixed record.
func DecodeRangePointRecord(src []byte) (RangePointRecord, error) {
	return decodeRangePointRecord(src)
}
