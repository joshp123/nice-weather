package dwd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ValidateCompressed checks a completed temporary download before it is made
// visible at its destination. Validation may inspect bzip2 framing, file size,
// or decode GRIB metadata using a separate adapter.
type ValidateCompressed func(path string) error

type Downloader struct {
	Client HTTPClient
	// AttemptTimeout overrides the size-derived deadline. It is primarily
	// useful for deterministic tests; production callers should leave it zero.
	AttemptTimeout time.Duration
}

type HTTPStatusError struct {
	URL        string
	StatusCode int
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("DWD resource %s returned HTTP %d", e.URL, e.StatusCode)
}
func (e *HTTPStatusError) HTTPStatusCode() int     { return e.StatusCode }
func (e *HTTPStatusError) RetryAfterValue() string { return e.RetryAfter }

// MaxCompressedFileBytes is deliberately well above observed ICON-EU
// single-field products while bounding disk use if an origin or proxy returns
// an unexpected body.
const MaxCompressedFileBytes int64 = 32 << 20

const (
	minimumDownloadTimeout = 30 * time.Second
	minimumDownloadRate    = 128 << 10 // 128 KiB/s, deliberately conservative.
)

// FetchCompressed downloads a .grib2.bz2 file to a caller-owned staging path.
// The destination is replaced atomically only after the response and validation
// both succeed. Decompression and GRIB decoding deliberately live elsewhere.
func (downloader Downloader) FetchCompressed(ctx context.Context, source File, destination string, validate ValidateCompressed) (err error) {
	if downloader.Client == nil {
		return fmt.Errorf("DWD download requires an HTTP client")
	}
	if source.URL == "" {
		return fmt.Errorf("DWD download source URL is required")
	}
	if destination == "" {
		return fmt.Errorf("DWD download destination is required")
	}
	attemptCtx, cancel := context.WithTimeout(ctx, downloader.downloadTimeout(MaxCompressedFileBytes))
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	response, err := downloader.Client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", source.URL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &HTTPStatusError{URL: source.URL, StatusCode: response.StatusCode, RetryAfter: response.Header.Get("Retry-After")}
	}
	if response.ContentLength == 0 {
		return fmt.Errorf("download %s: empty response", source.URL)
	}
	if response.ContentLength > MaxCompressedFileBytes {
		return fmt.Errorf("download %s: declared size %d exceeds %d-byte limit", source.URL, response.ContentLength, MaxCompressedFileBytes)
	}

	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".dwd-download-*.part")
	if err != nil {
		return fmt.Errorf("create temporary DWD download: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			_ = temporary.Close()
		}
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, MaxCompressedFileBytes+1))
	if copyErr != nil {
		err = copyErr
		return fmt.Errorf("write temporary DWD download: %w", err)
	}
	if written == 0 {
		err = fmt.Errorf("empty response body")
		return fmt.Errorf("download %s: %w", source.URL, err)
	}
	if written > MaxCompressedFileBytes {
		err = fmt.Errorf("response exceeds %d-byte limit", MaxCompressedFileBytes)
		return fmt.Errorf("download %s: %w", source.URL, err)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		err = fmt.Errorf("declared %d bytes but received %d", response.ContentLength, written)
		return fmt.Errorf("download %s: %w", source.URL, err)
	}
	if _, err = temporary.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind temporary DWD download: %w", err)
	}
	var magic [4]byte
	if _, err = io.ReadFull(temporary, magic[:]); err != nil {
		return fmt.Errorf("read DWD bzip2 header: %w", err)
	}
	if string(magic[:3]) != "BZh" || magic[3] < '1' || magic[3] > '9' {
		err = fmt.Errorf("invalid bzip2 header %q", magic)
		return fmt.Errorf("validate DWD download: %w", err)
	}
	if err = temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary DWD download: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return fmt.Errorf("close temporary DWD download: %w", err)
	}
	temporary = nil
	if validate != nil {
		if err = validate(temporaryPath); err != nil {
			return fmt.Errorf("validate DWD download: %w", err)
		}
	}
	if err = os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish DWD download: %w", err)
	}
	return nil
}

func (downloader Downloader) downloadTimeout(expectedBytes int64) time.Duration {
	if downloader.AttemptTimeout > 0 {
		return downloader.AttemptTimeout
	}
	if expectedBytes < 0 {
		expectedBytes = 0
	}
	transferSeconds := (expectedBytes + minimumDownloadRate - 1) / minimumDownloadRate
	return minimumDownloadTimeout + time.Duration(transferSeconds)*time.Second
}
