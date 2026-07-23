package era5hourly

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultHourlyScript      = "scripts/era5_hourly_frames.py"
	maxProducerSummaryBytes  = 32 * 1024
	maxProducerRequests      = 6_000
	maxProducerResponseBytes = 14 * 1024 * 1024 * 1024
	producerKillDelay        = 2 * time.Second

	pinnedRepository = "s3://earthmover-icechunk-era5/icechunkV2"
	pinnedUpstream   = "https://earthmover-icechunk-era5.s3.amazonaws.com/icechunkV2"
)

// DailyShardBuildConfig describes one exact resumable year-month build.
type DailyShardBuildConfig struct {
	PlanPath         string
	PlanSHA256       string
	OutputPath       string
	StagingRoot      string
	Year             int
	Month            time.Month
	HourlyScriptPath string
}

// DailyShardBuildSummary records the bounded work performed by one build.
type DailyShardBuildSummary struct {
	Existing                      bool
	PlanSHA256                    string
	Cells, Timezones              int
	FirstValidTime, LastValidTime time.Time
	Frames, Days                  int
	ShardBytes                    int64
	Requests                      int
	ResponseBytes, FrameBytes     int64
	Elapsed                       time.Duration
}

type producerSpec struct {
	Program     string
	Arguments   []string
	WorkingDir  string
	Environment []string
	First, Last time.Time
}

type runningProducer interface {
	Stdout() io.ReadCloser
	Wait() ([]byte, bool, error)
}

type dailyShardBuildDependencies struct {
	startProducer func(context.Context, producerSpec) (runningProducer, error)
	newDecoder    func(io.Reader) (FrameSource, error)
}

// BuildDailyShard loads the exact cell plan, resumes an existing shard or
// streams pinned ERA5 frames through the one canonical month aggregator.
func BuildDailyShard(ctx context.Context, config DailyShardBuildConfig) (DailyShardBuildSummary, error) {
	return buildDailyShard(ctx, config, dailyShardBuildDependencies{
		startProducer: startUVProducer,
		newDecoder: func(input io.Reader) (FrameSource, error) {
			return NewDecoder(input)
		},
	})
}

func buildDailyShard(ctx context.Context, config DailyShardBuildConfig, dependencies dailyShardBuildDependencies) (summary DailyShardBuildSummary, returnErr error) {
	started := time.Now()
	if ctx == nil || dependencies.startProducer == nil || dependencies.newDecoder == nil {
		return summary, errors.New("ERA5 daily shard builder dependencies are unavailable")
	}
	if err := validateDailyShardBuildConfig(config); err != nil {
		return summary, err
	}
	if config.HourlyScriptPath == "" {
		config.HourlyScriptPath = defaultHourlyScript
	}
	plan, err := LoadCellPlan(config.PlanPath, config.PlanSHA256)
	if err != nil {
		return summary, err
	}
	summary.PlanSHA256 = plan.SHA256
	summary.Cells = len(plan.Cells)
	summary.Timezones = plan.Timezones
	if err := os.MkdirAll(config.StagingRoot, 0o755); err != nil {
		return summary, fmt.Errorf("create ERA5 daily shard staging root: %w", err)
	}
	runDirectory, err := os.MkdirTemp(config.StagingRoot, ".era5-history-month-")
	if err != nil {
		return summary, fmt.Errorf("create ERA5 daily shard run staging: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(runDirectory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("clean ERA5 daily shard run staging: %w", cleanupErr))
		}
		summary.Elapsed = time.Since(started)
	}()
	state, err := PrepareDailyShardInStaging(config.OutputPath, runDirectory, config.Year, config.Month, plan.Cells, plan.SHA256)
	if err != nil {
		return summary, err
	}
	if state.Existing != nil {
		summary.Existing = true
		if err := state.Existing.Close(); err != nil {
			return summary, fmt.Errorf("close existing ERA5 daily shard: %w", err)
		}
		info, err := os.Stat(config.OutputPath)
		if err != nil {
			return summary, fmt.Errorf("stat existing ERA5 daily shard: %w", err)
		}
		summary.ShardBytes = info.Size()
		return summary, nil
	}
	if info, err := os.Stat(config.HourlyScriptPath); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("not a regular file")
		}
		return summary, fmt.Errorf("inspect ERA5 hourly producer script: %w", err)
	}
	first, last, err := RequiredEnvelope(config.Year, config.Month, plan.Cells)
	if err != nil {
		return summary, err
	}
	summary.FirstValidTime, summary.LastValidTime = first, last
	uvCache := filepath.Join(config.StagingRoot, "uv-cache")
	if err := os.MkdirAll(uvCache, 0o755); err != nil {
		return summary, fmt.Errorf("create ERA5 uv cache: %w", err)
	}
	spec := producerSpec{
		Program: "uv",
		Arguments: []string{
			"run", "--quiet", "--script", config.HourlyScriptPath,
			"--first-valid-time", first.Format(time.RFC3339),
			"--last-valid-time", last.Format(time.RFC3339),
		},
		Environment: producerEnvironment(os.Environ(), runDirectory, uvCache),
		First:       first, Last: last,
	}
	producerContext, cancelProducer := context.WithCancel(ctx)
	defer cancelProducer()
	producer, err := dependencies.startProducer(producerContext, spec)
	if err != nil {
		return summary, fmt.Errorf("start ERA5 hourly producer: %w", err)
	}
	stdout := producer.Stdout()
	if stdout == nil {
		cancelProducer()
		_, _, waitErr := producer.Wait()
		return summary, errors.Join(errors.New("ERA5 hourly producer has no output"), waitErr)
	}
	defer stdout.Close()
	source, err := dependencies.newDecoder(stdout)
	if err != nil {
		cancelProducer()
		_ = stdout.Close()
		_, _, waitErr := producer.Wait()
		return summary, errors.Join(fmt.Errorf("open ERA5 hourly decoder: %w", err), waitErr)
	}
	pending := &deferredCommitSink{inner: state.Sink}
	stats, aggregateErr := AggregateMonth(ctx, source, config.Year, config.Month, plan.Cells, pending)
	if aggregateErr != nil {
		cancelProducer()
		_ = stdout.Close()
		stderr, truncated, waitErr := producer.Wait()
		abortErr := pending.Abort()
		return summary, errors.Join(
			fmt.Errorf("aggregate ERA5 daily shard: %w", aggregateErr),
			producerWaitError(stderr, truncated, waitErr),
			abortErr,
		)
	}
	stderr, truncated, waitErr := producer.Wait()
	if err := producerWaitError(stderr, truncated, waitErr); err != nil {
		return summary, errors.Join(err, pending.Abort())
	}
	producerSummary, err := validateProducerSummary(stderr, first, last)
	if err != nil {
		return summary, errors.Join(err, pending.Abort())
	}
	if err := pending.Finalize(); err != nil {
		return summary, errors.Join(fmt.Errorf("publish ERA5 daily shard: %w", err), pending.Abort())
	}
	info, err := os.Stat(config.OutputPath)
	if err != nil {
		return summary, fmt.Errorf("stat ERA5 daily shard: %w", err)
	}
	summary.Frames = stats.Frames
	summary.Days = stats.Days
	summary.ShardBytes = info.Size()
	summary.Requests = producerSummary.Requests
	summary.ResponseBytes = producerSummary.ResponseBytes
	summary.FrameBytes = producerSummary.FrameBytes
	return summary, nil
}

