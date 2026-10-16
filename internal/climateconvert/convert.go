// Package climateconvert converts the finished ERA5 product without rescoring it.
package climateconvert

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/era5climateproduct"
)

const RecordBytes = 33

var layerNames = []string{"nice", "cycling", "comfort", "dry", "clear", "calm-nice", "calm-cycling"}

type Summary struct {
	Months    int
	Files     int
	Bytes     int64
	LandCells int
}

type field struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Offset int     `json:"offset"`
	Unit   string  `json:"unit"`
	Scale  float64 `json:"scale"`
}

type metadata struct {
	Version             int      `json:"version"`
	Years               string   `json:"years"`
	Source              string   `json:"source"`
	LocalDay            string   `json:"localDay"`
	Grid                grid     `json:"grid"`
	Months              []string `json:"months"`
	Layers              []string `json:"layers"`
	LayerValues         string   `json:"layerValues"`
	ProbabilityRounding string   `json:"probabilityRounding"`
	Sea                 string   `json:"sea"`
	Points              points   `json:"points"`
}

type grid struct {
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	West              float64 `json:"west"`
	North             float64 `json:"north"`
	ResolutionDegrees float64 `json:"resolutionDegrees"`
	Registration      string  `json:"registration"`
	Order             string  `json:"order"`
}

type points struct {
	Filename    string  `json:"filename"`
	ByteOrder   string  `json:"byteOrder"`
	RecordBytes int     `json:"recordBytes"`
	Fields      []field `json:"fields"`
}

// Convert writes twelve dense source-grid months. Output must be a new climate
// directory so a failed one-off run cannot overwrite an existing conversion.
func Convert(sourceRoot, outputRoot string) (Summary, error) {
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Summary{}, err
	}
	_, index, err := era5climateproduct.ValidateCurrentIndex(sourceRoot)
	if err != nil {
		return Summary{}, err
	}
	if len(index.Months) != 12 || index.ReferencePeriod.FirstYear != 2006 || index.ReferencePeriod.LastYear != 2025 {
		return Summary{}, fmt.Errorf("conversion requires all twelve 2006-2025 months")
	}
	geometry := index.Geometry
	if geometry.Columns != 1440 || geometry.Rows != 721 || geometry.West != -180 || geometry.North != 90 || geometry.CellResolutionDegrees != 0.25 {
		return Summary{}, fmt.Errorf("unexpected ERA5 grid: %+v", geometry)
	}
	climateRoot := filepath.Join(outputRoot, "climate")
	if err := os.MkdirAll(outputRoot, 0755); err != nil {
		return Summary{}, err
	}
	if err := os.Mkdir(climateRoot, 0755); err != nil {
		return Summary{}, err
	}
	summary := Summary{}
	manifest := metadata{
		Version: 1, Years: "2006-2025", Source: "ERA5", LocalDay: index.Source.LocalDay,
		Grid:                grid{1440, 721, -180, 90, 0.25, "grid-point-centres", "row-major from north-west; longitude = west + column * resolutionDegrees; latitude = north - row * resolutionDegrees"},
		Layers:              layerNames,
		LayerValues:         "8-bit greyscale; 0-100 = percent of days rated Good or better (score >= 55); 255 = no data",
		ProbabilityRounding: "Profile probabilities sum the stored rating shares (permille), then round to the nearest whole percent. Component probabilities are the stored whole percentages. The numerator good-day counts are not retained in the source product; daysCounted retains the denominator.",
		Sea:                 "Natural Earth centre-on-land display mask; sea records contain 33 bytes of 255. A record is available only when niceChancePercent is not 255. The independent +180 degree source records are excluded from this 1440-column grid.",
		Points:              points{"points.bin", "little-endian", RecordBytes, recordFields()},
	}
	for monthNumber, reference := range index.Months {
		period := fmt.Sprintf("month-%02d", monthNumber+1)
		if reference.Period != period {
			return summary, fmt.Errorf("unexpected period %q; want %q", reference.Period, period)
		}
		releaseRoot := filepath.Join(sourceRoot, "releases", reference.ReleaseID)
		catalogBody, err := os.ReadFile(filepath.Join(releaseRoot, "catalog.json"))
		if err != nil {
			return summary, err
		}
		if !matchesChecksum(catalogBody, reference.CatalogSHA256) {
			return summary, fmt.Errorf("%s catalog checksum mismatch", period)
		}
		catalog, err := era5climateproduct.ParseCatalog(catalogBody)
		if err != nil {
			return summary, err
		}
		monthSummary, err := convertMonth(releaseRoot, filepath.Join(climateRoot, period), catalog)
		if err != nil {
			return summary, fmt.Errorf("%s: %w", period, err)
		}
		summary.Months++
		summary.Files += monthSummary.Files
		summary.Bytes += monthSummary.Bytes
		summary.LandCells = monthSummary.LandCells
		manifest.Months = append(manifest.Months, period)
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return summary, err
	}
	body = append(body, '\n')
	if err := os.WriteFile(filepath.Join(climateRoot, "current.json"), body, 0644); err != nil {
		return summary, err
	}
	summary.Files++
	summary.Bytes += int64(len(body))
	return summary, nil
}

