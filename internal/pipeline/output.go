package pipeline

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func DeliveryRecords(metrics []weather.DailyMetrics) ([]delivery.DailyRecord, error) {
	records := make([]delivery.DailyRecord, len(metrics))
	for index, metric := range metrics {
		if !metric.Valid {
			continue
		}
		record, err := delivery.RecordFromWeather(metric)
		if err != nil {
			return nil, fmt.Errorf("weather cell %d: %w", index, err)
		}
		records[index] = record
	}
	return records, nil
}

// ApplyLandMask applies the already-rasterized mask for this exact product
// grid. Keeping the mask and weather cells in one index space avoids a second
// coordinate lookup that can drift at boundaries. Only the canonical value 1
// is land; 0 is water.
func ApplyLandMask(spec delivery.GridSpec, metrics []weather.DailyMetrics, mask FloatGrid) error {
	if spec.Rows == 0 || spec.Cols == 0 || mask.Spec != spec || len(mask.Values) != int(spec.Rows)*int(spec.Cols) || len(metrics) != len(mask.Values) {
		return fmt.Errorf("land mask and matching weather grid are required")
	}
	for index, value := range mask.Values {
		if value != 0 && value != 1 {
			return fmt.Errorf("land mask cell %d has value %g; want 0 or 1", index, value)
		}
		if value == 0 {
			metrics[index] = weather.DailyMetrics{}
		}
	}
	return nil
}

// RenderWebMercatorOverlay writes a PNG whose columns are regular longitude
// and whose rows are regular Web Mercator Y. MapLibre's image source then
// places every pixel at the same latitude represented by the canonical
// EPSG:4326 point grid. Its colors come only from the manifest palette.
func RenderWebMercatorOverlay(w io.Writer, spec delivery.GridSpec, metrics []weather.DailyMetrics, profile weather.Profile, palette []delivery.PaletteStop) error {
	return RenderWebMercatorLens(w, spec, metrics, profile, weather.LensOverall, palette)
}

// RenderWebMercatorLens renders one pre-evaluated score field. The browser may
// interpolate its pixels for display, but never calculates weather policy.
func RenderWebMercatorLens(w io.Writer, spec delivery.GridSpec, metrics []weather.DailyMetrics, profile weather.Profile, lens weather.Lens, paletteStops []delivery.PaletteStop) error {
	if spec.Rows == 0 || spec.Cols == 0 || len(metrics) != int(spec.Rows)*int(spec.Cols) {
		return fmt.Errorf("overlay grid dimensions do not match weather cells")
	}
	records, err := DeliveryRecords(metrics)
	if err != nil {
		return err
	}
	quantized, err := QuantizedMetrics(records, metrics)
	if err != nil {
		return err
	}
	return RenderWebMercatorLensRecords(w, spec, records, quantized, profile, lens, paletteStops)
}

// QuantizedMetrics reconstructs the exact values delivered by the binary
// point grid once per source cell. A period with several overlays reuses this
// plane, preserving point/pixel parity without repeating record conversion.
func QuantizedMetrics(records []delivery.DailyRecord, source []weather.DailyMetrics) ([]weather.DailyMetrics, error) {
	if len(records) != len(source) {
		return nil, fmt.Errorf("delivery records do not match weather cells")
	}
	metrics := make([]weather.DailyMetrics, len(records))
	for index, record := range records {
		if !record.Valid {
			continue
		}
		metric, err := record.WeatherMetrics(source[index].NativeResolutionKM)
		if err != nil {
			return nil, fmt.Errorf("rebuild quantized weather cell %d: %w", index, err)
		}
		metrics[index] = metric
	}
	return metrics, nil
}

