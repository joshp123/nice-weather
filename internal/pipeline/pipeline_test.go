package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestDecodeUsesBinaryCropAndExplicitMetadata(t *testing.T) {
	runner := &fakeRunner{t: t}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 150, East: 152, South: -35, North: -34})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := decoder.Decode(context.Background(), DecodeRequest{
		Path: "temperature.grib2", Field: Temperature,
		Run: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 7, CellDegrees: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Semantics != Instant || !frame.End.Equal(time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("metadata was not interpreted: %+v", frame)
	}
	if len(frame.Grid.Values) != 2 || math.Abs(float64(frame.Grid.Values[0])-6.85) > .001 || !float32NaN(frame.Grid.Values[1]) {
		t.Fatalf("binary grid was not decoded and normalized: %v", frame.Grid.Values)
	}
	if !runner.sawCrop {
		t.Fatal("gdal_translate crop was not requested")
	}
	if !runner.sawFloat {
		t.Fatal("GRIB intermediate was not requested as NC_FLOAT")
	}
}

func TestDecoderRejectsImplicitBounds(t *testing.T) {
	if _, err := NewDecoder(&fakeRunner{t: t}, delivery.GridBounds{}); err == nil {
		t.Fatal("decoder accepted implicit zero bounds")
	}
}

func TestCropPhysicalGridPreservesFloatNetCDFAndNoData(t *testing.T) {
	if _, err := exec.LookPath("gdal_translate"); err != nil {
		t.Skip("gdal_translate is not on PATH")
	}
	directory := t.TempDir()
	ascii := filepath.Join(directory, "packed.asc")
	if err := os.WriteFile(ascii, []byte("ncols 2\nnrows 2\nxllcorner 0\nyllcorner 0\ncellsize 1\nNODATA_value -32767\n-32767 100\n200 -32767\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packed := filepath.Join(directory, "float.nc")
	runner := ExecRunner{}
	if _, err := runner.Run(context.Background(), "gdal_translate", "-q", "-of", "netCDF", "-ot", "Float32", ascii, packed); err != nil {
		t.Fatal(err)
	}
	bounds := delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}
	output := filepath.Join(directory, "physical.bil")
	if err := cropPhysicalGrid(context.Background(), runner, packed, output, bounds, 2, 2); err != nil {
		t.Fatal(err)
	}
	grid, err := readEHdr(output, bounds)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid.Values) != 4 || !float32NaN(grid.Values[0]) || grid.Values[1] != 100 || grid.Values[2] != 200 || !float32NaN(grid.Values[3]) {
		t.Fatalf("float NetCDF crop = %v; want [NaN 100 200 NaN]", grid.Values)
	}
}

func TestRealECCodesFloatDecodePreservesEqualCumulativeRain(t *testing.T) {
	for _, command := range []string{"gdal_translate", "grib_get", "grib_set", "grib_ls", "grib_to_netcdf"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not on PATH", command)
		}
	}
	directory := t.TempDir()
	runner := ExecRunner{}
	ctx := context.Background()
	makeGRIB := func(name, values string, endStep int) string {
		t.Helper()
		ascii := filepath.Join(directory, name+".asc")
		payload := "ncols 3\nnrows 2\nxllcorner 0\nyllcorner 0\ncellsize 1\nNODATA_value -9999\n" + values + "\n" + values + "\n"
		if err := os.WriteFile(ascii, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(directory, name+"-base.grib2")
		if _, err := runner.Run(ctx, "gdal_translate", "-q", "-of", "GRIB", "-a_srs", "EPSG:4326", ascii, base); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(directory, name+".grib2")
		settings := fmt.Sprintf("shortName=tp,stepType=accum,packingType=grid_ccsds,stepUnits=h,startStep=0,endStep=%d", endStep)
		if _, err := runner.Run(ctx, "grib_set", "-r", "-s", settings, base, output); err != nil {
			t.Fatal(err)
		}
		return output
	}

	// The first source cell has the same cumulative value in both messages,
	// while the rest of the field range changes sharply. Per-message NC_SHORT
	// conversion used to invent a negative delta at such cells.
	firstPath := makeGRIB("first", "0.99609375 0 10", 1)
	secondPath := makeGRIB("second", "0.99609375 0 100", 2)
	firstSource, err := runner.Run(ctx, "grib_get", "-p", "min,max,units", firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := runner.Run(ctx, "grib_get", "-p", "min,max,units", secondPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 0, East: 3, South: 0, North: 2})
	if err != nil {
		t.Fatal(err)
	}
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	decode := func(path string) Frame {
		t.Helper()
		frame, err := decoder.Decode(ctx, DecodeRequest{Path: path, Field: Rain, Run: run, NativeResolutionKM: 7, CellDegrees: 1})
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}
	first, second := decode(firstPath), decode(secondPath)
	if len(first.Grid.Values) != 6 || len(second.Grid.Values) != 6 {
		t.Fatalf("unexpected decoded grids: %v and %v", first.Grid.Values, second.Grid.Values)
	}
	// Locate the source cell that began at 0.99609375; orientation details of
	// the generated GRIB are deliberately irrelevant to this boundary test.
	target := -1
	for index, value := range first.Grid.Values {
		if math.Abs(float64(value)-0.99609375) <= first.PackingError+.001 {
			target = index
			break
		}
	}
	if target < 0 {
		t.Fatalf("equal cumulative source cell not found in %v; GRIB sources %q and %q", first.Grid.Values, firstSource, secondSource)
	}
	if delta := math.Abs(float64(second.Grid.Values[target] - first.Grid.Values[target])); delta > first.PackingError+second.PackingError+1e-6 {
		t.Fatalf("equal cumulative rain drifted beyond source precision: delta=%g errors=%g+%g; %v -> %v", delta, first.PackingError, second.PackingError, first.Grid.Values, second.Grid.Values)
	}
	normalized, err := NormalizeSeries([]Frame{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || !finite32(normalized[1].Grid.Values[target]) || math.Abs(float64(normalized[1].Grid.Values[target])) > 1e-5 {
		t.Fatalf("equal cumulative rain became an invalid/non-zero interval: %v", normalized)
	}
}

func TestCoverageDiagnosticsExposeFieldHolesAndFinalLandSupport(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	mask := FloatGrid{Spec: spec, Values: []float32{1, 1}}
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	series := Series{Rain: []Frame{
		{Field: Rain, Start: run, End: run.Add(time.Hour), Semantics: IntervalAccumulation, Units: "mm", NativeResolutionKM: 7, Grid: FloatGrid{Spec: spec, Values: []float32{0, 0}}},
		{Field: Rain, Start: run.Add(time.Hour), End: run.Add(2 * time.Hour), Semantics: IntervalAccumulation, Units: "mm", NativeResolutionKM: 7, Grid: FloatGrid{Spec: spec, Values: []float32{0, float32(math.NaN())}}},
	}}
	fields, err := MeasureFieldCoverage(series, mask)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Field != Rain || fields[0].Frames != 2 || fields[0].WorstFrame.Finite != 1 || fields[0].WorstFrame.Support != 2 || fields[0].Fraction() != .5 || fields[0].BlockFraction() != .5 {
		t.Fatalf("field coverage did not expose the missing sample: %+v", fields)
	}
	metrics := []weather.DailyMetrics{{Valid: true}, {}}
	forecast, err := MeasureForecastCoverage(spec, metrics, mask)
	if err != nil {
		t.Fatal(err)
	}
	if forecast.Global.Finite != 1 || forecast.Global.Support != 2 || forecast.Fraction() != .5 || forecast.BlockFraction() != .5 {
		t.Fatalf("forecast land coverage = %+v", forecast)
	}
}

func TestCoverageDiagnosticsRejectLocalizedHoleAndUseImmutableDomain(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 64, South: 0, North: 16}, Rows: 16, Cols: 64}
	land := FloatGrid{Spec: spec, Values: make([]float32, 16*64)}
	for index := range land.Values {
		land.Values[index] = 1
	}
	support, err := SupportWithinBounds(land, delivery.GridBounds{West: 0, East: 32, South: 0, North: 16})
	if err != nil {
		t.Fatal(err)
	}
	values := make([]float32, len(land.Values))
	for index := range values {
		values[index] = 1
	}
	// One missing supported cell must remain visible in both global and spatial
	// diagnostics. The unsupported eastern half cannot hide it.
	values[0] = float32(math.NaN())
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	series := Series{Rain: {{Field: Rain, Start: run, End: run.Add(time.Hour), Semantics: IntervalAccumulation, Units: "mm", NativeResolutionKM: 7, Grid: FloatGrid{Spec: spec, Values: values}}}}
	fields, err := MeasureFieldCoverage(series, support)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].WorstFrame.Finite != 511 || fields[0].WorstFrame.Support != 512 || fields[0].WorstBlock.Finite != 255 || fields[0].WorstBlock.Support != 256 {
		t.Fatalf("localized hole was not isolated: %+v", fields[0])
	}
}

