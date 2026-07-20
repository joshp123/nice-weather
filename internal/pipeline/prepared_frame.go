package pipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nice-weather-finder/internal/delivery"
)

// PreparedFrameMetadata is the validated source identity, weather meaning and
// grid geometry of one prepared GRIB message. Units and packing precision stay
// in source physical units until ReadNormalizedWindowSeries applies the one
// canonical series normalizer.
type PreparedFrameMetadata struct {
	Field              Field
	Start              time.Time
	End                time.Time
	Semantics          Semantics
	Units              string
	PackingError       float64
	PackingQuantum     float64
	NativeResolutionKM float64
	Grid               delivery.GridSpec
}

// GridWindow identifies a half-open core region in prepared-grid coordinates.
// Row zero is north and column zero is west. ReadWindow adds exactly one cell
// of halo on every side. On a worldwide grid, columns crossing either edge
// wrap at the antimeridian; latitude never wraps.
type GridWindow struct {
	Row    uint32
	Column uint32
	Rows   uint32
	Cols   uint32
}

// PreparedWindow is a row-major physical Float32 window. OriginRow and
// OriginColumn include the one-cell halo and may therefore be -1. A worldwide
// window keeps an unwrapped column origin while Values wrap at the dateline.
// Latitude halo outside the prepared grid is NaN.
type PreparedWindow struct {
	OriginRow    int64
	OriginColumn int64
	Rows         uint32
	Cols         uint32
	Values       []float32
	metadata     PreparedFrameMetadata
}

// PreparedFrame owns one physical Float32 backing path and its temporary
// directory. Each read opens the path only for its duration, so idle prepared
// messages consume no file descriptors. Concurrent reads are safe. Close waits
// for active reads, removes all prepared data and is idempotent.
type PreparedFrame struct {
	mu        sync.RWMutex
	path      string
	directory string
	metadata  PreparedFrameMetadata
	wraps     bool
	closed    bool
}

// PrepareFrame inspects and converts a validated GRIB message exactly once,
// then stores canonical physical Float32 values on disk for bounded windowed
// reads. It does not aggregate or publish weather.
func (decoder *Decoder) PrepareFrame(ctx context.Context, request DecodeRequest) (*PreparedFrame, error) {
	if decoder == nil || decoder.Runner == nil {
		return nil, errors.New("decoder command runner is required")
	}
	if err := validatePrepareRequest(request); err != nil {
		return nil, err
	}
	metadataJSON, err := decoder.Runner.Run(ctx, "grib_ls", "-j", "-x", "-p", "shortName,stepType,startStep,endStep,units,packingError,binaryScaleFactor,decimalScaleFactor", request.Path)
	if err != nil {
		return nil, fmt.Errorf("inspect GRIB metadata: %w", err)
	}
	metadata, err := ParseECCodesJSON(metadataJSON)
	if err != nil {
		return nil, err
	}
	if !shortNameMatches(request.Field, metadata.ShortName) {
		return nil, fmt.Errorf("GRIB field %q does not match requested %s", metadata.ShortName, request.Field)
	}
	semantics, start, end, err := InterpretMetadata(request.Field, request.Run, metadata)
	if err != nil {
		return nil, err
	}

	cols, rows, err := alignedDimensions(decoder.bounds, request.CellDegrees)
	if err != nil {
		return nil, err
	}
	spec := delivery.GridSpec{Bounds: decoder.bounds, Rows: rows, Cols: cols}
	if err := delivery.ValidateGridSpec(spec); err != nil {
		return nil, fmt.Errorf("prepared frame grid: %w", err)
	}
	raw := Frame{
		Field: request.Field, Start: start, End: end, Semantics: semantics,
		Units: metadata.Units, PackingError: metadata.PackingError, PackingQuantum: metadata.PackingQuantum,
		NativeResolutionKM: request.NativeResolutionKM, Grid: FloatGrid{Spec: spec},
	}
	if err := raw.validateMetadata(); err != nil {
		return nil, err
	}
	if _, _, err := normalizationForFrame(raw); err != nil {
		return nil, err
	}

	directory, err := os.MkdirTemp(request.ScratchDirectory, "nice-weather-prepared-")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(directory)
		}
	}()
	decoded := filepath.Join(directory, "field.nc")
	rawPath := filepath.Join(directory, "field.bil")
	if _, err = decoder.Runner.Run(ctx, "grib_to_netcdf", "-D", "NC_FLOAT", "-o", decoded, request.Path); err != nil {
		return nil, fmt.Errorf("decode GRIB message: %w", err)
	}
	if err := cropPhysicalGrid(ctx, decoder.Runner, decoded, rawPath, decoder.bounds, int(cols), int(rows)); err != nil {
		return nil, err
	}
	layout, err := inspectEHdr(rawPath)
	if err != nil {
		return nil, err
	}
	if layout.rows != rows || layout.cols != cols {
		return nil, fmt.Errorf("prepared EHdr is %dx%d; want %dx%d", layout.cols, layout.rows, cols, rows)
	}
	preparedPath := filepath.Join(directory, "field.f32")
	if err := writePreparedValues(ctx, rawPath, preparedPath, layout); err != nil {
		return nil, err
	}
	file, err := os.Open(preparedPath)
	if err != nil {
		return nil, fmt.Errorf("open prepared frame: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close prepared frame after validation: %w", err)
	}
	// Only the canonical backing file remains after preparation.
	_ = os.Remove(decoded)
	_ = os.Remove(rawPath)
	_ = os.Remove(filepath.Join(directory, "field.hdr"))

	cleanup = false
	return &PreparedFrame{
		path: preparedPath, directory: directory,
		metadata: metadataFromFrame(raw),
		wraps:    decoder.bounds.West == -180 && decoder.bounds.East == 180,
	}, nil
}