func validateDailyShardBuildConfig(config DailyShardBuildConfig) error {
	if config.Year < climateFirstYear || config.Year > climateLastYear ||
		config.Month < time.January || config.Month > time.December {
		return errors.New("ERA5 daily shard build requires a supported year and month")
	}
	for label, path := range map[string]string{
		"plan": config.PlanPath, "output": config.OutputPath, "staging": config.StagingRoot,
	} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("ERA5 daily shard %s path must be absolute and clean", label)
		}
	}
	if config.PlanPath == config.OutputPath {
		return errors.New("ERA5 daily shard plan and output paths must differ")
	}
	if _, err := parseSHA256(config.PlanSHA256); err != nil {
		return fmt.Errorf("ERA5 daily shard requires a trusted compressed plan SHA-256: %w", err)
	}
	return nil
}

func producerEnvironment(environment []string, temporaryDirectory, uvCache string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && (name == "TMPDIR" || name == "UV_CACHE_DIR") {
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "TMPDIR="+temporaryDirectory, "UV_CACHE_DIR="+uvCache)
	return result
}

type deferredCommitSink struct {
	inner       MonthSink
	transaction MonthTransaction
	requested   bool
	finished    bool
}

func (sink *deferredCommitSink) BeginMonth(ctx context.Context, year int, month time.Month, cells []Cell) (MonthTransaction, error) {
	if sink == nil || sink.inner == nil || sink.transaction != nil {
		return nil, errors.New("ERA5 deferred month sink is unavailable or already started")
	}
	transaction, err := sink.inner.BeginMonth(ctx, year, month, cells)
	if err != nil {
		return nil, err
	}
	sink.transaction = transaction
	return &deferredCommitTransaction{owner: sink}, nil
}

func (sink *deferredCommitSink) Finalize() error {
	if sink == nil || sink.transaction == nil || !sink.requested || sink.finished {
		return errors.New("ERA5 deferred month transaction is not ready to publish")
	}
	if err := sink.transaction.Commit(); err != nil {
		return err
	}
	sink.finished = true
	return nil
}

func (sink *deferredCommitSink) Abort() error {
	if sink == nil || sink.transaction == nil || sink.finished {
		return nil
	}
	sink.finished = true
	return sink.transaction.Abort()
}

type deferredCommitTransaction struct {
	owner *deferredCommitSink
}

