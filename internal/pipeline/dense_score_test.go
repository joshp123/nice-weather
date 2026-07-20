package pipeline

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/weather"
)

func TestDenseForecastScoreSpecMatchesDetailedEuropeGeometry(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: -24, East: 45, South: 29, North: 71.5}, Rows: delivery.DenseForecastScoreRows, Cols: delivery.DenseForecastScoreCols}
	geometry, err := delivery.NewPointGeometry(spec)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Rows != 680 || spec.Cols != 1104 || geometry.Rows != 1179 || geometry.Cols != 1104 {
		t.Fatalf("dense spec = %dx%d regular, %dx%d displayed; want 1104x680 regular, 1104x1179 displayed", spec.Cols, spec.Rows, geometry.Cols, geometry.Rows)
	}
}

func TestDenseDisplayScoresBilinearlyInterpolateCanonicalScalars(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2}
	source, err := delivery.NewPointGeometry(sourceSpec)
	if err != nil {
		t.Fatal(err)
	}
	records := []delivery.DailyPointRecord{displaySource(0), displaySource(100), displaySource(100), displaySource(0)}
	target := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 3, Cols: 3}
	geometry, dense, err := DenseDailyDisplayScores(source, records, target, allLand(target))
	if err != nil {
		t.Fatal(err)
	}
	center := dense[int(geometry.Rows/2)*int(geometry.Cols)+int(geometry.Cols/2)]
	if !center.Valid || center.NiceScore != 50 || center.Thermal != 50 || center.CyclingWind != 50 {
		t.Fatalf("bilinear centre = %+v; want every scalar 50", center)
	}
}

func TestDenseDisplayScoresLeaveInlandProviderGapsTransparent(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2}
	source, _ := delivery.NewPointGeometry(sourceSpec)
	records := []delivery.DailyPointRecord{displaySource(17), {}, displaySource(90), displaySource(100)}
	target := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 3, Cols: 3}
	geometry, dense, err := DenseDailyDisplayScores(source, records, target, allLand(target))
	if err != nil {
		t.Fatal(err)
	}
	center := dense[int(geometry.Rows/2)*int(geometry.Cols)+int(geometry.Cols/2)]
	if center.Valid {
		t.Fatalf("inland provider gap = %+v; want transparent output", center)
	}
}

func TestDenseDisplayScoresRespectTargetLandMaskAndCoastalFallback(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2}
	source, _ := delivery.NewPointGeometry(sourceSpec)
	target := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 3, Cols: 3}
	mask := FloatGrid{Spec: target, Values: make([]float32, 9)}
	mask.Values[4] = 1 // canonical land authority says only the centre target cell is land
	geometry, dense, err := DenseDailyDisplayScores(source, []delivery.DailyPointRecord{displaySource(73), {}, {}, {}}, target, mask)
	if err != nil {
		t.Fatal(err)
	}
	valid := 0
	for _, record := range dense {
		if record.Valid {
			valid++
			if record.NiceScore != 73 {
				t.Fatalf("coastal fallback = %+v", record)
			}
		}
	}
	if valid != 1 || len(dense) != int(geometry.Rows)*int(geometry.Cols) {
		t.Fatalf("target mask yielded %d land pixels in %dx%d plane", valid, geometry.Cols, geometry.Rows)
	}
}

func TestDenseRangeRecordsRebuildSummaryFromInterpolatedDailyScores(t *testing.T) {
	sourceSpec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 2}, Rows: 2, Cols: 2}
	source, _ := delivery.NewPointGeometry(sourceSpec)
	records := []delivery.RangePointRecord{rangeSource(t, 0), rangeSource(t, 100), rangeSource(t, 100), rangeSource(t, 0)}
	target := delivery.GridSpec{Bounds: sourceSpec.Bounds, Rows: 3, Cols: 3}
	geometry, dense, err := DenseRangePointRecords(source, records, target, allLand(target))
	if err != nil {
		t.Fatal(err)
	}
	center := dense[int(geometry.Rows/2)*int(geometry.Cols)+int(geometry.Cols/2)]
	if !center.Nice.Valid || center.Nice.Score != 50 || center.Nice.MedianScore != 50 || center.Nice.NiceDays != 0 || center.Nice.AwfulDays != 0 || center.Nice.LongestPoorRun != 0 || center.Nice.Thermal != 50 || center.Cycling.Wind != 50 {
		t.Fatalf("dense window centre = %+v", center)
	}
	for day := range center.NiceDays {
		if center.NiceDays[day] != 50 || center.CyclingDays[day] != 50 {
			t.Fatalf("day %d scores = %d/%d", day, center.NiceDays[day], center.CyclingDays[day])
		}
	}
}

func TestDisplayScorePNGMatchesEveryStoredLensAndProfile(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	geometry, _ := delivery.NewPointGeometry(spec)
	record := delivery.DisplayScoreRecord{Thermal: 24, Dryness: 25, Sun: 39, NiceScore: 40, NiceWind: 54, CyclingScore: 55, CyclingWind: 69, Valid: true}
	palette, err := scorePalette(delivery.CanonicalPalette())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		profile weather.Profile
		lens    weather.Lens
		score   uint8
	}{
		{weather.ProfileNice, weather.LensOverall, 40}, {weather.ProfileCycling, weather.LensOverall, 55},
		{weather.ProfileNice, weather.LensComfort, 24}, {weather.ProfileCycling, weather.LensDry, 25},
		{weather.ProfileNice, weather.LensBright, 39}, {weather.ProfileNice, weather.LensCalm, 54},
		{weather.ProfileCycling, weather.LensCalm, 69},
	}
	for _, test := range tests {
		var encoded bytes.Buffer
		if err := RenderDisplayScoreRecords(&encoded, geometry, []delivery.DisplayScoreRecord{record}, test.profile, test.lens, delivery.CanonicalPalette()); err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(bytes.NewReader(encoded.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if got := color.NRGBAModel.Convert(image.At(0, 0)).(color.NRGBA); got != palette[test.score] {
			t.Fatalf("%s/%s pixel = %v; want %v", test.profile, test.lens, got, palette[test.score])
		}
	}
}

func displaySource(score uint8) delivery.DailyPointRecord {
	return delivery.DailyPointRecord{Weather: delivery.DailyRecord{Valid: true}, Thermal: score, Dryness: score, Sun: score, NiceScore: score, NiceWind: score, CyclingScore: score, CyclingWind: score}
}

func allLand(spec delivery.GridSpec) FloatGrid {
	values := make([]float32, int(spec.Rows)*int(spec.Cols))
	for index := range values {
		values[index] = 1
	}
	return FloatGrid{Spec: spec, Values: values}
}

func rangeSource(t *testing.T, score uint8) delivery.RangePointRecord {
	t.Helper()
	var point delivery.RangePointRecord
	for index := range point.NiceDays {
		point.NiceDays[index], point.CyclingDays[index] = score, score
	}
	var err error
	point.Nice, err = denseRangeSummary(point.NiceDays, score, score, score, score)
	if err != nil {
		t.Fatal(err)
	}
	point.Cycling, err = denseRangeSummary(point.CyclingDays, score, score, score, score)
	if err != nil {
		t.Fatal(err)
	}
	return point
}
