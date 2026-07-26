package era5climateproduct

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/providers/era5hourly"
)

func TestLegacyPlanRequiresExactPublicationAuthority(t *testing.T) {
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, values := testPlanAndMaskData(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, false)
	maskPath := writeTestMask(t, values)
	if _, err := LoadDisplayMask(maskPath, plan); err == nil {
		t.Fatal("legacy acquisition plan authorised display publication without a sidecar")
	}

	encoded := testPublicationAuthority(plan, values)
	authorityPath, authoritySHA := writeTestPublicationAuthority(t, encoded)
	authority, err := LoadPublicationAuthority(authorityPath, authoritySHA, plan)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := LoadDisplayMaskWithAuthority(maskPath, plan, authority)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := New(testConfig(t.TempDir(), "legacy-authority"), plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(testResult(0, cell, 70)); err != nil {
		t.Fatal(err)
	}
	if err := builder.Commit(); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(builder.config.StagingRoot, "releases", builder.config.ReleaseID)
	catalog, err := ValidateRelease(releaseRoot, plan, mask)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Source.AcquisitionPlanSHA256 != plan.SHA256() ||
		catalog.Source.LogicalPlanSHA256 != plan.LogicalSHA256() ||
		catalog.Source.PublicationAuthorityKind != sidecarAuthorityKind ||
		catalog.Source.PublicationAuthoritySHA256 != authoritySHA ||
		catalog.Source.PublicationAuthoritySHA256 == catalog.Source.AcquisitionPlanSHA256 {
		t.Fatalf("catalog did not distinguish acquisition and publication authority: %+v", catalog.Source)
	}
}

func TestEncodePublicationAuthorityIsCanonicalAndReloadable(t *testing.T) {
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, values := testPlanAndMaskData(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, false)
	first, err := EncodePublicationAuthority(plan, values)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodePublicationAuthority(plan, append([]byte(nil), values...))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.JSON, second.JSON) || first.SHA256 != second.SHA256 ||
		first.DisplayMaskSHA256 != second.DisplayMaskSHA256 || first.CentreLandCells != 1 {
		t.Fatalf("publication authority encoding is not stable: first=%+v second=%+v", first, second)
	}
	root := t.TempDir()
	authorityPath := filepath.Join(root, "authority.json")
	maskPath := filepath.Join(root, "mask.bin")
	if err := os.WriteFile(authorityPath, first.JSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(maskPath, values, 0o600); err != nil {
		t.Fatal(err)
	}
	authority, err := LoadPublicationAuthority(authorityPath, first.SHA256, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDisplayMaskWithAuthority(maskPath, plan, authority); err != nil {
		t.Fatal(err)
	}
}

func TestEncodePublicationAuthorityRejectsWrongMaskCountAndValues(t *testing.T) {
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, values := testPlanAndMaskData(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, false)
	wrongCount := append([]byte(nil), values...)
	wrongCount[Rows/2*Columns+Columns/2] = 0
	if _, err := EncodePublicationAuthority(plan, wrongCount); err == nil {
		t.Fatal("encoded a display mask whose land count disagreed with the plan")
	}
	invalid := append([]byte(nil), values...)
	invalid[0] = 2
	if _, err := EncodePublicationAuthority(plan, invalid); err == nil {
		t.Fatal("encoded a display mask with a value outside 0 or 1")
	}
}

func TestPublicationAuthorityCannotReplaceInlinePlanAuthority(t *testing.T) {
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, values := testPlanAndMaskData(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, true)
	path, sha := writeTestPublicationAuthority(t, testPublicationAuthority(plan, values))
	if _, err := LoadPublicationAuthority(path, sha, plan); err == nil {
		t.Fatal("sidecar replaced display authority already bound into the acquisition plan")
	}
}

func TestPublicationAuthorityRejectsSwappedPlanSourceCountDerivationAndChecksum(t *testing.T) {
	cell := era5hourly.Cell{Latitude: 0, Longitude: 0, TimezoneID: "UTC"}
	plan, values := testPlanAndMaskData(t, 11, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, false)
	encoded := testPublicationAuthority(plan, values)
	path, sha := writeTestPublicationAuthority(t, encoded)

	otherPlan, _ := testPlanAndMaskData(t, 12, []era5hourly.Cell{cell}, []era5hourly.Cell{cell}, false)
	if _, err := LoadPublicationAuthority(path, sha, otherPlan); err == nil {
		t.Fatal("publication authority accepted a different acquisition plan")
	}
	if _, err := LoadPublicationAuthority(path, strings.Repeat("0", 64), plan); err == nil {
		t.Fatal("publication authority accepted the wrong trusted sidecar checksum")
	}

	tests := []struct {
		name   string
		mutate func(*encodedPublicationAuthority)
	}{
		{"logical plan", func(value *encodedPublicationAuthority) { value.LogicalPlanSHA256 = strings.Repeat("2", 64) }},
		{"land source", func(value *encodedPublicationAuthority) { value.LandSourceSHA256 = strings.Repeat("2", 64) }},
		{"centre count", func(value *encodedPublicationAuthority) { value.CentreLandCells++ }},
		{"derivation", func(value *encodedPublicationAuthority) { value.DisplayMaskDerivation = "caller asserted" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := encoded
			test.mutate(&candidate)
			candidatePath, candidateSHA := writeTestPublicationAuthority(t, candidate)
			if _, err := LoadPublicationAuthority(candidatePath, candidateSHA, plan); err == nil {
				t.Fatal("forged publication authority was accepted with its own checksum")
			}
		})
	}
}

func TestPublicationAuthorityRejectsSwappedMask(t *testing.T) {
	cells := []era5hourly.Cell{
		{Latitude: 0, Longitude: 0, TimezoneID: "UTC"},
		{Latitude: 0, Longitude: 0.25, TimezoneID: "UTC"},
	}
	plan, values := testPlanAndMaskData(t, 11, cells, cells[:1], false)
	encoded := testPublicationAuthority(plan, values)
	authorityPath, authoritySHA := writeTestPublicationAuthority(t, encoded)
	authority, err := LoadPublicationAuthority(authorityPath, authoritySHA, plan)
	if err != nil {
		t.Fatal(err)
	}
	swapped := append([]byte(nil), values...)
	swapped[Rows/2*Columns+Columns/2] = 0
	swapped[Rows/2*Columns+Columns/2+1] = 1
	if _, err := LoadDisplayMaskWithAuthority(writeTestMask(t, swapped), plan, authority); err == nil {
		t.Fatal("publication authority accepted a different same-count display mask")
	}
}

func testPublicationAuthority(plan era5hourly.CellPlan, values []byte) encodedPublicationAuthority {
	digest := sha256.Sum256(values)
	return encodedPublicationAuthority{
		SchemaVersion:         publicationAuthorityVersion,
		AcquisitionPlanSHA256: plan.SHA256(),
		LogicalPlanSHA256:     plan.LogicalSHA256(),
		Rows:                  Rows, Columns: Columns,
		CellRegistration:      "exact-era5-grid-point-centres",
		LandSourceSHA256:      plan.LandSourceSHA256(),
		DisplayMaskAuthority:  DisplayMaskAuthority,
		DisplayMaskDerivation: era5hourly.CentreLandMaskDerivation,
		DisplayMaskSHA256:     hex.EncodeToString(digest[:]),
		DisplayMaskBytes:      len(values),
		CentreLandCells:       plan.CentreLandCellCount(),
	}
}

func writeTestPublicationAuthority(t *testing.T, authority encodedPublicationAuthority) (string, string) {
	t.Helper()
	body, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publication-authority.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return path, hex.EncodeToString(digest[:])
}

func writeTestMask(t *testing.T, values []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "display-mask.bin")
	if err := os.WriteFile(path, values, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
