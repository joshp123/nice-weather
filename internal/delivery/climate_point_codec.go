package delivery

import (
	"bytes"
	"errors"
	"fmt"
	"math"
)

// ClimatePointRecordBytes is the fixed size of one recent-climate point
// record. The first 36 bytes remain the established distribution contract.
const ClimatePointRecordBytes = 64

// ClimatePhysicalSummary is the physical evidence behind a climate
// distribution. Values use their named physical units before encoding.
type ClimatePhysicalSummary struct {
	DaytimeTemperatureP10C     float64
	DaytimeTemperatureP50C     float64
	DaytimeTemperatureP90C     float64
	DaytimeMinimumP50C         float64
	DaytimeMaximumP50C         float64
	DewPointP50C               float64
	DewPointP90C               float64
	RelativeHumidityP50Percent float64
	MuggyDayPercent            float64
	RainFreeDayPercent         float64
	RainP90MM                  float64
	CloudCoverP50Percent       float64
	ClearSkyDayPercent         float64
	WindP50KPH                 float64
	WindP90KPH                 float64
}

// ClimatePointRecord combines the scored climate distribution with the
// physical evidence used to explain it. An invalid record is all zero.
type ClimatePointRecord struct {
	Distribution ClimateDistributionRecord
	Physical     ClimatePhysicalSummary
	Days         uint16
}

// EncodeClimatePointRecord writes one fixed 64-byte climate point record.
func EncodeClimatePointRecord(dst []byte, record ClimatePointRecord) error {
	if len(dst) != ClimatePointRecordBytes {
		return fmt.Errorf("climate point record has %d bytes; want %d", len(dst), ClimatePointRecordBytes)
	}
	if !record.Distribution.Valid {
		if record != (ClimatePointRecord{}) {
			return errors.New("invalid climate point record must contain only zero values")
		}
		clear(dst)
		return nil
	}

	var distribution [ClimateDistributionRecordBytes]byte
	if err := EncodeClimateDistributionRecord(distribution[:], record.Distribution); err != nil {
		return fmt.Errorf("climate point distribution: %w", err)
	}
	encoded, err := encodeClimatePhysicalSummary(record.Physical, record.Days)
	if err != nil {
		return err
	}

	clear(dst)
	copy(dst[:ClimateDistributionRecordBytes], distribution[:])
	copy(dst[ClimateDistributionRecordBytes:ClimatePointRecordBytes], encoded[:])
	return nil
}

// DecodeClimatePointRecord validates and decodes one fixed climate point
// record, including its embedded distribution contract.
func DecodeClimatePointRecord(src []byte) (ClimatePointRecord, error) {
	if len(src) != ClimatePointRecordBytes {
		return ClimatePointRecord{}, fmt.Errorf("climate point record has %d bytes; want %d", len(src), ClimatePointRecordBytes)
	}
	distribution, err := DecodeClimateDistributionRecord(src[:ClimateDistributionRecordBytes])
	if err != nil {
		return ClimatePointRecord{}, fmt.Errorf("climate point distribution: %w", err)
	}
	if !distribution.Valid {
		if !allZero(src) {
			return ClimatePointRecord{}, errors.New("invalid climate point record contains nonzero values")
		}
		return ClimatePointRecord{}, nil
	}
	if src[63] != 0 {
		return ClimatePointRecord{}, errors.New("climate point record contains nonzero reserved data")
	}

	physical := ClimatePhysicalSummary{
		DaytimeTemperatureP10C:     float64(int16Value(src[36:38])) / 10,
		DaytimeTemperatureP50C:     float64(int16Value(src[38:40])) / 10,
		DaytimeTemperatureP90C:     float64(int16Value(src[40:42])) / 10,
		DaytimeMinimumP50C:         float64(int16Value(src[42:44])) / 10,
		DaytimeMaximumP50C:         float64(int16Value(src[44:46])) / 10,
		DewPointP50C:               float64(int16Value(src[46:48])) / 10,
		DewPointP90C:               float64(int16Value(src[48:50])) / 10,
		RainP90MM:                  float64(uint16Value(src[50:52])) / 10,
		WindP50KPH:                 float64(uint16Value(src[52:54])) / 10,
		WindP90KPH:                 float64(uint16Value(src[54:56])) / 10,
		RelativeHumidityP50Percent: float64(src[56]),
		MuggyDayPercent:            float64(src[57]),
		RainFreeDayPercent:         float64(src[58]),
		CloudCoverP50Percent:       float64(src[59]),
		ClearSkyDayPercent:         float64(src[60]),
	}
	record := ClimatePointRecord{
		Distribution: distribution,
		Physical:     physical,
		Days:         uint16Value(src[61:63]),
	}
	if _, err := encodeClimatePhysicalSummary(record.Physical, record.Days); err != nil {
		return ClimatePointRecord{}, err
	}
	var canonical [ClimatePointRecordBytes]byte
	if err := EncodeClimatePointRecord(canonical[:], record); err != nil {
		return ClimatePointRecord{}, err
	}
	if !bytes.Equal(src, canonical[:]) {
		return ClimatePointRecord{}, errors.New("climate point record is noncanonical")
	}
	return record, nil
}

