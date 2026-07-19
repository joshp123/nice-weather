package pipeline

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestSampleMetricsAtRepresentativeCellsStaysWithinTargetCell(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 4, South: 0, North: 4}, Rows: 4, Cols: 4}
	targetSpec := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 2, Cols: 2}
	source := make([]weather.DailyMetrics, 16)
	for index := range source {
		source[index] = rangeMetric(float64(index))
	}
	source[3*4+3] = weather.DailyMetrics{}
	got, err := SampleMetricsAtRepresentativeCells(sourceSpec, source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	// Each 2x2 source block is one target cell. When the south-east target's
	// nearest source cell is unavailable, another valid cell in that same block
	// represents it; no neighbouring target cell is consulted.
	if got[0].ComfortableTempC != source[5].ComfortableTempC || got[1].ComfortableTempC != source[7].ComfortableTempC || got[2].ComfortableTempC != source[13].ComfortableTempC || got[3].ComfortableTempC != source[10].ComfortableTempC {
		t.Fatalf("sampled metrics = %+v", got)
	}
	for _, index := range []int{10, 11, 14} {
		source[index] = weather.DailyMetrics{}
	}
	got, err = SampleMetricsAtRepresentativeCells(sourceSpec, source, targetSpec)
	if err != nil || got[3].Valid {
		t.Fatalf("empty target cell was filled across its boundary: metrics=%+v err=%v", got[3], err)
	}
	if _, err := SampleMetricsAtRepresentativeCells(targetSpec, got, sourceSpec); err == nil {
		t.Fatal("coarse forecast was allowed to masquerade as a finer grid")
	}
}

func TestSampleMaskAtRepresentativeCellsPreservesCoastalTouchesInsideTarget(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 4, South: 0, North: 4}, Rows: 4, Cols: 4}
	targetSpec := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 2, Cols: 2}
	// Fine cell 0 touches a tiny island inside the north-west coarse cell even
	// though that coarse cell's centre is water. Fine cell 7 supports the
	// north-east target cell. Neither may leak into the southern target cells.
	source := FloatGrid{Spec: sourceSpec, Values: make([]float32, 16)}
	source.Values[0], source.Values[7] = 1, 1
	target, err := SampleMaskAtRepresentativeCells(source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	if target.Values[0] != 1 || target.Values[1] != 1 || target.Values[2] != 0 || target.Values[3] != 0 {
		t.Fatalf("representative support = %v; want [1 1 0 0]", target.Values)
	}
}

func TestRepresentativeCellsOwnBoundaryCentresOnce(t *testing.T) {
	source := FloatGrid{
		Spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 3, South: 0, North: 1}, Rows: 1, Cols: 3},
		Values: []float32{0, 1, 0},
	}
	targetSpec := delivery.GridSpec{Bounds: source.Spec.Bounds, Rows: 1, Cols: 2}
	target, err := SampleMaskAtRepresentativeCells(source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	if target.Values[0] != 0 || target.Values[1] != 1 {
		t.Fatalf("boundary source support = %v; want half-open ownership [0 1]", target.Values)
	}
	shiftedSource := FloatGrid{
		Spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2},
		Values: []float32{1, 0},
	}
	shiftedTarget := delivery.GridSpec{Bounds: delivery.GridBounds{West: -.5, East: 1.5, South: 0, North: 1}, Rows: 1, Cols: 2}
	target, err = SampleMaskAtRepresentativeCells(shiftedSource, shiftedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if target.Values[0] != 0 || target.Values[1] != 1 {
		t.Fatalf("shifted boundary source support = %v; want canonical owner [0 1]", target.Values)
	}
}

