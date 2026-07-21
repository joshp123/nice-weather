package climatecanary

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/providers/agera5"
)

type failIfRead struct{}

func (failIfRead) Read([]byte) (int, error) { panic("synthetic canary read a token") }

func TestSyntheticCanaryNeedsNoTokenAndCleansStaging(t *testing.T) {
	parent := t.TempDir()
	summary, err := Run(context.Background(), Config{
		Block: agera5.BlockCoordinate{Latitude: 78, Longitude: 54},
		Year:  2024, StagingParent: parent, TokenReader: failIfRead{}, Synthetic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Source != "synthetic" || summary.FirstDate.Format(time.DateOnly) != "2024-01-01" || summary.LastDate.Format(time.DateOnly) != "2024-12-31" {
		t.Fatalf("synthetic identity = %+v", summary)
	}
	if summary.ExpectedDays != 366 || summary.ProcessedDays != 366 || summary.Width != 1 || summary.Height != 1 || summary.CompleteCells != 1 || summary.NoDataCells != 0 || summary.PeakRetainedDecodedBytes <= 0 || summary.SampledPeakHeapBytes <= 0 {
		t.Fatalf("synthetic evidence = %+v", summary)
	}
	assertEmpty(t, parent)
}

func TestLiveCanaryReadsBoundedStdinAndReturnsOnlySafeMetrics(t *testing.T) {
	parent := t.TempDir()
	token := "private-canary-token"
	executed := false
	summary, err := run(context.Background(), Config{
		Block: agera5.BlockCoordinate{Latitude: 78, Longitude: 54}, Year: 2023,
		StagingParent: parent, TokenReader: strings.NewReader(token + "\n"),
	}, func(_ context.Context, gotToken string, block agera5.BlockCoordinate, window climate.ReferenceWindow, staging string) (execution, error) {
		executed = true
		if gotToken != token || block != (agera5.BlockCoordinate{Latitude: 78, Longitude: 54}) || window != (climate.ReferenceWindow{FirstYear: 2023, LastYear: 2023}) {
			t.Fatalf("executor input token=%q block=%+v window=%+v", gotToken, block, window)
		}
		if _, err := os.Stat(filepath.Join(staging, "scope")); err != nil {
			t.Fatalf("isolated staging scope: %v", err)
		}
		return execution{
			result: climatebuild.Result{Width: 32, Height: 16, Stats: climatebuild.Stats{
				ExpectedDays: 365, ProcessedDays: 365, CompleteCells: 500, NoDataCells: 12,
				TemperatureRepairs: 17, MaximumTemperatureRepair: 1.25, PeakRetainedDecodedBytes: 38_928_384,
				DewPointRepairs: 2, MaximumDewPointRepair: 2.25,
			}},
			metrics: agera5.ClientMetrics{
				Responses: 14, Chunks: 9, FetchedBytes: 22_000_000, CompressedChunkBytes: 21_000_000,
				DecodedChunkBytes: 38_928_384, MaximumCompressedChunkBytes: 3_000_000, ChunkResponseLimitBytes: 4_325_392,
			},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executed || summary.Source != "agera5" || summary.ExpectedDays != 365 || summary.Chunks != 9 || summary.FetchedBytes != 22_000_000 || summary.ChunkResponseLimitBytes != 4_325_392 || summary.TemperatureRepairs != 17 || summary.MaximumTemperatureRepairC != 1.25 || summary.DewPointRepairs != 2 || summary.MaximumDewPointRepairC != 2.25 {
		t.Fatalf("live summary = %+v", summary)
	}
	if strings.Contains(summary.Source, token) {
		t.Fatal("summary retained token")
	}
	assertEmpty(t, parent)
}

func TestCanaryRedactsExecutorErrorsAndCleansFailureStaging(t *testing.T) {
	parent := t.TempDir()
	token := "never-report-this-token"
	_, err := run(context.Background(), Config{
		Block: agera5.BlockCoordinate{Latitude: 78, Longitude: 54}, Year: 2023,
		StagingParent: parent, TokenReader: strings.NewReader(token),
	}, func(_ context.Context, gotToken string, _ agera5.BlockCoordinate, _ climate.ReferenceWindow, staging string) (execution, error) {
		if err := os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		return execution{}, errors.New("provider rejected " + gotToken)
	})
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redacted error = %v", err)
	}
	assertEmpty(t, parent)
}

func TestCanaryRejectsOversizedOrMultilineTokenBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{"oversized", strings.Repeat("x", maximumTokenBytes+1)},
		{"multiline", "first\nsecond\n"},
		{"empty", "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			called := false
			_, err := run(context.Background(), Config{
				Block: agera5.BlockCoordinate{Latitude: 0, Longitude: 0}, Year: 2023,
				StagingParent: parent, TokenReader: strings.NewReader(test.input),
			}, func(context.Context, string, agera5.BlockCoordinate, climate.ReferenceWindow, string) (execution, error) {
				called = true
				return execution{}, nil
			})
			if err == nil || called {
				t.Fatalf("error=%v called=%t", err, called)
			}
			assertEmpty(t, parent)
		})
	}
}

func TestCanaryRejectsInvalidScopeBeforeReadingToken(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Block: agera5.BlockCoordinate{Latitude: 113, Longitude: 0},
		Year:  2024, StagingParent: t.TempDir(), TokenReader: failIfRead{},
	})
	if err == nil {
		t.Fatal("expected invalid block error")
	}
}

func TestLiveCanaryRejectsUnfinishedYearBeforeReadingToken(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Block: agera5.BlockCoordinate{Latitude: 0, Longitude: 0},
		Year:  time.Now().UTC().Year(), StagingParent: t.TempDir(), TokenReader: failIfRead{},
	})
	if err == nil {
		t.Fatal("expected unfinished-year error")
	}
}

func TestCancelledTokenReadReturnsWithoutCreatingStaging(t *testing.T) {
	parent := t.TempDir()
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Config{
			Block: agera5.BlockCoordinate{Latitude: 0, Longitude: 0},
			Year:  2024, StagingParent: parent, TokenReader: reader,
		})
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled token read error = %v", err)
	}
	assertEmpty(t, parent)
}

func assertEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging remains: %v", entries)
	}
}

var _ io.Reader = failIfRead{}
