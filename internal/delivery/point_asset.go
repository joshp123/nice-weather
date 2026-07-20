package delivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"nice-weather-finder/internal/weather"
)

const (
	PointAssetVersion       = 1
	PointAssetHeaderBytes   = 128
	DailyPointRecordBytes   = 30
	RangePointRecordBytes   = 44
	DisplayScoreRecordBytes = 8
	ForecastPointDayCount   = 14
)

type PointKind uint8

const (
	PointKindForecastDay PointKind = iota + 1
	PointKindForecastWindow
	PointKindLegacyClimateMonth
	// PointKindClimateDistribution is deliberately reserved for the recent
	// daily-distribution climate product. A CHELSA mean month must never be
	// decoded as that future evidence.
	PointKindClimateDistribution
	PointKindDisplayScore
)

const pointProjectionWebMercatorImage uint8 = 1

var pointMagic = [8]byte{'N', 'W', 'F', 'P', 'O', 'I', 'N', 'T'}

type PointQuad struct {
	West  float64 `json:"west"`
	East  float64 `json:"east"`
	North float64 `json:"north"`
	South float64 `json:"south"`
}

// PointGeometry describes records laid out one-for-one with rendered overlay
// texels. Quad contains MapLibre's rounded normalized-Mercator image edges, so
// clients do not need to reproduce source-cell coastal fallback.
type PointGeometry struct {
	Bounds GridBounds `json:"bounds"`
	Quad   PointQuad  `json:"quad"`
	Rows   uint32     `json:"rows"`
	Cols   uint32     `json:"cols"`
}

type PointAsset struct {
	URL           string        `json:"url"`
	SHA256        string        `json:"sha256"`
	PayloadSHA256 string        `json:"payloadSha256"`
	Bytes         uint64        `json:"bytes"`
	Kind          string        `json:"kind"`
	HeaderBytes   uint16        `json:"headerBytes"`
	RecordBytes   uint16        `json:"recordBytes"`
	Projection    string        `json:"projection"`
	Geometry      PointGeometry `json:"geometry"`
}

type PointMetadata struct {
	Version       uint16
	Kind          PointKind
	Geometry      PointGeometry
	HeaderBytes   uint16
	RecordBytes   uint16
	Bytes         uint64
	SHA256        string
	PayloadSHA256 string
}

type DailyPointRecord struct {
	Weather      DailyRecord
	Thermal      uint8
	Dryness      uint8
	Sun          uint8
	NiceScore    uint8
	NiceWind     uint8
	CyclingScore uint8
	CyclingWind  uint8
	SummaryCode  uint8
}

// DisplayScoreRecord is the evaluated scalar field used by the map. It is
// deliberately separate from DailyPointRecord: outlook weather facts retain
// their native provider geometry while the visible judgement may use a denser
// interpolated score plane.
type DisplayScoreRecord struct {
	Thermal, Dryness, Sun     uint8
	NiceScore, NiceWind       uint8
	CyclingScore, CyclingWind uint8
	Valid                     bool
}

type RangePointRecord struct {
	Nice        RangeRecord
	Cycling     RangeRecord
	NiceDays    [ForecastPointDayCount]uint8
	CyclingDays [ForecastPointDayCount]uint8
}

type PointAssetReader struct {
	file *os.File
	meta PointMetadata
}

func PointKindName(kind PointKind) string {
	switch kind {
	case PointKindForecastDay:
		return "forecast-day"
	case PointKindForecastWindow:
		return "forecast-window"
	case PointKindLegacyClimateMonth:
		return "legacy-climate-month"
	case PointKindClimateDistribution:
		return "climate-distribution"
	case PointKindDisplayScore:
		return "display-score"
	default:
		return ""
	}
}

func ParsePointKind(value string) (PointKind, bool) {
	for _, kind := range []PointKind{PointKindForecastDay, PointKindForecastWindow, PointKindLegacyClimateMonth, PointKindClimateDistribution, PointKindDisplayScore} {
		if value == PointKindName(kind) {
			return kind, true
		}
	}
	return 0, false
}

func pointRecordBytes(kind PointKind) (uint16, error) {
	switch kind {
	case PointKindForecastDay, PointKindLegacyClimateMonth:
		return DailyPointRecordBytes, nil
	case PointKindForecastWindow:
		return RangePointRecordBytes, nil
	case PointKindDisplayScore:
		return DisplayScoreRecordBytes, nil
	case PointKindClimateDistribution:
		return 0, errors.New("recent climate-distribution point records are not implemented")
	default:
		return 0, errors.New("unknown point record kind")
	}
}

