package main

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
	"time"

	"nice-weather-finder/internal/era5climateproduct"
	"nice-weather-finder/internal/era5publication"
	"nice-weather-finder/internal/providers/era5hourly"
)

func TestUsageListsOnlyActiveArtifactCommands(t *testing.T) {
	var output bytes.Buffer
	writeUsage(&output)

	for _, command := range []string{"forecast-world", "era5-history-month", "era5-publication-authority", "era5-climate-month", "places", "best-weather", "serve"} {
		if !strings.Contains(output.String(), command) {
			t.Errorf("usage omits active command %q: %s", command, output.String())
		}
	}
	for _, retired := range []string{"fixture", "update", "agera5-canary", "climate-world"} {
		if strings.Contains(output.String(), retired) {
			t.Errorf("usage still advertises retired command %q: %s", retired, output.String())
		}
	}
}

func TestERA5PublicationAuthorityCommandPassesExactInputsAndReportsDigests(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := 0
	err := runERA5PublicationAuthorityCommand(context.Background(), []string{
		"--plan", "/trusted/world-2025-11.json.gz",
		"--plan-sha256", strings.Repeat("a", 64),
		"--land-cache", "/large-disk/land-cache",
		"--out", "/large-disk/authority",
	}, &stdout, &stderr, func(_ context.Context, config era5publication.Config) (era5publication.Summary, error) {
		called++
		if config != (era5publication.Config{
			PlanPath: "/trusted/world-2025-11.json.gz", PlanSHA256: strings.Repeat("a", 64),
			LandCacheRoot: "/large-disk/land-cache", OutputRoot: "/large-disk/authority",
		}) {
			t.Fatalf("config = %+v", config)
		}
		return era5publication.Summary{
			OutputRoot: "/large-disk/authority", CentreLandCells: 364_578,
			DisplayMaskSHA256:          strings.Repeat("b", 64),
			PublicationAuthoritySHA256: strings.Repeat("c", 64),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || stderr.Len() != 0 || strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("called=%d stdout=%q stderr=%q", called, stdout.String(), stderr.String())
	}
	for _, want := range []string{"built", "364578 land centres", strings.Repeat("b", 64), strings.Repeat("c", 64), "/large-disk/authority"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("summary lacks %q: %s", want, stdout.String())
		}
	}
}

func TestERA5PublicationAuthorityCommandRejectsMissingAndPositionalInputs(t *testing.T) {
	valid := []string{
		"--plan", "/trusted/plan.json.gz", "--plan-sha256", strings.Repeat("a", 64),
		"--land-cache", "/large-disk/cache", "--out", "/large-disk/authority",
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing plan", valid[2:], "--plan is required"},
		{"positional", append(append([]string(nil), valid...), "extra"), "accepts no positional arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			err := runERA5PublicationAuthorityCommand(context.Background(), test.args, &stdout, &stderr,
				func(context.Context, era5publication.Config) (era5publication.Summary, error) {
					called = true
					return era5publication.Summary{}, nil
				})
			if err == nil || !strings.Contains(err.Error(), test.want) || called {
				t.Fatalf("called=%t error=%v; want %q", called, err, test.want)
			}
		})
	}
}

