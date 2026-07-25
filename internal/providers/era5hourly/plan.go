package era5hourly

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

const (
	planSchemaVersion = 1
	maxPlanBytes      = 8 * 1024 * 1024
	maxPlanGzipBytes  = 2 * 1024 * 1024

	planCellRegistration     = "exact-era5-grid-point-centres"
	planLandAuthority        = "Natural Earth 5.1.1 1:10m polygon intersects 0.25 degree ERA5 source-cell footprint"
	planLandFilter           = "featurecla IS NULL OR featurecla = 'Land'"
	planTimezoneAuthority    = "github.com/ringsaturn/tzf-dist@v0.0.2026-b-fix1"
	planCentreLandDerivation = "Natural Earth 5.1.1 featurecla Land point intersection at exact ERA5 grid-point centres v1"

	// CentreLandMaskDerivation is the canonical display-mask derivation which
	// a separately trusted publication authority must name exactly.
	CentreLandMaskDerivation = planCentreLandDerivation
)

// CellPlan is an opaque validated ordered set of logical land cells. It can
// only be created by LoadCellPlan.
type CellPlan struct {
	year, month          int
	cells                []Cell
	sha256               string
	logicalSHA256        string
	landCells            int
	centreLandCells      int
	timezones            int
	landSourceSHA256     string
	centreLandMaskSHA256 string
	expectedDays         []uint16
}

func (plan CellPlan) Year() int                { return plan.year }
func (plan CellPlan) Month() int               { return plan.month }
func (plan CellPlan) SHA256() string           { return plan.sha256 }
func (plan CellPlan) LogicalSHA256() string    { return plan.logicalSHA256 }
func (plan CellPlan) LandSourceSHA256() string { return plan.landSourceSHA256 }
func (plan CellPlan) LandCellCount() int       { return plan.landCells }
func (plan CellPlan) CentreLandCellCount() int { return plan.centreLandCells }
func (plan CellPlan) TimezoneCount() int       { return plan.timezones }
func (plan CellPlan) CellCount() int           { return len(plan.cells) }

// DisplayMaskAuthority returns the exact source and canonical derivation
// bound into the checksum-trusted plan. A false result means this older plan
// cannot authorise climate display publication.
func (plan CellPlan) DisplayMaskAuthority() (landSourceSHA256, derivation, maskSHA256 string, centreLandCells int, ok bool) {
	if plan.landSourceSHA256 == "" || plan.centreLandMaskSHA256 == "" {
		return "", "", "", 0, false
	}
	return plan.landSourceSHA256, planCentreLandDerivation, plan.centreLandMaskSHA256, plan.centreLandCells, true
}

// ExpectedDayCount returns the exact number of valid civil local daytime
// records for one plan cell and month across 2006–2025.
func (plan CellPlan) ExpectedDayCount(cellIndex int) (int, bool) {
	if cellIndex < 0 || cellIndex >= len(plan.expectedDays) {
		return 0, false
	}
	return int(plan.expectedDays[cellIndex]), true
}

// Cells returns a copy so callers cannot mutate the validated plan.
func (plan CellPlan) Cells() []Cell {
	return append([]Cell(nil), plan.cells...)
}

