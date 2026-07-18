package pipeline

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
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
// EPSG:4326 point grid. Its colors come only from the manifest legend.
func RenderWebMercatorOverlay(w io.Writer, spec delivery.GridSpec, metrics []weather.DailyMetrics, profile weather.Profile, legend []delivery.LegendCategory) error {
	if spec.Rows == 0 || spec.Cols == 0 || len(metrics) != int(spec.Rows)*int(spec.Cols) {
		return fmt.Errorf("overlay grid dimensions do not match weather cells")
	}
	width, height, err := delivery.WebMercatorImageDimensions(spec)
	if err != nil {
		return err
	}
	mapper, err := delivery.NewWebMercatorMapper(spec)
	if err != nil {
		return err
	}
	palette, err := legendPalette(legend)
	if err != nil {
		return err
	}
	records, err := DeliveryRecords(metrics)
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
			record := records[index]
			metric, err := record.WeatherMetrics(metrics[index].NativeResolutionKM)
			if err != nil {
				return fmt.Errorf("rebuild quantized weather cell %d: %w", index, err)
			}
			result := weather.Score(metric, profile)
			if result.Available {
				imageData.SetNRGBA(destinationCol, destinationRow, palette[result.Score])
			}
		}
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(w, imageData); err != nil {
		return fmt.Errorf("encode score overlay: %w", err)
	}
	return nil
}

func legendPalette(legend []delivery.LegendCategory) ([101]color.NRGBA, error) {
	var palette [101]color.NRGBA
	if len(legend) == 0 {
		return palette, fmt.Errorf("overlay legend is empty")
	}
	next := 0
	for _, category := range legend {
		if category.Min != next || category.Max < category.Min || category.Max > 100 {
			return palette, fmt.Errorf("overlay legend must cover 0–100 without gaps")
		}
		parsed, err := parseHexColor(category.Color)
		if err != nil {
			return palette, fmt.Errorf("legend %q: %w", category.Label, err)
		}
		for score := category.Min; score <= category.Max; score++ {
			palette[score] = parsed
		}
		next = category.Max + 1
	}
	if next != 101 {
		return palette, fmt.Errorf("overlay legend must cover 0–100 without gaps")
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
	return color.NRGBA{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed), A: 210}, nil
}
