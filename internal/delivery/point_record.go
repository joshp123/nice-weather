package delivery

import (
	"errors"
	"fmt"
	"nice-weather-finder/internal/weather"
)

const (
	DailyPointRecordBytes = 30
	RangePointRecordBytes = 44
	ForecastPointDayCount = 14
)

type DailyPointRecord struct {
	Weather      DailyRecord
	Thermal      uint8
	Dryness      uint8
	Sun          uint8
	NiceScore    uint8
	NiceWind     uint8
	CyclingScore uint8
	CyclingWind  uint8
	SummaryCode  uint8
}

type RangePointRecord struct {
	Nice        RangeRecord
	Cycling     RangeRecord
	NiceDays    [ForecastPointDayCount]uint8
	CyclingDays [ForecastPointDayCount]uint8
}

func DailyPointRecordFromWeather(record DailyRecord) (DailyPointRecord, error) {
	if err := validateRecord(record); err != nil {
		return DailyPointRecord{}, err
	}
	if !record.Valid {
		return DailyPointRecord{}, nil
	}
	metrics, err := record.WeatherMetrics(1)
	if err != nil {
		return DailyPointRecord{}, err
	}
	nice, cycling := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
	if !nice.Available || !cycling.Available || nice.Components.Thermal != cycling.Components.Thermal || nice.Components.Dryness != cycling.Components.Dryness || nice.Components.Sun != cycling.Components.Sun {
		return DailyPointRecord{}, errors.New("daily point scoring did not produce shared component evidence")
	}
	summary, err := weather.EncodeSummary(metrics)
	if err != nil {
		return DailyPointRecord{}, err
	}
	point := DailyPointRecord{
		Weather: record, Thermal: uint8(nice.Components.Thermal), Dryness: uint8(nice.Components.Dryness), Sun: uint8(nice.Components.Sun),
		NiceScore: uint8(nice.Score), NiceWind: uint8(nice.Components.Wind), CyclingScore: uint8(cycling.Score), CyclingWind: uint8(cycling.Components.Wind), SummaryCode: summary,
	}
	return point, validateDailyPointRecord(point)
}

func validateDailyPointRecord(record DailyPointRecord) error {
	if !record.Weather.Valid {
		if record != (DailyPointRecord{}) {
			return errors.New("invalid daily point record must contain only zero values")
		}
		return nil
	}
	if err := validateRecord(record.Weather); err != nil {
		return err
	}
	for name, value := range map[string]uint8{"thermal": record.Thermal, "dryness": record.Dryness, "sun": record.Sun, "nice score": record.NiceScore, "nice wind": record.NiceWind, "cycling score": record.CyclingScore, "cycling wind": record.CyclingWind} {
		if value > 100 {
			return fmt.Errorf("daily point %s exceeds 100", name)
		}
	}
	metrics, err := record.Weather.WeatherMetrics(1)
	if err != nil {
		return err
	}
	nice, cycling := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
	if int(record.Thermal) != nice.Components.Thermal || int(record.Dryness) != nice.Components.Dryness || int(record.Sun) != nice.Components.Sun || int(record.NiceScore) != nice.Score || int(record.NiceWind) != nice.Components.Wind || int(record.CyclingScore) != cycling.Score || int(record.CyclingWind) != cycling.Components.Wind {
		return errors.New("daily point scores do not match its physical weather")
	}
	summary, err := weather.EncodeSummary(metrics)
	if err != nil || summary != record.SummaryCode {
		return errors.New("daily point summary does not match its physical weather")
	}
	return nil
}

func validateRangePointRecord(record RangePointRecord) error {
	if !record.Nice.Valid {
		if record != (RangePointRecord{}) {
			return errors.New("invalid forecast-window point record must contain only zero values")
		}
		return nil
	}
	if !record.Cycling.Valid {
		return errors.New("forecast-window point profiles must share support")
	}
	if err := validateRangeRecord(record.Nice); err != nil {
		return fmt.Errorf("nice profile: %w", err)
	}
	if err := validateRangeRecord(record.Cycling); err != nil {
		return fmt.Errorf("cycling profile: %w", err)
	}
	if record.Nice.DayCount != ForecastPointDayCount || record.Cycling.DayCount != ForecastPointDayCount || record.Nice.Thermal != record.Cycling.Thermal || record.Nice.Dryness != record.Cycling.Dryness || record.Nice.Sun != record.Cycling.Sun {
		return errors.New("forecast-window point profiles have inconsistent shared evidence")
	}
	for index := 0; index < ForecastPointDayCount; index++ {
		if record.NiceDays[index] > 100 || record.CyclingDays[index] > 100 {
			return fmt.Errorf("forecast-window point day %d score exceeds 100", index)
		}
	}
	if err := validateRangeFingerprint(record.Nice, record.NiceDays); err != nil {
		return fmt.Errorf("nice fingerprint: %w", err)
	}
	if err := validateRangeFingerprint(record.Cycling, record.CyclingDays); err != nil {
		return fmt.Errorf("cycling fingerprint: %w", err)
	}
	return nil
}

