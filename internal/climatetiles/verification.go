package climatetiles

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

type ProbeExpectation uint8

const (
	ProbeMayHaveData ProbeExpectation = iota
	ProbeRequiresData
	ProbeRequiresNoData
)

// VerificationProbe names one product-relevant cell that must agree between
// the native climate record and every maximum-zoom scalar field.
type VerificationProbe struct {
	Label       string
	Period      string
	Longitude   float64
	Latitude    float64
	Expectation ProbeExpectation
}

type VerificationProbeResult struct {
	Probe           VerificationProbe
	SourceLongitude float64
	SourceLatitude  float64
	Record          delivery.ClimateDistributionRecord
}

type PublicationVerification struct {
	Pointer Pointer
	Index   Index
	Probes  []VerificationProbeResult
}

// VerifyCompletePublication is a read-only pre-deployment gate. The existing
// publication validator remains the only authority for pointer, index,
// catalogue, tree, checksum and binary-record integrity. This function adds
// representative point-to-scalar checks without duplicating those rules.
func VerifyCompletePublication(artifactRoot string, probes []VerificationProbe) (PublicationVerification, error) {
	climateRoot, err := existingPublicationRoot(artifactRoot)
	if err != nil {
		return PublicationVerification{}, err
	}
	pointer, index, validatedCatalogs, err := validateCurrentPublication(climateRoot)
	if err != nil {
		return PublicationVerification{}, err
	}
	if index.State != IndexStateComplete || len(index.Months) != 12 {
		return PublicationVerification{}, errors.New("climate publication is not a complete 12-month year")
	}
	catalogs := make(map[string]Catalog, len(validatedCatalogs))
	for _, catalog := range validatedCatalogs {
		catalogs[catalog.Period] = catalog
	}
	result := PublicationVerification{Pointer: pointer, Index: index, Probes: make([]VerificationProbeResult, 0, len(probes))}
	labels := make(map[string]struct{}, len(probes))
	for _, probe := range probes {
		if err := validateVerificationProbe(probe); err != nil {
			return PublicationVerification{}, err
		}
		if _, duplicate := labels[probe.Label]; duplicate {
			return PublicationVerification{}, fmt.Errorf("duplicate climate verification probe %q", probe.Label)
		}
		labels[probe.Label] = struct{}{}
		catalog, ok := catalogs[probe.Period]
		if !ok {
			return PublicationVerification{}, fmt.Errorf("climate verification probe %q uses an unpublished month", probe.Label)
		}
		releaseRoot := filepath.Join(climateRoot, "releases", catalog.ReleaseID)
		verified, err := verifyProbe(releaseRoot, catalog, probe)
		if err != nil {
			return PublicationVerification{}, fmt.Errorf("verify %s: %w", probe.Label, err)
		}
		result.Probes = append(result.Probes, verified)
	}
	if err := confirmVerificationPointer(climateRoot, pointer); err != nil {
		return PublicationVerification{}, errors.New("climate publication changed during verification")
	}
	return result, nil
}

func confirmVerificationPointer(climateRoot string, want Pointer) error {
	after, err := readCurrentPointer(climateRoot)
	if err != nil {
		return err
	}
	if after != want {
		return errors.New("climate pointer changed")
	}
	return nil
}

func validateVerificationProbe(probe VerificationProbe) error {
	if probe.Label == "" || !periodPattern.MatchString(probe.Period) || math.IsNaN(probe.Longitude) || math.IsInf(probe.Longitude, 0) || math.IsNaN(probe.Latitude) || math.IsInf(probe.Latitude, 0) {
		return errors.New("climate verification probe is invalid")
	}
	if probe.Expectation > ProbeRequiresNoData {
		return errors.New("climate verification probe expectation is invalid")
	}
	return nil
}

