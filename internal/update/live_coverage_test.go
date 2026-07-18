package update

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/aifs"
	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/weather"

	"github.com/ringsaturn/tzf"
)

// This opt-in test is the bounded real-data regression for the publication
// coverage gate. It downloads only one complete ICON-EU day and one complete
// AIFS day into t.TempDir; the ordinary suite remains offline and deterministic.
func TestLiveRealDayCoverage(t *testing.T) {
	if os.Getenv("NWF_LIVE_COVERAGE") != "1" {
		t.Skip("set NWF_LIVE_COVERAGE=1 for the bounded upstream regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	client := &http.Client{Timeout: 45 * time.Second}
	dwdRun, err := (dwd.Discoverer{Client: client}).LatestCompleteRun(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	aifsClient := aifs.NewClient(client)
	aifsClient.Now = func() time.Time { return now }
	aifsRun, err := aifsClient.DiscoverLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	days, err := planDays(now, dwdRun.StartedAt, aifsRun.Time)
	if err != nil {
		t.Fatal(err)
	}
	var detailed, outlook dayPlan
	for _, day := range days {
		if day.Detailed && detailed.Date.IsZero() {
			detailed = day
		}
		if !day.Detailed && outlook.Date.IsZero() {
			outlook = day
		}
	}
	if detailed.Date.IsZero() || outlook.Date.IsZero() {
		t.Fatal("plan did not contain both a detailed and outlook day")
	}
	root := t.TempDir()
	land := NewLandAuthority(root, client, pipeline.ExecRunner{})
	if _, err := land.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	dwdStage := root + "/dwd-stage"
	if err := os.MkdirAll(dwdStage, 0o755); err != nil {
		t.Fatal(err)
	}
	dwdFrames, err := stageDWDFields(ctx, client, dwdRun, stepsForDWD(dwdRun.StartedAt, []dayPlan{detailed}), dwdStage, dwdStage, dwd.RequiredFields())
	if err != nil {
		t.Fatal(err)
	}
	if err := assertLiveDayCoverage(ctx, t, land, detailed, dwdFrames, 7, "DWD ICON-EU"); err != nil {
		t.Fatal(err)
	}
	aifsStage := root + "/aifs-stage"
	aifsFrames, err := stageAIFS(ctx, aifsClient, aifsRun, stepsForAIFS(aifsRun.Time, []dayPlan{outlook}), aifsStage)
	if err != nil {
		t.Fatal(err)
	}
	if err := assertLiveDayCoverage(ctx, t, land, outlook, aifsFrames, 28, "ECMWF AIFS"); err != nil {
		t.Fatal(err)
	}
}

func assertLiveDayCoverage(ctx context.Context, t *testing.T, land *LandAuthority, day dayPlan, staged []stagedFrame, resolution float64, provider string) error {
	t.Helper()
	series, err := decodeFrames(ctx, pipeline.NewDecoder(nil), stagedForDay(staged, day.Date, day.Detailed))
	if err != nil {
		return err
	}
	spec, err := seriesSpec(series)
	if err != nil {
		return err
	}
	mask, err := land.Mask(ctx, spec)
	if err != nil {
		return err
	}
	expectedSupport, err := expectedForecastSupport(mask, day.Detailed)
	if err != nil {
		return err
	}
	fields, err := pipeline.MeasureFieldCoverage(series, expectedSupport)
	if err != nil {
		return err
	}
	if _, err := enforceFieldCoveragePolicy(day.Date, provider, series, fields); err != nil {
		return err
	}
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return err
	}
	cache := map[string]*time.Location{}
	locations, err := pipeline.CellLocations(spec, func(lon, lat float64) (*time.Location, error) {
		name := finder.GetTimezoneName(lon, lat)
		if name == "" {
			return time.UTC, nil
		}
		if location := cache[name]; location != nil {
			return location, nil
		}
		location, loadErr := time.LoadLocation(name)
		if loadErr == nil {
			cache[name] = location
		}
		return location, loadErr
	})
	if err != nil {
		return err
	}
	aggregatedSpec, metrics, err := pipeline.AggregateDay(day.Date, locations, series, resolution)
	if err != nil {
		return err
	}
	if err := pipeline.ApplyLandMask(aggregatedSpec, metrics, mask); err != nil {
		return err
	}
	coverage, err := pipeline.MeasureForecastCoverage(aggregatedSpec, metrics, expectedSupport)
	if err != nil {
		return err
	}
	if err := requireForecastCoverage(day.Date, provider, coverage); err != nil {
		return err
	}
	berlin, ok := metricAt(aggregatedSpec, metrics, 13.405, 52.52)
	if !ok || !berlin.Valid {
		return fmt.Errorf("%s %s Berlin cell is unavailable", provider, day.Date.Format("2006-01-02"))
	}
	t.Logf("%s %s: Berlin valid; %.2f%% expected-support coverage (%d/%d), worst block %.2f%%; fields %s", provider, day.Date.Format("2006-01-02"), 100*coverage.Fraction(), coverage.Global.Finite, coverage.Global.Support, 100*coverage.BlockFraction(), formatFieldCoverage(fields))
	return nil
}

func metricAt(spec delivery.GridSpec, metrics []weather.DailyMetrics, lon, lat float64) (weather.DailyMetrics, bool) {
	if lon < spec.Bounds.West || lon >= spec.Bounds.East || lat <= spec.Bounds.South || lat > spec.Bounds.North {
		return weather.DailyMetrics{}, false
	}
	col := int((lon - spec.Bounds.West) / (spec.Bounds.East - spec.Bounds.West) * float64(spec.Cols))
	row := int((spec.Bounds.North - lat) / (spec.Bounds.North - spec.Bounds.South) * float64(spec.Rows))
	index := row*int(spec.Cols) + col
	if index < 0 || index >= len(metrics) {
		return weather.DailyMetrics{}, false
	}
	return metrics[index], true
}