func validatePrepareRequest(request DecodeRequest) error {
	if request.Path == "" {
		return errors.New("decoder path is required")
	}
	if request.CellDegrees <= 0 || math.IsNaN(request.CellDegrees) || math.IsInf(request.CellDegrees, 0) {
		return errors.New("decoder cell spacing must be positive")
	}
	if request.ScratchDirectory == "" || !filepath.IsAbs(request.ScratchDirectory) {
		return errors.New("prepared frame scratch directory must be absolute")
	}
	return nil
}

func alignedDimensions(bounds delivery.GridBounds, cellDegrees float64) (uint32, uint32, error) {
	dimension := func(span float64, name string) (uint32, error) {
		exact := span / cellDegrees
		rounded := math.Round(exact)
		if rounded < 1 || rounded > math.MaxUint32 || math.Abs(exact-rounded) > 1e-8*math.Max(1, math.Abs(exact)) {
			return 0, fmt.Errorf("decoder %s span is not aligned to cell spacing", name)
		}
		return uint32(rounded), nil
	}
	cols, err := dimension(bounds.East-bounds.West, "longitude")
	if err != nil {
		return 0, 0, err
	}
	rows, err := dimension(bounds.North-bounds.South, "latitude")
	if err != nil {
		return 0, 0, err
	}
	return cols, rows, nil
}

func metadataFromFrame(frame Frame) PreparedFrameMetadata {
	return PreparedFrameMetadata{
		Field: frame.Field, Start: frame.Start, End: frame.End, Semantics: frame.Semantics,
		Units: frame.Units, PackingError: frame.PackingError, PackingQuantum: frame.PackingQuantum,
		NativeResolutionKM: frame.NativeResolutionKM, Grid: frame.Grid.Spec,
	}
}

const preparedChunkValues = 64 * 1024