type fakeRunner struct {
	t         *testing.T
	sawCrop   bool
	sawRaster bool
	sawFloat  bool
}

func (runner *fakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	switch command {
	case "grib_ls":
		return []byte(`{"messages":[{"shortName":{"value":"2t"},"stepType":{"value":"instant"},"startStep":{"value":"1"},"endStep":{"value":"1"},"units":{"value":"K"},"packingError":{"value":"0.01"},"binaryScaleFactor":{"value":"-6"},"decimalScaleFactor":{"value":"0"}}]}`), nil
	case "grib_to_netcdf":
		runner.sawFloat = strings.Contains(strings.Join(args, " "), "-D NC_FLOAT")
		return nil, nil
	case "gdalwarp", "gdal_translate":
		joined := strings.Join(args, " ")
		runner.sawCrop = command == "gdal_translate" && !strings.Contains(joined, "-unscale") && strings.Contains(joined, "-projwin 150 -34 152 -35") && strings.Contains(joined, "-outsize 2 1")
		runner.sawRaster = strings.Contains(joined, "-r bilinear") && strings.Contains(joined, "/vsicurl/https://example.test/climate.tif")
		output := args[len(args)-1]
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint32(payload[0:4], math.Float32bits(280))
		binary.LittleEndian.PutUint32(payload[4:8], math.Float32bits(-9999))
		if err := os.WriteFile(output, payload, 0o600); err != nil {
			runner.t.Fatal(err)
		}
		header := "BYTEORDER I\nLAYOUT BIL\nNROWS 1\nNCOLS 2\nNBANDS 1\nNBITS 32\nNODATA -9999\n"
		if err := os.WriteFile(strings.TrimSuffix(output, filepath.Ext(output))+".hdr", []byte(header), 0o600); err != nil {
			runner.t.Fatal(err)
		}
		return nil, nil
	default:
		runner.t.Fatalf("unexpected command %q", command)
		return nil, nil
	}
}

func TestNormalizeSeriesDifferencesCumulativeIFSFields(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Rain, run, run, CumulativeAccumulation, 0),
		frameAt(Rain, run, run.Add(6*time.Hour), CumulativeAccumulation, 2),
		frameAt(Rain, run, run.Add(12*time.Hour), CumulativeAccumulation, 5),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || normalized[0].Grid.Values[0] != 2 || normalized[1].Grid.Values[0] != 3 || !normalized[1].Start.Equal(run.Add(6*time.Hour)) {
		t.Fatalf("unexpected interval accumulations: %+v", normalized)
	}
}