func NewPointGeometry(source GridSpec) (PointGeometry, error) {
	width, height, err := WebMercatorImageDimensions(source)
	if err != nil {
		return PointGeometry{}, err
	}
	west, east, north, south := mapLibreImageQuad(source)
	geometry := PointGeometry{
		Bounds: source.Bounds, Rows: uint32(height), Cols: uint32(width),
		Quad: PointQuad{West: west, East: east, North: north, South: south},
	}
	if err := validatePointGeometry(geometry); err != nil {
		return PointGeometry{}, err
	}
	return geometry, nil
}

func validatePointGeometry(geometry PointGeometry) error {
	if err := validateGridSpec(GridSpec{Bounds: geometry.Bounds, Rows: geometry.Rows, Cols: geometry.Cols}); err != nil {
		return err
	}
	quad := geometry.Quad
	if !finite(quad.West) || !finite(quad.East) || !finite(quad.North) || !finite(quad.South) || quad.West >= quad.East || quad.North >= quad.South || quad.West < 0 || quad.East > 1 || quad.North < 0 || quad.South > 1 {
		return errors.New("point asset has invalid normalized Web Mercator quad")
	}
	west, east, north, south := mapLibreImageQuad(GridSpec{Bounds: geometry.Bounds, Rows: geometry.Rows, Cols: geometry.Cols})
	if quad != (PointQuad{West: west, East: east, North: north, South: south}) {
		return errors.New("point asset quad is not the canonical MapLibre image quad for its bounds")
	}
	return nil
}

func DailyPointRecords(source GridSpec, records []DailyRecord) (PointGeometry, []DailyPointRecord, error) {
	if len(records) != int(source.Rows)*int(source.Cols) {
		return PointGeometry{}, nil, errors.New("daily point source geometry does not match records")
	}
	geometry, err := NewPointGeometry(source)
	if err != nil {
		return PointGeometry{}, nil, err
	}
	mapper, err := NewWebMercatorMapper(source)
	if err != nil {
		return PointGeometry{}, nil, err
	}
	result := make([]DailyPointRecord, int(geometry.Rows)*int(geometry.Cols))
	for row := uint32(0); row < geometry.Rows; row++ {
		for col := uint32(0); col < geometry.Cols; col++ {
			sourceRow, sourceCol, ok := mapper.Select(int(row), int(col), func(candidateRow, candidateCol uint32) bool {
				return records[int(candidateRow)*int(source.Cols)+int(candidateCol)].Valid
			})
			if !ok {
				continue
			}
			weatherRecord := records[int(sourceRow)*int(source.Cols)+int(sourceCol)]
			point, err := DailyPointRecordFromWeather(weatherRecord)
			if err != nil {
				return PointGeometry{}, nil, fmt.Errorf("daily point texel %d,%d: %w", row, col, err)
			}
			result[int(row)*int(geometry.Cols)+int(col)] = point
		}
	}
	return geometry, result, nil
}

func DailyPointRecordFromWeather(record DailyRecord) (DailyPointRecord, error) {
	if err := validateRecord(record); err != nil {
		return DailyPointRecord{}, err
	}
	if !record.Valid {
		return DailyPointRecord{}, nil
	}
	metrics, err := record.WeatherMetrics(1)
	if err != nil {
		return DailyPointRecord{}, err
	}
	nice, cycling := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
	if !nice.Available || !cycling.Available || nice.Components.Thermal != cycling.Components.Thermal || nice.Components.Dryness != cycling.Components.Dryness || nice.Components.Sun != cycling.Components.Sun {
		return DailyPointRecord{}, errors.New("daily point scoring did not produce shared component evidence")
	}
	summary, err := weather.EncodeSummary(metrics)
	if err != nil {
		return DailyPointRecord{}, err
	}
	point := DailyPointRecord{
		Weather: record, Thermal: uint8(nice.Components.Thermal), Dryness: uint8(nice.Components.Dryness), Sun: uint8(nice.Components.Sun),
		NiceScore: uint8(nice.Score), NiceWind: uint8(nice.Components.Wind), CyclingScore: uint8(cycling.Score), CyclingWind: uint8(cycling.Components.Wind), SummaryCode: summary,
	}
	return point, validateDailyPointRecord(point)
}