func writePreparedValues(ctx context.Context, sourcePath, destinationPath string, layout ehdrLayout) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open EHdr payload: %w", err)
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create prepared frame: %w", err)
	}
	destinationOpen := true
	defer func() {
		if destinationOpen {
			_ = destination.Close()
		}
	}()

	remaining := layout.count
	input := make([]byte, preparedChunkValues*4)
	output := make([]byte, preparedChunkValues*4)
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := min(remaining, preparedChunkValues)
		input = input[:count*4]
		output = output[:count*4]
		if _, err := io.ReadFull(source, input); err != nil {
			return fmt.Errorf("read EHdr payload: %w", err)
		}
		for index := 0; index < count; index++ {
			value := math.Float32frombits(layout.order.Uint32(input[index*4 : index*4+4]))
			if !finite32(value) || (layout.hasNoData && value == layout.noData) {
				value = float32(math.NaN())
			}
			binary.LittleEndian.PutUint32(output[index*4:index*4+4], math.Float32bits(value))
		}
		if _, err := destination.Write(output); err != nil {
			return fmt.Errorf("write prepared frame: %w", err)
		}
		remaining -= count
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("sync prepared frame: %w", err)
	}
	closeErr := destination.Close()
	destinationOpen = false
	if closeErr != nil {
		return fmt.Errorf("close prepared frame: %w", closeErr)
	}
	return nil
}

// Metadata returns immutable prepared-frame metadata.
func (frame *PreparedFrame) Metadata() (PreparedFrameMetadata, error) {
	if frame == nil {
		return PreparedFrameMetadata{}, errors.New("prepared frame is nil")
	}
	frame.mu.RLock()
	defer frame.mu.RUnlock()
	if frame.closed || frame.path == "" {
		return PreparedFrameMetadata{}, errors.New("prepared frame is closed")
	}
	return frame.metadata, nil
}

