package update

import (
	"math"
	"strings"
	"testing"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/pipeline"
)

func TestCHELSAURLAccountsForSolarNamingException(t *testing.T) {
	if got := chelsaURL("tas", 1); got != chelsaRoot+"/tas/CHELSA_tas_01_1981-2010_V.2.1.tif" {
		t.Fatal(got)
	}
	if got := chelsaURL("rsds", 12); got != chelsaRoot+"/rsds/CHELSA_rsds_1981-2010_12_V.2.1.tif" {
		t.Fatal(got)
	}
}

func TestCHELSAScaleAndOffsetContracts(t *testing.T) {
	if got := scaleCHELSA("tas", 2931.5); got < 19.999 || got > 20.001 {
		t.Fatalf("temperature = %f", got)
	}
	if got := scaleCHELSA("sfcWind", 5000); got != 5 {
		t.Fatalf("wind = %f", got)
	}
	if got := scaleCHELSA("clt", 7500); got != 75 {
		t.Fatalf("cloud = %f", got)
	}
}

func TestClimateMetricsUseExplicitDaytimeProxyAndDailySolarEnergy(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	stored := map[string]float32{
		"tas": 2931.5, "tasmin": 2831.5, "tasmax": 3031.5,
		"pr": 310, "sfcWind": 5000, "clt": 4000, "rsds": 15000, "hurs": 6000,
	}
	grids := make(map[string]pipeline.FloatGrid, len(stored))
	for variable, value := range stored {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{value}}
	}
	metrics, err := climateMetrics(1, grids)
	if err != nil {
		t.Fatal(err)
	}
	metric := metrics[0]
	if !metric.Valid || math.Abs(metric.ComfortableTempC-25) > .001 || metric.WetTimeFraction.Available || metric.NativeResolutionKM != 11 {
		t.Fatalf("unexpected generic climate metric: %+v", metric)
	}
	if !metric.SolarEnergyMJm2.Available || math.Abs(metric.SolarEnergyMJm2.Value-15) > .001 {
		t.Fatalf("daily solar energy was reinterpreted: %+v", metric.SolarEnergyMJm2)
	}
}

func TestClimateMetricsRejectInvalidFiniteLandCell(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 50, North: 51}, Rows: 1, Cols: 2}
	stored := map[string][]float32{
		"tas": {float32(math.NaN()), 2931.5}, "tasmin": {float32(math.NaN()), 3031.5}, "tasmax": {float32(math.NaN()), 2831.5},
		"pr": {float32(math.NaN()), 310}, "sfcWind": {float32(math.NaN()), 5000}, "clt": {float32(math.NaN()), 4000},
		"rsds": {float32(math.NaN()), 15000}, "hurs": {float32(math.NaN()), 6000},
	}
	grids := make(map[string]pipeline.FloatGrid, len(stored))
	for variable, values := range stored {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: values}
	}
	_, err := climateMetrics(1, grids)
	if err == nil {
		t.Fatal("expected finite invalid land data to fail")
	}
	for _, evidence := range []string{"month 01", "cell 1", "row 0 col 1", "50.50000,1.50000", "minimum temperature exceeds maximum temperature", "tasmin=3031.5", "tasmax=2831.5"} {
		if !strings.Contains(err.Error(), evidence) {
			t.Fatalf("error %q does not contain %q", err, evidence)
		}
	}
}

func TestClimateMetricsLeavesNaNOceanInvalid(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	land := map[string]float32{
		"tas": 2931.5, "tasmin": 2831.5, "tasmax": 3031.5,
		"pr": 310, "sfcWind": 5000, "clt": 4000, "rsds": 15000, "hurs": 6000,
	}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{float32(math.NaN()), land[variable]}}
	}
	metrics, err := climateMetrics(1, grids)
	if err != nil {
		t.Fatal(err)
	}
	if metrics[0].Valid || !metrics[1].Valid {
		t.Fatalf("ocean/land validity is wrong: %+v", metrics)
	}
}

func TestNaturalEarthMaskOverridesFiniteClimateValuesOutsideLand(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	stored := map[string]float32{
		"tas": 2931.5, "tasmin": 2831.5, "tasmax": 3031.5,
		"pr": 310, "sfcWind": 5000, "clt": 4000, "rsds": 15000, "hurs": 6000,
	}
	grids := make(map[string]pipeline.FloatGrid, len(stored))
	for variable, value := range stored {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{value, value}}
	}
	centres := pipeline.FloatGrid{Spec: spec, Values: []float32{0, 1}}
	allTouched := pipeline.FloatGrid{Spec: spec, Values: []float32{0, 1}}
	report, err := canonicalizeClimateSupport(1, grids, centres, allTouched)
	if err != nil || report.CoreCells != 1 || report.CoreMissing != 0 || report.EdgeCells != 0 {
		t.Fatalf("canonical support: report=%+v err=%v", report, err)
	}
	metrics, err := climateMetrics(1, grids)
	if err != nil {
		t.Fatal(err)
	}
	if metrics[0].Valid || !metrics[1].Valid {
		t.Fatalf("explicit ocean/land validity is wrong: %+v", metrics)
	}
}