type encodedCellPlan struct {
	SchemaVersion  int     `json:"schemaVersion"`
	Year           int     `json:"year"`
	Month          int     `json:"month"`
	Rows           int     `json:"rows"`
	Columns        int     `json:"columns"`
	GridDegrees    float64 `json:"gridDegrees"`
	FirstLatitude  float64 `json:"firstLatitude"`
	FirstLongitude float64 `json:"firstLongitude"`

	CellRegistration         string `json:"cellRegistration"`
	LandAuthority            string `json:"landAuthority"`
	LandFeatureFilter        string `json:"landFeatureFilter"`
	LandFeatureClassAudit    string `json:"landFeatureClassAudit"`
	LandSourceSHA256         string `json:"landSourceSha256"`
	CentreLandMaskDerivation string `json:"centreLandMaskDerivation"`
	CentreLandMaskSHA256     string `json:"centreLandMaskSha256"`
	TimezoneAuthority        string `json:"timezoneAuthority"`

	LandCells                    int `json:"landCells"`
	WaterCells                   int `json:"waterCells"`
	CentreLandCells              int `json:"centreLandCells"`
	CoastSupportCells            int `json:"coastSupportCells"`
	PositiveDatelineSupportRows  int `json:"positiveDatelineSupportRows"`
	PositiveDatelineWrappedCells int `json:"positiveDatelineWrappedCells"`
	NegativeDatelineSupportRows  int `json:"negativeDatelineSupportRows"`
	PositiveDatelineWestOnlyRows int `json:"positiveDatelineWestOnlyRows"`
	PositiveDatelineEastOnlyRows int `json:"positiveDatelineEastOnlyRows"`
	PositiveDatelineBothRows     int `json:"positiveDatelineBothRows"`
	WestEdgeLandCells            int `json:"westEdgeLandCells"`
	EastEdgeLandCells            int `json:"eastEdgeLandCells"`
	CoastTimezoneResolvedCells   int `json:"coastTimezoneResolvedCells"`
	NonAntarcticFixedOffsetCells int `json:"nonAntarcticFixedOffsetLandCells"`

	LandMaskSHA256      string   `json:"landMaskSha256"`
	TimezoneIndexSHA256 string   `json:"timezoneIndexSha256"`
	LandMaskBase64      string   `json:"landMaskBase64"`
	TimezoneIndexBase64 string   `json:"timezoneIndexBase64"`
	Timezones           []string `json:"timezones"`

	SupportAudits                  []supportAudit          `json:"supportAudits"`
	ExclusionAudits                []supportAudit          `json:"exclusionAudits"`
	UnzonedRemoteIslandFixedAudits []fixedOffsetAudit      `json:"unzonedRemoteIslandFixedOffsetAudits"`
	RemoteIslandNamedZoneAudits    []namedZoneAudit        `json:"remoteIslandNamedZoneAudits"`
	PositiveDatelineTimezoneAudits []positiveDatelineAudit `json:"positiveDatelineTimezoneAudits"`
}

type supportAudit struct {
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Row       int     `json:"row"`
	Column    int     `json:"column"`
	Supported bool    `json:"supported"`
}

type fixedOffsetAudit struct {
	Row                   int     `json:"row"`
	Column                int     `json:"column"`
	Latitude              float64 `json:"latitude"`
	Longitude             float64 `json:"longitude"`
	Timezone              string  `json:"timezone"`
	LandIdentity          string  `json:"landIdentity"`
	EvidenceClass         string  `json:"evidenceClass"`
	UTCOffsetHours        float64 `json:"utcOffsetHours"`
	SolarOffsetDeltaHours float64 `json:"solarOffsetDeltaHours"`
	Reason                string  `json:"reason"`
	CentreLand            bool    `json:"centreLand"`
}

type namedZoneAudit struct {
	Row                           int     `json:"row"`
	Column                        int     `json:"column"`
	Latitude                      float64 `json:"latitude"`
	Longitude                     float64 `json:"longitude"`
	LandIdentity                  string  `json:"landIdentity"`
	Timezone                      string  `json:"timezone"`
	EvidenceClass                 string  `json:"evidenceClass"`
	EvaluationMonthUTCOffsetHours float64 `json:"evaluationMonthUtcOffsetHours"`
	SolarOffsetDeltaHours         float64 `json:"solarOffsetDeltaHours"`
	Reason                        string  `json:"reason"`
	CentreLand                    bool    `json:"centreLand"`
}

