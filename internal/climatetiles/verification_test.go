package climatetiles

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/tiledelivery"
)

func TestVerificationProbeMatchesNativePointAndEveryMaximumScalar(t *testing.T) {
	stage := fixtureStage(t, "verification")
	catalog, err := verificationFixtureBuilder(t, "verification", "month-11", false)(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(stage.ReleaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateRelease(stage.ReleaseRoot)
	if err != nil {
		t.Fatal(err)
	}

	probe := VerificationProbe{Label: "Malaga", Period: "month-11", Longitude: -4.5, Latitude: 36.7, Expectation: ProbeRequiresData}
	result, err := verifyProbe(stage.ReleaseRoot, validated, probe)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Record.Valid || result.Record.Nice.GoodOrBetterPercent() != 65 || result.Record.Cycling.GoodOrBetterPercent() != 65 || math.Abs(result.SourceLongitude-(-4.5)) > 1e-9 || math.Abs(result.SourceLatitude-36.7) > 1e-9 {
		t.Fatalf("verified probe = %+v", result)
	}

	ocean := VerificationProbe{Label: "Ocean", Period: "month-11", Longitude: 0, Latitude: 0, Expectation: ProbeRequiresNoData}
	transparent, err := verifyProbe(stage.ReleaseRoot, validated, ocean)
	if err != nil {
		t.Fatal(err)
	}
	if transparent.Record.Valid {
		t.Fatalf("ocean probe invented data: %+v", transparent)
	}
}

func TestCompleteVerificationRejectsPreviewBeforeProbing(t *testing.T) {
	artifacts := t.TempDir()
	month := installFixtureMonth(t, artifacts, 1, 61, fixtureProvenance())
	if _, err := BuildAndPublishIndex(context.Background(), artifacts, "preview", IndexStatePreview, []MonthReference{month}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCompletePublication(artifacts, []VerificationProbe{{Label: "test", Period: "month-01"}}); err == nil {
		t.Fatal("preview climate publication passed the complete-release gate")
	}
}

func TestVerificationProbeRequiresExpectedAvailability(t *testing.T) {
	stage := fixtureStage(t, "availability")
	catalog, err := verificationFixtureBuilder(t, "availability", "month-01", false)(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(stage.ReleaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateRelease(stage.ReleaseRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyProbe(stage.ReleaseRoot, validated, VerificationProbe{Label: "wrong land", Period: "month-01", Longitude: 0, Latitude: 0, Expectation: ProbeRequiresData}); err == nil {
		t.Fatal("no-data point satisfied a required-data probe")
	}
	if _, err := verifyProbe(stage.ReleaseRoot, validated, VerificationProbe{Label: "wrong ocean", Period: "month-01", Longitude: -4.5, Latitude: 36.7, Expectation: ProbeRequiresNoData}); err == nil {
		t.Fatal("data point satisfied a required-no-data probe")
	}
}

func TestVerificationProbeRejectsWrongTargetScalar(t *testing.T) {
	stage := fixtureStage(t, "mismatch")
	catalog, err := verificationFixtureBuilder(t, "mismatch", "month-11", true)(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCatalog(stage.ReleaseRoot, catalog); err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateRelease(stage.ReleaseRoot)
	if err != nil {
		t.Fatal(err)
	}
	probe := VerificationProbe{Label: "Malaga", Period: "month-11", Longitude: -4.5, Latitude: 36.7, Expectation: ProbeRequiresData}
	if _, err := verifyProbe(stage.ReleaseRoot, validated, probe); err == nil {
		t.Fatal("wrong target scalar passed point-to-scalar verification")
	}
}

func TestVerificationPointerConfirmationRejectsRotation(t *testing.T) {
	artifacts := t.TempDir()
	month := installFixtureMonth(t, artifacts, 1, 61, fixtureProvenance())
	want, err := BuildAndPublishIndex(context.Background(), artifacts, "preview", IndexStatePreview, []MonthReference{month})
	if err != nil {
		t.Fatal(err)
	}
	climateRoot, err := existingPublicationRoot(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	changed := want
	changed.IndexID = "rotated"
	payload, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(climateRoot, "current.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := confirmVerificationPointer(climateRoot, want); err == nil {
		t.Fatal("rotated pointer passed final verification confirmation")
	}
}

func verificationFixtureBuilder(t *testing.T, releaseID, period string, mismatch bool) Builder {
	t.Helper()
	return func(_ context.Context, stage Stage) (Catalog, error) {
		block := agera5.BlockCoordinate{Latitude: 79, Longitude: 54}
		identity, err := tiledelivery.NewClimateBlockIdentity(releaseID, period, block)
		if err != nil {
			return Catalog{}, err
		}
		record := validClimateRecord()
		records := make([]delivery.ClimateDistributionRecord, int(identity.LatitudeCount)*int(identity.LongitudeCount))
		const targetRecord = 3*32 + 27
		records[targetRecord] = record
		asset, err := tiledelivery.WriteClimateBlock(filepath.Join(stage.ReleaseRoot, filepath.FromSlash(PointBlockPath(period, block))), releaseID, period, block, records)
		if err != nil {
			return Catalog{}, err
		}
		point, err := PointBlockFromAsset(stage.ReleaseRoot, asset)
		if err != nil {
			return Catalog{}, err
		}

		writer, err := tiledelivery.NewWriter(stage.Root, releaseID)
		if err != nil {
			return Catalog{}, err
		}
		var scalars []ScalarTile
		for _, field := range Fields() {
			value, err := verificationFieldValue(record, field)
			if err != nil {
				return Catalog{}, err
			}
			for zoom := uint8(0); zoom <= MaximumZoom; zoom++ {
				id, targetRow, targetColumn, err := tiledelivery.At(string(field), zoom, -4.5, 36.7)
				if err != nil {
					return Catalog{}, err
				}
				targetX := int64(id.X)*tiledelivery.TileSize + int64(targetColumn)
				targetY := int64(id.Y)*tiledelivery.TileSize + int64(targetRow)
				scalarAsset, err := writer.WriteScalar(id, period, func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
					if zoom == MaximumZoom && sample.GlobalX == targetX && sample.GlobalY == targetY {
						if mismatch && field == FieldNiceGood {
							return (value + 1) % 101, true, nil
						}
						return value, true, nil
					}
					return (value + 1) % 101, true, nil
				})
				if err != nil {
					return Catalog{}, err
				}
				scalar, err := ScalarTileFromAsset(stage.ReleaseRoot, field, scalarAsset)
				if err != nil {
					return Catalog{}, err
				}
				scalars = append(scalars, scalar)
			}
		}
		return NewCatalog(releaseID, period, fixtureProvenance(), []PointBlock{point}, scalars), nil
	}
}
