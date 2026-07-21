package update

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"nice-weather-finder/internal/climatebuild"
	"nice-weather-finder/internal/providers/agera5"
)

type climateRetryTestReference struct{}

func (climateRetryTestReference) Field() agera5.Field { return agera5.DaytimeMeanTemperature }
func (climateRetryTestReference) FirstDate() time.Time {
	return time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
}
func (climateRetryTestReference) LastDate() time.Time {
	return time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC)
}
func (climateRetryTestReference) Block() agera5.BlockCoordinate {
	return agera5.BlockCoordinate{Latitude: 1, Longitude: 2}
}

type climateRetryTestBlock struct {
	reference climatebuild.ChunkReference
}

func (block *climateRetryTestBlock) Reference() climatebuild.ChunkReference { return block.reference }
func (*climateRetryTestBlock) TimeCount() int                               { return 1 }
func (*climateRetryTestBlock) LatitudeCount() int                           { return 1 }
func (*climateRetryTestBlock) LongitudeCount() int                          { return 1 }
func (*climateRetryTestBlock) Date(int) (time.Time, error) {
	return time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC), nil
}
func (*climateRetryTestBlock) Location(int, int) (float64, float64, error) { return 0, 0, nil }
func (*climateRetryTestBlock) Value(int, int, int) (float32, bool, error)  { return 1, true, nil }

type climateRetryTestSource struct {
	shapeWidth, shapeHeight int
	references              []climatebuild.ChunkReference
	shapeErr, planErr       error
	fetch                   func(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error)
}

func (source *climateRetryTestSource) Shape(agera5.BlockCoordinate) (int, int, error) {
	return source.shapeWidth, source.shapeHeight, source.shapeErr
}

func (source *climateRetryTestSource) Plan(time.Time, time.Time, agera5.BlockCoordinate) ([]climatebuild.ChunkReference, error) {
	return source.references, source.planErr
}

func (source *climateRetryTestSource) Fetch(ctx context.Context, reference climatebuild.ChunkReference) (climatebuild.Block, error) {
	return source.fetch(ctx, reference)
}

type climateRetryTestStatusError struct {
	code       int
	retryAfter string
	cause      error
}

func (err *climateRetryTestStatusError) Error() string           { return "source status" }
func (err *climateRetryTestStatusError) Unwrap() error           { return err.cause }
func (err *climateRetryTestStatusError) HTTPStatusCode() int     { return err.code }
func (err *climateRetryTestStatusError) RetryAfterValue() string { return err.retryAfter }

type climateRetryTestNetworkError struct {
	cause     error
	temporary bool
}

func (err *climateRetryTestNetworkError) Error() string   { return "transient transport" }
func (err *climateRetryTestNetworkError) Unwrap() error   { return err.cause }
func (*climateRetryTestNetworkError) Timeout() bool       { return false }
func (err *climateRetryTestNetworkError) Temporary() bool { return err.temporary }

func TestGlobalClimateRetrySourceLeavesShapeAndPlanUnchanged(t *testing.T) {
	reference := climateRetryTestReference{}
	shapeErr := errors.New("shape marker")
	planErr := errors.New("plan marker")
	underlying := &climateRetryTestSource{
		shapeWidth:  32,
		shapeHeight: 16,
		references:  []climatebuild.ChunkReference{reference},
		shapeErr:    shapeErr,
		planErr:     planErr,
	}
	source := newGlobalClimateRetrySource(underlying)

	width, height, err := source.Shape(reference.Block())
	if width != 32 || height != 16 || !errors.Is(err, shapeErr) {
		t.Fatalf("Shape() = %dx%d, %v", width, height, err)
	}
	references, err := source.Plan(reference.FirstDate(), reference.LastDate(), reference.Block())
	if !reflect.DeepEqual(references, underlying.references) || !errors.Is(err, planErr) {
		t.Fatalf("Plan() = %#v, %v", references, err)
	}
}