func TestMalagaCoastalPatternPublishesPointAndMapCell(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}, Rows: 680, Cols: 1104}
	targetSpec := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 170, Cols: 276}
	const lon, lat = -4.42034, 36.72016
	const targetRow, targetCol = 139, 78
	// This is the exact 4x4 validity pattern in Málaga's 28 km target cell
	// from the live 2026-07-19 ICON-EU grid. The target-centre source cell
	// (local row 2, column 2) is water, while six detailed land cells carry
	// complete weather inside the same target cell.
	pattern := [4]string{"1111", "1000", "1000", "0000"}
	sourceMask := FloatGrid{Spec: sourceSpec, Values: make([]float32, int(sourceSpec.Rows)*int(sourceSpec.Cols))}
	source := make([]weather.DailyMetrics, len(sourceMask.Values))
	for localRow, line := range pattern {
		for localCol, value := range line {
			if value != '1' {
				continue
			}
			index := (targetRow*4+localRow)*int(sourceSpec.Cols) + targetCol*4 + localCol
			sourceMask.Values[index] = 1
			source[index] = rangeMetric(24)
		}
	}
	sampledSupport, err := SampleMaskAtRepresentativeCells(sourceMask, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	sampled, err := SampleMetricsAtRepresentativeCells(sourceSpec, source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	targetIndex := targetRow*int(targetSpec.Cols) + targetCol
	if sampledSupport.Values[targetIndex] != 1 || !sampled[targetIndex].Valid {
		t.Fatalf("Málaga target support=%v metrics=%+v", sampledSupport.Values[targetIndex], sampled[targetIndex])
	}
	days := make([][]weather.DailyMetrics, 14)
	for day := range days {
		days[day] = sampled
	}
	nice, err := ForecastRangeRecords(targetSpec, days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	cycling, err := ForecastRangeRecords(targetSpec, days, weather.ProfileCycling)
	if err != nil {
		t.Fatal(err)
	}
	geometry, pointRecords, err := delivery.RangePointRecords(targetSpec, nice, cycling, days)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/range.nwp"
	if _, err := delivery.WriteRangePointAsset(path, "test-release", geometry, pointRecords); err != nil {
		t.Fatal(err)
	}
	points, err := delivery.OpenPointAsset(path)
	if err != nil {
		t.Fatal(err)
	}
	defer points.Close()
	if _, valid, err := points.RangeAt(lon, lat); err != nil || !valid {
		t.Fatalf("Málaga lookup valid=%v err=%v", valid, err)
	}
}

func TestForecastRangeRecordsRequireEveryDayAndKeepProfilesSeparate(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	days := make([][]weather.DailyMetrics, 14)
	for day := range days {
		days[day] = []weather.DailyMetrics{rangeMetric(24), rangeMetric(24)}
		if day%3 == 0 {
			days[day][0].WindSpeedKPH = 30
			days[day][0].GustKPH = weather.OptionalFloat{Value: 42, Available: true}
		}
	}
	days[6][1] = weather.DailyMetrics{}
	nice, err := ForecastRangeRecords(spec, days, weather.ProfileNice)
	if err != nil {
		t.Fatal(err)
	}
	cycling, err := ForecastRangeRecords(spec, days, weather.ProfileCycling)
	if err != nil {
		t.Fatal(err)
	}
	if !nice[0].Valid || !cycling[0].Valid || nice[0].DayCount != 14 || cycling[0].DayCount != 14 {
		t.Fatalf("complete cell was not summarized: nice=%+v cycling=%+v", nice[0], cycling[0])
	}
	if nice[0].Score == cycling[0].Score || nice[0].Wind == cycling[0].Wind {
		t.Fatalf("profile-specific wind judgement collapsed: nice=%+v cycling=%+v", nice[0], cycling[0])
	}
	if nice[1].Valid || cycling[1].Valid {
		t.Fatalf("missing promised day was silently shortened: nice=%+v cycling=%+v", nice[1], cycling[1])
	}
	if _, err := ForecastRangeRecords(spec, days, weather.Profile("commute")); err == nil {
		t.Fatal("unknown range profile was accepted as an all-transparent product")
	}
}

func TestRangeOverlayPixelsComeFromRangeRecordScores(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 50, North: 52}, Rows: 2, Cols: 2}
	records := []delivery.RangeRecord{
		rangeDeliveryRecord(20), rangeDeliveryRecord(45), {}, rangeDeliveryRecord(88),
	}
	var encoded bytes.Buffer
	if err := RenderWebMercatorRangeRecords(&encoded, spec, records, delivery.CanonicalPalette()); err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := delivery.NewWebMercatorMapper(spec)
	if err != nil {
		t.Fatal(err)
	}
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	for row := 0; row < image.Bounds().Dy(); row++ {
		for col := 0; col < image.Bounds().Dx(); col++ {
			sourceRow, sourceCol, ok := mapper.Select(row, col, func(sourceRow, sourceCol uint32) bool {
				return records[int(sourceRow)*int(spec.Cols)+int(sourceCol)].Valid
			})
			got := image.At(col, row)
			if !ok {
				_, _, _, alpha := got.RGBA()
				if alpha != 0 {
					t.Fatalf("no-data pixel %d,%d is opaque", row, col)
				}
				continue
			}
			record := records[int(sourceRow)*int(spec.Cols)+int(sourceCol)]
			want := palette[record.Score]
			if got != want {
				t.Fatalf("pixel %d,%d = %v; want score %d colour %v", row, col, got, record.Score, want)
			}
		}
	}
}

