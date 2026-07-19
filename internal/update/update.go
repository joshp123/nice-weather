// Package update owns the bounded batch job that turns current upstream
// weather into one atomic, browser-ready production release.
package update

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/destinations"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/aifs"
	"nice-weather-finder/internal/providers/dwd"
	"nice-weather-finder/internal/weather"

	"github.com/ringsaturn/tzf"
)

type Config struct {
	Out     string
	Cache   string
	Now     func() time.Time
	Smoke   bool
	Refresh bool
}

func Run(ctx context.Context, config Config) error {
	started := time.Now()
	if config.Out == "" || config.Cache == "" {
		return fmt.Errorf("output and cache directories are required")
	}
	lock, err := acquireUpdateLock(config.Cache)
	if err != nil {
		return err
	}
	defer lock.Close()
	cachePath, _ := filepath.Abs(config.Cache)
	outPath, _ := filepath.Abs(config.Out)
	if filepath.Clean(cachePath) != filepath.Clean(outPath) {
		outputLock, lockErr := acquireDirectoryLock(config.Out)
		if lockErr != nil {
			return lockErr
		}
		defer outputLock.Close()
	}
	if err := cleanupAbandonedBuilds(config.Out); err != nil {
		return err
	}
	now := time.Now().UTC()
	if config.Now != nil {
		now = config.Now().UTC()
	}
	stage := filepath.Join(config.Cache, "staging", fmt.Sprintf("update-%d", now.Unix()))
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	httpClient := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConns: 16, MaxIdleConnsPerHost: 8,
		DialContext:         (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}}
	var dwdRun dwd.Run
	err = withRetry(ctx, func() error {
		var discoverErr error
		dwdRun, discoverErr = (dwd.Discoverer{Client: httpClient}).LatestCompleteRun(ctx, now)
		return discoverErr
	})
	if err != nil {
		return err
	}
	aifsClient := aifs.NewClient(httpClient)
	aifsClient.Now = func() time.Time { return now }
	var aifsRun aifs.Run
	err = withRetry(ctx, func() error {
		var discoverErr error
		aifsRun, discoverErr = aifsClient.DiscoverLatest(ctx)
		return discoverErr
	})
	if err != nil {
		return err
	}
	decoder := pipeline.NewDecoder(nil)
	land := NewLandAuthority(config.Cache, httpClient, decoder.Runner)
	if config.Smoke {
		if _, err := land.Prepare(ctx); err != nil {
			return fmt.Errorf("prepare Natural Earth land authority: %w", err)
		}
		return smokeSources(ctx, stage, httpClient, aifsClient, land, dwdRun, aifsRun)
	}
	if !config.Refresh && currentUsesRuns(config.Out, dwdRun.StartedAt, aifsRun.Time) {
		fmt.Printf("current release already uses ICON-EU %s and AIFS %s; refreshing best-weather companion\n", dwdRun.ID(), aifsRun.Time.Format(time.RFC3339))
		_, err := finishCurrentProduct(ctx, config.Out, nil)
		return err
	}
	if _, err := land.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare Natural Earth land authority: %w", err)
	}
	days, err := planDays(now, dwdRun.StartedAt, aifsRun.Time)
	if err != nil {
		return err
	}
	// The catalog freshness is only as strong as the oldest forecast source in
	// the product. Individual periods retain their exact source run below.
	freshness := dwdRun.StartedAt
	if aifsRun.Time.Before(freshness) {
		freshness = aifsRun.Time
	}
	name := fmt.Sprintf("live-%s-%s", dwdRun.ID(), now.Format("20060102T150405Z"))
	writer, err := newReleaseWriter(config.Out, name, now)
	if err != nil {
		return err
	}
	defer writer.discardUncommitted()
	writer.manifest.Forecast.UpdatedAt = freshness.Format(time.RFC3339)

	fmt.Println("building CHELSA 1981–2010 typical months")
	climateCache := filepath.Join(config.Cache, "climate", "chelsa-2.1-1981-2010-0p1")
	if err := buildClimate(ctx, decoder, writer, climateCache, land); err != nil {
		return err
	}

	dwdSteps := stepsForDWD(dwdRun.StartedAt, days)
	aifsSteps := stepsForAIFS(aifsRun.Time, days)
	fmt.Printf("ICON-EU run %s: %d steps; AIFS run %s: %d steps\n", dwdRun.ID(), len(dwdSteps), aifsRun.Time.Format(time.RFC3339), len(aifsSteps))
	dwdStaged, err := stageDWD(ctx, httpClient, dwdRun, dwdSteps, stage)
	if err != nil {
		return err
	}
	aifsStaged, err := stageAIFS(ctx, aifsClient, aifsRun, aifsSteps, stage)
	if err != nil {
		return err
	}
	stageFiles, stageBytes, err := treeStats(stage)
	if err != nil {
		return fmt.Errorf("measure forecast staging: %w", err)
	}
	fmt.Printf("downloaded %d ICON-EU and %d AIFS field messages; staging %d files, %.1f MiB\n", len(dwdStaged), len(aifsStaged), stageFiles, float64(stageBytes)/(1<<20))
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return err
	}
	locationCache := map[string]*time.Location{}
	locations := map[bool][]*time.Location{}
	var rangeTargetSpec delivery.GridSpec
	var cachedOutlookDate time.Time
	var cachedOutlookSeries pipeline.Series
	for _, day := range days {
		if day.Detailed {
			continue
		}
		cachedOutlookDate = day.Date
		cachedOutlookSeries, err = decodeFrames(ctx, decoder, stagedForDay(aifsStaged, day.Date, false))
		if err != nil {
			return fmt.Errorf("decode forecast-window target grid: %w", err)
		}
		rangeTargetSpec, err = seriesSpec(cachedOutlookSeries)
		if err != nil {
			return fmt.Errorf("forecast-window target grid: %w", err)
		}
		break
	}
	if rangeTargetSpec.Rows == 0 {
		return fmt.Errorf("14-day forecast requires at least one AIFS outlook day")
	}
	rangeDays := make([][]weather.DailyMetrics, 0, len(days))
	var detailedRangeSupport pipeline.FloatGrid
	var outlookRangeSupport pipeline.FloatGrid
	for _, day := range days {
		staged, run, resolution, temporal, kind, providerID, providerLabel := stagedForDay(aifsStaged, day.Date, false), aifsRun.Time, 28.0, 6, "outlook", "ecmwf-aifs", "ECMWF AIFS"
		if day.Detailed {
			staged, run, resolution, temporal, kind, providerID, providerLabel = stagedForDay(dwdStaged, day.Date, true), dwdRun.StartedAt, 7, dwdTemporal(day.Date, dwdRun.StartedAt), "detailed", "dwd-icon-eu", "DWD ICON-EU"
		}
		series := cachedOutlookSeries
		if !day.Date.Equal(cachedOutlookDate) {
			series, err = decodeFrames(ctx, decoder, staged)
			if err != nil {
				return err
			}
		} else {
			cachedOutlookSeries = nil
		}
		spec, err := seriesSpec(series)
		if err != nil {
			return err
		}
		if _, ok := locations[day.Detailed]; !ok {
			locations[day.Detailed], err = pipeline.CellLocations(spec, func(lon, lat float64) (*time.Location, error) {
				name := finder.GetTimezoneName(lon, lat)
				if name == "" {
					return time.UTC, nil
				}
				if location, ok := locationCache[name]; ok {
					return location, nil
				}
				location, err := time.LoadLocation(name)
				if err == nil {
					locationCache[name] = location
				}
				return location, err
			})
			if err != nil {
				return err
			}
		}
		forecastMask, err := land.Mask(ctx, spec)
		if err != nil {
			return fmt.Errorf("Natural Earth forecast mask: %w", err)
		}
		expectedSupport, err := expectedForecastSupport(forecastMask, day.Detailed)
		if err != nil {
			return fmt.Errorf("build %s expected provider support: %w", day.Date.Format("2006-01-02"), err)
		}
		if day.Detailed && len(detailedRangeSupport.Values) == 0 {
			detailedRangeSupport = expectedSupport
		}
		if !day.Detailed && len(outlookRangeSupport.Values) == 0 {
			outlookRangeSupport = expectedSupport
		}
		fieldCoverage, err := pipeline.MeasureFieldCoverage(series, expectedSupport)
		if err != nil {
			return fmt.Errorf("measure %s source coverage: %w", day.Date.Format("2006-01-02"), err)
		}
		fmt.Printf("forecast %s normalized land-sample coverage: %s\n", day.Date.Format("2006-01-02"), formatFieldCoverage(fieldCoverage))
		solarOmitted, err := enforceFieldCoveragePolicy(day.Date, providerLabel, series, fieldCoverage)
		if err != nil {
			return err
		}
		if solarOmitted {
			fmt.Printf("warning: forecast %s solar omitted because direct/diffuse worst-frame coverage was below %.2f%%\n", day.Date.Format("2006-01-02"), 100*minimumOptionalSolarCoverage)
		}
		aggregatedSpec, metrics, err := pipeline.AggregateDay(day.Date, locations[day.Detailed], series, resolution)
		if err != nil {
			return fmt.Errorf("aggregate %s: %w", day.Date.Format("2006-01-02"), err)
		}
		if err := pipeline.ApplyLandMask(aggregatedSpec, metrics, forecastMask); err != nil {
			return err
		}
		coverage, err := pipeline.MeasureForecastCoverage(aggregatedSpec, metrics, expectedSupport)
		if err != nil {
			return fmt.Errorf("measure %s forecast coverage: %w", day.Date.Format("2006-01-02"), err)
		}
		if err := requireForecastCoverage(day.Date, providerLabel, coverage); err != nil {
			return err
		}
		fmt.Printf("forecast %s publishable expected-support coverage: %.2f%% (%d/%d), worst spatial block %.2f%% (%d/%d)\n", day.Date.Format("2006-01-02"), 100*coverage.Fraction(), coverage.Global.Finite, coverage.Global.Support, 100*coverage.BlockFraction(), coverage.WorstBlock.Finite, coverage.WorstBlock.Support)
		id := day.Date.Format("2006-01-02")
		source := delivery.Source{
			ProviderID: providerID, ProviderLabel: providerLabel, RunAt: run.Format(time.RFC3339),
			ValidFrom: day.Date.Add(6 * time.Hour).Format(time.RFC3339), ValidTo: day.Date.Add(21 * time.Hour).Format(time.RFC3339),
		}
		period, quantized, err := writer.writePeriod("forecast", id, day.Date.Format("Mon 2 Jan"), aggregatedSpec, metrics, source, resolution, temporal)
		if err != nil {
			return err
		}
		period.WindFrames, err = writer.writeWindFrames(id, series, source)
		if err != nil {
			return fmt.Errorf("publish %s wind frames: %w", id, err)
		}
		rangeMetrics, err := pipeline.SampleMetricsAtRepresentativeCells(aggregatedSpec, quantized, rangeTargetSpec)
		if err != nil {
			return fmt.Errorf("sample %s onto 14-day grid: %w", day.Date.Format("2006-01-02"), err)
		}
		rangeDays = append(rangeDays, rangeMetrics)
		writer.manifest.Forecast.Days = append(writer.manifest.Forecast.Days, delivery.ForecastPeriod{Period: period, Date: id, Kind: kind})
		fmt.Printf("forecast %s ready (%s, %.0f km)\n", id, kind, resolution)
	}
	detailedTargetSupport, err := pipeline.SampleMaskAtRepresentativeCells(detailedRangeSupport, rangeTargetSpec)
	if err != nil {
		return fmt.Errorf("sample detailed forecast support onto 14-day grid: %w", err)
	}
	if outlookRangeSupport.Spec != rangeTargetSpec || len(outlookRangeSupport.Values) != len(detailedTargetSupport.Values) {
		return fmt.Errorf("outlook support does not match 14-day target grid")
	}
	rangeExpectedSupport := detailedTargetSupport
	for index := range rangeExpectedSupport.Values {
		if outlookRangeSupport.Values[index] != 1 {
			rangeExpectedSupport.Values[index] = 0
		}
	}
	rangeAvailability, err := pipeline.ForecastRangeAvailability(rangeTargetSpec, rangeDays)
	if err != nil {
		return err
	}
	rangeCoverage, err := pipeline.MeasureForecastCoverage(rangeTargetSpec, rangeAvailability, rangeExpectedSupport)
	if err != nil {
		return fmt.Errorf("measure 14-day forecast coverage: %w", err)
	}
	if err := requireForecastWindowCoverage(rangeCoverage); err != nil {
		return err
	}
	fmt.Printf("forecast window complete-support coverage: %.2f%% (%d/%d), worst spatial block %.2f%% (%d/%d)\n", 100*rangeCoverage.Fraction(), rangeCoverage.Global.Finite, rangeCoverage.Global.Support, 100*rangeCoverage.BlockFraction(), rangeCoverage.WorstBlock.Finite, rangeCoverage.WorstBlock.Support)
	window, err := writer.writeForecastWindow("next-14-days", "Next 14 days", days[0].Date, days[len(days)-1].Date, rangeTargetSpec, rangeDays, 28)
	if err != nil {
		return err
	}
	writer.manifest.Forecast.Window = window
	fmt.Printf("forecast window ready (%d days, %.0f km)\n", window.DayCount, window.GridResolutionKM)
	if _, err := finishCurrentProduct(ctx, config.Out, func() error { return writer.finish(config.Out) }); err != nil {
		return err
	}
	files, bytes, err := treeStats(writer.dir)
	if err != nil {
		return err
	}
	fmt.Printf("published %s: %d files, %.1f MiB, wall %s\n", name, files, float64(bytes)/(1<<20), time.Since(started).Round(time.Second))
	return nil
}