func RangePointRecords(source GridSpec, nice, cycling []RangeRecord, days [][]weather.DailyMetrics) (PointGeometry, []RangePointRecord, error) {
	count := int(source.Rows) * int(source.Cols)
	if len(nice) != count || len(cycling) != count || len(days) != ForecastPointDayCount {
		return PointGeometry{}, nil, errors.New("forecast-window point source geometry or day count is invalid")
	}
	for _, day := range days {
		if len(day) != count {
			return PointGeometry{}, nil, errors.New("forecast-window daily source geometry is invalid")
		}
	}
	sourceRecords := make([]RangePointRecord, count)
	for index := 0; index < count; index++ {
		if !nice[index].Valid || !cycling[index].Valid {
			if nice[index].Valid != cycling[index].Valid {
				return PointGeometry{}, nil, fmt.Errorf("forecast-window profile support differs at source cell %d", index)
			}
			continue
		}
		record := RangePointRecord{Nice: nice[index], Cycling: cycling[index]}
		for dayIndex := range days {
			metrics := days[dayIndex][index]
			if err := metrics.Validate(); err != nil {
				return PointGeometry{}, nil, fmt.Errorf("forecast-window source cell %d day %d: %w", index, dayIndex, err)
			}
			niceScore, cyclingScore := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
			if !niceScore.Available || !cyclingScore.Available {
				return PointGeometry{}, nil, fmt.Errorf("forecast-window source cell %d day %d has no score", index, dayIndex)
			}
			record.NiceDays[dayIndex], record.CyclingDays[dayIndex] = uint8(niceScore.Score), uint8(cyclingScore.Score)
		}
		if err := validateRangePointRecord(record); err != nil {
			return PointGeometry{}, nil, fmt.Errorf("forecast-window source cell %d: %w", index, err)
		}
		sourceRecords[index] = record
	}
	geometry, err := NewPointGeometry(source)
	if err != nil {
		return PointGeometry{}, nil, err
	}
	mapper, err := NewWebMercatorMapper(source)
	if err != nil {
		return PointGeometry{}, nil, err
	}
	result := make([]RangePointRecord, int(geometry.Rows)*int(geometry.Cols))
	for row := uint32(0); row < geometry.Rows; row++ {
		for col := uint32(0); col < geometry.Cols; col++ {
			sourceRow, sourceCol, ok := mapper.Select(int(row), int(col), func(candidateRow, candidateCol uint32) bool {
				return sourceRecords[int(candidateRow)*int(source.Cols)+int(candidateCol)].Nice.Valid
			})
			if ok {
				result[int(row)*int(geometry.Cols)+int(col)] = sourceRecords[int(sourceRow)*int(source.Cols)+int(sourceCol)]
			}
		}
	}
	return geometry, result, nil
}

func WriteDailyPointAsset(path, releaseID string, kind PointKind, geometry PointGeometry, records []DailyPointRecord) (PointMetadata, error) {
	if kind != PointKindForecastDay && kind != PointKindLegacyClimateMonth {
		return PointMetadata{}, errors.New("daily point asset kind must be forecast-day or legacy-climate-month")
	}
	return writePointAsset(path, releaseID, kind, geometry, len(records), func(dst []byte, index int) error {
		if err := validateDailyPointRecord(records[index]); err != nil {
			return err
		}
		encodeDailyPointRecord(dst, records[index])
		return nil
	})
}

func WriteRangePointAsset(path, releaseID string, geometry PointGeometry, records []RangePointRecord) (PointMetadata, error) {
	return writePointAsset(path, releaseID, PointKindForecastWindow, geometry, len(records), func(dst []byte, index int) error {
		if err := validateRangePointRecord(records[index]); err != nil {
			return err
		}
		encodeRangePointRecord(dst, records[index])
		return nil
	})
}

func WriteDisplayScoreAsset(path, releaseID string, geometry PointGeometry, records []DisplayScoreRecord) (PointMetadata, error) {
	return writePointAsset(path, releaseID, PointKindDisplayScore, geometry, len(records), func(dst []byte, index int) error {
		return EncodeDisplayScoreRecord(dst, records[index])
	})
}

type pointEncoder func([]byte, int) error