func encodeClimatePhysicalSummary(summary ClimatePhysicalSummary, days uint16) ([28]byte, error) {
	if days == 0 {
		return [28]byte{}, errors.New("climate point record has zero days")
	}
	temperatureP10, err := exactInt16Tenth(summary.DaytimeTemperatureP10C, "daytime temperature p10")
	if err != nil {
		return [28]byte{}, err
	}
	temperatureP50, err := exactInt16Tenth(summary.DaytimeTemperatureP50C, "daytime temperature p50")
	if err != nil {
		return [28]byte{}, err
	}
	temperatureP90, err := exactInt16Tenth(summary.DaytimeTemperatureP90C, "daytime temperature p90")
	if err != nil {
		return [28]byte{}, err
	}
	minimumP50, err := exactInt16Tenth(summary.DaytimeMinimumP50C, "daytime minimum p50")
	if err != nil {
		return [28]byte{}, err
	}
	maximumP50, err := exactInt16Tenth(summary.DaytimeMaximumP50C, "daytime maximum p50")
	if err != nil {
		return [28]byte{}, err
	}
	dewPointP50, err := exactInt16Tenth(summary.DewPointP50C, "dew point p50")
	if err != nil {
		return [28]byte{}, err
	}
	dewPointP90, err := exactInt16Tenth(summary.DewPointP90C, "dew point p90")
	if err != nil {
		return [28]byte{}, err
	}
	if temperatureP10 > temperatureP50 || temperatureP50 > temperatureP90 {
		return [28]byte{}, errors.New("climate daytime temperature quantiles are unordered")
	}
	if minimumP50 > temperatureP50 || temperatureP50 > maximumP50 {
		return [28]byte{}, errors.New("climate daytime temperature median is outside its range")
	}
	if dewPointP50 > dewPointP90 {
		return [28]byte{}, errors.New("climate dew point quantiles are unordered")
	}

	rainP90, err := exactUint16Tenth(summary.RainP90MM, "rain p90")
	if err != nil {
		return [28]byte{}, err
	}
	windP50, err := exactUint16Tenth(summary.WindP50KPH, "wind p50")
	if err != nil {
		return [28]byte{}, err
	}
	windP90, err := exactUint16Tenth(summary.WindP90KPH, "wind p90")
	if err != nil {
		return [28]byte{}, err
	}
	if windP50 > windP90 {
		return [28]byte{}, errors.New("climate wind quantiles are unordered")
	}
	relativeHumidity, err := exactPercent(summary.RelativeHumidityP50Percent, "relative humidity p50")
	if err != nil {
		return [28]byte{}, err
	}
	muggy, err := exactPercent(summary.MuggyDayPercent, "muggy-day")
	if err != nil {
		return [28]byte{}, err
	}
	rainFree, err := exactPercent(summary.RainFreeDayPercent, "rain-free-day")
	if err != nil {
		return [28]byte{}, err
	}
	cloudCover, err := exactPercent(summary.CloudCoverP50Percent, "cloud-cover p50")
	if err != nil {
		return [28]byte{}, err
	}
	clearSky, err := exactPercent(summary.ClearSkyDayPercent, "clear-sky-day")
	if err != nil {
		return [28]byte{}, err
	}

	var encoded [28]byte
	putInt16(encoded[0:2], temperatureP10)
	putInt16(encoded[2:4], temperatureP50)
	putInt16(encoded[4:6], temperatureP90)
	putInt16(encoded[6:8], minimumP50)
	putInt16(encoded[8:10], maximumP50)
	putInt16(encoded[10:12], dewPointP50)
	putInt16(encoded[12:14], dewPointP90)
	putUint16(encoded[14:16], rainP90)
	putUint16(encoded[16:18], windP50)
	putUint16(encoded[18:20], windP90)
	encoded[20], encoded[21], encoded[22] = relativeHumidity, muggy, rainFree
	encoded[23], encoded[24] = cloudCover, clearSky
	putUint16(encoded[25:27], days)
	return encoded, nil
}

func exactInt16Tenth(value float64, name string) (int16, error) {
	if !finite(value) {
		return 0, fmt.Errorf("%s is not finite", name)
	}
	scaled := value * 10
	if math.Trunc(scaled) != scaled || scaled < math.MinInt16 || scaled > math.MaxInt16 {
		return 0, fmt.Errorf("%s is not an exactly representable int16 tenth", name)
	}
	return int16(scaled), nil
}

func exactUint16Tenth(value float64, name string) (uint16, error) {
	if !finite(value) {
		return 0, fmt.Errorf("%s is not finite", name)
	}
	scaled := value * 10
	if math.Trunc(scaled) != scaled || scaled < 0 || scaled > math.MaxUint16 {
		return 0, fmt.Errorf("%s is not an exactly representable uint16 tenth", name)
	}
	return uint16(scaled), nil
}

func exactPercent(value float64, name string) (uint8, error) {
	if !finite(value) || math.Trunc(value) != value || value < 0 || value > 100 {
		return 0, fmt.Errorf("%s is not an exactly representable percentage", name)
	}
	return uint8(value), nil
}

func int16Value(src []byte) int16 {
	return int16(uint16Value(src))
}

func uint16Value(src []byte) uint16 {
	return uint16(src[0]) | uint16(src[1])<<8
}

func putInt16(dst []byte, value int16) {
	putUint16(dst, uint16(value))
}

func putUint16(dst []byte, value uint16) {
	dst[0], dst[1] = byte(value), byte(value>>8)
}
