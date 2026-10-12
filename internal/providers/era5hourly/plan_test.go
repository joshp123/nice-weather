package era5hourly

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const acceptedPlanPath = "/private/tmp/nice-weather-era5-world-accepted-2025-11/plan.json.gz"

func TestLoadCellPlanAcceptedWorldwideConformance(t *testing.T) {
	if _, err := os.Stat(acceptedPlanPath); os.IsNotExist(err) {
		t.Skip("accepted ERA5 worldwide plan is not present")
	} else if err != nil {
		t.Fatal(err)
	}
	const acceptedSHA = "cb05d8c98d290923bae13d88e67995ef2687687970f0cd77cb9d0ccd46f8dd58"
	plan, err := LoadCellPlan(acceptedPlanPath, acceptedSHA)
	if err != nil {
		t.Fatal(err)
	}
	cells := plan.Cells()
	if plan.Year() != 2025 || plan.Month() != 11 ||
		len(cells) != 364578 || plan.SHA256() != acceptedSHA {
		t.Fatalf("accepted plan summary = %+v, cells=%d", plan, len(cells))
	}
	positive := cells[len(cells)-52:]
	for index, cell := range positive {
		if cell.SourceIndex%GridColumns != GridColumns/2 || cell.Longitude != 180 {
			t.Fatalf("positive-dateline cell %d = %+v", index, cell)
		}
		mainMatches := 0
		for _, main := range cells[:len(cells)-52] {
			if main.SourceIndex == cell.SourceIndex && main.Latitude == cell.Latitude && main.Longitude == -180 {
				mainMatches++
			}
		}
		if mainMatches != 1 {
			t.Fatalf("positive-dateline cell %d has %d main matches", index, mainMatches)
		}
	}
}