func writePointAsset(path, releaseID string, kind PointKind, geometry PointGeometry, count int, encode pointEncoder) (PointMetadata, error) {
	recordBytes, err := pointRecordBytes(kind)
	if err != nil {
		return PointMetadata{}, err
	}
	if err := validatePointGeometry(geometry); err != nil {
		return PointMetadata{}, err
	}
	wantCount := uint64(geometry.Rows) * uint64(geometry.Cols)
	if count < 0 || uint64(count) != wantCount || encode == nil {
		return PointMetadata{}, fmt.Errorf("point asset has %d records; want %d", count, wantCount)
	}
	if !releaseNamePattern.MatchString(releaseID) {
		return PointMetadata{}, errors.New("point asset release ID is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PointMetadata{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".point-*.tmp")
	if err != nil {
		return PointMetadata{}, err
	}
	tmpPath, committed := tmp.Name(), false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(make([]byte, PointAssetHeaderBytes)); err != nil {
		return PointMetadata{}, err
	}
	header := encodePointHeader(kind, recordBytes, geometry, nil)
	digest := sha256.New()
	payload := io.MultiWriter(tmp, digest)
	record := make([]byte, recordBytes)
	for index := 0; index < count; index++ {
		clear(record)
		if err := encode(record, index); err != nil {
			return PointMetadata{}, fmt.Errorf("point record %d: %w", index, err)
		}
		if _, err := payload.Write(record); err != nil {
			return PointMetadata{}, err
		}
	}
	payloadChecksum := digest.Sum(nil)
	header = encodePointHeader(kind, recordBytes, geometry, payloadChecksum)
	if _, err := tmp.WriteAt(header, 0); err != nil {
		return PointMetadata{}, err
	}
	if err := tmp.Sync(); err != nil {
		return PointMetadata{}, err
	}
	if err := tmp.Close(); err != nil {
		return PointMetadata{}, err
	}
	file, err := os.Open(tmpPath)
	if err != nil {
		return PointMetadata{}, err
	}
	wholeDigest := sha256.New()
	_, copyErr := io.Copy(wholeDigest, file)
	closeErr := file.Close()
	if copyErr != nil {
		return PointMetadata{}, copyErr
	}
	if closeErr != nil {
		return PointMetadata{}, closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return PointMetadata{}, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return PointMetadata{}, err
	}
	committed = true
	return pointMetadata(kind, recordBytes, geometry, wholeDigest.Sum(nil), payloadChecksum), nil
}

func OpenPointAsset(path string) (*PointAssetReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader := &PointAssetReader{file: file}
	if reader.meta, err = validatePointAsset(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return reader, nil
}

func ValidatePointAsset(path string) (PointMetadata, error) {
	reader, err := OpenPointAsset(path)
	if err != nil {
		return PointMetadata{}, err
	}
	defer reader.Close()
	return reader.Metadata(), nil
}

// ValidatePointAssetRecords is the explicit build/test verifier. Normal
// release opening streams the literal file digest once and validates only a
// requested record, avoiding a full re-score of every Europe texel at startup.
func ValidatePointAssetRecords(path string) (PointMetadata, error) {
	reader, err := OpenPointAsset(path)
	if err != nil {
		return PointMetadata{}, err
	}
	defer reader.Close()
	encoded := make([]byte, reader.meta.RecordBytes)
	count := uint64(reader.meta.Geometry.Rows) * uint64(reader.meta.Geometry.Cols)
	for index := uint64(0); index < count; index++ {
		if _, err := reader.file.ReadAt(encoded, int64(PointAssetHeaderBytes)+int64(index)*int64(reader.meta.RecordBytes)); err != nil {
			return PointMetadata{}, err
		}
		switch reader.meta.Kind {
		case PointKindForecastDay, PointKindLegacyClimateMonth:
			_, err = decodeDailyPointRecord(encoded)
		case PointKindForecastWindow:
			_, err = decodeRangePointRecord(encoded)
		case PointKindDisplayScore:
			_, err = DecodeDisplayScoreRecord(encoded)
		default:
			err = errors.New("unsupported point asset kind")
		}
		if err != nil {
			return PointMetadata{}, fmt.Errorf("point record %d: %w", index, err)
		}
	}
	return reader.Metadata(), nil
}

func (reader *PointAssetReader) DisplayScoreAt(lon, lat float64) (DisplayScoreRecord, bool, error) {
	if reader.meta.Kind != PointKindDisplayScore {
		return DisplayScoreRecord{}, false, errors.New("point asset does not contain display-score records")
	}
	index, ok := pointIndex(reader.meta.Geometry, lon, lat)
	if !ok {
		return DisplayScoreRecord{}, false, nil
	}
	encoded := make([]byte, reader.meta.RecordBytes)
	if _, err := reader.file.ReadAt(encoded, int64(PointAssetHeaderBytes)+int64(index)*int64(reader.meta.RecordBytes)); err != nil {
		return DisplayScoreRecord{}, false, err
	}
	record, err := DecodeDisplayScoreRecord(encoded)
	return record, record.Valid, err
}

func (reader *PointAssetReader) Close() error            { return reader.file.Close() }
func (reader *PointAssetReader) Metadata() PointMetadata { return reader.meta }

func (reader *PointAssetReader) DailyAt(lon, lat float64) (DailyPointRecord, bool, error) {
	if reader.meta.Kind != PointKindForecastDay && reader.meta.Kind != PointKindLegacyClimateMonth {
		return DailyPointRecord{}, false, errors.New("point asset does not contain daily records")
	}
	index, ok := pointIndex(reader.meta.Geometry, lon, lat)
	if !ok {
		return DailyPointRecord{}, false, nil
	}
	encoded := make([]byte, reader.meta.RecordBytes)
	if _, err := reader.file.ReadAt(encoded, int64(PointAssetHeaderBytes)+int64(index)*int64(reader.meta.RecordBytes)); err != nil {
		return DailyPointRecord{}, false, err
	}
	record, err := decodeDailyPointRecord(encoded)
	return record, record.Weather.Valid, err
}

func (reader *PointAssetReader) RangeAt(lon, lat float64) (RangePointRecord, bool, error) {
	if reader.meta.Kind != PointKindForecastWindow {
		return RangePointRecord{}, false, errors.New("point asset does not contain forecast-window records")
	}
	index, ok := pointIndex(reader.meta.Geometry, lon, lat)
	if !ok {
		return RangePointRecord{}, false, nil
	}
	encoded := make([]byte, reader.meta.RecordBytes)
	if _, err := reader.file.ReadAt(encoded, int64(PointAssetHeaderBytes)+int64(index)*int64(reader.meta.RecordBytes)); err != nil {
		return RangePointRecord{}, false, err
	}
	record, err := decodeRangePointRecord(encoded)
	return record, record.Nice.Valid, err
}

func pointIndex(geometry PointGeometry, lon, lat float64) (uint64, bool) {
	if !finite(lon) || !finite(lat) || lon < -180 || lon > 180 || lat < -webMercatorMaxLatitude || lat > webMercatorMaxLatitude {
		return 0, false
	}
	x, y := webMercatorX(lon), normalizedWebMercatorY(lat)
	q := geometry.Quad
	if x < q.West || x > q.East || y < q.North || y > q.South {
		return 0, false
	}
	col := int(math.Floor((x - q.West) / (q.East - q.West) * float64(geometry.Cols)))
	row := int(math.Floor((y - q.North) / (q.South - q.North) * float64(geometry.Rows)))
	if col == int(geometry.Cols) {
		col--
	}
	if row == int(geometry.Rows) {
		row--
	}
	if row < 0 || col < 0 || row >= int(geometry.Rows) || col >= int(geometry.Cols) {
		return 0, false
	}
	return uint64(row)*uint64(geometry.Cols) + uint64(col), true
}

func validatePointAsset(file *os.File) (PointMetadata, error) {
	var header [PointAssetHeaderBytes]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return PointMetadata{}, fmt.Errorf("read point asset header: %w", err)
	}
	meta, err := decodePointHeader(header[:])
	if err != nil {
		return PointMetadata{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return PointMetadata{}, err
	}
	if uint64(info.Size()) != meta.Bytes {
		return PointMetadata{}, fmt.Errorf("point asset file size is %d; want %d", info.Size(), meta.Bytes)
	}
	payloadDigest := sha256.New()
	wholeDigest := sha256.New()
	if _, err := wholeDigest.Write(header[:]); err != nil {
		return PointMetadata{}, err
	}
	if _, err := io.Copy(io.MultiWriter(payloadDigest, wholeDigest), io.NewSectionReader(file, PointAssetHeaderBytes, info.Size()-PointAssetHeaderBytes)); err != nil {
		return PointMetadata{}, err
	}
	if hex.EncodeToString(payloadDigest.Sum(nil)) != meta.PayloadSHA256 {
		return PointMetadata{}, errors.New("point asset payload checksum mismatch")
	}
	meta.SHA256 = hex.EncodeToString(wholeDigest.Sum(nil))
	return meta, nil
}

func encodePointHeader(kind PointKind, recordBytes uint16, geometry PointGeometry, checksum []byte) []byte {
	header := make([]byte, PointAssetHeaderBytes)
	copy(header[:8], pointMagic[:])
	binary.LittleEndian.PutUint16(header[8:10], PointAssetVersion)
	binary.LittleEndian.PutUint16(header[10:12], PointAssetHeaderBytes)
	binary.LittleEndian.PutUint16(header[12:14], recordBytes)
	header[14], header[15] = byte(kind), pointProjectionWebMercatorImage
	binary.LittleEndian.PutUint32(header[16:20], geometry.Rows)
	binary.LittleEndian.PutUint32(header[20:24], geometry.Cols)
	putFloat64(header[24:32], geometry.Bounds.West)
	putFloat64(header[32:40], geometry.Bounds.East)
	putFloat64(header[40:48], geometry.Bounds.South)
	putFloat64(header[48:56], geometry.Bounds.North)
	putFloat64(header[56:64], geometry.Quad.West)
	putFloat64(header[64:72], geometry.Quad.East)
	putFloat64(header[72:80], geometry.Quad.North)
	putFloat64(header[80:88], geometry.Quad.South)
	copy(header[88:120], checksum)
	return header
}

func decodePointHeader(header []byte) (PointMetadata, error) {
	if len(header) != PointAssetHeaderBytes || string(header[:8]) != string(pointMagic[:]) {
		return PointMetadata{}, errors.New("invalid point asset magic")
	}
	if version := binary.LittleEndian.Uint16(header[8:10]); version != PointAssetVersion {
		return PointMetadata{}, fmt.Errorf("unsupported point asset version %d", version)
	}
	if binary.LittleEndian.Uint16(header[10:12]) != PointAssetHeaderBytes || header[15] != pointProjectionWebMercatorImage {
		return PointMetadata{}, errors.New("unsupported point asset layout or projection")
	}
	if !allZero(header[120:128]) {
		return PointMetadata{}, errors.New("point asset header reserved bytes are not zero")
	}
	kind := PointKind(header[14])
	recordBytes, err := pointRecordBytes(kind)
	if err != nil || binary.LittleEndian.Uint16(header[12:14]) != recordBytes {
		return PointMetadata{}, errors.New("point asset record size does not match its kind")
	}
	geometry := PointGeometry{
		Rows: binary.LittleEndian.Uint32(header[16:20]), Cols: binary.LittleEndian.Uint32(header[20:24]),
		Bounds: GridBounds{West: getFloat64(header[24:32]), East: getFloat64(header[32:40]), South: getFloat64(header[40:48]), North: getFloat64(header[48:56])},
		Quad:   PointQuad{West: getFloat64(header[56:64]), East: getFloat64(header[64:72]), North: getFloat64(header[72:80]), South: getFloat64(header[80:88])},
	}
	if err := validatePointGeometry(geometry); err != nil {
		return PointMetadata{}, err
	}
	count := uint64(geometry.Rows) * uint64(geometry.Cols)
	if count > (math.MaxUint64-PointAssetHeaderBytes)/uint64(recordBytes) {
		return PointMetadata{}, errors.New("point asset is too large")
	}
	return PointMetadata{
		Version: PointAssetVersion, Kind: kind, Geometry: geometry, HeaderBytes: PointAssetHeaderBytes, RecordBytes: recordBytes,
		Bytes: PointAssetHeaderBytes + count*uint64(recordBytes), PayloadSHA256: hex.EncodeToString(header[88:120]),
	}, nil
}

func pointMetadata(kind PointKind, recordBytes uint16, geometry PointGeometry, checksum, payloadChecksum []byte) PointMetadata {
	return PointMetadata{
		Version: PointAssetVersion, Kind: kind, Geometry: geometry, HeaderBytes: PointAssetHeaderBytes, RecordBytes: recordBytes,
		Bytes:  PointAssetHeaderBytes + uint64(geometry.Rows)*uint64(geometry.Cols)*uint64(recordBytes),
		SHA256: hex.EncodeToString(checksum), PayloadSHA256: hex.EncodeToString(payloadChecksum),
	}
}

func PointAssetFromMetadata(url string, metadata PointMetadata) PointAsset {
	return PointAsset{
		URL: url, SHA256: metadata.SHA256, PayloadSHA256: metadata.PayloadSHA256, Bytes: metadata.Bytes, Kind: PointKindName(metadata.Kind),
		HeaderBytes: metadata.HeaderBytes, RecordBytes: metadata.RecordBytes, Projection: "web-mercator-image", Geometry: metadata.Geometry,
	}
}

func ValidatePointAssetDescriptor(asset PointAsset, releaseID string, expectedKind PointKind) error {
	if err := validateAssetPath(asset.URL); err != nil || !strings.HasPrefix(asset.URL, "points/") || !strings.HasSuffix(asset.URL, ".nwp") {
		return errors.New("point asset URL must be a clean points path")
	}
	if !validSHA256(asset.SHA256) || !validSHA256(asset.PayloadSHA256) || !strings.Contains(filepath.Base(asset.URL), strings.ToLower(asset.SHA256)) {
		return errors.New("point asset URL must contain its SHA-256")
	}
	if asset.Kind != PointKindName(expectedKind) || asset.HeaderBytes != PointAssetHeaderBytes {
		return errors.New("point asset kind or header size is invalid")
	}
	recordBytes, err := pointRecordBytes(expectedKind)
	if err != nil || asset.RecordBytes != recordBytes || asset.Projection != "web-mercator-image" {
		return errors.New("point asset record size or projection is invalid")
	}
	if err := validatePointGeometry(asset.Geometry); err != nil {
		return err
	}
	wantBytes := uint64(PointAssetHeaderBytes) + uint64(asset.Geometry.Rows)*uint64(asset.Geometry.Cols)*uint64(recordBytes)
	if asset.Bytes != wantBytes {
		return errors.New("point asset byte size does not match its geometry")
	}
	if !releaseNamePattern.MatchString(releaseID) {
		return errors.New("point asset release ID is invalid")
	}
	return nil
}

func validateDailyPointRecord(record DailyPointRecord) error {
	if !record.Weather.Valid {
		if record != (DailyPointRecord{}) {
			return errors.New("invalid daily point record must contain only zero values")
		}
		return nil
	}
	if err := validateRecord(record.Weather); err != nil {
		return err
	}
	for name, value := range map[string]uint8{"thermal": record.Thermal, "dryness": record.Dryness, "sun": record.Sun, "nice score": record.NiceScore, "nice wind": record.NiceWind, "cycling score": record.CyclingScore, "cycling wind": record.CyclingWind} {
		if value > 100 {
			return fmt.Errorf("daily point %s exceeds 100", name)
		}
	}
	metrics, err := record.Weather.WeatherMetrics(1)
	if err != nil {
		return err
	}
	nice, cycling := weather.Score(metrics, weather.ProfileNice), weather.Score(metrics, weather.ProfileCycling)
	if int(record.Thermal) != nice.Components.Thermal || int(record.Dryness) != nice.Components.Dryness || int(record.Sun) != nice.Components.Sun || int(record.NiceScore) != nice.Score || int(record.NiceWind) != nice.Components.Wind || int(record.CyclingScore) != cycling.Score || int(record.CyclingWind) != cycling.Components.Wind {
		return errors.New("daily point scores do not match its physical weather")
	}
	summary, err := weather.EncodeSummary(metrics)
	if err != nil || summary != record.SummaryCode {
		return errors.New("daily point summary does not match its physical weather")
	}
	return nil
}

func validateDisplayScoreRecord(record DisplayScoreRecord) error {
	if !record.Valid {
		if record != (DisplayScoreRecord{}) {
			return errors.New("invalid display-score record must contain only zero values")
		}
		return nil
	}
	for name, value := range map[string]uint8{"thermal": record.Thermal, "dryness": record.Dryness, "sun": record.Sun, "nice score": record.NiceScore, "nice wind": record.NiceWind, "cycling score": record.CyclingScore, "cycling wind": record.CyclingWind} {
		if value > 100 {
			return fmt.Errorf("display-score %s exceeds 100", name)
		}
	}
	return nil
}

// EncodeDisplayScoreRecord is the one fixed-record encoder shared by point
// assets and viewport tiles.
func EncodeDisplayScoreRecord(dst []byte, record DisplayScoreRecord) error {
	if len(dst) != DisplayScoreRecordBytes {
		return errors.New("invalid display-score record size")
	}
	if err := validateDisplayScoreRecord(record); err != nil {
		return err
	}
	clear(dst)
	if !record.Valid {
		return nil
	}
	dst[0], dst[1], dst[2] = record.Thermal, record.Dryness, record.Sun
	dst[3], dst[4] = record.NiceScore, record.NiceWind
	dst[5], dst[6], dst[7] = record.CyclingScore, record.CyclingWind, 1
	return nil
}

// DecodeDisplayScoreRecord decodes the canonical fixed display record.
func DecodeDisplayScoreRecord(src []byte) (DisplayScoreRecord, error) {
	if len(src) != DisplayScoreRecordBytes {
		return DisplayScoreRecord{}, errors.New("invalid display-score record size")
	}
	if src[7] == 0 {
		if !allZero(src) {
			return DisplayScoreRecord{}, errors.New("invalid display-score record contains data")
		}
		return DisplayScoreRecord{}, nil
	}
	if src[7] != 1 {
		return DisplayScoreRecord{}, errors.New("display-score record contains unknown flags")
	}
	record := DisplayScoreRecord{Thermal: src[0], Dryness: src[1], Sun: src[2], NiceScore: src[3], NiceWind: src[4], CyclingScore: src[5], CyclingWind: src[6], Valid: true}
	return record, validateDisplayScoreRecord(record)
}

func validateRangePointRecord(record RangePointRecord) error {
	if !record.Nice.Valid {
		if record != (RangePointRecord{}) {
			return errors.New("invalid forecast-window point record must contain only zero values")
		}
		return nil
	}
	if !record.Cycling.Valid {
		return errors.New("forecast-window point profiles must share support")
	}
	if err := validateRangeRecord(record.Nice); err != nil {
		return fmt.Errorf("nice profile: %w", err)
	}
	if err := validateRangeRecord(record.Cycling); err != nil {
		return fmt.Errorf("cycling profile: %w", err)
	}
	if record.Nice.DayCount != ForecastPointDayCount || record.Cycling.DayCount != ForecastPointDayCount || record.Nice.Thermal != record.Cycling.Thermal || record.Nice.Dryness != record.Cycling.Dryness || record.Nice.Sun != record.Cycling.Sun {
		return errors.New("forecast-window point profiles have inconsistent shared evidence")
	}
	for index := 0; index < ForecastPointDayCount; index++ {
		if record.NiceDays[index] > 100 || record.CyclingDays[index] > 100 {
			return fmt.Errorf("forecast-window point day %d score exceeds 100", index)
		}
	}
	if err := validateRangeFingerprint(record.Nice, record.NiceDays); err != nil {
		return fmt.Errorf("nice fingerprint: %w", err)
	}
	if err := validateRangeFingerprint(record.Cycling, record.CyclingDays); err != nil {
		return fmt.Errorf("cycling fingerprint: %w", err)
	}
	return nil
}

func validateRangeFingerprint(summary RangeRecord, scores [ForecastPointDayCount]uint8) error {
	days := make([]weather.Result, ForecastPointDayCount)
	components := weather.Components{Thermal: int(summary.Thermal), Dryness: int(summary.Dryness), Sun: int(summary.Sun), Wind: int(summary.Wind)}
	for index, score := range scores {
		value := int(score)
		days[index] = weather.Result{Available: true, Score: value, Rating: weather.Rating(value), Components: components}
	}
	got, err := weather.SummarizeRange(days)
	if err != nil {
		return err
	}
	if int(summary.Score) != got.Score || int(summary.MedianScore) != got.MedianScore || int(summary.NiceDays) != got.NiceDays || int(summary.AwfulDays) != got.AwfulDays || int(summary.LongestPoorRun) != got.LongestPoorRun || components != got.Components {
		return errors.New("aggregate does not match its 14 daily scores")
	}
	return nil
}

func encodeDailyPointRecord(dst []byte, record DailyPointRecord) {
	if !record.Weather.Valid {
		return
	}
	encodeRecord(dst[:dailyWeatherRecordBytes], record.Weather)
	dst[22], dst[23], dst[24] = record.Thermal, record.Dryness, record.Sun
	dst[25], dst[26] = record.NiceScore, record.NiceWind
	dst[27], dst[28] = record.CyclingScore, record.CyclingWind
	dst[29] = record.SummaryCode
}

func decodeDailyPointRecord(src []byte) (DailyPointRecord, error) {
	if len(src) != DailyPointRecordBytes {
		return DailyPointRecord{}, errors.New("invalid daily point record size")
	}
	weatherRecord, err := decodeRecord(src[:dailyWeatherRecordBytes])
	if err != nil {
		return DailyPointRecord{}, err
	}
	if !weatherRecord.Valid {
		if !allZero(src) {
			return DailyPointRecord{}, errors.New("invalid daily point record contains data")
		}
		return DailyPointRecord{}, nil
	}
	record := DailyPointRecord{
		Weather: weatherRecord, Thermal: src[22], Dryness: src[23], Sun: src[24], NiceScore: src[25], NiceWind: src[26], CyclingScore: src[27], CyclingWind: src[28], SummaryCode: src[29],
	}
	return record, validateDailyPointRecord(record)
}

func encodeRangePointRecord(dst []byte, record RangePointRecord) {
	if !record.Nice.Valid {
		return
	}
	dst[0], dst[1], dst[2] = record.Nice.Thermal, record.Nice.Dryness, record.Nice.Sun
	dst[3], dst[4], dst[5], dst[6], dst[7], dst[8] = record.Nice.Score, record.Nice.MedianScore, record.Nice.Wind, record.Nice.NiceDays, record.Nice.AwfulDays, record.Nice.LongestPoorRun
	dst[9], dst[10], dst[11], dst[12], dst[13], dst[14] = record.Cycling.Score, record.Cycling.MedianScore, record.Cycling.Wind, record.Cycling.NiceDays, record.Cycling.AwfulDays, record.Cycling.LongestPoorRun
	copy(dst[15:29], record.NiceDays[:])
	copy(dst[29:43], record.CyclingDays[:])
	dst[43] = 1
}

func decodeRangePointRecord(src []byte) (RangePointRecord, error) {
	if len(src) != RangePointRecordBytes {
		return RangePointRecord{}, errors.New("invalid forecast-window point record size")
	}
	if src[43] == 0 {
		if !allZero(src) {
			return RangePointRecord{}, errors.New("invalid forecast-window point record contains data")
		}
		return RangePointRecord{}, nil
	}
	if src[43] != 1 {
		return RangePointRecord{}, errors.New("forecast-window point record contains unknown flags")
	}
	record := RangePointRecord{
		Nice:    RangeRecord{Thermal: src[0], Dryness: src[1], Sun: src[2], Score: src[3], MedianScore: src[4], Wind: src[5], NiceDays: src[6], AwfulDays: src[7], LongestPoorRun: src[8], DayCount: ForecastPointDayCount, Valid: true},
		Cycling: RangeRecord{Thermal: src[0], Dryness: src[1], Sun: src[2], Score: src[9], MedianScore: src[10], Wind: src[11], NiceDays: src[12], AwfulDays: src[13], LongestPoorRun: src[14], DayCount: ForecastPointDayCount, Valid: true},
	}
	copy(record.NiceDays[:], src[15:29])
	copy(record.CyclingDays[:], src[29:43])
	return record, validateRangePointRecord(record)
}
