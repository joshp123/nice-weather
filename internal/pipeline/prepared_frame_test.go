package pipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nice-weather-finder/internal/delivery"
)

func TestPreparedFrameConvertsOnceAndReadsWrappedWindowsWithHalo(t *testing.T) {
	runner := &preparedFakeRunner{t: t}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: "temperature.grib2", Field: Temperature,
		Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 28, CellDegrees: .5, ScratchDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := prepared.directory
	t.Cleanup(func() { _ = prepared.Close() })

	metadata, err := prepared.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Units != "K" || metadata.Grid.Rows != 360 || metadata.Grid.Cols != 720 || metadata.Field != Temperature {
		t.Fatalf("unexpected prepared metadata: %+v", metadata)
	}
	window, err := prepared.ReadWindow(context.Background(), GridWindow{Row: 1, Column: 718, Rows: 2, Cols: 2})
	if err != nil {
		t.Fatal(err)
	}
	if window.OriginRow != 0 || window.OriginColumn != 717 || window.Rows != 4 || window.Cols != 4 {
		t.Fatalf("unexpected window geometry: %+v", window)
	}
	wantColumns := []int{717, 718, 719, 0}
	for row := 0; row < 4; row++ {
		for column, sourceColumn := range wantColumns {
			want := float32(row*1000 + sourceColumn)
			got := window.Values[row*4+column]
			if math.Abs(float64(got-want)) > 1e-4 {
				t.Fatalf("window[%d,%d] = %g; want %g", row, column, got, want)
			}
		}
	}
	if _, err := prepared.ReadWindow(context.Background(), GridWindow{Row: 10, Column: 20, Rows: 2, Cols: 3}); err != nil {
		t.Fatal(err)
	}
	if runner.count("grib_ls") != 1 || runner.count("grib_to_netcdf") != 1 || runner.count("gdal_translate") != 1 {
		t.Fatalf("repeated windows reran preparation commands: %+v", runner.counts)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared directory remained after Close: %v", err)
	}
	if _, err := prepared.ReadWindow(context.Background(), GridWindow{Rows: 1, Cols: 1}); err == nil {
		t.Fatal("closed prepared frame allowed a read")
	}
}

func TestPreparedFrameLatitudeHaloAndNoDataRemainNaN(t *testing.T) {
	runner := &preparedFakeRunner{t: t, noDataRow: 0, noDataColumn: 1}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 0, East: 4, South: 0, North: 2})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: "temperature.grib2", Field: Temperature,
		Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 28, CellDegrees: 1, ScratchDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	window, err := prepared.ReadWindow(context.Background(), GridWindow{Row: 0, Column: 0, Rows: 1, Cols: 2})
	if err != nil {
		t.Fatal(err)
	}
	if window.Rows != 3 || window.Cols != 4 {
		t.Fatalf("window is %dx%d; want 4x3", window.Cols, window.Rows)
	}
	for column := 0; column < int(window.Cols); column++ {
		if !float32NaN(window.Values[column]) {
			t.Fatalf("north latitude halo column %d is not NaN: %v", column, window.Values)
		}
	}
	if !float32NaN(window.Values[int(window.Cols)]) {
		t.Fatalf("regional longitude halo is not NaN: %v", window.Values)
	}
	if !float32NaN(window.Values[int(window.Cols)+2]) { // source cell [0,1] occupies destination column 2
		t.Fatal("source no-data was not preserved")
	}
}