type positiveDatelineAudit struct {
	Row              int     `json:"row"`
	Latitude         float64 `json:"latitude"`
	WestSupported    bool    `json:"westSupported"`
	EastSupported    bool    `json:"eastSupported"`
	MainTimezone     string  `json:"mainTimezone"`
	PositiveTimezone string  `json:"positiveTimezone"`
}

// LoadCellPlan validates a bounded gzip JSON plan against its separately
// trusted compressed checksum and reconstructs its exact row-major cells.
func LoadCellPlan(path, expectedCompressedSHA256 string) (CellPlan, error) {
	var result CellPlan
	compressed, err := readBoundedFile(path, maxPlanGzipBytes)
	if err != nil {
		return result, fmt.Errorf("read ERA5 cell plan: %w", err)
	}
	compressedSHA := sha256.Sum256(compressed)
	compressedSHAHex := hex.EncodeToString(compressedSHA[:])
	expected, err := parseSHA256(expectedCompressedSHA256)
	if err != nil {
		return result, fmt.Errorf("ERA5 cell plan expected checksum: %w", err)
	}
	if !bytes.Equal(expected[:], compressedSHA[:]) {
		return result, errors.New("ERA5 cell plan compressed checksum mismatch")
	}
	decoded, err := decodePlanGzip(compressed)
	if err != nil {
		return result, err
	}
	var encoded encodedCellPlan
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return result, fmt.Errorf("decode ERA5 cell plan: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return result, err
	}
	cells, err := encoded.validateAndCells()
	if err != nil {
		return result, err
	}
	expectedDays, err := expectedPlanDays(cells, time.Month(encoded.Month))
	if err != nil {
		return result, err
	}
	return CellPlan{
		year: encoded.Year, month: encoded.Month, cells: cells,
		sha256: compressedSHAHex, logicalSHA256: logicalPlanSHA256(encoded.Year, encoded.Month, cells, expectedDays),
		landCells:       encoded.LandCells,
		centreLandCells: encoded.CentreLandCells,
		timezones:       len(encoded.Timezones), landSourceSHA256: encoded.LandSourceSHA256,
		centreLandMaskSHA256: encoded.CentreLandMaskSHA256, expectedDays: expectedDays,
	}, nil
}

// logicalPlanSHA256 fingerprints the validated plan semantics used by daily
// shards and climate publication. It is independent of JSON and gzip encoding.
func logicalPlanSHA256(year, month int, cells []Cell, expectedDays []uint16) string {
	digest := sha256.New()
	writeString := func(value string) {
		var length [4]byte
		binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	writeUint64 := func(value uint64) {
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], value)
		_, _ = digest.Write(encoded[:])
	}
	writeString("nice-weather-era5-logical-cell-plan-v1")
	writeUint64(uint64(year))
	writeUint64(uint64(month))
	writeString(TimezoneContractVersion)
	writeUint64(uint64(len(cells)))
	for index, cell := range cells {
		writeUint64(uint64(cell.SourceIndex))
		writeUint64(math.Float64bits(cell.Latitude))
		writeUint64(math.Float64bits(cell.Longitude))
		writeString(cell.TimezoneID)
		writeString(cell.TimezoneFingerprint)
		writeUint64(uint64(expectedDays[index]))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	if path == "" {
		return nil, errors.New("path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("file size %d is outside 1..%d bytes", info.Size(), maximum)
	}
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) != info.Size() {
		return nil, errors.New("file changed while it was read")
	}
	return content, nil
}

func decodePlanGzip(compressed []byte) ([]byte, error) {
	input := bytes.NewReader(compressed)
	reader, err := gzip.NewReader(input)
	if err != nil {
		return nil, fmt.Errorf("open ERA5 cell plan gzip: %w", err)
	}
	reader.Multistream(false)
	decoded, readErr := io.ReadAll(io.LimitReader(reader, maxPlanBytes+1))
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read ERA5 cell plan gzip: %w", err)
	}
	if len(decoded) == 0 || len(decoded) > maxPlanBytes {
		return nil, fmt.Errorf("ERA5 cell plan expands outside 1..%d bytes", maxPlanBytes)
	}
	if input.Len() != 0 {
		return nil, errors.New("ERA5 cell plan gzip has trailing data or multiple members")
	}
	return decoded, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("ERA5 cell plan contains multiple JSON values")
		}
		return fmt.Errorf("finish ERA5 cell plan JSON: %w", err)
	}
	return nil
}

