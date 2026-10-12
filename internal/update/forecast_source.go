// Forecast source preparation keeps provider decoding and geographic masking
// behind one boundary used by the production publisher.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/pipeline"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/tiledelivery"

	"github.com/ringsaturn/tzf"
)

const (
	ifsSourceID                        = "ecmwf-ifs-control"
	iconEUSourceID                     = "dwd-icon-eu"
	webMercatorHalfWorldMetres         = 20037508.342789244
	webMercatorMaximumLatitude         = 85.0511287798066
	webMercatorLandCoordinateTolerance = 1e-7
)

var worldwideForecastBounds = delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}

func worldwideForecastDaysAndSteps(now time.Time, run ifs.Run) ([]dayPlan, []int, error) {
	available, err := ifs.ForecastSteps(run)
	if err != nil {
		return nil, nil, err
	}
	days, err := planDaysForCoverageCount(now, forecastCoverage{}, forecastCoverage{
		provider: "IFS", run: run.Time, maxForecastHours: available[len(available)-1],
	}, worldwideForecastPolicy, productDays)
	if err != nil {
		return nil, nil, err
	}
	steps := stepsForDays(run.Time, days, globalOutlookDecodeWindow, available)
	if len(steps) == 0 {
		return nil, nil, errors.New("worldwide IFS has no source steps")
	}
	return days, steps, nil
}

func validateStagedIFS(run time.Time, steps []int, staged []stagedFrame) error {
	want := make(map[pipeline.Field]map[int]bool)
	for _, parameter := range ifs.RequiredSurfaceFields() {
		field, err := ifsPipelineField(parameter)
		if err != nil {
			return err
		}
		want[field] = make(map[int]bool, len(steps))
		for _, step := range steps {
			want[field][step] = true
		}
	}
	seen := make(map[pipeline.Field]map[int]bool, len(want))
	for _, frame := range staged {
		if !frame.run.Equal(run.UTC()) {
			return fmt.Errorf("staged IFS frame uses run %s; want %s", frame.run.Format(time.RFC3339), run.UTC().Format(time.RFC3339))
		}
		if frame.resolution != 28 || frame.spacing != .25 {
			return errors.New("staged worldwide IFS frame changed source resolution")
		}
		stepsForField, ok := want[frame.field]
		if !ok || !stepsForField[frame.step] {
			return fmt.Errorf("unexpected staged IFS %s step %d", frame.field, frame.step)
		}
		if seen[frame.field] == nil {
			seen[frame.field] = make(map[int]bool)
		}
		if seen[frame.field][frame.step] {
			return fmt.Errorf("duplicate staged IFS %s step %d", frame.field, frame.step)
		}
		seen[frame.field][frame.step] = true
	}
	for field, fieldSteps := range want {
		for step := range fieldSteps {
			if !seen[field][step] {
				return fmt.Errorf("missing staged IFS %s step %d", field, step)
			}
		}
	}
	return nil
}

func prepareStagedDay(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame, scratch, sourceID, progressLabel string) (pipeline.PreparedSeries, func() error, error) {
	series, frames, err := prepareStagedFrames(ctx, decoder, staged, scratch, sourceID, progressLabel)
	if err != nil {
		return nil, nil, err
	}
	return series, func() error {
		var closeErr error
		for _, frame := range frames {
			closeErr = errors.Join(closeErr, frame.Close())
		}
		return closeErr
	}, nil
}

func stagedPrepareRequest(frame stagedFrame, scratch, sourceID string) pipeline.DecodeRequest {
	return pipeline.DecodeRequest{
		Path: frame.path, SourceID: sourceID, Field: frame.field, Run: frame.run.UTC(),
		NativeResolutionKM: frame.resolution, CellDegrees: frame.spacing, ScratchDirectory: scratch,
	}
}

