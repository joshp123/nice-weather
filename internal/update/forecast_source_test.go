package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/forecasttiles"
	"nice-weather-finder/internal/providers/ifs"
	"nice-weather-finder/internal/tiledelivery"
)

func TestForecastSourceUsesExactlyFourteenCompleteWorldwideDays(t *testing.T) {
	run := ifs.Run{Time: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	days, steps, err := worldwideForecastDaysAndSteps(time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 14 || days[0].Date.Format("2006-01-02") != "2026-07-19" || days[13].Date.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("forecast days = %+v", days)
	}
	available, err := ifs.ForecastSteps(run)
	if err != nil {
		t.Fatal(err)
	}
	want := stepsForDays(run.Time, days, globalOutlookDecodeWindow, available)
	if !reflect.DeepEqual(steps, want) || steps[0] != 6 || steps[len(steps)-1] != 360 {
		t.Fatalf("forecast steps = %v, want exact 6..360 envelope", steps)
	}
	firstWindow := globalOutlookDecodeWindow.start(days[0].Date)
	if valid := run.Time.Add(time.Duration(steps[0]) * time.Hour); !valid.Before(firstWindow) {
		t.Fatalf("step %d is not the one preceding worldwide window %s", steps[0], firstWindow)
	}
	if valid := run.Time.Add(time.Duration(steps[1]) * time.Hour); valid.Before(firstWindow) {
		t.Fatalf("step %d still precedes worldwide window %s", steps[1], firstWindow)
	}
	lastProductBoundary := globalProductWindow.end(days[len(days)-1].Date)
	if valid := run.Time.Add(time.Duration(steps[len(steps)-1]) * time.Hour); valid.Before(lastProductBoundary) {
		t.Fatalf("last step %d ends before worldwide product boundary %s", steps[len(steps)-1], lastProductBoundary)
	}
}

func TestValidateForecastSourceStagedRejectsProviderRunMixing(t *testing.T) {
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	days, steps, err := worldwideForecastDaysAndSteps(run.Add(time.Hour), ifs.Run{Time: run})
	if err != nil || len(days) != 14 {
		t.Fatal(err)
	}
	staged := syntheticForecastSourceStaged(run, steps)
	staged[len(staged)/2].run = run.Add(-24 * time.Hour)
	if err := validateStagedIFS(run, steps, staged); err == nil || !strings.Contains(err.Error(), "uses run") {
		t.Fatalf("mixed-run validation error = %v", err)
	}
}

func TestWebMercatorDisplayLandPredicateDoesNotBleedIntoNeighbouringTexel(t *testing.T) {
	const zoom = 3
	width := int(tiledelivery.TileSize) << zoom
	pixels := make([]byte, width*width)
	const landColumn, landRow = 1024, 1024
	pixels[landRow*width+landColumn] = 1
	isLand, err := webMercatorDisplayLandPredicate(pixels, zoom)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := isLand(webMercatorTestTexelBounds(width, landColumn, landRow)); err != nil || !got {
		t.Fatalf("marked display texel = %v, %v; want land", got, err)
	}
	if got, err := isLand(webMercatorTestTexelBounds(width, landColumn+1, landRow)); err != nil || got {
		t.Fatalf("neighbouring display texel = %v, %v; want water", got, err)
	}
	expanded := webMercatorTestTexelBounds(width, landColumn, landRow)
	expanded.East = webMercatorTestTexelBounds(width, landColumn+1, landRow).East
	if got, err := isLand(expanded); err != nil || !got {
		t.Fatalf("source-cell-sized lookup spanning marked texel = %v, %v; want land", got, err)
	}
	if _, err := isLand(delivery.GridBounds{West: 0, East: 1, South: 0, North: math.NaN()}); err == nil {
		t.Fatal("non-finite land lookup was accepted")
	}
}

func TestForecastSourceWebMercatorLandPredicateRasterizesExactDisplayGrid(t *testing.T) {
	archive := testLandZip(t)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	const zoom = 0
	width := int(tiledelivery.TileSize) << zoom
	pixels := make([]byte, width*width)
	pixels[20*width+10] = 1
	runner := &forecastSourceLandFakeRunner{payload: pixels}
	authority := newLandAuthority(t.TempDir(), server.Client(), runner, landSource{
		URL: server.URL, Size: int64(len(archive)), SHA256: hex.EncodeToString(digest[:]),
	})
	land, err := forecastWebMercatorDisplayLandMask(context.Background(), authority, t.TempDir(), zoom)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := land.Displays(webMercatorTestTexelBounds(width, 10, 20)); err != nil || !got {
		t.Fatalf("rasterized display texel = %v, %v; want land", got, err)
	}
	wantMask := sha256.Sum256(pixels)
	identity := land.Identity()
	if identity.SourceArchiveSHA256 != hex.EncodeToString(digest[:]) || identity.MaskSHA256 != hex.EncodeToString(wantMask[:]) ||
		identity.Authority != forecasttiles.LandAuthorityNaturalEarth || identity.Dataset != forecasttiles.LandDatasetNaturalEarth10M ||
		identity.Version != forecasttiles.LandVersionNaturalEarth511 || identity.DerivationContract != forecasttiles.LandDerivationWebMercatorCentreV1 {
		t.Fatalf("land identity = %+v", identity)
	}
	if len(runner.invocations) != 2 || runner.invocations[0].command != "ogr2ogr" || runner.invocations[1].command != "gdal_rasterize" {
		t.Fatalf("land commands = %+v; want projection then rasterization", runner.invocations)
	}
	projection := strings.Join(runner.invocations[0].arguments, " ")
	for _, required := range []string{"-f GPKG", "-t_srs EPSG:3857", "-nlt MULTIPOLYGON", "-nln land"} {
		if !strings.Contains(projection, required) {
			t.Errorf("Web Mercator ogr2ogr args %q missing %q", projection, required)
		}
	}
	arguments := strings.Join(runner.invocations[1].arguments, " ")
	for _, required := range []string{
		"-a_srs EPSG:3857",
		"-te -20037508.342789244 -20037508.342789244 20037508.342789244 20037508.342789244",
		"-ts 256 256", "-l land",
	} {
		if !strings.Contains(arguments, required) {
			t.Errorf("Web Mercator gdal_rasterize args %q missing %q", arguments, required)
		}
	}
	if strings.Contains(arguments, "-at") {
		t.Fatalf("display raster unexpectedly uses all-touched expansion: %q", arguments)
	}
}

type forecastSourceLandInvocation struct {
	command   string
	arguments []string
}

type forecastSourceLandFakeRunner struct {
	payload     []byte
	invocations []forecastSourceLandInvocation
}

func (runner *forecastSourceLandFakeRunner) Run(_ context.Context, command string, arguments ...string) ([]byte, error) {
	runner.invocations = append(runner.invocations, forecastSourceLandInvocation{
		command: command, arguments: append([]string(nil), arguments...),
	})
	switch command {
	case "ogr2ogr":
		if len(arguments) < 2 {
			return nil, errors.New("ogr2ogr destination is missing")
		}
		return nil, os.WriteFile(arguments[len(arguments)-2], []byte("projected fixture"), 0o600)
	case "gdal_rasterize":
		return nil, os.WriteFile(arguments[len(arguments)-1], runner.payload, 0o600)
	default:
		return nil, errors.New("unexpected command " + command)
	}
}

func webMercatorTestTexelBounds(width, column, row int) delivery.GridBounds {
	longitude := func(edge int) float64 {
		return float64(edge)/float64(width)*360 - 180
	}
	latitude := func(edge int) float64 {
		return math.Atan(math.Sinh(math.Pi*(1-2*float64(edge)/float64(width)))) * 180 / math.Pi
	}
	return delivery.GridBounds{
		West: longitude(column), East: longitude(column + 1),
		North: latitude(row), South: latitude(row + 1),
	}
}

func syntheticForecastSourceStaged(run time.Time, steps []int) []stagedFrame {
	result := make([]stagedFrame, 0, len(steps)*len(ifs.RequiredSurfaceFields()))
	for _, step := range steps {
		for _, parameter := range ifs.RequiredSurfaceFields() {
			field, err := ifsPipelineField(parameter)
			if err != nil {
				panic(err)
			}
			result = append(result, stagedFrame{
				path: fmt.Sprintf("/%s-%03d.grib2", parameter, step), field: field, step: step,
				run: run, resolution: 28, spacing: .25,
			})
		}
	}
	return result
}

func updateTestLandMask(t *testing.T, displays forecasttiles.DisplayLand) forecasttiles.LandMask {
	t.Helper()
	mask, err := forecasttiles.NewLandMask(displays, forecasttiles.LandIdentity{
		Authority: forecasttiles.LandAuthorityNaturalEarth, Dataset: forecasttiles.LandDatasetNaturalEarth10M, Version: forecasttiles.LandVersionNaturalEarth511,
		SourceArchiveSHA256: strings.Repeat("a", 64), MaskSHA256: strings.Repeat("b", 64), DerivationContract: forecasttiles.LandDerivationWebMercatorCentreV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mask
}