func convertMonth(sourceRoot, outputRoot string, catalog era5climateproduct.Catalog) (Summary, error) {
	const cells = 1440 * 721
	mask, err := os.ReadFile(filepath.Join(sourceRoot, catalog.Source.DisplayMaskPath))
	if err != nil {
		return Summary{}, err
	}
	if len(mask) != cells || !matchesChecksum(mask, catalog.Source.DisplayMaskSHA256) {
		return Summary{}, fmt.Errorf("display mask size or checksum mismatch")
	}
	layers := make([]*image.Gray, len(layerNames))
	for index := range layers {
		layers[index] = image.NewGray(image.Rect(0, 0, 1440, 721))
		copy(layers[index].Pix, bytes.Repeat([]byte{255}, cells))
	}
	records := bytes.Repeat([]byte{255}, cells*RecordBytes)
	seen := make([]bool, cells)
	for _, block := range catalog.PointBlocks {
		body, err := os.ReadFile(filepath.Join(sourceRoot, block.Path))
		if err != nil {
			return Summary{}, err
		}
		if len(body) != int(block.Bytes) || len(body) < 64 || !matchesChecksum(body, block.SHA256) {
			return Summary{}, fmt.Errorf("point block %s size or checksum mismatch", block.Path)
		}
		header, err := era5climateproduct.ParsePointHeader(body[:64])
		if err != nil {
			return Summary{}, err
		}
		payload := body[64:]
		if header.Kind != era5climateproduct.PointKindMain || int(header.LatitudeBlock) != block.LatitudeBlock || int(header.LongitudeBlock) != block.LongitudeBlock || header.PayloadSHA256 != block.PayloadSHA256 || !matchesChecksum(payload, header.PayloadSHA256) || len(payload) != int(header.Rows)*int(header.Columns)*delivery.ClimatePointRecordBytes {
			return Summary{}, fmt.Errorf("point block %s header or payload mismatch", block.Path)
		}
		for row := 0; row < int(block.LatitudeCount); row++ {
			for column := 0; column < int(block.LongitudeCount); column++ {
				cell := (block.LatitudeBlock*era5climateproduct.PointBlockRows+row)*1440 + block.LongitudeBlock*era5climateproduct.PointBlockCols + column
				if cell >= cells {
					return Summary{}, fmt.Errorf("point block outside grid")
				}
				if mask[cell] == 0 {
					continue
				}
				if seen[cell] {
					return Summary{}, fmt.Errorf("duplicate land cell %d", cell)
				}
				offset := (row*int(header.Columns) + column) * delivery.ClimatePointRecordBytes
				raw := payload[offset : offset+delivery.ClimatePointRecordBytes]
				source, err := delivery.DecodeClimatePointRecord(raw)
				if err != nil {
					return Summary{}, fmt.Errorf("cell %d: %w", cell, err)
				}
				if !source.Distribution.Valid {
					return Summary{}, fmt.Errorf("land cell %d has no climate record", cell)
				}
				distribution := source.Distribution
				values := []byte{distribution.Nice.GoodOrBetterPercent(), distribution.Cycling.GoodOrBetterPercent(), distribution.ComfortGoodPercent, distribution.DryGoodPercent, distribution.BrightGoodPercent, distribution.Nice.CalmGoodPercent, distribution.Cycling.CalmGoodPercent}
				for layer, value := range values {
					layers[layer].Pix[cell] = value
				}
				target := records[cell*RecordBytes : (cell+1)*RecordBytes]
				binary.LittleEndian.PutUint16(target[:2], source.Days)
				copy(target[2:8], []byte{values[0], distribution.Nice.AwfulPercent(), distribution.Nice.MedianScore, values[1], distribution.Cycling.AwfulPercent(), distribution.Cycling.MedianScore})
				copy(target[8:], raw[36:61])
				seen[cell] = true
			}
		}
	}
	summary := Summary{}
	for cell, land := range mask {
		if land > 1 {
			return summary, fmt.Errorf("invalid display mask value at cell %d", cell)
		}
		if land == 1 {
			if !seen[cell] {
				return summary, fmt.Errorf("land cell %d missing from point blocks", cell)
			}
			summary.LandCells++
		}
	}
	if err := os.Mkdir(outputRoot, 0755); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "points.bin"), records, 0644); err != nil {
		return summary, err
	}
	summary.Files++
	summary.Bytes += int64(len(records))
	for index, layer := range layers {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, layer); err != nil {
			return summary, err
		}
		if err := os.WriteFile(filepath.Join(outputRoot, layerNames[index]+".png"), encoded.Bytes(), 0644); err != nil {
			return summary, err
		}
		summary.Files++
		summary.Bytes += int64(encoded.Len())
	}
	return summary, nil
}

