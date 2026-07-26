package era5publication

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/era5hourly"
	weatherupdate "nice-weather-finder/internal/update"
)

func TestBuildUsesExactERA5CentresAndInstallsStableAuthority(t *testing.T) {
	root := t.TempDir()
	planPath, planSHA := writeLegacyPlan(t, root, 6)
	mask := make([]byte, era5climateproduct.Rows*era5climateproduct.Columns)
	setMask(t, mask, 52.5, 5, 1)
	setMask(t, mask, 36.75, -4.5, 1)
	setMask(t, mask, 10, -180, 1)
	mask[1_000], mask[2_000], mask[3_000] = 1, 1, 1
	runner := &maskRunner{mask: mask}
	prepared := 0
	prepare := func(_ context.Context, cacheRoot string, gotRunner pipeline.CommandRunner) (string, error) {
		prepared++
		if cacheRoot != filepath.Join(root, "land-cache") || gotRunner != runner {
			t.Fatalf("prepare inputs = %q, %T", cacheRoot, gotRunner)
		}
		return filepath.Join(root, "ne_10m_land.shp"), nil
	}
	config := Config{
		PlanPath: planPath, PlanSHA256: planSHA,
		LandCacheRoot: filepath.Join(root, "land-cache"),
		OutputRoot:    filepath.Join(root, "authority"),
	}
	first, err := build(context.Background(), config, runner, prepare)
	if err != nil {
		t.Fatal(err)
	}
	if first.Existing || first.CentreLandCells != 6 || prepared != 1 || runner.calls != 1 {
		t.Fatalf("first summary=%+v prepared=%d calls=%d", first, prepared, runner.calls)
	}
	arguments := strings.Join(runner.arguments, " ")
	for _, want := range []string{
		"-te -180.125 -90.125 179.875 90.125",
		"-ts 1440 721",
		"-where featurecla IS NULL OR featurecla = 'Land'",
		"-l ne_10m_land",
	} {
		if !strings.Contains(arguments, want) {
			t.Errorf("gdal_rasterize arguments %q lack %q", arguments, want)
		}
	}
	if strings.Contains(arguments, "-at") {
		t.Fatalf("exact-centre raster unexpectedly used all-touched semantics: %s", arguments)
	}
	installedMask, err := os.ReadFile(first.DisplayMaskPath)
	if err != nil {
		t.Fatal(err)
	}
	west, _ := exactCentreIndex(10, -180)
	east, _ := exactCentreIndex(10, 179.75)
	if installedMask[west] != 1 || installedMask[east] != 0 {
		t.Fatalf("antimeridian columns were wrapped or copied: west=%d east=%d", installedMask[west], installedMask[east])
	}
	firstAuthority, err := os.ReadFile(first.PublicationAuthorityPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := build(context.Background(), config, runner, prepare)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existing || second.DisplayMaskSHA256 != first.DisplayMaskSHA256 ||
		second.PublicationAuthoritySHA256 != first.PublicationAuthoritySHA256 {
		t.Fatalf("stable rerun summary=%+v; first=%+v", second, first)
	}
	secondAuthority, err := os.ReadFile(second.PublicationAuthorityPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstAuthority, secondAuthority) {
		t.Fatal("stable rerun changed canonical authority JSON")
	}
}

func TestBuildRefusesDifferentExistingOutput(t *testing.T) {
	root := t.TempDir()
	planPath, planSHA := writeLegacyPlan(t, root, 6)
	mask := validTestMask(t)
	runner := &maskRunner{mask: mask}
	prepare := func(context.Context, string, pipeline.CommandRunner) (string, error) {
		return filepath.Join(root, "land.shp"), nil
	}
	config := Config{
		PlanPath: planPath, PlanSHA256: planSHA,
		LandCacheRoot: filepath.Join(root, "land-cache"), OutputRoot: filepath.Join(root, "authority"),
	}
	if _, err := build(context.Background(), config, runner, prepare); err != nil {
		t.Fatal(err)
	}
	authorityPath := filepath.Join(config.OutputRoot, PublicationAuthorityFilename)
	if err := os.WriteFile(authorityPath, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := build(context.Background(), config, runner, prepare); err == nil || !strings.Contains(err.Error(), "refuse to overwrite different") {
		t.Fatalf("different output error = %v", err)
	}
	body, err := os.ReadFile(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "different" {
		t.Fatal("different existing output was overwritten")
	}
}

func TestBuildChecksPlanSHAAndLandSourceBeforeRasterizing(t *testing.T) {
	root := t.TempDir()
	planPath, _ := writeLegacyPlan(t, root, 6)
	prepared := false
	prepare := func(context.Context, string, pipeline.CommandRunner) (string, error) {
		prepared = true
		return "", errors.New("must not prepare")
	}
	_, err := build(context.Background(), Config{
		PlanPath: planPath, PlanSHA256: strings.Repeat("0", 64),
		LandCacheRoot: filepath.Join(root, "cache"), OutputRoot: filepath.Join(root, "out"),
	}, &maskRunner{mask: validTestMask(t)}, prepare)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || prepared {
		t.Fatalf("error=%v prepared=%t", err, prepared)
	}
}

func TestExactCentreIndexKeepsBothAntimeridianColumnsDistinct(t *testing.T) {
	west, westOK := exactCentreIndex(0, -180)
	east, eastOK := exactCentreIndex(0, 179.75)
	if !westOK || !eastOK || west == east || west%era5climateproduct.Columns != 0 || east%era5climateproduct.Columns != era5climateproduct.Columns-1 {
		t.Fatalf("west=%d ok=%t east=%d ok=%t", west, westOK, east, eastOK)
	}
	if _, ok := exactCentreIndex(0, 180); ok {
		t.Fatal("accepted duplicate +180 longitude")
	}
}

type maskRunner struct {
	mask      []byte
	arguments []string
	calls     int
}

func (runner *maskRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	runner.calls++
	runner.arguments = append([]string(nil), args...)
	if command != "gdal_rasterize" || len(args) == 0 {
		return nil, errors.New("unexpected command")
	}
	if err := os.WriteFile(args[len(args)-1], runner.mask, 0o600); err != nil {
		return nil, err
	}
	return nil, nil
}

func validTestMask(t *testing.T) []byte {
	t.Helper()
	mask := make([]byte, era5climateproduct.Rows*era5climateproduct.Columns)
	setMask(t, mask, 52.5, 5, 1)
	setMask(t, mask, 36.75, -4.5, 1)
	mask[1_000], mask[2_000], mask[3_000], mask[4_000] = 1, 1, 1, 1
	return mask
}

func setMask(t *testing.T, mask []byte, latitude, longitude float64, value byte) {
	t.Helper()
	index, ok := exactCentreIndex(latitude, longitude)
	if !ok {
		t.Fatalf("test probe %.2f,%.2f is not an exact centre", latitude, longitude)
	}
	mask[index] = value
}

func writeLegacyPlan(t *testing.T, root string, centreLandCells int) (string, string) {
	t.Helper()
	landMask := make([]byte, era5hourly.GridRows*era5hourly.GridColumns)
	for index := 10_000; index < 10_000+centreLandCells; index++ {
		landMask[index] = 1
	}
	timezones := make([]byte, len(landMask)*2)
	landDigest := sha256.Sum256(landMask)
	timezoneDigest := sha256.Sum256(timezones)
	encoded, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "year": 2025, "month": 11,
		"rows": era5hourly.GridRows, "columns": era5hourly.GridColumns,
		"gridDegrees": .25, "firstLatitude": 90, "firstLongitude": -180,
		"cellRegistration":      "exact-era5-grid-point-centres",
		"landAuthority":         "Natural Earth 5.1.1 1:10m polygon intersects 0.25 degree ERA5 source-cell footprint",
		"landFeatureFilter":     "featurecla IS NULL OR featurecla = 'Land'",
		"landFeatureClassAudit": "ERA5 publication authority test fixture",
		"landSourceSha256":      weatherupdate.LandSourceSHA256,
		"timezoneAuthority":     "github.com/ringsaturn/tzf-dist@v0.0.2026-b-fix1",
		"landCells":             centreLandCells, "waterCells": len(landMask) - centreLandCells,
		"centreLandCells": centreLandCells, "coastSupportCells": 0,
		"positiveDatelineSupportRows": 0, "positiveDatelineWrappedCells": 0,
		"negativeDatelineSupportRows": 0, "positiveDatelineWestOnlyRows": 0,
		"positiveDatelineEastOnlyRows": 0, "positiveDatelineBothRows": 0,
		"westEdgeLandCells": 0, "eastEdgeLandCells": 0,
		"coastTimezoneResolvedCells": 0, "nonAntarcticFixedOffsetLandCells": 0,
		"landMaskSha256":      hex.EncodeToString(landDigest[:]),
		"timezoneIndexSha256": hex.EncodeToString(timezoneDigest[:]),
		"landMaskBase64":      base64.StdEncoding.EncodeToString(landMask),
		"timezoneIndexBase64": base64.StdEncoding.EncodeToString(timezones),
		"timezones":           []string{"UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan.json.gz")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(compressed.Bytes())
	return path, hex.EncodeToString(digest[:])
}
