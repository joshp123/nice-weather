// Package climatecanary runs one bounded, disposable AgERA5 source-block
// build. It does not publish artifacts or retain source data.
package climatecanary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/providers/agera5"
)

const maximumTokenBytes = 4096

// Config identifies one source block and one complete calendar year. A live
// run reads its bearer token from TokenReader once; Synthetic never reads it.
type Config struct {
	Block         agera5.BlockCoordinate
	Year          int
	StagingParent string
	TokenReader   io.Reader
	Synthetic     bool
}

// Summary contains bounded operational evidence and no source keys or
// authentication material.
type Summary struct {
	Source                      string
	Block                       agera5.BlockCoordinate
	FirstDate                   time.Time
	LastDate                    time.Time
	ExpectedDays                int
	ProcessedDays               int
	Width                       int
	Height                      int
	CompleteCells               int
	NoDataCells                 int
	SparseOcean                 bool
	TemperatureRepairs          int
	MaximumTemperatureRepairC   float64
	DewPointRepairs             int
	MaximumDewPointRepairC      float64
	Responses                   int
	Chunks                      int
	FetchedBytes                int64
	CompressedChunkBytes        int64
	DecodedChunkBytes           int64
	MaximumCompressedChunkBytes int64
	ChunkResponseLimitBytes     int64
	PeakRetainedDecodedBytes    int64
	SampledPeakHeapBytes        uint64
	Elapsed                     time.Duration
}

type execution struct {
	result  climatebuild.Result
	metrics agera5.ClientMetrics
}

type executeFunc func(context.Context, string, agera5.BlockCoordinate, climate.ReferenceWindow, string) (execution, error)

// Run executes the configured canary and removes its isolated staging
// directory on success, failure or cancellation.
func Run(ctx context.Context, config Config) (Summary, error) {
	execute := executeLive
	if config.Synthetic {
		execute = executeSynthetic
	}
	return run(ctx, config, execute)
}