func TestCanonicalClimateSupportMasksPartialOceanCellWithoutInventingZero(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 2, South: 0, North: 1}, Rows: 1, Cols: 2}
	stored := map[string]float32{
		"tas": 2931.5, "tasmin": 2831.5, "tasmax": 3031.5,
		"pr": 310, "sfcWind": 5000, "clt": 4000, "rsds": 15000, "hurs": 6000,
	}
	grids := make(map[string]pipeline.FloatGrid, len(stored))
	for variable, value := range stored {
		first := value
		if variable == "clt" {
			first = float32(math.NaN())
		}
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{first, value}}
	}
	centres := pipeline.FloatGrid{Spec: spec, Values: []float32{0, 1}}
	allTouched := pipeline.FloatGrid{Spec: spec, Values: []float32{1, 1}}
	report, err := canonicalizeClimateSupport(1, grids, centres, allTouched)
	if err != nil || report.EdgeMissing != 1 || report.EdgeCells != 1 || report.CoreCells != 1 || len(report.EdgeSamples) != 1 {
		t.Fatalf("canonical support: report=%+v err=%v", report, err)
	}
	metrics, err := climateMetrics(1, grids)
	if err != nil {
		t.Fatal(err)
	}
	if metrics[0].Valid || !metrics[1].Valid {
		t.Fatalf("partial coast was not made transparent: %+v", metrics)
	}
}

func TestCanonicalClimateSupportRejectsImplausiblyBroadDisagreement(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 10, South: 0, North: 10}, Rows: 10, Cols: 10}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		values := make([]float32, 100)
		for index := range values {
			values[index] = 1
		}
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: values}
	}
	cloud := grids["clt"]
	for index := 0; index < 3; index++ {
		cloud.Values[index] = float32(math.NaN())
	}
	grids["clt"] = cloud
	centres := pipeline.FloatGrid{Spec: spec, Values: make([]float32, 100)}
	for index := range centres.Values {
		centres.Values[index] = 1
	}
	_, err := canonicalizeClimateSupport(2, grids, centres, centres)
	if err == nil || !strings.Contains(err.Error(), "core 3/100 missing") || !strings.Contains(err.Error(), "maximum missing-support cells is 2") {
		t.Fatalf("unexpected support-bound error: %v", err)
	}
}

func TestCanonicalClimateSupportUsesExactTwoPercentGuard(t *testing.T) {
	makeCase := func(cells int) (map[string]pipeline.FloatGrid, pipeline.FloatGrid) {
		spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: float64(cells), South: 0, North: 1}, Rows: 1, Cols: uint32(cells)}
		grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
		for _, variable := range climateVariables {
			values := make([]float32, cells)
			for index := range values {
				values[index] = 1
			}
			grids[variable] = pipeline.FloatGrid{Spec: spec, Values: values}
		}
		cloud := grids["clt"]
		cloud.Values[0] = float32(math.NaN())
		grids["clt"] = cloud
		mask := pipeline.FloatGrid{Spec: spec, Values: make([]float32, cells)}
		for index := range mask.Values {
			mask.Values[index] = 1
		}
		return grids, mask
	}
	grids, mask := makeCase(50)
	if report, err := canonicalizeClimateSupport(1, grids, mask, mask); err != nil || report.CoreMissing != 1 {
		t.Fatalf("exactly two percent should pass: report=%+v err=%v", report, err)
	}
	grids, mask = makeCase(49)
	if _, err := canonicalizeClimateSupport(1, grids, mask, mask); err == nil || !strings.Contains(err.Error(), "maximum missing-support cells is 0") {
		t.Fatalf("more than two percent should fail: %v", err)
	}
}