func prepareStagedFrames(ctx context.Context, decoder *pipeline.Decoder, staged []stagedFrame, scratch, sourceID, progressLabel string) (pipeline.PreparedSeries, []*pipeline.PreparedFrame, error) {
	if sourceID == "" || progressLabel == "" {
		return nil, nil, errors.New("prepared forecast source identity is required")
	}
	frames := make([]*pipeline.PreparedFrame, len(staged))
	var completed atomic.Int64
	err := runWorkers(ctx, len(staged), func(workerCtx context.Context, index int) error {
		item := staged[index]
		prepared, err := decoder.PrepareFrame(workerCtx, stagedPrepareRequest(item, scratch, sourceID))
		if err != nil {
			return fmt.Errorf("prepare %s step %d: %w", item.field, item.step, err)
		}
		frames[index] = prepared
		count := completed.Add(1)
		if count%25 == 0 || count == int64(len(staged)) {
			fmt.Printf("prepared %d/%d %s fields\n", count, len(staged), progressLabel)
		}
		return nil
	})
	if err != nil {
		closePreparedFrames(frames)
		return nil, nil, err
	}

	// Workers write fixed indexes. Assemble only after every conversion
	// succeeds so each field retains the provider's deterministic input order.
	series := make(pipeline.PreparedSeries)
	for index, item := range staged {
		series[item.field] = append(series[item.field], frames[index])
	}
	return series, frames, nil
}

func closePreparedFrames(frames []*pipeline.PreparedFrame) {
	for _, frame := range frames {
		_ = frame.Close()
	}
}

func forecastWebMercatorDisplayLandMask(ctx context.Context, authority *LandAuthority, scratch string, zoom uint8) (forecasttiles.LandMask, error) {
	if authority == nil || authority.runner == nil {
		return forecasttiles.LandMask{}, errors.New("land authority is not configured")
	}
	shapePath, err := authority.Prepare(ctx)
	if err != nil {
		return forecasttiles.LandMask{}, err
	}
	if zoom > 5 {
		return forecasttiles.LandMask{}, errors.New("display land mask zoom exceeds the supported forecast zoom")
	}
	width := int(tiledelivery.TileSize) << zoom
	temporary, err := os.MkdirTemp(scratch, ".forecast-land-*")
	if err != nil {
		return forecasttiles.LandMask{}, err
	}
	defer os.RemoveAll(temporary)
	projected := filepath.Join(temporary, "land.gpkg")
	projectionArguments := []string{
		"-f", "GPKG", "-t_srs", "EPSG:3857", "-nlt", "MULTIPOLYGON", "-nln", "land",
		projected, shapePath,
	}
	if _, err := authority.runner.Run(ctx, "ogr2ogr", projectionArguments...); err != nil {
		return forecasttiles.LandMask{}, fmt.Errorf("project Natural Earth land into Web Mercator: %w", err)
	}
	output := filepath.Join(temporary, "land.bil")
	arguments := []string{
		"-q", "-of", "EHdr", "-ot", "Byte", "-burn", "1", "-init", "0",
		"-a_srs", "EPSG:3857",
		"-te", landCoordinate(-webMercatorHalfWorldMetres), landCoordinate(-webMercatorHalfWorldMetres), landCoordinate(webMercatorHalfWorldMetres), landCoordinate(webMercatorHalfWorldMetres),
		"-ts", fmt.Sprint(width), fmt.Sprint(width),
		"-l", "land", projected, output,
	}
	if _, err := authority.runner.Run(ctx, "gdal_rasterize", arguments...); err != nil {
		return forecasttiles.LandMask{}, fmt.Errorf("rasterize Web Mercator Natural Earth land: %w", err)
	}
	pixels, err := os.ReadFile(output)
	if err != nil {
		return forecasttiles.LandMask{}, fmt.Errorf("read Web Mercator Natural Earth land: %w", err)
	}
	predicate, err := webMercatorDisplayLandPredicate(pixels, zoom)
	if err != nil {
		return forecasttiles.LandMask{}, err
	}
	digest := sha256.Sum256(pixels)
	return forecasttiles.NewLandMask(predicate, forecasttiles.LandIdentity{
		Authority: forecasttiles.LandAuthorityNaturalEarth, Dataset: forecasttiles.LandDatasetNaturalEarth10M,
		Version: forecasttiles.LandVersionNaturalEarth511, SourceArchiveSHA256: strings.ToLower(authority.source.SHA256),
		MaskSHA256: hex.EncodeToString(digest[:]), DerivationContract: forecasttiles.LandDerivationWebMercatorCentreV1,
	})
}

