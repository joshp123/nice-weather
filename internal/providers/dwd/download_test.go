package dwd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchCompressedValidatesThenPublishesAtomically(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	validatedPath := ""
	downloader := Downloader{Client: httpClientFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("request method = %s, want GET", request.Method)
		}
		return response(http.StatusOK, "BZh9-new-data"), nil
	})}
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, func(path string) error {
		validatedPath = path
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(content), "BZh9") {
			return errors.New("not bzip2")
		}
		oldContent, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		if string(oldContent) != "old" {
			return errors.New("destination changed before validation")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if validatedPath == "" || validatedPath == destination {
		t.Fatalf("validator saw %q, want temporary path", validatedPath)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "BZh9-new-data"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedLeavesExistingDestinationWhenValidationFails(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "BZh9-broken"), nil
	})}
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, func(string) error {
		return errors.New("invalid bzip2")
	})
	if err == nil || !strings.Contains(err.Error(), "invalid bzip2") {
		t.Fatalf("error = %v, want validation failure", err)
	}
	content, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "old" {
		t.Fatalf("destination = %q, want old content", content)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedRejectsHTTPErrorWithoutCreatingDestination(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusNotFound, "missing"), nil
	})}
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error = %v, want 404", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination unexpectedly exists: %v", statErr)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedRejectsKnownEmptyResponse(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
		result := response(http.StatusOK, "")
		result.ContentLength = 0
		return result, nil
	})}
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil)
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("error = %v, want empty response failure", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination unexpectedly exists: %v", statErr)
	}
}

func TestFetchCompressedRejectsEmptyChunkedResponse(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
		result := response(http.StatusOK, "")
		result.ContentLength = -1
		return result, nil
	})}
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil)
	if err == nil || !strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("error = %v, want empty response body failure", err)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedAcceptsValidChunkedResponse(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
		result := response(http.StatusOK, "BZh9-chunked")
		result.ContentLength = -1
		return result, nil
	})}
	if err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "BZh9-chunked"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

func TestFetchCompressedTimesOutStalledBodyAndRemovesPartialFile(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	downloader := Downloader{
		AttemptTimeout: 20 * time.Millisecond,
		Client: httpClientFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        make(http.Header),
				Body:          &contextBody{ctx: request.Context()},
				ContentLength: -1,
			}, nil
		}),
	}
	started := time.Now()
	err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled body returned after %s", elapsed)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination unexpectedly exists: %v", statErr)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedHonorsCallerCancellationDuringBody(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "field.grib2.bz2")
	ctx, cancel := context.WithCancel(context.Background())
	downloader := Downloader{
		AttemptTimeout: time.Minute,
		Client: httpClientFunc(func(request *http.Request) (*http.Response, error) {
			cancel()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        make(http.Header),
				Body:          &contextBody{ctx: request.Context()},
				ContentLength: -1,
			}, nil
		}),
	}
	err := downloader.FetchCompressed(ctx, File{URL: "https://example.test/field.bz2"}, destination, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
	assertNoPartialDownloads(t, directory)
}

func TestFetchCompressedRejectsInvalidEnvelopeAndPreservesDestination(t *testing.T) {
	tests := []struct {
		name      string
		response  func() *http.Response
		wantError string
	}{
		{
			name: "wrong magic",
			response: func() *http.Response {
				return response(http.StatusOK, "not-bzip2")
			},
			wantError: "invalid bzip2 header",
		},
		{
			name: "missing block size",
			response: func() *http.Response {
				return response(http.StatusOK, "BZh")
			},
			wantError: "read DWD bzip2 header",
		},
		{
			name: "invalid block size",
			response: func() *http.Response {
				return response(http.StatusOK, "BZh0-data")
			},
			wantError: "invalid bzip2 header",
		},
		{
			name: "declared oversize",
			response: func() *http.Response {
				result := response(http.StatusOK, "BZh9")
				result.ContentLength = MaxCompressedFileBytes + 1
				return result
			},
			wantError: "exceeds",
		},
		{
			name: "chunked oversize",
			response: func() *http.Response {
				body := io.MultiReader(strings.NewReader("BZh9"), io.LimitReader(zeroReader{}, MaxCompressedFileBytes-3))
				return &http.Response{
					StatusCode:    http.StatusOK,
					Status:        http.StatusText(http.StatusOK),
					Body:          io.NopCloser(body),
					ContentLength: -1,
					Header:        make(http.Header),
				}
			},
			wantError: "exceeds",
		},
		{
			name: "declared length mismatch",
			response: func() *http.Response {
				result := response(http.StatusOK, "BZh9-short")
				result.ContentLength++
				return result
			},
			wantError: "declared 11 bytes but received 10",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "field.grib2.bz2")
			if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			downloader := Downloader{Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
				return test.response(), nil
			})}
			err := downloader.FetchCompressed(context.Background(), File{URL: "https://example.test/field.bz2"}, destination, nil)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			content, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(content) != "old" {
				t.Fatalf("destination = %q, want old content", content)
			}
			assertNoPartialDownloads(t, directory)
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}

type contextBody struct {
	ctx context.Context
}

func (body *contextBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*contextBody) Close() error { return nil }

func assertNoPartialDownloads(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".dwd-download-*.part"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("partial downloads remain: %v", matches)
	}
}