func TestPreparedFrameMatchesDecodeAndHonoursCancellation(t *testing.T) {
	bounds := delivery.GridBounds{West: 150, East: 152, South: -35, North: -34}
	runner := &preparedFakeRunner{t: t}
	decoder, err := NewDecoder(runner, bounds)
	if err != nil {
		t.Fatal(err)
	}
	request := DecodeRequest{Path: "temperature.grib2", Field: Temperature, Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 7, CellDegrees: 1, ScratchDirectory: t.TempDir()}
	decoded, err := decoder.Decode(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decoder.PrepareFrame(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	secondRunner := &preparedFakeRunner{t: t, metadataJSON: `{"messages":[{"shortName":{"value":"2t"},"stepType":{"value":"instant"},"startStep":{"value":"2"},"endStep":{"value":"2"},"units":{"value":"K"},"packingError":{"value":"0.01"},"binaryScaleFactor":{"value":"-6"},"decimalScaleFactor":{"value":"0"}}]}`}
	secondDecoder, err := NewDecoder(secondRunner, bounds)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := request
	secondRequest.Run = request.Run
	second, err := secondDecoder.PrepareFrame(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	normalized, err := ReadNormalizedWindowSeries(context.Background(), GridWindow{Row: 0, Column: 0, Rows: 1, Cols: 2}, prepared, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || normalized[0].Semantics != IntervalMean {
		t.Fatalf("prepared series was not canonically normalized: %+v", normalized)
	}
	got := normalized[0].Grid.Values
	if len(got) != 12 || len(decoded.Grid.Values) != 2 || got[5] != decoded.Grid.Values[0] || got[6] != decoded.Grid.Values[1] {
		t.Fatalf("prepared normalized core %v differs from Decode %v", got, decoded.Grid.Values)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepared.ReadWindow(cancelled, GridWindow{Row: 0, Column: 0, Rows: 1, Cols: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error = %v; want context.Canceled", err)
	}
}

func TestReadNormalizedWindowSeriesKeepsDatelineHaloInOneCanonicalGrid(t *testing.T) {
	runner := &preparedFakeRunner{t: t, metadataJSON: `{"messages":[{"shortName":{"value":"tp"},"stepType":{"value":"accum"},"startStep":{"value":"0"},"endStep":{"value":"1"},"units":{"value":"mm"},"packingError":{"value":"0.01"},"binaryScaleFactor":{"value":"-6"},"decimalScaleFactor":{"value":"0"}}]}`}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: -180, East: 180, South: -90, North: 90})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: "rain.grib2", Field: Rain,
		Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 28, CellDegrees: 1, ScratchDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	series, err := ReadNormalizedWindowSeries(context.Background(), GridWindow{Row: 10, Column: 359, Rows: 1, Cols: 1}, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("normalized dateline series has %d frames; want 1", len(series))
	}
	grid := series[0].Grid
	if grid.Spec.Rows != 3 || grid.Spec.Cols != 3 || grid.Spec.Bounds.West != 178 || grid.Spec.Bounds.East != 181 {
		t.Fatalf("unexpected unwrapped dateline halo: %+v", grid.Spec)
	}
	if grid.Values[3] != 10000+358 || grid.Values[4] != 10000+359 || grid.Values[5] != 10000 {
		t.Fatalf("dateline halo did not wrap in order: %v", grid.Values)
	}
}

func TestPreparedFrameRejectsMisalignedGridBeforeConversion(t *testing.T) {
	runner := &preparedFakeRunner{t: t}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 0, East: 2, South: 0, North: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: "temperature.grib2", Field: Temperature,
		Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 7, CellDegrees: .3, ScratchDirectory: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "not aligned") {
		t.Fatalf("misaligned preparation error = %v", err)
	}
	if runner.count("grib_to_netcdf") != 0 || runner.count("gdal_translate") != 0 {
		t.Fatalf("misaligned preparation invoked conversion: %+v", runner.counts)
	}
}

func TestPreparedFrameCleansTemporaryFilesAfterFailedCrop(t *testing.T) {
	runner := &preparedFakeRunner{t: t, failCrop: true}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 0, East: 2, South: 0, North: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: "temperature.grib2", Field: Temperature,
		Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 7, CellDegrees: 1, ScratchDirectory: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic crop failure") {
		t.Fatalf("failed crop error = %v", err)
	}
	if runner.outputDirectory == "" {
		t.Fatal("failed crop did not expose its temporary output path")
	}
	if _, err := os.Stat(runner.outputDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation left temporary directory: %v", err)
	}
}

func TestPreparedFrameWithRealGDALSyntheticGRIB(t *testing.T) {
	for _, command := range []string{"gdal_translate", "grib_set", "grib_to_netcdf", "grib_ls"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not on PATH", command)
		}
	}
	directory := t.TempDir()
	ascii := filepath.Join(directory, "temperature.asc")
	if err := os.WriteFile(ascii, []byte("ncols 4\nnrows 2\nxllcorner 0\nyllcorner 0\ncellsize 1\nNODATA_value -9999\n280 281 282 283\n284 285 286 287\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := ExecRunner{}
	base := filepath.Join(directory, "base.grib2")
	if _, err := runner.Run(context.Background(), "gdal_translate", "-q", "-of", "GRIB", "-a_srs", "EPSG:4326", ascii, base); err != nil {
		t.Fatal(err)
	}
	grib := filepath.Join(directory, "temperature.grib2")
	if _, err := runner.Run(context.Background(), "grib_set", "-r", "-s", "shortName=2t,stepType=instant,stepUnits=h,startStep=1,endStep=1", base, grib); err != nil {
		t.Fatal(err)
	}
	decoder, err := NewDecoder(runner, delivery.GridBounds{West: 0, East: 4, South: 0, North: 2})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decoder.PrepareFrame(context.Background(), DecodeRequest{
		Path: grib, Field: Temperature, Run: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), NativeResolutionKM: 28, CellDegrees: 1, ScratchDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	window, err := prepared.ReadWindow(context.Background(), GridWindow{Row: 0, Column: 0, Rows: 2, Cols: 4})
	if err != nil {
		t.Fatal(err)
	}
	finite := 0
	for row := 1; row <= 2; row++ {
		for column := 1; column <= 4; column++ {
			if finite32(window.Values[row*int(window.Cols)+column]) {
				finite++
			}
		}
	}
	if finite != 8 {
		t.Fatalf("prepared synthetic core has %d finite values; want 8: %v", finite, window.Values)
	}
}

func BenchmarkPreparedFrameRead256Window(b *testing.B) {
	directory := b.TempDir()
	path := filepath.Join(directory, "field.f32")
	const rows, cols = 360, 720
	values := make([]byte, rows*cols*4)
	for index := 0; index < rows*cols; index++ {
		binary.LittleEndian.PutUint32(values[index*4:index*4+4], math.Float32bits(float32(index)))
	}
	if err := os.WriteFile(path, values, 0o600); err != nil {
		b.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	prepared := &PreparedFrame{file: file, metadata: PreparedFrameMetadata{Grid: delivery.GridSpec{Bounds: delivery.GridBounds{West: -180, East: 180, South: -90, North: 90}, Rows: rows, Cols: cols}}, wraps: true}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := prepared.ReadWindow(context.Background(), GridWindow{Row: 50, Column: 650, Rows: 256, Cols: 256}); err != nil {
			b.Fatal(err)
		}
	}
}

type preparedFakeRunner struct {
	t               *testing.T
	mu              sync.Mutex
	counts          map[string]int
	noDataRow       int
	noDataColumn    int
	failCrop        bool
	outputDirectory string
	metadataJSON    string
}

func (runner *preparedFakeRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	runner.mu.Lock()
	if runner.counts == nil {
		runner.counts = map[string]int{}
	}
	runner.counts[command]++
	runner.mu.Unlock()
	switch command {
	case "grib_ls":
		if runner.metadataJSON != "" {
			return []byte(runner.metadataJSON), nil
		}
		return []byte(`{"messages":[{"shortName":{"value":"2t"},"stepType":{"value":"instant"},"startStep":{"value":"1"},"endStep":{"value":"1"},"units":{"value":"K"},"packingError":{"value":"0.01"},"binaryScaleFactor":{"value":"-6"},"decimalScaleFactor":{"value":"0"}}]}`), nil
	case "grib_to_netcdf":
		return nil, nil
	case "gdal_translate":
		cols, rows, err := outputSize(args)
		if err != nil {
			return nil, err
		}
		output := args[len(args)-1]
		runner.mu.Lock()
		runner.outputDirectory = filepath.Dir(output)
		runner.mu.Unlock()
		if runner.failCrop {
			return nil, errors.New("synthetic crop failure")
		}
		payload := make([]byte, rows*cols*4)
		for row := 0; row < rows; row++ {
			for column := 0; column < cols; column++ {
				value := float32(row*1000 + column)
				if row == runner.noDataRow && column == runner.noDataColumn && (runner.noDataRow != 0 || runner.noDataColumn != 0) {
					value = -9999
				}
				index := row*cols + column
				binary.LittleEndian.PutUint32(payload[index*4:index*4+4], math.Float32bits(value))
			}
		}
		if err := os.WriteFile(output, payload, 0o600); err != nil {
			return nil, err
		}
		header := fmt.Sprintf("BYTEORDER I\nLAYOUT BIL\nNROWS %d\nNCOLS %d\nNBANDS 1\nNBITS 32\nNODATA -9999\n", rows, cols)
		if err := os.WriteFile(strings.TrimSuffix(output, filepath.Ext(output))+".hdr", []byte(header), 0o600); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command %q", command)
	}
}

func (runner *preparedFakeRunner) count(command string) int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.counts[command]
}

func outputSize(args []string) (int, int, error) {
	for index := range args {
		if args[index] == "-outsize" && index+2 < len(args) {
			cols, err := strconv.Atoi(args[index+1])
			if err != nil {
				return 0, 0, err
			}
			rows, err := strconv.Atoi(args[index+2])
			return cols, rows, err
		}
	}
	return 0, 0, errors.New("gdal_translate omitted -outsize")
}