func TestERA5ClimateMonthCommandPassesExactInputsAndReportsInstalledRelease(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"--plan", "/trusted/world-2025-11.json.gz",
		"--plan-sha256", strings.Repeat("a", 64),
		"--publication-authority", "/trusted/world-2025-11.authority.json",
		"--publication-authority-sha256", strings.Repeat("b", 64),
		"--display-mask", "/trusted/display-mask.bin",
		"--shards", "/history/daily",
		"--out", "/artifacts/climate",
		"--release-id", "era5-2006-2025-month-11-v1",
	}
	called := 0
	err := runERA5ClimateMonthCommand(context.Background(), args, &stdout, &stderr,
		func(_ context.Context, config era5ClimateMonthConfig) (era5ClimateMonthSummary, error) {
			called++
			if config != (era5ClimateMonthConfig{
				PlanPath: "/trusted/world-2025-11.json.gz", PlanSHA256: strings.Repeat("a", 64),
				PublicationAuthority: "/trusted/world-2025-11.authority.json", PublicationAuthoritySHA256: strings.Repeat("b", 64),
				DisplayMask: "/trusted/display-mask.bin", ShardRoot: "/history/daily",
				ProductRoot: "/artifacts/climate", ReleaseID: "era5-2006-2025-month-11-v1",
			}) {
				t.Fatalf("config = %+v", config)
			}
			return era5ClimateMonthSummary{
				ReleaseID: "era5-2006-2025-month-11-v1", Period: "month-11",
				Shards: 20, ShardBytes: 17_502_469_120, Cells: 364_578,
				CellDays: 226_037_884, OutputFiles: 1_077, OutputBytes: 482_344_960,
				IndexState: era5climateproduct.IndexStatePreview, Months: 1,
				Elapsed: 8*time.Minute + 14*time.Second,
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || stderr.Len() != 0 || strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("called=%d stdout=%q stderr=%q", called, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"month-11", "release era5-2006-2025-month-11-v1", "preview index with 1 month(s)", "20 shards/17502469120 bytes",
		"364578 cells/226037884 complete local days", "1077 files/482344960 bytes", "8m14s",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("summary lacks %q: %s", want, stdout.String())
		}
	}
}

func TestERA5ClimateMonthCommandFailsBeforeRunningAndHelpIsSuccessful(t *testing.T) {
	valid := []string{
		"--plan", "/trusted/plan.json.gz", "--plan-sha256", strings.Repeat("a", 64),
		"--publication-authority", "/trusted/authority.json", "--publication-authority-sha256", strings.Repeat("b", 64),
		"--display-mask", "/trusted/display-mask.bin", "--shards", "/history",
		"--out", "/artifacts/climate", "--release-id", "month-11-v1",
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing plan", args: valid[2:], want: "--plan is required"},
		{name: "positional", args: append(append([]string(nil), valid...), "extra"), want: "accepts no positional arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var stdout, stderr bytes.Buffer
			err := runERA5ClimateMonthCommand(context.Background(), test.args, &stdout, &stderr,
				func(context.Context, era5ClimateMonthConfig) (era5ClimateMonthSummary, error) {
					called = true
					return era5ClimateMonthSummary{}, nil
				})
			if err == nil || !strings.Contains(err.Error(), test.want) || called {
				t.Fatalf("called=%t error=%v; want %q", called, err, test.want)
			}
		})
	}
	called := false
	var stdout, stderr bytes.Buffer
	if err := runERA5ClimateMonthCommand(context.Background(), []string{"-h"}, &stdout, &stderr,
		func(context.Context, era5ClimateMonthConfig) (era5ClimateMonthSummary, error) {
			called = true
			return era5ClimateMonthSummary{}, nil
		}); err != nil || called || !strings.Contains(stderr.String(), "Usage of era5-climate-month:") {
		t.Fatalf("help: called=%t error=%v stderr=%q", called, err, stderr.String())
	}
}