func TestDWDRunAverageSolarBecomesNonOverlappingEnergy(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		rawSolarFluxFrame(SolarDirect, run, run, float32(math.NaN())),
		rawSolarFluxFrame(SolarDirect, run, run.Add(time.Hour), 100),
		rawSolarFluxFrame(SolarDirect, run, run.Add(2*time.Hour), 150),
		rawSolarFluxFrame(SolarDirect, run, run.Add(3*time.Hour), 200),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{.36, .72, 1.08}
	if len(normalized) != len(want) {
		t.Fatalf("got %d intervals; want %d", len(normalized), len(want))
	}
	for index, frame := range normalized {
		if frame.Semantics != IntervalAccumulation || frame.End.Sub(frame.Start) != time.Hour || math.Abs(float64(frame.Grid.Values[0])-want[index]) > 1e-5 {
			t.Fatalf("interval %d = %+v value %.6f; want %.6f MJ/m2", index, frame, frame.Grid.Values[0], want[index])
		}
	}
}

func TestDWDRunAverageSolarSupportsNativeThreeHourIntervals(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		rawSolarFluxFrame(SolarDirect, run, run, 0),
		rawSolarFluxFrame(SolarDirect, run, run.Add(3*time.Hour), 200),
		rawSolarFluxFrame(SolarDirect, run, run.Add(6*time.Hour), 300),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || normalized[0].End.Sub(normalized[0].Start) != 3*time.Hour || normalized[1].End.Sub(normalized[1].Start) != 3*time.Hour || math.Abs(float64(normalized[0].Grid.Values[0])-2.16) > 1e-5 || math.Abs(float64(normalized[1].Grid.Values[0])-4.32) > 1e-5 {
		t.Fatalf("unexpected native intervals: %+v", normalized)
	}
}

func TestDWDRunAverageSolarMalformedSequencesFailClosed(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		frames []Frame
	}{
		{"reset", []Frame{frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 1), frameAt(SolarDirect, run.Add(time.Hour), run.Add(2*time.Hour), CumulativeAccumulation, 2)}},
		{"duplicate end", []Frame{frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 1), frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 2)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeSeries(test.frames); err == nil {
				t.Fatal("malformed cumulative sequence was accepted")
			}
		})
	}

	values := []Frame{
		frameAt(SolarDirect, run, run, CumulativeAccumulation, 0),
		frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 1),
		frameAt(SolarDirect, run, run.Add(2*time.Hour), CumulativeAccumulation, float32(math.NaN())),
		frameAt(SolarDirect, run, run.Add(3*time.Hour), CumulativeAccumulation, .5),
	}
	normalized, err := NormalizeSeries(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || !float32NaN(normalized[1].Grid.Values[0]) || !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("non-finite cumulative values did not fail closed per cell: %+v", normalized)
	}

	tolerated := []Frame{
		frameAt(SolarDirect, run, run, CumulativeAccumulation, 0),
		frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 1),
		frameAt(SolarDirect, run, run.Add(2*time.Hour), CumulativeAccumulation, .98),
		frameAt(SolarDirect, run, run.Add(3*time.Hour), CumulativeAccumulation, .5),
	}
	for index := range tolerated {
		tolerated[index].PackingError = .011
	}
	normalized, err = NormalizeSeries(tolerated)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[1].Grid.Values[0] != 0 || !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("negative-delta tolerance was not applied safely: %+v", normalized)
	}
}

func TestDWDRunAverageSolarClampsAuditedAccumulatorArtifact(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		rawSolarFluxFrame(SolarDirect, run, run.Add(16*time.Hour), 130.818161),
		rawSolarFluxFrame(SolarDirect, run, run.Add(17*time.Hour), 122.973213),
		rawSolarFluxFrame(SolarDirect, run, run.Add(18*time.Hour), 115),
	}
	for index := range frames {
		frames[index].PackingError = .00390625
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || normalized[1].Grid.Values[0] != 0 || !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("audited solar artifact bound did not clamp small and reject large declines: %+v", normalized)
	}
}

func TestDWDRunAverageSolarDipRecoveryUsesHighWaterBaseline(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(SolarDirect, run, run.Add(time.Hour), CumulativeAccumulation, 4.148),
		frameAt(SolarDirect, run, run.Add(2*time.Hour), CumulativeAccumulation, 4.108),
		frameAt(SolarDirect, run, run.Add(3*time.Hour), CumulativeAccumulation, 4.148),
		frameAt(SolarDirect, run, run.Add(4*time.Hour), CumulativeAccumulation, 4.2),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 4 || normalized[1].Grid.Values[0] != 0 || normalized[2].Grid.Values[0] != 0 || math.Abs(float64(normalized[3].Grid.Values[0])-.052) > 1e-5 {
		t.Fatalf("solar dip recovery double counted cumulative energy: %+v", normalized)
	}
}

func TestDWDRunAverageSolarClampsAuditedHighWaterDeficit(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(SolarDirect, run, run.Add(17*time.Hour), CumulativeAccumulation, 4.472176075),
		frameAt(SolarDirect, run, run.Add(18*time.Hour), CumulativeAccumulation, 4.442001820),
		frameAt(SolarDirect, run, run.Add(19*time.Hour), CumulativeAccumulation, 4.422558784),
		frameAt(SolarDirect, run, run.Add(20*time.Hour), CumulativeAccumulation, 4.414489269),
		frameAt(SolarDirect, run, run.Add(21*time.Hour), CumulativeAccumulation, 4.407739639),
		frameAt(SolarDirect, run, run.Add(22*time.Hour), CumulativeAccumulation, 4.39),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	for interval := 1; interval < 5; interval++ {
		if normalized[interval].Grid.Values[0] != 0 {
			t.Fatalf("audited Iceland interval %d = %v; want zero", interval, normalized[interval].Grid.Values[0])
		}
	}
	if !float32NaN(normalized[5].Grid.Values[0]) {
		t.Fatalf("decline beyond audited ceiling = %v; want NaN", normalized[5].Grid.Values[0])
	}
}

