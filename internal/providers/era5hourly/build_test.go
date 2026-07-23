package era5hourly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeRunningProducer struct {
	output    io.ReadCloser
	summary   []byte
	truncated bool
	waitErr   error
	waited    bool
}

func (producer *fakeRunningProducer) Stdout() io.ReadCloser { return producer.output }

func (producer *fakeRunningProducer) Wait() ([]byte, bool, error) {
	if producer.waited {
		return nil, false, errors.New("fake producer waited twice")
	}
	producer.waited = true
	return producer.summary, producer.truncated, producer.waitErr
}

func TestBuildDailyShardStreamsExactEnvelopePublishesAndResumesWithoutSource(t *testing.T) {
	config, plan := dailyShardBuildFixture(t)
	var started int
	var captured producerSpec
	dependencies := dailyShardBuildDependencies{
		startProducer: func(_ context.Context, spec producerSpec) (runningProducer, error) {
			started++
			captured = spec
			return successfulFakeProducer(t, spec.First, spec.Last, false), nil
		},
		newDecoder: func(input io.Reader) (FrameSource, error) {
			return newDecoder(input, 1, 1)
		},
	}
	summary, err := buildDailyShard(context.Background(), config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Existing || summary.Cells != 1 || summary.Timezones != 1 ||
		summary.Frames == 0 || summary.Days != 30 || summary.ShardBytes <= 0 ||
		summary.Requests != 1 || summary.ResponseBytes != 1234 || started != 1 {
		t.Fatalf("build summary = %+v; starts=%d", summary, started)
	}
	first, last, err := RequiredEnvelope(config.Year, config.Month, plan.Cells)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"run", "--quiet", "--script", config.HourlyScriptPath,
		"--first-valid-time", first.Format(time.RFC3339),
		"--last-valid-time", last.Format(time.RFC3339),
	}
	if captured.Program != "uv" || !slices.Equal(captured.Arguments, wantArgs) ||
		!captured.First.Equal(first) || !captured.Last.Equal(last) {
		t.Fatalf("producer spec = %+v", captured)
	}
	if !environmentHasPath(captured.Environment, "UV_CACHE_DIR", filepath.Join(config.StagingRoot, "uv-cache")) {
		t.Fatalf("producer environment lacks bounded uv cache: %v", captured.Environment)
	}
	if environmentCount(captured.Environment, "TMPDIR") != 1 || environmentCount(captured.Environment, "UV_CACHE_DIR") != 1 {
		t.Fatalf("producer environment has ambiguous overrides: %v", captured.Environment)
	}
	tmpDirectory := environmentValue(captured.Environment, "TMPDIR")
	if filepath.Dir(tmpDirectory) != config.StagingRoot || !strings.HasPrefix(filepath.Base(tmpDirectory), ".era5-history-month-") {
		t.Fatalf("producer TMPDIR = %q", tmpDirectory)
	}
	assertNoRunStaging(t, config.StagingRoot)
	shard, err := OpenDailyShardForPlan(config.OutputPath, config.Year, config.Month, plan.Cells, config.PlanSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := shard.Close(); err != nil {
		t.Fatal(err)
	}

	resume := config
	resume.HourlyScriptPath = filepath.Join(t.TempDir(), "missing.py")
	summary, err = buildDailyShard(context.Background(), resume, dailyShardBuildDependencies{
		startProducer: func(context.Context, producerSpec) (runningProducer, error) {
			t.Fatal("existing shard constructed a producer")
			return nil, nil
		},
		newDecoder: func(io.Reader) (FrameSource, error) {
			t.Fatal("existing shard constructed a decoder")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Existing || summary.ShardBytes <= 0 || started != 1 {
		t.Fatalf("resume summary = %+v; starts=%d", summary, started)
	}
	assertNoRunStaging(t, config.StagingRoot)
}

func TestBuildDailyShardBindsExistingShardToTrustedPlanArtifact(t *testing.T) {
	config, _ := dailyShardBuildFixture(t)
	dependencies := dailyShardBuildDependencies{
		startProducer: func(_ context.Context, spec producerSpec) (runningProducer, error) {
			return successfulFakeProducer(t, spec.First, spec.Last, false), nil
		},
		newDecoder: func(input io.Reader) (FrameSource, error) {
			return newDecoder(input, 1, 1)
		},
	}
	if _, err := buildDailyShard(context.Background(), config, dependencies); err != nil {
		t.Fatal(err)
	}

	otherPlan := minimalEncodedCellPlan(t, false)
	otherPlan.LandFeatureClassAudit = "same cells, different accepted artifact"
	otherPath, otherSHA := writeEncodedCellPlan(t, otherPlan)
	other := config
	other.PlanPath = otherPath
	other.PlanSHA256 = otherSHA
	other.HourlyScriptPath = filepath.Join(t.TempDir(), "missing.py")
	if summary, err := buildDailyShard(context.Background(), other, dailyShardBuildDependencies{
		startProducer: func(context.Context, producerSpec) (runningProducer, error) {
			t.Fatal("plan-mismatched existing shard started a producer")
			return nil, nil
		},
		newDecoder: func(io.Reader) (FrameSource, error) {
			t.Fatal("plan-mismatched existing shard constructed a decoder")
			return nil, nil
		},
	}); err == nil || summary.Existing {
		t.Fatalf("plan-mismatched existing shard result = %+v, %v", summary, err)
	}
}

func TestBuildDailyShardProducerAndAggregationFailuresNeverPublish(t *testing.T) {
	tests := []struct {
		name     string
		producer func(*testing.T, time.Time, time.Time) *fakeRunningProducer
	}{
		{
			name: "producer exit",
			producer: func(t *testing.T, first, last time.Time) *fakeRunningProducer {
				producer := successfulFakeProducer(t, first, last, false)
				producer.waitErr = errors.New("injected producer failure")
				producer.summary = []byte("producer failed")
				return producer
			},
		},
		{
			name: "producer summary",
			producer: func(t *testing.T, first, last time.Time) *fakeRunningProducer {
				producer := successfulFakeProducer(t, first, last, false)
				producer.summary = []byte(`{"frames":1}`)
				return producer
			},
		},
		{
			name: "aggregation",
			producer: func(t *testing.T, first, last time.Time) *fakeRunningProducer {
				return successfulFakeProducer(t, first, last, true)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, _ := dailyShardBuildFixture(t)
			var producer *fakeRunningProducer
			_, err := buildDailyShard(context.Background(), config, dailyShardBuildDependencies{
				startProducer: func(_ context.Context, spec producerSpec) (runningProducer, error) {
					producer = test.producer(t, spec.First, spec.Last)
					return producer, nil
				},
				newDecoder: func(input io.Reader) (FrameSource, error) {
					return newDecoder(input, 1, 1)
				},
			})
			if err == nil {
				t.Fatal("failed build was accepted")
			}
			if _, statErr := os.Stat(config.OutputPath); !os.IsNotExist(statErr) {
				t.Fatalf("failed build published output: %v", statErr)
			}
			if producer == nil || !producer.waited {
				t.Fatal("failed build did not reap its producer")
			}
			assertNoRunStaging(t, config.StagingRoot)
		})
	}
}

func TestBuildDailyShardRejectsSourceStartAndDecoderFailureWithoutOutput(t *testing.T) {
	tests := []struct {
		name string
		deps dailyShardBuildDependencies
	}{
		{
			name: "source start",
			deps: dailyShardBuildDependencies{
				startProducer: func(context.Context, producerSpec) (runningProducer, error) {
					return nil, errors.New("injected start failure")
				},
				newDecoder: func(io.Reader) (FrameSource, error) { return nil, nil },
			},
		},
		{
			name: "decoder",
			deps: dailyShardBuildDependencies{
				startProducer: func(_ context.Context, _ producerSpec) (runningProducer, error) {
					return &fakeRunningProducer{
						output:  io.NopCloser(bytes.NewReader(nil)),
						waitErr: errors.New("cancelled"),
					}, nil
				},
				newDecoder: func(io.Reader) (FrameSource, error) {
					return nil, errors.New("injected decoder failure")
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, _ := dailyShardBuildFixture(t)
			if _, err := buildDailyShard(context.Background(), config, test.deps); err == nil {
				t.Fatal("failed source setup was accepted")
			}
			if _, err := os.Stat(config.OutputPath); !os.IsNotExist(err) {
				t.Fatalf("failed source setup published output: %v", err)
			}
			assertNoRunStaging(t, config.StagingRoot)
		})
	}
}

func TestBuildDailyShardRequiresAbsoluteCleanPaths(t *testing.T) {
	config, _ := dailyShardBuildFixture(t)
	tests := []struct {
		name   string
		mutate func(*DailyShardBuildConfig)
	}{
		{"relative plan", func(config *DailyShardBuildConfig) { config.PlanPath = "plan.json.gz" }},
		{"unclean output", func(config *DailyShardBuildConfig) { config.OutputPath += "/../month.ned" }},
		{"relative staging", func(config *DailyShardBuildConfig) { config.StagingRoot = "staging" }},
		{"same plan and output", func(config *DailyShardBuildConfig) { config.OutputPath = config.PlanPath }},
		{"missing trusted plan checksum", func(config *DailyShardBuildConfig) { config.PlanSHA256 = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := config
			test.mutate(&candidate)
			if _, err := buildDailyShard(context.Background(), candidate, dailyShardBuildDependencies{
				startProducer: func(context.Context, producerSpec) (runningProducer, error) {
					t.Fatal("invalid config started a producer")
					return nil, nil
				},
				newDecoder: func(io.Reader) (FrameSource, error) { return nil, nil },
			}); err == nil {
				t.Fatal("invalid build config was accepted")
			}
		})
	}
}

func TestStartUVProducerCancellationKillsGrandchildProcessGroup(t *testing.T) {
	root := t.TempDir()
	childPath := filepath.Join(root, "child.sh")
	parentPath := filepath.Join(root, "parent.sh")
	pidPath := filepath.Join(root, "child.pid")
	if err := os.WriteFile(childPath, []byte("#!/bin/sh\ntrap '' TERM\necho $$ > \"$1\"\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentPath, []byte("#!/bin/sh\n\"$1\" \"$2\" &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	producer, err := startUVProducer(ctx, producerSpec{
		Program: parentPath, Arguments: []string{childPath, pidPath},
		Environment: os.Environ(),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		encoded, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(encoded)))
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			break
		}
		if !os.IsNotExist(readErr) {
			cancel()
			t.Fatal(readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		cancel()
		t.Fatal("grandchild did not start")
	}
	cancel()
	started := time.Now()
	_, _, waitErr := producer.Wait()
	if waitErr == nil {
		t.Fatal("cancelled producer exited successfully")
	}
	if elapsed := time.Since(started); elapsed > producerKillDelay+time.Second {
		t.Fatalf("producer cancellation took %s", elapsed)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived process-group cancellation: %v", childPID, err)
}

func dailyShardBuildFixture(t *testing.T) (DailyShardBuildConfig, CellPlan) {
	t.Helper()
	root := t.TempDir()
	planPath, planSHA := writeEncodedCellPlan(t, minimalEncodedCellPlan(t, false))
	script := filepath.Join(root, "era5_hourly_frames.py")
	if err := os.WriteFile(script, []byte("# test fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := DailyShardBuildConfig{
		PlanPath: planPath, PlanSHA256: planSHA,
		OutputPath:  filepath.Join(root, "out", "2025-11.ned"),
		StagingRoot: filepath.Join(root, "staging"),
		Year:        2025, Month: time.November,
		HourlyScriptPath: script,
	}
	plan, err := LoadCellPlan(planPath, planSHA)
	if err != nil {
		t.Fatal(err)
	}
	return config, plan
}

func successfulFakeProducer(t *testing.T, first, last time.Time, corrupt bool) *fakeRunningProducer {
	t.Helper()
	var output bytes.Buffer
	frameIndex := 0
	for current := first; !current.After(last); current = current.Add(time.Hour) {
		values := [FieldCount][]float32{}
		scalars := [FieldCount]float32{295, 285, 0, 1_000_000, .25, 3, 4}
		if corrupt && frameIndex == 20 {
			scalars[4] = 2
		}
		for field := range values {
			values[field] = []float32{scalars[field]}
		}
		output.Write(encodedFrame(t, current, 1, 1, values))
		frameIndex++
	}
	frameBytes := int64(frameIndex) * int64(HeaderBytes+FieldCount*GridRows*GridColumns*scalarBytes+TrailerBytes)
	summary, err := json.Marshal(producerSummary{
		Frames: frameIndex, FrameBytes: frameBytes,
		Requests: 1, ResponseBytes: 1234,
		Repository: pinnedRepository, Upstream: pinnedUpstream,
		SnapshotID:     SnapshotID,
		ContractSHA256: sourceContractSHAHex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRunningProducer{output: io.NopCloser(bytes.NewReader(output.Bytes())), summary: summary}
}

func sourceContractSHAHex() string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(sourceContractSHA256)*2)
	for index, value := range sourceContractSHA256 {
		result[index*2] = alphabet[value>>4]
		result[index*2+1] = alphabet[value&15]
	}
	return string(result)
}

func environmentHasPath(environment []string, name, value string) bool {
	return environmentValue(environment, name) == value
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func environmentCount(environment []string, name string) int {
	count := 0
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	return count
}

func assertNoRunStaging(t *testing.T, stagingRoot string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(stagingRoot, ".era5-history-month-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("owned run staging remains: %v", matches)
	}
}