func run(ctx context.Context, config Config, execute executeFunc) (summary Summary, resultErr error) {
	started := time.Now()
	if ctx == nil {
		return summary, errors.New("nil AgERA5 canary context")
	}
	if execute == nil {
		return summary, errors.New("nil AgERA5 canary executor")
	}
	latestYear := time.Now().UTC().Year()
	if !config.Synthetic {
		latestYear--
	}
	if config.Year < 1979 || config.Year > latestYear {
		return summary, fmt.Errorf("AgERA5 canary year must be from 1979 through %d", latestYear)
	}
	if _, _, err := agera5.BlockShape(config.Block); err != nil {
		return summary, err
	}
	if config.StagingParent == "" {
		return summary, errors.New("AgERA5 canary staging parent is required")
	}
	token := ""
	var err error
	if !config.Synthetic {
		token, err = readToken(ctx, config.TokenReader)
		if err != nil {
			return summary, err
		}
	}
	stagingParent, err := filepath.Abs(config.StagingParent)
	if err != nil {
		return summary, fmt.Errorf("resolve AgERA5 canary staging parent: %w", err)
	}
	if err := os.MkdirAll(stagingParent, 0o700); err != nil {
		return summary, fmt.Errorf("create AgERA5 canary staging parent: %w", err)
	}
	staging, err := os.MkdirTemp(stagingParent, ".block-")
	if err != nil {
		return summary, fmt.Errorf("create AgERA5 canary staging: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(staging)) }()

	window := climate.ReferenceWindow{FirstYear: config.Year, LastYear: config.Year}
	firstDate := time.Date(config.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastDate := time.Date(config.Year, time.December, 31, 0, 0, 0, 0, time.UTC)
	scope := fmt.Sprintf("block=%d,%d\nfirst=%s\nlast=%s\n", config.Block.Latitude, config.Block.Longitude, firstDate.Format(time.DateOnly), lastDate.Format(time.DateOnly))
	if err := os.WriteFile(filepath.Join(staging, "scope"), []byte(scope), 0o600); err != nil {
		return summary, fmt.Errorf("write AgERA5 canary scope: %w", err)
	}

	stopHeapSampler := startHeapSampler()
	runResult, runErr := execute(ctx, token, config.Block, window, staging)
	peakHeap := stopHeapSampler()
	if runErr != nil {
		return summary, redact(runErr, token)
	}
	stats := runResult.result.Stats
	metrics := runResult.metrics
	source := "agera5"
	if config.Synthetic {
		source = "synthetic"
	}
	summary = Summary{
		Source: source, Block: config.Block, FirstDate: firstDate, LastDate: lastDate,
		ExpectedDays: stats.ExpectedDays, ProcessedDays: stats.ProcessedDays,
		Width: runResult.result.Width, Height: runResult.result.Height,
		CompleteCells: stats.CompleteCells, NoDataCells: stats.NoDataCells, SparseOcean: stats.SparseOcean,
		TemperatureRepairs: stats.TemperatureRepairs, MaximumTemperatureRepairC: stats.MaximumTemperatureRepair,
		DewPointRepairs: stats.DewPointRepairs, MaximumDewPointRepairC: stats.MaximumDewPointRepair,
		Responses: metrics.Responses, Chunks: metrics.Chunks, FetchedBytes: metrics.FetchedBytes,
		CompressedChunkBytes: metrics.CompressedChunkBytes, DecodedChunkBytes: metrics.DecodedChunkBytes,
		MaximumCompressedChunkBytes: metrics.MaximumCompressedChunkBytes, ChunkResponseLimitBytes: metrics.ChunkResponseLimitBytes,
		PeakRetainedDecodedBytes: stats.PeakRetainedDecodedBytes, SampledPeakHeapBytes: peakHeap, Elapsed: time.Since(started),
	}
	return summary, nil
}

func executeLive(ctx context.Context, token string, block agera5.BlockCoordinate, window climate.ReferenceWindow, _ string) (execution, error) {
	client := agera5.NewClient()
	metadata, err := client.FetchMetadata(ctx, token)
	if err != nil {
		return execution{}, err
	}
	result, err := climatebuild.BuildSourceCanaryBlock(ctx, climatebuild.NewAgERA5Source(metadata, client, token), block, window)
	return execution{result: result, metrics: client.Metrics()}, err
}

func readToken(ctx context.Context, reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("AgERA5 bearer token is required on standard input")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type readResult struct {
		payload []byte
		err     error
	}
	read := make(chan readResult, 1)
	go func() {
		payload, err := io.ReadAll(io.LimitReader(reader, maximumTokenBytes+1))
		read <- readResult{payload: payload, err: err}
	}()
	var result readResult
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result = <-read:
	}
	payload, err := result.payload, result.err
	if err != nil {
		return "", errors.New("read AgERA5 bearer token from standard input")
	}
	if len(payload) > maximumTokenBytes {
		return "", fmt.Errorf("AgERA5 bearer token exceeds %d-byte input limit", maximumTokenBytes)
	}
	if len(payload) > 0 && payload[len(payload)-1] == '\n' {
		payload = payload[:len(payload)-1]
		if len(payload) > 0 && payload[len(payload)-1] == '\r' {
			payload = payload[:len(payload)-1]
		}
	}
	token := string(payload)
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("standard input must contain one non-empty AgERA5 bearer token")
	}
	return token, nil
}

func redact(err error, token string) error {
	if err == nil || token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "[redacted]"))
}

func startHeapSampler() func() uint64 {
	var peak atomic.Uint64
	sample := func() {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		for current := peak.Load(); memory.Alloc > current && !peak.CompareAndSwap(current, memory.Alloc); current = peak.Load() {
		}
	}
	sample()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sample()
			case <-stop:
				sample()
				return
			}
		}
	}()
	return func() uint64 {
		close(stop)
		<-done
		return peak.Load()
	}
}