var buildBestWeatherCurrent = destinations.BuildCurrent

// finishCurrentProduct owns the publication seam between a weather release and
// its static best-weather companion. A nil weather publication means the
// provider runs are unchanged, but the companion is still rebuilt. Companion
// failure never mutates its old pointer; if weather was just published the
// error says exactly which half completed.
func finishCurrentProduct(ctx context.Context, root string, publishWeather func() error) (destinations.BuildSummary, error) {
	published := false
	if publishWeather != nil {
		if err := publishWeather(); err != nil {
			return destinations.BuildSummary{}, err
		}
		published = true
	}
	bestWeather, err := buildBestWeatherCurrent(ctx, root)
	if err != nil {
		if published {
			return destinations.BuildSummary{}, fmt.Errorf("weather release published, but best-weather artifact failed: %w", err)
		}
		return destinations.BuildSummary{}, fmt.Errorf("best-weather artifact refresh failed: %w", err)
	}
	fmt.Printf("best weather ready: %d places -> %d bytes in %s\n", bestWeather.ScannedPlaces, bestWeather.Bytes, bestWeather.Elapsed.Round(time.Millisecond))
	return bestWeather, nil
}

// DWD's regular ICON-EU element packages have this fixed documented domain:
// https://www.dwd.de/EN/ourservices/nwp_forecast_data/nwp_forecast_data.html
// Product bounds intentionally extend slightly beyond it. AIFS is global, so
// its expected support is the complete Europe product grid.
var iconEUElementDomain = delivery.GridBounds{West: -23.5, East: 45, South: 29.5, North: 70.5}