func (plan encodedCellPlan) validateAndCells() ([]Cell, error) {
	if plan.SchemaVersion != planSchemaVersion ||
		plan.Year < climateFirstYear || plan.Year > climateLastYear ||
		plan.Month < int(timeJanuary) || plan.Month > int(timeDecember) {
		return nil, errors.New("ERA5 cell plan version or period is unsupported")
	}
	if plan.Rows != GridRows || plan.Columns != GridColumns ||
		plan.GridDegrees != .25 || plan.FirstLatitude != 90 || plan.FirstLongitude != -180 ||
		plan.CellRegistration != planCellRegistration {
		return nil, errors.New("ERA5 cell plan geometry changed")
	}
	if plan.LandAuthority != planLandAuthority ||
		plan.LandFeatureFilter != planLandFilter ||
		plan.LandFeatureClassAudit == "" ||
		plan.TimezoneAuthority != planTimezoneAuthority {
		return nil, errors.New("ERA5 cell plan land or timezone authority changed")
	}
	if _, err := parseSHA256(plan.LandSourceSHA256); err != nil {
		return nil, fmt.Errorf("ERA5 cell plan land source checksum: %w", err)
	}
	if (plan.CentreLandMaskDerivation == "") != (plan.CentreLandMaskSHA256 == "") {
		return nil, errors.New("ERA5 cell plan centre-land authority is incomplete")
	}
	if plan.CentreLandMaskDerivation != "" {
		if plan.CentreLandMaskDerivation != planCentreLandDerivation {
			return nil, errors.New("ERA5 cell plan centre-land derivation changed")
		}
		if _, err := parseSHA256(plan.CentreLandMaskSHA256); err != nil {
			return nil, fmt.Errorf("ERA5 cell plan centre-land mask checksum: %w", err)
		}
	}
	totalCells := GridRows * GridColumns
	declaredCounts := []int{
		plan.LandCells, plan.WaterCells, plan.CentreLandCells, plan.CoastSupportCells,
		plan.PositiveDatelineSupportRows, plan.PositiveDatelineWrappedCells,
		plan.NegativeDatelineSupportRows, plan.PositiveDatelineWestOnlyRows,
		plan.PositiveDatelineEastOnlyRows, plan.PositiveDatelineBothRows,
		plan.WestEdgeLandCells, plan.EastEdgeLandCells,
		plan.CoastTimezoneResolvedCells, plan.NonAntarcticFixedOffsetCells,
	}
	for _, count := range declaredCounts {
		if count < 0 || count > totalCells {
			return nil, errors.New("ERA5 cell plan contains an impossible declared count")
		}
	}
	if plan.PositiveDatelineSupportRows > GridRows ||
		plan.LandCells > totalCells-plan.PositiveDatelineSupportRows {
		return nil, errors.New("ERA5 cell plan logical cell count is impossible")
	}
	mask, err := decodePlanPlane("land mask", plan.LandMaskBase64, GridRows*GridColumns, plan.LandMaskSHA256)
	if err != nil {
		return nil, err
	}
	timezoneBytes, err := decodePlanPlane("timezone index", plan.TimezoneIndexBase64, GridRows*GridColumns*2, plan.TimezoneIndexSHA256)
	if err != nil {
		return nil, err
	}
	if err := validateTimezoneNames(plan.Timezones); err != nil {
		return nil, err
	}
	fingerprints := make([]string, len(plan.Timezones))
	for index, name := range plan.Timezones {
		fingerprint, err := TimezoneFingerprint(name)
		if err != nil {
			return nil, fmt.Errorf("ERA5 cell plan timezone %d: %w", index, err)
		}
		fingerprints[index] = fingerprint
	}

	land, westEdge, eastEdge := 0, 0, 0
	cells := make([]Cell, 0, plan.LandCells+plan.PositiveDatelineSupportRows)
	for row := 0; row < GridRows; row++ {
		for column := 0; column < GridColumns; column++ {
			index := row*GridColumns + column
			maskValue := mask[index]
			timezoneIndex := int(binary.LittleEndian.Uint16(timezoneBytes[index*2:]))
			if maskValue != 0 && maskValue != 1 {
				return nil, fmt.Errorf("ERA5 cell plan land mask value %d at %d is invalid", maskValue, index)
			}
			if maskValue == 0 {
				if timezoneIndex != 0 {
					return nil, fmt.Errorf("ERA5 cell plan water cell %d has a timezone", index)
				}
				continue
			}
			if timezoneIndex >= len(plan.Timezones) {
				return nil, fmt.Errorf("ERA5 cell plan timezone index %d at cell %d is invalid", timezoneIndex, index)
			}
			land++
			if column == 0 {
				westEdge++
			}
			if column == GridColumns-1 {
				eastEdge++
			}
			longitude := plan.FirstLongitude + float64(column)*plan.GridDegrees
			sourceColumn := (column + GridColumns/2) % GridColumns
			cells = append(cells, Cell{
				SourceIndex:         row*GridColumns + sourceColumn,
				Latitude:            plan.FirstLatitude - float64(row)*plan.GridDegrees,
				Longitude:           longitude,
				TimezoneID:          plan.Timezones[timezoneIndex],
				TimezoneFingerprint: fingerprints[timezoneIndex],
			})
		}
	}
	if land != plan.LandCells || plan.WaterCells != GridRows*GridColumns-land ||
		plan.CentreLandCells < 0 || plan.CoastSupportCells < 0 ||
		plan.CentreLandCells+plan.CoastSupportCells != land ||
		westEdge != plan.WestEdgeLandCells || eastEdge != plan.EastEdgeLandCells {
		return nil, errors.New("ERA5 cell plan land, coast or edge counts disagree with its planes")
	}
	if plan.CoastTimezoneResolvedCells < 0 || plan.CoastTimezoneResolvedCells > plan.CoastSupportCells ||
		plan.NonAntarcticFixedOffsetCells != len(plan.UnzonedRemoteIslandFixedAudits) {
		return nil, errors.New("ERA5 cell plan coast timezone audit counts disagree")
	}
	if err := validateSupportAudits(plan.SupportAudits, mask, true); err != nil {
		return nil, err
	}
	if err := validateSupportAudits(plan.ExclusionAudits, mask, false); err != nil {
		return nil, err
	}
	if err := validateSpatialTimezoneAudits(plan.UnzonedRemoteIslandFixedAudits, plan.Timezones, timezoneBytes, mask); err != nil {
		return nil, err
	}
	if err := validateNamedTimezoneAudits(plan.RemoteIslandNamedZoneAudits, plan.Timezones, timezoneBytes, mask); err != nil {
		return nil, err
	}
	positive, err := plan.validatePositiveDateline(mask, timezoneBytes, fingerprints)
	if err != nil {
		return nil, err
	}
	cells = append(cells, positive...)
	return cells, nil
}

