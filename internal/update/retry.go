package update

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const retryAttempts = 5

type statusError interface {
	error
	HTTPStatusCode() int
	RetryAfterValue() string
}

func withRetry(ctx context.Context, operation func() error) error {
	return retryWithWait(ctx, operation, waitContext)
}

func retryWithWait(ctx context.Context, operation func() error, wait func(context.Context, time.Duration) error) error {
	var last error
	for attempt := 0; attempt < retryAttempts; attempt++ {
		last = operation()
		if last == nil || !retryable(last) || attempt == retryAttempts-1 {
			return last
		}
		delay := time.Second << attempt
		if delay > 15*time.Second {
			delay = 15 * time.Second
		}
		var status statusError
		if errors.As(last, &status) {
			if parsed, ok := parseRetryAfter(status.RetryAfterValue(), time.Now()); ok && parsed > delay {
				delay = parsed
			}
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
	return last
}

func retryable(err error) bool {
	var status statusError
	if errors.As(err, &status) {
		switch status.HTTPStatusCode() {
		case 429, 500, 502, 503, 504:
			return true
		default:
			return false
		}
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if date, err := httpTime(value); err == nil && date.After(now) {
		return date.Sub(now), true
	}
	return 0, false
}

var httpTime = func(value string) (time.Time, error) { return time.Parse(time.RFC1123, value) }

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