func TestGlobalClimateRetrySourceRetriesTransientFetches(t *testing.T) {
	for _, test := range []struct {
		name string
		err  func() error
	}{
		{
			name: "HTTP 429",
			err: func() error {
				return &climateRetryTestStatusError{code: http.StatusTooManyRequests}
			},
		},
		{
			name: "HTTP 500",
			err: func() error {
				return &climateRetryTestStatusError{code: http.StatusInternalServerError}
			},
		},
		{
			name: "HTTP 503",
			err: func() error {
				return &climateRetryTestStatusError{code: http.StatusServiceUnavailable}
			},
		},
		{
			name: "transient transport",
			err: func() error {
				return &climateRetryTestNetworkError{cause: net.ErrClosed, temporary: true}
			},
		},
		{
			name: "non-temporary net.Error follows shared policy",
			err: func() error {
				return &climateRetryTestNetworkError{cause: net.ErrClosed}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			reference := climateRetryTestReference{}
			want := &climateRetryTestBlock{reference: reference}
			underlying := &climateRetryTestSource{fetch: func(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error) {
				attempts++
				if attempts < 3 {
					return nil, test.err()
				}
				return want, nil
			}}
			source := globalClimateRetrySource{
				Source: underlying,
				retry: func(ctx context.Context, operation func() error) error {
					return retryWithWait(ctx, operation, func(context.Context, time.Duration) error { return nil })
				},
			}

			got, err := source.Fetch(context.Background(), reference)
			if err != nil || got != want || attempts != 3 {
				t.Fatalf("Fetch() = %#v, %v after %d attempts", got, err, attempts)
			}
		})
	}
}

func TestGlobalClimateRetrySourceHonoursRetryAfterWithoutSleeping(t *testing.T) {
	attempts := 0
	var waits []time.Duration
	reference := climateRetryTestReference{}
	underlying := &climateRetryTestSource{fetch: func(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error) {
		attempts++
		if attempts == 1 {
			return nil, &climateRetryTestStatusError{code: http.StatusTooManyRequests, retryAfter: "90"}
		}
		return &climateRetryTestBlock{reference: reference}, nil
	}}
	source := globalClimateRetrySource{
		Source: underlying,
		retry: func(ctx context.Context, operation func() error) error {
			return retryWithWait(ctx, operation, func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				return nil
			})
		},
	}

	if _, err := source.Fetch(context.Background(), reference); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !reflect.DeepEqual(waits, []time.Duration{30 * time.Second}) {
		t.Fatalf("attempts=%d waits=%v", attempts, waits)
	}
}

func TestGlobalClimateRetrySourceStopsImmediatelyWhenCancelled(t *testing.T) {
	attempts := 0
	underlying := &climateRetryTestSource{fetch: func(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error) {
		attempts++
		return nil, &climateRetryTestNetworkError{cause: net.ErrClosed}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := make(chan error, 1)
	go func() {
		_, err := newGlobalClimateRetrySource(underlying).Fetch(ctx, climateRetryTestReference{})
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || attempts != 1 {
			t.Fatalf("Fetch() = %v after %d attempts", err, attempts)
		}
	case <-time.After(time.Second):
		t.Fatal("Fetch did not stop on cancellation")
	}
}

func TestGlobalClimateRetrySourceDoesNotRetryPermanentFetchErrors(t *testing.T) {
	permissionMarker := errors.New("permission marker")
	for _, test := range []struct {
		name  string
		input error
		cause error
		code  int
	}{
		{
			name:  "sparse chunk 404",
			input: &climateRetryTestStatusError{code: http.StatusNotFound, cause: agera5.ErrChunkNotFound},
			cause: agera5.ErrChunkNotFound,
			code:  http.StatusNotFound,
		},
		{
			name:  "ordinary permanent 4xx",
			input: &climateRetryTestStatusError{code: http.StatusForbidden, cause: permissionMarker},
			cause: permissionMarker,
			code:  http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			underlying := &climateRetryTestSource{fetch: func(context.Context, climatebuild.ChunkReference) (climatebuild.Block, error) {
				attempts++
				return nil, test.input
			}}

			_, err := newGlobalClimateRetrySource(underlying).Fetch(context.Background(), climateRetryTestReference{})
			if err == nil || attempts != 1 {
				t.Fatalf("Fetch() = %v after %d attempts", err, attempts)
			}
			if test.cause != nil && !errors.Is(err, test.cause) {
				t.Fatalf("Fetch() error %v does not preserve errors.Is(%v)", err, test.cause)
			}
			var status interface {
				HTTPStatusCode() int
				RetryAfterValue() string
			}
			if !errors.As(err, &status) || status.HTTPStatusCode() != test.code {
				t.Fatalf("Fetch() error does not preserve status interface: %v", err)
			}
		})
	}
}
