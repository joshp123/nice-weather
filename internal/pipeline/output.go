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

// RenderPointDailyRecords paints the already-evaluated browser point plane.
// The point asset and overlay therefore have one texel authority: neither the
// browser nor image generation reselects a source cell or scores weather.
func RenderPointDailyRecords(w io.Writer, geometry delivery.PointGeometry, records []delivery.DailyPointRecord, profile weather.Profile, lens weather.Lens, paletteStops []delivery.PaletteStop) error {
	if len(records) != int(geometry.Rows)*int(geometry.Cols) {
		return fmt.Errorf("daily point overlay geometry does not match records")
	}
	palette, err := scorePalette(paletteStops)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, int(geometry.Cols), int(geometry.Rows)))
	for index, record := range records {
		if !record.Weather.Valid {
			continue
		}
		var score uint8
		switch lens {
		case weather.LensOverall:
			if profile == weather.ProfileCycling {
				score = record.CyclingScore
			} else {
				score = record.NiceScore
			}
		case weather.LensComfort:
			score = record.Thermal
		case weather.LensDry:
			score = record.Dryness
		case weather.LensBright:
			score = record.Sun
		case weather.LensCalm:
			if profile == weather.ProfileCycling {
				score = record.CyclingWind
			} else {
				score = record.NiceWind
			}
		default:
			return fmt.Errorf("unsupported daily point lens %q", lens)
		}
		imageData.SetNRGBA(index%int(geometry.Cols), index/int(geometry.Cols), palette[score])
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	return encoder.Encode(w, imageData)
}

// RenderDisplayScoreRecords renders the same compact records used for a
// selected outlook headline. Physical weather does not participate here.
func RenderDisplayScoreRecords(w io.Writer, geometry delivery.PointGeometry, records []delivery.DisplayScoreRecord, profile weather.Profile, lens weather.Lens, paletteStops []delivery.PaletteStop) error {
	if len(records) != int(geometry.Rows)*int(geometry.Cols) {
		return fmt.Errorf("display-score overlay geometry does not match records")
	}
	if profile != weather.ProfileNice && profile != weather.ProfileCycling {
		return fmt.Errorf("unsupported display-score profile %q", profile)
	}
	palette, err := scorePalette(paletteStops)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, int(geometry.Cols), int(geometry.Rows)))
	for index, record := range records {
		if !record.Valid {
			continue
		}
		var score uint8
		switch lens {
		case weather.LensOverall:
			score = record.NiceScore
			if profile == weather.ProfileCycling {
				score = record.CyclingScore
			}
		case weather.LensComfort:
			score = record.Thermal
		case weather.LensDry:
			score = record.Dryness
		case weather.LensBright:
			score = record.Sun
		case weather.LensCalm:
			score = record.NiceWind
			if profile == weather.ProfileCycling {
				score = record.CyclingWind
			}
		default:
			return fmt.Errorf("unsupported display-score lens %q", lens)
		}
		imageData.SetNRGBA(index%int(geometry.Cols), index/int(geometry.Cols), palette[score])
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	return encoder.Encode(w, imageData)
}

// RenderPointRangeLensRecords renders an already-aggregated forecast-window
// judgement. Every value comes from the same RangePointRecord returned for a
// selected texel; this function never re-scores or re-aggregates weather.
func RenderPointRangeLensRecords(w io.Writer, geometry delivery.PointGeometry, records []delivery.RangePointRecord, profile weather.Profile, lens weather.Lens, paletteStops []delivery.PaletteStop) error {
	if len(records) != int(geometry.Rows)*int(geometry.Cols) {
		return fmt.Errorf("forecast-window point overlay geometry does not match records")
	}
	if profile != weather.ProfileNice && profile != weather.ProfileCycling {
		return fmt.Errorf("unsupported forecast-window profile %q", profile)
	}
	palette, err := scorePalette(paletteStops)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, int(geometry.Cols), int(geometry.Rows)))
	for index, record := range records {
		if !record.Nice.Valid {
			continue
		}
		profileRecord := record.Nice
		if profile == weather.ProfileCycling {
			profileRecord = record.Cycling
		}
		var score uint8
		switch lens {
		case weather.LensOverall:
			score = profileRecord.Score
		case weather.LensComfort:
			score = profileRecord.Thermal
		case weather.LensDry:
			score = profileRecord.Dryness
		case weather.LensBright:
			score = profileRecord.Sun
		case weather.LensCalm:
			score = profileRecord.Wind
		default:
			return fmt.Errorf("unsupported forecast-window lens %q", lens)
		}
		imageData.SetNRGBA(index%int(geometry.Cols), index/int(geometry.Cols), palette[score])
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	return encoder.Encode(w, imageData)
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
		switch score {
		case left.score:
			palette[score] = left.color
		case right.score:
			palette[score] = right.color
		default:
			position := float64(score-left.score) / float64(right.score-left.score)
			palette[score] = interpolateOKLab(left.color, right.color, position)
		}
	}
	return palette, nil
}

type okLab struct {
	l float64
	a float64
	b float64
}

// interpolateOKLab blends perceived lightness and chroma instead of encoded
// sRGB channels. This keeps the low-chroma stone transition visually ordered.
func interpolateOKLab(left, right color.NRGBA, position float64) color.NRGBA {
	leftLab, rightLab := colorToOKLab(left), colorToOKLab(right)
	interpolate := func(a, b float64) float64 { return a + position*(b-a) }
	return okLabToColor(okLab{
		l: interpolate(leftLab.l, rightLab.l),
		a: interpolate(leftLab.a, rightLab.a),
		b: interpolate(leftLab.b, rightLab.b),
	})
}

func colorToOKLab(value color.NRGBA) okLab {
	r := srgbToLinear(float64(value.R) / 255)
	g := srgbToLinear(float64(value.G) / 255)
	b := srgbToLinear(float64(value.B) / 255)

	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return okLab{
		l: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		a: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		b: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

func okLabToColor(value okLab) color.NRGBA {
	l := value.l + 0.3963377774*value.a + 0.2158037573*value.b
	m := value.l - 0.1055613458*value.a - 0.0638541728*value.b
	s := value.l - 0.0894841775*value.a - 1.2914855480*value.b
	l, m, s = l*l*l, m*m*m, s*s*s

	r := linearToSRGB(4.0767416621*l - 3.3077115913*m + 0.2309699292*s)
	g := linearToSRGB(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s)
	b := linearToSRGB(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s)
	channel := func(component float64) uint8 {
		return uint8(math.Round(255 * math.Max(0, math.Min(1, component))))
	}
	return color.NRGBA{R: channel(r), G: channel(g), B: channel(b), A: 255}
}

func srgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func linearToSRGB(value float64) float64 {
	if value <= 0.0031308 {
		return 12.92 * value
	}
	return 1.055*math.Pow(value, 1/2.4) - 0.055
}

func parseHexColor(value string) (color.NRGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.NRGBA{}, fmt.Errorf("color %q is not #RRGGBB", value)
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 24)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("color %q is not #RRGGBB", value)
	}
	return color.NRGBA{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed), A: 255}, nil
}
