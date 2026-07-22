package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/climatetiles"
	weatherupdate "nice-weather-finder/internal/update"
)

func TestClimateWorldCommandBuildsCanonicalMonthAndPrintsBoundedSummary(t *testing.T) {
	directories := climateWorldTestDirectories(t)
	stdin := strings.NewReader("private-bearer-token\n")
	var stdout, stderr bytes.Buffer
	called := 0
	runner := func(_ context.Context, config weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
		called++
		if config.ArtifactRoot != directories.artifacts || config.CheckpointRoot != directories.checkpoints || config.LandCacheRoot != directories.landCache {
			t.Fatalf("unexpected roots: %+v", config)
		}
		if config.Period != "month-11" || config.Month != time.November {
			t.Fatalf("unexpected month identity: %q %s", config.Period, config.Month)
		}
		if config.TokenReader != stdin {
			t.Fatal("standard input was not passed directly to the backend token reader")
		}
		return weatherupdate.GlobalClimateSummary{
			ReleaseID: "agera5-month-11-test", Pointer: climatetiles.Pointer{IndexID: "preview-index-test"},
			IndexState: climatetiles.IndexStatePreview, MonthsAvailable: 3, SelectedMonthInstalled: true,
			SourceResponses: 73, SourceResponseBytes: 987654321,
			BlocksReused: 111, BlocksBuilt: 222, BlocksSkipped: 333,
			UnsupportedLandCells: 4,
			CheckpointBytes:      444, ArtifactBytes: 555, PointBlocks: 666, ScalarTiles: 777,
			TemperatureRepairs: 8, MaximumInversionC: .301,
			ComfortTemperatureRepairs: 13, MaximumComfortTemperatureAdjustmentC: 5.237,
			DewPointRepairs: 5, MaximumDewPointCapC: 2.104, MainPointerUnchanged: true,
			Elapsed: 90*time.Second + 400*time.Millisecond,
		}, nil
	}

	err := runClimateWorldCommand(context.Background(), []string{
		"--month", "11",
		"--out", directories.artifacts,
		"--checkpoints", directories.checkpoints,
		"--land-cache", directories.landCache,
	}, stdin, &stdout, &stderr, runner)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("runner called %d times", called)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	for _, want := range []string{
		"building November", "2006-2025", "111 reused", "222 built", "333 water-only skipped",
		"73 responses", "987654321 bytes", "agera5-month-11-test installed", "3/12 months available", "preview index pointer preview-index-test", "666 point blocks", "777 scalar tiles",
		"444 checkpoint bytes", "555 artifact bytes", "4 structurally unsupported land cells", "8 crossed-extrema repairs", "largest 0.301 C", "13 daytime-range clamps beyond 5 C tolerance", "largest 5.237 C", "5 dew-point caps", "largest 2.104 C",
		"main weather release unchanged", "elapsed 1m30s",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout.String())
		}
	}
	if lines := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; lines != 4 {
		t.Fatalf("summary grew beyond four bounded lines: %d\n%s", lines, stdout.String())
	}
}

func TestClimateWorldCommandRejectsInvalidInputsBeforeRunning(t *testing.T) {
	directories := climateWorldTestDirectories(t)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "checkpoint-link")
	if err := os.Symlink(directories.checkpoints, symlink); err != nil {
		t.Fatal(err)
	}
	valid := []string{
		"--month", "11", "--out", directories.artifacts,
		"--checkpoints", directories.checkpoints, "--land-cache", directories.landCache,
	}
	tests := []struct {
		name string
		ctx  context.Context
		args []string
		want string
	}{
		{name: "missing month", ctx: context.Background(), args: withoutFlagValue(valid, "--month"), want: "--month must be between 1 and 12"},
		{name: "invalid month", ctx: context.Background(), args: replaceFlagValue(valid, "--month", "13"), want: "--month must be between 1 and 12"},
		{name: "missing artifact root", ctx: context.Background(), args: withoutFlagValue(valid, "--out"), want: "--out is required"},
		{name: "missing checkpoint root", ctx: context.Background(), args: withoutFlagValue(valid, "--checkpoints"), want: "--checkpoints is required"},
		{name: "relative checkpoint root", ctx: context.Background(), args: replaceFlagValue(valid, "--checkpoints", "checkpoints"), want: "must be an absolute directory"},
		{name: "unclean checkpoint root", ctx: context.Background(), args: replaceFlagValue(valid, "--checkpoints", directories.checkpoints+"/.."), want: "must be a clean directory path"},
		{name: "root checkpoint", ctx: context.Background(), args: replaceFlagValue(valid, "--checkpoints", string(filepath.Separator)), want: "other than the filesystem root"},
		{name: "symlink checkpoint", ctx: context.Background(), args: replaceFlagValue(valid, "--checkpoints", symlink), want: "existing real directory"},
		{name: "file land cache", ctx: context.Background(), args: replaceFlagValue(valid, "--land-cache", file), want: "existing real directory"},
		{name: "positional argument", ctx: context.Background(), args: append(append([]string(nil), valid...), "unexpected"), want: "accepts no positional arguments"},
		{name: "nil context", args: valid, want: "requires a context"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			runner := func(context.Context, weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
				called = true
				return weatherupdate.GlobalClimateSummary{}, nil
			}
			var stdout, stderr bytes.Buffer
			err := runClimateWorldCommand(test.ctx, test.args, strings.NewReader("secret"), &stdout, &stderr, runner)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if called {
				t.Fatal("runner was called after invalid input")
			}
		})
	}
}