func expectedPlanDays(cells []Cell, month time.Month) ([]uint16, error) {
	counts := make(map[string]uint16)
	result := make([]uint16, len(cells))
	for index, cell := range cells {
		count, exists := counts[cell.TimezoneID]
		if !exists {
			location, err := time.LoadLocation(cell.TimezoneID)
			if err != nil {
				return nil, fmt.Errorf("load ERA5 timezone %s for climate day count: %w", cell.TimezoneID, err)
			}
			days := 0
			for year := climateFirstYear; year <= climateLastYear; year++ {
				lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
				for day := 1; day <= lastDay; day++ {
					window, valid, err := civilDayWindow(year, month, day, location)
					if err != nil {
						return nil, fmt.Errorf("count ERA5 climate days for %s: %w", cell.TimezoneID, err)
					}
					if valid {
						if window.end.Sub(window.start) != 10*time.Hour {
							return nil, fmt.Errorf("ERA5 timezone %s has a non-10-hour climate day", cell.TimezoneID)
						}
						days++
					}
				}
			}
			if days < 1 || days > math.MaxUint16 {
				return nil, fmt.Errorf("ERA5 timezone %s has invalid climate day count %d", cell.TimezoneID, days)
			}
			count = uint16(days)
			counts[cell.TimezoneID] = count
		}
		result[index] = count
	}
	return result, nil
}