func TestGenericSolarDoesNotUseDWDSolarArtifactTolerance(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Solar, run, run.Add(time.Hour), CumulativeAccumulation, 1),
		frameAt(Solar, run, run.Add(2*time.Hour), CumulativeAccumulation, .98),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || !float32NaN(normalized[1].Grid.Values[0]) {
		t.Fatalf("generic/IFS solar incorrectly used DWD artifact tolerance: %+v", normalized)
	}
}

func TestCumulativeIFSSolarExactQuantumOverridesInconsistentPackingError(t *testing.T) {
	run := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Solar, run, run.Add(96*time.Hour), CumulativeAccumulation, 10),
		frameAt(Solar, run, run.Add(102*time.Hour), CumulativeAccumulation, 9.98),
	}
	for index := range frames {
		frames[index].PackingError = .1
		frames[index].PackingQuantum = .004096
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || !float32NaN(normalized[1].Grid.Values[0]) {
		t.Fatalf("inconsistent error metadata widened IFS solar beyond one exact quantum: %+v", normalized)
	}
}

func TestDWDSolarRejectsPackingUncertaintyAboveAuditedCeiling(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)

	first := rawSolarFluxFrame(SolarDirect, run, run.Add(time.Hour), -1)
	first.PackingError = 100 // 0.36 MJ/m2 after flux integration.
	normalized, err := NormalizeSeries([]Frame{first})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || !float32NaN(normalized[0].Grid.Values[0]) {
		t.Fatalf("oversized first-interval packing uncertainty did not fail closed: %+v", normalized)
	}

	frames := []Frame{
		frameAt(SolarDiffuse, run, run.Add(time.Hour), CumulativeAccumulation, 1),
		frameAt(SolarDiffuse, run, run.Add(2*time.Hour), CumulativeAccumulation, .98),
	}
	frames[0].PackingError = .04
	frames[1].PackingError = .04
	normalized, err = NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || !float32NaN(normalized[1].Grid.Values[0]) {
		t.Fatalf("oversized cumulative packing uncertainty did not fail closed: %+v", normalized)
	}
}

func TestSolarNormalizationClampsFirstIntervalPackingWobble(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		flux    float32
		wantNaN bool
	}{
		{"negligible negative", -1, false},  // -0.0036 MJ/m2
		{"materially negative", -100, true}, // -0.36 MJ/m2
	} {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := NormalizeSeries([]Frame{rawSolarFluxFrame(SolarDiffuse, run, run.Add(time.Hour), test.flux)})
			if err != nil {
				t.Fatal(err)
			}
			if len(normalized) != 1 {
				t.Fatalf("got %d intervals; want 1", len(normalized))
			}
			got := normalized[0].Grid.Values[0]
			if test.wantNaN != float32NaN(got) || (!test.wantNaN && got != 0) {
				t.Fatalf("normalized first interval = %v; want zero=%t NaN=%t", got, !test.wantNaN, test.wantNaN)
			}
		})
	}
}

func TestOrdinaryAverageFieldsRemainIntervalMeans(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	semantics, _, _, err := InterpretMetadata(Temperature, run, GRIBMetadata{StepType: "avg", StartStep: 1, EndStep: 2})
	if err != nil {
		t.Fatal(err)
	}
	if semantics != IntervalMean {
		t.Fatalf("ordinary average semantics = %v; want IntervalMean", semantics)
	}
}