func (transaction *deferredCommitTransaction) Add(day DailyMetrics) error {
	if transaction == nil || transaction.owner == nil || transaction.owner.transaction == nil ||
		transaction.owner.requested || transaction.owner.finished {
		return errors.New("ERA5 deferred month transaction is closed")
	}
	return transaction.owner.transaction.Add(day)
}

func (transaction *deferredCommitTransaction) Commit() error {
	if transaction == nil || transaction.owner == nil || transaction.owner.transaction == nil ||
		transaction.owner.requested || transaction.owner.finished {
		return errors.New("ERA5 deferred month transaction is closed")
	}
	transaction.owner.requested = true
	return nil
}

func (transaction *deferredCommitTransaction) Abort() error {
	if transaction == nil || transaction.owner == nil {
		return nil
	}
	return transaction.owner.Abort()
}

type producerSummary struct {
	Frames         int    `json:"frames"`
	FrameBytes     int64  `json:"frameBytes"`
	Requests       int    `json:"requests"`
	ResponseBytes  int64  `json:"responseBytes"`
	Repository     string `json:"repository"`
	Upstream       string `json:"upstream"`
	SnapshotID     string `json:"snapshotId"`
	ContractSHA256 string `json:"contractSha256"`
}

func validateProducerSummary(encoded []byte, first, last time.Time) (producerSummary, error) {
	var summary producerSummary
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return summary, fmt.Errorf("decode ERA5 hourly producer summary: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return summary, fmt.Errorf("finish ERA5 hourly producer summary: %w", err)
	}
	frames := int(last.Sub(first)/time.Hour) + 1
	frameBytes := int64(frames) * int64(HeaderBytes+FieldCount*GridRows*GridColumns*scalarBytes+TrailerBytes)
	if summary.Frames != frames || summary.FrameBytes != frameBytes ||
		summary.Requests <= 0 || summary.Requests > maxProducerRequests ||
		summary.ResponseBytes <= 0 || summary.ResponseBytes > maxProducerResponseBytes ||
		summary.Repository != pinnedRepository || summary.Upstream != pinnedUpstream ||
		summary.SnapshotID != SnapshotID ||
		summary.ContractSHA256 != hex.EncodeToString(sourceContractSHA256[:]) {
		return summary, errors.New("ERA5 hourly producer summary does not match the requested pinned source")
	}
	return summary, nil
}

func producerWaitError(stderr []byte, truncated bool, waitErr error) error {
	if truncated {
		return errors.New("ERA5 hourly producer diagnostics exceeded the bounded limit")
	}
	if waitErr == nil {
		return nil
	}
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		return fmt.Errorf("ERA5 hourly producer failed: %w", waitErr)
	}
	return fmt.Errorf("ERA5 hourly producer failed: %w: %s", waitErr, message)
}

type commandProducer struct {
	command      *exec.Cmd
	stdout       io.ReadCloser
	stderr       *boundedBuffer
	processGroup int
	cancelled    *atomic.Bool
}

func startUVProducer(ctx context.Context, spec producerSpec) (runningProducer, error) {
	if ctx == nil || spec.Program == "" || len(spec.Arguments) == 0 {
		return nil, errors.New("ERA5 hourly producer command is incomplete")
	}
	command := exec.CommandContext(ctx, spec.Program, spec.Arguments...)
	command.Dir = spec.WorkingDir
	command.Env = spec.Environment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cancelled := &atomic.Bool{}
	command.Cancel = func() error {
		cancelled.Store(true)
		err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = producerKillDelay
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &boundedBuffer{maximum: maxProducerSummaryBytes}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &commandProducer{
		command: command, stdout: stdout, stderr: stderr,
		processGroup: command.Process.Pid, cancelled: cancelled,
	}, nil
}

func (producer *commandProducer) Stdout() io.ReadCloser {
	if producer == nil {
		return nil
	}
	return producer.stdout
}

func (producer *commandProducer) Wait() ([]byte, bool, error) {
	if producer == nil || producer.command == nil {
		return nil, false, errors.New("ERA5 hourly producer process is unavailable")
	}
	err := producer.command.Wait()
	if err != nil || producer.cancelled.Load() {
		killErr := syscall.Kill(-producer.processGroup, syscall.SIGKILL)
		if errors.Is(killErr, syscall.ESRCH) {
			killErr = nil
		}
		err = errors.Join(err, killErr)
	}
	return append([]byte(nil), producer.stderr.bytes...), producer.stderr.truncated, err
}

type boundedBuffer struct {
	bytes     []byte
	maximum   int
	truncated bool
}

func (buffer *boundedBuffer) Write(input []byte) (int, error) {
	if buffer == nil || buffer.maximum <= 0 {
		return len(input), nil
	}
	remaining := buffer.maximum - len(buffer.bytes)
	if remaining > 0 {
		if remaining > len(input) {
			remaining = len(input)
		}
		buffer.bytes = append(buffer.bytes, input[:remaining]...)
	}
	if remaining < len(input) {
		buffer.truncated = true
	}
	return len(input), nil
}