func validateRangeFingerprint(summary RangeRecord, scores [ForecastPointDayCount]uint8) error {
	days := make([]weather.Result, ForecastPointDayCount)
	components := weather.Components{Thermal: int(summary.Thermal), Dryness: int(summary.Dryness), Sun: int(summary.Sun), Wind: int(summary.Wind)}
	for index, score := range scores {
		value := int(score)
		days[index] = weather.Result{Available: true, Score: value, Rating: weather.Rating(value), Components: components}
	}
	got, err := weather.SummarizeRange(days)
	if err != nil {
		return err
	}
	if int(summary.Score) != got.Score || int(summary.MedianScore) != got.MedianScore || int(summary.NiceDays) != got.NiceDays || int(summary.AwfulDays) != got.AwfulDays || int(summary.LongestPoorRun) != got.LongestPoorRun || components != got.Components {
		return errors.New("aggregate does not match its 14 daily scores")
	}
	return nil
}

func encodeDailyPointRecord(dst []byte, record DailyPointRecord) {
	if !record.Weather.Valid {
		return
	}
	encodeRecord(dst[:dailyWeatherRecordBytes], record.Weather)
	dst[22], dst[23], dst[24] = record.Thermal, record.Dryness, record.Sun
	dst[25], dst[26] = record.NiceScore, record.NiceWind
	dst[27], dst[28] = record.CyclingScore, record.CyclingWind
	dst[29] = record.SummaryCode
}

func decodeDailyPointRecord(src []byte) (DailyPointRecord, error) {
	if len(src) != DailyPointRecordBytes {
		return DailyPointRecord{}, errors.New("invalid daily point record size")
	}
	weatherRecord, err := decodeRecord(src[:dailyWeatherRecordBytes])
	if err != nil {
		return DailyPointRecord{}, err
	}
	if !weatherRecord.Valid {
		if !allZero(src) {
			return DailyPointRecord{}, errors.New("invalid daily point record contains data")
		}
		return DailyPointRecord{}, nil
	}
	record := DailyPointRecord{
		Weather: weatherRecord, Thermal: src[22], Dryness: src[23], Sun: src[24], NiceScore: src[25], NiceWind: src[26], CyclingScore: src[27], CyclingWind: src[28], SummaryCode: src[29],
	}
	return record, validateDailyPointRecord(record)
}

func encodeRangePointRecord(dst []byte, record RangePointRecord) {
	if !record.Nice.Valid {
		return
	}
	dst[0], dst[1], dst[2] = record.Nice.Thermal, record.Nice.Dryness, record.Nice.Sun
	dst[3], dst[4], dst[5], dst[6], dst[7], dst[8] = record.Nice.Score, record.Nice.MedianScore, record.Nice.Wind, record.Nice.NiceDays, record.Nice.AwfulDays, record.Nice.LongestPoorRun
	dst[9], dst[10], dst[11], dst[12], dst[13], dst[14] = record.Cycling.Score, record.Cycling.MedianScore, record.Cycling.Wind, record.Cycling.NiceDays, record.Cycling.AwfulDays, record.Cycling.LongestPoorRun
	copy(dst[15:29], record.NiceDays[:])
	copy(dst[29:43], record.CyclingDays[:])
	dst[43] = 1
}

func decodeRangePointRecord(src []byte) (RangePointRecord, error) {
	if len(src) != RangePointRecordBytes {
		return RangePointRecord{}, errors.New("invalid forecast-window point record size")
	}
	if src[43] == 0 {
		if !allZero(src) {
			return RangePointRecord{}, errors.New("invalid forecast-window point record contains data")
		}
		return RangePointRecord{}, nil
	}
	if src[43] != 1 {
		return RangePointRecord{}, errors.New("forecast-window point record contains unknown flags")
	}
	record := RangePointRecord{
		Nice:    RangeRecord{Thermal: src[0], Dryness: src[1], Sun: src[2], Score: src[3], MedianScore: src[4], Wind: src[5], NiceDays: src[6], AwfulDays: src[7], LongestPoorRun: src[8], DayCount: ForecastPointDayCount, Valid: true},
		Cycling: RangeRecord{Thermal: src[0], Dryness: src[1], Sun: src[2], Score: src[9], MedianScore: src[10], Wind: src[11], NiceDays: src[12], AwfulDays: src[13], LongestPoorRun: src[14], DayCount: ForecastPointDayCount, Valid: true},
	}
	copy(record.NiceDays[:], src[15:29])
	copy(record.CyclingDays[:], src[29:43])
	return record, validateRangePointRecord(record)
}