func TestDWDDirectAndDiffuseAreDifferencedSeparatelyThenAddedOnce(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	run := date.Add(10 * time.Hour)
	series := Series{}
	for hour := 0; hour <= 10; hour++ {
		end := run.Add(time.Duration(hour) * time.Hour)
		series[SolarDirect] = append(series[SolarDirect], rawSolarFluxFrame(SolarDirect, run, end, 400))
		series[SolarDiffuse] = append(series[SolarDiffuse], rawSolarFluxFrame(SolarDiffuse, run, end, 100))
		if hour == 0 {
			continue
		}
		start := end.Add(-time.Hour)
		series[Temperature] = append(series[Temperature], frameAt(Temperature, start, end, IntervalMean, 22))
		series[DewPoint] = append(series[DewPoint], frameAt(DewPoint, start, end, IntervalMean, 12))
		series[CloudCover] = append(series[CloudCover], frameAt(CloudCover, start, end, IntervalMean, 20))
		series[Rain] = append(series[Rain], frameAt(Rain, start, end, IntervalAccumulation, 0))
		series[WindU] = append(series[WindU], frameAt(WindU, start, end, IntervalMean, 5))
		series[WindV] = append(series[WindV], frameAt(WindV, start, end, IntervalMean, 0))
	}
	_, metrics, err := AggregateDay(date, []*time.Location{time.UTC}, series, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || !metrics[0].Valid || !metrics[0].SolarEnergyMJm2.Available || math.Abs(metrics[0].SolarEnergyMJm2.Value-18) > .001 {
		t.Fatalf("solar components were double counted or invalidated: %+v", metrics)
	}
	for hour := 1; hour <= 10; hour++ {
		end := run.Add(time.Duration(hour) * time.Hour)
		series[Solar] = append(series[Solar], frameAt(Solar, end.Add(-time.Hour), end, IntervalAccumulation, 1))
	}
	if _, _, err := AggregateDay(date, []*time.Location{time.UTC}, series, 7); err == nil {
		t.Fatal("aggregate solar was accepted alongside direct and diffuse components")
	}
	delete(series, Solar)
	delete(series, SolarDiffuse)
	if _, _, err := AggregateDay(date, []*time.Location{time.UTC}, series, 7); err == nil {
		t.Fatal("a lone DWD solar component was accepted")
	}
}

func TestAggregateDayNeverPublishesPartialSolarSum(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	series := Series{}
	for hour := 0; hour < 10; hour++ {
		start, end := date.Add(time.Duration(10+hour)*time.Hour), date.Add(time.Duration(11+hour)*time.Hour)
		add := func(field Field, semantics Semantics, values ...float32) {
			frame := frameAt(field, start, end, semantics, values[0])
			frame.Grid.Spec.Cols = uint32(len(values))
			frame.Grid.Spec.Bounds.East = float64(len(values))
			frame.Grid.Values = append([]float32(nil), values...)
			series[field] = append(series[field], frame)
		}
		add(Temperature, IntervalMean, 22, 22)
		add(DewPoint, IntervalMean, 12, 12)
		add(CloudCover, IntervalMean, 20, 20)
		add(Rain, IntervalAccumulation, 0, 0)
		add(WindU, IntervalMean, 5, 5)
		add(WindV, IntervalMean, 0, 0)
		direct := []float32{1, 1}
		if hour == 4 {
			direct[1] = float32(math.NaN())
		}
		add(SolarDirect, IntervalAccumulation, direct...)
		add(SolarDiffuse, IntervalAccumulation, .5, .5)
	}
	_, metrics, err := AggregateDay(date, []*time.Location{time.UTC, time.UTC}, series, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 2 || !metrics[0].Valid || !metrics[1].Valid {
		t.Fatalf("optional solar hole invalidated final weather metrics: %+v", metrics)
	}
	if !metrics[0].SolarEnergyMJm2.Available || math.Abs(metrics[0].SolarEnergyMJm2.Value-15) > 1e-9 {
		t.Fatalf("complete solar cell was not retained: %+v", metrics[0].SolarEnergyMJm2)
	}
	if metrics[1].SolarEnergyMJm2.Available || metrics[1].SolarEnergyMJm2.Value != 0 {
		t.Fatalf("partial solar sum was published: %+v", metrics[1].SolarEnergyMJm2)
	}
}

func TestAggregateDayRejectsSolarOverlapThatCouldHideGap(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	base := func() Series {
		series := Series{}
		for hour := 10; hour < 20; hour++ {
			start, end := date.Add(time.Duration(hour)*time.Hour), date.Add(time.Duration(hour+1)*time.Hour)
			series[Temperature] = append(series[Temperature], frameAt(Temperature, start, end, IntervalMean, 22))
			series[DewPoint] = append(series[DewPoint], frameAt(DewPoint, start, end, IntervalMean, 12))
			series[CloudCover] = append(series[CloudCover], frameAt(CloudCover, start, end, IntervalMean, 20))
			series[Rain] = append(series[Rain], frameAt(Rain, start, end, IntervalAccumulation, 0))
			series[WindU] = append(series[WindU], frameAt(WindU, start, end, IntervalMean, 5))
			series[WindV] = append(series[WindV], frameAt(WindV, start, end, IntervalMean, 0))
		}
		return series
	}
	intervals := [][2]int{{10, 15}, {14, 18}, {19, 20}} // Ten summed hours, but 14–15 overlaps and 18–19 is absent.
	for _, test := range []struct {
		name       string
		components bool
	}{{"aggregate", false}, {"paired components", true}} {
		t.Run(test.name, func(t *testing.T) {
			series := base()
			for _, interval := range intervals {
				start, end := date.Add(time.Duration(interval[0])*time.Hour), date.Add(time.Duration(interval[1])*time.Hour)
				if test.components {
					series[SolarDirect] = append(series[SolarDirect], frameAt(SolarDirect, start, end, IntervalAccumulation, 1))
					series[SolarDiffuse] = append(series[SolarDiffuse], frameAt(SolarDiffuse, start, end, IntervalAccumulation, .5))
				} else {
					series[Solar] = append(series[Solar], frameAt(Solar, start, end, IntervalAccumulation, 1.5))
				}
			}
			if _, _, err := AggregateDay(date, []*time.Location{time.UTC}, series, 7); err == nil || !strings.Contains(err.Error(), "solar intervals overlap") {
				t.Fatalf("overlap-plus-gap solar was accepted: %v", err)
			}
		})
	}
}

func TestNormalizeSeriesDifferencesICONAverageSinceModelStartSolar(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	metadata := []GRIBMetadata{
		{ShortName: "ASWDIR_S", StepType: "avg", StartStep: 0, EndStep: 1, Units: "W m-2"},
		{ShortName: "ASWDIR_S", StepType: "avg", StartStep: 0, EndStep: 2, Units: "W m-2"},
	}
	values := []float32{100, 150}
	frames := make([]Frame, len(metadata))
	for index, message := range metadata {
		semantics, start, end, err := InterpretMetadata(Solar, run, message)
		if err != nil {
			t.Fatal(err)
		}
		frames[index] = frameAt(Solar, start, end, semantics, values[index])
		frames[index].Units = message.Units
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 {
		t.Fatalf("normalized solar intervals = %d; want 2", len(normalized))
	}
	// 0..1h at 100 W/m2 is 0.36 MJ/m2 cumulative. 0..2h at an
	// average 150 W/m2 is 1.08 MJ/m2 cumulative. The true second-hour
	// interval is therefore 0.72 MJ/m2, equivalent to 200 W/m2.
	if first := float64(normalized[0].Grid.Values[0]); math.Abs(first-.36) > .0001 {
		t.Fatalf("ICON first solar interval was not integrated: %+v", normalized[0])
	}
	if got := float64(normalized[1].Grid.Values[0]); math.Abs(got-.72) > .0001 ||
		!normalized[1].Start.Equal(run.Add(time.Hour)) || !normalized[1].End.Equal(run.Add(2*time.Hour)) ||
		normalized[1].Semantics != IntervalAccumulation {
		t.Fatalf("ICON solar interval was not differenced: %+v", normalized[1])
	}
}

func TestMetadataKeepsBoundedSolarAverageAsOneInterval(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	semantics, start, end, err := InterpretMetadata(Solar, run, GRIBMetadata{
		ShortName: "solar", StepType: "avg", StartStep: 1, EndStep: 2, Units: "W m-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if semantics != IntervalMean || !start.Equal(run.Add(time.Hour)) || !end.Equal(run.Add(2*time.Hour)) {
		t.Fatalf("bounded solar average was misinterpreted: %v %s..%s", semantics, start, end)
	}
}

func TestMetadataAcceptsGRIBLSArrayAndKeepsZeroHourAccumulationCumulative(t *testing.T) {
	data := []byte(`[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":0,"units":"m","packingError":0.000001,"binaryScaleFactor":-19,"decimalScaleFactor":0}]`)
	metadata, err := ParseECCodesJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	semantics, start, end, err := InterpretMetadata(Rain, time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), metadata)
	if err != nil {
		t.Fatal(err)
	}
	if semantics != CumulativeAccumulation || !start.Equal(end) || metadata.PackingQuantum != math.Ldexp(1, -19) {
		t.Fatalf("zero-hour cumulative baseline was misinterpreted: %v %s..%s", semantics, start, end)
	}
}

func TestMetadataConvertsExplicitECCodesStepUnitsToHours(t *testing.T) {
	tests := []struct {
		start, end string
		wantStart  float64
		wantEnd    float64
	}{
		{start: "0m", end: "90m", wantStart: 0, wantEnd: 1.5},
		{start: "3600s", end: "7200s", wantStart: 1, wantEnd: 2},
		{start: "1h", end: "3h", wantStart: 1, wantEnd: 3},
		{start: "1d", end: "2d", wantStart: 24, wantEnd: 48},
	}
	for _, test := range tests {
		data := []byte(fmt.Sprintf(`[{"shortName":"2t","stepType":"instant","startStep":%q,"endStep":%q,"units":"K","packingError":0.01,"binaryScaleFactor":-6,"decimalScaleFactor":0}]`, test.start, test.end))
		metadata, err := ParseECCodesJSON(data)
		if err != nil {
			t.Fatalf("steps %s..%s: %v", test.start, test.end, err)
		}
		if metadata.StartStep != test.wantStart || metadata.EndStep != test.wantEnd {
			t.Fatalf("steps %s..%s = %.3f..%.3f hours; want %.3f..%.3f", test.start, test.end, metadata.StartStep, metadata.EndStep, test.wantStart, test.wantEnd)
		}
	}
}

func TestMetadataRejectsUnknownOrCompoundStepUnits(t *testing.T) {
	for _, step := range []string{"1w", "1h30m", "NaNh"} {
		data := []byte(fmt.Sprintf(`[{"shortName":"2t","stepType":"instant","startStep":"0m","endStep":%q,"units":"K","packingError":0.01,"binaryScaleFactor":-6,"decimalScaleFactor":0}]`, step))
		if _, err := ParseECCodesJSON(data); err == nil {
			t.Fatalf("invalid step %q was accepted", step)
		}
	}
}

func TestNormalizeSeriesDropsOnlyLeadingZeroDurationMaximum(t *testing.T) {
	run := time.Date(2026, 7, 19, 6, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Gust, run, run, IntervalMaximum, 0),
		frameAt(Gust, run, run.Add(time.Hour), IntervalMaximum, 32),
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || !normalized[0].Start.Equal(run) || !normalized[0].End.Equal(run.Add(time.Hour)) || normalized[0].Grid.Values[0] != 32 {
		t.Fatalf("zero-duration gust baseline was not discarded exactly: %+v", normalized)
	}

	malformed := []Frame{
		frameAt(Gust, run, run.Add(time.Hour), IntervalMaximum, 32),
		frameAt(Gust, run.Add(time.Hour), run.Add(time.Hour), IntervalMaximum, 0),
	}
	if _, err := NormalizeSeries(malformed); err == nil {
		t.Fatal("non-leading zero-duration maximum was accepted")
	}
	for name, malformed := range map[string][]Frame{
		"standalone baseline": {
			frameAt(Gust, run, run, IntervalMaximum, 0),
		},
		"disconnected baseline": {
			frameAt(Gust, run, run, IntervalMaximum, 0),
			frameAt(Gust, run.Add(time.Hour), run.Add(2*time.Hour), IntervalMaximum, 32),
		},
	} {
		if _, err := NormalizeSeries(malformed); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := NormalizeSeries([]Frame{frameAt(Temperature, run, run, IntervalMaximum, 20)}); err == nil {
		t.Fatal("zero-duration maximum for a non-gust field was accepted")
	}
}

func TestMetadataRejectsInvalidPackingScales(t *testing.T) {
	tests := map[string]string{
		"missing binary scale":       `[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":1,"units":"m","packingError":0.1,"decimalScaleFactor":0}]`,
		"missing decimal scale":      `[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":1,"units":"m","packingError":0.1,"binaryScaleFactor":-8}]`,
		"fractional binary scale":    `[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":1,"units":"m","packingError":0.1,"binaryScaleFactor":-8.5,"decimalScaleFactor":0}]`,
		"out of range binary scale":  `[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":1,"units":"m","packingError":0.1,"binaryScaleFactor":1024,"decimalScaleFactor":0}]`,
		"out of range decimal scale": `[{"shortName":"tp","stepType":"accum","startStep":0,"endStep":1,"units":"m","packingError":0.1,"binaryScaleFactor":-8,"decimalScaleFactor":309}]`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseECCodesJSON([]byte(data)); err == nil {
				t.Fatal("invalid packing scale was accepted")
			}
		})
	}
}

func TestCumulativeRainUsesDeclaredPackingPrecision(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Rain, run, run, CumulativeAccumulation, 0),
		frameAt(Rain, run, run.Add(time.Hour), CumulativeAccumulation, .99609375),
		frameAt(Rain, run, run.Add(2*time.Hour), CumulativeAccumulation, .9955),
		frameAt(Rain, run, run.Add(3*time.Hour), CumulativeAccumulation, .98),
	}
	for index := range frames {
		frames[index].PackingError = .000976562
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || normalized[1].Grid.Values[0] != 0 || !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("packing-derived cumulative tolerance failed: %+v", normalized)
	}
}