func TestClimateWorldCommandKeepsBearerTokenOutOfArgumentsAndOutput(t *testing.T) {
	directories := climateWorldTestDirectories(t)
	secret := "private-bearer-token-value"
	args := []string{
		"--month", "1", "--out", directories.artifacts,
		"--checkpoints", directories.checkpoints, "--land-cache", directories.landCache,
	}
	runner := func(_ context.Context, config weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
		payload, err := io.ReadAll(config.TokenReader)
		if err != nil {
			return weatherupdate.GlobalClimateSummary{}, err
		}
		if string(payload) != secret+"\n" {
			return weatherupdate.GlobalClimateSummary{}, errors.New("backend did not receive exact standard input")
		}
		return weatherupdate.GlobalClimateSummary{ReleaseID: "safe-release", Pointer: climatetiles.Pointer{IndexID: "safe-index"}, IndexState: climatetiles.IndexStatePreview, MonthsAvailable: 1, SelectedMonthInstalled: true, MainPointerUnchanged: true}, nil
	}
	var stdout, stderr bytes.Buffer
	err := runClimateWorldCommand(context.Background(), args, strings.NewReader(secret+"\n"), &stdout, &stderr, runner)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatal("bearer token appeared in command output")
	}

	stdout.Reset()
	stderr.Reset()
	err = runClimateWorldCommand(context.Background(), append(args, "--token="+secret), strings.NewReader("unused"), &stdout, &stderr, runner)
	if err == nil {
		t.Fatal("undefined token argument was accepted")
	}
	if strings.Contains(err.Error()+stdout.String()+stderr.String(), secret) {
		t.Fatal("rejected token argument value appeared in diagnostics")
	}
}

func TestClimateWorldCommandHelpIsSuccessfulAndDoesNotRun(t *testing.T) {
	called := false
	var stdout, stderr bytes.Buffer
	err := runClimateWorldCommand(context.Background(), []string{"-h"}, strings.NewReader("unused"), &stdout, &stderr, func(context.Context, weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
		called = true
		return weatherupdate.GlobalClimateSummary{}, nil
	})
	if err != nil {
		t.Fatalf("help returned an error: %v", err)
	}
	if called {
		t.Fatal("runner was called for help")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage of climate-world:") || !strings.Contains(stderr.String(), "-checkpoints") {
		t.Fatalf("unexpected help output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestClimateWorldCommandRejectsUnprovenMainPointer(t *testing.T) {
	directories := climateWorldTestDirectories(t)
	var stdout, stderr bytes.Buffer
	err := runClimateWorldCommand(context.Background(), []string{
		"--month", "3", "--out", directories.artifacts,
		"--checkpoints", directories.checkpoints, "--land-cache", directories.landCache,
	}, strings.NewReader("secret"), &stdout, &stderr, func(context.Context, weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
		return weatherupdate.GlobalClimateSummary{ReleaseID: "release-without-proof"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "main weather release stayed unchanged") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(stdout.String(), "Output:") {
		t.Fatalf("printed success summary without pointer proof: %s", stdout.String())
	}
}

func TestClimateWorldCommandRejectsIncoherentYearIndexSummary(t *testing.T) {
	directories := climateWorldTestDirectories(t)
	var stdout, stderr bytes.Buffer
	err := runClimateWorldCommand(context.Background(), []string{
		"--month", "3", "--out", directories.artifacts,
		"--checkpoints", directories.checkpoints, "--land-cache", directories.landCache,
	}, strings.NewReader("secret"), &stdout, &stderr, func(context.Context, weatherupdate.GlobalClimateConfig) (weatherupdate.GlobalClimateSummary, error) {
		return weatherupdate.GlobalClimateSummary{
			ReleaseID: "release-03", Pointer: climatetiles.Pointer{IndexID: "wrong-preview"},
			IndexState: climatetiles.IndexStatePreview, MonthsAvailable: 12,
			SelectedMonthInstalled: true, MainPointerUnchanged: true,
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid year-index summary") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(stdout.String(), "Output:") {
		t.Fatalf("printed success summary for incoherent year index: %s", stdout.String())
	}
}

type climateWorldDirectories struct {
	artifacts   string
	checkpoints string
	landCache   string
}

func climateWorldTestDirectories(t *testing.T) climateWorldDirectories {
	t.Helper()
	root := t.TempDir()
	directories := climateWorldDirectories{
		artifacts: filepath.Join(root, "artifacts"), checkpoints: filepath.Join(root, "checkpoints"), landCache: filepath.Join(root, "land"),
	}
	for _, path := range []string{directories.artifacts, directories.checkpoints, directories.landCache} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return directories
}

func withoutFlagValue(args []string, name string) []string {
	result := make([]string, 0, len(args)-2)
	for index := 0; index < len(args); index += 2 {
		if args[index] != name {
			result = append(result, args[index], args[index+1])
		}
	}
	return result
}

func replaceFlagValue(args []string, name, value string) []string {
	result := append([]string(nil), args...)
	for index := 0; index < len(result); index += 2 {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}