func expectedForecastSupport(land pipeline.FloatGrid, detailed bool) (pipeline.FloatGrid, error) {
	domain := pipeline.EuropeGridBounds()
	if detailed {
		domain = iconEUElementDomain
	}
	return pipeline.SupportWithinBounds(land, domain)
}

func formatFieldCoverage(coverage []pipeline.FieldCoverage) string {
	parts := make([]string, len(coverage))
	for index, field := range coverage {
		parts[index] = fmt.Sprintf("%s worst frame %.2f%% (%d/%d), block %.2f%% (%d/%d)", field.Field, 100*field.Fraction(), field.WorstFrame.Finite, field.WorstFrame.Support, 100*field.BlockFraction(), field.WorstBlock.Finite, field.WorstBlock.Support)
	}
	return strings.Join(parts, ", ")
}

func enforceFieldCoveragePolicy(date time.Time, provider string, series pipeline.Series, coverage []pipeline.FieldCoverage) (bool, error) {
	solarOmitted, err := applyOptionalSolarCoveragePolicy(series, coverage)
	if err != nil {
		return false, fmt.Errorf("refuse forecast %s from %s: optional solar coverage: %w", date.Format("2006-01-02"), provider, err)
	}
	for _, field := range coverage {
		// ICON direct and diffuse radiation were admitted or atomically removed
		// above. Required weather and every other staged field remain exact.
		if field.Field == pipeline.SolarDirect || field.Field == pipeline.SolarDiffuse {
			continue
		}
		if field.WorstFrame.Support == 0 || field.WorstFrame.Finite != field.WorstFrame.Support {
			return false, fmt.Errorf("refuse forecast %s from %s: normalized %s worst-frame coverage %.2f%% (%d/%d); every expected provider-support cell is required", date.Format("2006-01-02"), provider, field.Field, 100*field.Fraction(), field.WorstFrame.Finite, field.WorstFrame.Support)
		}
	}
	return solarOmitted, nil
}