func TestCumulativeIFSFieldsUseOneCoarserQuantumAcrossPackingScaleChange(t *testing.T) {
	run := time.Date(2026, 7, 17, 18, 0, 0, 0, time.UTC)
	base := []Frame{
		frameAt(Rain, run, run.Add(264*time.Hour), CumulativeAccumulation, 42.28125),
		frameAt(Rain, run, run.Add(270*time.Hour), CumulativeAccumulation, 42.2734375),
		frameAt(Rain, run, run.Add(276*time.Hour), CumulativeAccumulation, 42.265625),
	}
	base[0].PackingError = .00390625
	base[1].PackingError = .00390625
	base[2].PackingError = .0078125
	base[0].PackingQuantum = .0078125
	base[1].PackingQuantum = .0078125
	base[2].PackingQuantum = .015625
	normalized, err := NormalizeSeries(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || normalized[1].Grid.Values[0] != 0 || normalized[2].Grid.Values[0] != 0 {
		t.Fatalf("packing-scale transition was not bounded to one coarser quantum: %+v", normalized)
	}

	beyond := append([]Frame(nil), base...)
	representationBound := float32RepresentationError(base[1].Grid.Values[0]) + float32RepresentationError(base[2].Grid.Values[0])
	beyond[2].Grid.Values = []float32{base[2].Grid.Values[0] - float32(4*representationBound)}
	normalized, err = NormalizeSeries(beyond)
	if err != nil {
		t.Fatal(err)
	}
	if !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("rain decline beyond the coarser quantum and Float32 representation bound was accepted: %+v", normalized)
	}

	solar := append([]Frame(nil), base...)
	for index := range solar {
		solar[index].Field = Solar
		solar[index].Units = "MJ/m2"
	}
	normalized, err = NormalizeSeries(solar)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[1].Grid.Values[0] != 0 || normalized[2].Grid.Values[0] != 0 {
		t.Fatalf("IFS solar packing-scale transition was not bounded to one coarser quantum: %+v", normalized)
	}
	solarBeyond := append([]Frame(nil), solar...)
	solarBeyond[2].Grid.Values = []float32{solar[2].Grid.Values[0] - float32(4*representationBound)}
	normalized, err = NormalizeSeries(solarBeyond)
	if err != nil {
		t.Fatal(err)
	}
	if !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("IFS solar decline beyond the coarser quantum and Float32 representation bound was accepted: %+v", normalized)
	}
}

