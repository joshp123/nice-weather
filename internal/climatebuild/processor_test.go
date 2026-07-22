package climatebuild

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"nice-weather-finder/internal/climate"
	"nice-weather-finder/internal/providers/agera5"
	"nice-weather-finder/internal/weather"
)

type fakeReference struct {
	field       agera5.Field
	first, last time.Time
	coordinate  agera5.BlockCoordinate
	group       int
}

func (reference fakeReference) Field() agera5.Field           { return reference.field }
func (reference fakeReference) FirstDate() time.Time          { return reference.first }
func (reference fakeReference) LastDate() time.Time           { return reference.last }
func (reference fakeReference) Block() agera5.BlockCoordinate { return reference.coordinate }

type fakeSource struct {
	references []ChunkReference
	blocks     map[fakeReference]Block
	absent     map[fakeReference]bool
	planErr    error
	fetches    int
	width      int
	height     int
}

func (source *fakeSource) Shape(agera5.BlockCoordinate) (int, int, error) {
	return source.width, source.height, nil
}

func (source *fakeSource) Plan(firstDate, lastDate time.Time, coordinate agera5.BlockCoordinate) ([]ChunkReference, error) {
	if source.planErr != nil {
		return nil, source.planErr
	}
	return append([]ChunkReference(nil), source.references...), nil
}

func (source *fakeSource) Fetch(ctx context.Context, reference ChunkReference) (Block, error) {
	source.fetches++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := reference.(fakeReference)
	if source.absent[value] {
		return nil, agera5.ErrChunkNotFound
	}
	block, ok := source.blocks[value]
	if !ok {
		return nil, errors.New("missing fake block")
	}
	return block, nil
}

type fakeBlock struct {
	field         agera5.Field
	reference     fakeReference
	dates         []time.Time
	width, height int
	value         func(agera5.Field, time.Time, int, int) (float32, bool)
	latitudeBias  float64
}

func (block fakeBlock) Reference() ChunkReference { return block.reference }
func (block fakeBlock) TimeCount() int            { return len(block.dates) }
func (block fakeBlock) LatitudeCount() int        { return block.height }
func (block fakeBlock) LongitudeCount() int       { return block.width }

func (block fakeBlock) Date(offset int) (time.Time, error) {
	if offset < 0 || offset >= len(block.dates) {
		return time.Time{}, errors.New("bad date offset")
	}
	return block.dates[offset], nil
}

func (block fakeBlock) Location(latitudeOffset, longitudeOffset int) (float64, float64, error) {
	if latitudeOffset < 0 || latitudeOffset >= block.height || longitudeOffset < 0 || longitudeOffset >= block.width {
		return 0, 0, errors.New("bad location offset")
	}
	return 36.5 + float64(latitudeOffset)/10 + block.latitudeBias, -4.5 + float64(longitudeOffset)/10, nil
}

func (block fakeBlock) Value(timeOffset, latitudeOffset, longitudeOffset int) (float32, bool, error) {
	if timeOffset < 0 || timeOffset >= len(block.dates) || latitudeOffset < 0 || latitudeOffset >= block.height || longitudeOffset < 0 || longitudeOffset >= block.width {
		return 0, false, errors.New("bad value offset")
	}
	value, available := block.value(block.field, block.dates[timeOffset], latitudeOffset, longitudeOffset)
	return value, available, nil
}

