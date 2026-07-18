package update

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testStatusError struct{ code int }

func (e testStatusError) Error() string           { return "status" }
func (e testStatusError) HTTPStatusCode() int     { return e.code }
func (e testStatusError) RetryAfterValue() string { return "" }

func TestRetryTransient503ThenSuccess(t *testing.T) {
	attempts := 0
	err := retryWithWait(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return testStatusError{code: 503}
		}
		return nil
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil || attempts != 3 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRetryExhaustsTransientButNotValidationOrOther4xx(t *testing.T) {
	for _, test := range []struct {
		name  string
		input error
		want  int
	}{
		{"exhaustion", testStatusError{code: 503}, retryAttempts},
		{"other 4xx", testStatusError{code: 400}, 1},
		{"validation", errors.New("bad GRIB envelope"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			_ = retryWithWait(context.Background(), func() error { attempts++; return test.input }, func(context.Context, time.Duration) error { return nil })
			if attempts != test.want {
				t.Fatalf("attempts=%d want=%d", attempts, test.want)
			}
		})
	}
}