func TestCumulativeRainUsesExactScaleQuantumWhenPackingErrorIsTruncated(t *testing.T) {
	run := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	frames := []Frame{
		frameAt(Rain, run, run.Add(108*time.Hour), CumulativeAccumulation, 1.0078125),
		frameAt(Rain, run, run.Add(111*time.Hour), CumulativeAccumulation, 1.00390625),
		frameAt(Rain, run, run.Add(114*time.Hour), CumulativeAccumulation, 1.003905),
	}
	for index := range frames {
		// ecCodes JSON renders the half-quantum with eight significant decimal
		// digits, while binaryScaleFactor=-8 defines the exact quantum.
		frames[index].PackingError = .00195312
		frames[index].PackingQuantum = math.Ldexp(1, -8)
	}
	normalized, err := NormalizeSeries(frames)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[1].Grid.Values[0] != 0 || !float32NaN(normalized[2].Grid.Values[0]) {
		t.Fatalf("exact scale-derived quantum boundary failed: %+v", normalized)
	}
}

func TestAggregateDayUsesLocalWindowAndDurationWeights(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	location, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	localStart := time.Date(2026, 7, 18, 10, 0, 0, 0, location).UTC()
	series := Series{}
	for hour := 0; hour < 10; hour++ {
		start := localStart.Add(time.Duration(hour) * time.Hour)
		end := start.Add(time.Hour)
		temperature := float32(20)
		if hour == 0 {
			temperature = 10
		}
		series[Temperature] = append(series[Temperature], frameAt(Temperature, start, end, IntervalMean, temperature))
		series[DewPoint] = append(series[DewPoint], frameAt(DewPoint, start, end, IntervalMean, 10))
		series[CloudCover] = append(series[CloudCover], frameAt(CloudCover, start, end, IntervalMean, 25))
		rain := float32(0)
		if hour == 0 {
			rain = 1
		}
		series[Rain] = append(series[Rain], frameAt(Rain, start, end, IntervalAccumulation, rain))
		series[WindU] = append(series[WindU], frameAt(WindU, start, end, IntervalMean, 3))
		series[WindV] = append(series[WindV], frameAt(WindV, start, end, IntervalMean, 4))
		series[Solar] = append(series[Solar], frameAt(Solar, start, end, IntervalAccumulation, 1.5))
		series[Gust] = append(series[Gust], frameAt(Gust, start, end, IntervalMaximum, 30))
	}
	spec, metrics, err := AggregateDay(date, []*time.Location{location}, series, 7)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Rows != 1 || len(metrics) != 1 || !metrics[0].Valid {
		t.Fatalf("unexpected aggregation shape: %+v %+v", spec, metrics)
	}
	metric := metrics[0]
	if math.Abs(metric.ComfortableTempC-19) > .001 || metric.MinTempC != 10 || metric.MaxTempC != 20 {
		t.Fatalf("temperature was not duration weighted: %+v", metric)
	}
	if math.Abs(metric.RainMM-1) > .001 || !metric.WetTimeFraction.Available || math.Abs(metric.WetTimeFraction.Value-.1) > .001 || math.Abs(metric.WindSpeedKPH-5) > .001 || math.Abs(metric.SolarEnergyMJm2.Value-15) > .001 || !metric.GustKPH.Available || metric.GustKPH.Value != 30 {
		t.Fatalf("interval fields were not aggregated correctly: %+v", metric)
	}
}