func TestBuildBlockMatchesSingleCellAggregateAndIgnoresFieldOrder(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	ordered := fixtureSource(window, 3, 2, completeValue)
	shuffled := fixtureSource(window, 3, 2, completeValue)
	rand.New(rand.NewSource(7)).Shuffle(len(shuffled.references), func(left, right int) {
		shuffled.references[left], shuffled.references[right] = shuffled.references[right], shuffled.references[left]
	})

	got, err := BuildSourceCanaryBlock(context.Background(), ordered, agera5.BlockCoordinate{Latitude: 7, Longitude: 8}, window)
	if err != nil {
		t.Fatal(err)
	}
	gotShuffled, err := BuildSourceCanaryBlock(context.Background(), shuffled, agera5.BlockCoordinate{Latitude: 7, Longitude: 8}, window)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, gotShuffled) {
		t.Fatal("field/reference order changed the climate block")
	}
	if got.Width != 3 || got.Height != 2 || got.Stats.ExpectedDays != 366 || got.Stats.ProcessedDays != 366 || got.Stats.CompleteCells != 6 || got.Stats.NoDataCells != 0 || got.Stats.SourceTimeChunks != 2 || got.Stats.ChunksFetched != 18 || got.Stats.PeakRetainedDecodedBytes <= 0 {
		t.Fatalf("unexpected result or stats: %+v", got.Stats)
	}

	var byMonth [12][]climate.AgERA5Day
	for date := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC); date.Year() == 2024; date = date.AddDate(0, 0, 1) {
		byMonth[date.Month()-1] = append(byMonth[date.Month()-1], normalizedDay(date, 36.5, -4.5))
	}
	for month := range got.Months {
		distribution, err := climate.Aggregate(byMonth[month], window)
		if err != nil {
			t.Fatalf("aggregate month %d: %v", month+1, err)
		}
		want, err := distribution.DeliveryRecord()
		if err != nil {
			t.Fatalf("delivery month %d: %v", month+1, err)
		}
		if got.Months[month][0] != want {
			t.Fatalf("month %d differs\ngot  %+v\nwant %+v", month+1, got.Months[month][0], want)
		}
	}
}

func TestRequiredFieldOrderMatchesProviderContract(t *testing.T) {
	if !reflect.DeepEqual(requiredFields[:], agera5.RequiredFields()) {
		t.Fatalf("climate-build fields %v differ from provider fields %v", requiredFields, agera5.RequiredFields())
	}
}

func TestNormalizeUsesExactDomainUnits(t *testing.T) {
	metrics, repairs := normalize(rawValues{300, 290, 305, 285, 7.5, .25, 12_500_000, .42, 5})
	if repairs != (repairMagnitudes{}) {
		t.Fatalf("valid temperatures reported as repaired: %+v", repairs)
	}
	if err := metrics.Validate(); err != nil {
		t.Fatal(err)
	}
	assertNear(t, metrics.ComfortableTempC, 26.85)
	assertNear(t, metrics.MinTempC, 16.85)
	assertNear(t, metrics.MaxTempC, 31.85)
	assertNear(t, metrics.DewPointC.Value, 11.85)
	assertNear(t, metrics.RainMM, 7.5)
	assertNear(t, metrics.WetTimeFraction.Value, .25)
	assertNear(t, metrics.SolarEnergyMJm2.Value, 12.5)
	assertNear(t, metrics.CloudCoverPercent.Value, 42)
	assertNear(t, metrics.WindSpeedKPH, 18)
	if !metrics.Valid || !metrics.WetTimeFraction.Available || !metrics.SolarEnergyMJm2.Available || !metrics.CloudCoverPercent.Available || !metrics.DewPointC.Available || metrics.RelativeHumidityPercent.Available || metrics.NativeResolutionKM != 30 {
		t.Fatalf("unexpected evidence flags: %+v", metrics)
	}
}

func TestNormalizeOrdersAgERA5CrossedExtremaAtProviderBoundary(t *testing.T) {
	metrics, repairs := normalize(rawValues{300, 305, 290, 285, 7.5, .25, 12_500_000, .42, 5})
	if repairs.temperature == 0 {
		t.Fatal("crossed AgERA5 extrema were not repaired")
	}
	assertNear(t, repairs.temperature, 15)
	assertNear(t, repairs.dewPoint, 0)
	assertNear(t, metrics.MinTempC, 16.85)
	assertNear(t, metrics.MaxTempC, 31.85)
	if err := metrics.Validate(); err != nil {
		t.Fatalf("repaired metrics do not satisfy the strict domain contract: %v", err)
	}
}