const minimumOptionalSolarCoverage = .999

// applyOptionalSolarCoveragePolicy keeps sparse, honest per-cell solar
// evidence but removes the whole component pair when holes become widespread.
// Direct and diffuse are never admitted independently.
func applyOptionalSolarCoveragePolicy(series pipeline.Series, coverage []pipeline.FieldCoverage) (bool, error) {
	directFrames, directPresent := series[pipeline.SolarDirect]
	diffuseFrames, diffusePresent := series[pipeline.SolarDiffuse]
	directPresent = directPresent && len(directFrames) > 0
	diffusePresent = diffusePresent && len(diffuseFrames) > 0
	if directPresent != diffusePresent {
		return false, fmt.Errorf("direct and diffuse solar series must be present together")
	}
	if !directPresent {
		return false, nil
	}
	byField := make(map[pipeline.Field]pipeline.FieldCoverage, 2)
	for _, measured := range coverage {
		if measured.Field == pipeline.SolarDirect || measured.Field == pipeline.SolarDiffuse {
			byField[measured.Field] = measured
		}
	}
	direct, directMeasured := byField[pipeline.SolarDirect]
	diffuse, diffuseMeasured := byField[pipeline.SolarDiffuse]
	if !directMeasured || !diffuseMeasured || direct.WorstFrame.Support == 0 || diffuse.WorstFrame.Support == 0 {
		return false, fmt.Errorf("direct and diffuse solar coverage must both be measured")
	}
	if direct.Fraction() >= minimumOptionalSolarCoverage && diffuse.Fraction() >= minimumOptionalSolarCoverage {
		return false, nil
	}
	delete(series, pipeline.SolarDirect)
	delete(series, pipeline.SolarDiffuse)
	return true, nil
}