func TestERA5ClimateMonthClosesOpenedShardsWhenALaterYearFails(t *testing.T) {
	root := t.TempDir()
	for year := 2006; year <= 2008; year++ {
		if err := os.WriteFile(filepath.Join(root, time.Date(year, time.November, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")+".ned"), []byte{1}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, second := &era5hourly.DailyShard{}, &era5hourly.DailyShard{}
	opened := 0
	var closed []*era5hourly.DailyShard
	sentinel := errors.New("third shard rejected")
	err := withERA5DailyShards(
		context.Background(), root, time.November, []era5hourly.Cell{{}}, strings.Repeat("a", 64),
		func([]*era5hourly.DailyShard, uint64) error {
			t.Fatal("reducer ran with an incomplete shard set")
			return nil
		},
		func(string, int, time.Month, []era5hourly.Cell, string) (*era5hourly.DailyShard, error) {
			opened++
			switch opened {
			case 1:
				return first, nil
			case 2:
				return second, nil
			default:
				return nil, sentinel
			}
		},
		func(shard *era5hourly.DailyShard) error {
			closed = append(closed, shard)
			return nil
		},
	)
	if !errors.Is(err, sentinel) || opened != 3 || len(closed) != 2 || closed[0] != second || closed[1] != first {
		t.Fatalf("opened=%d closed=%p error=%v", opened, closed, err)
	}
}

func TestERA5ClimateMonthRejectsLegacyPlanWithoutTrustedAuthorityBeforeOpeningShardsOrInstallingOutput(t *testing.T) {
	root := t.TempDir()
	planPath, planSHA := writeLegacyERA5Plan(t, root, time.November)
	shards := filepath.Join(root, "shards")
	out := filepath.Join(root, "product")
	for _, directory := range []string{shards, out} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, err := buildERA5ClimateMonth(context.Background(), era5ClimateMonthConfig{
		PlanPath: planPath, PlanSHA256: planSHA,
		PublicationAuthority:       filepath.Join(root, "missing-authority.json"),
		PublicationAuthoritySHA256: strings.Repeat("a", 64),
		DisplayMask:                filepath.Join(root, "display-mask.bin"),
		ShardRoot:                  shards, ProductRoot: out, ReleaseID: "legacy-must-fail",
	})
	if err == nil || !strings.Contains(err.Error(), "publication authority") {
		t.Fatalf("legacy authority error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "releases")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("legacy plan created output: %v", statErr)
	}
}

func writeLegacyERA5Plan(t *testing.T, root string, month time.Month) (string, string) {
	t.Helper()
	const rows, columns = era5hourly.GridRows, era5hourly.GridColumns
	landMask := make([]byte, rows*columns)
	landMask[columns+1] = 1
	timezoneIndexes := make([]byte, rows*columns*2)
	landDigest := sha256.Sum256(landMask)
	timezoneDigest := sha256.Sum256(timezoneIndexes)
	encoded, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "year": 2025, "month": int(month),
		"rows": rows, "columns": columns, "gridDegrees": .25,
		"firstLatitude": 90, "firstLongitude": -180,
		"cellRegistration":      "exact-era5-grid-point-centres",
		"landAuthority":         "Natural Earth 5.1.1 1:10m polygon intersects 0.25 degree ERA5 source-cell footprint",
		"landFeatureFilter":     "featurecla IS NULL OR featurecla = 'Land'",
		"landFeatureClassAudit": "legacy authority regression fixture",
		"landSourceSha256":      strings.Repeat("1", 64),
		"timezoneAuthority":     "github.com/ringsaturn/tzf-dist@v0.0.2026-b-fix1",
		"landCells":             1, "waterCells": rows*columns - 1,
		"centreLandCells": 0, "coastSupportCells": 1,
		"positiveDatelineSupportRows": 0, "positiveDatelineWrappedCells": 0,
		"negativeDatelineSupportRows": 0, "positiveDatelineWestOnlyRows": 0,
		"positiveDatelineEastOnlyRows": 0, "positiveDatelineBothRows": 0,
		"westEdgeLandCells": 0, "eastEdgeLandCells": 0,
		"coastTimezoneResolvedCells": 0, "nonAntarcticFixedOffsetLandCells": 0,
		"landMaskSha256":      hex.EncodeToString(landDigest[:]),
		"timezoneIndexSha256": hex.EncodeToString(timezoneDigest[:]),
		"landMaskBase64":      base64.StdEncoding.EncodeToString(landMask),
		"timezoneIndexBase64": base64.StdEncoding.EncodeToString(timezoneIndexes),
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
	path := filepath.Join(root, "legacy-plan.json.gz")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(compressed.Bytes())
	return path, hex.EncodeToString(digest[:])
}

func TestERA5HistoryMonthCommandPassesExactBuildIdentityAndPrintsOneSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := 0
	args := []string{
		"--plan", "/accepted/plan.json.gz",
		"--plan-sha256", strings.Repeat("a", 64),
		"--out", "/large-disk/history/2025-11.ned",
		"--staging", "/large-disk/staging",
		"--year", "2025",
		"--month", "11",
	}
	err := runERA5HistoryMonthCommand(context.Background(), args, &stdout, &stderr,
		func(_ context.Context, config era5hourly.DailyShardBuildConfig) (era5hourly.DailyShardBuildSummary, error) {
			called++
			if config.PlanPath != "/accepted/plan.json.gz" ||
				config.PlanSHA256 != strings.Repeat("a", 64) ||
				config.OutputPath != "/large-disk/history/2025-11.ned" ||
				config.StagingRoot != "/large-disk/staging" ||
				config.Year != 2025 || config.Month != time.November {
				t.Fatalf("build config = %+v", config)
			}
			return era5hourly.DailyShardBuildSummary{
				PlanSHA256: strings.Repeat("b", 64),
				Cells:      364578, Timezones: 422,
				Frames: 733, Days: 10_937_340,
				Requests: 5165, ResponseBytes: 7_014_871_020,
				FrameBytes: 21_308_860_912, ShardBytes: 875_123_456,
				Diagnostics: "Warning: source retried one bounded request",
				Elapsed:     11*time.Minute + 47*time.Second,
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 ||
		stderr.String() != "ERA5 hourly source diagnostics:\nWarning: source retried one bounded request\n" ||
		strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("called=%d stdout=%q stderr=%q", called, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"2025-11", "364578 cells", "733 frames", "10937340 local days",
		"7014871020 source bytes in 5165 requests", "21308860912 frame bytes",
		"875123456 shard bytes", strings.Repeat("b", 64), "11m47s",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("summary lacks %q: %s", want, stdout.String())
		}
	}
}

func TestERA5HistoryMonthCommandResumeAndArgumentFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runERA5HistoryMonthCommand(context.Background(), []string{
		"--plan", "/accepted/plan.json.gz",
		"--plan-sha256", strings.Repeat("c", 64),
		"--out", "/large-disk/history/2025-11.ned",
		"--staging", "/large-disk/staging",
		"--year", "2025", "--month", "11",
	}, &stdout, &stderr, func(context.Context, era5hourly.DailyShardBuildConfig) (era5hourly.DailyShardBuildSummary, error) {
		return era5hourly.DailyShardBuildSummary{
			Existing: true, PlanSHA256: strings.Repeat("c", 64),
			Cells: 364578, ShardBytes: 875_123_456, Elapsed: 900 * time.Millisecond,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "already valid") || !strings.Contains(stdout.String(), "900ms") {
		t.Fatalf("resume summary = %q", stdout.String())
	}

	called := false
	if err := runERA5HistoryMonthCommand(context.Background(), []string{"unexpected"}, &stdout, &stderr,
		func(context.Context, era5hourly.DailyShardBuildConfig) (era5hourly.DailyShardBuildSummary, error) {
			called = true
			return era5hourly.DailyShardBuildSummary{}, nil
		}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("positional argument error = %v", err)
	}
	if called {
		t.Fatal("invalid command invoked builder")
	}

	called = false
	if err := runERA5HistoryMonthCommand(context.Background(), []string{
		"--plan", "/accepted/plan.json.gz",
		"--out", "/large-disk/history/2025-11.ned",
		"--staging", "/large-disk/staging",
		"--year", "2025", "--month", "11",
	}, &stdout, &stderr, func(context.Context, era5hourly.DailyShardBuildConfig) (era5hourly.DailyShardBuildSummary, error) {
		called = true
		return era5hourly.DailyShardBuildSummary{}, nil
	}); err == nil || !strings.Contains(err.Error(), "--plan-sha256 is required") {
		t.Fatalf("missing trusted plan checksum error = %v", err)
	}
	if called {
		t.Fatal("missing trusted plan checksum invoked builder")
	}
}

func TestVerifyClimateCommandUsesERA5ReadOnlyGate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := 0
	verify := func(root string) (era5climateproduct.PublicationVerification, error) {
		called++
		if root != "/verified/artifacts" {
			t.Fatalf("verification input = %q", root)
		}
		return era5climateproduct.PublicationVerification{
			Pointer: era5climateproduct.Pointer{IndexID: "era5-2006-2025"},
			Index: era5climateproduct.Index{
				ReferencePeriod: era5climateproduct.ReferencePeriod{FirstYear: 2006, LastYear: 2025},
			},
		}, nil
	}
	if err := runVerifyClimateCommand([]string{"--out", "/verified/artifacts"}, &stdout, &stderr, verify); err != nil {
		t.Fatal(err)
	}
	if called != 1 || stderr.Len() != 0 {
		t.Fatalf("called=%d stderr=%q", called, stderr.String())
	}
	for _, want := range []string{"Climate publication valid: 12 months, 2006-2025, index era5-2006-2025."} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestVerifyClimateCommandRejectsArgumentsAndPropagatesFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runVerifyClimateCommand([]string{"unexpected"}, &stdout, &stderr, func(string) (era5climateproduct.PublicationVerification, error) {
		t.Fatal("verification ran after an invalid positional argument")
		return era5climateproduct.PublicationVerification{}, nil
	}); err == nil || !strings.Contains(err.Error(), "accepts no positional arguments") {
		t.Fatalf("positional argument error = %v", err)
	}
	want := errors.New("publication invalid")
	if err := runVerifyClimateCommand(nil, &stdout, &stderr, func(string) (era5climateproduct.PublicationVerification, error) {
		return era5climateproduct.PublicationVerification{}, want
	}); !errors.Is(err, want) {
		t.Fatalf("verification error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("printed success before validation: %q", stdout.String())
	}
}
