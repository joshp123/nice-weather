package delivery

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const ClimateDistributionRecordBytes = 36

const climateDistributionValid = 1

// ClimateRatingShares stores the exact permille share in each canonical
// rating band. A complete distribution totals 1000.
type ClimateRatingShares struct {
	Awful     uint16
	Poor      uint16
	Mixed     uint16
	Good      uint16
	VeryGood  uint16
	Excellent uint16
}

// ClimateProfileDistribution is the profile-specific evidence for one
// historical distribution. PoorWeekPermille is the chance that a complete
// seven-day window contains the product's defined poor-weather spell.
type ClimateProfileDistribution struct {
	MedianScore      uint8
	CalmGoodPercent  uint8
	PoorWeekPermille uint16
	RatingShares     ClimateRatingShares
}

// GoodOrBetterPermille derives the overall probability from the canonical
// Good, Very good and Excellent rating shares.
func (profile ClimateProfileDistribution) GoodOrBetterPermille() uint16 {
	return profile.RatingShares.Good + profile.RatingShares.VeryGood + profile.RatingShares.Excellent
}

// GoodOrBetterPercent is the nearest whole percentage used by scalar maps.
func (profile ClimateProfileDistribution) GoodOrBetterPercent() uint8 {
	return permillePercent(profile.GoodOrBetterPermille())
}

// AwfulPermille derives the exact Awful probability from the rating shares.
func (profile ClimateProfileDistribution) AwfulPermille() uint16 {
	return profile.RatingShares.Awful
}

// AwfulPercent is the nearest whole Awful percentage.
func (profile ClimateProfileDistribution) AwfulPercent() uint8 {
	return permillePercent(profile.AwfulPermille())
}

// ClimateDistributionRecord contains shared physical-component probabilities
// and the two product-profile distributions. An invalid record has no climate
// meaning and must contain only zero values.
type ClimateDistributionRecord struct {
	ComfortGoodPercent uint8
	DryGoodPercent     uint8
	BrightGoodPercent  uint8
	Nice               ClimateProfileDistribution
	Cycling            ClimateProfileDistribution
	Valid              bool
}

// EncodeClimateDistributionRecord writes the fixed 36-byte delivery record.
func EncodeClimateDistributionRecord(dst []byte, record ClimateDistributionRecord) error {
	if len(dst) != ClimateDistributionRecordBytes {
		return fmt.Errorf("climate distribution record has %d bytes; want %d", len(dst), ClimateDistributionRecordBytes)
	}
	if !record.Valid {
		if record != (ClimateDistributionRecord{}) {
			return errors.New("invalid climate distribution record must contain only zero values")
		}
		clear(dst)
		return nil
	}
	if err := validateClimateDistributionRecord(record); err != nil {
		return err
	}
	clear(dst)
	dst[0] = climateDistributionValid
	dst[1], dst[2], dst[3] = record.ComfortGoodPercent, record.DryGoodPercent, record.BrightGoodPercent
	encodeClimateProfile(dst[4:20], record.Nice)
	encodeClimateProfile(dst[20:36], record.Cycling)
	return nil
}

// DecodeClimateDistributionRecord validates and decodes one fixed record.
func DecodeClimateDistributionRecord(src []byte) (ClimateDistributionRecord, error) {
	if len(src) != ClimateDistributionRecordBytes {
		return ClimateDistributionRecord{}, fmt.Errorf("climate distribution record has %d bytes; want %d", len(src), ClimateDistributionRecordBytes)
	}
	flags := src[0]
	if flags&^byte(climateDistributionValid) != 0 {
		return ClimateDistributionRecord{}, errors.New("climate distribution record has unknown flags")
	}
	if flags == 0 {
		for _, value := range src[1:] {
			if value != 0 {
				return ClimateDistributionRecord{}, errors.New("invalid climate distribution record contains nonzero values")
			}
		}
		return ClimateDistributionRecord{}, nil
	}
	record := ClimateDistributionRecord{
		ComfortGoodPercent: src[1],
		DryGoodPercent:     src[2],
		BrightGoodPercent:  src[3],
		Nice:               decodeClimateProfile(src[4:20]),
		Cycling:            decodeClimateProfile(src[20:36]),
		Valid:              true,
	}
	if err := validateClimateDistributionRecord(record); err != nil {
		return ClimateDistributionRecord{}, err
	}
	return record, nil
}