func TestCanonicalClimateSupportBoundUsesCandidateSupportNotOcean(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 100, South: 0, North: 10}, Rows: 10, Cols: 100}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		values := make([]float32, 1000)
		for index := range values {
			values[index] = float32(math.NaN())
		}
		for index := 0; index < 100; index++ {
			values[index] = 1
		}
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: values}
	}
	radiation := grids["rsds"]
	for index := 100; index < len(radiation.Values); index++ {
		radiation.Values[index] = 1 // Radiation legitimately describes ocean.
	}
	grids["rsds"] = radiation
	cloud := grids["clt"]
	for index := 0; index < 3; index++ {
		cloud.Values[index] = float32(math.NaN())
	}
	grids["clt"] = cloud
	centres := pipeline.FloatGrid{Spec: spec, Values: make([]float32, 1000)}
	for index := 0; index < 100; index++ {
		centres.Values[index] = 1
	}
	_, err := canonicalizeClimateSupport(2, grids, centres, centres)
	if err == nil || !strings.Contains(err.Error(), "core 3/100 missing") || !strings.Contains(err.Error(), "maximum missing-support cells is 2") {
		t.Fatalf("ocean diluted support-bound error: %v", err)
	}
}

func TestCanonicalClimateSupportDoesNotTreatTouchedOnlyGapsAsProviderCorruption(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 100, South: 0, North: 1}, Rows: 1, Cols: 100}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		values := make([]float32, 100)
		for index := range values {
			values[index] = 1
		}
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: values}
	}
	cloud := grids["clt"]
	for index := 0; index < 99; index++ {
		cloud.Values[index] = float32(math.NaN())
	}
	grids["clt"] = cloud
	centres := pipeline.FloatGrid{Spec: spec, Values: make([]float32, 100)}
	centres.Values[99] = 1
	allTouched := pipeline.FloatGrid{Spec: spec, Values: make([]float32, 100)}
	for index := range allTouched.Values {
		allTouched.Values[index] = 1
	}
	report, err := canonicalizeClimateSupport(3, grids, centres, allTouched)
	if err != nil || report.CoreCells != 1 || report.CoreMissing != 0 || report.EdgeCells != 99 || report.EdgeMissing != 99 {
		t.Fatalf("touched-only gaps affected core guard: report=%+v err=%v", report, err)
	}
	for index := 0; index < 99; index++ {
		for _, variable := range climateVariables {
			if !math.IsNaN(float64(grids[variable].Values[index])) {
				t.Fatalf("edge cell %d %s was not made transparent", index, variable)
			}
		}
	}
}

func TestCanonicalClimateSupportKeepsCompleteTouchedOnlyIsland(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{1}}
	}
	centres := pipeline.FloatGrid{Spec: spec, Values: []float32{0}}
	allTouched := pipeline.FloatGrid{Spec: spec, Values: []float32{1}}
	report, err := canonicalizeClimateSupport(4, grids, centres, allTouched)
	if err != nil || report.EdgeCells != 1 || report.EdgeMissing != 0 || math.IsNaN(float64(grids["tas"].Values[0])) {
		t.Fatalf("complete touched-only cell was lost: report=%+v err=%v", report, err)
	}
}

func TestCanonicalClimateSupportRejectsImpossibleMaskTopology(t *testing.T) {
	spec := delivery.GridSpec{Bounds: delivery.GridBounds{West: 0, East: 1, South: 0, North: 1}, Rows: 1, Cols: 1}
	grids := make(map[string]pipeline.FloatGrid, len(climateVariables))
	for _, variable := range climateVariables {
		grids[variable] = pipeline.FloatGrid{Spec: spec, Values: []float32{1}}
	}
	centres := pipeline.FloatGrid{Spec: spec, Values: []float32{1}}
	allTouched := pipeline.FloatGrid{Spec: spec, Values: []float32{0}}
	_, err := canonicalizeClimateSupport(5, grids, centres, allTouched)
	if err == nil || !strings.Contains(err.Error(), "mask topology is invalid") {
		t.Fatalf("impossible topology was accepted: %v", err)
	}
}

func TestDecodedCHELSAGridRejectsOutOfRangeFiniteValue(t *testing.T) {
	grid := pipeline.FloatGrid{
		Spec:   delivery.GridSpec{Bounds: delivery.GridBounds{West: 4, East: 5, South: 51, North: 52}, Rows: 1, Cols: 1},
		Values: []float32{12000},
	}
	err := validateDecodedCHELSAGrid(7, "clt", grid)
	if err == nil || !strings.Contains(err.Error(), "CHELSA clt month 07 cell 0") || !strings.Contains(err.Error(), "120") {
		t.Fatalf("unexpected range error: %v", err)
	}
}

func TestCHELSAVariableSpecIsSingleSourceForURLTransformAndMetadata(t *testing.T) {
	spec, err := chelsaVariable("rsds")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.SolarFilenameOrder || spec.MetadataUnit != "" || spec.MetadataUnitPolicy != chelsaUnitUnreliable || spec.Minimum != 0 || spec.Maximum != 60 {
		t.Fatalf("unexpected rsds contract: %+v", spec)
	}
	if got := scaleCHELSA("rsds", 15000); got != 15 {
		t.Fatalf("rsds transform = %f", got)
	}
}