func verifyProbe(releaseRoot string, catalog Catalog, probe VerificationProbe) (VerificationProbeResult, error) {
	longitude, latitude, latitudeIndex, longitudeIndex, included := verificationCell(catalog.Geometry, probe.Longitude, probe.Latitude)
	if !included {
		return VerificationProbeResult{}, errors.New("coordinate is outside the published climate grid")
	}
	result := VerificationProbeResult{Probe: probe, SourceLongitude: longitude, SourceLatitude: latitude}
	blockCoordinate := agera5.BlockCoordinate{Latitude: latitudeIndex / 16, Longitude: longitudeIndex / 32}
	var point *PointBlock
	for index := range catalog.PointBlocks {
		candidate := &catalog.PointBlocks[index]
		if candidate.LatitudeBlock == blockCoordinate.Latitude && candidate.LongitudeBlock == blockCoordinate.Longitude {
			point = candidate
			break
		}
	}
	if point != nil {
		identity, err := tiledelivery.NewClimateBlockIdentity(catalog.ReleaseID, catalog.Period, blockCoordinate)
		if err != nil {
			return VerificationProbeResult{}, err
		}
		reader, err := tiledelivery.OpenClimateBlock(filepath.Join(releaseRoot, filepath.FromSlash(point.Path)), identity, point.SHA256)
		if err != nil {
			return VerificationProbeResult{}, err
		}
		record, valid, readErr := reader.RecordAt(longitude, latitude)
		closeErr := reader.Close()
		if readErr != nil {
			return VerificationProbeResult{}, readErr
		}
		if closeErr != nil {
			return VerificationProbeResult{}, closeErr
		}
		if valid {
			result.Record = record
		}
	}

	for _, field := range Fields() {
		actual, available, err := verificationScalar(releaseRoot, catalog, field, longitude, latitude)
		if err != nil {
			return VerificationProbeResult{}, err
		}
		if result.Record.Valid {
			expected, err := verificationFieldValue(result.Record, field)
			if err != nil {
				return VerificationProbeResult{}, err
			}
			if !available || actual != expected {
				return VerificationProbeResult{}, fmt.Errorf("%s scalar is %d, available=%t; point record is %d", field, actual, available, expected)
			}
		} else if available {
			return VerificationProbeResult{}, fmt.Errorf("%s scalar invents data for a no-data point", field)
		}
	}

	switch probe.Expectation {
	case ProbeRequiresData:
		if !result.Record.Valid {
			return VerificationProbeResult{}, errors.New("expected climate data but the point is transparent")
		}
	case ProbeRequiresNoData:
		if result.Record.Valid {
			return VerificationProbeResult{}, errors.New("expected transparent water or unsupported data")
		}
	}
	return result, nil
}

func verificationCell(geometry Geometry, longitude, latitude float64) (float64, float64, int, int, bool) {
	if longitude < geometry.West || longitude > geometry.East || latitude < geometry.South || latitude > geometry.North {
		return 0, 0, 0, 0, false
	}
	column := int(math.Floor((longitude - geometry.West) / geometry.CellResolutionDegrees))
	rowFromSouth := int(math.Floor((latitude - geometry.South) / geometry.CellResolutionDegrees))
	if column == int(geometry.Columns) {
		column--
	}
	if rowFromSouth == int(geometry.Rows) {
		rowFromSouth--
	}
	if column < 0 || column >= int(geometry.Columns) || rowFromSouth < 0 || rowFromSouth >= int(geometry.Rows) {
		return 0, 0, 0, 0, false
	}
	sourceLongitude := geometry.West + (float64(column)+.5)*geometry.CellResolutionDegrees
	sourceLatitude := geometry.South + (float64(rowFromSouth)+.5)*geometry.CellResolutionDegrees
	longitudeIndex := int(math.Round((sourceLongitude + 180) * 10))
	latitudeIndex := int(math.Round((sourceLatitude + 90) * 10))
	return sourceLongitude, sourceLatitude, latitudeIndex, longitudeIndex, true
}

func verificationScalar(releaseRoot string, catalog Catalog, field Field, longitude, latitude float64) (uint8, bool, error) {
	id, row, column, err := tiledelivery.At(string(field), MaximumZoom, longitude, latitude)
	if err != nil {
		return 0, false, err
	}
	var tile *ScalarTile
	for index := range catalog.ScalarTiles {
		candidate := &catalog.ScalarTiles[index]
		if candidate.Field == field && candidate.Zoom == id.Zoom && candidate.X == id.X && candidate.Y == id.Y {
			tile = candidate
			break
		}
	}
	if tile == nil {
		return 0, false, nil
	}
	encoded, _, err := tiledelivery.ReadScalar(filepath.Join(releaseRoot, filepath.FromSlash(tile.Path)), tile.SHA256)
	if err != nil {
		return 0, false, err
	}
	value := encoded[(row+tiledelivery.ScalarGutter)*tiledelivery.ScalarTextureSize+column+tiledelivery.ScalarGutter]
	return value, value != tiledelivery.ScalarNoData, nil
}

func verificationFieldValue(record delivery.ClimateDistributionRecord, field Field) (uint8, error) {
	switch field {
	case FieldComfortGood:
		return record.ComfortGoodPercent, nil
	case FieldDryGood:
		return record.DryGoodPercent, nil
	case FieldBrightGood:
		return record.BrightGoodPercent, nil
	case FieldNiceGood:
		return record.Nice.GoodOrBetterPercent(), nil
	case FieldNiceCalmGood:
		return record.Nice.CalmGoodPercent, nil
	case FieldCyclingGood:
		return record.Cycling.GoodOrBetterPercent(), nil
	case FieldCyclingCalmGood:
		return record.Cycling.CalmGoodPercent, nil
	default:
		return 0, fmt.Errorf("unknown climate verification field %q", field)
	}
}