func matchesChecksum(body []byte, expected string) bool {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]) == expected
}

func recordFields() []field {
	return []field{
		{"daysCounted", "uint16", 0, "days", 1},
		{"niceChancePercent", "uint8", 2, "%", 1}, {"niceAwfulPercent", "uint8", 3, "%", 1}, {"niceMedianScore", "uint8", 4, "score 0-100", 1},
		{"cyclingChancePercent", "uint8", 5, "%", 1}, {"cyclingAwfulPercent", "uint8", 6, "%", 1}, {"cyclingMedianScore", "uint8", 7, "score 0-100", 1},
		{"daytimeTemperatureP10C", "int16", 8, "°C", 0.1}, {"daytimeTemperatureP50C", "int16", 10, "°C", 0.1}, {"daytimeTemperatureP90C", "int16", 12, "°C", 0.1},
		{"daytimeMinimumP50C", "int16", 14, "°C", 0.1}, {"daytimeMaximumP50C", "int16", 16, "°C", 0.1},
		{"dewPointP50C", "int16", 18, "°C", 0.1}, {"dewPointP90C", "int16", 20, "°C", 0.1},
		{"rainP90MM", "uint16", 22, "mm", 0.1}, {"windP50KPH", "uint16", 24, "km/h", 0.1}, {"windP90KPH", "uint16", 26, "km/h", 0.1},
		{"relativeHumidityP50Percent", "uint8", 28, "%", 1}, {"muggyDayPercent", "uint8", 29, "%", 1}, {"rainFreeDayPercent", "uint8", 30, "%", 1},
		{"cloudCoverP50Percent", "uint8", 31, "%", 1}, {"clearSkyDayPercent", "uint8", 32, "%", 1},
	}
}
