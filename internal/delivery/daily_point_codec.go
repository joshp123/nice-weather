package delivery

import "errors"

// EncodeDailyPointRecord encodes the canonical fixed forecast-day record.
// Native tiled delivery uses this function rather than defining a second
// weather or score representation.
func EncodeDailyPointRecord(dst []byte, record DailyPointRecord) error {
	if len(dst) != DailyPointRecordBytes {
		return errors.New("invalid daily point record size")
	}
	if err := validateDailyPointRecord(record); err != nil {
		return err
	}
	clear(dst)
	encodeDailyPointRecord(dst, record)
	return nil
}

// DecodeDailyPointRecord decodes and validates the canonical fixed
// forecast-day record.
func DecodeDailyPointRecord(src []byte) (DailyPointRecord, error) {
	return decodeDailyPointRecord(src)
}