func requireForecastCoverage(date time.Time, provider string, coverage pipeline.ForecastCoverage) error {
	if coverage.Global.Support == 0 || coverage.Global.Finite != coverage.Global.Support {
		return fmt.Errorf("refuse forecast %s from %s: publishable coverage %.2f%% (%d/%d); every expected provider-support cell is required", date.Format("2006-01-02"), provider, 100*coverage.Fraction(), coverage.Global.Finite, coverage.Global.Support)
	}
	return nil
}

const requiredForecastWindowCoverage = 1.0

func requireForecastWindowCoverage(coverage pipeline.ForecastCoverage) error {
	if coverage.Global.Support == 0 || coverage.Fraction() < requiredForecastWindowCoverage {
		return fmt.Errorf("refuse 14-day forecast: complete-window coverage %.2f%% (%d/%d); every target cell supported by all forecast providers is required", 100*coverage.Fraction(), coverage.Global.Finite, coverage.Global.Support)
	}
	return nil
}

func currentUsesRuns(root string, dwdRun, aifsRun time.Time) bool {
	current, ok := delivery.CurrentDir(root)
	if !ok {
		return false
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return false
	}
	manifest, err := delivery.ValidateProductionRelease(resolved)
	if err != nil || manifest.ProductContract != delivery.ProductContract || manifest.DataStatus != delivery.DataStatusLive || len(manifest.Forecast.Days) != productDays {
		return false
	}
	dwdText, aifsText := dwdRun.UTC().Format(time.RFC3339), aifsRun.UTC().Format(time.RFC3339)
	seenDWD, seenAIFS := false, false
	for _, day := range manifest.Forecast.Days {
		switch day.Source.ProviderID {
		case "dwd-icon-eu":
			if day.Source.RunAt != dwdText {
				return false
			}
			seenDWD = true
		case "ecmwf-aifs":
			if day.Source.RunAt != aifsText {
				return false
			}
			seenAIFS = true
		default:
			return false
		}
	}
	return seenDWD && seenAIFS
}

