package pipeline

import (
	"bufio"
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
	"time"

	"nice-weather-finder/internal/delivery"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Decoder struct {
	Runner CommandRunner
	bounds delivery.GridBounds
}

func NewDecoder(runner CommandRunner, bounds delivery.GridBounds) (*Decoder, error) {
	if err := delivery.ValidateGridSpec(delivery.GridSpec{Bounds: bounds, Rows: 1, Cols: 1}); err != nil {
		return nil, fmt.Errorf("decoder bounds: %w", err)
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Decoder{Runner: runner, bounds: bounds}, nil
}

type DecodeRequest struct {
	Path               string
	Field              Field
	Run                time.Time
	NativeResolutionKM float64
	CellDegrees        float64
	// ScratchDirectory is required only by PrepareFrame. It must be an
	// absolute caller-owned directory on durable local storage.
	ScratchDirectory string
}

// Decode crops before loading values into Go. gdalwarp emits a binary float32
// EHdr grid on the exact product bounds; ecCodes supplies interval semantics
// and units. CellDegrees is the provider's native regular-grid spacing
// (0.0625 for ICON-EU and 0.25 for IFS).
func (decoder *Decoder) Decode(ctx context.Context, request DecodeRequest) (Frame, error) {
	if decoder == nil || decoder.Runner == nil {
		return Frame{}, errors.New("decoder command runner is required")
	}
	if request.CellDegrees <= 0 || math.IsNaN(request.CellDegrees) || math.IsInf(request.CellDegrees, 0) {
		return Frame{}, errors.New("decoder cell spacing must be positive")
	}
	metadataJSON, err := decoder.Runner.Run(ctx, "grib_ls", "-j", "-x", "-p", "shortName,stepType,startStep,endStep,units,packingError,binaryScaleFactor,decimalScaleFactor", request.Path)
	if err != nil {
		return Frame{}, fmt.Errorf("inspect GRIB metadata: %w", err)
	}
	metadata, err := ParseECCodesJSON(metadataJSON)
	if err != nil {
		return Frame{}, err
	}
	if !shortNameMatches(request.Field, metadata.ShortName) {
		return Frame{}, fmt.Errorf("GRIB field %q does not match requested %s", metadata.ShortName, request.Field)
	}
	semantics, start, end, err := InterpretMetadata(request.Field, request.Run, metadata)
	if err != nil {
		return Frame{}, err
	}
	temporary, err := os.MkdirTemp("", "nice-weather-grid-")
	if err != nil {
		return Frame{}, err
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "field.bil")
	decoded := filepath.Join(temporary, "field.nc")
	// ecCodes supports the CCSDS packing used by current ICON-EU even when a
	// particular GDAL build lacks libaec. NetCDF also preserves the decoded
	// scale/offset while keeping the full field out of Go memory.
	// NC_SHORT is grib_to_netcdf's default and independently re-quantizes every
	// message. On the audited ICON-EU Berlin cell, a physically constant
	// 0.99609375 mm became 0.99538..0.99652 mm across adjacent files, creating
	// false negative cumulative deltas and patchwork holes. NC_FLOAT preserves
	// the ecCodes-decoded source values and their declared packing precision.
	if _, err = decoder.Runner.Run(ctx, "grib_to_netcdf", "-D", "NC_FLOAT", "-o", decoded, request.Path); err != nil {
		return Frame{}, fmt.Errorf("decode GRIB message: %w", err)
	}
	bounds := decoder.bounds
	cols := int(math.Round((bounds.East - bounds.West) / request.CellDegrees))
	rows := int(math.Round((bounds.North - bounds.South) / request.CellDegrees))
	if err := cropPhysicalGrid(ctx, decoder.Runner, decoded, output, bounds, cols, rows); err != nil {
		return Frame{}, err
	}
	grid, err := readEHdr(output, bounds)
	if err != nil {
		return Frame{}, err
	}
	frame := Frame{Field: request.Field, Start: start, End: end, Semantics: semantics, Units: metadata.Units, PackingError: metadata.PackingError, PackingQuantum: metadata.PackingQuantum, NativeResolutionKM: request.NativeResolutionKM, Grid: grid}
	return normalizeFrame(frame)
}

func cropPhysicalGrid(ctx context.Context, runner CommandRunner, source, output string, bounds delivery.GridBounds, cols, rows int) error {
	_, err := runner.Run(ctx, "gdal_translate",
		// Decode already requested NC_FLOAT from ecCodes, so values here are
		// physical float samples rather than independently packed Int16 data.
		"-q", "-of", "EHdr", "-ot", "Float32", "-r", "nearest",
		"-projwin", formatCoordinate(bounds.West), formatCoordinate(bounds.North), formatCoordinate(bounds.East), formatCoordinate(bounds.South),
		"-outsize", strconv.Itoa(cols), strconv.Itoa(rows),
		source, output,
	)
	if err != nil {
		return fmt.Errorf("crop GRIB field: %w", err)
	}
	return nil
}

// DecodeRaster crops a regular raster onto the product grid without assigning
// weather meaning. It is the non-GRIB boundary used for static climatology
// COGs; callers remain responsible for units and metric construction.
func (decoder *Decoder) DecodeRaster(ctx context.Context, path string, cellDegrees float64) (FloatGrid, error) {
	if decoder == nil || decoder.Runner == nil {
		return FloatGrid{}, errors.New("decoder command runner is required")
	}
	if strings.TrimSpace(path) == "" || cellDegrees <= 0 || math.IsNaN(cellDegrees) || math.IsInf(cellDegrees, 0) {
		return FloatGrid{}, errors.New("raster path and positive cell spacing are required")
	}
	temporary, err := os.MkdirTemp("", "nice-weather-raster-")
	if err != nil {
		return FloatGrid{}, err
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "field.bil")
	bounds := decoder.bounds
	_, err = decoder.Runner.Run(ctx, "gdalwarp",
		"-q", "-overwrite", "-of", "EHdr", "-ot", "Float32", "-r", "bilinear", "-dstnodata", "nan",
		"-te", formatCoordinate(bounds.West), formatCoordinate(bounds.South), formatCoordinate(bounds.East), formatCoordinate(bounds.North),
		"-tr", formatCoordinate(cellDegrees), formatCoordinate(cellDegrees), "-tap",
		path, output,
	)
	if err != nil {
		return FloatGrid{}, fmt.Errorf("crop raster: %w", err)
	}
	return readEHdr(output, bounds)
}

func shortNameMatches(field Field, shortName string) bool {
	shortName = strings.ToLower(strings.TrimSpace(shortName))
	allowed := map[Field]map[string]bool{
		Temperature:  {"2t": true, "t_2m": true},
		DewPoint:     {"2d": true, "td_2m": true},
		CloudCover:   {"tcc": true, "clct": true},
		Rain:         {"tp": true, "tot_prec": true},
		WindU:        {"10u": true, "u_10m": true},
		WindV:        {"10v": true, "v_10m": true},
		Gust:         {"10fg": true, "max_i10fg": true, "gust": true, "vmax_10m": true},
		Solar:        {"ssrd": true},
		SolarDirect:  {"aswdir_s": true},
		SolarDiffuse: {"aswdifd_s": true},
	}
	return allowed[field][shortName]
}

func formatCoordinate(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func readEHdr(path string, bounds delivery.GridBounds) (FloatGrid, error) {
	layout, err := inspectEHdr(path)
	if err != nil {
		return FloatGrid{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return FloatGrid{}, fmt.Errorf("read EHdr payload: %w", err)
	}
	decoded := make([]float32, layout.count)
	for index := range decoded {
		value := math.Float32frombits(layout.order.Uint32(payload[index*4 : index*4+4]))
		if !finite32(value) || (layout.hasNoData && value == layout.noData) {
			value = float32(math.NaN())
		}
		decoded[index] = value
	}
	return FloatGrid{Spec: delivery.GridSpec{Bounds: bounds, Rows: layout.rows, Cols: layout.cols}, Values: decoded}, nil
}

type ehdrLayout struct {
	rows      uint32
	cols      uint32
	count     int
	order     binary.ByteOrder
	noData    float32
	hasNoData bool
}

func inspectEHdr(path string) (ehdrLayout, error) {
	header, err := os.Open(strings.TrimSuffix(path, filepath.Ext(path)) + ".hdr")
	if err != nil {
		return ehdrLayout{}, fmt.Errorf("open EHdr header: %w", err)
	}
	defer header.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(header)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) >= 2 {
			values[strings.ToUpper(parts[0])] = strings.Join(parts[1:], " ")
		}
	}
	if err := scanner.Err(); err != nil {
		return ehdrLayout{}, fmt.Errorf("read EHdr header: %w", err)
	}
	rows, err := strconv.ParseUint(values["NROWS"], 10, 32)
	if err != nil || rows == 0 {
		return ehdrLayout{}, errors.New("EHdr NROWS is missing or invalid")
	}
	cols, err := strconv.ParseUint(values["NCOLS"], 10, 32)
	if err != nil || cols == 0 {
		return ehdrLayout{}, errors.New("EHdr NCOLS is missing or invalid")
	}
	if bands := values["NBANDS"]; bands != "" && bands != "1" {
		return ehdrLayout{}, fmt.Errorf("EHdr has %s bands; want 1", bands)
	}
	if bits := values["NBITS"]; bits != "32" {
		return ehdrLayout{}, fmt.Errorf("EHdr has %q bits; want 32", bits)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ehdrLayout{}, fmt.Errorf("stat EHdr payload: %w", err)
	}
	count := uint64(rows) * uint64(cols)
	if count > uint64(^uint(0)>>1) || count > uint64(math.MaxInt64/4) || uint64(info.Size()) != count*4 {
		return ehdrLayout{}, fmt.Errorf("EHdr payload has %d bytes; want %d", info.Size(), count*4)
	}
	var order binary.ByteOrder = binary.LittleEndian
	if byteOrder := strings.ToUpper(values["BYTEORDER"]); byteOrder == "M" || byteOrder == "MSBFIRST" {
		order = binary.BigEndian
	}
	nodata, hasNoData := float32(0), false
	noDataText := values["NODATA"]
	if noDataText == "" {
		noDataText = values["NODATA_VALUE"]
	}
	if text := noDataText; text != "" {
		parsed, parseErr := strconv.ParseFloat(text, 32)
		if parseErr != nil {
			return ehdrLayout{}, fmt.Errorf("invalid EHdr NODATA %q", text)
		}
		nodata, hasNoData = float32(parsed), true
	}
	return ehdrLayout{rows: uint32(rows), cols: uint32(cols), count: int(count), order: order, noData: nodata, hasNoData: hasNoData}, nil
}