// ReadWindow reads one half-open core plus its one-cell halo without invoking
// an external command or loading the prepared frame into memory.
func (frame *PreparedFrame) ReadWindow(ctx context.Context, window GridWindow) (result PreparedWindow, resultErr error) {
	if frame == nil {
		return PreparedWindow{}, errors.New("prepared frame is nil")
	}
	frame.mu.RLock()
	defer frame.mu.RUnlock()
	if frame.closed || frame.path == "" {
		return PreparedWindow{}, errors.New("prepared frame is closed")
	}
	if err := ctx.Err(); err != nil {
		return PreparedWindow{}, err
	}
	grid := frame.metadata.Grid
	if window.Rows == 0 || window.Cols == 0 || window.Row >= grid.Rows || window.Column >= grid.Cols || uint64(window.Row)+uint64(window.Rows) > uint64(grid.Rows) {
		return PreparedWindow{}, errors.New("prepared window is outside the grid")
	}
	if !frame.wraps && uint64(window.Column)+uint64(window.Cols) > uint64(grid.Cols) {
		return PreparedWindow{}, errors.New("prepared window crosses a non-worldwide longitude edge")
	}
	if frame.wraps && window.Cols > grid.Cols {
		return PreparedWindow{}, errors.New("prepared window is wider than the world")
	}
	rows64, cols64 := uint64(window.Rows)+2, uint64(window.Cols)+2
	maxInt := uint64(^uint(0) >> 1)
	if rows64 > math.MaxUint32 || cols64 > math.MaxUint32 || (cols64 != 0 && rows64 > maxInt/cols64) {
		return PreparedWindow{}, errors.New("prepared window is too large")
	}
	values := make([]float32, int(rows64*cols64))
	for index := range values {
		values[index] = float32(math.NaN())
	}
	result = PreparedWindow{
		OriginRow: int64(window.Row) - 1, OriginColumn: int64(window.Column) - 1,
		Rows: uint32(rows64), Cols: uint32(cols64), Values: values, metadata: frame.metadata,
	}
	file, err := os.Open(frame.path)
	if err != nil {
		return PreparedWindow{}, fmt.Errorf("open prepared frame for reading: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	scratch := make([]byte, int(result.Cols)*4)
	for destinationRow := uint32(0); destinationRow < result.Rows; destinationRow++ {
		if err := ctx.Err(); err != nil {
			return PreparedWindow{}, err
		}
		sourceRow := result.OriginRow + int64(destinationRow)
		if sourceRow < 0 || sourceRow >= int64(grid.Rows) {
			continue
		}
		destination := values[int(destinationRow)*int(result.Cols) : int(destinationRow+1)*int(result.Cols)]
		if err := frame.readRow(file, sourceRow, result.OriginColumn, destination, scratch); err != nil {
			return PreparedWindow{}, err
		}
	}
	return result, nil
}

// ReadNormalizedWindowSeries reads one aligned core and halo from every
// prepared message, then delegates interval and cumulative policy to the
// existing canonical NormalizeSeries path.
func ReadNormalizedWindowSeries(ctx context.Context, window GridWindow, prepared ...*PreparedFrame) ([]Frame, error) {
	if len(prepared) == 0 {
		return nil, nil
	}
	frames := make([]Frame, len(prepared))
	for index, item := range prepared {
		read, err := item.ReadWindow(ctx, window)
		if err != nil {
			return nil, fmt.Errorf("read prepared frame %d: %w", index, err)
		}
		metadata := read.metadata
		frames[index] = Frame{
			Field: metadata.Field, Start: metadata.Start, End: metadata.End, Semantics: metadata.Semantics,
			Units: metadata.Units, PackingError: metadata.PackingError, PackingQuantum: metadata.PackingQuantum,
			NativeResolutionKM: metadata.NativeResolutionKM,
			Grid:               FloatGrid{Spec: preparedWindowSpec(metadata.Grid, read), Values: read.Values},
		}
	}
	return NormalizeSeries(frames)
}

func preparedWindowSpec(source delivery.GridSpec, window PreparedWindow) delivery.GridSpec {
	longitudeCell := (source.Bounds.East - source.Bounds.West) / float64(source.Cols)
	latitudeCell := (source.Bounds.North - source.Bounds.South) / float64(source.Rows)
	west := source.Bounds.West + float64(window.OriginColumn)*longitudeCell
	north := source.Bounds.North - float64(window.OriginRow)*latitudeCell
	return delivery.GridSpec{
		Bounds: delivery.GridBounds{
			West: west, East: west + float64(window.Cols)*longitudeCell,
			North: north, South: north - float64(window.Rows)*latitudeCell,
		},
		Rows: window.Rows, Cols: window.Cols,
	}
}

func (frame *PreparedFrame) readRow(file *os.File, row, firstColumn int64, destination []float32, scratch []byte) error {
	columns := int64(frame.metadata.Grid.Cols)
	for destinationStart := 0; destinationStart < len(destination); {
		sourceColumn := firstColumn + int64(destinationStart)
		if frame.wraps {
			sourceColumn = ((sourceColumn % columns) + columns) % columns
		} else if sourceColumn < 0 || sourceColumn >= columns {
			destinationStart++
			continue
		}
		run := len(destination) - destinationStart
		if remaining := int(columns - sourceColumn); run > remaining {
			run = remaining
		}
		bytes := scratch[:run*4]
		offset := (row*columns + sourceColumn) * 4
		read, err := file.ReadAt(bytes, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read prepared frame: %w", err)
		}
		if read != len(bytes) {
			return fmt.Errorf("read prepared frame: got %d bytes; want %d", read, len(bytes))
		}
		for index := 0; index < run; index++ {
			destination[destinationStart+index] = math.Float32frombits(binary.LittleEndian.Uint32(bytes[index*4 : index*4+4]))
		}
		destinationStart += run
	}
	return nil
}

// Close waits for active reads and removes the owned temporary directory.
func (frame *PreparedFrame) Close() error {
	if frame == nil {
		return nil
	}
	frame.mu.Lock()
	defer frame.mu.Unlock()
	if frame.closed || frame.path == "" {
		return nil
	}
	frame.closed = true
	frame.path = ""
	removeErr := os.RemoveAll(frame.directory)
	frame.directory = ""
	return removeErr
}