func smokeSources(ctx context.Context, stage string, httpClient *http.Client, aifsClient *aifs.Client, land *LandAuthority, dwdRun dwd.Run, aifsRun aifs.Run) error {
	decoder := pipeline.NewDecoder(nil)
	dwdFrames, err := stageDWD(ctx, httpClient, dwdRun, []int{1, 2}, stage)
	if err != nil {
		return err
	}
	dwdSeries, err := decodeFrames(ctx, decoder, dwdFrames)
	if err != nil {
		return err
	}
	if err := validateDWDSolarSmoke(dwdSeries); err != nil {
		return err
	}
	aifsFrames, err := stageAIFS(ctx, aifsClient, aifsRun, []int{6}, stage)
	if err != nil {
		return err
	}
	aifsSeries, err := decodeFrames(ctx, decoder, aifsFrames)
	if err != nil {
		return err
	}
	var climate pipeline.FloatGrid
	for _, variable := range climateVariables {
		operation, cancel := context.WithTimeout(ctx, 3*time.Minute)
		if err := verifyCHELSAMetadata(operation, variable, 1); err != nil {
			cancel()
			return err
		}
		grid, err := decoder.DecodeRaster(operation, "/vsicurl/"+chelsaURL(variable, 1), .1)
		cancel()
		if err != nil {
			return err
		}
		minimum, maximum := transformedRange(variable, grid.Values)
		if err := validateCHELSARange(variable, minimum, maximum); err != nil {
			return err
		}
		fmt.Printf("CHELSA %s metadata and decoded range verified: %.2f..%.2f\n", variable, minimum, maximum)
		if variable == "tas" {
			climate = grid
		}
	}
	allTouched, err := land.Mask(ctx, climate.Spec)
	if err != nil {
		return err
	}
	centres, err := land.CenterMask(ctx, climate.Spec)
	if err != nil {
		return err
	}
	coreCells, touchedCells := 0, 0
	for index := range allTouched.Values {
		if centres.Values[index] > allTouched.Values[index] {
			return fmt.Errorf("Natural Earth smoke mask topology is invalid at cell %d", index)
		}
		if centres.Values[index] == 1 {
			coreCells++
		}
		if allTouched.Values[index] == 1 {
			touchedCells++
		}
	}
	if coreCells == 0 || touchedCells == len(allTouched.Values) || coreCells > touchedCells {
		return fmt.Errorf("Natural Earth smoke masks have implausible support: centre %d, all-touched %d/%d", coreCells, touchedCells, len(allTouched.Values))
	}
	for name, point := range map[string]struct {
		lon, lat float64
		want     float32
	}{
		"Amsterdam": {lon: 4.9041, lat: 52.3676, want: 1},
		"Malaga":    {lon: -4.4214, lat: 36.7213, want: 1},
		"Reykjavik": {lon: -21.9426, lat: 64.1466, want: 1},
		"Madeira":   {lon: -16.9241, lat: 32.6669, want: 1},
		"Atlantic":  {lon: -15, lat: 45, want: 0},
	} {
		got, err := landMaskValueAt(allTouched, point.lon, point.lat)
		if err != nil || got != point.want {
			return fmt.Errorf("Natural Earth all-touched smoke point %s = %.0f, want %.0f: %w", name, got, point.want, err)
		}
	}
	dwdSpec, _ := seriesSpec(dwdSeries)
	aifsSpec, _ := seriesSpec(aifsSeries)
	forecastMask, err := land.Mask(ctx, dwdSpec)
	if err != nil {
		return fmt.Errorf("Natural Earth forecast smoke mask: %w", err)
	}
	forecastMetrics := make([]weather.DailyMetrics, len(forecastMask.Values))
	for index := range forecastMetrics {
		forecastMetrics[index].Valid = true
	}
	if err := pipeline.ApplyLandMask(dwdSpec, forecastMetrics, forecastMask); err != nil {
		return fmt.Errorf("apply real forecast smoke mask: %w", err)
	}
	forecastLand := 0
	for index, metric := range forecastMetrics {
		wantLand := forecastMask.Values[index] == 1
		if metric.Valid != wantLand {
			return fmt.Errorf("forecast smoke mask cell %d validity is %t, want %t", index, metric.Valid, wantLand)
		}
		if metric.Valid {
			forecastLand++
		}
	}
	fmt.Printf("smoke passed: ICON-EU %s %dx%d with %d/%d forecast land cells, AIFS %s %dx%d, CHELSA %dx%d, Natural Earth climate centre %d, all-touched %d/%d; units and GRIB intervals normalized\n",
		dwdRun.ID(), dwdSpec.Cols, dwdSpec.Rows, forecastLand, len(forecastMetrics), aifsRun.Time.Format(time.RFC3339), aifsSpec.Cols, aifsSpec.Rows, climate.Spec.Cols, climate.Spec.Rows, coreCells, touchedCells, len(allTouched.Values))
	return nil
}