func webMercatorDisplayLandPredicate(pixels []byte, zoom uint8) (forecasttiles.DisplayLand, error) {
	if zoom > 5 {
		return nil, errors.New("display land mask zoom exceeds the supported forecast zoom")
	}
	width := int(tiledelivery.TileSize) << zoom
	if len(pixels) != width*width {
		return nil, fmt.Errorf("display land mask has %d bytes; want %d", len(pixels), width*width)
	}
	for index, value := range pixels {
		if value != 0 && value != 1 {
			return nil, fmt.Errorf("display land mask cell %d has value %d; want 0 or 1", index, value)
		}
	}
	return func(bounds delivery.GridBounds) (bool, error) {
		coordinates := []float64{bounds.West, bounds.East, bounds.South, bounds.North}
		for _, coordinate := range coordinates {
			if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
				return false, errors.New("land lookup bounds must be finite")
			}
		}
		mercatorNorth := webMercatorMaximumLatitude
		if bounds.West < -180 || bounds.East > 180 || bounds.West >= bounds.East || bounds.South >= bounds.North || bounds.North <= -mercatorNorth || bounds.South >= mercatorNorth {
			return false, errors.New("land lookup bounds are outside Web Mercator")
		}
		bounds.North = min(bounds.North, mercatorNorth)
		bounds.South = max(bounds.South, -mercatorNorth)
		west := (bounds.West + 180) / 360 * float64(width)
		east := (bounds.East + 180) / 360 * float64(width)
		north := webMercatorPixelEdge(bounds.North, width)
		south := webMercatorPixelEdge(bounds.South, width)
		// Use only raster texels whose centres are inside the requested bounds.
		// This keeps a wider provider-cell lookup from borrowing land that lies
		// in the outside part of a merely overlapping coastal display texel.
		firstColumn := max(0, int(math.Ceil(west-.5-webMercatorLandCoordinateTolerance)))
		lastColumn := min(width-1, int(math.Ceil(east-.5-webMercatorLandCoordinateTolerance))-1)
		firstRow := max(0, int(math.Ceil(north-.5-webMercatorLandCoordinateTolerance)))
		lastRow := min(width-1, int(math.Ceil(south-.5-webMercatorLandCoordinateTolerance))-1)
		if firstColumn > lastColumn || firstRow > lastRow {
			return false, errors.New("land lookup bounds contain no display texel")
		}
		for row := firstRow; row <= lastRow; row++ {
			for column := firstColumn; column <= lastColumn; column++ {
				if pixels[row*width+column] == 1 {
					return true, nil
				}
			}
		}
		return false, nil
	}, nil
}

func webMercatorPixelEdge(latitude float64, width int) float64 {
	radians := latitude * math.Pi / 180
	return (1 - math.Asinh(math.Tan(radians))/math.Pi) / 2 * float64(width)
}

func globalTimezoneLocator() (forecasttiles.LocateTimezone, error) {
	finder, err := tzf.NewFullFinder()
	if err != nil {
		return nil, err
	}
	cache := make(map[string]*time.Location)
	return func(longitude, latitude float64) (*time.Location, error) {
		name := finder.GetTimezoneName(longitude, latitude)
		if name == "" {
			return time.UTC, nil
		}
		if location, ok := cache[name]; ok {
			return location, nil
		}
		location, err := time.LoadLocation(name)
		if err == nil {
			cache[name] = location
		}
		return location, err
	}, nil
}
