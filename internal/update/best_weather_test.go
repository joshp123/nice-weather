package update

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nice-weather-finder/internal/destinations"
)

func TestFinishCurrentProductRebuildsCompanionWhenWeatherIsUnchanged(t *testing.T) {
	original := buildBestWeatherCurrent
	t.Cleanup(func() { buildBestWeatherCurrent = original })
	calls := 0
	buildBestWeatherCurrent = func(context.Context, string) (destinations.BuildSummary, error) {
		calls++
		return destinations.BuildSummary{ScannedPlaces: 876_613, Bytes: 4_000}, nil
	}
	got, err := finishCurrentProduct(context.Background(), "artifacts", nil)
	if err != nil || calls != 1 || got.ScannedPlaces != 876_613 {
		t.Fatalf("unchanged refresh calls=%d summary=%+v err=%v", calls, got, err)
	}
}

func TestFinishCurrentProductReportsPostPublishCompanionFailure(t *testing.T) {
	original := buildBestWeatherCurrent
	t.Cleanup(func() { buildBestWeatherCurrent = original })
	buildBestWeatherCurrent = func(context.Context, string) (destinations.BuildSummary, error) {
		return destinations.BuildSummary{}, errors.New("place index invalid")
	}
	published := false
	_, err := finishCurrentProduct(context.Background(), "artifacts", func() error { published = true; return nil })
	if !published || err == nil || !strings.Contains(err.Error(), "weather release published, but best-weather artifact failed") {
		t.Fatalf("published=%t err=%v", published, err)
	}

	buildCalled := false
	buildBestWeatherCurrent = func(context.Context, string) (destinations.BuildSummary, error) {
		buildCalled = true
		return destinations.BuildSummary{}, nil
	}
	_, err = finishCurrentProduct(context.Background(), "artifacts", func() error { return errors.New("weather failed") })
	if err == nil || buildCalled {
		t.Fatalf("weather failure err=%v buildCalled=%t", err, buildCalled)
	}
}