func validateClimateDistributionRecord(record ClimateDistributionRecord) error {
	if !record.Valid {
		return errors.New("climate distribution record is not valid")
	}
	shared := [...]struct {
		name  string
		value uint8
	}{
		{"comfort Good probability", record.ComfortGoodPercent},
		{"dry Good probability", record.DryGoodPercent},
		{"bright Good probability", record.BrightGoodPercent},
	}
	for _, field := range shared {
		if field.value > 100 {
			return fmt.Errorf("climate distribution %s exceeds 100", field.name)
		}
	}
	if err := validateClimateProfile("Nice", record.Nice); err != nil {
		return err
	}
	if err := validateClimateProfile("Cycling", record.Cycling); err != nil {
		return err
	}
	if record.Cycling.CalmGoodPercent > record.Nice.CalmGoodPercent {
		return errors.New("Cycling climate Calm probability exceeds Nice")
	}
	return nil
}

func validateClimateProfile(name string, profile ClimateProfileDistribution) error {
	if profile.MedianScore > 100 || profile.CalmGoodPercent > 100 {
		return fmt.Errorf("%s climate median or Calm probability exceeds 100", name)
	}
	if profile.PoorWeekPermille > 1000 {
		return fmt.Errorf("%s climate poor-week probability exceeds 1000 permille", name)
	}
	shares := [...]uint16{
		profile.RatingShares.Awful,
		profile.RatingShares.Poor,
		profile.RatingShares.Mixed,
		profile.RatingShares.Good,
		profile.RatingShares.VeryGood,
		profile.RatingShares.Excellent,
	}
	total := 0
	for _, share := range shares {
		if share > 1000 {
			return fmt.Errorf("%s climate rating share exceeds 1000 permille", name)
		}
		total += int(share)
	}
	if total != 1000 {
		return fmt.Errorf("%s climate rating shares total %d permille; want 1000", name, total)
	}
	return nil
}

func encodeClimateProfile(dst []byte, profile ClimateProfileDistribution) {
	dst[0], dst[1] = profile.MedianScore, profile.CalmGoodPercent
	binary.LittleEndian.PutUint16(dst[2:4], profile.PoorWeekPermille)
	shares := [...]uint16{
		profile.RatingShares.Awful,
		profile.RatingShares.Poor,
		profile.RatingShares.Mixed,
		profile.RatingShares.Good,
		profile.RatingShares.VeryGood,
		profile.RatingShares.Excellent,
	}
	for index, share := range shares {
		binary.LittleEndian.PutUint16(dst[4+index*2:6+index*2], share)
	}
}

func decodeClimateProfile(src []byte) ClimateProfileDistribution {
	return ClimateProfileDistribution{
		MedianScore:      src[0],
		CalmGoodPercent:  src[1],
		PoorWeekPermille: binary.LittleEndian.Uint16(src[2:4]),
		RatingShares: ClimateRatingShares{
			Awful:     binary.LittleEndian.Uint16(src[4:6]),
			Poor:      binary.LittleEndian.Uint16(src[6:8]),
			Mixed:     binary.LittleEndian.Uint16(src[8:10]),
			Good:      binary.LittleEndian.Uint16(src[10:12]),
			VeryGood:  binary.LittleEndian.Uint16(src[12:14]),
			Excellent: binary.LittleEndian.Uint16(src[14:16]),
		},
	}
}

func permillePercent(value uint16) uint8 {
	return uint8((value + 5) / 10)
}