func TestLoadCellPlanValidatesChecksumPlanesCountsAndAuthorities(t *testing.T) {
	valid := minimalEncodedCellPlan(t, false)
	path, sha := writeEncodedCellPlan(t, valid)
	plan, err := LoadCellPlan(path, sha)
	if err != nil {
		t.Fatal(err)
	}
	cells := plan.Cells()
	if len(cells) != 1 || cells[0].SourceIndex != 0 ||
		cells[0].Latitude != 90 || cells[0].Longitude != 0 ||
		cells[0].TimezoneID != "UTC" {
		t.Fatalf("minimal plan cells = %+v", cells)
	}
	cells[0].Longitude = 99
	if plan.Cells()[0].Longitude != 0 {
		t.Fatal("caller mutated an opaque validated cell plan")
	}
	if days, ok := plan.ExpectedDayCount(0); !ok || days != 600 {
		t.Fatalf("November UTC expected days = %d, %t; want 600, true", days, ok)
	}
	if _, err := LoadCellPlan(path, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "compressed checksum") {
		t.Fatalf("wrong compressed checksum error = %v", err)
	}
	if _, err := LoadCellPlan(path, ""); err == nil || !strings.Contains(err.Error(), "expected checksum") {
		t.Fatalf("missing compressed checksum error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*encodedCellPlan)
	}{
		{"geometry", func(plan *encodedCellPlan) { plan.Columns-- }},
		{"authority", func(plan *encodedCellPlan) { plan.TimezoneAuthority = "changed" }},
		{"land count", func(plan *encodedCellPlan) { plan.LandCells++ }},
		{"overflowing declared count", func(plan *encodedCellPlan) { plan.LandCells = int(^uint(0) >> 1) }},
		{"excess dateline support", func(plan *encodedCellPlan) { plan.PositiveDatelineSupportRows = GridRows + 1 }},
		{"coast count", func(plan *encodedCellPlan) { plan.CoastSupportCells++ }},
		{"mask checksum", func(plan *encodedCellPlan) { plan.LandMaskSHA256 = strings.Repeat("0", 64) }},
		{"timezone checksum", func(plan *encodedCellPlan) { plan.TimezoneIndexSHA256 = strings.Repeat("0", 64) }},
		{"mask value", func(plan *encodedCellPlan) {
			mask, _ := base64.StdEncoding.DecodeString(plan.LandMaskBase64)
			mask[0] = 2
			plan.LandMaskBase64 = base64.StdEncoding.EncodeToString(mask)
			sum := sha256.Sum256(mask)
			plan.LandMaskSHA256 = hex.EncodeToString(sum[:])
		}},
		{"water timezone", func(plan *encodedCellPlan) {
			indexes, _ := base64.StdEncoding.DecodeString(plan.TimezoneIndexBase64)
			binary.LittleEndian.PutUint16(indexes[2:4], 1)
			plan.Timezones = append(plan.Timezones, "Etc/UTC")
			plan.TimezoneIndexBase64 = base64.StdEncoding.EncodeToString(indexes)
			sum := sha256.Sum256(indexes)
			plan.TimezoneIndexSHA256 = hex.EncodeToString(sum[:])
		}},
		{"unsupported timezone index", func(plan *encodedCellPlan) {
			indexes, _ := base64.StdEncoding.DecodeString(plan.TimezoneIndexBase64)
			binary.LittleEndian.PutUint16(indexes[0:2], 1)
			plan.TimezoneIndexBase64 = base64.StdEncoding.EncodeToString(indexes)
			sum := sha256.Sum256(indexes)
			plan.TimezoneIndexSHA256 = hex.EncodeToString(sum[:])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			path, sha := writeEncodedCellPlan(t, candidate)
			if _, err := LoadCellPlan(path, sha); err == nil {
				t.Fatal("malformed plan was accepted")
			}
		})
	}
}

func TestLoadCellPlanRejectsUnknownJSONAndGzipTrailingData(t *testing.T) {
	valid := minimalEncodedCellPlan(t, false)
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	path := writePlanJSON(t, unknown, nil)
	if _, err := LoadCellPlan(path, fileSHA256(t, path)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	path = writePlanJSON(t, encoded, []byte("trailing"))
	if _, err := LoadCellPlan(path, fileSHA256(t, path)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing gzip error = %v", err)
	}
}

func TestLoadCellPlanBuildsExactAntimeridianPairWithIndependentTimezones(t *testing.T) {
	encoded := minimalEncodedCellPlan(t, true)
	path, sha := writeEncodedCellPlan(t, encoded)
	plan, err := LoadCellPlan(path, sha)
	if err != nil {
		t.Fatal(err)
	}
	cells := plan.Cells()
	if len(cells) != 2 {
		t.Fatalf("cell count = %d", len(cells))
	}
	left, right := cells[0], cells[1]
	if left.SourceIndex != 360*GridColumns+GridColumns/2 ||
		left.Latitude != 0 || right.Latitude != 0 ||
		left.Longitude != -180 || right.Longitude != 180 ||
		left.TimezoneID != "UTC" || right.TimezoneID != "Etc/GMT-12" {
		t.Fatalf("antimeridian pair = %+v / %+v", left, right)
	}
}

func minimalEncodedCellPlan(t *testing.T, antimeridian bool) encodedCellPlan {
	t.Helper()
	mask := make([]byte, GridRows*GridColumns)
	timezones := make([]byte, GridRows*GridColumns*2)
	row, column := 0, GridColumns/2
	zones := []string{"UTC"}
	if antimeridian {
		row, column = GridRows/2, 0
		zones = append(zones, "Etc/GMT-12")
	}
	cellIndex := row*GridColumns + column
	mask[cellIndex] = 1
	maskSHA := sha256.Sum256(mask)
	timezoneSHA := sha256.Sum256(timezones)
	plan := encodedCellPlan{
		SchemaVersion: planSchemaVersion,
		Year:          2025, Month: 11,
		Rows: GridRows, Columns: GridColumns,
		GridDegrees: .25, FirstLatitude: 90, FirstLongitude: -180,
		CellRegistration:      planCellRegistration,
		LandAuthority:         planLandAuthority,
		LandFeatureFilter:     planLandFilter,
		LandFeatureClassAudit: "test fixture",
		LandSourceSHA256:      strings.Repeat("1", 64),
		TimezoneAuthority:     planTimezoneAuthority,
		LandCells:             1, WaterCells: GridRows*GridColumns - 1,
		CentreLandCells:     1,
		LandMaskSHA256:      hex.EncodeToString(maskSHA[:]),
		TimezoneIndexSHA256: hex.EncodeToString(timezoneSHA[:]),
		LandMaskBase64:      base64.StdEncoding.EncodeToString(mask),
		TimezoneIndexBase64: base64.StdEncoding.EncodeToString(timezones),
		Timezones:           zones,
	}
	if antimeridian {
		plan.PositiveDatelineSupportRows = 1
		plan.PositiveDatelineWrappedCells = 1
		plan.PositiveDatelineEastOnlyRows = 1
		plan.WestEdgeLandCells = 1
		plan.PositiveDatelineTimezoneAudits = []positiveDatelineAudit{{
			Row: row, Latitude: 0, EastSupported: true,
			MainTimezone: "UTC", PositiveTimezone: "Etc/GMT-12",
		}}
	}
	return plan
}

func writeEncodedCellPlan(t *testing.T, plan encodedCellPlan) (string, string) {
	t.Helper()
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := writePlanJSON(t, encoded, nil)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:])
}

func writePlanJSON(t *testing.T, encoded, trailing []byte) string {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	compressed.Write(trailing)
	path := filepath.Join(t.TempDir(), "plan.json.gz")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