// RenderWebMercatorLensRecords renders a lens from the exact point-delivery
// records and their precomputed metric plane.
func RenderWebMercatorLensRecords(w io.Writer, spec delivery.GridSpec, records []delivery.DailyRecord, metrics []weather.DailyMetrics, profile weather.Profile, lens weather.Lens, paletteStops []delivery.PaletteStop) error {
	if spec.Rows == 0 || spec.Cols == 0 || len(records) != int(spec.Rows)*int(spec.Cols) || len(metrics) != len(records) {
		return fmt.Errorf("overlay grid dimensions do not match delivery records")
	}
	width, height, err := delivery.WebMercatorImageDimensions(spec)
	if err != nil {
		return err
	}
	mapper, err := delivery.NewWebMercatorMapper(spec)
	if err != nil {
		return err
	}
	palette, err := scorePalette(paletteStops)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for destinationRow := 0; destinationRow < height; destinationRow++ {
		for destinationCol := 0; destinationCol < width; destinationCol++ {
			sourceRow, sourceCol, ok := mapper.Select(destinationRow, destinationCol, func(row, col uint32) bool {
				return records[int(row)*int(spec.Cols)+int(col)].Valid
			})
			if !ok {
				continue
			}
			index := int(sourceRow)*int(spec.Cols) + int(sourceCol)
			value, available := weather.LensValue(metrics[index], profile, lens)
			if available {
				imageData.SetNRGBA(destinationCol, destinationRow, palette[value])
			}
		}
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(w, imageData); err != nil {
		return fmt.Errorf("encode score overlay: %w", err)
	}
	return nil
}

// RenderWebMercatorRangeRecords renders the score stored in the immutable
// range grid. It cannot drift from point lookup because both consume the same
// compact record rather than independently aggregating daily weather.
func RenderWebMercatorRangeRecords(w io.Writer, spec delivery.GridSpec, records []delivery.RangeRecord, paletteStops []delivery.PaletteStop) error {
	if spec.Rows == 0 || spec.Cols == 0 || len(records) != int(spec.Rows)*int(spec.Cols) {
		return fmt.Errorf("range overlay grid dimensions do not match delivery records")
	}
	for index, record := range records {
		if !record.Valid {
			if record != (delivery.RangeRecord{}) {
				return fmt.Errorf("range overlay record %d contains invalid data", index)
			}
			continue
		}
		if _, err := record.Summary(); err != nil {
			return fmt.Errorf("range overlay record %d: %w", index, err)
		}
	}
	width, height, err := delivery.WebMercatorImageDimensions(spec)
	if err != nil {
		return err
	}
	mapper, err := delivery.NewWebMercatorMapper(spec)
	if err != nil {
		return err
	}
	palette, err := scorePalette(paletteStops)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for destinationRow := 0; destinationRow < height; destinationRow++ {
		for destinationCol := 0; destinationCol < width; destinationCol++ {
			sourceRow, sourceCol, ok := mapper.Select(destinationRow, destinationCol, func(row, col uint32) bool {
				return records[int(row)*int(spec.Cols)+int(col)].Valid
			})
			if !ok {
				continue
			}
			record := records[int(sourceRow)*int(spec.Cols)+int(sourceCol)]
			imageData.SetNRGBA(destinationCol, destinationRow, palette[record.Score])
		}
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(w, imageData); err != nil {
		return fmt.Errorf("encode forecast range overlay: %w", err)
	}
	return nil
}

func scorePalette(stops []delivery.PaletteStop) ([101]color.NRGBA, error) {
	var palette [101]color.NRGBA
	if len(stops) < 2 || stops[0].Score != 0 || stops[len(stops)-1].Score != 100 {
		return palette, fmt.Errorf("overlay palette must run from 0 to 100")
	}
	anchors := make([]struct {
		score int
		color color.NRGBA
	}, 0, len(stops))
	previous := -1
	for _, stop := range stops {
		if stop.Score <= previous || stop.Score < 0 || stop.Score > 100 {
			return palette, fmt.Errorf("overlay palette scores must be ordered")
		}
		parsed, err := parseHexColor(stop.Color)
		if err != nil {
			return palette, fmt.Errorf("palette score %d: %w", stop.Score, err)
		}
		anchors = append(anchors, struct {
			score int
			color color.NRGBA
		}{score: stop.Score, color: parsed})
		previous = stop.Score
	}
	for score := 0; score <= 100; score++ {
		left, right := anchors[0], anchors[len(anchors)-1]
		for index := 1; index < len(anchors); index++ {
			if score <= anchors[index].score {
				left, right = anchors[index-1], anchors[index]
				break
			}
		}
		position := 0.0
		if right.score > left.score {
			position = float64(score-left.score) / float64(right.score-left.score)
		}
		blend := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + position*(float64(b)-float64(a)))) }
		palette[score] = color.NRGBA{R: blend(left.color.R, right.color.R), G: blend(left.color.G, right.color.G), B: blend(left.color.B, right.color.B), A: 200}
	}
	return palette, nil
}

func parseHexColor(value string) (color.NRGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.NRGBA{}, fmt.Errorf("color %q is not #RRGGBB", value)
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 24)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("color %q is not #RRGGBB", value)
	}
	return color.NRGBA{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed), A: 200}, nil
}