const (
	timeJanuary  = 1
	timeDecember = 12
)

func decodePlanPlane(label, encoded string, expectedBytes int, declaredSHA string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("ERA5 cell plan %s is missing", label)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode ERA5 cell plan %s: %w", label, err)
	}
	if len(decoded) != expectedBytes {
		return nil, fmt.Errorf("ERA5 cell plan %s has %d bytes; want %d", label, len(decoded), expectedBytes)
	}
	expected, err := parseSHA256(declaredSHA)
	if err != nil {
		return nil, fmt.Errorf("ERA5 cell plan %s checksum: %w", label, err)
	}
	actual := sha256.Sum256(decoded)
	if !bytes.Equal(expected[:], actual[:]) {
		return nil, fmt.Errorf("ERA5 cell plan %s checksum mismatch", label)
	}
	return decoded, nil
}

func parseSHA256(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if len(value) != hex.EncodedLen(sha256.Size) || value != strings.ToLower(value) {
		return result, errors.New("checksum must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return result, errors.New("checksum must be 64 lowercase hexadecimal characters")
	}
	copy(result[:], decoded)
	return result, nil
}

func validateTimezoneNames(names []string) error {
	if len(names) == 0 || len(names) > math.MaxUint16+1 {
		return errors.New("ERA5 cell plan timezone table is empty or too large")
	}
	seen := make(map[string]struct{}, len(names))
	for index, name := range names {
		if name == "" {
			return fmt.Errorf("ERA5 cell plan timezone %d is empty", index)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("ERA5 cell plan timezone %q is repeated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateSupportAudits(audits []supportAudit, mask []byte, expected bool) error {
	for index, audit := range audits {
		if audit.Label == "" || !finite(audit.Latitude, audit.Longitude) ||
			audit.Row < 0 || audit.Row >= GridRows || audit.Column < 0 || audit.Column >= GridColumns ||
			audit.Supported != expected ||
			(mask[audit.Row*GridColumns+audit.Column] == 1) != expected {
			return fmt.Errorf("ERA5 cell plan support audit %d is inconsistent", index)
		}
	}
	return nil
}

func validateSpatialTimezoneAudits(audits []fixedOffsetAudit, names []string, timezoneBytes, mask []byte) error {
	for index, audit := range audits {
		if audit.LandIdentity == "" || audit.EvidenceClass == "" || audit.Reason == "" ||
			!finite(audit.Latitude, audit.Longitude, audit.UTCOffsetHours, audit.SolarOffsetDeltaHours) ||
			audit.SolarOffsetDeltaHours < 0 ||
			!auditCellTimezoneMatches(audit.Row, audit.Column, audit.Timezone, names, timezoneBytes, mask) {
			return fmt.Errorf("ERA5 cell plan fixed-offset audit %d is inconsistent", index)
		}
	}
	return nil
}

func validateNamedTimezoneAudits(audits []namedZoneAudit, names []string, timezoneBytes, mask []byte) error {
	for index, audit := range audits {
		if audit.LandIdentity == "" || audit.EvidenceClass == "" || audit.Reason == "" ||
			!finite(audit.Latitude, audit.Longitude, audit.EvaluationMonthUTCOffsetHours, audit.SolarOffsetDeltaHours) ||
			audit.SolarOffsetDeltaHours < 0 ||
			!auditCellTimezoneMatches(audit.Row, audit.Column, audit.Timezone, names, timezoneBytes, mask) {
			return fmt.Errorf("ERA5 cell plan named-zone audit %d is inconsistent", index)
		}
	}
	return nil
}

func auditCellTimezoneMatches(row, column int, timezone string, names []string, timezoneBytes, mask []byte) bool {
	if row < 0 || row >= GridRows || column < 0 || column >= GridColumns || timezone == "" {
		return false
	}
	index := row*GridColumns + column
	timezoneIndex := int(binary.LittleEndian.Uint16(timezoneBytes[index*2:]))
	return mask[index] == 1 && timezoneIndex < len(names) && names[timezoneIndex] == timezone
}

func (plan encodedCellPlan) validatePositiveDateline(mask, timezoneBytes []byte, fingerprints []string) ([]Cell, error) {
	if plan.PositiveDatelineSupportRows != len(plan.PositiveDatelineTimezoneAudits) ||
		plan.PositiveDatelineSupportRows != plan.PositiveDatelineEastOnlyRows+plan.PositiveDatelineBothRows ||
		plan.NegativeDatelineSupportRows != plan.PositiveDatelineWestOnlyRows+plan.PositiveDatelineBothRows ||
		plan.PositiveDatelineWrappedCells != plan.PositiveDatelineEastOnlyRows ||
		plan.WestEdgeLandCells != plan.NegativeDatelineSupportRows+plan.PositiveDatelineEastOnlyRows {
		return nil, errors.New("ERA5 cell plan dateline counts disagree")
	}
	nameIndexes := make(map[string]int, len(plan.Timezones))
	for index, name := range plan.Timezones {
		nameIndexes[name] = index
	}
	eastOnly, both := 0, 0
	previousRow := -1
	result := make([]Cell, 0, len(plan.PositiveDatelineTimezoneAudits))
	for index, audit := range plan.PositiveDatelineTimezoneAudits {
		if audit.Row < 0 || audit.Row >= GridRows ||
			audit.Row <= previousRow ||
			math.Abs(audit.Latitude-(90-float64(audit.Row)*.25)) > 1e-9 ||
			audit.MainTimezone == "" || audit.PositiveTimezone == "" ||
			!audit.EastSupported {
			return nil, fmt.Errorf("ERA5 cell plan positive-dateline audit %d is invalid", index)
		}
		previousRow = audit.Row
		mainIndex := audit.Row * GridColumns
		mainTimezoneIndex := int(binary.LittleEndian.Uint16(timezoneBytes[mainIndex*2:]))
		positiveTimezoneIndex, exists := nameIndexes[audit.PositiveTimezone]
		if mask[mainIndex] != 1 || mainTimezoneIndex >= len(plan.Timezones) ||
			plan.Timezones[mainTimezoneIndex] != audit.MainTimezone || !exists {
			return nil, fmt.Errorf("ERA5 cell plan positive-dateline audit %d disagrees with the main plane", index)
		}
		if audit.WestSupported {
			both++
		} else {
			eastOnly++
		}
		result = append(result, Cell{
			SourceIndex: audit.Row*GridColumns + GridColumns/2,
			Latitude:    audit.Latitude, Longitude: 180,
			TimezoneID:          audit.PositiveTimezone,
			TimezoneFingerprint: fingerprints[positiveTimezoneIndex],
		})
	}
	if eastOnly != plan.PositiveDatelineEastOnlyRows || both != plan.PositiveDatelineBothRows {
		return nil, errors.New("ERA5 cell plan positive-dateline audit classes disagree")
	}
	return result, nil
}