func validateDWDSolarSmoke(series pipeline.Series) error {
	for _, field := range []pipeline.Field{pipeline.SolarDirect, pipeline.SolarDiffuse} {
		frames := series[field]
		if len(frames) == 0 {
			return fmt.Errorf("ICON-EU smoke requires complete direct and diffuse solar series")
		}
		normalized, err := pipeline.NormalizeSeries(frames)
		if err != nil {
			return fmt.Errorf("normalize ICON-EU %s smoke series: %w", field, err)
		}
		if len(normalized) != 2 {
			return fmt.Errorf("ICON-EU %s smoke produced %d intervals; want 2", field, len(normalized))
		}
		for index, frame := range normalized {
			if frame.Semantics != pipeline.IntervalAccumulation || !frame.End.After(frame.Start) {
				return fmt.Errorf("ICON-EU %s smoke interval %d has invalid semantics or duration", field, index)
			}
			if index > 0 && !frame.Start.Equal(normalized[index-1].End) {
				return fmt.Errorf("ICON-EU %s smoke intervals are not contiguous", field)
			}
			finite := 0
			for _, value := range frame.Grid.Values {
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					continue
				}
				finite++
				if value < 0 || value > 5 {
					return fmt.Errorf("ICON-EU %s smoke interval energy %.3f MJ/m2 is implausible", field, value)
				}
			}
			if finite*2 < len(frame.Grid.Values) {
				return fmt.Errorf("ICON-EU %s smoke interval %d has finite energy in only %d/%d cells", field, index, finite, len(frame.Grid.Values))
			}
		}
	}
	return nil
}

func landMaskValueAt(grid pipeline.FloatGrid, lon, lat float64) (float32, error) {
	bounds := grid.Spec.Bounds
	if lon < bounds.West || lon >= bounds.East || lat <= bounds.South || lat > bounds.North {
		return 0, fmt.Errorf("point %.4f,%.4f is outside mask bounds", lon, lat)
	}
	col := int((lon - bounds.West) / (bounds.East - bounds.West) * float64(grid.Spec.Cols))
	row := int((bounds.North - lat) / (bounds.North - bounds.South) * float64(grid.Spec.Rows))
	index := row*int(grid.Spec.Cols) + col
	if row < 0 || row >= int(grid.Spec.Rows) || col < 0 || col >= int(grid.Spec.Cols) || index >= len(grid.Values) {
		return 0, fmt.Errorf("point %.4f,%.4f resolved outside mask grid", lon, lat)
	}
	value := grid.Values[index]
	if value != 0 && value != 1 {
		return 0, fmt.Errorf("mask value is %g, want 0 or 1", value)
	}
	return value, nil
}

func validateCHELSARange(variable string, minimum, maximum float64) error {
	low, high := 0.0, 0.0
	switch variable {
	case "tas", "tasmin", "tasmax":
		low, high = -80, 70
	case "pr":
		low, high = 0, 5000
	case "sfcWind":
		low, high = 0, 100
	case "clt", "hurs":
		low, high = 0, 100
	case "rsds":
		low, high = 0, 60
	default:
		return fmt.Errorf("unknown CHELSA variable %q", variable)
	}
	if math.IsInf(minimum, 0) || math.IsInf(maximum, 0) || minimum < low || maximum > high || minimum > maximum {
		return fmt.Errorf("CHELSA %s decoded range %.3f..%.3f is outside %.1f..%.1f", variable, minimum, maximum, low, high)
	}
	return nil
}

func transformedRange(variable string, values []float32) (float64, float64) {
	minimum, maximum := math.Inf(1), math.Inf(-1)
	for _, stored := range values {
		if math.IsNaN(float64(stored)) || math.IsInf(float64(stored), 0) {
			continue
		}
		value := scaleCHELSA(variable, float64(stored))
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	return minimum, maximum
}

func seriesSpec(series pipeline.Series) (delivery.GridSpec, error) {
	for _, frames := range series {
		if len(frames) > 0 {
			return frames[0].Grid.Spec, nil
		}
	}
	return delivery.GridSpec{}, fmt.Errorf("forecast series is empty")
}

func dwdTemporal(date, run time.Time) int {
	if date.Add(21*time.Hour).Sub(run) > 78*time.Hour {
		return 3
	}
	return 1
}

func treeStats(root string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes, err
}