func TestRangeOverlayRejectsUnvalidatedRecords(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 50, North: 51}, Rows: 1, Cols: 1}
	bad := rangeDeliveryRecord(70)
	bad.Score = 255
	if err := RenderWebMercatorRangeRecords(&bytes.Buffer{}, spec, []delivery.RangeRecord{bad}, delivery.CanonicalPalette()); err == nil {
		t.Fatal("range renderer accepted an out-of-contract score")
	}
	bad = rangeDeliveryRecord(70)
	bad.Valid = false
	if err := RenderWebMercatorRangeRecords(&bytes.Buffer{}, spec, []delivery.RangeRecord{bad}, delivery.CanonicalPalette()); err == nil {
		t.Fatal("range renderer accepted data hidden behind an invalid flag")
	}
}

func TestForecastWindowLensPixelsUseStoredRangeComponents(t *testing.T) {
	geometry, err := delivery.NewPointGeometry(delivery.GridSpec{
		Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := delivery.RangePointRecord{
		Nice:    delivery.RangeRecord{Score: 11, Thermal: 22, Dryness: 33, Sun: 44, Wind: 55, Valid: true},
		Cycling: delivery.RangeRecord{Score: 66, Thermal: 22, Dryness: 33, Sun: 44, Wind: 77, Valid: true},
	}
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		profile weather.Profile
		lens    weather.Lens
		want    uint8
	}{
		{"nice overall", weather.ProfileNice, weather.LensOverall, 11},
		{"cycling overall", weather.ProfileCycling, weather.LensOverall, 66},
		{"comfort", weather.ProfileNice, weather.LensComfort, 22},
		{"dry", weather.ProfileNice, weather.LensDry, 33},
		{"bright", weather.ProfileNice, weather.LensBright, 44},
		{"nice calm", weather.ProfileNice, weather.LensCalm, 55},
		{"cycling calm", weather.ProfileCycling, weather.LensCalm, 77},
	} {
		t.Run(test.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := RenderPointRangeLensRecords(&encoded, geometry, []delivery.RangePointRecord{record}, test.profile, test.lens, delivery.CanonicalPalette()); err != nil {
				t.Fatal(err)
			}
			imageData, err := png.Decode(bytes.NewReader(encoded.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(imageData.At(0, 0)); got != palette[test.want] {
				t.Fatalf("pixel = %v; want stored value %d colour %v", got, test.want, palette[test.want])
			}
		})
	}
	if err := RenderPointRangeLensRecords(&bytes.Buffer{}, geometry, []delivery.RangePointRecord{record}, weather.Profile("commute"), weather.LensOverall, delivery.CanonicalPalette()); err == nil {
		t.Fatal("unknown forecast-window profile was accepted")
	}
	if err := RenderPointRangeLensRecords(&bytes.Buffer{}, geometry, []delivery.RangePointRecord{record}, weather.ProfileNice, weather.Lens("humidity"), delivery.CanonicalPalette()); err == nil {
		t.Fatal("unknown forecast-window lens was accepted")
	}
}

func rangeMetric(temperature float64) weather.DailyMetrics {
	return weather.DailyMetrics{
		ComfortableTempC: temperature, MinTempC: temperature - 3, MaxTempC: temperature + 3,
		CloudCoverPercent: weather.OptionalFloat{Value: 15, Available: true}, WindSpeedKPH: 8,
		GustKPH: weather.OptionalFloat{Value: 12, Available: true}, DewPointC: weather.OptionalFloat{Value: 12, Available: true},
		NativeResolutionKM: 7, Valid: true,
	}
}

func rangeDeliveryRecord(score uint8) delivery.RangeRecord {
	result := delivery.RangeRecord{
		Score: score, MedianScore: score, Thermal: score, Dryness: score, Sun: score, Wind: score,
		DayCount: 14, Valid: true,
	}
	if score >= 55 {
		result.NiceDays = 14
	}
	if score <= 24 {
		result.AwfulDays, result.LongestPoorRun = 14, 14
	} else if score <= 39 {
		result.LongestPoorRun = 14
	}
	return result
}
