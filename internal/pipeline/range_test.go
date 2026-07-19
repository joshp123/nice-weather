package pipeline

import (
	"bytes"
	"image/png"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestSampleMetricsAtCellCentersDownsamplesWithoutFillingMissingCells(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 4, South: 0, North: 4}, Rows: 4, Cols: 4}
	targetSpec := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 2, Cols: 2}
	source := make([]weather.DailyMetrics, 16)
	for index := range source {
		source[index] = rangeMetric(float64(index))
	}
	source[3*4+3] = weather.DailyMetrics{}
	got, err := SampleMetricsAtCellCenters(sourceSpec, source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	// Target centres (1,3), (3,3), (1,1), (3,1) select source cells
	// (1,1), (1,3), (3,1), (3,3) respectively.
	if got[0].ComfortableTempC != source[5].ComfortableTempC || got[1].ComfortableTempC != source[7].ComfortableTempC || got[2].ComfortableTempC != source[13].ComfortableTempC || got[3].Valid {
		t.Fatalf("sampled metrics = %+v", got)
	}
	if _, err := SampleMetricsAtCellCenters(targetSpec, got, sourceSpec); err == nil {
		t.Fatal("coarse forecast was allowed to masquerade as a finer grid")
	}
}

func TestSampleMaskAtCellCentersDoesNotPromoteCoastalTouches(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 4, South: 0, North: 4}, Rows: 4, Cols: 4}
	targetSpec := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 2, Cols: 2}
	// Fine cell 0 touches a tiny island inside the north-west coarse cell, but
	// that coarse cell's centre maps to fine cell 5, which is water. Fine cell 7
	// supports the north-east target centre.
	source := FloatGrid{Spec: sourceSpec, Values: make([]float32, 16)}
	source.Values[0], source.Values[7] = 1, 1
	target, err := SampleMaskAtCellCenters(source, targetSpec)
	if err != nil {
		t.Fatal(err)
	}
	if target.Values[0] != 0 || target.Values[1] != 1 {
		t.Fatalf("target-centre support = %v; want north row [0 1]", target.Values)
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

func TestRangeOverlayPixelsComeFromRangeGridScores(t *testing.T) {
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