func TestNormalizeClampsExactLiveAgERA5DaytimeMeanBelowDailyRange(t *testing.T) {
	// Official AgERA5 v2 block 88,90, cell 4,22, 2009-06-19.
	metrics, repairs := normalize(rawValues{
		271.15441895, 276.39099121, 282.12991333, 265,
		0, 0, 10_000_000, .2, 2,
	})
	assertNear(t, metrics.ComfortableTempC, 3.24099121)
	assertNear(t, metrics.MinTempC, 3.24099121)
	assertNear(t, metrics.MaxTempC, 8.97991333)
	assertNear(t, repairs.comfortTemperature, 5.23657226)
	if repairs.comfortTemperatureDirection != -1 {
		t.Fatalf("repair direction = %d; want below-range", repairs.comfortTemperatureDirection)
	}
	if err := metrics.Validate(); err != nil {
		t.Fatalf("AgERA5 daytime-range repair did not satisfy the strict generic contract: %v", err)
	}
}

func TestNormalizeClampsOnlyAgERA5DaytimeMeanBeyondExistingTolerance(t *testing.T) {
	tests := []struct {
		name       string
		comfort    float64
		want       float64
		adjustment float64
		direction  int8
	}{
		{name: "below minimum within tolerance", comfort: 289, want: 289, adjustment: 0, direction: 0},
		{name: "below minimum at tolerance", comfort: 285, want: 285, adjustment: 0, direction: 0},
		{name: "below minimum beyond tolerance", comfort: 284, want: 290, adjustment: 6, direction: -1},
		{name: "inside range", comfort: 300, want: 300, adjustment: 0, direction: 0},
		{name: "above maximum within tolerance", comfort: 307.5, want: 307.5, adjustment: 0, direction: 0},
		{name: "above maximum at tolerance", comfort: 310, want: 310, adjustment: 0, direction: 0},
		{name: "above maximum beyond tolerance", comfort: 311, want: 305, adjustment: 6, direction: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics, repairs := normalize(rawValues{test.comfort, 290, 305, 285, 0, 0, 10_000_000, .2, 2})
			assertNear(t, metrics.ComfortableTempC, test.want-273.15)
			assertNear(t, repairs.comfortTemperature, test.adjustment)
			if repairs.comfortTemperatureDirection != test.direction {
				t.Fatalf("repair direction = %d; want %d", repairs.comfortTemperatureDirection, test.direction)
			}
			if err := metrics.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeDoesNotLaunderInvalidDaytimeOrRangeTemperature(t *testing.T) {
	tests := []struct {
		name   string
		values rawValues
	}{
		{name: "nonfinite daytime", values: rawValues{math.Inf(1), 290, 305, 285, 0, 0, 10_000_000, .2, 2}},
		{name: "unsupported daytime", values: rawValues{344.15, 290, 305, 285, 0, 0, 10_000_000, .2, 2}},
		{name: "unsupported minimum", values: rawValues{300, 172, 305, 285, 0, 0, 10_000_000, .2, 2}},
		{name: "unsupported maximum", values: rawValues{300, 290, 344.15, 285, 0, 0, 10_000_000, .2, 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics, repairs := normalize(test.values)
			if repairs.comfortTemperature != 0 {
				t.Fatalf("invalid source temperature was repaired: %+v", repairs)
			}
			if err := metrics.Validate(); err == nil {
				t.Fatal("strict generic validation accepted invalid source temperature")
			}
		})
	}
}

func TestNormalizeCapsExactLiveAgERA5DewPointCrossingAtOrderedMaximum(t *testing.T) {
	// Official AgERA5 v2 block 104,35, cell 9,16, 2009-03-10. The
	// independently bias-corrected dew point is physically above Tmax.
	metrics, repairs := normalize(rawValues{
		239.5069580078125, 238.32176208496094, 241.38815307617188,
		243.49264526367188, 0, 0, 0, 0, 0,
	})
	assertNear(t, metrics.ComfortableTempC, -33.64304199218748)
	assertNear(t, metrics.MinTempC, -34.82823791503904)
	assertNear(t, metrics.MaxTempC, -31.761846923828102)
	assertNear(t, metrics.DewPointC.Value, metrics.MaxTempC)
	assertNear(t, repairs.temperature, 0)
	assertNear(t, repairs.dewPoint, 2.1044921875)
	if err := metrics.Validate(); err != nil {
		t.Fatalf("AgERA5-specific repair did not satisfy the strict generic contract: %v", err)
	}
}

func TestNormalizePreservesPreviouslyAcceptedDewPointTolerance(t *testing.T) {
	metrics, repairs := normalize(rawValues{300, 290, 305, 306.5, 0, 0, 0, 0, 0})
	assertNear(t, metrics.DewPointC.Value, 33.35)
	if repairs.dewPoint != 0 {
		t.Fatalf("accepted v1 dew-point tolerance was changed: %+v", repairs)
	}
	if err := metrics.Validate(); err != nil {
		t.Fatalf("generic weather contract rejected its existing tolerance: %v", err)
	}
}

func TestNormalizeDoesNotLaunderInvalidDewPointEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		dewPoint float64
	}{
		{name: "positive infinity", dewPoint: math.Inf(1)},
		{name: "above supported Celsius range", dewPoint: 344.15},
	} {
		t.Run(test.name, func(t *testing.T) {
			metrics, repairs := normalize(rawValues{300, 290, 305, test.dewPoint, 0, 0, 0, 0, 0})
			if repairs.dewPoint != 0 || metrics.DewPointC.Value != test.dewPoint-273.15 {
				t.Fatalf("invalid dew point was repaired: metrics=%+v repairs=%+v", metrics, repairs)
			}
			if err := metrics.Validate(); err == nil {
				t.Fatal("strict generic validation accepted invalid raw dew point")
			}
		})
	}
}

func TestBuildLandBlockMasksWaterBeforeNormalisation(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	value := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if longitudeOffset == 1 && field == agera5.Precipitation {
			return -50, true
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	result, err := BuildLandBlock(context.Background(), fixtureSource(window, 2, 1, value), coordinate, window, []bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.LandCells != 1 || result.Stats.WaterCells != 1 || result.Stats.CompleteCells != 1 || result.Stats.NoDataCells != 1 || result.Stats.TemperatureRepairs != 0 {
		t.Fatalf("unexpected land-mask stats: %+v", result.Stats)
	}
	if !result.Months[0][0].Valid || result.Months[0][1].Valid {
		t.Fatalf("unexpected land-mask records: land=%+v water=%+v", result.Months[0][0], result.Months[0][1])
	}
}

func TestBuildLandBlockReportsAgERA5TemperatureRepairs(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	crossingDate := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	dewPointCrossingDate := time.Date(2024, time.February, 15, 0, 0, 0, 0, time.UTC)
	comfortCrossingDate := time.Date(2024, time.March, 16, 0, 0, 0, 0, time.UTC)
	value := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if date.Equal(crossingDate) {
			switch field {
			case agera5.MinimumTemperature:
				return 292.2, true
			case agera5.MaximumTemperature:
				return 292, true
			}
		}
		if date.Equal(dewPointCrossingDate) && field == agera5.MeanDewPoint {
			return 304, true
		}
		if date.Equal(comfortCrossingDate) && field == agera5.DaytimeMeanTemperature {
			return 284, true
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	result, err := BuildLandBlock(context.Background(), fixtureSource(window, 1, 1, value), coordinate, window, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.TemperatureRepairs != 1 {
		t.Fatalf("temperature repairs = %d; want 1", result.Stats.TemperatureRepairs)
	}
	assertNear(t, result.Stats.MaximumTemperatureRepair, float64(float32(292.2)-float32(292)))
	if result.Stats.ComfortTemperatureRepairs != 1 {
		t.Fatalf("material daytime-range repairs = %d; want 1", result.Stats.ComfortTemperatureRepairs)
	}
	if result.Stats.ComfortTemperatureBelowRangeRepairs != 1 || result.Stats.ComfortTemperatureAboveRangeRepairs != 0 {
		t.Fatalf("material daytime-range directions = below %d above %d", result.Stats.ComfortTemperatureBelowRangeRepairs, result.Stats.ComfortTemperatureAboveRangeRepairs)
	}
	assertNear(t, result.Stats.MaximumComfortTemperatureRepair, float64(float32(291.15)-float32(284)))
	if result.Stats.DewPointRepairs != 1 {
		t.Fatalf("dew-point repairs = %d; want 1", result.Stats.DewPointRepairs)
	}
	assertNear(t, result.Stats.MaximumDewPointRepair, float64(float32(304)-float32(295.15)))
}

func TestBuildLandBlockRequiresExactMaskGeometry(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	source := fixtureSource(window, 2, 1, completeValue)
	if _, err := BuildLandBlock(context.Background(), source, coordinate, window, nil); err == nil || !strings.Contains(err.Error(), "land mask is required") {
		t.Fatalf("nil mask error = %v", err)
	}
	if _, err := BuildLandBlock(context.Background(), source, coordinate, window, []bool{true}); err == nil || !strings.Contains(err.Error(), "has 1 cells; want 2") {
		t.Fatalf("wrong-size mask error = %v", err)
	}
}

func TestBuildLandBlockKeepsStructurallyUnsupportedLandInvalid(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	missingCell := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if longitudeOffset == 1 {
			return 0, false
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	result, err := BuildLandBlock(context.Background(), fixtureSource(window, 2, 1, missingCell), coordinate, window, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.LandCells != 2 || result.Stats.CompleteCells != 1 || result.Stats.UnsupportedLandCells != 1 || result.Stats.NoDataCells != 1 || result.Months[0][1].Valid {
		t.Fatalf("structurally unsupported land result = %+v, record = %+v", result.Stats, result.Months[0][1])
	}

	absentBlock := fixtureSource(window, 1, 1, completeValue)
	for reference := range absentBlock.blocks {
		absentBlock.absent[reference] = true
	}
	absentResult, err := BuildLandBlock(context.Background(), absentBlock, coordinate, window, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if absentResult.Stats.LandCells != 1 || absentResult.Stats.UnsupportedLandCells != 1 || absentResult.Stats.CompleteCells != 0 || absentResult.Stats.NoDataCells != 1 || absentResult.Stats.SparseOcean || absentResult.Months[0][0].Valid {
		t.Fatalf("isolated absent land result = %+v, record = %+v", absentResult.Stats, absentResult.Months[0][0])
	}

	absentTwoCellBlock := fixtureSource(window, 2, 1, completeValue)
	for reference := range absentTwoCellBlock.blocks {
		absentTwoCellBlock.absent[reference] = true
	}
	twoCellResult, err := BuildLandBlock(context.Background(), absentTwoCellBlock, coordinate, window, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	if twoCellResult.Stats.LandCells != 2 || twoCellResult.Stats.UnsupportedLandCells != 2 || twoCellResult.Stats.CompleteCells != 0 || twoCellResult.Stats.NoDataCells != 2 || twoCellResult.Stats.SparseOcean || twoCellResult.Months[0][0].Valid || twoCellResult.Months[0][1].Valid {
		t.Fatalf("two-cell absent land result = %+v, records = %+v", twoCellResult.Stats, twoCellResult.Months[0])
	}
}

func TestBuildBlockKeepsAllFieldNoDataCellsInvalid(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2023, LastYear: 2023}
	value := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if latitudeOffset == 0 && longitudeOffset == 1 {
			return 0, false
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	result, err := BuildSourceCanaryBlock(context.Background(), fixtureSource(window, 2, 1, value), agera5.BlockCoordinate{Latitude: 7, Longitude: 8}, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.CompleteCells != 1 || result.Stats.NoDataCells != 1 || result.Months[0][1].Valid {
		t.Fatalf("unexpected no-data result: %+v %+v", result.Stats, result.Months[0][1])
	}
}

func TestBuildBlockDistinguishesSparseOceanFromPartialChunk(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}

	ocean := fixtureSource(window, 2, 1, completeValue)
	for reference := range ocean.blocks {
		ocean.absent[reference] = true
	}
	result, err := BuildSourceCanaryBlock(context.Background(), ocean, coordinate, window)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Stats.SparseOcean || result.Width != 2 || result.Height != 1 || result.Stats.LogicalCells != 2 || result.Stats.NoDataCells != 2 || result.Stats.ChunksAbsent != 18 || result.Stats.CompleteCells != 0 || len(result.Months[0]) != 2 || result.Months[0][0].Valid {
		t.Fatalf("sparse result = %+v", result)
	}

	partial := fixtureSource(window, 2, 1, completeValue)
	partial.absent[partial.references[0].(fakeReference)] = true
	if _, err := BuildSourceCanaryBlock(context.Background(), partial, coordinate, window); !errors.Is(err, ErrPartialFieldChunk) {
		t.Fatalf("partial field chunk error = %v", err)
	}

	missingTime := fixtureSource(window, 2, 1, completeValue)
	for reference := range missingTime.blocks {
		if reference.group == 0 {
			missingTime.absent[reference] = true
		}
	}
	if _, err := BuildSourceCanaryBlock(context.Background(), missingTime, coordinate, window); !errors.Is(err, ErrIncompleteSource) {
		t.Fatalf("missing time chunk error = %v", err)
	}
}

func TestBuildBlockRejectsPartialAndIncompleteCellDays(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	partialValue := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if date.Equal(time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)) && field == agera5.CloudCover {
			return 0, false
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	if _, err := BuildSourceCanaryBlock(context.Background(), fixtureSource(window, 1, 1, partialValue), coordinate, window); !errors.Is(err, ErrPartialCell) {
		t.Fatalf("partial cell error = %v", err)
	}

	incompleteValue := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if date.Equal(time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)) {
			return 0, false
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	if _, err := BuildSourceCanaryBlock(context.Background(), fixtureSource(window, 1, 1, incompleteValue), coordinate, window); !errors.Is(err, ErrIncompleteCell) {
		t.Fatalf("incomplete cell error = %v", err)
	}
}

func TestBuildBlockReportsExactUnscoreableCellDayCause(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	invalidValue := func(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
		if date.Equal(time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)) && latitudeOffset == 11 && longitudeOffset == 9 && field == agera5.Precipitation {
			return -0.25, true
		}
		return completeValue(field, date, latitudeOffset, longitudeOffset)
	}
	_, err := BuildSourceCanaryBlock(context.Background(), fixtureSource(window, 32, 16, invalidValue), coordinate, window)
	if err == nil {
		t.Fatal("expected invalid weather error")
	}
	want := "accumulate AgERA5 2024-01-15 cell 11,9: day 2024-01-15 is not scoreable: rain, solar energy and wind cannot be negative: rain=-0.25 mm solar=9 MJ/m² wind=19.8 km/h gust=0 km/h"
	if err.Error() != want {
		t.Fatalf("invalid weather error = %q, want %q", err, want)
	}
}

func TestBuildBlockRejectsWrongReturnedReference(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	source := fixtureSource(window, 1, 1, completeValue)
	want := source.references[0].(fakeReference)
	wrong := want
	wrong.field = agera5.CloudCover
	source.blocks[want] = fakeBlock{
		field: want.field, reference: wrong,
		dates: datesBetween(want.first, want.last), width: 1, height: 1, value: completeValue,
	}
	if _, err := BuildSourceCanaryBlock(context.Background(), source, want.coordinate, window); err == nil || !strings.Contains(err.Error(), "different source reference") {
		t.Fatalf("wrong-reference error = %v", err)
	}
}

func TestBuildBlockUsesSourceGeometryAndHonoursCancellation(t *testing.T) {
	window := climate.ReferenceWindow{FirstYear: 2023, LastYear: 2023}
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	edge := fixtureSource(window, 16, 9, completeValue)
	result, err := BuildSourceCanaryBlock(context.Background(), edge, coordinate, window)
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 16 || result.Height != 9 || result.Stats.LogicalCells != 144 {
		t.Fatalf("edge geometry = %dx%d, stats %+v", result.Width, result.Height, result.Stats)
	}

	cancelled := fixtureSource(window, 1, 1, completeValue)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildSourceCanaryBlock(ctx, cancelled, coordinate, window); !errors.Is(err, context.Canceled) || cancelled.fetches != 0 {
		t.Fatalf("cancelled build error=%v fetches=%d", err, cancelled.fetches)
	}
}

func TestHotCellDayNormalizationAllocatesNothing(t *testing.T) {
	var blocks fieldBlocks
	date := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for index, field := range requiredFields {
		blocks[index] = fakeBlock{field: field, reference: fakeReference{field: field, first: date, last: date}, dates: []time.Time{date}, width: 1, height: 1, value: completeValue}
	}
	allocations := testing.AllocsPerRun(1000, func() {
		values, available, err := readCellDay(blocks, 0, 0, 0)
		if err != nil || available != len(requiredFields) {
			panic("fixture failed")
		}
		_, _ = normalize(values)
	})
	if allocations != 0 {
		t.Fatalf("hot cell-day allocations = %.2f; want 0", allocations)
	}
}

func TestAccumulatorMemoryIsBoundedPerBlock(t *testing.T) {
	accumulator, err := climate.NewStreamingAccumulator(climate.DefaultReferenceWindow())
	if err != nil {
		t.Fatal(err)
	}
	accumulatorBytes := uintptr(16*32) * unsafe.Sizeof(*accumulator)
	sourceBytes := uintptr(9 * 2112 * 16 * 32 * 4)
	if accumulatorBytes > 16<<20 || sourceBytes > 40<<20 {
		t.Fatalf("bounded state grew: accumulators=%d source-chunk-set=%d", accumulatorBytes, sourceBytes)
	}
}

func BenchmarkHotCellDayNormalization(b *testing.B) {
	var blocks fieldBlocks
	date := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for index, field := range requiredFields {
		blocks[index] = fakeBlock{field: field, reference: fakeReference{field: field, first: date, last: date}, dates: []time.Time{date}, width: 1, height: 1, value: completeValue}
	}
	b.ReportAllocs()
	b.ReportMetric(float64(9*2112*16*32*4)/(1<<20), "source-MiB/chunk-set")
	for index := 0; index < b.N; index++ {
		values, _, err := readCellDay(blocks, 0, 0, 0)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = normalize(values)
	}
}

func BenchmarkCompleteBlockAndFinalization(b *testing.B) {
	window := climate.ReferenceWindow{FirstYear: 2024, LastYear: 2024}
	source := fixtureSource(window, 32, 16, completeValue)
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	b.ReportAllocs()
	b.ReportMetric(512*366, "cell-days/op")
	b.ReportMetric(1, "reference-years/op")
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		result, err := BuildSourceCanaryBlock(context.Background(), source, coordinate, window)
		if err != nil {
			b.Fatal(err)
		}
		if result.Stats.CompleteCells != 512 || len(result.Months[0]) != 512 {
			b.Fatal("incomplete benchmark result")
		}
	}
}

func BenchmarkCompleteCellDayPhases(b *testing.B) {
	metrics, _ := normalize(rawValues{297.15, 291.15, 301.15, 285.15, 0, 0, 16_000_000, .2, 3})
	b.Run("validate", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if err := metrics.Validate(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("score-two-profiles", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if !weather.Score(metrics, weather.ProfileNice).Available || !weather.Score(metrics, weather.ProfileCycling).Available {
				b.Fatal("unscoreable benchmark metrics")
			}
		}
	})
	b.Run("score-shared-profiles", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			nice, cycling, err := weather.ScoreProfilesChecked(metrics)
			if err != nil || !nice.Available || !cycling.Available {
				b.Fatal("unscoreable benchmark metrics")
			}
		}
	})
}

func fixtureSource(window climate.ReferenceWindow, width, height int, value func(agera5.Field, time.Time, int, int) (float32, bool)) *fakeSource {
	coordinate := agera5.BlockCoordinate{Latitude: 7, Longitude: 8}
	first := time.Date(window.FirstYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(window.LastYear, time.December, 31, 0, 0, 0, 0, time.UTC)
	middle := time.Date(window.FirstYear, time.July, 1, 0, 0, 0, 0, time.UTC)
	if window.FirstYear != window.LastYear {
		middle = time.Date(window.FirstYear+(window.LastYear-window.FirstYear+1)/2, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	ranges := [][2]time.Time{{first, middle.AddDate(0, 0, -1)}, {middle, last}}
	source := &fakeSource{blocks: make(map[fakeReference]Block), absent: make(map[fakeReference]bool), width: width, height: height}
	for group, dateRange := range ranges {
		dates := datesBetween(dateRange[0], dateRange[1])
		for _, field := range requiredFields {
			reference := fakeReference{field: field, first: dateRange[0], last: dateRange[1], coordinate: coordinate, group: group}
			source.references = append(source.references, reference)
			source.blocks[reference] = fakeBlock{field: field, reference: reference, dates: dates, width: width, height: height, value: value}
		}
	}
	return source
}

func datesBetween(first, last time.Time) []time.Time {
	result := make([]time.Time, 0, int(last.Sub(first)/(24*time.Hour))+1)
	for date := first; !date.After(last); date = date.AddDate(0, 0, 1) {
		result = append(result, date)
	}
	return result
}

func completeValue(field agera5.Field, date time.Time, latitudeOffset, longitudeOffset int) (float32, bool) {
	values := rawForDate(date)
	switch field {
	case agera5.DaytimeMeanTemperature:
		return values.daytimeTemperature, true
	case agera5.MinimumTemperature:
		return values.minimumTemperature, true
	case agera5.MaximumTemperature:
		return values.maximumTemperature, true
	case agera5.MeanDewPoint:
		return values.dewPoint, true
	case agera5.Precipitation:
		return values.precipitation, true
	case agera5.PrecipitationDuration:
		return values.wetTimeFraction, true
	case agera5.SolarRadiation:
		return values.solarRadiation, true
	case agera5.CloudCover:
		return values.cloudCoverFraction, true
	case agera5.MeanWindSpeed:
		return values.windSpeed, true
	default:
		return 0, false
	}
}

type rawFloat32 struct {
	daytimeTemperature float32
	minimumTemperature float32
	maximumTemperature float32
	dewPoint           float32
	precipitation      float32
	wetTimeFraction    float32
	solarRadiation     float32
	cloudCoverFraction float32
	windSpeed          float32
}

func rawForDate(date time.Time) rawFloat32 {
	daytime, minimum, maximum, dewPoint := float32(297.15), float32(291.15), float32(301.15), float32(285.15)
	rain, wet, solar, cloud, wind := float32(0), float32(0), float32(16_000_000), float32(.2), float32(3)
	switch {
	case date.Day() >= 11 && date.Day() <= 13:
		daytime, minimum, maximum, dewPoint = 280.15, 277.15, 283.15, 278.15
		rain, wet, solar, cloud, wind = 5, .8, 2_000_000, .95, 9
	case date.Day()%7 == 0:
		daytime, minimum, maximum, dewPoint = 303.15, 296.15, 307.15, 297.15
	case date.Day()%5 == 0:
		daytime, minimum, maximum, dewPoint = 291.15, 286.15, 295.15, 283.15
		rain, wet, solar, cloud, wind = 1, .2, 9_000_000, .55, 5.5
	}
	return rawFloat32{daytime, minimum, maximum, dewPoint, rain, wet, solar, cloud, wind}
}

func normalizedDay(date time.Time, latitude, longitude float64) climate.AgERA5Day {
	raw := rawForDate(date)
	return climate.AgERA5Day{
		Date: date.Format(time.DateOnly), Latitude: latitude, Longitude: longitude,
		DaytimeMeanTemperatureC:   float64(raw.daytimeTemperature) - 273.15,
		MinimumTemperatureC:       float64(raw.minimumTemperature) - 273.15,
		MaximumTemperatureC:       float64(raw.maximumTemperature) - 273.15,
		DewPointC:                 float64(raw.dewPoint) - 273.15,
		PrecipitationMM:           float64(raw.precipitation),
		PrecipitationDurationFrac: float64(raw.wetTimeFraction),
		SolarEnergyMJm2:           float64(raw.solarRadiation) / 1_000_000,
		CloudCoverPercent:         float64(raw.cloudCoverFraction) * 100,
		WindSpeedKPH:              float64(raw.windSpeed) * 3.6,
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12g, want %.12g", got, want)
	}
}