func TestAggregateDayDoesNotShiftWesternCalendarDate(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	west := time.FixedZone("UTC-1", -60*60)
	localStart := time.Date(2026, 7, 18, 10, 0, 0, 0, west).UTC()
	series := Series{}
	for hour := 0; hour < 10; hour++ {
		start, end := localStart.Add(time.Duration(hour)*time.Hour), localStart.Add(time.Duration(hour+1)*time.Hour)
		series[Temperature] = append(series[Temperature], frameAt(Temperature, start, end, IntervalMean, 22))
		series[DewPoint] = append(series[DewPoint], frameAt(DewPoint, start, end, IntervalMean, 12))
		series[CloudCover] = append(series[CloudCover], frameAt(CloudCover, start, end, IntervalMean, 20))
		series[Rain] = append(series[Rain], frameAt(Rain, start, end, IntervalAccumulation, 0))
		series[WindU] = append(series[WindU], frameAt(WindU, start, end, IntervalMean, 5))
		series[WindV] = append(series[WindV], frameAt(WindV, start, end, IntervalMean, 0))
	}
	_, metrics, err := AggregateDay(date, []*time.Location{west}, series, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || !metrics[0].Valid {
		t.Fatal("UTC-1 cell was incorrectly aggregated as the previous day")
	}
}

func TestLocalDaytimeUTCWindowCoversWorldwideCivilTimeExtremes(t *testing.T) {
	date := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	plus14 := time.FixedZone("UTC+14", 14*60*60)
	minus12 := time.FixedZone("UTC-12", -12*60*60)

	start, end := LocalDaytimeUTCWindow(date, plus14)
	if !start.Equal(time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC+14 window = %s..%s", start, end)
	}
	start, end = LocalDaytimeUTCWindow(date, minus12)
	if !start.Equal(time.Date(2026, 7, 18, 22, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC-12 window = %s..%s", start, end)
	}
}

func TestLocalDaytimeUTCWindowUsesCivilDSTOnTheLabelledDate(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	winterStart, winterEnd := LocalDaytimeUTCWindow(time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC), london)
	summerStart, summerEnd := LocalDaytimeUTCWindow(time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), london)
	if winterStart.Hour() != 10 || winterEnd.Hour() != 20 || summerStart.Hour() != 9 || summerEnd.Hour() != 19 {
		t.Fatalf("London DST windows = %s..%s then %s..%s", winterStart, winterEnd, summerStart, summerEnd)
	}
}

func TestSixHourlyPointAndCumulativeFramesCoverBothWorldwideDayExtremes(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	run := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	series := Series{}
	for step := 6; step <= 48; step += 6 {
		end := run.Add(time.Duration(step) * time.Hour)
		for field, value := range map[Field]float32{
			Temperature: 22,
			DewPoint:    12,
			CloudCover:  20,
			WindU:       5,
			WindV:       0,
		} {
			series[field] = append(series[field], frameAt(field, run, end, Instant, value))
		}
		series[Rain] = append(series[Rain], frameAt(Rain, run, end, CumulativeAccumulation, float32(step)))
	}

	for _, location := range []*time.Location{
		time.FixedZone("UTC+14", 14*60*60),
		time.FixedZone("UTC-12", -12*60*60),
	} {
		_, metrics, err := AggregateDay(date, []*time.Location{location}, series, 28)
		if err != nil {
			t.Fatalf("%s aggregation: %v", location, err)
		}
		if len(metrics) != 1 || !metrics[0].Valid || math.Abs(metrics[0].RainMM-10) > 1e-9 {
			t.Fatalf("%s worldwide boundary metric = %+v", location, metrics)
		}
	}
}

func TestDWDIntervalMaximumGustShortNameIsAccepted(t *testing.T) {
	if !shortNameMatches(Gust, "max_i10fg") {
		t.Fatal("live ICON-EU maximum gust short name was rejected")
	}
}

func TestDWDSolarComponentsKeepDistinctShortNames(t *testing.T) {
	if !shortNameMatches(SolarDirect, "ASWDIR_S") || shortNameMatches(SolarDirect, "ASWDIFD_S") {
		t.Fatal("direct shortwave field identity is not strict")
	}
	if !shortNameMatches(SolarDiffuse, "ASWDIFD_S") || shortNameMatches(SolarDiffuse, "ASWDIR_S") {
		t.Fatal("diffuse shortwave field identity is not strict")
	}
}

func frameAt(field Field, start, end time.Time, semantics Semantics, value float32) Frame {
	return Frame{
		Field: field, Start: start, End: end, Semantics: semantics, Units: canonicalUnit(field), NativeResolutionKM: 7,
		Grid: FloatGrid{Spec: delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, Values: []float32{value}},
	}
}

func canonicalUnit(field Field) string {
	switch field {
	case Temperature, DewPoint:
		return "C"
	case CloudCover:
		return "%"
	case Rain:
		return "mm"
	case WindU, WindV, Gust:
		return "km/h"
	case Solar, SolarDirect, SolarDiffuse:
		return "MJ/m2"
	}
	return ""
}

func rawSolarFluxFrame(field Field, start, end time.Time, value float32) Frame {
	return Frame{
		Field: field, Start: start, End: end, Semantics: CumulativeMean, Units: "W m-2", PackingError: 2, NativeResolutionKM: 7,
		Grid: FloatGrid{Spec: delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}, Values: []float32{value}},
	}
}
